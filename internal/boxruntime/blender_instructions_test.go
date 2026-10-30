package boxruntime

import (
	"os"
	"strings"
	"testing"
)

func TestStandaloneBlenderGuidanceIsLinkedToEveryAgentAndIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := ApplyManagedInstructions(home, "# Owner instructions\nKeep my files.\n", nil, nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := EnsureStandaloneBlenderInstructions(home, "5.2.0"); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range AgentInstructionLinks(home) {
		data, err := os.ReadFile(link.Path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, "Keep my files.") || !strings.Contains(text, "Blender 5.2.0") || !strings.Contains(text, "Do not install") || !strings.Contains(text, "Blender MCP 1.9.1") || strings.Count(text, "## Installed Blender tools") != 1 {
			t.Fatal("missing or duplicated Blender instructions")
		}
	}
}
