package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHelpIsOfflineAndShortByDefault(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"help", "--all"}} {
		var out bytes.Buffer
		a := New()
		a.ConfigPath = "/nonexistent/vmbox-test/config"
		a.Out = &out
		a.IsTerminal = func() bool { return false }
		if err := a.Run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "notifications list") != (len(args) == 2) {
			t.Fatal("advanced commands not confined to full help")
		}
		for _, obsolete := range []string{"vmbox menu", "delete-volume", "vmbox close"} {
			if strings.Contains(out.String(), obsolete) {
				t.Fatalf("obsolete help: %s", obsolete)
			}
		}
	}
}
