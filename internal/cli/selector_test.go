package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Executed under a real PTY by tests/tui-pty.py, not a mocked key reader.
func TestTUIRealTerminalHelper(t *testing.T) {
	if os.Getenv("VMBOX_TUI_PTY_TEST") != "1" {
		t.Skip("PTY harness only")
	}
	a := New()
	if os.Getenv("VMBOX_TUI_PTY_TASK") == "1" {
		agent, err := a.promptTaskAgent(context.Background())
		fmt.Fprintf(a.Err, "AGENT=%s ERROR=%v\n", agent, err)
		return
	}
	labels := make([]string, 40)
	for i := range labels {
		labels[i] = fmt.Sprintf("session-%02d", i)
	}
	selected, err := a.selectTUI(context.Background(), "Real terminal sessions", labels, 0)
	fmt.Fprintf(a.Err, "RESULT=%d ERROR=%v\n", selected, err)
}

func TestTUISelectKeysAndRestore(t *testing.T) {
	for _, tt := range []struct {
		name, keys    string
		initial, want int
	}{{"down", "\x1b[B\r", 0, 1}, {"up", "\x1b[A\r", 1, 0}, {"wrap", "\x1b[A\r", 0, 2}, {"application-cursor", "\x1bOB\r", 0, 1}, {"home", "\x1b[H\r", 2, 0}, {"end", "\x1b[F\r", 0, 2}, {"default", "\r", 1, 1}} {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			in := strings.NewReader(tt.keys + "not-terminal-input")
			a.In = in
			a.Err = &bytes.Buffer{}
			got, err := a.selectTUI(context.Background(), "Sessions", []string{"first", "second", "third"}, tt.initial)
			if err != nil || got != tt.want {
				t.Fatal(got, err)
			}
			out := a.Err.(*bytes.Buffer).String()
			if !strings.HasPrefix(out, "\x1b[?1049h") || !strings.HasSuffix(out, "\x1b[0m\x1b[?25h\x1b[?1049l") {
				t.Fatal("alternate screen not restored")
			}
			if in.Len() != len("not-terminal-input") {
				t.Fatal("read past the selection keys")
			}
		})
	}
}

func TestTUICancelAndEscapeLabels(t *testing.T) {
	for _, key := range []string{"q", "\x03", "\x04", "\x1b"} {
		a := New()
		a.In = strings.NewReader(key)
		a.Err = &bytes.Buffer{}
		if _, err := a.selectTUI(context.Background(), "Sessions", []string{"x"}, 0); err == nil {
			t.Fatal("cancel accepted")
		}
		if !strings.Contains(a.Err.(*bytes.Buffer).String(), "\x1b[?1049l") {
			t.Fatal("cancel left alternate screen")
		}
	}
	if strings.ContainsAny(tuiLabel("evil\x1b[2J\nname", 60), "\x1b\n") {
		t.Fatal("untrusted control character rendered")
	}
	if got := tuiLabel("漢字abcdef", 5); got != "漢字…" {
		t.Fatal(got)
	}
}
