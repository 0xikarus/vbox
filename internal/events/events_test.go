package events

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
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

func TestEventSignatureCoversMutableFields(t *testing.T) {
	event := v1.Event{RunID: "run-1", Sequence: 7, Type: "state", State: v1.JobRunning, Stream: "stderr", Message: "working", Data: json.RawMessage(`{"phase":2}`), Timestamp: time.Unix(123, 456).UTC()}
	signature, err := Sign([]byte("job-scoped-key"), event)
	if err != nil {
		t.Fatal(err)
	}
	event.Signature = signature
	if !Verify([]byte("job-scoped-key"), event) {
		t.Fatal("valid signature rejected")
	}
	mutations := []func(*v1.Event){
		func(value *v1.Event) { value.ID = "tampered" },
		func(value *v1.Event) { value.State = v1.JobFailed },
		func(value *v1.Event) { value.Stream = "stdout" },
		func(value *v1.Event) { value.Message = "tampered" },
		func(value *v1.Event) { value.Data = json.RawMessage(`{"phase":3}`) },
	}
	for i, mutate := range mutations {
		changed := event
		mutate(&changed)
		if Verify([]byte("job-scoped-key"), changed) {
			t.Fatalf("mutation %d retained a valid signature", i)
		}
	}
}

func TestSanitizeTruncatesOnRuneBoundary(t *testing.T) {
	store, err := NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	value := store.Sanitize(strings.Repeat("€", 600))
	if !utf8.ValidString(value) || len([]rune(value)) != store.preview {
		t.Fatalf("invalid UTF-8 preview: runes=%d valid=%v", len([]rune(value)), utf8.ValidString(value))
	}
}
