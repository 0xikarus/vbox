// The trusted wrapper delivers durable evidence before process auto-hibernation.
package taskflowruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"golang.org/x/sys/unix"
)

type Job struct {
	Version       int     `json:"version"`
	AttemptID     string  `json:"attemptId"`
	Request       Request `json:"request"`
	ReceiptPath   string  `json:"receiptPath"`
	DeliveryURL   string  `json:"deliveryUrl"`
	DeliveryToken string  `json:"deliveryToken"`
}

// Envelope distinguishes the agent's actual exit from this delivery command's
// exit. An exit of zero is not a claim that the proposed feature was verified.
type Envelope = resultinbox.Result

type receipt struct {
	RequestHash string   `json:"requestHash"`
	Result      Envelope `json:"result"`
}

type agentFunc func(context.Context, Request) (AgentResult, error)

func Execute(ctx context.Context, input io.Reader) error {
	// Protect private stdin and in-memory capabilities from same-uid /proc and
	// ptrace access. Agent children additionally inherit a Landlock allowlist.
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("private process isolation unavailable")
	}
	return execute(ctx, input, runIsolated, nil)
}

func execute(ctx context.Context, input io.Reader, run agentFunc, client *http.Client) error {
	b, err := io.ReadAll(io.LimitReader(input, 200001))
	if err != nil || len(b) > 200000 {
		return fmt.Errorf("job exceeds limit")
	}
	var job Job
	if err = decode(b, &job); err != nil {
		return err
	}
	if job.Version != 1 || !validIdentity(job.AttemptID) || validateRequest(job.Request) != nil {
		return fmt.Errorf("invalid attempt identity")
	}
	u, err := url.Parse(job.DeliveryURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" {
		return fmt.Errorf("delivery requires HTTPS")
	}
	if len(job.DeliveryToken) != 43 || strings.ContainsAny(job.DeliveryToken, "\r\n") {
		return fmt.Errorf("invalid delivery capability")
	}
	// Receipt and agent document have distinct create-only paths in one private
	// attempt directory. No remote pathname is derived from the agent's output.
	if job.ReceiptPath != taskRoot+"/attempts/"+job.AttemptID+"/receipt.json" || job.Request.Workspace != taskRoot+"/attempts/"+job.AttemptID+"/workspace" || job.Request.ResultPath != taskRoot+"/attempts/"+job.AttemptID+"/result.json" {
		return fmt.Errorf("result paths must be distinct siblings")
	}
	dir, name, err := openReceiptDir(job.ReceiptPath)
	if err != nil {
		return err
	}
	defer dir.Close()
	// Serialize local invocations. A second launch must not race the first agent.
	lock, err := unix.Openat(int(dir.Fd()), ".job.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("attempt lock unavailable")
	}
	defer unix.Close(lock)
	if unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return fmt.Errorf("attempt already running")
	}
	defer unix.Flock(lock, unix.LOCK_UN)
	requestData, _ := json.Marshal(struct {
		Attempt string
		Request Request
	}{job.AttemptID, job.Request})
	h := sha256.Sum256(requestData)
	fingerprint := hex.EncodeToString(h[:])
	saved, found, err := readReceipt(dir, name)
	if err != nil {
		return err
	}
	if found {
		if saved.RequestHash != fingerprint || saved.Result.AttemptID != job.AttemptID {
			return fmt.Errorf("attempt request changed")
		}
	} else {
		// A durable start marker prevents a crash between process completion and
		// receipt persistence from silently re-running paid agent work.
		fd, e := unix.Openat(int(dir.Fd()), ".job.started", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if e != nil {
			return fmt.Errorf("attempt was started; outcome requires reconciliation")
		}
		f := os.NewFile(uintptr(fd), ".job.started")
		_, e = f.WriteString(fingerprint)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil || dir.Sync() != nil {
			return fmt.Errorf("cannot persist start marker")
		}
		result, runErr := run(ctx, job.Request)
		if result.ExitCode == nil && result.Signal == 0 {
			return fmt.Errorf("agent has no terminal process evidence")
		}
		saved = receipt{RequestHash: fingerprint, Result: Envelope{Version: 1, AttemptID: job.AttemptID, ExitCode: result.ExitCode, Signal: result.Signal, Truncated: result.Truncated}}
		// Invalid output and failed persistence never become accepted documents,
		// even if the subprocess itself returned zero.
		report := Report{AgentEvidence: result.AgentEvidence, Failure: result.Failure}
		if runErr == nil {
			validated, validationErr := ValidateResult(result.Document, job.Request.Stage, job.Request.Revision)
			if validationErr != nil {
				report.Failure = "agent_result_invalid"
			} else {
				report.Result = validated
			}
		} else if report.Failure == "" {
			report.Failure = "agent_result_invalid"
		}
		saved.Result.Document, _ = json.Marshal(report)
		if err = writeReceipt(dir, name, saved); err != nil {
			return err
		}
	}
	// Stay alive until the inbox has acknowledged durable storage. Controller
	// auto-hibernation must not race a delivery outage. Cancellation retains the
	// receipt; a later wrapper invocation redelivers without executing the agent.
	for {
		if err := deliver(ctx, job, saved.Result, client); err == nil {
			return nil
		}
		timer := time.NewTimer(15 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("delivery interrupted; receipt retained")
		case <-timer.C:
		}
	}
}

