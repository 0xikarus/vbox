package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// ControllerClient is bound to one account by operator configuration and verified
// against whoami. It never consumes a caller-supplied provider or controller token.
type ControllerClient struct {
	URL, Token, AccountID string
	HTTP                  *http.Client
}

func (c *ControllerClient) request(ctx context.Context, method, path, key string, body, out any) error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("invalid controller endpoint")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return fmt.Errorf("controller transport must use HTTPS or loopback")
	}
	if c.Token == "" || c.AccountID == "" {
		return fmt.Errorf("controller account binding missing")
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	client := http.Client{Timeout: 40 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(r)
	if err != nil {
		return fmt.Errorf("controller request unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("controller HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if err != nil || len(data) > 2<<20 {
		return fmt.Errorf("controller response unavailable or oversized")
	}
	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("invalid controller response")
	}
	return nil
}

func (c *ControllerClient) Authorize(ctx context.Context, account string) error {
	if account == "" || account != c.AccountID {
		return fmt.Errorf("controller binding unavailable for this account")
	}
	var who struct {
		AccountID string `json:"accountId"`
		Role      string `json:"role"`
	}
	if err := c.request(ctx, "GET", "/v1/whoami", "", nil, &who); err != nil {
		return err
	}
	if who.AccountID != account || who.Role != "owner" {
		return fmt.Errorf("controller binding identity or authority changed")
	}
	return nil
}

func (c *ControllerClient) Profiles(ctx context.Context, account string) ([]Profile, error) {
	if err := c.Authorize(ctx, account); err != nil {
		return nil, err
	}
	var profiles []Profile
	if err := c.request(ctx, "GET", "/v1/login-profiles", "", nil, &profiles); err != nil {
		return nil, err
	}
	out := []Profile{}
	for _, p := range profiles {
		if p.Application == "codex" || p.Application == "claude" {
			out = append(out, p)
		}
	}
	return out, nil
}

// EnsurePlanningBox progresses existing controller lifecycle, without touching
// provider APIs or interrupting another logical box. Poll between calls rather
// than holding a factory lease through a multi-minute provisioning operation.
func (c *ControllerClient) EnsurePlanningBox(ctx context.Context, account string, w Work) (v1.LogicalBox, error) {
	if err := c.Authorize(ctx, account); err != nil {
		return v1.LogicalBox{}, err
	}
	if len(w.ID) != 32 || len(w.Attempts) == 0 {
		return v1.LogicalBox{}, fmt.Errorf("invalid work identity")
	}
	for _, ch := range w.ID {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return v1.LogicalBox{}, fmt.Errorf("invalid work identity")
		}
	}
	return c.ensureFactoryBox(ctx, w, "factory-plan-"+w.ID, "factory-create:"+w.ID, "factory-resume:"+w.Attempts[len(w.Attempts)-1].ID)
}

// EnsureBuilderBox uses a separate logical box for each persisted build attempt.
// Recovery keeps the same identity; it never reassigns a planner or sibling box.
func (c *ControllerClient) EnsureBuilderBox(ctx context.Context, account string, w Work, attemptID, boxID string) (v1.LogicalBox, error) {
	if err := c.Authorize(ctx, account); err != nil {
		return v1.LogicalBox{}, err
	}
	if !factoryIdentity(w.ID) || !factoryIdentity(attemptID) || (w.Agent != "codex" && w.Agent != "claude") || w.Profile == "" {
		return v1.LogicalBox{}, fmt.Errorf("invalid builder identity or profile")
	}
	w.BoxID = boxID
	return c.ensureFactoryBox(ctx, w, "factory-build-"+attemptID, "factory-build-box:"+attemptID, "factory-build-resume:"+attemptID)
}

func factoryIdentity(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, ch := range id {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return false
		}
	}
	return true
}

func (c *ControllerClient) ensureFactoryBox(ctx context.Context, w Work, name, createKey, resumeKey string) (v1.LogicalBox, error) {
	var boxes []v1.LogicalBox
	if err := c.request(ctx, "GET", "/v1/logical-boxes", "", nil, &boxes); err != nil {
		return v1.LogicalBox{}, err
	}
	for _, b := range boxes {
		if (w.BoxID != "" && b.ID == w.BoxID) || (w.BoxID == "" && b.Name == name) {
			if b.Name != name {
				return v1.LogicalBox{}, fmt.Errorf("factory box identity changed")
			}
			if b.State == v1.LogicalBoxHibernated || b.State == v1.LogicalBoxDetached {
				var allocation v1.Allocation
				err := c.request(ctx, "POST", "/v1/logical-boxes/"+url.PathEscape(b.ID)+"/allocate", resumeKey, map[string]string{"leaseOwner": "factory:" + w.ID}, &allocation)
				return b, err
			}
			return b, nil
		}
	}
	if w.BoxID != "" {
		return v1.LogicalBox{}, fmt.Errorf("previous factory box is missing; explicit recovery required")
	}
	var defaults struct {
		Provider           string `json:"provider"`
		ProviderCredential string `json:"providerCredential"`
	}
	if err := c.request(ctx, "GET", "/v1/controller-defaults", "", nil, &defaults); err != nil {
		return v1.LogicalBox{}, err
	}
	var b v1.LogicalBox
	err := c.request(ctx, "POST", "/v1/logical-boxes", createKey, v1.CreateLogicalBoxRequest{Name: name, Provider: defaults.Provider, ProviderCredential: defaults.ProviderCredential, DefaultAgent: "shell", DiskGiB: 10, LoginProfiles: []v1.LoginProfileRef{{Application: w.Agent, Name: w.Profile}}}, &b)
	return b, err
}

