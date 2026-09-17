package controller

import (
	"strings"
	"testing"
)

// The worker already explains why a desktop did not come up. Reporting that
// reason is the difference between an actionable error and a wrong guess about
// missing packages.
func TestDesktopFailureDetailUsesTheLastWorkerLine(t *testing.T) {
	detail := desktopFailureDetail("starting desktop\nXtigervnc exited before the desktop was ready: Server is already active for display 99\n")
	if !strings.Contains(detail, "already active for display 99") {
		t.Fatalf("detail lost the worker reason: %q", detail)
	}
	if strings.ContainsAny(detail, "\n\r") {
		t.Fatalf("detail must be one line: %q", detail)
	}
}

func TestDesktopFailureDetailIsBoundedAndPrintable(t *testing.T) {
	if got := desktopFailureDetail("   \n\n  \n"); got != "" {
		t.Fatalf("blank stderr must yield no detail: %q", got)
	}
	if got := desktopFailureDetail("a\x07b\x00c"); got != "abc" {
		t.Fatalf("control characters survived: %q", got)
	}
	long := desktopFailureDetail(strings.Repeat("y", 900))
	if len([]rune(long)) > 301 {
		t.Fatalf("detail was not bounded: %d runes", len([]rune(long)))
	}
}

// A worker that cannot fork is not a worker missing desktop packages; the reader
// needs to be pointed at the process limit instead.
func TestDesktopExhaustionHintRecognisesPidPressure(t *testing.T) {
	for _, detail := range []string{
		"chromium: pthread_create: Resource temporarily unavailable",
		"fork failed: EAGAIN",
		"sh: cannot fork",
	} {
		if desktopExhaustionHint(detail) == "" {
			t.Fatalf("no hint for %q", detail)
		}
	}
	if hint := desktopExhaustionHint("desktop components unavailable; enable the desktop worker image first"); hint != "" {
		t.Fatalf("unrelated failure gained a process-limit hint: %q", hint)
	}
}

// The request budget has to leave room for the worker's own startup budget,
// otherwise a desktop that is still coming up is reported as broken.
func TestDesktopStartBudgetExceedsWorkerStartupBudget(t *testing.T) {
	// StartDesktop waits 15s for the VNC socket, may retire a stale session and
	// wait again, then sets up the panel, icons and one viewer per session.
	if desktopStartBudget <= 2*15_000_000_000 {
		t.Fatalf("desktop start budget %s cannot cover two socket waits", desktopStartBudget)
	}
	if desktopEnableBudget <= desktopStartBudget {
		t.Fatalf("package installation needs the longest budget, got %s", desktopEnableBudget)
	}
}
