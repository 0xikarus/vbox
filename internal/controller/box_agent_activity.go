package controller

import (
	"context"
	"database/sql"
	"errors"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// agentActivitySignals is the raw controller state a single box's agent
// activity is derived from. It deliberately reuses the same sources the chat UI
// and the Go compact handler already trust: box_tasks.agent_busy* and the
// stored mascot phrase/observation, plus the last agent reply, the last inbound
// message and its delivery state, and whether a chat_ask is still open.
type agentActivitySignals struct {
	HasTask          bool
	Agent            string
	Busy             *bool
	BusyAt           *time.Time
	Streaming        bool
	Phrase           string
	PhraseAt         *time.Time
	MascotActivity   string
	MascotObservedAt *time.Time
	LastAgentAt      *time.Time
	LastInboundAt    *time.Time
	Unanswered       int
	LastDelivery     string
	PendingQuestion  bool
}

// The chat UI treats "no output" as a stall after ten minutes of inactivity; the
// same threshold keeps the MCP view and the transcript hint in agreement.
const agentStallInactiveWindow = 10 * time.Minute

// The mascot classifier observation is only trusted briefly, matching the chat
// UI's freshness window.
const agentObservationFreshWindow = 40 * time.Second

// An unknown busy flag falls back to the UI heuristic: an unanswered prompt
// delivered in the last ten minutes is treated as in flight.
const agentInferredBusyWindow = 10 * time.Minute

func isManagedAgent(agent string) bool {
	switch agent {
	case "claude", "codex", "opencode":
		return true
	default:
		return false
	}
}

// BoxAgentActivity loads the signals for one box and derives its activity.
func (s *Store) BoxAgentActivity(ctx context.Context, accountID string, state v1.LogicalBoxState, boxID string) (v1.AgentActivity, error) {
	signals, err := s.BoxAgentActivitySignals(ctx, accountID, boxID)
	if err != nil {
		return v1.AgentActivity{}, err
	}
	return deriveAgentActivity(time.Now(), state, signals), nil
}

func (s *Store) BoxAgentActivitySignals(ctx context.Context, accountID, boxID string) (agentActivitySignals, error) {
	var signals agentActivitySignals
	var busy sql.NullBool
	var busyAt, phraseAt, observedAt, lastAgentAt, lastInboundAt sql.NullTime
	var phrase, mascotActivity, lastDelivery sql.NullString
	var unanswered sql.NullInt64
	var streaming, pendingQuestion sql.NullBool
	err := s.DB.QueryRowContext(ctx, `SELECT
		t.agent, t.agent_busy, t.agent_busy_updated_at, t.mascot_phrase, t.mascot_phrase_at, t.mascot_activity, t.mascot_observed_at,
		EXISTS(SELECT 1 FROM box_messages sm WHERE sm.account_id=$1 AND sm.task_id=t.id AND sm.state='streaming'),
		(SELECT max(m.created_at) FROM box_messages m WHERE m.account_id=$1 AND m.task_id=t.id AND m.direction='agent'),
		(SELECT max(m.created_at) FROM box_messages m WHERE m.account_id=$1 AND m.task_id=t.id AND m.direction IN ('user','box') AND m.submit),
		(SELECT count(*) FROM box_messages m WHERE m.account_id=$1 AND m.task_id=t.id AND m.direction IN ('user','box') AND m.submit AND m.state IN ('delivered','read')
			AND NOT EXISTS (SELECT 1 FROM box_messages r WHERE r.account_id=m.account_id AND r.idempotency_key='agent-reply:'||m.id::text AND r.state='delivered')),
		(SELECT m.state FROM box_messages m WHERE m.account_id=$1 AND m.task_id=t.id AND m.direction IN ('user','box') AND m.submit ORDER BY m.created_at DESC,m.id DESC LIMIT 1),
		EXISTS(SELECT 1 FROM box_messages q WHERE q.account_id=$1 AND q.task_id=t.id AND q.direction='agent' AND q.body LIKE 'vmbox-question:%'
			AND NOT EXISTS (SELECT 1 FROM box_messages a WHERE a.account_id=$1 AND a.task_id=t.id AND a.direction IN ('user','box') AND a.created_at>q.created_at))
		FROM box_tasks t
		WHERE t.account_id=$1 AND t.logical_box_id=$2 AND t.state='active' AND t.agent<>'shell'
		ORDER BY t.created_at DESC,t.id DESC LIMIT 1`, accountID, boxID).
		Scan(&signals.Agent, &busy, &busyAt, &phrase, &phraseAt, &mascotActivity, &observedAt, &streaming, &lastAgentAt, &lastInboundAt, &unanswered, &lastDelivery, &pendingQuestion)
	if errors.Is(err, sql.ErrNoRows) {
		return agentActivitySignals{}, nil
	}
	if err != nil {
		return agentActivitySignals{}, err
	}
	signals.HasTask = true
	if busy.Valid {
		value := busy.Bool
		signals.Busy = &value
	}
	signals.BusyAt = timePtr(busyAt)
	signals.Streaming = streaming.Valid && streaming.Bool
	signals.Phrase = phrase.String
	signals.PhraseAt = timePtr(phraseAt)
	signals.MascotActivity = mascotActivity.String
	signals.MascotObservedAt = timePtr(observedAt)
	signals.LastAgentAt = timePtr(lastAgentAt)
	signals.LastInboundAt = timePtr(lastInboundAt)
	if unanswered.Valid {
		signals.Unanswered = int(unanswered.Int64)
	}
	signals.LastDelivery = lastDelivery.String
	signals.PendingQuestion = pendingQuestion.Valid && pendingQuestion.Bool
	return signals, nil
}

func timePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	stamp := value.Time.UTC()
	return &stamp
}

