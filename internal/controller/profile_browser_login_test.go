package controller

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/coder/websocket"
	"github.com/creack/pty"
)

func TestOfficialProfileLoginURLRejectsUntrustedLinks(t *testing.T) {
	cases := []struct{ app, raw, want string }{
		{"codex", "https://auth.openai.com/codex/device", "https://auth.openai.com/codex/device"},
		{"claude", "https://claude.ai/oauth/authorize?state=synthetic", "https://claude.ai/oauth/authorize?state=synthetic"},
		{"codex", "https://auth.openai.com.evil.test/codex/device", ""},
		{"claude", "https://evil.test/login", ""},
		{"claude", "https://claude.ai/login?access_token=synthetic", ""},
		{"codex", "http://auth.openai.com/codex/device", ""},
	}
	for _, tc := range cases {
		if got := officialProfileLoginURL(tc.app, tc.raw); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.app, got, tc.want)
		}
	}
}

func TestProfileLoginSessionShowsOnlyValidatedStatus(t *testing.T) {
	session := &profileLoginSession{id: "synthetic", app: "codex", status: "starting", expires: time.Now().Add(time.Minute)}
	session.ingest([]byte("private-token=synthetic-secret\r\nhttps://evil.test/login\r\n"))
	if session.view().URL != "" {
		t.Fatal("untrusted URL was exposed")
	}
	session.ingest([]byte("https://auth.openai.com/codex/device\r\nABCD-EFGH\r\n"))
	view := session.view()
	if view.Status != "waiting" || view.Code != "ABCD-EFGH" || view.URL != "https://auth.openai.com/codex/device" {
		t.Fatalf("wrong device status: %+v", view)
	}
	if strings.Contains(fmt.Sprintf("%+v", view), "synthetic-secret") {
		t.Fatal("raw CLI output was exposed through status")
	}
	manager := newProfileLoginManager()
	manager.sessions[session.id] = session
	session.owner = Principal{AccountID: "account-a", UserID: "owner-a"}
	if manager.get(session.id, Principal{AccountID: "account-b", UserID: "owner-a"}) != nil || manager.get(session.id, Principal{AccountID: "account-a", UserID: "owner-b"}) != nil {
		t.Fatal("foreign owner can access login session")
	}
}

