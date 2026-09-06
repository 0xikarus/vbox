package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Invoked by tests/form-pty.py with a real terminal, not a byte-reader mock.
func TestFormRealTerminalHelper(t *testing.T) {
	if os.Getenv("VMBOX_FORM_PTY_TEST") != "1" {
		t.Skip("PTY harness only")
	}
	a := New()
	name := &formField{Label: "Name", Value: "test"}
	mode := &formField{Label: "After creation", Value: "Connect", Choices: []string{"Connect", "Hibernate"}}
	calls := 0
	err := a.runForm(context.Background(), "Creation form PTY", []*formField{name, mode}, func(progress func(string)) error {
		calls++
		progress("Preparing workspace")
		if calls == 1 {
			return fmt.Errorf("temporary failure; retry")
		}
		return nil
	})
	fmt.Fprintf(a.Err, "FORM name=%s mode=%s calls=%d error=%v\n", name.Value, mode.Value, calls, err)
}

func TestUnifiedFormOneScreenAndRetry(t *testing.T) {
	a := New()
	var out bytes.Buffer
	a.Err = &out
	a.IsTerminal = func() bool { return true }
	name := &formField{Label: "Name", Value: "test"}
	mode := &formField{Label: "After creation", Value: "Connect", Choices: []string{"Connect", "Hibernate"}}
	// Edit name, move to mode, choose inline, submit twice after a recoverable error.
	a.In = strings.NewReader("\r\x15Grüße\r\t\x1b[C\t\r\r")
	calls := 0
	err := a.runForm(context.Background(), "Create box", []*formField{name, mode}, func(progress func(string)) error {
		calls++
		if name.Value != "Grüße" || mode.Value != "Hibernate" {
			t.Fatal("form values changed")
		}
		progress("Initializing workspace")
		if calls == 1 {
			return fmt.Errorf("temporary failure; retry")
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	for _, status := range []string{"Initializing workspace", "temporary failure; retry"} {
		if !strings.Contains(out.String(), status+"\r\n"+strings.Repeat("─", 79)+"\r\n") {
			t.Fatal("missing divider below status", status)
		}
	}
	if strings.Count(out.String(), "\x1b[?1049h") != 1 || strings.Count(out.String(), "\x1b[?1049l") != 1 {
		t.Fatal("dialog changed screens")
	}
}

func TestUnifiedFormCancelDoesNotSubmit(t *testing.T) {
	a := New()
	a.Err = &bytes.Buffer{}
	a.In = strings.NewReader("\x03")
	a.IsTerminal = func() bool { return true }
	if a.runForm(context.Background(), "Create", nil, func(func(string)) error { t.Fatal("cancel submitted"); return nil }) == nil {
		t.Fatal("cancel accepted")
	}
}

func TestUnifiedFormPasteCannotSubmit(t *testing.T) {
	a := New()
	a.Err = &bytes.Buffer{}
	a.IsTerminal = func() bool { return true }
	a.In = strings.NewReader("\r\x1b[200~hello\r\nCreate\x1b[201~\x03")
	f := &formField{Label: "Command"}
	_ = a.runForm(context.Background(), "Create", []*formField{f}, func(func(string)) error { t.Fatal("paste submitted"); return nil })
	if f.Value != "hello  Create" {
		t.Fatalf("paste=%q", f.Value)
	}
}
