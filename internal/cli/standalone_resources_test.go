package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestStandaloneMemoryUnitsAndInvalidLimits(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int64
	}{{"6g", 6144}, {"6GiB", 6144}, {"6144", 6144}, {"512m", 512}, {"1.5g", 1536}} {
		var m standaloneMemory
		if err := m.Set(tc.text); err != nil || int64(m) != tc.want {
			t.Errorf("%s -> %d, %v", tc.text, m, err)
		}
	}
	for _, s := range []string{"0", "-1g", "NaN", "Inf", "nonsense"} {
		var m standaloneMemory
		if m.Set(s) == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
func TestReadOnlyHostResourcesCannotBeEdited(t *testing.T) {
	a := New()
	a.Err = &bytes.Buffer{}
	a.In = strings.NewReader("\r\x15forged\x1b[C\t\r")
	a.IsTerminal = func() bool { return true }
	f := &formField{Label: "Host RAM", Value: "6 GiB", ReadOnly: true, Choices: []string{"6 GiB", "forged"}}
	if err := a.runForm(context.Background(), "Resources", []*formField{f}, func(func(string)) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if f.Value != "6 GiB" {
		t.Fatal("read-only resource measurement was edited")
	}
}
