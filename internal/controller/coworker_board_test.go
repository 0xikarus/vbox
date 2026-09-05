package controller

import (
	"testing"
	"time"
)

func TestCoworkerBoardEdits(t *testing.T) {
	b := CoworkerBoard{}
	now := time.Now()
	for _, e := range []CoworkerBoardEdit{
		{Revision: 0, Action: "create", TaskID: "task-1", Title: "Review implementation"},
		{Revision: 1, Action: "move", TaskID: "task-1", Status: "doing"},
		{Revision: 2, Action: "comment", TaskID: "task-1", Comment: "Tests running"},
	} {
		if err := applyCoworkerBoardEdit(&b, e, "coworker-a", now); err != nil {
			t.Fatal(err)
		}
	}
	if b.Revision != 3 || b.Tasks[0].Comments[0].Author != "coworker-a" || b.Tasks[0].Status != "doing" {
		t.Fatal("board edits missing")
	}
	if err := applyCoworkerBoardEdit(&b, CoworkerBoardEdit{Revision: 1, Action: "move", TaskID: "task-1", Status: "done"}, "coworker-b", now); err == nil {
		t.Fatal("stale edit accepted")
	}
	if b.Tasks[0].Status != "doing" {
		t.Fatal("stale edit mutated board")
	}
}
