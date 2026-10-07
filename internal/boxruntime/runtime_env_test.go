package boxruntime

import (
	"path/filepath"
	"testing"
)

// isolateRuntimeEnv keeps a test from inheriting the workspace layout of the
// box that runs it; a live box exports these paths and its own display.
func isolateRuntimeEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"VMBOX_WORKSPACE_ROOT", "VMBOX_RUNTIME_DIR", "VMBOX_DESKTOP_DISPLAY"} {
		t.Setenv(name, "")
	}
}

// withoutImageBlender hides a Blender bundled in the host image from the test.
func withoutImageBlender(t *testing.T) {
	t.Helper()
	blender, server := pinnedBlenderImage, pinnedBlenderMCPImage
	missing := t.TempDir()
	pinnedBlenderImage, pinnedBlenderMCPImage = filepath.Join(missing, "blender"), filepath.Join(missing, "blender-mcp")
	t.Cleanup(func() { pinnedBlenderImage, pinnedBlenderMCPImage = blender, server })
}
