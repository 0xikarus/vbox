package planjob

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/factory/planner"
)

// Controlled runners below test transport and persistence, not agent behavior.
func testJob(t *testing.T, endpoint string) Job {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return Job{Version: 1, AttemptID: strings.Repeat("a", 32), Request: planner.Request{Agent: "codex", Workspace: dir, Prompt: "Inspect repository", ResultPath: filepath.Join(dir, "plan.json")}, ReceiptPath: filepath.Join(dir, "receipt.json"), DeliveryURL: endpoint, DeliveryToken: strings.Repeat("x", 32)}
}
func input(job Job) io.Reader { b, _ := json.Marshal(job); return strings.NewReader(string(b)) }
func fixtureResult(context.Context, planner.Request) (planner.Result, error) {
	n := 0
	return planner.Result{ExitCode: &n, Document: []byte(`{"version":1}`)}, nil
}

func TestDeliveryRetryDoesNotRerunAgent(t *testing.T) {
	requests, runs := 0, 0
	var first []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
			t.Error("missing capability")
		}
		b, _ := io.ReadAll(r.Body)
		if requests == 1 {
			first = b
			w.WriteHeader(409)
			return
		}
		if string(first) != string(b) {
			t.Error("retry changed evidence")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	job := testJob(t, server.URL+"/result")
	run := func(ctx context.Context, r planner.Request) (planner.Result, error) {
		runs++
		return fixtureResult(ctx, r)
	}
	if execute(context.Background(), input(job), run, nil) == nil {
		t.Fatal("refusal ignored")
	}
	if err := execute(context.Background(), input(job), run, nil); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || requests != 2 {
		t.Fatalf("runs=%d requests=%d", runs, requests)
	}
	b, err := os.ReadFile(job.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), job.DeliveryToken) {
		t.Fatal("capability persisted in receipt")
	}
	job.Request.Prompt = "Changed request"
	if execute(context.Background(), input(job), run, nil) == nil || runs != 1 {
		t.Fatal("changed attempt replayed")
	}
}

func TestActualAgentFailureDeliveredSeparately(t *testing.T) {
	for _, code := range []int{0, 7} {
		t.Run(string(rune('0'+code)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var envelope Envelope
				if json.NewDecoder(r.Body).Decode(&envelope) != nil {
					t.Error("invalid envelope")
				}
				if envelope.ExitCode == nil || *envelope.ExitCode != code || len(envelope.Document) != 0 {
					t.Error("lost exit evidence or accepted invalid document")
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			job := testJob(t, server.URL)
			run := func(context.Context, planner.Request) (planner.Result, error) {
				return planner.Result{ExitCode: &code, Document: []byte(`{"invalid":true}`)}, errors.New("fixture failure")
			}
			if err := execute(context.Background(), input(job), run, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMissingExitNeverManufacturesCompletionOrReplays(t *testing.T) {
	job := testJob(t, "https://example.invalid/result")
	runs := 0
	run := func(context.Context, planner.Request) (planner.Result, error) {
		runs++
		return planner.Result{}, errors.New("no process")
	}
	for i := 0; i < 2; i++ {
		if execute(context.Background(), input(job), run, nil) == nil {
			t.Fatal("missing outcome accepted")
		}
	}
	if runs != 1 {
		t.Fatal("ambiguous start replayed")
	}
	if _, err := os.Stat(job.ReceiptPath); !os.IsNotExist(err) {
		t.Fatal("invented terminal receipt")
	}
}

func TestDeliveryNeverFollowsRedirect(t *testing.T) {
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 307) }))
	defer server.Close()
	job := testJob(t, server.URL)
	if execute(context.Background(), input(job), fixtureResult, nil) == nil || leaked {
		t.Fatal("redirect accepted or capability leaked")
	}
}

func TestUnsafeJobsFailBeforeAgent(t *testing.T) {
	cases := []func(*Job){
		func(j *Job) { j.DeliveryURL = "http://example.com/result" },
		func(j *Job) { j.DeliveryURL = "https://example.com/result?secret=1" },
		func(j *Job) { j.DeliveryURL = "https://user:pass@example.com/result" },
		func(j *Job) { j.AttemptID = "../bad" },
		func(j *Job) { j.ReceiptPath = j.Request.ResultPath },
		func(j *Job) { j.DeliveryToken = "short" },
		func(j *Job) { os.Chmod(filepath.Dir(j.ReceiptPath), 0755) },
		func(j *Job) { os.Symlink("/etc/passwd", j.ReceiptPath) },
	}
	for i, modify := range cases {
		job := testJob(t, "https://example.invalid/result")
		modify(&job)
		run := func(context.Context, planner.Request) (planner.Result, error) {
			t.Errorf("case %d ran agent", i)
			return planner.Result{}, nil
		}
		if execute(context.Background(), input(job), run, nil) == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}
