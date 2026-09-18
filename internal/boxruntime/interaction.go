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
var captureTmuxCommand = tmuxOutput
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

func DeliverTmuxInput(ctx context.Context, root, session, messageID, text string, submit bool, steer ...bool) error {
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
	if len(steer) > 0 && steer[0] {
		marker, err := tmuxCommand(ctx, "", "show-environment", "-t", session, taskAgentEnvironment)
		if err != nil {
			return ErrAmbiguousMessage
		}
		agent := strings.TrimPrefix(strings.TrimSpace(string(marker)), taskAgentEnvironment+"=")
		if agent == "codex" || agent == "claude" || agent == "opencode" {
			if _, err := tmuxCommand(ctx, "", "send-keys", "-t", session, "Escape"); err != nil {
				return ErrAmbiguousMessage
			}
			if err := tmuxSubmitPause(ctx); err != nil {
				return ErrAmbiguousMessage
			}
		}
	}
	// Bash consumes a pasted carriage return as a terminal byte. The prompt
	// heuristic below is for agent TUIs; it cannot recognize a shell prompt and
	// would report an already executed shell command as ambiguous.
	plainShell := false
	if submit {
		command, commandErr := tmuxCommand(ctx, "", "display-message", "-p", "-t", session, "#{pane_current_command}")
		plainShell = commandErr == nil && strings.TrimSpace(string(command)) == "bash"
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
		if !plainShell {
			if err := confirmTmuxSubmit(ctx, buffer, session, text); err != nil {
				return ErrAmbiguousMessage
			}
		}
	}
	if err := os.Rename(pending, delivered); err != nil {
		return ErrAmbiguousMessage
	}
	return nil
}

