package controller

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

//go:embed usage_probe.py
var usageProbeScript string

const profileUsageFreshness = 5 * time.Minute

type profileUsageWindow struct {
	Name            string   `json:"name"`
	Group           string   `json:"group,omitempty"`
	UsedPercent     *float64 `json:"usedPercent,omitempty"`
	ResetsAt        *string  `json:"resetsAt,omitempty"`
	DurationMinutes *int     `json:"durationMinutes,omitempty"`
	Scope           string   `json:"scope,omitempty"`
	Severity        string   `json:"severity,omitempty"`
}

type profileUsageBalance struct {
	Unit   string  `json:"unit"`
	Amount float64 `json:"amount"`
}

type profileUsageSpend struct {
	Currency  string   `json:"currency,omitempty"`
	Unit      string   `json:"unit,omitempty"`
	Limit     *float64 `json:"limit,omitempty"`
	Remaining *float64 `json:"remaining,omitempty"`
	Used      *float64 `json:"used,omitempty"`
	Period    string   `json:"period,omitempty"`
}

type profileUsageRateCap struct {
	Model  string  `json:"model"`
	Type   string  `json:"type"`
	Amount float64 `json:"amount"`
}

type profileUsageSnapshot struct {
	Windows  []profileUsageWindow  `json:"windows"`
	Balances []profileUsageBalance `json:"balances"`
	Spend    *profileUsageSpend    `json:"spend,omitempty"`
	RateCaps []profileUsageRateCap `json:"rateCaps"`
	Note     string                `json:"note,omitempty"`
}

type profileUsageCandidate struct {
	AccountID, Application, Name, Model             string
	BoxID, BoxName, Provider, Credential, ServiceID string
	Generation                                      int64
	Boxes                                           []string
}

func (c profileUsageCandidate) key() string {
	return c.AccountID + "\x00" + c.Application + "\x00" + c.Name
}

