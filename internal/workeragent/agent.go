// Package workeragent connects an existing worker to its controller without
// depending on the provider API or restarting any workspace process.
package workeragent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type Config struct {
	ControllerURL    string `json:"controllerUrl"`
	AccountID        string `json:"accountId"`
	SlotID           string `json:"slotId"`
	WorkerID         string `json:"workerId"`
	EnrollmentToken  string `json:"enrollmentToken,omitempty"`
	Credential       string `json:"credential"`
	JournalDirectory string `json:"journalDirectory"`
	BindingFile      string `json:"bindingFile"`
}

type Agent struct {
	bindingMu   sync.Mutex
	Config      Config
	ConfigFile  string
	HTTP        *http.Client
	Incarnation string
	// Report receives stable diagnostics only, never credentials or server bodies.
	Report func(string)
}

func secret() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
func Load(path string) (Config, error) {
	var config Config
	info, err := os.Lstat(path)
	if err != nil {
		return config, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return config, errors.New("worker config must be a private regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}
	if len(data) > 16*1024 {
		return config, errors.New("worker config too large")
	}
	err = json.Unmarshal(data, &config)
	return config, err
}
func (a *Agent) save() error {
	dir := filepath.Dir(a.ConfigFile)
	file, err := os.CreateTemp(dir, ".worker-config-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	defer file.Close()
	if err = json.NewEncoder(file).Encode(a.Config); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, a.ConfigFile); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}
func (a *Agent) endpoint(path string) (string, error) {
	u, err := url.Parse(a.Config.ControllerURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("worker controller must be an HTTPS URL without credentials")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	u.ForceQuery = false
	return u.String(), nil
}

func (a *Agent) Run(ctx context.Context) error {
	if a.Config.AccountID == "" || a.Config.SlotID == "" || a.Config.WorkerID == "" || a.Config.JournalDirectory == "" || a.Config.BindingFile == "" || a.ConfigFile == "" {
		return errors.New("incomplete worker configuration")
	}
	if _, err := a.endpoint("/v1/workers/connect"); err != nil {
		return err
	}
	var err error
	if a.Incarnation == "" {
		a.Incarnation, err = secret()
		if err != nil {
			return err
		}
	}
	if a.Config.Credential == "" {
		a.Config.Credential, err = secret()
		if err != nil {
			return err
		}
		if err = a.save(); err != nil {
			return err
		}
	}
	delay := time.Second
	for {
		connected, err := a.connect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !connected && a.Config.EnrollmentToken != "" {
			if a.enroll(ctx) == nil {
				delay = time.Second
				continue
			}
		}
		if err != nil && a.Report != nil {
			a.Report("worker connection unavailable; reconnecting")
		}
		if connected {
			delay = time.Second
		}
		// Jitter without sharing a predictable random source with credentials.
		var random [1]byte
		_, _ = rand.Read(random[:])
		wait := delay + time.Duration(random[0])*delay/256
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}
func (a *Agent) enroll(ctx context.Context) error {
	endpoint, err := a.endpoint("/v1/workers/enroll")
	if err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"enrollmentToken": a.Config.EnrollmentToken, "credential": a.Config.Credential})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := a.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	// Never follow redirects carrying a credential-bearing request body.
	safe := *client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := safe.Do(req)
	if err != nil {
		return errors.New("worker enrollment unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("worker enrollment rejected")
	}
	var identity struct {
		ID        string `json:"id"`
		AccountID string `json:"accountId"`
		SlotID    string `json:"slotId"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&identity); err != nil {
		return errors.New("invalid enrollment reply")
	}
	if identity.ID != a.Config.WorkerID || identity.AccountID != a.Config.AccountID || identity.SlotID != a.Config.SlotID {
		return errors.New("worker enrollment identity mismatch")
	}
	a.Config.EnrollmentToken = ""
	return a.save()
}
func (a *Agent) connect(ctx context.Context) (bool, error) {
	endpoint, err := a.endpoint("/v1/workers/connect")
	if err != nil {
		return false, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.Config.Credential)
	client := a.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	safe := *client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	handshake, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(handshake, endpoint, &websocket.DialOptions{HTTPHeader: header, HTTPClient: &safe})
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return false, errors.New("worker connection unavailable")
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8192)
	binding, err := a.localBinding(handshake)
	if err != nil || a.validateBinding(handshake, binding) != nil {
		return false, errors.New("worker local assignment unavailable")
	}
	if err = wsjson.Write(handshake, conn, workerprotocol.Hello{Binding: &binding, Version: workerprotocol.Version, Incarnation: a.Incarnation, Capabilities: []string{"exec-v1", "stream-v1", "journal-v1", "binding-v1", "observations-v1"}}); err != nil {
		return false, err
	}
	var welcome workerprotocol.Welcome
	if err = wsjson.Read(handshake, conn, &welcome); err != nil {
		return false, err
	}
	if welcome.Version != workerprotocol.Version || welcome.WorkerID != a.Config.WorkerID || welcome.AccountID != a.Config.AccountID || welcome.SlotID != a.Config.SlotID || welcome.Epoch <= 0 {
		return false, errors.New("worker handshake identity mismatch")
	}
	if a.Config.EnrollmentToken != "" {
		a.Config.EnrollmentToken = ""
		if err = a.save(); err != nil {
			return false, err
		}
	}
	peerCtx, stopPeer := context.WithCancel(ctx)
	defer stopPeer()
	peer := workerprotocol.New(peerCtx, conn, false)
	defer peer.Close()
	if slices.Contains(welcome.Capabilities, "observations-v1") {
		go a.observations(peerCtx, peer)
	}
	if a.Report != nil {
		a.Report("worker connected")
	}
	for {
		stream, err := peer.Accept(ctx)
		if err != nil {
			return true, err
		}
		go func() {
			if stream.Request.Rebind != nil {
				a.rebindStream(ctx, stream)
				return
			}
			_ = workerprotocol.Execute(ctx, stream, workerprotocol.Journal{Directory: a.Config.JournalDirectory}, a.validateBinding)
		}()
	}
}
func (a *Agent) validateBinding(ctx context.Context, binding workerprotocol.Binding) error {
	if binding.AccountID != a.Config.AccountID || binding.SlotID != a.Config.SlotID || binding.Incarnation != a.Incarnation || binding.BoxID == "" || binding.Assignment == "" {
		return errors.New("worker binding mismatch")
	}
	local, err := a.localBinding(ctx)
	if err != nil {
		return err
	}
	if local != binding {
		return errors.New("worker assignment changed")
	}
	return ctx.Err()
}

func (a *Agent) localBinding(ctx context.Context) (workerprotocol.Binding, error) {
	var local workerprotocol.Binding
	info, err := os.Lstat(a.Config.BindingFile)
	if err != nil {
		return local, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return local, errors.New("worker binding must be private")
	}
	data, err := os.ReadFile(a.Config.BindingFile)
	if err != nil {
		return local, err
	}
	if len(data) > 4096 {
		return local, errors.New("invalid worker binding")
	}
	if err = json.Unmarshal(data, &local); err != nil {
		return local, err
	}
	// Incarnation is process-local, not restored from a previous agent process.
	local.Incarnation = a.Incarnation
	return local, ctx.Err()
}
