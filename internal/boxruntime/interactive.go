package boxruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func StartInteractive(ctx context.Context, root, session, agent string) error {
	return StartInteractiveCommand(ctx, root, session, agent, "")
}

func StartInteractiveCommand(ctx context.Context, root, session, agent, startCLI string) error {
	if !processID.MatchString(session) {
		return fmt.Errorf("invalid session name")
	}
	argv, err := interactiveArgv(session, agent)
	if err != nil {
		return err
	}
	if startCLI != "" {
		if agent != "shell" || len(startCLI) > 16384 {
			return fmt.Errorf("start-cli requires shell and a command up to 16 KiB")
		}
		argv = append(argv, "--start-cli", startCLI)
	}
	assignment, err := prepareManagedDesktop(ctx, agent)
	if err != nil {
		return err
	}
	if err := ensureAgentBackend(ctx, session, agent, assignment); err != nil {
		return err
	}
	if agent == "codex" {
		if err := markFreshCodexTUI(root, session); err != nil {
			return err
		}
	}
	args := []string{"new-session", "-d", "-s", session, "-c", filepath.Join(filepath.Dir(root), "workspace"), "--"}
	args = append(args, argv...)
	_, err = tmuxOutput(ctx, args...)
	if err == nil {
		_, err = tmuxOutput(ctx, "set-environment", "-t", session, taskAgentEnvironment, agent)
	}
	if err == nil && agent == "shell" {
		_, err = tmuxOutput(ctx, "set-option", "-t", "="+session+":", "@vmbox-shell", "1")
	}
	if err == nil {
		err = ApplyTmuxContext(ctx, root, session)
	}
	if err == nil && assignment != "" {
		err = EnsureDesktopTerminals(ctx, assignment)
	}
	if err == nil && agent != "shell" {
		err = waitForAgentReady(ctx, session, agent)
		if err == nil {
			err = settleAgentReadiness(ctx, session, agent)
		}
	}
	return err
}

func interactiveArgv(session, agent string) ([]string, error) {
	switch agent {
	case "codex", "claude", "opencode":
		return persistentAgentArgv(session, agent)
	case "shell":
		return []string{"vmbox-runtime", "welcome"}, nil
	default:
		return nil, fmt.Errorf("interactive agent must be codex, claude, opencode, or shell")
	}
}

// RestoreManagedAgent reconstructs the current managed launcher instead of
// replaying the bare executable saved by an older tmux snapshot. Backends and
// desktop registration must exist before the process replaces this launcher.
func RestoreManagedAgent(ctx context.Context, root, session, agent string) error {
	if !processID.MatchString(session) {
		return fmt.Errorf("invalid session name")
	}
	argv, err := persistentAgentArgv(session, agent)
	if err != nil {
		return err
	}
	assignment, err := prepareManagedDesktop(ctx, agent)
	if err != nil {
		return err
	}
	if err := ensureAgentBackend(ctx, session, agent, assignment); err != nil {
		return err
	}
	if agent == "codex" {
		if err := markFreshCodexTUI(root, session); err != nil {
			return err
		}
	}
	if _, err := tmuxOutput(ctx, "set-environment", "-t", session, taskAgentEnvironment, agent); err != nil {
		return err
	}
	if err := ApplyTmuxContext(ctx, root, session); err != nil {
		return err
	}
	_, _ = tmuxOutput(ctx, "source-file", "/etc/vmbox/tmux.conf")
	if assignment != "" {
		if err := EnsureDesktopTerminals(ctx, assignment); err != nil {
			return err
		}
	}
	binary, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(binary, argv, os.Environ())
}

// recoverInterruptedCodexSession repairs only the known controller-managed
// terminal left behind by an old restore. A user shell, even in a Codex-named
// session, is never replaced unless it contains the runtime's interruption
// notice and its foreground process is still a shell.
var RecoverInterruptedCodexSession = recoverInterruptedCodexSession

func recoverInterruptedCodexSession(ctx context.Context, root, session string) error {
	if !processID.MatchString(session) || !strings.HasPrefix(session, "codex-") {
		return nil
	}
	target := "=" + session + ":0.0"
	command, err := tmuxCommand(ctx, "", "display-message", "-p", "-t", target, "#{pane_current_command}")
	if err != nil {
		return err
	}
	current := strings.TrimSpace(string(command))
	if current != "bash" && current != "sh" && current != "zsh" && current != "fish" {
		return nil
	}
	content, err := tmuxCommand(ctx, "", "capture-pane", "-p", "-S", "-", "-t", target)
	if err != nil {
		return err
	}
	if !strings.Contains(string(content), "[vmbox] Interrupted:") {
		return nil
	}
	// The assignment has already been bound by the controller. The restored
	// launcher recreates the app server and the visible TUI in this same pane.
	launcher := shellJoin([]string{"vmbox-runtime", "agent-restore", session, "codex"})
	if _, err := tmuxCommand(ctx, "", "respawn-pane", "-k", "-t", target, "-c", WorkspaceDirectory(), launcher); err != nil {
		return fmt.Errorf("recover interrupted codex session: %w", err)
	}
	return nil
}

