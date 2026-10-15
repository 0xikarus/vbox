package controller

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestGoalTaskPresetSnapshotAndReplayPostgres(t *testing.T) {
	store, p, token := goalTaskTestStore(t)
	ctx := context.Background()
	srv := &Server{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	settings := v1.CreateGoalTaskRequest{Agent: "opencode", DiskGiB: 4, MemoryGiB: 2}
	one := int64(1)
	settings.SwapGiB = &one
	body := map[string]any{"name": "glm5.3flash", "settings": settings}
	w := taskHTTP(srv, "POST", "/v1/task-profiles", token, body)
	if w.Code != 201 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var profile v1.GoalTaskProfile
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	a := goalTaskActor{Principal: p, Kind: "owner", ID: p.UserID}
	req := v1.CreateGoalTaskRequest{Name: "preset-task", Goal: "fixture goal", TaskProfile: profile.Name, Links: &v1.TaskLinkRequest{Kinds: []string{"view"}}}
	expanded, err := store.expandTaskProfile(ctx, a, req)
	if err != nil {
		t.Fatal(err)
	}
	if expanded.MemoryGiB != 2 || expanded.SwapGiB == nil || *expanded.SwapGiB != 1 || expanded.DiskGiB != 4 || len(expanded.LoginProfiles) != 0 || len(expanded.Tools) != 1 || expanded.Tools[0] != "desktop" {
		t.Fatalf("wrong configuration: %+v", expanded)
	}
	if w = taskHTTP(srv, "POST", "/v1/task-profiles", token, body); w.Code != 409 {
		t.Fatal("duplicate preset accepted", w.Code)
	}
	// API integration below needs no worker; live verification covers cgroup limits.
	settings.MemoryGiB = 0
	settings.SwapGiB = nil
	body["settings"] = settings
	if w = taskHTTP(srv, "PUT", "/v1/task-profiles/"+profile.ID, token, body); w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	w = taskHTTP(srv, "POST", "/v1/tasks", token, req)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var task v1.GoalTask
	json.Unmarshal(w.Body.Bytes(), &task)
	if task.TaskProfile != profile.Name || task.Agent != "opencode" || task.State != "queued" {
		t.Fatalf("wrong task: %+v", task)
	}
	settings.DiskGiB = 8
	body["settings"] = settings
	if w = taskHTTP(srv, "PUT", "/v1/task-profiles/"+profile.Name, token, body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved []byte
	if err = store.DB.QueryRowContext(ctx, `SELECT settings FROM goal_tasks WHERE id=$1`, task.ID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	var snapshot v1.CreateGoalTaskRequest
	json.Unmarshal(saved, &snapshot)
	if snapshot.DiskGiB != 4 {
		t.Fatal("edit changed queued task", snapshot.DiskGiB)
	}
	w = taskHTTP(srv, "POST", "/v1/tasks", token, req)
	if w.Code != 201 || w.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay after edit: %d %s", w.Code, w.Body.String())
	}
	if w = taskHTTP(srv, "DELETE", "/v1/task-profiles/"+profile.ID, token, nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = taskHTTP(srv, "POST", "/v1/tasks", token, req)
	if w.Code != 201 || w.Header().Get("Idempotency-Replayed") != "true" || !strings.Contains(w.Body.String(), task.ID) {
		t.Fatal("replay after deletion", w.Code, w.Body.String())
	}
	req.Goal = "different"
	if w = taskHTTP(srv, "POST", "/v1/tasks", token, req); w.Code != 409 {
		t.Fatal("changed replay accepted", w.Code)
	}
	if w = taskHTTP(srv, "GET", "/v1/task-profiles/"+profile.ID, token, nil); w.Code != 404 {
		t.Fatal("deleted profile readable", w.Code)
	}
}

func TestGoalTaskPresetPermissionsPostgres(t *testing.T) {
	store, p, token := goalTaskTestStore(t)
	ctx := context.Background()
	srv := &Server{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	profileID := "lp_fixture"
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO login_profiles(account_id,application,name,encrypted_value,public_id) VALUES($1,'codex','private@example.test','fixture',$2)`, p.AccountID, profileID); err != nil {
		t.Fatal(err)
	}
	settings := v1.CreateGoalTaskRequest{Agent: "codex", LoginProfiles: []v1.TaskProfileRef{{ProfileID: profileID, Model: "gpt-6-sol", ReasoningEffort: "high"}}, DiskGiB: 4}
	raw, _ := json.Marshal(settings)
	id := uuid()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO goal_task_profiles(id,account_id,name,settings) VALUES($1,$2,'codex-preset',$3)`, id, p.AccountID, raw); err != nil {
		t.Fatal(err)
	}
	credential := func(allowed []string) string {
		t.Helper()
		w := taskHTTP(srv, "POST", "/v1/task-service-credentials", token, map[string]any{"name": "fixture", "profileIds": allowed})
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		var c struct{ Token string }
		json.Unmarshal(w.Body.Bytes(), &c)
		return c.Token
	}
	denied, allowed := credential([]string{}), credential([]string{profileID})
	for _, service := range []string{denied, allowed} {
		w := taskHTTP(srv, "GET", "/v1/task-profiles", service, nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), "private@example.test") || strings.Contains(w.Body.String(), "encrypted_value") {
			t.Fatal("catalog leak", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "codex-preset") != (service == allowed) {
			t.Fatal("profile visibility ignores login grants")
		}
		for _, method := range []string{"POST", "PUT", "DELETE"} {
			path := "/v1/task-profiles"
			if method != "POST" {
				path += "/" + id
			}
			if response := taskHTTP(srv, method, path, service, map[string]any{}); response.Code != 401 && response.Code != 403 {
				t.Fatalf("service can %s: %d", method, response.Code)
			}
		}
	}
	req := v1.CreateGoalTaskRequest{Name: "test", Goal: "fixture", TaskProfile: "codex-preset"}
	if w := taskHTTP(srv, "POST", "/v1/tasks", denied, req); w.Code != 404 {
		t.Fatal("preset bypassed login permission", w.Code, w.Body.String())
	}
	req.DiskGiB = 100
	if w := taskHTTP(srv, "POST", "/v1/tasks", allowed, req); w.Code != 400 {
		t.Fatal("preset accepted override", w.Code, w.Body.String())
	}
	otherToken := uuid()
	if _, err := store.Bootstrap(ctx, "other", "other", otherToken); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "DELETE"} {
		if w := taskHTTP(srv, method, "/v1/task-profiles/"+id, otherToken, nil); w.Code != 404 {
			t.Fatal("cross-account access", method, w.Code)
		}
	}
	// Service permissions also apply to roles and to removed login profiles.
	settings.RoleIDs = []string{uuid()}
	raw, _ = json.Marshal(settings)
	store.DB.ExecContext(ctx, `UPDATE goal_task_profiles SET settings=$2 WHERE id=$1`, id, raw)
	if w := taskHTTP(srv, "GET", "/v1/task-profiles/"+id, allowed, nil); w.Code != 404 {
		t.Fatal("service saw owner-only role preset", w.Code)
	}
	settings.RoleIDs = nil
	raw, _ = json.Marshal(settings)
	store.DB.ExecContext(ctx, `UPDATE goal_task_profiles SET settings=$2 WHERE id=$1`, id, raw)
	store.DB.ExecContext(ctx, `DELETE FROM login_profiles WHERE public_id=$1`, profileID)
	if w := taskHTTP(srv, "GET", "/v1/task-profiles/"+id, allowed, nil); w.Code != 404 {
		t.Fatal("service saw broken login preset", w.Code)
	}
}
