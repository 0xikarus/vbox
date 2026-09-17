package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestToolCheckboxUsesSpaceAndEnter(t *testing.T) {
	a := New()
	a.In = strings.NewReader(" \r \t\t\t\r")
	a.Out = &bytes.Buffer{}
	a.Err = &bytes.Buffer{}
	a.IsTerminal = func() bool { return true }
	fields := toolFields(nil)
	if err := a.runFormButton(context.Background(), "Tools", "Save", fields, func(func(string)) error {
		if got := selectedTools(fields); len(got) != 1 || got[0] != "foundry" {
			t.Fatalf("selection %v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
