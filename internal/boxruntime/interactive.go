package boxruntime

import (
	"context"
	"fmt"
	"path/filepath"
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
			err = agentReadySettlePause(ctx)
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
		// its own, so chat and the box show the same thread. Approvals and Codex's
		// own sandbox are off because a box is already an externally sandboxed
		// machine and that sandbox needs namespaces the runtime refuses; OpenCode
		// gets the same from --auto.
		return []string{agent, "--remote", codexAppServerURL(session),
			"-c", "check_for_update_on_startup=false", "--dangerously-bypass-approvals-and-sandbox"}, nil
	case "opencode":
		return []string{agent, "--auto", "--hostname", "127.0.0.1", "--port", fmt.Sprintf("%d", OpenCodeChatPort(session))}, nil
	case "claude":
		return []string{"env", "DISABLE_AUTOUPDATER=1", "claude", "--add-dir", WorkloadHome() + "/.local/share/vmbox/chat", "--dangerously-load-development-channels", "server:vmbox-desktop"}, nil
	default:
		return nil, fmt.Errorf("unsupported persistent agent %q", agent)
	}
}
