package boxruntime

import (
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestValidateContactRefAcceptsBoxIdentities(t *testing.T) {
	for _, value := range []string{"builder", "box-1", "team.box_2", "00000000-0000-4000-8000-000000000000"} {
		if err := validateContactRef(value); err != nil {
			t.Fatalf("validateContactRef(%q)=%v", value, err)
		}
	}
	for _, value := range []string{"", "with space", "slash/name", "quote\"name", "line\nbreak"} {
		if err := validateContactRef(value); err == nil {
			t.Fatalf("validateContactRef(%q) accepted an invalid contact", value)
		}
	}
}

func TestResolveContactAcceptsCompactIDNameOrIncomingFullID(t *testing.T) {
	contacts := []ContactSummary{{ID: "a1b2c3d4", Name: "CodeChecker", CanMessage: true}}
	for _, ref := range []string{"a1b2c3d4", "CodeChecker", "codechecker", "a1b2c3d4-1234-4000-8000-000000000000"} {
		name, err := resolveContactFromList(contacts, ref)
		if err != nil || name != "CodeChecker" {
			t.Fatalf("resolveContactFromList(%q)=(%q,%v)", ref, name, err)
		}
	}
	for _, ref := range []string{"unknown", "deadbeef-1234-4000-8000-000000000000", "a1b2c3d4-not-a-valid-uuid"} {
		if _, err := resolveContactFromList(contacts, ref); err == nil {
			t.Fatalf("unknown contact %q was accepted", ref)
		}
	}
	blocked := []ContactSummary{{ID: "a1b2c3d4", Name: "CodeChecker", CanMessage: false}}
	if _, err := resolveContactFromList(blocked, "a1b2c3d4-1234-4000-8000-000000000000"); err == nil {
		t.Fatal("the full ID bypassed a blocked contact")
	}
}

func TestResolveContactRejectsHibernatedBoxBeforeQueueing(t *testing.T) {
	contacts := []ContactSummary{{ID: "a1b2c3d4", Name: "mascot", State: "hibernated", CanMessage: true}}
	_, err := resolveContactFromList(contacts, "mascot")
	if err == nil || !strings.Contains(err.Error(), "wake it and retry") {
		t.Fatalf("hibernated contact error=%v", err)
	}
}

func TestDesktopContactLineShowsGroupWithoutBreakingRows(t *testing.T) {
	line := desktopContactLine(ContactSummary{ID: "a1b2c3d4", Name: "CodeChecker", Group: "Review\nTeam", Agent: "codex", State: "running", CanMessage: true})
	if line != `- id a1b2c3d4 | name CodeChecker | group "Review\nTeam" | agent codex | running | message true` {
		t.Fatalf("contact line=%q", line)
	}
	line = desktopContactLine(ContactSummary{ID: "deadbeef", Name: "Builder", CanMessage: true})
	if line != "- id deadbeef | name Builder | group none | agent  | unknown | message true" {
		t.Fatalf("ungrouped contact line=%q", line)
	}
}

func TestDesktopContactLineShowsCachedUsageWithObservationTime(t *testing.T) {
	remaining := 23.0
	observed := time.Date(2026, 10, 1, 4, 0, 0, 0, time.UTC)
	line := desktopContactLine(ContactSummary{ID: "abcd1234", Name: "Worker", Agent: "claude", State: "running", CanMessage: true, Usage: ContactUsage{Status: "available", RemainingPercent: &remaining, ObservedAt: &observed}})
	if !strings.Contains(line, "usage 23% left (as of 2026-10-01T04:00:00Z)") {
		t.Fatalf("contact line=%q", line)
	}
}

func TestDesktopContactLineShowsCompactAgentActivity(t *testing.T) {
	now := time.Now()
	stamp := func(age time.Duration) *time.Time { value := now.Add(-age); return &value }
	line := desktopContactLine(ContactSummary{ID: "a1b2c3d4", Name: "CodeChecker", Agent: "codex", State: "running", CanMessage: true,
		Activity: &v1.AgentActivity{State: v1.AgentActivityWorking, Since: stamp(12 * time.Minute), Phrase: "Running tests"}})
	if !strings.Contains(line, "agent working 12m · Running tests") {
		t.Fatalf("working contact line=%q", line)
	}
	line = desktopContactLine(ContactSummary{ID: "a1b2c3d4", Name: "Builder", Agent: "claude", State: "running", CanMessage: true,
		Activity: &v1.AgentActivity{State: v1.AgentActivityIdle, Since: stamp(3 * time.Hour)}})
	if !strings.Contains(line, "idle 3h") {
		t.Fatalf("idle contact line=%q", line)
	}
	line = desktopContactLine(ContactSummary{ID: "a1b2c3d4", Name: "Builder", Agent: "claude", State: "running", CanMessage: true,
		Activity: &v1.AgentActivity{State: v1.AgentActivityIdle, Since: stamp(5 * time.Minute), Unanswered: 1}})
	if !strings.Contains(line, "idle 5m · 1 unanswered message") {
		t.Fatalf("idle unanswered contact line=%q", line)
	}
	line = desktopContactLine(ContactSummary{ID: "a1b2c3d4", Name: "Builder", Agent: "claude", State: "running", CanMessage: true,
		Activity: &v1.AgentActivity{State: v1.AgentActivityWaiting, Unanswered: 1}})
	if !strings.Contains(line, "waiting for reply") || strings.Contains(line, "unanswered") {
		t.Fatalf("waiting contact line=%q", line)
	}
	line = desktopContactLine(ContactSummary{ID: "a1b2c3d4", Name: "Builder", Agent: "claude", State: "running", CanMessage: true,
		Activity: &v1.AgentActivity{State: v1.AgentActivityStalled, Since: stamp(40 * time.Minute), LastOutputAt: stamp(25 * time.Minute)}})
	if !strings.Contains(line, "stalled 25m (no output)") {
		t.Fatalf("stalled contact line=%q", line)
	}
	line = desktopContactLine(ContactSummary{ID: "a1b2c3d4", Name: "Builder", Agent: "claude", State: "hibernated", CanMessage: true,
		Activity: &v1.AgentActivity{State: v1.AgentActivityHibernated}})
	if !strings.Contains(line, "| hibernated |") {
		t.Fatalf("hibernated contact line=%q", line)
	}
}
