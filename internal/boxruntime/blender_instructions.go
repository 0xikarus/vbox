package boxruntime

import (
	"fmt"
	"os"
	"strings"
)

// Shared by controller creation and standalone setup. The preset finishes
// installation before the agent starts; a disconnected bridge is not evidence
// that another Blender distribution should be downloaded.
func BlenderAgentGuidance() string { return blenderAgentGuidance(blenderVersion) }

func blenderAgentGuidance(version string) string {
	return fmt.Sprintf("- Blender: Blender %s and Blender MCP %s are already installed and configured. Use `blender` on PATH (`~/bin/blender` when present) and the existing MCP server named `blender` to create scenes and render. Do not install, download, downgrade, or replace Blender or Blender MCP. Check `blender --version` if needed. If the MCP connection is unavailable, reconnect/start the existing installed Blender and its enabled MCP add-on; report an installation failure instead of installing a different version.", version, blenderMCPVersion)
}

func EnsureStandaloneBlenderInstructions(home, version string) error {
	const start = "<!-- vbox:standalone-blender:start -->"
	const end = "<!-- vbox:standalone-blender:end -->"
	data, err := os.ReadFile(instructionCanonicalPath(home))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	markdown := string(data)
	if at := strings.Index(markdown, start); at >= 0 {
		last := strings.Index(markdown[at:], end)
		if last < 0 {
			return fmt.Errorf("incomplete managed Blender instruction block")
		}
		markdown = markdown[:at] + markdown[at+last+len(end):]
	}
	markdown = strings.TrimRight(markdown, "\n")
	if markdown != "" {
		markdown += "\n\n"
	}
	markdown += start + "\n## Installed Blender tools\n\n" + blenderAgentGuidance(version) + "\n" + end + "\n"
	_, err = ApplyManagedInstructions(home, markdown, nil, nil)
	return err
}
