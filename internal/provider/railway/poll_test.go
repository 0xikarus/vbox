package railway

import (
	"testing"
	"time"
)

func TestDeploymentPollingBacksOffAndResetsOnProgress(t *testing.T) {
	poll := deploymentPoll{base: 2 * time.Second}
	for _, step := range []struct {
		status  string
		seconds int
	}{
		{"BUILDING", 2}, {"BUILDING", 4}, {"BUILDING", 8}, {"BUILDING", 10}, {"BUILDING", 10},
		{"DEPLOYING", 2}, {"DEPLOYING", 4},
	} {
		if got := poll.next(step.status); got != time.Duration(step.seconds)*time.Second {
			t.Fatalf("status %s: delay %s, want %ds", step.status, got, step.seconds)
		}
	}
}

func TestDeploymentPollingPreservesLongConfiguredInterval(t *testing.T) {
	poll := deploymentPoll{base: 30 * time.Second}
	for range 5 {
		if got := poll.next("BUILDING"); got != 30*time.Second {
			t.Fatalf("delay %s", got)
		}
	}
}

func TestVolumePollingBacksOffAndPreservesLongConfiguredInterval(t *testing.T) {
	poll := volumePoll{base: 2 * time.Second}
	for _, want := range []time.Duration{2, 4, 8, 16, 20, 20} {
		if got := poll.next(); got != want*time.Second {
			t.Fatalf("delay %s, want %s", got, want*time.Second)
		}
	}
	poll = volumePoll{base: 30 * time.Second}
	for range 3 {
		if got := poll.next(); got != 30*time.Second {
			t.Fatalf("configured interval shortened to %s", got)
		}
	}
}
