package taskflow

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type Profile struct {
	Application string `json:"application"`
	Name        string `json:"name"`
}
type Service struct {
	Store          *Store
	GatewayToken   string
	Runner         Runner
	MaxWorkers     int
	Profiles       func(context.Context, string) ([]Profile, error)
	ValidateAssets func(context.Context, string, []string) error
	Images         map[string]bool
}

func (s *Service) limit() int {
	if s.MaxWorkers <= 0 || s.MaxWorkers > 6 {
		return 6
	}
	return s.MaxWorkers
}
func (s *Service) validateSelection(ctx context.Context, account string, c Create) error {
	if !validText(c.Idea, 30000) || (c.Agent != "codex" && c.Agent != "claude") || !validText(c.Profile, 256) || c.MaxWorkers < 1 || c.MaxWorkers > s.limit() {
		return invalid("idea, agent, saved profile and worker limit are required")
	}
	if s.Profiles == nil {
		return invalid("saved profiles unavailable")
	}
	profiles, err := s.Profiles(ctx, account)
	if err != nil {
		return err
	}
	found := false
	for _, p := range profiles {
		if p.Application == c.Agent && p.Name == c.Profile {
			found = true
		}
	}
	if !found {
		return invalid("selected profile does not exist")
	}
	if c.GitHubProfile != "" {
		if !validText(c.GitHubProfile, 256) {
			return invalid("invalid GitHub profile")
		}
		found = false
		for _, p := range profiles {
			if p.Application == "github" && p.Name == c.GitHubProfile {
				found = true
			}
		}
		if !found {
			return invalid("selected GitHub profile does not exist")
		}
	}
	if len(c.AssetIDs) > 8 {
		return invalid("at most eight attachments")
	}
	seen := map[string]bool{}
	for _, id := range c.AssetIDs {
		if !validText(id, 256) || seen[id] {
			return invalid("invalid attachment selection")
		}
		seen[id] = true
	}
	if len(c.AssetIDs) > 0 {
		if !s.Images[c.Agent] || s.ValidateAssets == nil {
			return invalid("attachments unavailable for selected agent")
		}
		if err = s.ValidateAssets(ctx, account, c.AssetIDs); err != nil {
			return invalid("attachments unavailable")
		}
	}
	return nil
}
func selection(w Workflow) Create {
	return Create{Idea: w.Idea, Agent: w.Agent, Profile: w.Profile, GitHubProfile: w.GitHubProfile, AssetIDs: w.AssetIDs, MaxWorkers: w.MaxWorkers}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, err error) {
	status, message := 500, "Task storage or service unavailable"
	switch {
	case errors.Is(err, ErrInvalid):
		status = 400
		message = err.Error()
	case errors.Is(err, ErrConflict):
		status = 409
		message = err.Error()
	case errors.Is(err, sql.ErrNoRows):
		status = 404
		message = "Task not found"
	}
	writeJSON(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 300000))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeError(w, invalid("invalid JSON body"))
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		writeError(w, invalid("expected one JSON object"))
		return false
	}
	return true
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/factory/tasks/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"enabled": s.Store != nil, "executionReady": s.Store != nil && s.Runner != nil && s.Profiles != nil, "agents": []any{map[string]any{"name": "codex", "images": s.Images["codex"]}, map[string]any{"name": "claude", "images": s.Images["claude"]}}, "maxWorkers": s.limit()})
	})
	mux.HandleFunc("GET /v1/factory/tasks", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Store.List(r.Context(), r.Header.Get("X-Vmbox-Account"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, v)
	})
	mux.HandleFunc("GET /v1/factory/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Store.Get(r.Context(), r.Header.Get("X-Vmbox-Account"), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, v)
	})
	mux.HandleFunc("POST /v1/factory/tasks", s.create)
	for _, action := range []string{"messages", "run", "retry", "cancel"} {
		mux.HandleFunc("POST /v1/factory/tasks/{id}/"+action, s.action)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		got, want := sha256.Sum256([]byte(r.Header.Get("Authorization"))), sha256.Sum256([]byte("Bearer "+s.GatewayToken))
		if s.GatewayToken == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 || !validText(r.Header.Get("X-Vmbox-Account"), 256) || !validText(r.Header.Get("X-Vmbox-User"), 256) {
			writeJSON(w, 401, map[string]string{"error": "Authenticated factory gateway required"})
			return
		}
		if s.Store == nil && r.URL.Path != "/v1/factory/tasks/capabilities" {
			writeJSON(w, 503, map[string]string{"error": "Task service unavailable"})
			return
		}
		if r.Method == "POST" && !validText(r.Header.Get("Idempotency-Key"), 256) {
			writeError(w, invalid("Idempotency-Key required"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var c Create
	if !decode(w, r, &c) {
		return
	}
	account := r.Header.Get("X-Vmbox-Account")
	// Replay is resolved before external validation, even if the profile was
	// subsequently removed. mutate repeats the check under its idempotency lock.
	hash := fingerprint([]any{"create", c})
	if v, ok, err := s.Store.replay(r.Context(), account, r.Header.Get("Idempotency-Key"), hash); err != nil {
		writeError(w, err)
		return
	} else if ok {
		writeJSON(w, 200, v)
		return
	}
	if err := s.validateSelection(r.Context(), account, c); err != nil {
		writeError(w, err)
		return
	}
	v, err := s.Store.mutate(r.Context(), account, r.Header.Get("X-Vmbox-User"), r.Header.Get("Idempotency-Key"), hash, "", 0, &c, nil)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Store) replay(ctx context.Context, account, key, hash string) (Workflow, bool, error) {
	var old string
	var b []byte
	var w Workflow
	err := s.DB.QueryRowContext(ctx, `SELECT request_hash,document FROM general_task_mutations WHERE account_id=$1 AND request_key=$2`, account, key).Scan(&old, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return w, false, nil
	}
	if err != nil {
		return w, false, err
	}
	if old != hash {
		return w, false, ErrConflict
	}
	err = json.Unmarshal(b, &w)
	return w, true, err
}
func (s *Service) action(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	var version, revision int
	var text, id string
	var body any
	switch action {
	case "messages":
		var b struct {
			Version int    `json:"version"`
			Text    string `json:"text"`
		}
		if !decode(w, r, &b) {
			return
		}
		version, text, body = b.Version, b.Text, b
	case "run":
		var b struct {
			Version      int `json:"version"`
			PlanRevision int `json:"planRevision"`
		}
		if !decode(w, r, &b) {
			return
		}
		version, revision, body = b.Version, b.PlanRevision, b
	case "retry":
		var b struct {
			Version   int    `json:"version"`
			AttemptID string `json:"attemptId"`
		}
		if !decode(w, r, &b) {
			return
		}
		version, id, body = b.Version, b.AttemptID, b
	case "cancel":
		var b struct {
			Version int `json:"version"`
		}
		if !decode(w, r, &b) {
			return
		}
		version, body = b.Version, b
	}
	if version < 1 || (action == "messages" && !validText(text, 30000)) || (action == "run" && revision < 1) || (action == "retry" && !validText(id, 128)) {
		writeError(w, invalid("invalid action fields"))
		return
	}
	v, err := s.Store.mutate(r.Context(), r.Header.Get("X-Vmbox-Account"), r.Header.Get("X-Vmbox-User"), r.Header.Get("Idempotency-Key"), fingerprint([]any{action, r.PathValue("id"), body}), r.PathValue("id"), version, nil, func(d *document) error {
		switch action {
		case "messages":
			return reply(d, text)
		case "run":
			return approve(d, revision)
		case "retry":
			return retry(d, id)
		case "cancel":
			return cancel(d)
		}
		return ErrInvalid
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
