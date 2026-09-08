package taskflow

import "testing"

func TestNextActiveRotatesWithoutReorderingHistory(t *testing.T) {
	d := document{Workflow: Workflow{Attempts: []Attempt{
		{ID: "a", State: "running"}, {ID: "done", State: "exited"}, {ID: "b", State: "submitted"},
	}}}
	for _, want := range []string{"a", "b", "a", "b"} {
		if got := nextActive(&d); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		d.LastPolled = want
	}
	if d.Workflow.Attempts[0].ID != "a" {
		t.Fatal("history reordered")
	}
	if nextActive(&document{}) != "" {
		t.Fatal("empty task has active attempt")
	}
}
