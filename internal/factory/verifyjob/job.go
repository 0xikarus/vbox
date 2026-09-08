// Package verifyjob runs one controller-staged verification attempt and delivers its
// durable evidence before the controller's process task can auto-hibernate.
package verifyjob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	"unicode/utf8"

	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"github.com/0xikarus/vmbox-service/internal/factory/verification"
	"golang.org/x/sys/unix"
)

type Job struct {
	Version       int                  `json:"version"`
	AttemptID     string               `json:"attemptId"`
	Request       verification.Request `json:"request"`
	ReceiptPath   string               `json:"receiptPath"`
	DeliveryURL   string               `json:"deliveryUrl"`
	DeliveryToken string               `json:"deliveryToken"`
}

// Envelope distinguishes the verifier child's actual exit from this delivery command's
// exit. An exit of zero is not a claim that the proposed feature was verified.
type Envelope = resultinbox.Result

type Report struct {
	Version      int                  `json:"version"`
	RequestHash  string               `json:"requestHash"`
	Verification *verification.Report `json:"verification,omitempty"`
	Accepted     bool                 `json:"accepted"`
	ErrorCode    string               `json:"errorCode,omitempty"`
}

type receipt struct {
	RequestHash string   `json:"requestHash"`
	Result      Envelope `json:"result"`
}

type runVerifier func(context.Context, verification.Request) (processResult, error)

func Execute(ctx context.Context, input io.Reader) error {
	return execute(ctx, input, runChild, nil)
}

func execute(ctx context.Context, input io.Reader, run runVerifier, client *http.Client) error {
	b, err := io.ReadAll(io.LimitReader(input, 200001))
	if err != nil || len(b) > 200000 {
		return fmt.Errorf("job exceeds limit")
	}
	var job Job
	if err = decode(b, &job); err != nil {
		return err
	}
	if job.Version != 1 || !identity(job.AttemptID) {
		return fmt.Errorf("invalid attempt identity")
	}
	u, err := url.Parse(job.DeliveryURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" {
		return fmt.Errorf("delivery requires HTTPS")
	}
	token, tokenErr := base64.RawURLEncoding.DecodeString(job.DeliveryToken)
	if tokenErr != nil || len(token) != 32 || base64.RawURLEncoding.EncodeToString(token) != job.DeliveryToken {
		return fmt.Errorf("invalid delivery capability")
	}
	if reserved(filepath.Base(job.ReceiptPath)) || filepath.Dir(job.ReceiptPath) != job.Request.OutputDir {
		return fmt.Errorf("receipt must be in request output directory")
	}
	dir, name, err := openReceiptDir(job.ReceiptPath)
	if err != nil {
		return err
	}
	defer dir.Close()
	// Serialize local invocations. A second launch must not race the first child.
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
		Attempt     string
		Request     verification.Request
		DeliveryURL string
	}{job.AttemptID, job.Request, job.DeliveryURL})
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
		if err := validateRequest(job.Request); err != nil {
			return err
		}
		// A durable start marker prevents a crash between process completion and
		// receipt persistence from silently re-running checks.
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
			return fmt.Errorf("child has no terminal process evidence")
		}
		saved = receipt{RequestHash: fingerprint, Result: Envelope{Version: 1, AttemptID: job.AttemptID, ExitCode: result.ExitCode, Signal: result.Signal, Truncated: result.Truncated}}

		report := Report{Version: 1, RequestHash: fingerprint, Verification: result.Report}
		if runErr != nil {
			report.ErrorCode = "verification_incomplete"
		}
		report.Accepted = runErr == nil && result.ExitCode != nil && *result.ExitCode == 0 && result.Signal == 0 && result.Report != nil && result.Report.AllPassed
		saved.Result.Document, err = json.Marshal(report)
		if err != nil {
			return fmt.Errorf("invalid verification report")
		}
		if err = writeReceipt(dir, name, saved); err != nil {
			return err
		}
	}
	if dir.Sync() != nil {
		return fmt.Errorf("receipt sync failed")
	}
	deliveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 65*time.Second)
	defer cancel()
	return deliver(deliveryCtx, job, saved.Result, client)
}

func identity(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}

func reserved(name string) bool {
	return strings.HasPrefix(name, ".job.")
}

func decode(b []byte, v any) error {
	if !utf8.Valid(b) || uniqueJSON(b) != nil {
		return fmt.Errorf("invalid job or receipt")
	}
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

func openReceiptDir(path string) (*os.File, string, error) { return openDirectory(path, true) }

func openDirectory(path string, private bool) (*os.File, string, error) {
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
	if e != nil || (private && st.Mode().Perm()&0077 != 0) {
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

	var report Report
	envelope, marshalErr := json.Marshal(saved.Result)
	if marshalErr != nil {
		return saved, false, fmt.Errorf("invalid receipt envelope")
	}
	if _, err := resultinbox.Decode(envelope); err != nil {
		return saved, false, fmt.Errorf("invalid receipt envelope")
	}
	if decode(saved.Result.Document, &report) != nil || report.Version != 1 || report.RequestHash != saved.RequestHash {
		return saved, false, fmt.Errorf("invalid receipt report")
	}
	if report.ErrorCode != "" && report.ErrorCode != "verification_incomplete" {
		return saved, false, fmt.Errorf("invalid report error code")
	}
	if report.Accepted && (report.ErrorCode != "" || saved.Result.ExitCode == nil || *saved.Result.ExitCode != 0 || report.Verification == nil || !report.Verification.AllPassed) {
		return saved, false, fmt.Errorf("inconsistent acceptance")
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
