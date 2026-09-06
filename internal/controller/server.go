package controller

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/events"
	"github.com/0xikarus/vmbox-service/internal/notifications"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type ProviderResolver func(context.Context, string, string, string) (provider.Provider, error)
type NotificationSink func(context.Context, v1.Run, string, v1.JobState, string, string)

type Server struct {
	Store          *Store
	Providers      *provider.Registry
	Logger         *slog.Logger
	MaxConcurrent  int
	mu             sync.Mutex
	recent         map[string][]time.Time
	replyWatches   map[string]struct{}
	PublicURL      string
	DefaultImage   string
	WorkerRuntime  []byte
	Resolve        ProviderResolver
	Bootstrap      func(context.Context, provider.Provider, provider.Box, []string) error
	HTTP           *http.Client
	ReconcileEvery time.Duration
	Deliver        NotificationSink
	// StartTask hands a freshly created task to its agent. It is a field so
	// that tests can observe the hand-off instead of racing a detached
	// goroutine against their fixtures.
	StartTask func(context.Context, string, v1.BoxTask)
	// StartHibernate lets tests observe the durable hand-off without running a
	// provider operation. Production leaves it nil and uses the reconciler.
	StartHibernate func(context.Context, Principal, string) error
	StartDelete    func(context.Context, Principal, string) error
}

// startBoxTask runs a new task without making the caller wait for the agent.
func (s *Server) startBoxTask(accountID string, task v1.BoxTask) {
	if s.StartTask != nil {
		s.StartTask(context.Background(), accountID, task)
		return
	}
	go func() {
		if err := s.executeBoxTask(context.Background(), accountID, task); err != nil {
			s.Logger.Error("direct box message could not start agent", "task", task.ID, "error", err)
		}
	}()
}

