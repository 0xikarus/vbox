package events

import (
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeRedactAndPersist(t *testing.T) {
	store, err := NewStore(t.TempDir(), []string{"secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	event, err := store.Append(v1.Event{Type: "output", Message: "\x1b[31mprogress\rsecret-token\x1b[0m"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(event.Message, "secret-token") || strings.Contains(event.Message, "\x1b") || !strings.Contains(event.Message, "[REDACTED]") {
		t.Fatalf("unsafe preview: %q", event.Message)
	}
	events, err := ReadAll(filepath.Join(store.dir, "events.ndjson"), 10)
	if err != nil || len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}
