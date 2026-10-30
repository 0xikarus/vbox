package hub

import (
	"strings"
	"testing"
)

func TestWorkspaceCanBeVisitedWhileQueuedWithoutStartingIt(t *testing.T) {
	s, cookie := authenticated(t)
	b, err := s.Manager.Create(Spec{Name: "Queued workspace", Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	route := "/boxes/" + b.ID + "/"
	if w := call(s, "GET", route, "", ""); w.Code != 303 {
		t.Fatal("workspace needs owner login")
	}
	w := call(s, "GET", route, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Queued workspace") || !strings.Contains(w.Body.String(), "Start box") {
		t.Fatal("queued workspace cannot be visited")
	}
	after, err := s.Manager.Store.Get(b.ID)
	if err != nil || after.State != b.State || after.Desired != b.Desired {
		t.Fatal("visiting the workspace changed lifecycle intent")
	}
}
