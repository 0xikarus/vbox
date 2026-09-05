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
	req := v1.SaveLoginProfileRequest{Files: map[string][]byte{"auth.json": []byte(`{"token":"synthetic-private-profile"}`)}}
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
	t.Run("coworker gate and durable delivery", func(t *testing.T) {
		boxID := creation.Assignment.Box.ID
		if _, err := s.DB.ExecContext(ctx, `UPDATE logical_boxes SET name='coworker-test',state='running' WHERE id=$1`, boxID); err != nil {
			t.Fatal(err)
		}
		if err := s.EnrollCoworker(ctx, p, boxID); err == nil {
			t.Fatal("account gate bypassed")
		}
		if err := s.EnableCoworkers(ctx, p, true); err != nil {
			t.Fatal(err)
		}
		if err := s.EnrollCoworker(ctx, p, boxID); err != nil {
			t.Fatal(err)
		}
		var sealed string
		if err := s.DB.QueryRowContext(ctx, `SELECT encrypted_token FROM coworkers WHERE account_id=$1 AND box_id=$2`, p.AccountID, boxID).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		token, err := s.Envelope.Open(p.AccountID+":coworker:"+boxID, sealed)
		if err != nil {
			t.Fatal(err)
		}
		defer clear(token)
		id, err := s.AuthenticateCoworker(ctx, string(token))
		if err != nil {
			t.Fatal(err)
		}
		first, err := s.SendCoworkerMessage(ctx, id, boxID, "unique-test-key", "real durable test")
		if err != nil {
			t.Fatal(err)
		}
		again, err := s.SendCoworkerMessage(ctx, id, boxID, "unique-test-key", "real durable test")
		if err != nil || again != first {
			t.Fatal("retry duplicated message")
		}
		if _, err := s.SendCoworkerMessage(ctx, id, boxID, "unique-test-key", "changed"); err == nil {
			t.Fatal("key conflict accepted")
		}
		if _, err := s.SendCoworkerMessage(ctx, CoworkerIdentity{AccountID: q.AccountID, BoxID: boxID}, boxID, "cross-account", "blocked"); err == nil {
			t.Fatal("cross-account send accepted")
		}
		inbox, err := fresh.CoworkerInbox(ctx, id, 0)
		if err != nil || len(inbox) != 1 || inbox[0].Sequence != first {
			t.Fatal("durable inbox missing")
		}
		board, err := s.EditCoworkerBoard(ctx, id, CoworkerBoardEdit{Action: "create", TaskID: "task-1", Title: "Durable board"})
		if err != nil || board.Revision != 1 {
			t.Fatalf("board create: %v", err)
		}
		if _, err = s.EditCoworkerBoard(ctx, id, CoworkerBoardEdit{Revision: 0, Action: "move", TaskID: "task-1", Status: "done"}); err == nil {
			t.Fatal("stale board update accepted")
		}
		board, err = fresh.ReadCoworkerBoard(ctx, id)
		if err != nil || len(board.Tasks) != 1 || board.Tasks[0].Status != "todo" {
			t.Fatal("board persistence failed")
		}
		inbox, err = fresh.CoworkerInbox(ctx, id, first)
		if err != nil || len(inbox) != 1 || inbox[0].Kind != "board" {
			t.Fatal("atomic board event missing")
		}
		ownerSeq, err := s.SendOwnerCoworkerMessage(ctx, p, boxID, "owner:test:1", "owner message")
		if err != nil {
			t.Fatal(err)
		}
		retrySeq, err := s.SendOwnerCoworkerMessage(ctx, p, boxID, "owner:test:1", "owner message")
		if err != nil || retrySeq != ownerSeq {
			t.Fatal("owner message retry duplicated")
		}
		ownerInbox, err := fresh.CoworkerInbox(ctx, id, ownerSeq-1)
		if err != nil || len(ownerInbox) != 1 || ownerInbox[0].Sender != "owner" || ownerInbox[0].Kind != "owner_message" {
			t.Fatal("owner attribution lost")
		}
		if _, err := s.SendOwnerCoworkerMessage(ctx, q, boxID, "owner:other:1", "cross account"); err == nil {
			t.Fatal("cross-account owner message accepted")
		}
		t.Run("concurrent event commit ordering", func(t *testing.T) {
			other, err := Open(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			other.DB.SetMaxOpenConns(1)
			if _, err = other.DB.ExecContext(ctx, "SET search_path TO "+ns); err != nil {
				t.Fatal(err)
			}
			var pid int
			if err = other.DB.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			writers := []struct {
				name  string
				write func() error
			}{
				{"peer", func() error { _, err := other.SendCoworkerMessage(ctx, id, boxID, "ordered-peer", "peer"); return err }},
				{"owner", func() error {
					_, err := other.SendOwnerCoworkerMessage(ctx, p, boxID, "ordered-owner", "owner")
					return err
				}},
				{"board", func() error {
					_, err := other.EditCoworkerBoard(ctx, id, CoworkerBoardEdit{Revision: 1, Action: "comment", TaskID: "task-1", Comment: "ordered"})
					return err
				}},
			}
			for _, writer := range writers {
				t.Run(writer.name, func(t *testing.T) {
					tx, err := s.beginCoworkerWrite(ctx, p.AccountID)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					var first int64
					if err = tx.QueryRowContext(ctx, `INSERT INTO coworker_events(account_id,recipient_box_id,sender_box_id,message_key,kind,data) VALUES($1,$2,$2,$3,'message','{"text":"held transaction"}') RETURNING sequence`, p.AccountID, boxID, "held-"+writer.name).Scan(&first); err != nil {
						t.Fatal(err)
					}
					done := make(chan error, 1)
					go func() { done <- writer.write() }()
					// Observe PostgreSQL's actual lock wait, not a timing assumption.
					deadline := time.Now().Add(2 * time.Second)
					for {
						select {
						case err := <-done:
							t.Fatalf("writer bypassed account lock: %v", err)
						default:
						}
						var waiting bool
						if err = tx.QueryRowContext(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
							t.Fatal(err)
						}
						if waiting {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("writer did not reach database lock")
						}
						time.Sleep(10 * time.Millisecond)
					}
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					select {
					case err = <-done:
						if err != nil {
							t.Fatal(err)
						}
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					events, err := s.CoworkerInbox(ctx, id, first-1)
					if err != nil || len(events) != 2 || events[0].Sequence != first || events[1].Sequence <= first {
						t.Fatalf("commit-ordered inbox: %#v, %v", events, err)
					}
				})
			}
		})
		t.Run("two coworkers over HTTP", func(t *testing.T) {
			testCoworkerHTTP(t, ctx, s, p, q, boxID, string(token))
		})
		if err := s.EnableCoworkers(ctx, p, false); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AuthenticateCoworker(ctx, string(token)); err == nil {
			t.Fatal("disabled gate accepted token")
		}
	})
	t.Run("restore preserves location", func(t *testing.T) {
		boxID := creation.Assignment.Box.ID
		if _, err := s.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='hibernated',slot_id=NULL WHERE id=$1`, boxID); err != nil {
			t.Fatal(err)
		}
		// The lower-ordinal slot is healthy and free, but in the wrong region.
		if _, err := s.DB.ExecContext(ctx, `UPDATE compute_slots SET state='free',region='wrong-region' WHERE id=$1`, slot); err != nil {
			t.Fatal(err)
		}
		allocation, err := s.ReserveAllocation(ctx, p, boxID, "region-queued", "test", time.Minute)
		if err != nil || allocation.State != "queued" {
			t.Fatalf("wrong-region slot used: %+v, %v", allocation, err)
		}
		if _, ready, err := s.ReserveNextQueuedAllocation(ctx, p.AccountID, "railway", creation.Request.ProviderCredential); err != nil || ready {
			t.Fatalf("queue used wrong region: %v, %v", ready, err)
		}
		matching := uuid()
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,provider_credential,ordinal,state,service_id,health,region) VALUES($1,$2,'railway',$3,2,'free','matching-region-service','healthy','test-region')`, matching, p.AccountID, creation.Request.ProviderCredential); err != nil {
			t.Fatal(err)
		}
		queued, ready, err := s.ReserveNextQueuedAllocation(ctx, p.AccountID, "railway", creation.Request.ProviderCredential)
		if err != nil || !ready || queued.SlotID != matching {
			t.Fatalf("queue did not select matching region: %+v, %v, %v", queued, ready, err)
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE allocation_requests SET state='ready' WHERE id=$1`, queued.RequestID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='hibernated',slot_id=NULL WHERE id=$1`, boxID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE compute_slots SET state='free' WHERE id=$1`, matching); err != nil {
			t.Fatal(err)
		}
		direct, err := s.ReserveAllocation(ctx, p, boxID, "region-direct", "test", time.Minute)
		if err != nil || direct.SlotID != matching {
			t.Fatalf("direct restore did not select matching region: %+v, %v", direct, err)
		}
	})
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