func NewServer(store *Store, providers *provider.Registry) *Server {
	return &Server{Store: store, Providers: providers, Logger: slog.Default(), MaxConcurrent: 10, recent: make(map[string][]time.Time), replyWatches: make(map[string]struct{})}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", uiHandler("index.html", "text/html; charset=utf-8", true))
	mux.HandleFunc("GET /app.css", uiHandler("app.css", "text/css; charset=utf-8", false))
	mux.HandleFunc("GET /app.js", uiHandler("app.js", "text/javascript; charset=utf-8", false))
	mux.HandleFunc("GET /favicon.svg", uiHandler("favicon.svg", "image/svg+xml", false))
	mux.HandleFunc("GET /favicon.ico", uiHandler("favicon.svg", "image/svg+xml", false))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "compatibility": v1.CompatibilityVersion})
	})
	mux.HandleFunc("GET /v1/fleet/status", s.auth(s.fleetStatus))
	mux.HandleFunc("GET /v1/fleet/slots", s.auth(s.fleetSlots))
	mux.HandleFunc("PUT /v1/fleet/slots", s.owner(s.setFleetSlots))
	mux.HandleFunc("GET /v1/fleet/regions", s.auth(s.fleetRegions))
	mux.HandleFunc("PUT /v1/fleet/location", s.owner(s.setFleetLocation))
	mux.HandleFunc("GET /v1/inventory", s.auth(s.boxInventoryHandler))
	mux.HandleFunc("GET /v1/capabilities", s.auth(func(w http.ResponseWriter, r *http.Request, p Principal) {
		writeJSON(w, 200, map[string]any{"nativeSessions": true, "nativeAttach": p.Role == "owner", "snapshotUpdates": true, "providerEdits": p.Role == "owner", "oneShotTasks": true, "interactiveLaunch": p.Role == "owner"})
	}))
	mux.HandleFunc("GET /v1/logical-boxes/{id}/sessions", s.auth(s.sessionsHandler))
	mux.HandleFunc("GET /v1/logical-boxes/{id}/sessions/primary", s.owner(s.primarySessionHandler))
	mux.HandleFunc("PUT /v1/logical-boxes/{id}/sessions/primary", s.owner(s.primarySessionHandler))
	mux.HandleFunc("GET /v1/logical-boxes/{id}/updates", s.auth(s.updatesHandler))
	mux.HandleFunc("POST /v1/logical-boxes/{id}/updates/ack", s.auth(s.ackUpdateHandler))
	mux.HandleFunc("POST /v1/logical-boxes/{id}/sessions/welcome", s.owner(s.nativeWelcomeHandler))
	mux.HandleFunc("POST /v1/logical-boxes/{id}/sessions/interactive", s.owner(s.interactiveStartHandler))
	mux.HandleFunc("POST /v1/logical-boxes/{id}/process-tasks", s.auth(s.createProcessHandler))
	mux.HandleFunc("GET /v1/logical-boxes/{id}/process-tasks", s.auth(s.processResultHandler))
	mux.HandleFunc("GET /v1/process-tasks/{id}", s.auth(s.processResultHandler))
	mux.HandleFunc("GET /v1/process-tasks/{id}/output", s.auth(s.processResultHandler))
	mux.HandleFunc("POST /v1/logical-boxes/{id}/sessions/enable", s.owner(s.enableNativeHandler))
	mux.HandleFunc("GET /v1/logical-boxes/{id}/native-connection", s.owner(s.nativeConnectionHandler))
	mux.HandleFunc("POST /v1/logical-boxes", s.auth(s.createLogicalBoxHandler))
	mux.HandleFunc("POST /v1/logical-boxes/{id}/allocate", s.auth(s.reserveLogicalBox))
	mux.HandleFunc("GET /v1/logical-boxes", s.auth(s.listLogicalBoxes))
	mux.HandleFunc("GET /v1/logical-boxes/{id}", s.auth(s.getLogicalBox))
	mux.HandleFunc("GET /v1/logical-boxes/{id}/status", s.auth(s.boxStatusHandler))
	mux.HandleFunc("PATCH /v1/logical-boxes/{id}", s.auth(s.updateLogicalBoxHandler))
	mux.HandleFunc("POST /v1/logical-boxes/{id}/hibernate", s.auth(s.hibernateLogicalBoxHandler))
	mux.HandleFunc("DELETE /v1/logical-boxes/{id}/volume", s.auth(s.deleteLogicalBoxVolumeHandler))
	mux.HandleFunc("GET /v1/allocations/{id}", s.auth(s.getAllocation))
	mux.HandleFunc("POST /v1/logical-boxes/{id}/tasks", s.auth(s.createBoxTaskHandler))
	mux.HandleFunc("GET /v1/logical-boxes/{id}/tasks", s.auth(s.listBoxTasksHandler))
	mux.HandleFunc("GET /v1/logical-boxes/{id}/connection", s.owner(s.logicalBoxConnectionHandler))
	mux.HandleFunc("GET /v1/logical-boxes/{id}/terminal", s.auth(s.terminalSnapshotHandler))
	mux.HandleFunc("POST /v1/logical-boxes/{id}/terminal/input", s.auth(s.terminalInputHandler))
	mux.HandleFunc("POST /v1/logical-boxes/{id}/messages", s.auth(s.directBoxMessageHandler))
	mux.HandleFunc("GET /v1/tasks/{id}", s.auth(s.getBoxTaskHandler))
	mux.HandleFunc("GET /v1/tasks/{id}/messages", s.auth(s.listBoxMessagesHandler))
	mux.HandleFunc("POST /v1/tasks/{id}/messages", s.auth(s.sendBoxMessageHandler))
	mux.HandleFunc("GET /v1/chat-groups", s.auth(s.chatGroupsHandler))
	mux.HandleFunc("POST /v1/chat-groups", s.auth(s.chatGroupsHandler))
	mux.HandleFunc("GET /v1/chat-groups/{id}", s.auth(s.chatGroupHandler))
	mux.HandleFunc("PUT /v1/chat-groups/{id}", s.auth(s.chatGroupHandler))
	mux.HandleFunc("DELETE /v1/chat-groups/{id}", s.auth(s.chatGroupHandler))
	mux.HandleFunc("GET /v1/chat-groups/{id}/messages", s.auth(s.chatGroupMessagesHandler))
	mux.HandleFunc("POST /v1/chat-groups/{id}/messages", s.auth(s.chatGroupMessagesHandler))
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
	mux.HandleFunc("GET /v1/users", s.owner(s.listUsers))
	mux.HandleFunc("GET /v1/whoami", s.auth(s.whoami))
	mux.HandleFunc("POST /v1/token/rotate", s.owner(s.rotateToken))
	mux.HandleFunc("POST /v1/users", s.owner(s.createUser))
	mux.HandleFunc("DELETE /v1/users/{id}", s.owner(s.removeUser))
	mux.HandleFunc("GET /v1/provider-credentials", s.owner(s.listProviderCredentials))
	mux.HandleFunc("GET /v1/login-profiles", s.owner(s.listLoginProfiles))
	mux.HandleFunc("DELETE /v1/login-profiles/{application}/{name}", s.owner(s.deleteLoginProfile))
	mux.HandleFunc("GET /v1/locations", s.auth(s.locationsHandler))
	mux.HandleFunc("PUT /v1/login-profiles/{application}/{name}", s.owner(s.saveLoginProfile))
	mux.HandleFunc("GET /v1/provider-schemas", s.owner(s.providerSchemasHandler))
	mux.HandleFunc("GET /v1/controller-defaults", s.auth(s.defaultProviderHandler))
	mux.HandleFunc("PUT /v1/controller-defaults", s.owner(s.defaultProviderHandler))
	mux.HandleFunc("GET /v1/provider-credentials/{provider}/{name}", s.owner(s.providerShowHandler))
	mux.HandleFunc("PATCH /v1/provider-credentials/{provider}/{name}", s.owner(s.providerPatchHandler))
	mux.HandleFunc("POST /v1/provider-credentials/{provider}/{name}/validate", s.owner(s.providerValidateHandler))
	mux.HandleFunc("PUT /v1/provider-credentials/{provider}/{name}", s.owner(s.putProviderCredential))
	mux.HandleFunc("DELETE /v1/provider-credentials/{provider}/{name}", s.owner(s.deleteProviderCredential))
	mux.HandleFunc("GET /v1/notifications", s.owner(s.listNotifications))
	mux.HandleFunc("PUT /v1/notifications/{kind}/{name}", s.owner(s.putNotification))
	mux.HandleFunc("POST /v1/notifications/{kind}/{name}/test", s.owner(s.testNotification))
	mux.HandleFunc("DELETE /v1/notifications/{kind}/{name}", s.owner(s.deleteNotification))
	mux.HandleFunc("POST /v1/integrations/{kind}/{account}/{name}", s.notificationInbound)
	return securityHeaders(mux)
}