func latestTime(values ...*time.Time) *time.Time {
	var latest *time.Time
	for _, value := range values {
		if value == nil {
			continue
		}
		if latest == nil || value.After(*latest) {
			candidate := *value
			latest = &candidate
		}
	}
	return latest
}

// deriveAgentActivity mirrors the chat UI's activityState plus its stall hint.
// A running box with an active managed agent is working, stalled, waiting or
// idle; "waiting" means the agent itself is blocked on a human (an open chat_ask
// or the mascot classifier), while an idle agent with unanswered inbound
// messages has dropped them and stays idle with Unanswered > 0.
func deriveAgentActivity(now time.Time, state v1.LogicalBoxState, signals agentActivitySignals) v1.AgentActivity {
	baseOutput := latestTime(signals.LastAgentAt, signals.LastInboundAt, signals.PhraseAt)
	activity := v1.AgentActivity{
		Phrase:             signals.Phrase,
		LastAgentMessageAt: signals.LastAgentAt,
		LastInboundAt:      signals.LastInboundAt,
		LastOutputAt:       baseOutput,
		Unanswered:         signals.Unanswered,
		LastDelivery:       deliveryState(signals.LastDelivery),
	}
	switch {
	case state == v1.LogicalBoxHibernated:
		activity.State = v1.AgentActivityHibernated
		return activity
	case state != v1.LogicalBoxRunning:
		activity.State = v1.AgentActivityStopped
		return activity
	}
	if !signals.HasTask || !isManagedAgent(signals.Agent) {
		activity.State = v1.AgentActivityIdle
		activity.Since = baseOutput
		return activity
	}
	freshObservation := signals.MascotObservedAt != nil && !signals.MascotObservedAt.Before(now.Add(-agentObservationFreshWindow))
	// The same evidence the chat stall hint uses: the busiest start, agent and
	// inbound messages, the stored phrase, a fresh mascot observation, or now
	// while the agent is streaming.
	busyStart := latestTime(signals.BusyAt, signals.LastInboundAt)
	lastOutput := latestTime(busyStart, baseOutput)
	if freshObservation {
		lastOutput = latestTime(lastOutput, signals.MascotObservedAt)
	}
	if signals.Streaming {
		lastOutput = &now
	}
	activity.LastOutputAt = lastOutput

	if freshObservation && signals.MascotActivity == "waiting" {
		activity.State = v1.AgentActivityWaiting
		activity.Since = signals.MascotObservedAt
		return activity
	}
	if signals.PendingQuestion {
		activity.State = v1.AgentActivityWaiting
		activity.Since = latestTime(signals.LastAgentAt, lastOutput)
		return activity
	}
	busy := false
	if signals.Busy != nil {
		busy = *signals.Busy
	} else if signals.Unanswered > 0 && signals.LastInboundAt != nil && now.Sub(*signals.LastInboundAt) < agentInferredBusyWindow &&
		(activity.LastDelivery == v1.DeliveryDelivered || activity.LastDelivery == v1.DeliveryRead) {
		busy = true
	}
	if busy {
		if busyStart == nil {
			activity.State = v1.AgentActivityWorking
			return activity
		}
		if lastOutput != nil && now.Sub(*lastOutput) >= agentStallInactiveWindow {
			activity.State = v1.AgentActivityStalled
			activity.Since = lastOutput
		} else {
			activity.State = v1.AgentActivityWorking
			activity.Since = busyStart
		}
		return activity
	}
	activity.State = v1.AgentActivityIdle
	activity.Since = latestTime(lastOutput, signals.LastAgentAt)
	return activity
}

func deliveryState(state string) string {
	switch state {
	case "queued", "delivering", "sent":
		return v1.DeliverySent
	case "delivered":
		return v1.DeliveryDelivered
	case "read":
		return v1.DeliveryRead
	case "failed":
		return v1.DeliveryFailed
	case "ambiguous":
		return v1.DeliveryUnconfirmed
	default:
		return state
	}
}
