package factory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestControllerBindingCannotCrossAccountsOrRedirect(t *testing.T) {
	requests := 0
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/v1/whoami" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"accountId":"a","role":"owner"}`))
			return
		}
		http.Redirect(w, r, other.URL, 302)
	}))
	defer upstream.Close()
	c := &ControllerClient{URL: upstream.URL, Token: "test-token", AccountID: "a"}
	if _, err := c.Profiles(context.Background(), "victim"); err == nil || requests != 0 {
		t.Fatal("foreign account reached controller")
	}
	if _, err := c.Profiles(context.Background(), "a"); err == nil || leaked {
		t.Fatal("redirect followed or accepted")
	}
}

func TestPlannerSubmissionCarriesOnlyFixedCommand(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/whoami" {
			w.Write([]byte(`{"accountId":"a","role":"owner"}`))
			return
		}
		calls++
		if r.URL.Path != "/v1/logical-boxes/box/process-tasks" || r.Method != "POST" || r.Header.Get("Idempotency-Key") != "factory-plan:"+strings.Repeat("b", 32) {
			t.Error("wrong submission")
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid body")
		}
		if body["agent"] != "shell" || strings.Contains(body["prompt"], "token") || !strings.HasSuffix(body["prompt"], "/job.json") {
			t.Error("unexpected command")
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "task", "logicalBoxId": "box", "agent": "shell", "prompt": body["prompt"]})
	}))
	defer upstream.Close()
	c := &ControllerClient{URL: upstream.URL, Token: "test-token", AccountID: "a"}
	for range 2 {
		if _, err := c.SubmitPlanner(context.Background(), "a", "box", strings.Repeat("b", 32)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.SubmitPlanner(context.Background(), "a", "box", "bad; command"); err == nil || calls != 2 {
		t.Fatal("unsafe attempt accepted")
	}
}

func TestControllerBindingRevalidatesAuthority(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"accountId":"a","role":"user"}`)) }))
	defer upstream.Close()
	c := &ControllerClient{URL: upstream.URL, Token: "test-token", AccountID: "a"}
	if c.Authorize(context.Background(), "a") == nil {
		t.Fatal("revoked owner authority accepted")
	}
}

func TestBuilderBoxAndTaskRecoveryStaySeparateFromPlanning(t *testing.T) {
	attempt := strings.Repeat("c", 32)
	work := Work{ID: strings.Repeat("a", 32), CreateWork: CreateWork{Agent: "codex", Profile: "saved"}, BoxID: "planner"}
	boxes := []v1.LogicalBox{{ID: "planner", Name: "factory-plan-" + work.ID, State: v1.LogicalBoxRunning}}
	var tasks []v1.ProcessTask
	creates, allocations := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encode := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		switch r.URL.Path {
		case "/v1/whoami":
			encode(map[string]string{"accountId": "a", "role": "owner"})
		case "/v1/controller-defaults":
			encode(map[string]string{"provider": "railway", "providerCredential": "primary"})
		case "/v1/logical-boxes":
			if r.Method == "GET" {
				encode(boxes)
				return
			}
			var in v1.CreateLogicalBoxRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				t.Error("invalid creation")
			}
			if in.Name != "factory-build-"+attempt || len(in.LoginProfiles) != 1 || in.LoginProfiles[0].Application != "codex" || in.LoginProfiles[0].Name != "saved" || r.Header.Get("Idempotency-Key") != "factory-build-box:"+attempt {
				t.Error("wrong builder provisioning")
			}
			creates++
			box := v1.LogicalBox{ID: "builder", Name: in.Name, State: v1.LogicalBoxHibernated}
			boxes = append(boxes, box)
			encode(box)
		case "/v1/logical-boxes/builder/allocate":
			allocations++
			encode(v1.Allocation{})
		case "/v1/logical-boxes/builder/process-tasks":
			if r.Method == "GET" {
				encode(tasks)
				return
			}
			var in v1.CreateBoxTaskRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				t.Error("invalid task")
			}
			want := "exec /data/workspace/.vmbox-factory/bin/vmbox-builder < /data/workspace/.vmbox-factory/attempts/" + attempt + "/job.json"
			if in.Prompt != want || in.Agent != "shell" || r.Header.Get("Idempotency-Key") != "factory-build:"+attempt {
				t.Error("builder command changed")
			}
			task := v1.ProcessTask{ID: "task", LogicalBoxID: "builder", Agent: "shell", Prompt: in.Prompt}
			tasks = append(tasks, task)
			encode(task)
		default:
			t.Error("unexpected request", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := &ControllerClient{URL: server.URL, Token: "fixture", AccountID: "a"}
	ctx := context.Background()
	b, err := c.EnsureBuilderBox(ctx, "a", work, attempt, "")
	if err != nil || b.ID != "builder" {
		t.Fatal(b, err)
	}
	if _, err = c.EnsureBuilderBox(ctx, "a", work, attempt, b.ID); err != nil {
		t.Fatal(err)
	}
	if creates != 1 || allocations != 1 {
		t.Fatal("builder identity not recovered")
	}
	if _, err = c.EnsureBuilderBox(ctx, "a", work, attempt, "planner"); err == nil {
		t.Fatal("planner reused as builder")
	}
	if _, err = c.SubmitBuilder(ctx, "a", b.ID, attempt); err != nil {
		t.Fatal(err)
	}
	recovered, err := c.FindBuilder(ctx, "a", b.ID, attempt)
	if err != nil || recovered == nil || recovered.ID != "task" {
		t.Fatal("builder receipt not found", err)
	}
	if p, err := c.FindPlanner(ctx, "a", b.ID, attempt); err != nil || p != nil {
		t.Fatal("builder mistaken for planner")
	}
	tasks = append(tasks, tasks[0])
	if _, err = c.FindBuilder(ctx, "a", b.ID, attempt); err == nil {
		t.Fatal("ambiguous duplicate accepted")
	}
}
