package boxruntime

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
)

//go:embed desktop-panel.sh
var desktopPanelScript string

// Attach a panel to the live desktop. A process lock in the script makes this
// safe to repeat; existing VNC, Openbox and application processes stay intact.
func ensureDesktopPanel(ctx context.Context, assignment string) error {
	if _, err := exec.LookPath("tint2"); err != nil {
		return nil
	}
	command := "new-window -d -t vmbox-desktop -n panel " + shellQuote("sh -c "+shellQuote(desktopPanelScript))
	_, err := tmuxOutput(ctx, "if-shell", "-F", "#{==:#{@vmbox_assignment},"+assignment+"}", command, "display-message 'assignment changed'")
	if err != nil {
		return fmt.Errorf("start desktop panel: %w", err)
	}
	return nil
}
