package boxruntime

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func standaloneBlenderReady() bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:9876", 250*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// The GUI owns the MCP socket; the agent still performs all scene creation and
// rendering through its registered Blender MCP client.
func StartStandaloneBlender(ctx context.Context, home string) error {
	if _, err := os.Stat(filepath.Join(home, ".config/vmbox/blender-enabled")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if standaloneBlenderReady() {
		return nil
	}
	script := filepath.Join(home, ".config/vmbox/standalone-blender.py")
	source := `import bpy, addon_utils, requests
addon_utils.enable("blender_mcp", default_set=True, persistent=True)
assert addon_utils.check("blender_mcp")[1], "Blender MCP failed to enable"
import blender_mcp
server=blender_mcp.BlenderMCPServer(host="127.0.0.1", port=9876)
bpy.types.blendermcp_server=server
server.start()
bpy.context.scene.blendermcp_server_running=server.running
assert server.running, "Blender MCP socket did not start"
`
	if err := os.WriteFile(script, []byte(source), 0600); err != nil {
		return err
	}
	windows, err := tmuxOutput(ctx, "list-windows", "-t", "vmbox-desktop", "-F", "#{window_name}")
	if err != nil {
		return err
	}
	if !strings.Contains("\n"+string(windows), "\nblender-mcp\n") {
		threads := strconv.Itoa(max(1, min(16, runtime.GOMAXPROCS(0))))
		_, err = tmuxOutput(ctx, "new-window", "-d", "-t", "vmbox-desktop", "-n", "blender-mcp", "-c", WorkspaceDirectory(), "--", "env", "DISPLAY="+DesktopDisplay(), "LIBGL_ALWAYS_SOFTWARE=1", "OMP_NUM_THREADS="+threads, "OPENBLAS_NUM_THREADS="+threads, "blender", "-t", threads, "--python-exit-code", "1", "--python", script)
		if err != nil {
			return fmt.Errorf("start Blender MCP GUI: %w", err)
		}
	}
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	for !standaloneBlenderReady() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Blender MCP GUI did not become ready")
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}
