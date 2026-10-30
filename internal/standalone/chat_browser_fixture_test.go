package standalone

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in browser fixture: an isolated store and fake runtime, never a worker.
func TestChatFirstBrowserFixture(t *testing.T) {
	dir := os.Getenv("VMBOX_STANDALONE_BROWSER_FIXTURE")
	if dir == "" {
		t.Skip("opt-in browser fixture")
	}
	s, _ := setup(t)
	s.state = "ready"
	if _, err := s.Store.Insert("fixture-agent", "agent", "Hello **from your agent**.\n\n`print(1)`", "answered"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	data, _ := json.Marshal(map[string]string{"url": server.URL})
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("browser fixture timed out")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
