package taskflow

import "testing"

func TestFailedProcessEvidenceIsNotMissingResult(t *testing.T) {
	code := 23
	for _, observation := range []Observation{
		{Finished: true, ExitCode: &code, Failure: "agent failed"},
		{Finished: true, Signal: 15, Failure: "agent terminated"},
	} {
		d := document{Revision: 1, AttemptRevisions: map[string]int{"a": 1}, Workflow: Workflow{State: "planning", Attempts: []Attempt{{ID: "a", Stage: "plan", State: "running"}}}}
		if err := acceptObservation(&d, "a", observation); err != nil {
			t.Fatal(err)
		}
		if d.Workflow.Attempts[0].State != "exited" {
			t.Fatal("actual process failure lost")
		}
		if err := retry(&d, "a"); err != nil {
			t.Fatalf("explicit retry rejected: %v", err)
		}
	}
}
