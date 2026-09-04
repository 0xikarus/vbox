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

func tmuxCommand(ctx context.Context, stdin string, args ...string) ([]byte, error) {
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
		if _, err := tmuxCommand(ctx, "", "send-keys", "-t", session, "Enter"); err != nil {
			return ErrAmbiguousMessage
		}
	}
	if err := os.Rename(pending, delivered); err != nil {
		return ErrAmbiguousMessage
	}
	return nil
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
		_, _ = tmuxCommand(ctx, "", "source-file", "/etc/vmbox/tmux.conf")
	}
	return DeliverTmuxInput(ctx, root, session, messageID, prompt, true)
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
