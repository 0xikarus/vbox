package controller

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type Server struct {
	Store         *Store
	Providers     *provider.Registry
	Logger        *slog.Logger
	MaxConcurrent int
	mu            sync.Mutex
	recent        map[string][]time.Time
	PublicURL     string
	DefaultImage  string
}

func NewServer(store *Store, providers *provider.Registry) *Server {
	return &Server{Store: store, Providers: providers, Logger: slog.Default(), MaxConcurrent: 10, recent: make(map[string][]time.Time)}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "compatibility": v1.CompatibilityVersion})
	})
	mux.HandleFunc("POST /v1/runs", s.auth(s.createRun))
	mux.HandleFunc("GET /v1/runs", s.auth(s.listRuns))
	mux.HandleFunc("GET /v1/runs/{id}", s.auth(s.getRun))
	mux.HandleFunc("POST /v1/runs/{id}/stop", s.auth(s.stopRun))
	mux.HandleFunc("POST /v1/runs/{id}/start", s.auth(s.startRun))
	mux.HandleFunc("POST /v1/runs/{id}/resize", s.auth(s.resizeRun))
	mux.HandleFunc("GET /v1/runs/{id}/usage", s.auth(s.runUsage))
	mux.HandleFunc("GET /v1/runs/{id}/logs", s.auth(s.runLogs))
	mux.HandleFunc("DELETE /v1/runs/{id}", s.auth(s.deleteRun))
	mux.HandleFunc("POST /v1/runs/{id}/events", s.boxAuth(s.postEvent))
	mux.HandleFunc("GET /v1/runs/{id}/questions/{question}/answer", s.boxAuth(s.questionAnswer))
	mux.HandleFunc("GET /v1/questions", s.auth(s.questions))
	mux.HandleFunc("POST /v1/questions/{id}/answer", s.auth(s.answer))
	mux.HandleFunc("POST /v1/hosts/heartbeat", s.auth(s.hostHeartbeat))
	return securityHeaders(mux)
}

type handler func(http.ResponseWriter, *http.Request, Principal)