type handler func(http.ResponseWriter, *http.Request, Principal)

func (s *Server) auth(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := authorizationValue(r.Header.Get("Authorization"), "Bearer")
		if !ok {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("Bearer authorization is required"))
			return
		}
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
func (s *Server) owner(next handler) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request, p Principal) {
		if p.Role != "owner" {
			writeError(w, http.StatusForbidden, fmt.Errorf("owner role required"))
			return
		}
		next(w, r, p)
	})
}
func (s *Server) boxAuth(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		value, authorized := authorizationValue(r.Header.Get("Authorization"), "VMBox")
		if !authorized {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("VMBox authorization is required"))
			return
		}
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
	if !immutableImage(req.Provider, req.Image) {
		writeError(w, 400, fmt.Errorf("controller runs require an OCI image pinned by sha256 digest"))
		return
	}
	if _, err := s.provider(r.Context(), p.AccountID, req.Provider, req.ProviderCredential); err != nil {
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
	prov, err := s.provider(ctx, p.AccountID, run.Provider, run.Request.ProviderCredential)
	if err != nil {
		s.fail(ctx, p, run, err)
		return
	}
	_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, "", v1.JobProvisioning, "", nil)
	s.notify(ctx, run, "provisioning", v1.JobProvisioning, "box provisioning started", "")
	name := run.Request.Box
	if name == "" {
		name = "run-" + strings.ReplaceAll(run.ID, "-", "")[:12]
	}
	owner := provider.Owner{AccountID: p.AccountID, BoxID: name, RunID: run.ID, Lease: run.Lease}
	box, err := prov.Create(ctx, provider.CreateRequest{Name: name, Image: run.Request.Image, Region: run.Request.Region, Resources: run.Request.Resources, Owner: owner, Components: run.Request.Components, Env: map[string]string{
		"VMBOX_RUN_ID": run.ID, "VMBOX_EVENT_KEY": run.Lease, "VMBOX_CONTROLLER_URL": s.PublicURL,
		"VMBOX_NAME": name, "VMBOX_PROVIDER": run.Provider, "VMBOX_REGION": run.Request.Region,
		"VMBOX_CPU":        strconv.FormatFloat(run.Request.Resources.CPU, 'f', -1, 64),
		"VMBOX_MEMORY_MIB": strconv.FormatInt(run.Request.Resources.MemoryMiB, 10),
		"VMBOX_DISK_GIB":   strconv.FormatInt(run.Request.Resources.DiskGiB, 10), "VMBOX_WORKSPACE": "/data/workspace",
		"VMBOX_COST": "use vmbox cost " + name,
	}})
	if err != nil {
		s.fail(ctx, p, run, err)
		return
	}
	_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, box.ID, v1.JobPreparing, "", nil)
	if s.Bootstrap != nil {
		if err := s.Bootstrap(ctx, prov, box, run.Request.Components); err != nil {
			s.fail(ctx, p, run, fmt.Errorf("bootstrap box runtime: %w", err))
			return
		}
	}
	_, err = prov.Exec(ctx, box.ID, run.Request.Command, provider.ExecOptions{Detach: true})
	if err != nil {
		s.fail(ctx, p, run, err)
		return
	}
	_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, box.ID, v1.JobRunning, "", nil)
	run.BoxID = box.ID
	s.notify(ctx, run, "ready", v1.JobRunning, "box is ready and command was accepted", "")
}
func (s *Server) fail(ctx context.Context, p Principal, run v1.Run, err error) {
	code := 1
	_ = s.Store.SetRunState(ctx, p.AccountID, run.ID, "", v1.JobFailed, err.Error(), &code)
	s.Logger.Error("run failed", "run", run.ID, "error", err)
	s.notify(ctx, run, "failed", v1.JobFailed, err.Error(), "")
}

