package boxruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// claudeReceiptScanner remembers how far the live channel has read each
// transcript. A long-lived Claude session can have a very large JSONL file;
// rescanning it once for every pending inbox event on every poll can starve
// the channel that is supposed to deliver those events.
type claudeReceiptScanner struct {
	files map[string]claudeReceiptFile
	seen  map[string]bool
}

type claudeReceiptFile struct {
	info   os.FileInfo
	offset int64
}

func (s *claudeReceiptScanner) scan(ctx context.Context, home, session string, pending []pendingInbound) (map[string]bool, error) {
	if s.files == nil {
		s.files = make(map[string]claudeReceiptFile)
		s.seen = make(map[string]bool)
	}
	wanted := make(map[string]bool, len(pending))
	if len(pending) == 0 {
		return wanted, nil
	}
	oldest := pending[0].mtime.Add(-5 * time.Second)
	for _, event := range pending {
		wanted[event.event.ID] = true
	}
	root := filepath.Join(home, ".claude", "projects")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(oldest) {
			return nil
		}
		state := s.files[path]
		if state.info == nil || !os.SameFile(state.info, info) || state.offset > info.Size() {
			state.offset = 0
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		err = func() error {
			defer file.Close()
			if _, err := file.Seek(state.offset, io.SeekStart); err != nil {
				return err
			}
			reader := bufio.NewReader(file)
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				line, readErr := reader.ReadBytes('\n')
				if len(line) > 0 && line[len(line)-1] == '\n' {
					state.offset += int64(len(line))
					if bytes.Contains(line, []byte("vmbox-desktop")) && bytes.Contains(line, []byte("message_id")) {
						for id := range wanted {
							if !s.seen[id] && bytes.Contains(line, []byte(id)) && claudeReceiptLineMatches(line, session, id) {
								s.seen[id] = true
							}
						}
					}
				}
				if errors.Is(readErr, io.EOF) {
					return nil
				}
				if readErr != nil {
					return readErr
				}
			}
		}()
		if err != nil {
			return err
		}
		state.info = info
		s.files[path] = state
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	for id := range wanted {
		wanted[id] = s.seen[id]
	}
	return wanted, err
}

func claudeReceiptLineMatches(line []byte, session, messageID string) bool {
	var record struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Attachment struct {
			Type   string `json:"type"`
			Prompt string `json:"prompt"`
			Origin struct {
				Kind   string `json:"kind"`
				Server string `json:"server"`
			} `json:"origin"`
		} `json:"attachment"`
	}
	if json.Unmarshal(line, &record) != nil {
		return false
	}
	if record.Type == "attachment" && record.Attachment.Type == "queued_command" &&
		record.Attachment.Origin.Kind == "channel" && record.Attachment.Origin.Server == "vmbox-desktop" &&
		claudeChannelHeaderMatches(record.Attachment.Prompt, session, messageID) {
		return true
	}
	if record.Type == "user" && record.Message.Role == "user" {
		for _, content := range claudeUserTexts(record.Message.Content) {
			if claudeChannelHeaderMatches(content, session, messageID) {
				return true
			}
		}
	}
	return false
}

var claudeChannelCurrent = func(ctx context.Context, session string) bool {
	data, err := tmuxCommand(ctx, "", "display-message", "-p", "-t", "="+session+":", "#{pane_pid}")
	if err != nil {
		return false
	}
	rootPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || rootPID < 2 {
		return false
	}
	pid := os.Getpid()
	for depth := 0; pid > 1 && depth < 128; depth++ {
		if pid == rootPID {
			return true
		}
		parent, err := parentProcessID(pid)
		if err != nil || parent <= 0 || parent == pid {
			break
		}
		pid = parent
	}
	return false
}

var claudeNativeReceiptWait = 30 * time.Second

func claudeChannelOwnerAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Claude's channel notification is a one-way MCP write. Only the user record
// in Claude's own conversation transcript proves that the native client
// consumed the event. Its channel tag carries our exact durable message ID.
func claudeNativeReceipt(ctx context.Context, home, session, messageID string, since time.Time) (bool, error) {
	root := filepath.Join(home, ".claude", "projects")
	found := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil || found || ctx.Err() != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(since.Add(-5 * time.Second)) {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		reader := bufio.NewReader(file)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			line, readErr := reader.ReadBytes('\n')
			if len(line) > 0 && strings.Contains(string(line), messageID) {
				var record struct {
					Type    string `json:"type"`
					Message struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"message"`
					Attachment struct {
						Type   string `json:"type"`
						Prompt string `json:"prompt"`
						Origin struct {
							Kind   string `json:"kind"`
							Server string `json:"server"`
						} `json:"origin"`
					} `json:"attachment"`
				}
				if json.Unmarshal(line, &record) == nil && record.Type == "attachment" &&
					record.Attachment.Type == "queued_command" &&
					record.Attachment.Origin.Kind == "channel" &&
					record.Attachment.Origin.Server == "vmbox-desktop" &&
					claudeChannelHeaderMatches(record.Attachment.Prompt, session, messageID) {
					found = true
					return nil
				}
				if record.Type == "user" && record.Message.Role == "user" {
					for _, content := range claudeUserTexts(record.Message.Content) {
						if claudeChannelHeaderMatches(content, session, messageID) {
							found = true
							return nil
						}
					}
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return found, err
}

func claudeChannelHeaderMatches(content, session, messageID string) bool {
	end := strings.IndexByte(content, '>')
	if end < 0 {
		return false
	}
	header := content[:end]
	return strings.HasPrefix(header, "<channel ") &&
		strings.Contains(header, `source="vmbox-desktop"`) &&
		strings.Contains(header, `chat_id="`+session+`"`) &&
		strings.Contains(header, `message_id="`+messageID+`"`)
}

func claudeUserTexts(raw json.RawMessage) []string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []string{text}
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" {
			texts = append(texts, part.Text)
		}
	}
	return texts
}

func claudeChatReceiptPath(home, session, messageID string) string {
	return filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", session, messageID+".delivered")
}

func ackClaudeNativeReceipt(home, session, messageID, inboxPath string) error {
	if err := writeTextAtomic(claudeChatReceiptPath(home, session, messageID), "native\n", 0600); err != nil {
		return err
	}
	err := os.Remove(inboxPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func DeliverClaudeChat(ctx context.Context, home, session string, inbound ChatInbound) error {
	if err := StoreChatInbound(home, session, inbound); err != nil {
		return err
	}
	inboxPath := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", session, inbound.ID+".json")
	if _, err := os.Stat(claudeChatReceiptPath(home, session, inbound.ID)); err == nil {
		return os.Remove(inboxPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	info, err := os.Stat(inboxPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, receiptErr := os.Stat(claudeChatReceiptPath(home, session, inbound.ID)); receiptErr == nil {
				return nil
			}
		}
		return err
	}
	deadline := time.NewTimer(claudeNativeReceiptWait)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	scanner := &claudeReceiptScanner{}
	pending := []pendingInbound{{event: chatInboundFile{ID: inbound.ID}, mtime: info.ModTime()}}
	for {
		if _, err := os.Stat(claudeChatReceiptPath(home, session, inbound.ID)); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		owners, err := claudeChannelOwners(home, session)
		if err != nil {
			return err
		}
		// The live channel scans once for all pending messages and writes the
		// receipt marker. A separate delivery process must not scan the same
		// large transcript for each message while that channel is active.
		if len(owners) == 0 {
			accepted, err := scanner.scan(ctx, home, session, pending)
			if err != nil {
				return err
			}
			if accepted[inbound.ID] {
				return ackClaudeNativeReceipt(home, session, inbound.ID, inboxPath)
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: Claude native receipt wait interrupted", ErrAmbiguousMessage)
		case <-deadline.C:
			return fmt.Errorf("%w: Claude native receipt not observed", ErrAmbiguousMessage)
		case <-ticker.C:
		}
	}
}

// ConfirmClaudeChat checks a previously attempted handoff without emitting a
// second channel notification. A new MCP connection can separately deliver a
// durable inbox event after the old TUI has exited.
func ConfirmClaudeChat(ctx context.Context, home, session, messageID string) (bool, error) {
	inboxPath := filepath.Join(home, ".local", "share", "vmbox", "chat", "inbox", session, messageID+".json")
	if _, err := os.Stat(claudeChatReceiptPath(home, session, messageID)); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	info, err := os.Stat(inboxPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owners, err := claudeChannelOwners(home, session)
	if err != nil {
		return false, err
	}
	if len(owners) > 0 {
		return false, nil
	}
	accepted, err := claudeNativeReceipt(ctx, home, session, messageID, info.ModTime())
	if err != nil || !accepted {
		return false, err
	}
	return true, ackClaudeNativeReceipt(home, session, messageID, inboxPath)
}
