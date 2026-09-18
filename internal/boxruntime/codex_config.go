package boxruntime

import (
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
// The table is edited where it already is: appending a second one is a duplicate
// key, and Codex then refuses to parse the file at all.
func setCodexProjectTrust(lines []string, workspace string) []string {
	header := fmt.Sprintf("[projects.%q]", workspace)
	const trusted = `trust_level = "trusted"`
	out := make([]string, 0, len(lines)+3)
	inTarget, wrote, found, dropping := false, false, false, false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if inTarget && !wrote {
				out = append(out, trusted)
				wrote = true
			}
			inTarget, dropping = false, false
			if trimmed == header {
				// A second copy is what an earlier append left behind. Carrying
				// it forward would keep the file unparseable.
				if found {
					dropping = true
					continue
				}
				inTarget, found, wrote = true, true, false
			}
			out = append(out, line)
			continue
		}
		if dropping {
			continue
		}
		if inTarget && strings.HasPrefix(trimmed, "trust_level") {
			if !wrote {
				out = append(out, trusted)
				wrote = true
			}
			continue
		}
		out = append(out, line)
	}
	if inTarget && !wrote {
		out = append(out, trusted)
	}
	if found {
		return out
	}
	return append(out, "", header, trusted)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