func validIdentity(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}

func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return fmt.Errorf("invalid job or receipt")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("trailing job or receipt data")
	}
	return nil
}

func deliver(ctx context.Context, job Job, result Envelope, client *http.Client) error {
	b, err := json.Marshal(result)
	if err != nil || len(b) > 400000 {
		return fmt.Errorf("result envelope exceeds limit")
	}
	if _, err = resultinbox.Decode(b); err != nil {
		return fmt.Errorf("invalid result envelope")
	}
	c := http.Client{Timeout: 20 * time.Second}
	if client != nil {
		c = *client
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Short bounded retries cover a lost acknowledgement. Durable receipt allows
	// later delivery retries without another agent invocation.
	for attempt := 0; attempt < 3; attempt++ {
		r, e := http.NewRequestWithContext(ctx, "POST", job.DeliveryURL, bytes.NewReader(b))
		if e != nil {
			return fmt.Errorf("invalid delivery request")
		}
		r.Header.Set("Authorization", "Bearer "+job.DeliveryToken)
		r.Header.Set("Content-Type", "application/json")
		response, e := c.Do(r)
		if e == nil {
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
			if response.StatusCode < 500 && response.StatusCode != 429 {
				return fmt.Errorf("result delivery refused")
			}
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("result delivery unavailable; receipt retained")
}

func openReceiptDir(path string) (*os.File, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, "", fmt.Errorf("invalid receipt path")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("receipt root unavailable")
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Dir(path), "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, "", fmt.Errorf("unsafe receipt directory")
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), filepath.Dir(path))
	st, e := f.Stat()
	if e != nil || st.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, "", fmt.Errorf("receipt directory must be private")
	}
	return f, filepath.Base(path), nil
}

func readReceipt(dir *os.File, name string) (receipt, bool, error) {
	var saved receipt
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return saved, false, nil
	}
	if err != nil {
		return saved, false, fmt.Errorf("unsafe receipt")
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return saved, false, fmt.Errorf("unsafe receipt")
	}
	b, err := io.ReadAll(io.LimitReader(f, 400001))
	if err != nil || len(b) > 400000 {
		return saved, false, fmt.Errorf("receipt exceeds limit")
	}
	if err = decode(b, &saved); err != nil {
		return saved, false, err
	}
	if saved.Result.Version != 1 || (saved.Result.ExitCode == nil && saved.Result.Signal == 0) {
		return saved, false, fmt.Errorf("invalid receipt evidence")
	}
	return saved, true, nil
}

func writeReceipt(dir *os.File, name string, saved receipt) error {
	b, err := json.Marshal(saved)
	if err != nil || len(b) > 400000 {
		return fmt.Errorf("invalid receipt size")
	}
	// The attempt lock permits a fixed private temporary name; O_EXCL refuses
	// leftovers instead of overwriting anything after an ambiguous crash.
	tmp := ".job.receipt.tmp"
	fd, err := unix.Openat(int(dir.Fd()), tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("receipt creation failed")
	}
	defer unix.Unlinkat(int(dir.Fd()), tmp, 0)
	f := os.NewFile(uintptr(fd), tmp)
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf("receipt persistence failed")
	}
	if unix.Renameat2(int(dir.Fd()), tmp, int(dir.Fd()), name, unix.RENAME_NOREPLACE) != nil || dir.Sync() != nil {
		return fmt.Errorf("receipt commit failed")
	}
	return nil
}
