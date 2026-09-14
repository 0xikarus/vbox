package controller

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopControlRejectsUnrelatedActionsBeforeAccessingWorker(t *testing.T) {
	s := NewServer(nil, nil)
	for _, body := range []string{`{"action":"type","text":"command"}`, `{"action":"shutdown"}`, `{"action":"pause"} {}`, strings.Repeat("x", 1025)} {
		r := httptest.NewRequest("POST", "/v1/logical-boxes/box/desktop/control", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.desktopControl(w, r, Principal{})
		if w.Code != 400 {
			t.Fatalf("invalid control request returned %d", w.Code)
		}
	}
}