func DeliverTmuxKeys(ctx context.Context, root, session, messageID string, keys []string) error {
	if err := validateTmuxToken("session", session); err != nil {
		return err
	}
	if err := validateTmuxToken("message ID", messageID); err != nil {
		return err
	}
	if len(keys) == 0 || len(keys) > 64 {
		return fmt.Errorf("terminal key event must contain between 1 and 64 keys")
	}
	for _, key := range keys {
		if !validTmuxKey(key) {
			return fmt.Errorf("unsupported terminal key %q", key)
		}
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
	args := []string{"send-keys", "-t", session}
	args = append(args, keys...)
	if _, err := tmuxCommand(ctx, "", args...); err != nil {
		return ErrAmbiguousMessage
	}
	if err := os.Rename(pending, delivered); err != nil {
		return ErrAmbiguousMessage
	}
	return nil
}

func validTmuxKey(key string) bool {
	switch key {
	case "Enter", "BSpace", "Tab", "Escape", "Up", "Down", "Right", "Left", "Home", "End", "DC", "PPage", "NPage":
		return true
	}
	if len(key) == 3 && strings.HasPrefix(key, "C-") {
		character := key[2]
		return (character >= 'A' && character <= 'Z') || strings.ContainsRune("@[\\]^_?", rune(character))
	}
	if strings.HasPrefix(key, "F") {
		number, err := strconv.Atoi(strings.TrimPrefix(key, "F"))
		return err == nil && number >= 1 && number <= 12
	}
	return false
}

func pasteTmuxCarriageReturn(ctx context.Context, buffer, session string) error {
	if _, err := tmuxCommand(ctx, "\r", "load-buffer", "-b", buffer, "-"); err != nil {
		return err
	}
	_, err := tmuxCommand(ctx, "", "paste-buffer", "-d", "-b", buffer, "-t", session)
	return err
}

func confirmTmuxSubmit(ctx context.Context, buffer, session, text string) error {
	clearSamples := 0
	for attempt := 0; attempt < 32; attempt++ {
		if err := tmuxSubmitConfirmPause(ctx); err != nil {
			return err
		}
		content, err := tmuxCommand(ctx, "", "capture-pane", "-p", "-J", "-t", session)
		if err != nil {
			return err
		}
		if terminalBlocksSubmit(string(content)) {
			return nil
		}
		switch tmuxInputSubmissionState(string(content), text) {
		case tmuxInputStaged:
			clearSamples = 0
			if err := pasteTmuxCarriageReturn(ctx, buffer, session); err != nil {
				return err
			}
		case tmuxInputCleared:
			clearSamples++
			if clearSamples >= 4 {
				return nil
			}
		default:
			clearSamples = 0
		}
	}
	return fmt.Errorf("task input remained staged after bounded submit retries")
}

type tmuxInputState uint8

const (
	tmuxInputUnknown tmuxInputState = iota
	tmuxInputStaged
	tmuxInputCleared
)

func tmuxInputSubmissionState(content, text string) tmuxInputState {
	needle := strings.TrimSpace(strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")[0])
	if needle == "" {
		return tmuxInputCleared
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
				return tmuxInputCleared
			}
			// Claude renders its rotating suggestion inside the empty input row.
			// It is not evidence that a pasted message was submitted; treating it
			// as cleared can acknowledge a delivery before Claude consumes paste.
			if marker == "❯" && strings.HasPrefix(input, `Try "`) {
				return tmuxInputUnknown
			}
			if input == needle || len(input) >= 8 && strings.HasPrefix(needle, input) {
				return tmuxInputStaged
			}
			return tmuxInputCleared
		}
	}
	return tmuxInputUnknown
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
	case "shell":
		argv = []string{"/bin/bash", "-l"}
	default:
		var err error
		argv, err = persistentAgentArgv(session, agent)
		if err != nil {
			return fmt.Errorf("unsupported task agent %q", agent)
		}
	}
	opencodeStartupPrompt := agent == "opencode" && prompt != ""
	if opencodeStartupPrompt {
		argv = append(argv, "--prompt", prompt)
	}
	created, err := startTmuxTaskSession(ctx, root, session, agent, argv)
	if err != nil {
		return err
	}
	if agent != "shell" {
		if err := waitForAgentReady(ctx, session, agent); err != nil {
			return err
		}
		if err := agentReadySettlePause(ctx); err != nil {
			return err
		}
	}
	if prompt == "" {
		return nil
	}
	if !created && agent != "shell" {
		if agent == "opencode" {
			return DeliverOpenCodeChat(ctx, os.Getenv("HOME"), session, ChatInbound{ID: messageID, Text: prompt})
		}
		// Existing native agent sessions receive messages through their native
		// conversation transport, never through terminal input.
		return fmt.Errorf("tmux session %q already exists", session)
	}
	if opencodeStartupPrompt {
		return nil
	}
	return DeliverTmuxInput(ctx, root, session, messageID, prompt, true)
}

func startTmuxTaskSession(ctx context.Context, root, session, agent string, argv []string) (bool, error) {
	if _, err := tmuxCommand(ctx, "", "has-session", "-t", session); err != nil {
		assignment, err := prepareManagedDesktop(ctx, agent)
		if err != nil {
			return false, err
		}
		if err := ensureAgentBackend(ctx, session, agent); err != nil {
			return false, err
		}
		args := []string{"new-session", "-d", "-s", session, "-c", WorkspaceDirectory(), "--"}
		args = append(args, argv...)
		if _, err := tmuxCommand(ctx, "", args...); err != nil {
			return false, fmt.Errorf("start %s task session: %w", agent, err)
		}
		if _, err := tmuxCommand(ctx, "", "set-environment", "-t", session, taskAgentEnvironment, agent); err != nil {
			return false, fmt.Errorf("mark %s task session: %w", agent, err)
		}
		if err := ApplyTmuxContext(ctx, root, session); err != nil {
			return false, fmt.Errorf("apply %s task session context: %w", agent, err)
		}
		_, _ = tmuxCommand(ctx, "", "source-file", "/etc/vmbox/tmux.conf")
		if assignment != "" {
			if err := EnsureDesktopTerminals(ctx, assignment); err != nil {
				return false, err
			}
		}
		return true, nil
	} else {
		marker, err := tmuxCommand(ctx, "", "show-environment", "-t", session, taskAgentEnvironment)
		if err != nil || strings.TrimSpace(string(marker)) != taskAgentEnvironment+"="+agent {
			return false, fmt.Errorf("tmux session %q already exists and is not a %s task session; choose a different session name", session, agent)
		}
	}
	return false, nil
}

