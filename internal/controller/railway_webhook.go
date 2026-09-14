package controller

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	railwayWebhookBodyLimit = 64 << 10
	railwayWebhookRateLimit = 120
)

// RailwayWebhookReceiver accepts only provider hints. It never changes slot,
// deployment, volume, health, capacity, or logical-box state.
type RailwayWebhookReceiver struct {
	Store      *Store
	secretHash [32]byte
	owner      string
	now        func() time.Time
	mu         sync.Mutex
	window     time.Time
	received   int
	limit      int
}

// NewRailwayWebhookReceiver requires 32 bytes of URL-safe random secret. Railway
// does not sign these payloads, so the secret is the receiver authentication.
func NewRailwayWebhookReceiver(store *Store, secret string) (*RailwayWebhookReceiver, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(decoded) != 32 || store == nil {
		return nil, errors.New("Railway webhook requires a 32-byte URL-safe secret")
	}
	owner, err := secretToken()
	if err != nil {
		return nil, errors.New("Railway webhook receiver identity unavailable")
	}
	return &RailwayWebhookReceiver{Store: store, secretHash: sha256.Sum256([]byte(secret)), owner: owner, now: time.Now, limit: railwayWebhookRateLimit}, nil
}

func (h *RailwayWebhookReceiver) authenticated(candidate string) bool {
	digest := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(digest[:], h.secretHash[:]) == 1
}

func (h *RailwayWebhookReceiver) allow() bool {
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.window.IsZero() || now.Sub(h.window) >= time.Minute || now.Before(h.window) {
		h.window, h.received = now, 0
	}
	if h.received >= h.limit {
		return false
	}
	h.received++
	return true
}

type railwayWebhookEvent struct {
	Type    string `json:"type"`
	Details struct {
		ID string `json:"id"`
	} `json:"details"`
	Resource struct {
		Project     railwayWebhookResource `json:"project"`
		Environment railwayWebhookResource `json:"environment"`
		Service     railwayWebhookResource `json:"service"`
		Deployment  railwayWebhookResource `json:"deployment"`
	} `json:"resource"`
	Timestamp string `json:"timestamp"`
}

type railwayWebhookResource struct {
	ID string `json:"id"`
}

func (e railwayWebhookEvent) validate() (time.Time, error) {
	validID := func(value string) bool { return value != "" && len(value) <= 128 }
	if !strings.HasPrefix(e.Type, "Deployment.") || len(e.Type) > 128 || !validID(e.Details.ID) ||
		!validID(e.Resource.Project.ID) || !validID(e.Resource.Environment.ID) ||
		!validID(e.Resource.Service.ID) || !validID(e.Resource.Deployment.ID) {
		return time.Time{}, errors.New("invalid Railway deployment event")
	}
	timestamp, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return time.Time{}, errors.New("invalid Railway event timestamp")
	}
	return timestamp, nil
}

func (e railwayWebhookEvent) digest() string {
	// Hash the parsed documented identity rather than raw JSON so reordered keys
	// and newly added non-identity fields remain the same retried delivery.
	identity, _ := json.Marshal([]string{e.Type, e.Details.ID, e.Resource.Project.ID, e.Resource.Environment.ID, e.Resource.Service.ID, e.Resource.Deployment.ID, e.Timestamp})
	sum := sha256.Sum256(identity)
	return hex.EncodeToString(sum[:])
}

func (h *RailwayWebhookReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !h.authenticated(r.PathValue("secret")) {
		// Do not reveal whether the endpoint or secret exists.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !h.allow() {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "webhook receiver busy", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, railwayWebhookBodyLimit)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid webhook payload", http.StatusBadRequest)
		return
	}
	var event railwayWebhookEvent
	if len(raw) == 0 || json.Unmarshal(raw, &event) != nil {
		http.Error(w, "invalid webhook payload", http.StatusBadRequest)
		return
	}
	timestamp, err := event.validate()
	if err != nil {
		// Non-deployment platform events are successfully ignored; Railway sends
		// volume and monitor alerts to the same project webhook.
		if event.Type != "" && !strings.HasPrefix(event.Type, "Deployment.") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "invalid webhook payload", http.StatusBadRequest)
		return
	}
	_, err = h.Store.EnqueueRailwayRefreshHint(r.Context(), event.digest(), event.Type, event.Resource.Project.ID, event.Resource.Environment.ID, event.Resource.Service.ID, event.Resource.Deployment.ID, timestamp)
	if err != nil {
		http.Error(w, "webhook hint unavailable", http.StatusServiceUnavailable)
		return
	}
	// Out-of-scope and duplicate events deliberately receive the same response.
	w.WriteHeader(http.StatusAccepted)
}

