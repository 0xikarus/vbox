package taskflow

import (
	"strings"
	"time"
)

func validText(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= max && !strings.ContainsRune(s, 0)
}
func validatePlan(p Plan, approval bool) error {
	if !validText(p.Summary, 30000) || len(p.Questions) > 20 {
		return invalid("invalid plan summary or questions")
	}
	for _, q := range p.Questions {
		if !validText(q, 10000) {
			return invalid("empty or oversized question")
		}
	}
	if approval && len(p.Questions) > 0 {
		return invalid("answer all plan questions first")
	}
	if len(p.Assignments) > 20 || ((approval || len(p.Questions) == 0) && len(p.Assignments) == 0) {
		return invalid("plan must contain 1–20 assignments")
	}
	ids := map[string]Assignment{}
	for _, a := range p.Assignments {
		if !validText(a.ID, 128) || !validText(a.Title, 1000) || !validText(a.Instruction, 30000) || len(a.AcceptanceCriteria) == 0 || len(a.AcceptanceCriteria) > 30 {
			return invalid("invalid assignment or acceptance criteria")
		}
		if _, ok := ids[a.ID]; ok {
			return invalid("duplicate assignment ID")
		}
		ids[a.ID] = a
		for _, c := range a.AcceptanceCriteria {
			if !validText(c, 10000) {
				return invalid("invalid acceptance criterion")
			}
		}
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return invalid("dependency cycle")
		}
		if done[id] {
			return nil
		}
		a, ok := ids[id]
		if !ok {
			return invalid("unknown dependency")
		}
		visiting[id] = true
		seen := map[string]bool{}
		for _, dep := range a.DependsOn {
			if seen[dep] {
				return invalid("duplicate dependency")
			}
			seen[dep] = true
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[id] = false
		done[id] = true
		return nil
	}
	for id := range ids {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
func addAttempt(d *document, stage, assignment string) {
	a := Attempt{ID: newID(), Stage: stage, AssignmentID: assignment, State: "queued"}
	d.Workflow.Attempts = append(d.Workflow.Attempts, a)
	d.AttemptRevisions[a.ID] = d.Revision
}
func terminal(a Attempt) bool { return a.State == "exited" || a.State == "result_missing" }
func successful(a Attempt) bool {
	return a.State == "exited" && a.ExitCode != nil && *a.ExitCode == 0 && a.Signal == 0 && a.Failure == ""
}
func active(a Attempt) bool {
	return a.State == "provisioning" || a.State == "submitted" || a.State == "running"
}
func latest(d *document, stage, id string) *Attempt {
	for i := len(d.Workflow.Attempts) - 1; i >= 0; i-- {
		a := &d.Workflow.Attempts[i]
		if d.AttemptRevisions[a.ID] == d.Revision && a.Stage == stage && a.AssignmentID == id {
			return a
		}
	}
	return nil
}
func approvedPlan(d *document) *Plan {
	for i := range d.Workflow.Plans {
		if d.Workflow.Plans[i].Revision == d.Workflow.ApprovedRevision {
			return &d.Workflow.Plans[i]
		}
	}
	return nil
}
func finalState(state string) bool {
	return state == "completed" || state == "needs_revision" || state == "failed" || state == "cancelled"
}

func reply(d *document, text string) error {
	w := &d.Workflow
	if w.State != "awaiting_reply" && w.State != "awaiting_approval" && !finalState(w.State) {
		return ErrConflict
	}
	w.Messages = append(w.Messages, Message{Role: "user", Text: text, CreatedAt: time.Now().UTC()})
	d.Revision++
	w.State = "planning_queued"
	w.Failure = ""
	w.ApprovedRevision = 0
	// Previous final text/verdict remain visible until replaced; each synthesis is
	// also an immutable coordinator message and terminal attempt.
	addAttempt(d, "plan", "")
	return nil
}
func approve(d *document, revision int) error {
	w := &d.Workflow
	if w.State != "awaiting_approval" || len(w.Plans) == 0 || revision != d.Revision {
		return ErrConflict
	}
	p := w.Plans[len(w.Plans)-1]
	if p.Revision != revision {
		return ErrConflict
	}
	if err := validatePlan(p, true); err != nil {
		return err
	}
	w.ApprovedRevision = revision
	w.State = "running"
	for _, a := range p.Assignments {
		addAttempt(d, "work", a.ID)
	}
	return nil
}
func cancel(d *document) error {
	if finalState(d.Workflow.State) {
		return ErrConflict
	}
	d.Workflow.State = "cancelling"
	advance(d)
	return nil
}
func retry(d *document, id string) error {
	w := &d.Workflow
	if w.State == "cancelling" || w.State == "cancelled" {
		return ErrConflict
	}
	var old *Attempt
	for i := range w.Attempts {
		if w.Attempts[i].ID == id {
			old = &w.Attempts[i]
			break
		}
	}
	if old == nil || d.AttemptRevisions[id] != d.Revision || old.State != "exited" || (old.ExitCode == nil && old.Signal == 0) || successful(*old) {
		return ErrConflict
	}
	if latest(d, old.Stage, old.AssignmentID).ID != id {
		return ErrConflict
	}
	// Never replace running work or a running synthesis with a retry.
	for _, a := range w.Attempts {
		if d.AttemptRevisions[a.ID] == d.Revision && (active(a) || (a.State == "queued" && a.Stage != "work")) {
			return ErrConflict
		}
	}
	stage, assignment := old.Stage, old.AssignmentID
	switch stage {
	case "plan":
		w.State = "planning_queued"
	case "work":
		w.State = "running"
	case "synthesize":
		w.State = "synthesizing"
	default:
		return ErrConflict
	}
	w.Failure = ""
	addAttempt(d, stage, assignment)
	return nil
}

func ready(d *document, a Attempt) bool {
	if a.State != "queued" || d.AttemptRevisions[a.ID] != d.Revision {
		return false
	}
	w := &d.Workflow
	if a.Stage == "plan" {
		return w.State == "planning_queued"
	}
	if a.Stage == "synthesize" {
		return w.State == "synthesizing"
	}
	if w.State != "running" || latest(d, "work", a.AssignmentID).ID != a.ID {
		return false
	}
	p := approvedPlan(d)
	if p == nil {
		return false
	}
	for _, assignment := range p.Assignments {
		if assignment.ID == a.AssignmentID {
			for _, dep := range assignment.DependsOn {
				v := latest(d, "work", dep)
				if v == nil || !successful(*v) {
					return false
				}
			}
			return true
		}
	}
	return false
}
func advance(d *document) {
	w := &d.Workflow
	if w.State == "cancelling" {
		for _, a := range w.Attempts {
			if active(a) {
				return
			}
		}
		w.State = "cancelled"
		return
	}
	if w.State != "running" {
		return
	}
	for _, a := range w.Attempts {
		if d.AttemptRevisions[a.ID] == d.Revision && (active(a) || ready(d, a)) {
			return
		}
	}
	// Blocked dependency assignments stay queued and are not invented failures.
	// The coordinator receives the full graph, real outputs and failed attempts.
	w.State = "synthesizing"
	addAttempt(d, "synthesize", "")
}

func acceptObservation(d *document, id string, o Observation) error {
	var a *Attempt
	for i := range d.Workflow.Attempts {
		if d.Workflow.Attempts[i].ID == id {
			a = &d.Workflow.Attempts[i]
			break
		}
	}
	if a == nil || !active(*a) {
		return ErrConflict
	}
	if !o.Finished {
		if o.State == "running" || o.State == "submitted" {
			a.State = o.State
		}
		return nil
	}
	if d.Results == nil {
		d.Results = map[string]Result{}
	}
	d.Results[id] = o.Result
	a.ExitCode = o.ExitCode
	a.Signal = o.Signal
	a.Output = o.Result.Text
	a.Failure = o.Failure
	a.State = "exited"
	if (o.ExitCode == nil && o.Signal == 0) || (strings.TrimSpace(o.Result.Text) == "" && o.Result.Plan == nil && o.Failure == "" && o.ExitCode != nil && *o.ExitCode == 0) {
		a.State = "result_missing"
		if a.Failure == "" {
			a.Failure = "terminal process result missing"
		}
	}
	if (o.ExitCode != nil && *o.ExitCode != 0) || o.Signal != 0 {
		if a.Failure == "" {
			a.Failure = "process exited unsuccessfully"
		}
	}
	w := &d.Workflow
	if w.State == "cancelling" {
		advance(d)
		return nil
	}
	switch a.Stage {
	case "plan":
		if successful(*a) {
			if o.Result.Plan == nil {
				a.Failure = "planner returned no plan"
			} else if err := validatePlan(*o.Result.Plan, false); err != nil {
				a.Failure = err.Error()
			} else {
				p := *o.Result.Plan
				p.Revision = d.Revision
				w.Plans = append(w.Plans, p)
				w.Messages = append(w.Messages, Message{Role: "coordinator", Text: p.Summary, CreatedAt: time.Now().UTC()})
				w.State = "awaiting_approval"
				if len(p.Questions) > 0 {
					w.State = "awaiting_reply"
				}
				return nil
			}
		}
		w.State = "failed"
		w.Failure = a.Failure
	case "synthesize":
		if successful(*a) && validText(o.Result.Text, 1000000) {
			switch o.Result.Verdict {
			case "accepted":
				w.State = "completed"
			case "needs_revision":
				w.State = "needs_revision"
			case "blocked":
				w.State = "failed"
			default:
				a.Failure = "coordinator returned no valid verdict"
				w.State = "failed"
			}
			if a.Failure == "" {
				w.Final = o.Result.Text
				w.Verdict = o.Result.Verdict
				w.Messages = append(w.Messages, Message{Role: "coordinator", Text: o.Result.Text, CreatedAt: time.Now().UTC()})
			}
		} else {
			w.State = "failed"
		}
		w.Failure = a.Failure
	case "work":
		advance(d)
	}
	return nil
}
