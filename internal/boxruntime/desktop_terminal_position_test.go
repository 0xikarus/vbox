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
	if !strings.Contains(string(source), `"-geometry", "100x28-24+24"`) {
		t.Fatal("managed xterm must anchor 24 pixels from the desktop's right and top edges")
	}
}
