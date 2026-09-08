package factory

import "testing"

func validPlan() Plan {
	return Plan{Revision: 1, Markdown: "Build two independent features, then integrate", BaseSHA: "resolved-source",
		Features: []Feature{
			{ID: "a", Title: "A", AcceptanceCriteria: []string{"works"}, Checks: []Check{{Argv: []string{"go", "test", "./..."}, Cwd: "/data/workspace/repo", TimeoutSeconds: 300}}},
			{ID: "b", Title: "B", DependsOn: []string{"a"}, AcceptanceCriteria: []string{"works together"}, Checks: []Check{{Argv: []string{"go", "test", "./..."}, Cwd: "/data/workspace/repo", TimeoutSeconds: 300}}},
		}}
}

func TestPlanApprovalRequiresVerifiableDAG(t *testing.T) {
	if err := validPlan().ValidateApproval(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Plan){
		"cycle":                func(p *Plan) { p.Features[0].DependsOn = []string{"b"} },
		"missing prerequisite": func(p *Plan) { p.Features[1].DependsOn = []string{"missing"} },
		"duplicate ID":         func(p *Plan) { p.Features[1].ID = "a" },
		"unanswered question":  func(p *Plan) { p.Questions = []string{"What should we build?"} },
		"no tests":             func(p *Plan) { p.Features[0].Checks = nil },
		"unbounded check":      func(p *Plan) { p.Features[0].Checks[0].TimeoutSeconds = 0 },
		"no source":            func(p *Plan) { p.BaseSHA = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := validPlan()
			mutate(&p)
			if p.ValidateApproval() == nil {
				t.Fatal("unsafe approval accepted")
			}
		})
	}
}

func TestCreateWorkRejectsAmbiguousInput(t *testing.T) {
	r := CreateWork{RepositoryID: "123", Idea: "Implement profile management", Agent: "codex", Profile: "selected"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.AssetIDs = []string{"image", "image"}
	if r.Validate() == nil {
		t.Fatal("duplicate attachment accepted")
	}
	r.AssetIDs = nil
	r.Agent = "shell"
	if r.Validate() == nil {
		t.Fatal("shell accepted as planner")
	}
}
