package standalone

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

type fakeRuntime struct {
	delivered int
	confirmed bool
	events    []boxruntime.ChatEvent
	acks      []string
}

func (r *fakeRuntime) Start(context.Context) error { return nil }
func (r *fakeRuntime) Deliver(context.Context, boxruntime.ChatInbound) error {
	r.delivered++
	return errors.New("connection lost after submission")
}
func (r *fakeRuntime) Confirm(context.Context, boxruntime.ChatInbound) (bool, error) {
	return r.confirmed, nil
}
func (r *fakeRuntime) Pull() (boxruntime.ChatEvent, bool, error) {
	if len(r.events) == 0 {
		return boxruntime.ChatEvent{}, false, nil
	}
	return r.events[0], true, nil
}
func (r *fakeRuntime) Ack(id string) error {
	r.acks = append(r.acks, id)
	r.events = r.events[1:]
	return nil
}
func (r *fakeRuntime) Stream(context.Context, io.Reader, io.Writer) error { return nil }
func (r *fakeRuntime) Frame(context.Context, io.Writer) error             { return nil }
func setup(t *testing.T) (*Server, *fakeRuntime) {
	t.Helper()
	dir := t.TempDir()
	c, e := LoadConfig(dir)
	if e != nil {
		t.Fatal(e)
	}
	db, e := OpenStore(filepath.Join(dir, "chat.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	r := &fakeRuntime{}
	return NewServer(db, c, r), r
}
func request(s *Server, method, path, scope, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+s.Config.Capabilities[scope])
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestAuthorizationAndIdempotentSubmission(t *testing.T) {
	s, _ := setup(t)
	for _, scope := range []string{"view", "desktop", ""} {
		if w := request(s, "POST", "/v1/standalone/messages", scope, "job1", `{"text":"hello"}`); w.Code != 403 {
			t.Fatalf("%s: %d", scope, w.Code)
		}
	}
	for i := 0; i < 2; i++ {
		if w := request(s, "POST", "/v1/standalone/messages", "chat", "job1", `{"text":"hello"}`); w.Code != 202 {
			t.Fatalf("%d: %s", w.Code, w.Body)
		}
	}
	if w := request(s, "POST", "/v1/standalone/messages", "chat", "job1", `{"text":"different"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	m, e := s.Store.List()
	if e != nil || len(m) != 1 {
		t.Fatalf("%v %v", m, e)
	}
	r := httptest.NewRequest("POST", "/v1/standalone/messages", strings.NewReader(`{"text":"evil"}`))
	r.Header.Set("Origin", "https://other-instance.example")
	r.Header.Set("Authorization", "Bearer "+s.Config.Capabilities["chat"])
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	other, _ := setup(t)
	r = httptest.NewRequest("GET", "/v1/standalone/messages", nil)
	r.Header.Set("Authorization", "Bearer "+other.Config.Capabilities["chat"])
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestUncertainDeliveryIsNeverResubmitted(t *testing.T) {
	s, r := setup(t)
	if _, e := s.Store.Insert("job1", "user", "hello", "queued"); e != nil {
		t.Fatal(e)
	}
	s.tick(context.Background())
	s.tick(context.Background())
	if r.delivered != 1 {
		t.Fatal(r.delivered)
	}
	r.confirmed = true
	s.tick(context.Background())
	m, _ := s.Store.List()
	if m[0].State != "accepted" {
		t.Fatal(m)
	}
	s.tick(context.Background())
	if r.delivered != 1 {
		t.Fatal(r.delivered)
	}
}
func TestOutboxPersistsBeforeAcknowledgement(t *testing.T) {
	s, r := setup(t)
	r.events = []boxruntime.ChatEvent{{ID: "event1", Text: "done"}}
	s.tick(context.Background())
	m, e := s.Store.List()
	if e != nil || len(m) != 1 || m[0].Text != "done" || len(r.acks) != 1 {
		t.Fatalf("%v %v %v", m, e, r.acks)
	}
	r.events = []boxruntime.ChatEvent{{ID: "event1", Text: "done"}}
	s.tick(context.Background())
	m, _ = s.Store.List()
	if len(m) != 1 {
		t.Fatal(m)
	}
}
func TestStoreSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "chat.sqlite")
	s, e := OpenStore(p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Insert("job1", "user", "hello", "uncertain"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = OpenStore(p)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m, e := s.Pending()
	if e != nil || len(m) != 1 || m[0].State != "uncertain" {
		t.Fatalf("%v %v", m, e)
	}
}
func TestAgentPolicyIsPrivateAndScoped(t *testing.T) {
	s, _ := setup(t)
	r := httptest.NewRequest("GET", "/v1/agent-desktop/tool-policy", nil)
	r.Header.Set("Authorization", "Bearer "+s.Config.Capabilities["chat"])
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r.Header.Set("Authorization", "DesktopAgent "+s.Config.AgentToken)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "create_agent") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestViewCapabilityCannotObtainInteractiveStream(t *testing.T) {
	s, _ := setup(t)
	w := request(s, "POST", "/v1/standalone/stream-ticket", "view", "", "")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = request(s, "GET", "/v1/standalone/messages", "view", "", "")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = request(s, "GET", "/v1/standalone/frame", "chat", "", "")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestDefaultDashboardOpensRealScopedViews(t *testing.T) {
	s, _ := setup(t)
	w := request(s, "GET", "/", "", "", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("dashboard status/cache: %d %s", w.Code, w.Header().Get("Cache-Control"))
	}
	body := w.Body.String()
	if strings.Contains(body, "<iframe") {
		t.Error("combined workspace still embeds separate scrolling pages")
	}
	for _, scope := range []string{"desktop", "terminal", "chat"} {
		if !strings.Contains(body, `data-scope="`+scope+`"`) {
			t.Errorf("missing inline %s component", scope)
		}
	}
	if !strings.Contains(body, `href="/view#`+s.Config.Capabilities["view"]+`"`) {
		t.Error("missing read-only link")
	}
	if strings.Contains(body, s.Config.AgentToken) || strings.Contains(body, s.Config.Assignment) {
		t.Error("internal agent identity leaked into dashboard")
	}
}

func TestPrivateLinkDashboardDoesNotDiscloseCapabilities(t *testing.T) {
	s, _ := setup(t)
	s.Config.PrivateLinksOnly = true
	w := request(s, "GET", "/", "", "", "")
	for _, token := range s.Config.Capabilities {
		if strings.Contains(w.Body.String(), token) {
			t.Fatal("private link exposed by dashboard")
		}
	}
}

func TestMessageFragmentEscapesContentAndRequiresChat(t *testing.T) {
	s, _ := setup(t)
	_, err := s.Store.Insert("fragment", "user", `<img src=x onerror=alert(1)>`, "queued")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"view", "desktop", ""} {
		if w := request(s, "GET", "/v1/standalone/messages/html", scope, "", ""); w.Code != 403 {
			t.Fatalf("scope %s: %d", scope, w.Code)
		}
	}
	w := request(s, "GET", "/v1/standalone/messages/html", "chat", "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "<img") || !strings.Contains(w.Body.String(), "&lt;img") {
		t.Fatal("unsafe message fragment")
	}
}
func TestTerminalTicketIsDesktopOnlyAndPurposeBound(t *testing.T) {
	s, _ := setup(t)
	for _, scope := range []string{"view", "chat", ""} {
		if w := request(s, "POST", "/v1/standalone/stream-ticket?kind=terminal", scope, "", ""); w.Code != 403 {
			t.Fatalf("scope %s: %d", scope, w.Code)
		}
	}
	w := request(s, "POST", "/v1/standalone/stream-ticket?kind=terminal", "desktop", "", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	for id := range s.tickets {
		if w := request(s, "GET", "/v1/standalone/desktop/ws?ticket="+id, "", "", ""); w.Code != 401 {
			t.Fatal("terminal ticket accepted for desktop")
		}
	}
}

type recoveringRuntime struct {
	fakeRuntime
	starts atomic.Int32
}

func (r *recoveringRuntime) Start(context.Context) error {
	if r.starts.Add(1) == 1 {
		return errors.New("temporary startup failure")
	}
	return nil
}
func TestStartupRetriesAfterTransientFailure(t *testing.T) {
	s, _ := setup(t)
	r := &recoveringRuntime{}
	s.Runtime = r
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		ready := s.state == "ready"
		s.mu.RUnlock()
		if ready && r.starts.Load() >= 2 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("startup failure stranded controller instead of retrying")
}

func TestThinkingControlsRequireChatAndPersist(t *testing.T) {
	s, _ := setup(t)
	t.Setenv("VMBOX_MODEL", "qwen3.8-27b-abliterated")
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config/vmbox"), 0700); err != nil {
		t.Fatal(err)
	}
	s.Runtime = &BoxRuntime{Home: home, Agent: "opencode"}
	saveThinkingCapabilities(home, thinkingCapabilities{Efforts: []string{"low", "medium", "xhigh"}, Default: "xhigh"})
	for _, scope := range []string{"view", "desktop", ""} {
		if w := request(s, "POST", "/v1/standalone/thinking", scope, "", `{"mode":"low"}`); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	if w := request(s, "POST", "/v1/standalone/thinking", "chat", "", `{"mode":"max"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/v1/standalone/thinking", "chat", "", `{"mode":"low"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(s, "GET", "/v1/standalone/thinking", "chat", "", ""); !strings.Contains(w.Body.String(), `"mode":"low"`) {
		t.Fatal(w.Body.String())
	}
}

func TestShutdownRequiresControlGrant(t *testing.T) {
	s, _ := setup(t)
	called := make(chan struct{}, 1)
	s.Shutdown = func() { called <- struct{}{} }
	for _, scope := range []string{"view", "chat", ""} {
		if w := request(s, "POST", "/v1/standalone/shutdown", scope, "", ""); w.Code != 403 {
			t.Fatal("unauthorized shutdown", scope, w.Code)
		}
	}
	if w := request(s, "POST", "/v1/standalone/shutdown", "desktop", "", ""); w.Code != 202 {
		t.Fatal(w.Code)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("shutdown callback not invoked")
	}
}

// Attachments remain archived in the local outbox store and are available only
// through chat authorization, never through the public live-screen capability.
func TestChatImageArchiveAndAuthorization(t *testing.T) {
	s, _ := setup(t)
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	event := boxruntime.ChatEvent{ID: "render/result", Text: "<script>alert(1)</script>", Images: []boxruntime.ChatEventImage{{Name: `fish<test>.png`, MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(png)}}}
	if err = s.Store.StoreEvent(event); err != nil {
		t.Fatal(err)
	}
	messages, err := s.Store.List()
	if err != nil || len(messages) != 1 || len(messages[0].Images) != 1 {
		t.Fatalf("missing archived image: %v", err)
	}
	path := messages[0].Images[0].URL
	if !strings.Contains(path, "%2F") {
		t.Fatal("message ID must be escaped")
	}
	for _, scope := range []string{"", "view", "desktop"} {
		if w := request(s, "GET", path, scope, "", ""); w.Code != 403 {
			t.Fatalf("%s image access: %d", scope, w.Code)
		}
	}
	w := request(s, "GET", path, "chat", "", "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), png) || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("invalid image response: %d", w.Code)
	}
	for _, suffix := range []string{"-1", "16", "text"} {
		path := strings.TrimSuffix(messages[0].Images[0].URL, "0") + suffix
		if w := request(s, "GET", path, "chat", "", ""); w.Code != 404 {
			t.Fatalf("invalid index %s: %d", suffix, w.Code)
		}
	}
	w = request(s, "GET", "/v1/standalone/messages", "chat", "", "")
	var history []Message
	if err = json.Unmarshal(w.Body.Bytes(), &history); err != nil || len(history) != 1 || len(history[0].Images) != 1 || strings.Contains(w.Body.String(), event.Images[0].Data) {
		t.Fatal("history must expose image metadata without base64")
	}
	w = request(s, "GET", "/v1/standalone/messages/html", "chat", "", "")
	if strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), `class="msg agent"`) || !strings.Contains(w.Body.String(), `data-image-src=`) || !strings.Contains(w.Body.String(), "fish&lt;test&gt;.png") {
		t.Fatal("unsafe or missing attachment markup")
	}
	event.ID = "not-an-image"
	event.Images[0].Data = base64.StdEncoding.EncodeToString([]byte("<svg onload='alert(1)'></svg>"))
	if err = s.Store.StoreEvent(event); err != nil {
		t.Fatal(err)
	}
	if w = request(s, "GET", "/v1/standalone/messages/event_not-an-image/images/0", "chat", "", ""); w.Code != 415 {
		t.Fatalf("active content must not be served as image: %d", w.Code)
	}
	s.Config.PasswordRequired = true
	if w = request(s, "GET", path, "chat", "", ""); w.Code != 403 {
		t.Fatalf("capability must not bypass password: %d", w.Code)
	}
}

func TestHubWorkspaceUsesPrefixedRoutes(t *testing.T) {
	s, _ := setup(t)
	s.Config.BasePath = "/boxes/hb-012345678901234567890123"
	s.Config.HubManaged = true
	w := request(s, "GET", "/", "", "", "")
	page := w.Body.String()
	if !strings.Contains(page, `window.VBOX_BASE_PATH="/boxes/hb-012345678901234567890123"`) || !strings.Contains(page, `src="/boxes/hb-012345678901234567890123/novnc.js"`) || !strings.Contains(page, `href="/">Hub</a>`) {
		t.Fatal("hub workspace lost its route prefix or hub link")
	}
	w = request(s, "GET", "/view", "", "", "")
	if !strings.Contains(w.Body.String(), `const scope="view"`) {
		t.Fatal("nested view inferred scope from full URL path")
	}
}
