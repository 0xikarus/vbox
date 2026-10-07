package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func tp(value time.Time) *time.Time { return &value }

func TestDeriveAgentActivityStates(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	minutes := func(value int) *time.Time { return tp(now.Add(time.Duration(-value) * time.Minute)) }
	boolPtr := func(value bool) *bool { return &value }
	cases := []struct {
		name      string
		state     v1.LogicalBoxState
		signals   agentActivitySignals
		want      string
		wantSince *time.Time
	}{
		{name: "hibernated", state: v1.LogicalBoxHibernated, want: v1.AgentActivityHibernated},
		{name: "stopped", state: v1.LogicalBoxDetached, want: v1.AgentActivityStopped},
		{name: "no active task is idle", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: false}, want: v1.AgentActivityIdle},
		{name: "shell agent is idle", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "shell", Busy: boolPtr(true)}, want: v1.AgentActivityIdle},
		{name: "working", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "claude", Busy: boolPtr(true), BusyAt: minutes(2), LastAgentAt: minutes(1), Phrase: "Running tests"}, want: v1.AgentActivityWorking, wantSince: minutes(2)},
		{name: "working while streaming", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "claude", Busy: boolPtr(true), BusyAt: minutes(30), LastAgentAt: minutes(30), Streaming: true}, want: v1.AgentActivityWorking, wantSince: minutes(30)},
		{name: "stalled at ten minutes without output", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "codex", Busy: boolPtr(true), BusyAt: minutes(30), LastAgentAt: minutes(10)}, want: v1.AgentActivityStalled, wantSince: minutes(10)},
		{name: "stalled after twenty-five minutes", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "opencode", Busy: boolPtr(true), BusyAt: minutes(40), LastAgentAt: minutes(25)}, want: v1.AgentActivityStalled, wantSince: minutes(25)},
		{name: "idle managed agent", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "claude", Busy: boolPtr(false), LastAgentAt: minutes(180)}, want: v1.AgentActivityIdle, wantSince: minutes(180)},
		{name: "idle with an unanswered inbound is not waiting", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "claude", Busy: boolPtr(false), Unanswered: 1, LastInboundAt: minutes(5), LastDelivery: "delivered"}, want: v1.AgentActivityIdle, wantSince: minutes(5)},
		{name: "waiting on an open question", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "claude", Busy: boolPtr(false), PendingQuestion: true, LastAgentAt: minutes(3)}, want: v1.AgentActivityWaiting, wantSince: minutes(3)},
		{name: "waiting from the mascot classifier", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "claude", Busy: boolPtr(true), MascotActivity: "waiting", MascotObservedAt: minutes(0)}, want: v1.AgentActivityWaiting, wantSince: minutes(0)},
		{name: "stale classifier observation is ignored", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "claude", Busy: boolPtr(false), MascotActivity: "waiting", MascotObservedAt: minutes(5)}, want: v1.AgentActivityIdle},
		{name: "inferred busy for a legacy task", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "codex", Busy: nil, Unanswered: 1, LastInboundAt: minutes(1), LastDelivery: "delivered"}, want: v1.AgentActivityWorking, wantSince: minutes(1)},
		{name: "legacy idle task with an old unanswered prompt stays idle", state: v1.LogicalBoxRunning, signals: agentActivitySignals{HasTask: true, Agent: "codex", Busy: nil, Unanswered: 1, LastInboundAt: minutes(30), LastDelivery: "delivered"}, want: v1.AgentActivityIdle, wantSince: minutes(30)},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got := deriveAgentActivity(now, item.state, item.signals)
			if got.State != item.want {
				t.Fatalf("state=%q want %q", got.State, item.want)
			}
			if item.wantSince == nil {
				if got.Since != nil {
					t.Fatalf("since=%v want nil", got.Since)
				}
				return
			}
			if got.Since == nil || !got.Since.Equal(*item.wantSince) {
				t.Fatalf("since=%v want %v", got.Since, item.wantSince)
			}
		})
	}
}

func TestDeriveAgentActivityReportsPhraseAndDelivery(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	busy := true
	got := deriveAgentActivity(now, v1.LogicalBoxRunning, agentActivitySignals{
		HasTask: true, Agent: "claude", Busy: &busy,
		BusyAt: tp(now.Add(-2 * time.Minute)), LastAgentAt: tp(now.Add(-time.Minute)),
		LastInboundAt: tp(now.Add(-3 * time.Minute)), Unanswered: 2, LastDelivery: "ambiguous",
		Phrase: "Running tests",
	})
	if got.Phrase != "Running tests" || got.Unanswered != 2 || got.LastDelivery != v1.DeliveryUnconfirmed {
		t.Fatalf("activity=%+v", got)
	}
	if got.LastInboundAt == nil || got.LastAgentMessageAt == nil {
		t.Fatalf("missing message timestamps: %+v", got)
	}
}