func (c *ControllerClient) Process(ctx context.Context, account, id string) (v1.ProcessTask, error) {
	if err := c.Authorize(ctx, account); err != nil {
		return v1.ProcessTask{}, err
	}
	var t v1.ProcessTask
	err := c.request(ctx, "GET", "/v1/process-tasks/"+url.PathEscape(id), "", nil, &t)
	return t, err
}

// Connection resolves the current assignment every time; callers must never
// cache a deployment endpoint across a box's hibernate/resume lifecycle.
func (c *ControllerClient) Connection(ctx context.Context, account, boxID string) (v1.LogicalBoxConnection, error) {
	var result v1.LogicalBoxConnection
	if boxID == "" {
		return result, fmt.Errorf("box identity required")
	}
	if err := c.Authorize(ctx, account); err != nil {
		return result, err
	}
	err := c.request(ctx, "GET", "/v1/logical-boxes/"+url.PathEscape(boxID)+"/connection", "", nil, &result)
	if err == nil && result.LogicalBoxID != boxID {
		err = fmt.Errorf("connection box identity changed")
	}
	return result, err
}

// SubmitPlanner launches only a fixed command using an already-staged private
// job. Neither agent prompts nor delivery/GitHub tokens enter task logs.
func (c *ControllerClient) SubmitPlanner(ctx context.Context, account, boxID, attemptID string) (v1.ProcessTask, error) {
	return c.submitFactoryJob(ctx, account, boxID, attemptID, "planner", "factory-plan:")
}

// SubmitBuilder exposes only the fixed staged wrapper, not prompts or credentials.
func (c *ControllerClient) SubmitBuilder(ctx context.Context, account, boxID, attemptID string) (v1.ProcessTask, error) {
	return c.submitFactoryJob(ctx, account, boxID, attemptID, "builder", "factory-build:")
}

func (c *ControllerClient) submitFactoryJob(ctx context.Context, account, boxID, attemptID, binary, keyPrefix string) (v1.ProcessTask, error) {
	var task v1.ProcessTask
	if boxID == "" || len(attemptID) != 32 {
		return task, fmt.Errorf("invalid planning identity")
	}
	for _, ch := range attemptID {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return task, fmt.Errorf("invalid planning identity")
		}
	}
	if err := c.Authorize(ctx, account); err != nil {
		return task, err
	}
	root := "/data/workspace/.vmbox-factory/attempts/" + attemptID
	command := "exec /data/workspace/.vmbox-factory/bin/vmbox-" + binary + " < " + root + "/job.json"
	err := c.request(ctx, "POST", "/v1/logical-boxes/"+url.PathEscape(boxID)+"/process-tasks", keyPrefix+attemptID, v1.CreateBoxTaskRequest{Agent: "shell", Prompt: command}, &task)
	if err == nil && (task.ID == "" || task.LogicalBoxID != boxID || task.Agent != "shell" || task.Prompt != command) {
		err = fmt.Errorf("submitted planner identity changed")
	}
	return task, err
}

// FindPlanner recovers an accepted submission before any fresh staging or grant
// issuance. This also works after the box hibernates or a capability expires.
func (c *ControllerClient) FindPlanner(ctx context.Context, account, boxID, attemptID string) (*v1.ProcessTask, error) {
	return c.findFactoryJob(ctx, account, boxID, attemptID, "planner")
}

func (c *ControllerClient) FindBuilder(ctx context.Context, account, boxID, attemptID string) (*v1.ProcessTask, error) {
	return c.findFactoryJob(ctx, account, boxID, attemptID, "builder")
}

func (c *ControllerClient) findFactoryJob(ctx context.Context, account, boxID, attemptID, binary string) (*v1.ProcessTask, error) {
	if boxID == "" || len(attemptID) != 32 {
		return nil, fmt.Errorf("invalid planning identity")
	}
	for _, ch := range attemptID {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return nil, fmt.Errorf("invalid planning identity")
		}
	}
	if err := c.Authorize(ctx, account); err != nil {
		return nil, err
	}
	var tasks []v1.ProcessTask
	if err := c.request(ctx, "GET", "/v1/logical-boxes/"+url.PathEscape(boxID)+"/process-tasks", "", nil, &tasks); err != nil {
		return nil, err
	}
	command := "exec /data/workspace/.vmbox-factory/bin/vmbox-" + binary + " < /data/workspace/.vmbox-factory/attempts/" + attemptID + "/job.json"
	var found *v1.ProcessTask
	for _, task := range tasks {
		if task.Prompt != command {
			continue
		}
		if found != nil || task.ID == "" || task.LogicalBoxID != boxID || task.Agent != "shell" {
			return nil, fmt.Errorf("ambiguous planning task identity")
		}
		copy := task
		found = &copy
	}
	return found, nil
}
