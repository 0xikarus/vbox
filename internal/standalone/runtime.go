package standalone

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

// Runtime keeps the native delivery, capture, and session implementations shared.
type Runtime interface {
	Start(context.Context) error
	Deliver(context.Context, boxruntime.ChatInbound) error
	Confirm(context.Context, boxruntime.ChatInbound) (bool, error)
	Pull() (boxruntime.ChatEvent, bool, error)
	Ack(string) error
	Stream(context.Context, io.Reader, io.Writer) error
	Frame(context.Context, io.Writer) error
}
type BoxRuntime struct{ Assignment, Home, Session, Agent string }

func (r *BoxRuntime) agent() string {
	if r.Agent == "" {
		return "opencode"
	}
	return r.Agent
}

func (r *BoxRuntime) Start(ctx context.Context) error {
	// Fleet provisioning stages this path before launching an agent. Standalone
	// uses the immutable binary supplied by its own image instead.
	binary, err := exec.LookPath("vmbox-runtime")
	if err != nil {
		return err
	}
	binDir := filepath.Join(r.Home, "bin")
	if err = os.MkdirAll(binDir, 0700); err != nil {
		return err
	}
	staged := filepath.Join(binDir, "vmbox-runtime")
	if _, err = os.Lstat(staged); os.IsNotExist(err) {
		if err = os.Symlink(binary, staged); err != nil {
			return err
		}
	}
	if err := boxruntime.SetNativeAssignment(ctx, r.Assignment); err != nil {
		return err
	}
	if err := boxruntime.StartDesktop(ctx, r.Assignment); err != nil {
		return err
	}
	if err := boxruntime.StartStandaloneBlender(ctx, r.Home); err != nil {
		return err
	}
	if r.agent() == "codex" {
		if err := boxruntime.EnsureCodexDefaults(r.Home, boxruntime.WorkspaceDirectory()); err != nil {
			return err
		}
	}
	if r.agent() == "claude" {
		if err := boxruntime.EnsureClaudeDefaults(r.Home, boxruntime.WorkspaceDirectory()); err != nil {
			return err
		}
	}
	inv, err := boxruntime.NativeSessions(ctx, r.Assignment)
	if err != nil {
		return err
	}
	for _, session := range inv.Sessions {
		if session.Name == r.Session {
			return r.stabilizeViewers(ctx)
		}
	}
	root := boxruntime.New("").Root
	var candidate *boxruntime.ResumeCandidate
	var codexCandidate *boxruntime.CodexResumeCandidate
	if r.agent() == "codex" {
		codexCandidate, err = boxruntime.FindCodexResumeCandidate(root, r.Home, r.Session)
	} else if r.agent() == "claude" {
		candidate, err = boxruntime.FindClaudeResumeCandidate(root, r.Home, boxruntime.WorkspaceDirectory(), r.Session)
	} else {
		candidate, err = boxruntime.FindOpenCodeResumeCandidate(root, r.Home, boxruntime.WorkspaceDirectory(), r.Session)
	}
	if err != nil {
		return err
	}
	if err = boxruntime.StartInteractive(ctx, root, r.Session, r.agent()); err != nil {
		return err
	}
	if codexCandidate != nil {
		if err = boxruntime.RestoreCodexConversation(ctx, root, r.Session, *codexCandidate); err != nil {
			return err
		}
	}
	if candidate != nil {
		if r.agent() == "claude" {
			err = boxruntime.RestoreClaudeConversation(ctx, root, r.Session, *candidate)
		} else {
			err = boxruntime.RestoreOpenCodeConversation(ctx, root, r.Session, *candidate)
		}
		if err != nil {
			return err
		}
	}
	return r.stabilizeViewers(ctx)
}

// Two simultaneous terminal clients must not resize the same application on
// each focus change. Desktop mirrors ignore size; web terminals determine it.
func (r *BoxRuntime) stabilizeViewers(ctx context.Context) error {
	for _, option := range [][]string{{"window-size", "largest"}, {"aggressive-resize", "off"}} {
		args := append([]string{"set-option", "-w", "-t", "=" + r.Session + ":"}, option...)
		if err := exec.CommandContext(ctx, "tmux", args...).Run(); err != nil {
			return fmt.Errorf("stabilize shared terminal size: %w", err)
		}
	}
	// fill-character is unavailable on some older tmux releases.
	_ = exec.CommandContext(ctx, "tmux", "set-option", "-w", "-t", "="+r.Session+":", "fill-character", " ").Run()
	return nil
}

func (r *BoxRuntime) Snapshot(ctx context.Context) error {
	_, err := boxruntime.SaveTmuxState(ctx, boxruntime.New("").Root)
	return err
}
func (r *BoxRuntime) Deliver(ctx context.Context, m boxruntime.ChatInbound) error {
	if r.agent() == "codex" {
		return boxruntime.DeliverCodexChat(ctx, boxruntime.New("").Root, r.Home, r.Session, m)
	}
	if r.agent() == "claude" {
		return boxruntime.DeliverClaudeChat(ctx, r.Home, r.Session, m)
	}
	return boxruntime.DeliverOpenCodeChat(ctx, r.Home, r.Session, m)
}
func (r *BoxRuntime) Confirm(ctx context.Context, m boxruntime.ChatInbound) (bool, error) {
	if r.agent() == "codex" {
		return boxruntime.ConfirmCodexChat(ctx, boxruntime.New("").Root, r.Home, r.Session, m.ID)
	}
	if r.agent() == "claude" {
		return boxruntime.ConfirmClaudeChat(ctx, r.Home, r.Session, m.ID)
	}
	return boxruntime.ConfirmOpenCodeChat(ctx, r.Home, r.Session, m)
}
func (r *BoxRuntime) Pull() (boxruntime.ChatEvent, bool, error) {
	return boxruntime.PullChatEvent(r.Home, r.Session)
}
func (r *BoxRuntime) Ack(id string) error { return boxruntime.AckChatEvent(r.Home, r.Session, id) }
func (r *BoxRuntime) Stream(ctx context.Context, in io.Reader, out io.Writer) error {
	return boxruntime.StreamDesktop(ctx, r.Assignment, in, out)
}
func (r *BoxRuntime) Frame(ctx context.Context, out io.Writer) error {
	return boxruntime.CaptureDesktop(ctx, r.Assignment, out)
}

func WriteAgentConfig(home, url, assignment, token string) error {
	dir := filepath.Join(home, ".config", "vmbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(boxruntime.DesktopAgentConfig{Mode: "standalone", Controller: url, Assignment: assignment, Token: token})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "desktop-agent.json"), data, 0600)
}

func (r *BoxRuntime) Terminal(ctx context.Context, input io.Reader, output io.Writer) error {
	inv, err := boxruntime.NativeSessions(ctx, r.Assignment)
	if err != nil {
		return err
	}
	for _, session := range inv.Sessions {
		if session.Name == r.Session {
			cmd := exec.CommandContext(ctx, "vmbox-runtime", "web-terminal", r.Assignment, session.ID, session.Incarnation)
			cmd.Stdin = input
			cmd.Stdout = output
			return cmd.Run()
		}
	}
	return fmt.Errorf("agent session unavailable")
}

func (r *BoxRuntime) Healthy(ctx context.Context) bool {
	inv, err := boxruntime.NativeSessions(ctx, r.Assignment)
	if err != nil {
		return false
	}
	for _, session := range inv.Sessions {
		if session.Name == r.Session {
			return true
		}
	}
	return false
}