func TestCodexBrowserCallbackChecksStateAndForwardsOnlyLoopback(t *testing.T) {
	if !validCodexBrowserCallbackURL("http://localhost:1455/auth/callback") || validCodexBrowserCallbackURL("http://localhost.evil.test:1455/auth/callback") {
		t.Fatal("localhost callback allow-list is incorrect")
	}
	server := NewServer(nil, nil)
	owner := Principal{AccountID: "account-a", UserID: "owner-a"}
	session := &profileLoginSession{id: "synthetic", owner: owner, app: "codex", flow: "browser", status: "starting", expires: time.Now().Add(time.Minute)}
	session.ingest([]byte("https://auth.openai.com/oauth/authorize?state=synthetic-state&redirect_uri=http%3A%2F%2F127.0.0.1%3A1455%2Fauth%2Fcallback\r\n"))
	if session.view().Status != "waiting" {
		t.Fatal("browser login did not reach waiting state")
	}
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	session.pty = master
	server.profileLogins.sessions[session.id] = session
	forwarded := 0
	server.profileLogins.callbackHTTP = &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		forwarded++
		if r.Method != http.MethodGet || r.URL.Host != "127.0.0.1:1455" || r.URL.Path != "/auth/callback" || r.URL.Query().Get("state") != "synthetic-state" || r.URL.Query().Get("code") != "synthetic-code" {
			t.Error("callback was forwarded to an unexpected destination")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	call := func(principal Principal, callback string) int {
		r := httptest.NewRequest(http.MethodPost, "/v1/login-profiles/browser/synthetic/callback", strings.NewReader(fmt.Sprintf(`{"url":%q}`, callback)))
		r.SetPathValue("id", session.id)
		w := httptest.NewRecorder()
		server.browserProfileLoginCallback(w, r, principal)
		return w.Code
	}
	good := "http://127.0.0.1:1455/auth/callback?code=synthetic-code&state=synthetic-state"
	for _, bad := range []string{
		"http://evil.test/auth/callback?code=synthetic-code&state=synthetic-state",
		"http://127.0.0.1:1456/auth/callback?code=synthetic-code&state=synthetic-state",
		"http://127.0.0.1:1455/auth/callback?code=synthetic-code&state=wrong-state",
		"http://127.0.0.1:1455/auth/callback?code=synthetic-code&state=synthetic-state&next=evil",
	} {
		if status := call(owner, bad); status < 400 {
			t.Fatalf("accepted invalid callback: %d", status)
		}
	}
	if status := call(Principal{AccountID: owner.AccountID, UserID: "other"}, good); status != 404 {
		t.Fatalf("foreign owner got %d", status)
	}
	if forwarded != 0 {
		t.Fatal("rejected callback was forwarded")
	}
	if status := call(owner, good); status != 200 {
		t.Fatalf("valid callback got %d", status)
	}
	if forwarded != 1 {
		t.Fatalf("forwarded %d callbacks", forwarded)
	}
}

func TestBrowserLoginSyntheticCodexSavesAndDeletesWorkspace(t *testing.T) {
	store, mock := testStore(t)
	var err error
	store.Envelope, err = secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("account-a:codex").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT name,encrypted_value FROM login_profiles").WithArgs("account-a", "codex").WillReturnRows(sqlmock.NewRows([]string{"name", "encrypted_value"}))
	mock.ExpectQuery("INSERT INTO login_profiles").WithArgs("account-a", "codex", "synthetic", sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
	mock.ExpectCommit()
	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix())))
	token := "header." + claims + ".signature"
	marker := filepath.Join(t.TempDir(), "home-path")
	script := filepath.Join(t.TempDir(), "fake-codex")
	body := "#!/bin/sh\n" +
		"printf '%s' \"$HOME\" > '" + marker + "'\n" +
		"printf '%s' '{\"tokens\":{\"access_token\":\"" + token + "\"}}' > \"$CODEX_HOME/auth.json\"\n" +
		"printf 'Visit https://auth.openai.com/codex/device\\nABCD-EFGH\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session := &profileLoginSession{id: "synthetic", owner: Principal{AccountID: "account-a", UserID: "owner-a"}, app: "codex", name: "synthetic", status: "starting", cancel: func() {}}
	server.runBrowserProfileLogin(ctx, session, script, "")
	if got := session.view().Status; got != "saved" {
		t.Fatalf("synthetic login ended as %s", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	home, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(home)); !os.IsNotExist(err) {
		t.Fatalf("temporary login home still exists: %v", err)
	}
}

