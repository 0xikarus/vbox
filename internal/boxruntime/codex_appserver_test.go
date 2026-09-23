package boxruntime

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCodexCurrentThreadPrefersFirstTurnAfterClear(t *testing.T) {
	root := t.TempDir()
	if err := writeTextAtomic(codexResetPendingFile(root, "session"), "fresh-thread\n", 0600); err != nil {
		t.Fatal(err)
	}
	// No app-server query is needed while the freshly created thread is idle;
	// the older conversation may still appear more recently active in its list.
	id, err := CodexCurrentThread(context.Background(), nil, root, "session", "/workspace")
	if err != nil || id != "fresh-thread" {
		t.Fatalf("thread=%q err=%v", id, err)
	}
}

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

func TestCodexImageURLTurnInputSupportsLegacyAppServer(t *testing.T) {
	encoded := "R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs="
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pixel.gif")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := codexImageURLTurnInput("inspect this", []string{path})
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"type": "text", "text": "inspect this"},
		{"type": "image", "url": "data:image/gif;base64," + encoded},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("turn input = %#v, want %#v", got, want)
	}
}

func TestCodexImageURLFallbackMatchesOnlyLegacySchemaErrors(t *testing.T) {
	for _, message := range []string{"Invalid request: missing field `url`", "unknown variant `localImage`"} {
		if !codexNeedsImageURLFallback(errors.New(message)) {
			t.Fatalf("legacy schema error %q did not trigger fallback", message)
		}
	}
	if codexNeedsImageURLFallback(errors.New("turn is already running")) {
		t.Fatal("unrelated app-server error triggered image fallback")
	}
}

func TestCodexStartTurnRetriesLegacyImageSchemaOnce(t *testing.T) {
	encoded := "R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs="
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pixel.gif")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var calls [][]map[string]any
	err = codexStartTurnWithFallback("inspect this", []string{path}, func(input []map[string]any) error {
		calls = append(calls, input)
		if len(calls) == 1 {
			return errors.New("codex app server: Invalid request: missing field `url`")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("turn/start calls = %d, want 2", len(calls))
	}
	if calls[0][1]["type"] != "localImage" || calls[1][1]["type"] != "image" {
		t.Fatalf("turn/start inputs = %#v", calls)
	}
}

func TestCodexStartTurnDoesNotRetryUnrelatedError(t *testing.T) {
	calls := 0
	want := errors.New("turn is already running")
	err := codexStartTurnWithFallback("inspect this", []string{"/tmp/image.png"}, func([]map[string]any) error {
		calls++
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("error = %v, calls = %d; want original error and one call", err, calls)
	}
}

func TestCodexImageURLTurnInputRejectsUnreadableImage(t *testing.T) {
	_, err := codexImageURLTurnInput("inspect this", []string{filepath.Join(t.TempDir(), "missing.png")})
	if err == nil || !strings.Contains(err.Error(), "read Codex image fallback") {
		t.Fatalf("error = %v, want image read failure", err)
	}
}