func waitForAgentReady(ctx context.Context, session, agent string) error {
	if agent == "opencode" {
		deadline := time.NewTimer(agentReadyTimeout)
		defer deadline.Stop()
		for {
			ready, err := openCodeReadyProbe(ctx, session)
			if err != nil {
				return err
			}
			if ready {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return fmt.Errorf("%s task session API did not become ready", agent)
			case <-time.After(agentReadyPollInterval):
			}
		}
	}
	deadline := time.NewTimer(agentReadyTimeout)
	defer deadline.Stop()
	for {
		content, err := tmuxCommand(ctx, "", "capture-pane", "-p", "-J", "-S", "-80", "-t", session)
		if err == nil {
			text := string(content)
			if agent == "claude" && strings.Contains(text, "WARNING: Loading development channels") && strings.Contains(text, "I am using this for local development") && strings.Contains(text, "Enter to confirm") {
				if _, err := tmuxCommand(ctx, "", "send-keys", "-t", session, "Enter"); err != nil {
					return fmt.Errorf("accept development channel in %s session: %w", agent, err)
				}
			} else if agent == "codex" && strings.Contains(text, "Approaching rate limits") && strings.Contains(text, "Press enter to confirm or esc to go back") {
				if _, err := tmuxCommand(ctx, "", "send-keys", "-t", session, "Escape"); err != nil {
					return fmt.Errorf("dismiss rate limit reminder in %s session: %w", agent, err)
				}
			} else if agent == "claude" && strings.Contains(text, "Quick safety check:") && strings.Contains(text, "Yes, I trust this folder") && strings.Contains(text, "Enter to confirm") {
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
		return !terminalBlocksSubmit(content) && !strings.Contains(content, "Approaching rate limits") && strings.Contains(content, "OpenAI Codex") && strings.Contains(content, "›")
	case "claude":
		if !strings.Contains(content, "Claude Code v") {
			return false
		}
		for _, line := range strings.Split(strings.ReplaceAll(content, "\u00a0", " "), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "❯ Try \"") {
				return true
			}
		}
		return false
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
	format := strings.Join([]string{"#{session_name}", "#{pane_id}", "#{pane_title}", "#{pane_current_command}", "#{pane_width}", "#{pane_height}"}, "\x1f")
	metadata, err := captureTmuxCommand(ctx, "display-message", "-p", "-t", session, format)
	if err != nil {
		return snapshot, err
	}
	fields := strings.Split(strings.TrimSuffix(string(metadata), "\n"), "\x1f")
	if len(fields) != 6 {
		return snapshot, fmt.Errorf("unexpected tmux pane metadata")
	}
	if fields[0] != session || fields[1] == "" {
		return snapshot, fmt.Errorf("tmux returned session %q pane %q for requested session %q", fields[0], fields[1], session)
	}
	content, err := captureTmuxCommand(ctx, "capture-pane", "-p", "-J", "-S", "-"+strconv.Itoa(history), "-t", fields[1])
	if err != nil {
		return snapshot, err
	}
	snapshot = v1.TerminalSnapshot{Session: session, Pane: fields[1], Title: fields[2], Command: fields[3], Content: string(content), CapturedAt: time.Now().UTC()}
	snapshot.Width, _ = strconv.Atoi(fields[4])
	snapshot.Height, _ = strconv.Atoi(fields[5])
	return snapshot, nil
}