// Only a running box with an imported agent profile is eligible. In particular,
// this query never allocates, resumes, or contacts a sleeping box.
func (s *Server) liveProfileUsageCandidates(ctx context.Context, accountID string) ([]profileUsageCandidate, error) {
	query := `SELECT b.account_id::text,b.id::text,b.name,b.provider,b.provider_credential,b.default_agent,
COALESCE(b.metadata->'importedLoginProfiles',b.metadata->'loginProfiles','[]'::jsonb),
s.service_id,b.assignment_generation
FROM logical_boxes b JOIN compute_slots s ON s.id=b.slot_id AND s.account_id=b.account_id
WHERE b.state='running' AND s.service_id IS NOT NULL`
	args := []any{}
	if accountID != "" {
		query += " AND b.account_id=$1"
		args = append(args, accountID)
	}
	query += " ORDER BY b.account_id,b.name"
	rows, err := s.Store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grouped := map[string]*profileUsageCandidate{}
	order := []string{}
	for rows.Next() {
		var account, id, name, providerName, credential, agent, service string
		var generation int64
		var raw []byte
		if err := rows.Scan(&account, &id, &name, &providerName, &credential, &agent, &raw, &service, &generation); err != nil {
			return nil, err
		}
		var refs []v1.LoginProfileRef
		if json.Unmarshal(raw, &refs) != nil {
			continue
		}
		for _, ref := range refs {
			if ref.Application != agent || (agent != "claude" && agent != "codex" && agent != "opencode") || ref.Name == "" {
				continue
			}
			candidate := profileUsageCandidate{AccountID: account, Application: agent, Name: ref.Name, Model: ref.Model,
				BoxID: id, BoxName: name, Provider: providerName, Credential: credential, ServiceID: service, Generation: generation}
			key := candidate.key()
			if current := grouped[key]; current != nil {
				current.Boxes = append(current.Boxes, name)
			} else {
				candidate.Boxes = []string{name}
				grouped[key] = &candidate
				order = append(order, key)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]profileUsageCandidate, 0, len(order))
	for _, key := range order {
		result = append(result, *grouped[key])
	}
	return result, nil
}

func (s *Server) claimProfileUsage(ctx context.Context, candidate profileUsageCandidate, token string) (bool, error) {
	var claimed string
	err := s.Store.DB.QueryRowContext(ctx, `INSERT INTO profile_usage_snapshots(account_id,application,profile_name,claim_until,claim_token)
VALUES($1,$2,$3,now()+interval '90 seconds',$4)
ON CONFLICT(account_id,application,profile_name) DO UPDATE SET claim_until=EXCLUDED.claim_until,claim_token=EXCLUDED.claim_token
WHERE (profile_usage_snapshots.claim_until IS NULL OR profile_usage_snapshots.claim_until<now())
AND (profile_usage_snapshots.checked_at IS NULL OR profile_usage_snapshots.checked_at<now()-interval '5 minutes')
RETURNING claim_token`, candidate.AccountID, candidate.Application, candidate.Name, token).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Server) finishProfileUsage(ctx context.Context, candidate profileUsageCandidate, token string, snapshot *profileUsageSnapshot, detail string) error {
	var encoded any
	if snapshot != nil {
		value, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		encoded = string(value)
	}
	_, err := s.Store.DB.ExecContext(ctx, `UPDATE profile_usage_snapshots SET checked_at=now(),
observed_at=CASE WHEN $5::jsonb IS NULL THEN observed_at ELSE now() END,
snapshot=COALESCE($5::jsonb,snapshot),last_error=$6,claim_until=NULL,claim_token=NULL
WHERE account_id=$1 AND application=$2 AND profile_name=$3 AND claim_token=$4`,
		candidate.AccountID, candidate.Application, candidate.Name, token, encoded, detail)
	return err
}

func (s *Server) probeProfileUsage(ctx context.Context, candidate profileUsageCandidate) (profileUsageSnapshot, error) {
	var snapshot profileUsageSnapshot
	model := candidate.Model
	if candidate.Application == "opencode" && model == "" {
		profile, err := s.Store.LoadLoginProfile(ctx, Principal{AccountID: candidate.AccountID}, "opencode", candidate.Name)
		if err == nil {
			model = loginprofile.Model("opencode", profile.Files)
			for _, data := range profile.Files {
				clear(data)
			}
		}
	}
	providerName, modelID, _ := strings.Cut(model, "/")
	if candidate.Application == "opencode" && providerName != "" && providerName != "openrouter" && providerName != "venice" {
		return snapshot, fmt.Errorf("OpenCode model does not select OpenRouter or Venice")
	}
	prov, err := s.provider(ctx, candidate.AccountID, candidate.Provider, candidate.Credential)
	if err != nil {
		return snapshot, fmt.Errorf("worker connection unavailable")
	}
	argv := []string{"python3", "-c", usageProbeScript, candidate.Application, providerName, modelID}
	result, err := prov.Exec(ctx, candidate.ServiceID, argv, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		return snapshot, fmt.Errorf("worker usage probe unavailable")
	}
	if len(result.Stdout) > 1<<20 {
		return snapshot, fmt.Errorf("usage response too large")
	}
	var response struct {
		profileUsageSnapshot
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(result.Stdout), &response) != nil {
		return snapshot, fmt.Errorf("invalid usage response")
	}
	if response.Error != "" {
		return snapshot, fmt.Errorf("%s", response.Error)
	}
	return response.profileUsageSnapshot, nil
}

func (s *Server) pollProfileUsage(ctx context.Context, candidate profileUsageCandidate) {
	token := uuid()
	claimed, err := s.claimProfileUsage(ctx, candidate, token)
	if err != nil {
		s.Logger.Error("claim profile usage failed", "error", err)
		return
	}
	if !claimed {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	snapshot, probeErr := s.probeProfileUsage(probeCtx, candidate)
	cancel()
	detail := ""
	var output *profileUsageSnapshot
	if probeErr != nil {
		detail = probeErr.Error()
	} else {
		output = &snapshot
	}
	// A probe that crossed a box reassignment or credential replacement must not
	// publish a snapshot from the old workspace.
	var stillCurrent bool
	err = s.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM logical_boxes b JOIN compute_slots sl ON sl.id=b.slot_id
WHERE b.account_id=$1 AND b.id=$2 AND b.state='running' AND b.assignment_generation=$3
AND sl.service_id=$4 AND COALESCE(b.metadata->'importedLoginProfiles',b.metadata->'loginProfiles','[]'::jsonb)
@> jsonb_build_array(jsonb_build_object('application',$5::text,'name',$6::text)))`,
		candidate.AccountID, candidate.BoxID, candidate.Generation, candidate.ServiceID, candidate.Application, candidate.Name).Scan(&stillCurrent)
	if err != nil || !stillCurrent {
		output = nil
		detail = "Live box assignment or imported profile changed"
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if err := s.finishProfileUsage(finishCtx, candidate, token, output, detail); err != nil {
		s.Logger.Error("store profile usage failed", "error", err)
	}
}

func (s *Server) reconcileProfileUsageNow(ctx context.Context) error {
	candidates, err := s.liveProfileUsageCandidates(ctx, "")
	if err != nil {
		return err
	}
	semaphore := make(chan struct{}, 4)
	var group sync.WaitGroup
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			break
		}
		semaphore <- struct{}{}
		group.Add(1)
		go func(value profileUsageCandidate) {
			defer group.Done()
			defer func() { <-semaphore }()
			s.pollProfileUsage(ctx, value)
		}(candidate)
	}
	group.Wait()
	return ctx.Err()
}

func (s *Server) startProfileUsagePoller(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if err := s.reconcileProfileUsageNow(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("profile usage polling failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

type profileUsageOverviewRow struct {
	Application string                `json:"application"`
	Name        string                `json:"name"`
	Boxes       []string              `json:"boxes"`
	CheckedAt   *time.Time            `json:"checkedAt,omitempty"`
	ObservedAt  *time.Time            `json:"observedAt,omitempty"`
	Error       string                `json:"error,omitempty"`
	Snapshot    *profileUsageSnapshot `json:"snapshot,omitempty"`
}

func (s *Server) profileUsageOverview(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	candidates, err := s.liveProfileUsageCandidates(r.Context(), p.AccountID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("usage overview unavailable"))
		return
	}
	rows := make([]profileUsageOverviewRow, 0, len(candidates))
	for _, candidate := range candidates {
		row := profileUsageOverviewRow{Application: candidate.Application, Name: candidate.Name, Boxes: candidate.Boxes}
		var raw []byte
		err = s.Store.DB.QueryRowContext(r.Context(), `SELECT checked_at,observed_at,COALESCE(snapshot,'null'::jsonb),last_error
FROM profile_usage_snapshots WHERE account_id=$1 AND application=$2 AND profile_name=$3`, candidate.AccountID, candidate.Application, candidate.Name).
			Scan(&row.CheckedAt, &row.ObservedAt, &raw, &row.Error)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeError(w, 500, fmt.Errorf("usage overview unavailable"))
			return
		}
		if len(raw) > 0 && string(raw) != "null" {
			var snapshot profileUsageSnapshot
			if json.Unmarshal(raw, &snapshot) == nil {
				row.Snapshot = &snapshot
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Application != rows[j].Application {
			return rows[i].Application < rows[j].Application
		}
		return rows[i].Name < rows[j].Name
	})
	writeJSON(w, 200, map[string]any{"profiles": rows, "refreshSeconds": 60, "pollSeconds": int(profileUsageFreshness.Seconds())})
}
