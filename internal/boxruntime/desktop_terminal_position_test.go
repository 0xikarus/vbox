package boxruntime

import (
	"os"
	"strings"
	"testing"
)

func TestManagedDesktopTerminalStartsAtTopRight(t *testing.T) {
	source, err := os.ReadFile("desktop_terminal.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `"-geometry", "72x22-24+24"`) {
		t.Fatal("managed xterm must fit beside desktop icons and stay anchored 24 pixels from the right and top edges")
	}
}
