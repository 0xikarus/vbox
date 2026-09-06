package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/secrets"
)

func TestLoginProfileValidation(t *testing.T) {
	for _, tc := range []struct {
		app, name, file string
		valid           bool
	}{
		{"codex", "work", "auth.json", true},
		{"claude", "personal", ".credentials.json", true},
		{"shell", "work", "auth.json", false},
		{"codex", "../work", "auth.json", false},
		{"codex", "work", "../auth.json", false},
		{"claude", "work", "auth.json", false},
	} {
		err := validateLoginProfile(tc.app, tc.name, v1.SaveLoginProfileRequest{Files: map[string][]byte{tc.file: []byte("synthetic")}})
		if (err == nil) != tc.valid {
			t.Errorf("%s/%s/%s: %v", tc.app, tc.name, tc.file, err)
		}
	}
	if validateLoginProfile("codex", "large", v1.SaveLoginProfileRequest{Files: map[string][]byte{"auth.json": make([]byte, 512*1024+1)}}) == nil {
		t.Fatal("oversized profile accepted")
	}
}

func TestLoginProfilesPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.DB.SetMaxOpenConns(1)
	ns := "profiles_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = s.DB.ExecContext(ctx, "CREATE SCHEMA "+ns); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(context.Background(), "DROP SCHEMA "+ns+" CASCADE")
	if _, err = s.DB.ExecContext(ctx, "SET search_path TO "+ns); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	s.Envelope, err = secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Bootstrap(ctx, "profiles-a", "owner-a", uuid())
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.Bootstrap(ctx, "profiles-b", "owner-b", uuid())
	if err != nil {
		t.Fatal(err)
	}
	req := v1.SaveLoginProfileRequest{Files: map[string][]byte{"auth.json": []byte(`{"OPENAI_API_KEY":"synthetic-private-profile"}`)}}
	for _, name := range []string{"work", "personal"} {
		if _, err = s.SaveLoginProfile(ctx, p, "codex", name, req); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.SaveLoginProfile(ctx, p, "codex", "work", req); err == nil {
		t.Fatal("overwrote existing profile")
	}
	var sealed string
	if err = s.DB.QueryRowContext(ctx, `SELECT encrypted_value FROM login_profiles WHERE account_id=$1 AND name='work'`, p.AccountID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "synthetic-private-profile") {
		t.Fatal("plaintext stored")
	}
	if _, err = s.Envelope.Open(profileEncryptionScope(q.AccountID, "codex", "work"), sealed); err == nil {
		t.Fatal("ciphertext not account bound")
	}
	if _, err = s.Envelope.Open(profileEncryptionScope(p.AccountID, "codex", "personal"), sealed); err == nil {
		t.Fatal("ciphertext not profile bound")
	}
	other, err := s.ListLoginProfiles(ctx, q)
	if err != nil || len(other) != 0 {
		t.Fatal("cross-account list")
	}
	if _, err = s.LoadLoginProfile(ctx, q, "codex", "work"); err == nil {
		t.Fatal("cross-account read")
	}
	fresh := &Store{DB: s.DB, Envelope: s.Envelope}
	got, err := fresh.LoadLoginProfile(ctx, p, "codex", "work")
	if err != nil || string(got.Files["auth.json"]) != string(req.Files["auth.json"]) {
		t.Fatal("profile roundtrip failed")
	}
	listed, err := fresh.ListLoginProfiles(ctx, p)
	if err != nil || len(listed) != 2 {
		t.Fatal("named profiles missing")
	}
	public, _ := json.Marshal(listed)
	if strings.Contains(string(public), "synthetic-private-profile") || strings.Contains(string(public), "files") {
		t.Fatal("secret in metadata")
	}
	slot := uuid()
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,health,region) VALUES($1,$2,'railway',1,'free','profile-test-service','healthy','test-region')`, slot, p.AccountID); err != nil {
		t.Fatal(err)
	}
	createReq := v1.CreateLogicalBoxRequest{Name: "profile-box", Provider: "railway", LoginProfiles: []v1.LoginProfileRef{{Application: "codex", Name: "work"}}}
	creation, err := s.BeginLogicalBoxCreation(ctx, p, createReq)
	if err != nil {
		t.Fatal(err)
	}
	if creation.Request.Region != "test-region" {
		t.Fatal("implicit creation location was not pinned")
	}
	recovered, err := fresh.RecoverableLogicalBoxCreations(ctx)
	if err != nil || len(recovered) != 1 || len(recovered[0].Request.LoginProfiles) != 1 || recovered[0].Request.LoginProfiles[0].Name != "work" {
		t.Fatalf("selection recovery failed: %v", err)
	}
	storage := provider.Storage{ID: "profile-test-volume", Name: "profile-test-volume"}
	if err = s.PersistLogicalBoxVolume(ctx, creation, storage, "creation-initializing"); err != nil {
		t.Fatal(err)
	}
	creation.Assignment.Box.VolumeID = storage.ID
	server := &Server{Store: s}
	transport := &profileTestTransport{volume: storage.ID}
	if err = server.provisionCreationProfiles(ctx, transport, creation); err != nil {
		t.Fatal(err)
	}
	if len(transport.files) != 1 || transport.files[0].Path != "/data/home/.codex/auth.json" || transport.files[0].Mode != "0600" {
		t.Fatal("incorrect selected file provisioning")
	}
	// Transport records only metadata; production bytes are intentionally cleared.
	if transport.writes != 1 {
		t.Fatal("wrong transfer count")
	}
	if container := os.Getenv("VMBOX_TEST_WORKER_CONTAINER"); container != "" {
		// Inspect bytes and ownership as the real workload user, not from the
		// transport double. The fixture credential above is deliberately fake.
		cmd := exec.CommandContext(ctx, "docker", "exec", "--user", "10001:10001", container, "sh", "-c", `test "$(stat -c '%a:%u:%g' /data/home/.codex/auth.json)" = '600:10001:10001' && test "$(stat -c '%a:%u:%g' /data/home/.codex)" = '700:10001:10001' && test ! -e /data/home/.claude/.credentials.json && sha256sum /data/home/.codex/auth.json`)
		output, err := cmd.Output()
		if err != nil || !strings.HasPrefix(string(output), fmt.Sprintf("%x", sha256.Sum256(req.Files["auth.json"]))) {
			t.Fatal("real worker selected credential contents, permissions, or isolation incorrect")
		}
		t.Log("real worker: selected Codex bytes match; file 0600, directory 0700, owned by workload user; no Claude login copied")
	}
	transport.volume = "unrelated-volume"
	if err = server.provisionCreationProfiles(ctx, transport, creation); err == nil || transport.writes != 1 {
		t.Fatal("volume mismatch sent credentials")
	}
	transport.volume = storage.ID
	creation.Assignment.Box.AssignmentGeneration++
	if err = server.provisionCreationProfiles(ctx, transport, creation); err == nil || transport.writes != 1 {
		t.Fatal("stale assignment sent credentials")
	}

}

// Real PostgreSQL above; worker transport here is a controlled test double.
func TestCredentialVolumeVerification(t *testing.T) {
	for _, tc := range []struct {
		name, service, expected, actual string
		valid                           bool
	}{
		{"exact", "service", "volume", "volume", true},
		{"wrong workspace", "service", "volume", "other", false},
		{"no attachment", "service", "volume", "", false},
		{"missing service", "", "volume", "volume", false},
		{"missing volume", "service", "", "", false},
		{"pending creation", "service", "pending:volume", "pending:volume", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyCredentialVolume(context.Background(), &profileTestTransport{volume: tc.actual}, tc.service, tc.expected)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

type profileTestTransport struct {
	provider.Provider
	volume string
	writes int
	files  []boxruntime.SyncFile
}

func (p *profileTestTransport) AttachedStorage(context.Context, string) (*provider.Storage, error) {
	return &provider.Storage{ID: p.volume}, nil
}
func (p *profileTestTransport) Exec(ctx context.Context, _ string, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if strings.Join(argv, " ") == "codex login status" || (len(argv) > 1 && argv[0] == "codex" && argv[1] == "exec") {
		return provider.ExecResult{}, nil
	}
	if len(argv) == 2 && argv[1] == "prepare-hibernate" {
		if container := os.Getenv("VMBOX_TEST_WORKER_CONTAINER"); container != "" {
			return runProfileWorker(ctx, container, argv, nil)
		}
		return provider.ExecResult{}, nil
	}
	if len(argv) != 2 || argv[1] != "sync-files" {
		return provider.ExecResult{}, fmt.Errorf("unexpected command")
	}
	data, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return provider.ExecResult{}, err
	}
	defer clear(data)
	var req boxruntime.SyncRequest
	if err = json.Unmarshal(data, &req); err != nil {
		return provider.ExecResult{}, err
	}
	p.writes++
	if container := os.Getenv("VMBOX_TEST_WORKER_CONTAINER"); container != "" {
		result, err := runProfileWorker(ctx, container, argv, bytes.NewReader(data))
		if err != nil || result.ExitCode != 0 {
			return result, err
		}
		if strings.TrimSpace(result.Stdout) != fmt.Sprintf("%x", sha256.Sum256(data)) {
			return result, fmt.Errorf("real worker digest mismatch")
		}
	}
	for _, f := range req.Files {
		clear(f.Data)
		f.Data = nil
		p.files = append(p.files, f)
	}
	return provider.ExecResult{Stdout: fmt.Sprintf("%x", sha256.Sum256(data))}, nil
}

func runProfileWorker(ctx context.Context, container string, argv []string, input io.Reader) (provider.ExecResult, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", container}, argv...)...)
	cmd.Stdin = input
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if err != nil {
		return provider.ExecResult{}, fmt.Errorf("real profile worker command failed")
	}
	return provider.ExecResult{Stdout: out.String()}, nil
}
