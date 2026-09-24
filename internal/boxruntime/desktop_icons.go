package boxruntime

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
)

//go:embed desktop-icons.sh
var desktopIconsScript string

func ensureDesktopIcons(ctx context.Context, assignment string) error {
	// Older workers keep their working desktop until packages are enabled again.
	if _, err := exec.LookPath("pcmanfm"); err != nil {
		return nil
	}
	command := "new-window -d -t vmbox-desktop -n icons " + shellQuote("sh -c "+shellQuote(desktopIconsScript))
	_, err := tmuxOutput(ctx, "if-shell", "-F", "#{==:#{@vmbox_assignment},"+assignment+"}", command, "display-message 'assignment changed'")
	if err != nil {
		return fmt.Errorf("start desktop icons: %w", err)
	}
	command = "new-window -d -t vmbox-desktop -n folders " + shellQuote("vmbox-runtime desktop-folders "+shellQuote(assignment))
	_, err = tmuxOutput(ctx, "if-shell", "-F", "#{==:#{@vmbox_assignment},"+assignment+"}", command, "display-message 'assignment changed'")
	if err != nil {
		return fmt.Errorf("start workspace folder icons: %w", err)
	}
	return nil
}