var RecoverCodexMCPStartup = recoverCodexMCPStartup
var codexDesktopMCPPolicy = desktopAgentToolPolicy

// A Codex TUI that started with a stale desktop-agent credential has no chat
// reply tool. Never accept a new turn in that state: wait for the controller
// to refresh the credential, then rebuild only the idle managed Codex pair.
func recoverCodexMCPStartup(ctx context.Context, session string) error {
	if !processID.MatchString(session) || !strings.HasPrefix(session, "codex-") {
		return nil
	}
	target := "=" + session + ":0.0"
	content, err := tmuxCommand(ctx, "", "capture-pane", "-p", "-t", target)
	if err != nil {
		return err
	}
	screen := string(content)
	if !strings.Contains(screen, "MCP startup issue") && !strings.Contains(screen, "MCP startup incomplete") {
		return nil
	}
	if !strings.Contains(screen, "Ask Codex") && !strings.Contains(screen, "q close") {
		return fmt.Errorf("codex desktop MCP startup failed while the session is busy")
	}
	fence, err := tmuxCommand(ctx, "", "show-option", "-gv", "@vmbox_assignment")
	if err != nil {
		return err
	}
	if _, err := codexDesktopMCPPolicy(ctx, strings.TrimSpace(string(fence))); err != nil {
		return fmt.Errorf("codex desktop MCP policy unavailable")
	}
	if err := RegisterDesktopMCP(ctx, os.Getenv("HOME"), "codex"); err != nil {
		return err
	}
	if _, err := tmuxCommand(ctx, "", "kill-session", "-t", "="+codexAppServerSession(session)); err != nil {
		return fmt.Errorf("stop stale codex app server: %w", err)
	}
	if err := EnsureCodexAppServer(ctx, session); err != nil {
		return err
	}
	launcher := shellJoin([]string{"vmbox-runtime", "agent-restore", session, "codex"})
	if _, err := tmuxCommand(ctx, "", "respawn-pane", "-k", "-t", target, "-c", WorkspaceDirectory(), launcher); err != nil {
		return fmt.Errorf("restart codex after MCP repair: %w", err)
	}
	if err := waitForAgentReady(ctx, session, "codex"); err != nil {
		return err
	}
	if err := settleAgentReadiness(ctx, session, "codex"); err != nil {
		return err
	}
	content, err = tmuxCommand(ctx, "", "capture-pane", "-p", "-t", target)
	if err != nil {
		return err
	}
	if strings.Contains(string(content), "MCP startup issue") || strings.Contains(string(content), "MCP startup incomplete") {
		return fmt.Errorf("codex desktop MCP startup failed after repair")
	}
	return nil
}

// ensureAgentBackend starts whatever an agent needs before its terminal can
// attach. Every box gets the HTTP tool façade so scripts can reach the desktop
// tools; OpenCode serves its own API in-process, and Codex needs its app server
// running first, because the terminal joins it with --remote.
func ensureAgentBackend(ctx context.Context, session, agent, assignment string) error {
	if err := EnsureDesktopMCPHTTP(ctx, assignment); err != nil {
		return err
	}
	if agent != "codex" {
		return nil
	}
	return EnsureCodexAppServer(ctx, session)
}

func persistentAgentArgv(session, agent string) ([]string, error) {
	switch agent {
	case "codex":
		// The terminal attaches to the session's app server rather than running
		// its own, so chat and the box show the same thread. Full access is set
		// in ~/.codex/config.toml; a CLI permission override prevents the remote
		// TUI from switching threads after Clear context.
		return []string{agent, "--remote", codexAppServerURL(session),
			"-c", "check_for_update_on_startup=false",
			"-c", "suppress_unstable_features_warning=true",
			"-c", "notice.hide_rate_limit_model_nudge=true"}, nil
	case "opencode":
		return []string{agent, "--auto", "--hostname", "127.0.0.1", "--port", fmt.Sprintf("%d", OpenCodeChatPort(session))}, nil
	case "claude":
		return []string{"env", "DISABLE_AUTOUPDATER=1", "claude", "--add-dir", WorkloadHome() + "/.local/share/vmbox/chat", "--dangerously-load-development-channels", "server:vmbox-desktop"}, nil
	default:
		return nil, fmt.Errorf("unsupported persistent agent %q", agent)
	}
}