func (s *Server) auth(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		p, err := s.Store.Authenticate(r.Context(), token)
		if err != nil {
			writeError(w, 401, err)
			return
		}
		if !s.allow(p.AccountID) {
			writeError(w, 429, fmt.Errorf("account API rate limit exceeded"))
			return
		}
		next(w, r, p)
	}
}
func (s *Server) boxAuth(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "VMBox "))
		runID, lease, ok := strings.Cut(value, ".")
		if !ok || runID != r.PathValue("id") {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("invalid box identity"))
			return
		}
		p, err := s.Store.AuthenticateBox(r.Context(), runID, lease)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err)
			return
		}
		r.Header.Set("X-VMBox-Lease", lease)
		next(w, r, p)
	}
}
func (s *Server) allow(account string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	values := s.recent[account][:0]
	for _, v := range s.recent[account] {
		if v.After(cutoff) {
			values = append(values, v)
		}
	}
	if len(values) >= 120 {
		s.recent[account] = values
		return false
	}
	s.recent[account] = append(values, now)
	return true
}
func (s *Server) createRun(w http.ResponseWriter, r *http.Request, p Principal) {
	var req v1.CreateRunRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if len(req.Command) == 0 {
		writeError(w, 400, fmt.Errorf("command argv is required"))
		return
	}
	if req.Provider == "" {
		writeError(w, 400, fmt.Errorf("provider is required"))
		return
	}
	if req.Image == "" {
		req.Image = s.DefaultImage
	}
	if req.Provider != "incus" && !strings.Contains(req.Image, "@sha256:") {
		writeError(w, 400, fmt.Errorf("controller runs require an OCI image pinned by sha256 digest"))
		return
	}
	if _, err := s.Providers.Get(req.Provider); err != nil {
		writeError(w, 400, err)
		return
	}
	run, reused, err := s.Store.CreateRun(r.Context(), p, req, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	status := 202
	if reused {
		status = 200
	}
	writeJSON(w, status, run)
	if !reused {
		go s.schedule(context.Background(), p, run)
	}
}
func (s *Server) schedule(ctx context.Context, p Principal, run v1.Run) {
	prov, err := s.Providers.Get(run.Provider)
	if err != nil {
		return
	}
	_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, "", v1.JobProvisioning, "", nil)
	name := run.Request.Box
	if name == "" {
		name = "run-" + strings.ReplaceAll(run.ID, "-", "")[:12]
	}
	owner := provider.Owner{AccountID: p.AccountID, BoxID: name, RunID: run.ID, Lease: run.Lease}
	box, err := prov.Create(ctx, provider.CreateRequest{Name: name, Image: run.Request.Image, Region: run.Request.Region, Resources: run.Request.Resources, Owner: owner, Components: run.Request.Components, Env: map[string]string{"VMBOX_RUN_ID": run.ID, "VMBOX_EVENT_KEY": run.Lease, "VMBOX_CONTROLLER_URL": s.PublicURL}})
	if err != nil {
		s.fail(ctx, p, run, err)
		return
	}
	_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, box.ID, v1.JobRunning, "", nil)
	_, err = prov.Exec(ctx, box.ID, run.Request.Command, provider.ExecOptions{Detach: true})
	if err != nil {
		s.fail(ctx, p, run, err)
		return
	}
}
func (s *Server) fail(ctx context.Context, p Principal, run v1.Run, err error) {
	code := 1
	_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, "", v1.JobFailed, err.Error(), &code)
	s.Logger.Error("run failed", "run", run.ID, "error", err)
}
func (s *Server) getRun(w http.ResponseWriter, r *http.Request, p Principal) {
	run, err := s.Store.GetRun(r.Context(), p.AccountID, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	writeJSON(w, 200, run)
}
func (s *Server) listRuns(w http.ResponseWriter, r *http.Request, p Principal) {
	runs, err := s.Store.ListRuns(r.Context(), p.AccountID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, runs)
}
func (s *Server) runProvider(ctx context.Context, p Principal, id string) (v1.Run, provider.Provider, error) {
	run, err := s.Store.GetRun(ctx, p.AccountID, id)
	if err != nil {
		return run, nil, err
	}
	prov, err := s.Providers.Get(run.Provider)
	return run, prov, err
}
func (s *Server) stopRun(w http.ResponseWriter, r *http.Request, p Principal) {
	run, prov, err := s.runProvider(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	box, err := prov.Stop(r.Context(), run.BoxID)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	_ = s.Store.SetRunState(r.Context(), p.AccountID, run.ID, run.BoxID, v1.JobRetained, "stopped by user", nil)
	writeJSON(w, 200, box)
}
func (s *Server) startRun(w http.ResponseWriter, r *http.Request, p Principal) {
	run, prov, err := s.runProvider(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	box, err := prov.Start(r.Context(), run.BoxID)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	_ = s.Store.SetRunState(r.Context(), p.AccountID, run.ID, run.BoxID, v1.JobRunning, "resumed by user", nil)
	writeJSON(w, 200, box)
}
func (s *Server) resizeRun(w http.ResponseWriter, r *http.Request, p Principal) {
	run, prov, err := s.runProvider(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	var resources provider.Resources
	if err := decodeJSON(r, &resources); err != nil {
		writeError(w, 400, err)
		return
	}
	box, err := prov.Resize(r.Context(), run.BoxID, resources)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, box)
}
func (s *Server) runUsage(w http.ResponseWriter, r *http.Request, p Principal) {
	run, prov, err := s.runProvider(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	usage, err := prov.Usage(r.Context(), run.BoxID)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, usage)
}
func (s *Server) runLogs(w http.ResponseWriter, r *http.Request, p Principal) {
	run, prov, err := s.runProvider(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := prov.Logs(r.Context(), run.BoxID, provider.LogOptions{Follow: r.URL.Query().Get("follow") == "true", Tail: tail}, w); err != nil {
		s.Logger.Error("logs failed", "run", run.ID, "error", err)
	}
}
func (s *Server) deleteRun(w http.ResponseWriter, r *http.Request, p Principal) {
	run, prov, err := s.runProvider(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	name := run.Request.Box
	if name == "" {
		name = "run-" + strings.ReplaceAll(run.ID, "-", "")[:12]
	}
	owner := provider.Owner{AccountID: p.AccountID, BoxID: name, RunID: run.ID, Lease: run.Lease}
	if err := prov.Delete(r.Context(), run.BoxID, owner); err != nil {
		writeError(w, 400, err)
		return
	}
	_ = s.Store.SetRunState(r.Context(), p.AccountID, run.ID, run.BoxID, v1.JobDeleted, "deleted by user", nil)
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) postEvent(w http.ResponseWriter, r *http.Request, p Principal) {
	var event v1.Event
	if err := decodeJSON(r, &event); err != nil {
		writeError(w, 400, err)
		return
	}
	event.RunID = r.PathValue("id")
	lease := r.Header.Get("X-VMBox-Lease")
	mac := hmac.New(sha256.New, []byte(lease))
	fmt.Fprintf(mac, "%s\n%d\n%s\n%s\n%s", event.RunID, event.Sequence, event.Type, event.Timestamp.UTC().Format(time.RFC3339Nano), event.Message)
	signature, err := base64.RawURLEncoding.DecodeString(event.Signature)
	if err != nil || !hmac.Equal(mac.Sum(nil), signature) {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("invalid event signature"))
		return
	}
	if err := s.Store.AppendEvent(r.Context(), p, event); err != nil {
		writeError(w, 400, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
	if event.State == v1.JobSucceeded || event.State == v1.JobFailed || event.State == v1.JobCancelled {
		go s.applyLifecycle(context.Background(), p, event.RunID, event.State)
	}
}
func (s *Server) applyLifecycle(ctx context.Context, p Principal, runID string, state v1.JobState) {
	run, err := s.Store.GetRun(ctx, p.AccountID, runID)
	if err != nil {
		return
	}
	action := run.Request.Lifecycle.OnFailure
	if state == v1.JobSucceeded {
		action = run.Request.Lifecycle.OnSuccess
	}
	if action == v1.LifecycleRetain {
		_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, run.BoxID, v1.JobRetained, "", nil)
		return
	}
	grace := run.Request.Lifecycle.GracePeriod
	if grace <= 0 {
		grace = 5 * time.Minute
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	prov, err := s.Providers.Get(run.Provider)
	if err != nil {
		return
	}
	_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, run.BoxID, v1.JobCleaningUp, "", nil)
	if action == v1.LifecycleStop {
		_, err = prov.Stop(ctx, run.BoxID)
		if err == nil {
			_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, run.BoxID, v1.JobRetained, "", nil)
		}
		return
	}
	name := run.Request.Box
	if name == "" {
		name = "run-" + strings.ReplaceAll(run.ID, "-", "")[:12]
	}
	owner := provider.Owner{AccountID: p.AccountID, BoxID: name, RunID: run.ID, Lease: run.Lease}
	if err := prov.Delete(ctx, run.BoxID, owner); err != nil {
		s.Logger.Error("lifecycle cleanup failed", "run", run.ID, "error", err)
		_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, run.BoxID, v1.JobRetained, "cleanup failed: "+err.Error(), nil)
		return
	}
	_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, run.BoxID, v1.JobDeleted, "", nil)
}
func (s *Server) questions(w http.ResponseWriter, r *http.Request, p Principal) {
	values, err := s.Store.ListQuestions(r.Context(), p)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, values)
}
func (s *Server) questionAnswer(w http.ResponseWriter, r *http.Request, p Principal) {
	question, err := s.Store.QuestionAnswer(r.Context(), p.AccountID, r.PathValue("id"), r.PathValue("question"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	writeJSON(w, 200, question)
}
func (s *Server) answer(w http.ResponseWriter, r *http.Request, p Principal) {
	var req v1.AnswerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := s.Store.Answer(r.Context(), p, r.PathValue("id"), req.Answer); err != nil {
		writeError(w, 409, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) hostHeartbeat(w http.ResponseWriter, r *http.Request, p Principal) {
	var req struct {
		ID           string                `json:"id"`
		Capabilities provider.Capabilities `json:"capabilities"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := s.Store.HeartbeatHost(r.Context(), p, req.ID, req.Capabilities); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func decodeJSON(r *http.Request, v any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, v1.Error{Error: err.Error()})
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
