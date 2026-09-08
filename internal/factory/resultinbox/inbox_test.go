package resultinbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Synthetic worker result fixture; no real agent execution is claimed.
const fixture = `{"version":1,"attemptId":"attempt","exitCode":0,"document":{"answer":"fixture"},"truncated":false}`

func TestDecode(t *testing.T) {
	valid := []string{fixture, `{"version":1,"attemptId":"attempt","exitCode":null,"signal":15,"truncated":true}`}
	for _, s := range valid {
		if _, err := Decode([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []string{
		fixture + ` {}`, fixture + ` null`, strings.Replace(fixture, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(fixture, `"version":1`, `"Version":1`, 1), strings.Replace(fixture, `"version":1`, `"version":2`, 1),
		strings.Replace(fixture, `"exitCode":0,`, ``, 1), strings.Replace(fixture, `"exitCode":0`, `"exitCode":null`, 1),
		strings.Replace(fixture, `"exitCode":0`, `"exitCode":0,"signal":15`, 1),
		strings.Replace(fixture, `"exitCode":0`, `"exitCode":-1`, 1), strings.Replace(fixture, `"exitCode":0`, `"exitCode":256`, 1),
		strings.Replace(fixture, `"truncated":false`, `"truncated":null`, 1), strings.Replace(fixture, `"truncated":false`, `"unknown":false`, 1),
		`null`, `[]`, `{`, fixture[:len(fixture)-1],
	}
	for _, s := range invalid {
		if _, err := Decode([]byte(s)); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted invalid fixture: %q: %v", s, err)
		}
	}
	// Whitespace is valid JSON and counts toward the exact byte limit.
	exact := append([]byte(fixture), bytes.Repeat([]byte(" "), MaxBodyBytes-len(fixture))...)
	if _, err := Decode(exact); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(append(exact, ' ')); err != ErrTooLarge {
		t.Fatal(err)
	}
}

func TestCapabilityAndConfig(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	i, err := New(&sql.DB{}, secret, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok := i.capability("a", "bc", "d")
	secret[0] = 8
	if tok != i.capability("a", "bc", "d") || len(tok) != 43 {
		t.Fatal("unstable capability")
	}
	seen := map[string]bool{tok: true}
	for _, ids := range [][3]string{{"ab", "c", "d"}, {"a", "bc", "e"}, {"b", "bc", "d"}} {
		s := i.capability(ids[0], ids[1], ids[2])
		if seen[s] {
			t.Fatal("domain collision")
		}
		seen[s] = true
	}
	for _, ttl := range []time.Duration{-1, MaxTTL + 1} {
		if _, err := New(&sql.DB{}, secret, ttl); err != ErrInvalid {
			t.Fatal(err)
		}
	}
	if _, err := New(&sql.DB{}, secret[:31], 0); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := New(nil, secret, 0); err != ErrInvalid {
		t.Fatal(err)
	}
}

func TestHTTPBounds(t *testing.T) {
	i := &Inbox{}
	cases := []struct {
		method, path, auth, body string
		status                   int
	}{
		{"GET", "/result", "", "", 405}, {"POST", "/other", "", "", 404}, {"POST", "/result", "", "", 401},
		{"POST", "/result", "Bearer " + strings.Repeat("A", 43), strings.Repeat("x", MaxBodyBytes+1), 413},
		{"POST", "/result", "Bearer " + strings.Repeat("A", 43), `{}`, 400},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		r.Header.Set("Authorization", c.auth)
		w := httptest.NewRecorder()
		i.Handler().ServeHTTP(w, r)
		if w.Code != c.status {
			t.Fatalf("%s: got %d want %d", c.path, w.Code, c.status)
		}
	}
}

// PostgreSQL fixtures live in a unique, disposable schema in the explicitly
// configured test database. These tests never contact production by default.
func postgresFixture(t *testing.T) (*Inbox, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("VMBOX_FACTORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set VMBOX_FACTORY_TEST_DATABASE_URL to run real PostgreSQL fixtures")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("test database requires postgres URL")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("open test database failed")
	}
	suffix := make([]byte, 12)
	if _, err = rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	schema := "resultinbox_test_" + hex.EncodeToString(suffix)
	if _, err = db.Exec("CREATE SCHEMA " + schema); err != nil {
		db.Close()
		t.Fatal("create test schema failed")
	}
	t.Cleanup(func() { db.Exec("DROP SCHEMA " + schema + " CASCADE"); db.Close() })
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	scoped, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal("open scoped database failed")
	}
	t.Cleanup(func() { scoped.Close() })
	i, err := New(scoped, bytes.Repeat([]byte{42}, 32), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = i.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = i.Migrate(context.Background()); err != nil {
		t.Fatal("repeat migration:", err)
	}
	return i, scoped
}

func TestPostgresDurability(t *testing.T) {
	i, db := postgresFixture(t)
	ctx := context.Background()
	token, err := i.Issue(ctx, "account", "work", "attempt")
	if err != nil {
		t.Fatal(err)
	}
	restarted, _ := New(db, bytes.Repeat([]byte{42}, 32), time.Minute)
	again, err := restarted.Issue(ctx, "account", "work", "attempt")
	if err != nil || again != token {
		t.Fatal("restart recovery failed", err)
	}
	var hash []byte
	var remaining float64
	if err = db.QueryRow(`SELECT capability_hash,EXTRACT(EPOCH FROM (expires_at-clock_timestamp())) FROM factory_result_inbox_v1`).Scan(&hash, &remaining); err != nil {
		t.Fatal("inspect fixture failed")
	}
	expected := sha256.Sum256([]byte(token))
	if !bytes.Equal(hash, expected[:]) || bytes.Contains(hash, []byte(token)) || remaining < 1700 {
		t.Fatal("hash or immutable expiry mismatch")
	}
	changed, _ := New(db, bytes.Repeat([]byte{43}, 32), 0)
	if _, err = changed.Issue(ctx, "account", "work", "attempt"); err != ErrSecretChanged {
		t.Fatal(err)
	}
	if err = i.Accept(ctx, i.capability("other", "work", "attempt"), []byte(fixture)); err != ErrUnauthorized {
		t.Fatal(err)
	}
	if err = i.Accept(ctx, token, []byte(strings.Replace(fixture, `"attempt"`, `"wrong"`, 1))); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err = i.Get(ctx, "account", "work", "attempt"); err != ErrNotFound {
		t.Fatal(err)
	}
	if err = i.Accept(ctx, token, []byte(fixture)); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Accept(ctx, token, []byte(fixture)); err != nil {
		t.Fatal(err)
	}
	if err = i.Accept(ctx, token, []byte(fixture+" ")); err != ErrConflict {
		t.Fatal(err)
	}
	if _, err = i.Get(ctx, "other", "work", "attempt"); err != ErrNotFound {
		t.Fatal(err)
	}
	body, err := restarted.Get(ctx, "account", "work", "attempt")
	if err != nil || string(body) != fixture {
		t.Fatal("persisted bytes mismatch", err)
	}
	if _, err = db.Exec(`UPDATE factory_result_inbox_v1 SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal("expire fixture failed")
	}
	if _, err = i.Issue(ctx, "account", "work", "attempt"); err != ErrExpired {
		t.Fatal(err)
	}
	if err = i.Accept(ctx, token, []byte(fixture)); err != nil {
		t.Fatal("lost response retry failed", err)
	}
	expired, err := i.Issue(ctx, "account", "work", "expired")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE factory_result_inbox_v1 SET expires_at=clock_timestamp()-interval '1 second' WHERE attempt_id='expired'`); err != nil {
		t.Fatal("expire fixture failed")
	}
	if err = i.Accept(ctx, expired, []byte(strings.Replace(fixture, `"attempt"`, `"expired"`, 1))); err != ErrExpired {
		t.Fatal(err)
	}
}

func TestPostgresConcurrent(t *testing.T) {
	i, _ := postgresFixture(t)
	ctx := context.Background()
	const n = 12
	tokens := make(chan string, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for j := 0; j < n; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := i.Issue(ctx, "account", "work", "attempt")
			tokens <- token
			errs <- err
		}()
	}
	wg.Wait()
	close(tokens)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	token := ""
	for s := range tokens {
		if token != "" && s != token {
			t.Fatal("issue race")
		}
		token = s
	}
	errs = make(chan error, n)
	for j := 0; j < n; j++ {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			body := fixture
			if j%2 == 1 {
				body += " "
			}
			errs <- i.Accept(ctx, token, []byte(body))
		}(j)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		switch err {
		case nil:
			success++
		case ErrConflict:
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != n/2 || conflict != n/2 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}
