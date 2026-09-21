package boxruntime

import (
	"reflect"
	"testing"
)

func TestCodexTurnInputUsesLocalImageSchema(t *testing.T) {
	got := codexTurnInput("inspect this", []string{"/tmp/first.png", "/tmp/second.jpg"})
	want := []map[string]any{
		{"type": "text", "text": "inspect this"},
		{"type": "localImage", "path": "/tmp/first.png"},
		{"type": "localImage", "path": "/tmp/second.jpg"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("turn input = %#v, want %#v", got, want)
	}
}
