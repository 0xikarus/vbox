package boxruntime

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A box is already an externally sandboxed machine, so Codex's own approvals and
// sandbox only get in the way: its sandbox needs namespaces the runtime refuses.
var codexFullAccess = map[string]string{
	"approval_policy": `"never"`,
	"sandbox_mode":    `"danger-full-access"`,
}

// EnsureCodexDefaults writes the box's permissions and workspace trust into
// ~/.codex/config.toml, so a terminal `codex` behaves like a chat-started one.
// It is idempotent: an imported login profile can be re-applied over it.
func EnsureCodexDefaults(home, workspace string) error {
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	lines = setCodexTopLevel(lines, codexFullAccess)
	if workspace != "" {
		lines = setCodexProjectTrust(lines, workspace)
	}
	return writeTextAtomic(path, strings.Join(lines, "\n")+"\n", 0600)
}

// setCodexTopLevel replaces keys before the first table header. Keys inside a
// table are left alone: they belong to it.
func setCodexTopLevel(lines []string, values map[string]string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(lines)+len(values))
	done := false
	flush := func() {
		for _, key := range sortedKeys(values) {
			if !seen[key] {
				out = append(out, key+" = "+values[key])
				seen[key] = true
			}
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !done && strings.HasPrefix(trimmed, "[") {
			flush()
			done = true
		}
		if !done {
			if key, _, ok := strings.Cut(trimmed, "="); ok {
				name := strings.TrimSpace(key)
				if _, managed := values[name]; managed {
					if seen[name] {
						continue
					}
					out = append(out, name+" = "+values[name])
					seen[name] = true
					continue
				}
			}
		}
		out = append(out, line)
	}
	flush()
	return out
}

// setCodexProjectTrust marks one workspace trusted so Codex never asks about it.
func setCodexProjectTrust(lines []string, workspace string) []string {
	header := fmt.Sprintf("[projects.%q]", workspace)
	out := make([]string, 0, len(lines)+3)
	inTarget, wrote := false, false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if inTarget && !wrote {
				out = append(out, `trust_level = "trusted"`)
			}
			inTarget = trimmed == header
			wrote = false
			out = append(out, line)
			continue
		}
		if inTarget && strings.HasPrefix(trimmed, "trust_level") {
			if !wrote {
				out = append(out, `trust_level = "trusted"`)
				wrote = true
			}
			continue
		}
		out = append(out, line)
	}
	if inTarget {
		if !wrote {
			out = append(out, `trust_level = "trusted"`)
		}
		return out
	}
	return append(out, "", header, `trust_level = "trusted"`)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// CodexThreadID maps a tmux session to the Codex thread running in it, the way
// OpenCodeChatPort maps one to a port. Codex will not take an id at launch, so
// the id it recorded in its rollout is read once and remembered against the
// session name. That addresses a Codex started from a terminal too, which
// renaming its thread through the TUI could not do without typing into it.
func CodexThreadID(root, home, session, workspace string) (string, error) {
	marker := filepath.Join(root, "chat", "codex-threads", session)
	if data, err := os.ReadFile(marker); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id, nil
		}
	}
	id, err := newestCodexThread(home, workspace)
	if err != nil {
		return "", err
	}
	if err := writeTextAtomic(marker, id+"\n", 0600); err != nil {
		return "", err
	}
	return id, nil
}

// ForgetCodexThread drops a remembered id so the next delivery re-reads it.
func ForgetCodexThread(root, session string) error {
	err := os.Remove(filepath.Join(root, "chat", "codex-threads", session))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// newestCodexThread returns the id of the most recently written rollout that
// Codex recorded for this workspace.
func newestCodexThread(home, workspace string) (string, error) {
	root := filepath.Join(home, ".codex", "sessions")
	best, newest := "", int64(0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil || info.ModTime().UnixNano() <= newest {
			return nil
		}
		id, cwd := readCodexSessionMeta(path)
		if id == "" || cwd != workspace {
			return nil
		}
		best, newest = id, info.ModTime().UnixNano()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if best == "" {
		return "", fmt.Errorf("no Codex thread recorded for %s", workspace)
	}
	return best, nil
}

func readCodexSessionMeta(path string) (string, string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()
	line, err := bufio.NewReaderSize(file, 128*1024).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return "", ""
	}
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			SessionID string `json:"session_id"`
			CWD       string `json:"cwd"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "session_meta" {
		return "", ""
	}
	return record.Payload.SessionID, record.Payload.CWD
}

func writeTextAtomic(path, contents string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.WriteString(contents); err != nil {
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
	return os.Rename(temporary, path)
}
