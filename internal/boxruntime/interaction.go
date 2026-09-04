package boxruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

var ErrAmbiguousMessage = errors.New("message delivery is ambiguous and will not be replayed automatically")

const taskAgentEnvironment = "VMBOX_TASK_AGENT"

func validateTmuxToken(kind, value string) error {
	if value == "" || len(value) > 128 {
		return fmt.Errorf("%s is empty or too long", kind)
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return fmt.Errorf("%s contains unsupported character %q", kind, character)
	}
	return nil
}

var tmuxCommand = runTmuxCommand
var agentReadyPollInterval = 200 * time.Millisecond
var agentReadyTimeout = 20 * time.Second
var tmuxSubmitPause = waitBeforeTmuxSubmit
var agentReadySettlePause = waitForAgentSettle
var tmuxSubmitConfirmPause = waitBeforeTmuxSubmitConfirmation

func waitBeforeTmuxSubmit(ctx context.Context) error {
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitForAgentSettle(ctx context.Context) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitBeforeTmuxSubmitConfirmation(ctx context.Context) error {
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runTmuxCommand(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "tmux", args...)
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func DeliverTmuxInput(ctx context.Context, root, session, messageID, text string, submit bool) error {
	if err := validateTmuxToken("session", session); err != nil {
		return err
	}
	if err := validateTmuxToken("message ID", messageID); err != nil {
		return err
	}
	if text == "" || len(text) > 100_000 {
		return fmt.Errorf("message must contain between 1 and 100000 bytes")
	}
	directory := filepath.Join(root, "messages")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	pending := filepath.Join(directory, messageID+".pending")
	delivered := filepath.Join(directory, messageID+".delivered")
	if _, err := os.Stat(delivered); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return ErrAmbiguousMessage
	}
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(file, "%s\n", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	buffer := "vmbox_" + messageID
	defer func() { _, _ = tmuxCommand(context.Background(), "", "delete-buffer", "-b", buffer) }()
	if _, err := tmuxCommand(ctx, text, "load-buffer", "-b", buffer, "-"); err != nil {
		return ErrAmbiguousMessage
	}
	if _, err := tmuxCommand(ctx, "", "paste-buffer", "-d", "-b", buffer, "-t", session); err != nil {
		return ErrAmbiguousMessage
	}
	if submit {
		// Full-screen TUIs consume bracketed paste asynchronously. Sending Enter in
		// the same burst can leave the text visible but unsubmitted. Paste the
		// carriage return through the same byte path as controller terminal choices;
		// Claude can ignore tmux's symbolic Enter key during startup.
		if err := tmuxSubmitPause(ctx); err != nil {
			return ErrAmbiguousMessage
		}
		content, err := tmuxCommand(ctx, "", "capture-pane", "-p", "-J", "-t", session)
		if err != nil {
			return ErrAmbiguousMessage
		}
		if terminalBlocksSubmit(string(content)) {
			if err := os.Rename(pending, delivered); err != nil {
				return ErrAmbiguousMessage
			}
			return nil
		}
		if err := pasteTmuxCarriageReturn(ctx, buffer, session); err != nil {
			return ErrAmbiguousMessage
		}
		if err := confirmTmuxSubmit(ctx, buffer, session, text); err != nil {
			return ErrAmbiguousMessage
		}
	}
	if err := os.Rename(pending, delivered); err != nil {
		return ErrAmbiguousMessage
	}
	return nil
}

func pasteTmuxCarriageReturn(ctx context.Context, buffer, session string) error {
	if _, err := tmuxCommand(ctx, "\r", "load-buffer", "-b", buffer, "-"); err != nil {
		return err
	}
	_, err := tmuxCommand(ctx, "", "paste-buffer", "-d", "-b", buffer, "-t", session)
	return err
}

func confirmTmuxSubmit(ctx context.Context, buffer, session, text string) error {
	for attempt := 0; attempt < 20; attempt++ {
		if err := tmuxSubmitConfirmPause(ctx); err != nil {
			return err
		}
		content, err := tmuxCommand(ctx, "", "capture-pane", "-p", "-J", "-t", session)
		if err != nil {
			return err
		}
		if terminalBlocksSubmit(string(content)) || !tmuxInputStillStaged(string(content), text) {
			return nil
		}
		if err := pasteTmuxCarriageReturn(ctx, buffer, session); err != nil {
			return err
		}
	}
	return fmt.Errorf("task input remained staged after bounded submit retries")
}

func tmuxInputStillStaged(content, text string) bool {
	needle := strings.TrimSpace(strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")[0])
	if needle == "" {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(content, "\u00a0", " "), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		for _, marker := range []string{"❯", "›"} {
			if !strings.HasPrefix(line, marker) {
				continue
			}
			input := strings.TrimSpace(strings.TrimPrefix(line, marker))
			if input == "" {
				return false
			}
			return input == needle || len(input) >= 8 && strings.HasPrefix(needle, input)
		}
	}
	return false
}

func terminalBlocksSubmit(content string) bool {
	return strings.Contains(content, "Update available!") &&
		strings.Contains(content, "Skip until next version") &&
		strings.Contains(content, "Press enter to continue")
}

func StartTmuxTask(ctx context.Context, root, session, agent, messageID, prompt string) error {
	if err := validateTmuxToken("session", session); err != nil {
		return err
	}
	var argv []string
	switch agent {
	case "codex", "claude", "opencode":
		argv = []string{agent}
	case "shell":
		argv = []string{"/bin/bash", "-l"}
	default:
		return fmt.Errorf("unsupported task agent %q", agent)
	}
	if _, err := tmuxCommand(ctx, "", "has-session", "-t", session); err != nil {
		args := []string{"new-session", "-d", "-s", session, "-c", "/data/workspace", "--"}
		args = append(args, argv...)
		if _, err := tmuxCommand(ctx, "", args...); err != nil {
			return fmt.Errorf("start %s task session: %w", agent, err)
		}
		if _, err := tmuxCommand(ctx, "", "set-environment", "-t", session, taskAgentEnvironment, agent); err != nil {
			return fmt.Errorf("mark %s task session: %w", agent, err)
		}
		if err := ApplyTmuxContext(ctx, root, session); err != nil {
			return fmt.Errorf("apply %s task session context: %w", agent, err)
		}
		_, _ = tmuxCommand(ctx, "", "source-file", "/etc/vmbox/tmux.conf")
	} else {
		marker, err := tmuxCommand(ctx, "", "show-environment", "-t", session, taskAgentEnvironment)
		if err != nil || strings.TrimSpace(string(marker)) != taskAgentEnvironment+"="+agent {
			return fmt.Errorf("tmux session %q already exists and is not a %s task session; choose a different session name", session, agent)
		}
	}
	if agent != "shell" {
		if err := waitForAgentReady(ctx, session, agent); err != nil {
			return err
		}
		if err := agentReadySettlePause(ctx); err != nil {
			return err
		}
	}
	return DeliverTmuxInput(ctx, root, session, messageID, prompt, true)
}

func waitForAgentReady(ctx context.Context, session, agent string) error {
	deadline := time.NewTimer(agentReadyTimeout)
	defer deadline.Stop()
	for {
		content, err := tmuxCommand(ctx, "", "capture-pane", "-p", "-J", "-S", "-80", "-t", session)
		if err == nil {
			text := string(content)
			if agent == "claude" && strings.Contains(text, "Quick safety check:") && strings.Contains(text, "Yes, I trust this folder") && strings.Contains(text, "Enter to confirm") {
				if _, err := tmuxCommand(ctx, "", "send-keys", "-t", session, "Down", "Enter"); err != nil {
					return fmt.Errorf("accept trusted workspace in %s session: %w", agent, err)
				}
			} else if agentInputReady(agent, text) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%s task session did not reach an input-ready prompt", agent)
		case <-time.After(agentReadyPollInterval):
		}
	}
}

func agentInputReady(agent, content string) bool {
	switch agent {
	case "codex":
		return strings.Contains(content, "OpenAI Codex") && strings.Contains(content, "›")
	case "claude":
		return strings.Contains(content, "Claude Code v") && strings.Contains(content, "❯")
	case "opencode":
		return strings.Contains(strings.ToLower(content), "opencode")
	default:
		return false
	}
}

func CaptureTmuxScreen(ctx context.Context, session string, history int) (v1.TerminalSnapshot, error) {
	var snapshot v1.TerminalSnapshot
	if err := validateTmuxToken("session", session); err != nil {
		return snapshot, err
	}
	if history <= 0 {
		history = 200
	}
	if history > 2000 {
		history = 2000
	}
	format := strings.Join([]string{"#{pane_id}", "#{pane_title}", "#{pane_current_command}", "#{pane_width}", "#{pane_height}"}, "\x1f")
	metadata, err := tmuxOutput(ctx, "display-message", "-p", "-t", session, format)
	if err != nil {
		return snapshot, err
	}
	fields := strings.Split(strings.TrimSuffix(string(metadata), "\n"), "\x1f")
	if len(fields) != 5 {
		return snapshot, fmt.Errorf("unexpected tmux pane metadata")
	}
	content, err := tmuxOutput(ctx, "capture-pane", "-p", "-J", "-S", "-"+strconv.Itoa(history), "-t", fields[0])
	if err != nil {
		return snapshot, err
	}
	snapshot = v1.TerminalSnapshot{Session: session, Pane: fields[0], Title: fields[1], Command: fields[2], Content: string(content), CapturedAt: time.Now().UTC()}
	snapshot.Width, _ = strconv.Atoi(fields[3])
	snapshot.Height, _ = strconv.Atoi(fields[4])
	return snapshot, nil
}