type RailwayRefreshHint struct {
	AccountID          string
	SlotID             string
	ProviderCredential string
	ProjectID          string
	EnvironmentID      string
	ServiceID          string
	DeploymentID       string
	EventType          string
	EventTimestamp     time.Time
	eventHashes        []string
	attempts           int
}

// EnqueueRailwayRefreshHint validates project, environment, and service against
// the configured Railway slot before persisting a hint. Deployment identity is
// intentionally not treated as authority: a change can precede inventory.
func (s *Store) EnqueueRailwayRefreshHint(ctx context.Context, deliveryHash, eventType, projectID, environmentID, serviceID, deploymentID string, eventTimestamp time.Time) (int, error) {
	if len(deliveryHash) != 64 || eventTimestamp.IsZero() {
		return 0, errors.New("invalid Railway refresh hint")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.account_id::text,c.id::text,c.provider_credential
 FROM compute_slots c JOIN provider_credentials p ON p.account_id=c.account_id AND p.provider='railway' AND p.name=c.provider_credential
 WHERE c.provider='railway' AND c.service_id=$1
 AND p.config->>'projectId'=$2 AND p.config->>'environmentId'=$3`, serviceID, projectID, environmentID)
	if err != nil {
		return 0, err
	}
	type target struct{ account, slot, credential string }
	var targets []target
	for rows.Next() {
		var target target
		if err := rows.Scan(&target.account, &target.slot, &target.credential); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	inserted := 0
	for _, target := range targets {
		targetDigest := sha256.Sum256([]byte(deliveryHash + "\x00" + target.slot))
		result, err := s.DB.ExecContext(ctx, `INSERT INTO railway_refresh_hints(event_hash,delivery_hash,account_id,slot_id,provider_credential,project_id,environment_id,service_id,deployment_id,event_type,event_timestamp)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(event_hash) DO NOTHING`, hex.EncodeToString(targetDigest[:]), deliveryHash, target.account, target.slot, target.credential, projectID, environmentID, serviceID, deploymentID, eventType, eventTimestamp)
		if err != nil {
			return inserted, err
		}
		if count, err := result.RowsAffected(); err == nil {
			inserted += int(count)
		}
	}
	return inserted, nil
}

// claimRailwayRefreshHints claims a bounded batch and coalesces events for the
// same target. The production rollout has one receiver loop per controller.
func (h *RailwayWebhookReceiver) claimRailwayRefreshHints(ctx context.Context, limit int) ([]RailwayRefreshHint, error) {
	if limit < 1 || limit > 256 {
		return nil, errors.New("invalid Railway hint claim limit")
	}
	rows, err := h.Store.DB.QueryContext(ctx, `WITH target AS (
 SELECT account_id,slot_id,provider_credential,project_id,environment_id,service_id FROM railway_refresh_hints
 WHERE (state='pending' AND next_attempt_at<=now()) OR (state='claimed' AND claim_expires_at<=now())
 ORDER BY received_at LIMIT 1 FOR UPDATE SKIP LOCKED
), picked AS (
 SELECT h.event_hash FROM railway_refresh_hints h JOIN target t
 ON h.account_id=t.account_id AND h.slot_id=t.slot_id AND h.provider_credential=t.provider_credential
 AND h.project_id=t.project_id AND h.environment_id=t.environment_id AND h.service_id=t.service_id
 WHERE (h.state='pending' AND h.next_attempt_at<=now()) OR (h.state='claimed' AND h.claim_expires_at<=now())
 ORDER BY h.received_at LIMIT $2 FOR UPDATE OF h SKIP LOCKED
), claimed AS (
 UPDATE railway_refresh_hints h SET state='claimed',claim_owner=$1,claim_expires_at=now()+interval '2 minutes',attempts=attempts+1,updated_at=now()
 FROM picked WHERE h.event_hash=picked.event_hash
 RETURNING h.event_hash,h.account_id::text,h.slot_id::text,h.provider_credential,h.project_id,h.environment_id,h.service_id,h.deployment_id,h.event_type,h.event_timestamp,h.attempts
) SELECT * FROM claimed`, h.owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byScope := make(map[string]int)
	var hints []RailwayRefreshHint
	for rows.Next() {
		var eventHash string
		var hint RailwayRefreshHint
		if err := rows.Scan(&eventHash, &hint.AccountID, &hint.SlotID, &hint.ProviderCredential, &hint.ProjectID, &hint.EnvironmentID, &hint.ServiceID, &hint.DeploymentID, &hint.EventType, &hint.EventTimestamp, &hint.attempts); err != nil {
			return nil, err
		}
		key := hint.AccountID + "\x00" + hint.SlotID + "\x00" + hint.ProviderCredential + "\x00" + hint.ProjectID + "\x00" + hint.EnvironmentID + "\x00" + hint.ServiceID
		if index, ok := byScope[key]; ok {
			hints[index].eventHashes = append(hints[index].eventHashes, eventHash)
			if hint.attempts > hints[index].attempts {
				hints[index].attempts = hint.attempts
			}
			continue
		}
		hint.eventHashes = []string{eventHash}
		byScope[key] = len(hints)
		hints = append(hints, hint)
	}
	return hints, rows.Err()
}

func (h *RailwayWebhookReceiver) renewRailwayRefreshHint(ctx context.Context, hint RailwayRefreshHint) error {
	for _, eventHash := range hint.eventHashes {
		result, err := h.Store.DB.ExecContext(ctx, `UPDATE railway_refresh_hints SET claim_expires_at=now()+interval '2 minutes',updated_at=now()
 WHERE event_hash=$1 AND state='claimed' AND claim_owner=$2 AND claim_expires_at>now()`, eventHash, h.owner)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return errors.New("Railway refresh hint ownership changed")
		}
	}
	return nil
}

func (h *RailwayWebhookReceiver) finishRailwayRefreshHint(ctx context.Context, hint RailwayRefreshHint, succeeded bool) error {
	state := "done"
	next := h.now()
	if !succeeded {
		state = "pending"
		base := time.Duration(1<<min(hint.attempts, 8)) * time.Second
		seed := sha256.Sum256([]byte(strings.Join(hint.eventHashes, "\x00") + "\x00" + strconv.Itoa(hint.attempts)))
		// Deterministic 0.75–1.25 jitter stays stable across controller restarts.
		delay := base * time.Duration(192+int(seed[0])/2) / 256
		next = next.Add(delay)
	}
	for _, eventHash := range hint.eventHashes {
		result, err := h.Store.DB.ExecContext(ctx, `UPDATE railway_refresh_hints SET state=$3,claim_owner=NULL,claim_expires_at=NULL,next_attempt_at=$4,updated_at=now()
 WHERE event_hash=$1 AND state='claimed' AND claim_owner=$2`, eventHash, h.owner, state, next)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return errors.New("Railway refresh hint ownership changed")
		}
	}
	return nil
}

func (h *RailwayWebhookReceiver) pruneRailwayRefreshHints(ctx context.Context) {
	// Old hints no longer improve a five-minute inventory reconciliation loop.
	// Retaining seven days covers delayed provider retries and bounds durable
	// dedupe storage even if a refresh target remains permanently unavailable.
	_, _ = h.Store.DB.ExecContext(ctx, `DELETE FROM railway_refresh_hints
 WHERE (state='done' AND updated_at<now()-interval '7 days')
 OR (state='pending' AND received_at<now()-interval '7 days')`)
}

// Run consumes durable hints through the supplied budget-aware refresh callback.
// Callback success means only that reconciliation ran; it does not establish
// readiness or authorize a lifecycle mutation.
func (h *RailwayWebhookReceiver) Run(ctx context.Context, refresh func(context.Context, RailwayRefreshHint) error) {
	if refresh == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(time.Hour)
	defer pruneTicker.Stop()
	h.pruneRailwayRefreshHints(ctx)
	for {
		hints, err := h.claimRailwayRefreshHints(ctx, 64)
		if err == nil {
			for _, hint := range hints {
				if h.renewRailwayRefreshHint(ctx, hint) != nil {
					continue
				}
				refreshCtx, cancel := context.WithTimeout(ctx, time.Minute)
				err := refresh(refreshCtx, hint)
				cancel()
				_ = h.finishRailwayRefreshHint(ctx, hint, err == nil)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-pruneTicker.C:
			h.pruneRailwayRefreshHints(ctx)
		case <-ticker.C:
		}
	}
}