func (s *Server) provider(ctx context.Context, accountID, name, credential string) (provider.Provider, error) {
	if s.Resolve != nil {
		return s.Resolve(ctx, accountID, name, credential)
	}
	return s.Providers.Get(name)
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
	prov, err := s.provider(ctx, run.AccountID, run.Provider, run.Request.ProviderCredential)
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
	s.notify(r.Context(), run, "stopped", v1.JobRetained, "stopped by user", "")
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
	s.notify(r.Context(), run, "resumed", v1.JobRunning, "resumed by user", "")
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
	s.notify(r.Context(), run, "deleted", v1.JobDeleted, "deleted by user", "")
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
	if event.ID == "" || event.Sequence == 0 || event.Timestamp.IsZero() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("event id, sequence, and timestamp are required"))
		return
	}
	if !events.Verify([]byte(lease), event) {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("invalid event signature"))
		return
	}
	inserted, err := s.Store.AppendEvent(r.Context(), p, event)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
	if !inserted {
		return
	}
	if run, err := s.Store.GetRun(r.Context(), p.AccountID, event.RunID); err == nil {
		questionID := ""
		if event.Type == "needs_input" && len(event.Data) > 0 {
			var data struct {
				QuestionID string `json:"questionId"`
			}
			_ = json.Unmarshal(event.Data, &data)
			questionID = data.QuestionID
		}
		go s.notify(context.Background(), run, event.Type, event.State, event.Message, questionID)
	}
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
		s.cleanupRun(ctx, p, run, action, "lifecycle policy")
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
	s.cleanupRun(ctx, p, run, action, "lifecycle policy")
}