func TestDeriveAgentActivityStallUsesLastOutput(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	busy := true
	got := deriveAgentActivity(now, v1.LogicalBoxRunning, agentActivitySignals{
		HasTask: true, Agent: "claude", Busy: &busy,
		BusyAt: tp(now.Add(-40 * time.Minute)), LastAgentAt: tp(now.Add(-25 * time.Minute)),
	})
	if got.State != v1.AgentActivityStalled {
		t.Fatalf("state=%q", got.State)
	}
	wantStopped := now.Add(-25 * time.Minute)
	if got.LastOutputAt == nil || !got.LastOutputAt.Equal(wantStopped) {
		t.Fatalf("lastOutputAt=%v want %v", got.LastOutputAt, wantStopped)
	}
	if got.Since == nil || !got.Since.Equal(wantStopped) {
		t.Fatalf("since=%v should equal when output stopped %v", got.Since, wantStopped)
	}
}

func TestDeliveryStateMapping(t *testing.T) {
	for raw, want := range map[string]string{
		"queued": v1.DeliverySent, "delivering": v1.DeliverySent, "sent": v1.DeliverySent,
		"delivered": v1.DeliveryDelivered, "read": v1.DeliveryRead, "failed": v1.DeliveryFailed,
		"ambiguous": v1.DeliveryUnconfirmed, "": "",
	} {
		if got := deliveryState(raw); got != want {
			t.Errorf("deliveryState(%q)=%q want %q", raw, got, want)
		}
	}
}

func TestBoxAgentActivitySignalsLoadsFromActiveTask(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	columns := []string{"agent", "agent_busy", "agent_busy_updated_at", "mascot_phrase", "mascot_phrase_at", "mascot_activity", "mascot_observed_at", "streaming", "last_agent_at", "last_inbound_at", "unanswered", "last_delivery", "pending_question"}
	mock.ExpectQuery(`SELECT\s+t.agent, t.agent_busy`).WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows(columns).AddRow("claude", true, now.Add(-2*time.Minute), "Running tests", now.Add(-time.Minute), "working", now.Add(-30*time.Second), false, now.Add(-time.Minute), now.Add(-3*time.Minute), 2, "delivered", false))
	signals, err := store.BoxAgentActivitySignals(context.Background(), "account-a", "box-a")
	if err != nil {
		t.Fatal(err)
	}
	if !signals.HasTask || signals.Agent != "claude" || signals.Busy == nil || !*signals.Busy {
		t.Fatalf("signals=%+v", signals)
	}
	if signals.Phrase != "Running tests" || signals.Unanswered != 2 || signals.LastDelivery != "delivered" || signals.PendingQuestion {
		t.Fatalf("signals=%+v", signals)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBoxAgentActivitySignalsWithoutActiveTask(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT\s+t.agent, t.agent_busy`).WithArgs("account-a", "box-a").WillReturnError(sql.ErrNoRows)
	signals, err := store.BoxAgentActivitySignals(context.Background(), "account-a", "box-a")
	if err != nil {
		t.Fatal(err)
	}
	if signals.HasTask {
		t.Fatalf("signals=%+v", signals)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentManagedBoxJSONIncludesActivity(t *testing.T) {
	box := safeAgentManagedBox(v1.LogicalBox{ID: "box-a", Name: "Builder", State: v1.LogicalBoxRunning, DefaultAgent: "claude"})
	since := time.Date(2026, 10, 1, 11, 48, 0, 0, time.UTC)
	output := time.Date(2026, 10, 1, 11, 57, 0, 0, time.UTC)
	box.Activity = &v1.AgentActivity{State: v1.AgentActivityWorking, Since: &since, Phrase: "Running tests", LastOutputAt: &output, Unanswered: 1, LastDelivery: v1.DeliveryDelivered}
	encoded, err := json.Marshal(box)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	activity, ok := payload["activity"].(map[string]any)
	if !ok {
		t.Fatalf("activity missing from %s", encoded)
	}
	if activity["state"] != "working" || activity["phrase"] != "Running tests" || activity["unanswered"] != float64(1) || activity["lastDelivery"] != "delivered" {
		t.Fatalf("activity=%v", activity)
	}
	if !strings.Contains(string(encoded), `"since":"2026-10-01T11:48:00Z"`) || !strings.Contains(string(encoded), `"lastOutputAt":"2026-10-01T11:57:00Z"`) {
		t.Fatalf("timestamps missing from %s", encoded)
	}
}