func TestProfileAPIKeyVerificationUsesFixedProviderEndpoint(t *testing.T) {
	key := "synthetic-key-123456"
	server := NewServer(nil, nil)
	server.HTTP = &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.anthropic.com" || r.Header.Get("x-api-key") != key || r.Header.Get("Authorization") != "" {
			t.Error("API key was sent to an unexpected host or header")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-synthetic-model"}]}`)), Header: make(http.Header)}, nil
	})}
	models, err := server.profileAPIKeyModels(context.Background(), profileAPIKeyRequest{Application: "claude", Provider: "anthropic", Key: key})
	if err != nil || len(models) != 1 || models[0] != "claude-synthetic-model" {
		t.Fatalf("model verification failed: %v %v", models, err)
	}
	files, err := profileAPIKeyFiles(profileAPIKeyRequest{Application: "claude", Provider: "anthropic", Key: key, Model: models[0]})
	if err != nil || len(files["settings.json"]) == 0 || len(files[".credentials.json"]) != 0 {
		t.Fatal("invalid Claude API profile")
	}
	if err := loginprofile.Validate("claude", files, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserLoginTerminalStreamsOnlyToItsOwner(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	server := NewServer(nil, nil)
	login := &profileLoginSession{id: "synthetic", owner: Principal{AccountID: "account-a", UserID: "owner-a"}, app: "codex", status: "waiting", pty: master, expires: time.Now().Add(time.Minute)}
	login.ingest([]byte("Synthetic login prompt\r\n"))
	server.profileLogins.sessions[login.id] = login
	mux := http.NewServeMux()
	mux.HandleFunc("GET /terminal/{id}", func(w http.ResponseWriter, r *http.Request) {
		server.browserProfileLoginTerminal(w, r, Principal{AccountID: "account-a", UserID: "owner-a"})
	})
	web := httptest.NewServer(mux)
	defer web.Close()
	server.PublicURL = web.URL
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(web.URL, "http")+"/terminal/synthetic", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{web.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	kind, data, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || !strings.Contains(string(data), "Synthetic login prompt") {
		t.Fatalf("terminal stream failed: %v", err)
	}
	other := httptest.NewRequest(http.MethodGet, web.URL+"/terminal/synthetic", nil)
	other.Header.Set("Origin", web.URL)
	other.SetPathValue("id", "synthetic")
	response := httptest.NewRecorder()
	server.browserProfileLoginTerminal(response, other, Principal{AccountID: "account-a", UserID: "other-user"})
	if response.Code != http.StatusNotFound {
		t.Fatalf("foreign owner got terminal status %d", response.Code)
	}
	other.Header.Set("Origin", "https://evil.test")
	response = httptest.NewRecorder()
	server.browserProfileLoginTerminal(response, other, Principal{AccountID: "account-a", UserID: "owner-a"})
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign origin got terminal status %d", response.Code)
	}
}

func TestCanceledBrowserLoginCannotSaveCredentials(t *testing.T) {
	store, mock := testStore(t)
	script := filepath.Join(t.TempDir(), "fake-codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'https://auth.openai.com/codex/device\\nABCD-EFGH\\n'\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	session := &profileLoginSession{id: "synthetic", owner: Principal{AccountID: "account-a", UserID: "owner-a"}, app: "codex", name: "synthetic", status: "starting", cancel: cancel}
	done := make(chan struct{})
	go func() { server.runBrowserProfileLogin(ctx, session, script, ""); close(done) }()
	deadline := time.After(2 * time.Second)
	for session.view().Status != "waiting" {
		select {
		case <-deadline:
			t.Fatal("synthetic CLI did not reach prompt")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled CLI did not stop")
	}
	if got := session.view().Status; got != "canceled" {
		t.Fatalf("canceled login ended as %s", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserLoginSyntheticClaudeAcceptsCodeAndSaves(t *testing.T) {
	store, mock := testStore(t)
	var err error
	store.Envelope, err = secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("account-a:claude").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT name,encrypted_value FROM login_profiles").WithArgs("account-a", "claude").WillReturnRows(sqlmock.NewRows([]string{"name", "encrypted_value"}))
	mock.ExpectQuery("INSERT INTO login_profiles").WithArgs("account-a", "claude", "synthetic", sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
	mock.ExpectCommit()
	script := filepath.Join(t.TempDir(), "fake-claude")
	body := "#!/bin/sh\n" +
		"printf 'https://claude.ai/oauth/authorize?state=synthetic\\n'\n" +
		"IFS= read -r code\n" +
		"[ \"$code\" = synthetic-code ] || exit 1\n" +
		"mkdir -p \"$CLAUDE_CONFIG_DIR\"\n" +
		"printf '%s' '{\"claudeAiOauth\":{\"accessToken\":\"synthetic-access\",\"expiresAt\":4102444800000}}' > \"$CLAUDE_CONFIG_DIR/.credentials.json\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session := &profileLoginSession{id: "synthetic", owner: Principal{AccountID: "account-a", UserID: "owner-a"}, app: "claude", name: "synthetic", status: "starting", cancel: cancel}
	server.profileLogins.sessions[session.id] = session
	done := make(chan struct{})
	go func() { server.runBrowserProfileLogin(ctx, session, script, ""); close(done) }()
	deadline := time.After(2 * time.Second)
	for session.view().Status != "waiting" {
		select {
		case <-deadline:
			t.Fatal("synthetic Claude did not reach browser login")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/login-profiles/browser/synthetic/code", strings.NewReader(`{"code":"synthetic-code"}`))
	request.SetPathValue("id", session.id)
	response := httptest.NewRecorder()
	server.browserProfileLoginCode(response, request, session.owner)
	if response.Code != http.StatusOK {
		t.Fatalf("code submission returned HTTP %d", response.Code)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic Claude did not finish")
	}
	if got := session.view().Status; got != "saved" {
		t.Fatalf("synthetic Claude login ended as %s", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