func (s *Server) StartReconciler(ctx context.Context) error {
	if err := s.ReconcileNow(ctx); err != nil {
		s.Logger.Error("initial controller reconciliation failed", "error", err)
	}
	if err := s.ReconcileFleetNow(ctx); err != nil {
		s.Logger.Error("initial compute fleet reconciliation failed", "error", err)
	}
	if err := s.ReconcileLogicalBoxCreationsNow(ctx); err != nil {
		s.Logger.Error("initial logical box creation reconciliation failed", "error", err)
	}
	if err := s.ReconcileAllocationsNow(ctx); err != nil {
		s.Logger.Error("initial logical box allocation reconciliation failed", "error", err)
	}
	if err := s.ReconcileLogicalBoxHibernatesNow(ctx); err != nil {
		s.Logger.Error("initial logical box hibernate reconciliation failed", "error", err)
	}
	if err := s.ReconcileLogicalBoxDeletesNow(ctx); err != nil {
		s.Logger.Error("initial logical box deletion reconciliation failed", "error", err)
	}
	if err := s.ReconcileBoxInteractionsNow(ctx); err != nil {
		s.Logger.Error("initial logical box task reconciliation failed", "error", err)
	}
	if err := s.ReconcileProcessesNow(ctx); err != nil {
		s.Logger.Error("initial process reconciliation failed", "error", err)
	}
	if err := s.ReconcileGroupDeliveriesNow(ctx); err != nil {
		s.Logger.Error("initial group message reconciliation failed", "error", err)
	}
	interval := s.ReconcileEvery
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.ReconcileNow(ctx); err != nil {
					s.Logger.Error("controller reconciliation failed", "error", err)
				}
				if err := s.ReconcileFleetNow(ctx); err != nil {
					s.Logger.Error("compute fleet reconciliation failed", "error", err)
				}
				if err := s.ReconcileLogicalBoxCreationsNow(ctx); err != nil {
					s.Logger.Error("logical box creation reconciliation failed", "error", err)
				}
				if err := s.ReconcileAllocationsNow(ctx); err != nil {
					s.Logger.Error("logical box allocation reconciliation failed", "error", err)
				}
				if err := s.ReconcileLogicalBoxHibernatesNow(ctx); err != nil {
					s.Logger.Error("logical box hibernate reconciliation failed", "error", err)
				}
				if err := s.ReconcileLogicalBoxDeletesNow(ctx); err != nil {
					s.Logger.Error("logical box deletion reconciliation failed", "error", err)
				}
				if err := s.ReconcileBoxInteractionsNow(ctx); err != nil {
					s.Logger.Error("logical box task reconciliation failed", "error", err)
				}
				if err := s.ReconcileProcessesNow(ctx); err != nil {
					s.Logger.Error("process reconciliation failed", "error", err)
				}
				if err := s.ReconcileGroupDeliveriesNow(ctx); err != nil {
					s.Logger.Error("group message reconciliation failed", "error", err)
				}
			}
		}
	}()
	return nil
}

func (s *Server) ReconcileNow(ctx context.Context) error {
	runs, err := s.Store.ListReconcileRuns(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, run := range runs {
		p := Principal{AccountID: run.AccountID, Subject: "controller:reconciler"}
		if maxTTLExpired(run, now) {
			if run.BoxID == "" {
				_ = s.Store.SetRunState(ctx, run.AccountID, run.ID, "", v1.JobDeleted, "maximum TTL expired before provisioning", nil)
				s.notify(ctx, run, "ttl_expired", v1.JobDeleted, "maximum TTL expired", "")
				continue
			}
			s.cleanupRun(ctx, p, run, v1.LifecycleDelete, "maximum TTL expired")
			continue
		}
		switch run.State {
		case v1.JobQueued, v1.JobProvisioning:
			if run.BoxID == "" {
				s.schedule(ctx, p, run)
			}
		case v1.JobPreparing:
			// Provider creation and runtime detached launch are idempotent by box
			// ownership and controller run ID, so this closes either crash window.
			s.schedule(ctx, p, run)
		case v1.JobRunning, v1.JobNeedsInput, v1.JobResuming:
			prov, err := s.provider(ctx, run.AccountID, run.Provider, run.Request.ProviderCredential)
			if err != nil {
				s.Logger.Error("resolve provider during reconciliation", "run", run.ID, "error", err)
				continue
			}
			actual, err := prov.Inspect(ctx, run.BoxID)
			if errors.Is(err, provider.ErrNotFound) {
				s.fail(ctx, p, run, fmt.Errorf("provider box disappeared"))
				continue
			}
			if err != nil {
				s.Logger.Error("inspect provider box", "run", run.ID, "error", err)
				continue
			}
			if actual.State == provider.StateFailed {
				s.fail(ctx, p, run, fmt.Errorf("provider reports failed box state"))
				continue
			}
			desired := actual
			desired.State = provider.StateRunning
			if _, err := prov.Reconcile(ctx, desired); err != nil {
				s.Logger.Error("reconcile provider box", "run", run.ID, "error", err)
			}
		case v1.JobSucceeded, v1.JobFailed, v1.JobCancelled:
			finished := run.UpdatedAt
			if run.FinishedAt != nil {
				finished = *run.FinishedAt
			}
			if now.Before(finished.Add(run.Request.Lifecycle.GracePeriod)) {
				continue
			}
			action := run.Request.Lifecycle.OnFailure
			if run.State == v1.JobSucceeded {
				action = run.Request.Lifecycle.OnSuccess
			}
			s.cleanupRun(ctx, p, run, action, "lifecycle reconciliation")
		}
	}
	return nil
}

func (s *Server) cleanupRun(ctx context.Context, p Principal, run v1.Run, action v1.LifecycleAction, reason string) {
	if action == v1.LifecycleRetain {
		_ = s.Store.SetRunState(ctx, run.AccountID, run.ID, run.BoxID, v1.JobRetained, reason, nil)
		s.notify(ctx, run, "retained", v1.JobRetained, reason, "")
		return
	}
	prov, err := s.provider(ctx, run.AccountID, run.Provider, run.Request.ProviderCredential)
	if err != nil {
		s.Logger.Error("resolve provider for cleanup", "run", run.ID, "error", err)
		return
	}
	_ = s.Store.SetRunState(ctx, run.AccountID, run.ID, run.BoxID, v1.JobCleaningUp, reason, nil)
	if action == v1.LifecycleStop {
		_, err = prov.Stop(ctx, run.BoxID)
		if err == nil {
			_ = s.Store.SetRunState(ctx, run.AccountID, run.ID, run.BoxID, v1.JobRetained, reason, nil)
			s.notify(ctx, run, "stopped", v1.JobRetained, reason, "")
		}
		return
	}
	name := run.Request.Box
	if name == "" {
		name = "run-" + strings.ReplaceAll(run.ID, "-", "")[:12]
	}
	owner := provider.Owner{AccountID: run.AccountID, BoxID: name, RunID: run.ID, Lease: run.Lease}
	if err := prov.Delete(ctx, run.BoxID, owner); err != nil {
		s.Logger.Error("cleanup failed", "run", run.ID, "error", err)
		_ = s.Store.SetRunState(ctx, run.AccountID, run.ID, run.BoxID, v1.JobRetained, "cleanup failed: "+err.Error(), nil)
		return
	}
	_ = s.Store.SetRunState(ctx, run.AccountID, run.ID, run.BoxID, v1.JobDeleted, reason, nil)
	s.notify(ctx, run, "deleted", v1.JobDeleted, reason, "")
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
	if question, err := s.Store.GetQuestion(r.Context(), p.AccountID, r.PathValue("id")); err == nil {
		if run, err := s.Store.GetRun(r.Context(), p.AccountID, question.RunID); err == nil {
			s.notify(r.Context(), run, "answer_accepted", v1.JobRunning, "answer accepted; job resumed", question.ID)
		}
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

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, p Principal) {
	values, err := s.Store.ListUsers(r.Context(), p)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, values)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, p Principal) {
	var req v1.CreateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	value, err := s.Store.CreateUser(r.Context(), p, req)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (s *Server) removeUser(w http.ResponseWriter, r *http.Request, p Principal) {
	if err := s.Store.RemoveUser(r.Context(), p, r.PathValue("id")); err != nil {
		writeError(w, 400, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listProviderCredentials(w http.ResponseWriter, r *http.Request, p Principal) {
	values, err := s.Store.ListProviderCredentials(r.Context(), p.AccountID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, values)
}

func (s *Server) putProviderCredential(w http.ResponseWriter, r *http.Request, p Principal) {
	var req v1.PutProviderCredentialRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	value, err := s.Store.PutProviderCredential(r.Context(), p, r.PathValue("provider"), r.PathValue("name"), req)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, value)
}

func (s *Server) deleteProviderCredential(w http.ResponseWriter, r *http.Request, p Principal) {
	writeError(w, http.StatusConflict, fmt.Errorf("provider deletion requires an explicit resource/default migration; no credentials deleted"))
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request, p Principal) {
	values, err := s.Store.ListNotifications(r.Context(), p.AccountID, false)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	result := make([]v1.NotificationDestination, 0, len(values))
	for _, value := range values {
		result = append(result, value.NotificationDestination)
	}
	writeJSON(w, 200, result)
}

func (s *Server) putNotification(w http.ResponseWriter, r *http.Request, p Principal) {
	var req v1.PutNotificationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	kind := r.PathValue("kind")
	if kind == "discord" && (len(req.AllowedUsers) == 0 || len(req.AllowedChats) == 0) {
		writeError(w, 400, fmt.Errorf("Discord requires non-empty user and chat/channel allowlists"))
		return
	}
	if err := validateInteractiveNotification(kind, req); err != nil {
		writeError(w, 400, err)
		return
	}
	value, err := s.Store.PutNotification(r.Context(), p, kind, r.PathValue("name"), req)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, value)
}

func (s *Server) deleteNotification(w http.ResponseWriter, r *http.Request, p Principal) {
	if err := s.Store.DeleteNotification(r.Context(), p, r.PathValue("kind"), r.PathValue("name")); err != nil {
		writeError(w, 404, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testNotification(w http.ResponseWriter, r *http.Request, p Principal) {
	values, err := s.Store.ListNotifications(r.Context(), p.AccountID, true)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	for _, value := range values {
		if value.Kind != r.PathValue("kind") || value.Name != r.PathValue("name") {
			continue
		}
		adapter, err := s.notificationAdapter(value)
		if err == nil {
			errors := (notifications.Dispatcher{Adapters: []notifications.Adapter{adapter}}).Send(r.Context(), notifications.Delivery{AccountID: p.AccountID, Event: "test", Message: "vmbox notification test"})
			err = errors[adapter.Name()]
		}
		s.Store.NotificationAttempt(r.Context(), p.AccountID, value.ID, err)
		if err != nil {
			writeError(w, 502, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, 404, fmt.Errorf("notification destination not found"))
}

func (s *Server) notify(ctx context.Context, run v1.Run, event string, state v1.JobState, message, questionID string) {
	if s.Deliver != nil {
		s.Deliver(ctx, run, event, state, message, questionID)
		return
	}
	go func() {
		deliveryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		s.deliverNotifications(deliveryCtx, run, event, state, message, questionID)
	}()
}

func (s *Server) deliverNotifications(ctx context.Context, run v1.Run, event string, state v1.JobState, message, questionID string) {
	values, err := s.Store.ListNotifications(ctx, run.AccountID, true)
	if err != nil {
		s.Logger.Error("load notification destinations", "account", run.AccountID, "error", err)
		return
	}
	if event == "output" {
		message = ""
	}
	for _, value := range values {
		if !value.Enabled {
			continue
		}
		adapter, err := s.notificationAdapter(value)
		if err == nil {
			errors := (notifications.Dispatcher{Adapters: []notifications.Adapter{adapter}}).Send(ctx, notifications.Delivery{AccountID: run.AccountID, RunID: run.ID, Box: run.Request.Box, Event: event, State: state, Message: message, QuestionID: questionID})
			err = errors[adapter.Name()]
		}
		s.Store.NotificationAttempt(ctx, run.AccountID, value.ID, err)
		if err != nil {
			s.Logger.Error("notification delivery failed", "destination", value.ID, "run", run.ID, "error", err)
		}
	}
}

func maxTTLExpired(run v1.Run, now time.Time) bool {
	return run.Request.Lifecycle.MaxTTL > 0 && !run.CreatedAt.IsZero() && !now.Before(run.CreatedAt.Add(run.Request.Lifecycle.MaxTTL))
}

func (s *Server) notificationAdapter(value DecryptedNotification) (notifications.Adapter, error) {
	var secret, config map[string]any
	if err := json.Unmarshal(value.Secret, &secret); err != nil {
		return nil, err
	}
	if len(value.Config) > 0 {
		if err := json.Unmarshal(value.Config, &config); err != nil {
			return nil, err
		}
	}
	stringValue := func(values map[string]any, key string) string {
		value, _ := values[key].(string)
		return value
	}
	switch value.Kind {
	case "webhook":
		url := stringValue(secret, "url")
		if url == "" {
			return nil, fmt.Errorf("webhook URL is required")
		}
		return notifications.Webhook{URL: url, Secret: []byte(stringValue(secret, "signingSecret")), Client: s.HTTP}, nil
	case "discord":
		url := stringValue(secret, "webhookUrl")
		if url == "" {
			return nil, fmt.Errorf("Discord webhookUrl is required")
		}
		return notifications.Discord{WebhookURL: url, Client: s.HTTP}, nil
	default:
		return nil, fmt.Errorf("unsupported notification kind %q", value.Kind)
	}
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; script-src 'self'; style-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func immutableImage(providerName, image string) bool {
	if providerName == "incus" {
		return true
	}
	if strings.Contains(image, "@sha256:") {
		return true
	}
	if providerName != "docker" || !strings.HasPrefix(image, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(image, "sha256:"))
	return err == nil && len(decoded) == 32
}

func authorizationValue(header, scheme string) (string, bool) {
	prefix := scheme + " "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return value, value != ""
}
