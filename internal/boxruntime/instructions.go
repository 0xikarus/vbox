package boxruntime

// Managed agent instructions. One canonical per-box file under the vmbox
// config directory is linked into each agent's global instruction slot:
//
//	Codex    reads ~/.codex/AGENTS.md                (verified codex-cli 0.154)
//	Claude   reads ~/.claude/CLAUDE.md               (verified Claude Code 2.1)
//	OpenCode reads ~/.config/opencode/AGENTS.md      (verified OpenCode 1.18)
//
// Those slots are global per-box memory: project-level AGENTS.md/CLAUDE.md
// files keep their documented precedence, and repository-owned instruction
// files are never written, overwritten, or appended to. vmbox-owned links are
// tracked in a ledger; pre-existing user files at those paths are conflicts
// that stay untouched and get reported, never replaced.

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

const (
	// InstructionCanonicalName is the single per-box instruction source.
	InstructionCanonicalName = "instructions.md"
	instructionLedgerName    = "instructions.json"
)

// InstructionFile names the on-box paths (workspace root substituted at call
// time, so shared workers keep their per-slot home).
func instructionCanonicalPath(home string) string {
	return filepath.Join(home, ".config", "vmbox", InstructionCanonicalName)
}

func instructionLedgerPath(home string) string {
	return filepath.Join(home, ".config", "vmbox", instructionLedgerName)
}

// AgentInstructionLinks lists every agent-specific global instruction slot the
// canonical file is linked into, in stable order.
func AgentInstructionLinks(home string) []InstructionLink {
	return []InstructionLink{
		{Agent: "codex", Path: filepath.Join(home, ".codex", "AGENTS.md")},
		{Agent: "claude", Path: filepath.Join(home, ".claude", "CLAUDE.md")},
		{Agent: "opencode", Path: filepath.Join(home, ".config", "opencode", "AGENTS.md")},
	}
}

type InstructionLink struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
}

const (
	InstructionLinked    = "linked"    // canonical content linked into the agent slot
	InstructionAbsent    = "absent"    // no instructions requested and nothing present
	InstructionRemoved   = "removed"   // vmbox-owned link removed after instructions were cleared
	InstructionConflict  = "conflict"  // a pre-existing non-vmbox file/link was left untouched
	InstructionUntouched = "untouched" // vmbox content already in place; nothing changed
)

// InstructionApplyResult reports the per-agent outcome of one apply pass.
type InstructionApplyResult struct {
	Changed   bool              `json:"changed"`
	Empty     bool              `json:"empty"`
	SHA256    string            `json:"sha256"` // digest of the canonical markdown (empty when unset)
	Links     map[string]string `json:"links"`
	Canonical string            `json:"canonical"`
}

// instructionLedger persists which slots vmbox created, so non-owned files are
// never overwritten and owned links survive across runtime versions.
type instructionLedger struct {
	SHA256 string            `json:"sha256"`
	Links  map[string]string `json:"links"` // agent -> InstructionLinked | InstructionConflict
}

// InstructionSyncRequest is the payload the controller streams to
// `vmbox-runtime sync-instructions`.
type InstructionSyncRequest struct {
	Markdown string `json:"markdown"`
}

func readInstructionLedger(home string) (instructionLedger, error) {
	ledger := instructionLedger{Links: map[string]string{}}
	data, err := os.ReadFile(instructionLedgerPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return ledger, nil
	}
	if err != nil {
		return ledger, err
	}
	if err := json.Unmarshal(data, &ledger); err != nil {
		return instructionLedger{Links: map[string]string{}}, fmt.Errorf("invalid vbox instruction ledger; remove %s to re-ownership %s", instructionLedgerPath(home), instructionCanonicalPath(home))
	}
	if ledger.Links == nil {
		ledger.Links = map[string]string{}
	}
	return ledger, nil
}

func writeInstructionFile(home, path string, data []byte, mode os.FileMode, owner *Ownership, chown Chowner) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vmbox-instructions-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := owner.Apply(tmpPath, chown); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func writeInstructionLedger(home string, ledger instructionLedger, owner *Ownership, chown Chowner) error {
	data, err := json.Marshal(ledger)
	if err != nil {
		return err
	}
	return writeInstructionFile(home, instructionLedgerPath(home), append(data, '\n'), 0o600, owner, chown)
}

// applyInstructionCanonical writes or removes the canonical Markdown. Changed
// content replaces atomically; identical content is left untouched.
func applyInstructionCanonical(home, markdown string, owner *Ownership, chown Chowner) (string, bool, error) {
	canonical := instructionCanonicalPath(home)
	if strings.TrimSpace(markdown) == "" {
		if _, err := os.Lstat(canonical); errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		} else if err != nil {
			return "", false, err
		}
		if err := os.Remove(canonical); err != nil {
			return "", false, err
		}
		return "", true, nil
	}
	if err := v1.ValidateEffectiveInstructionMarkdown(markdown); err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(canonical)
	if err == nil && string(data) == markdown {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(markdown))), false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	if err := EnsurePrivateDirectory(home, filepath.Dir(canonical), owner, chown); err != nil {
		return "", false, err
	}
	if err := writeInstructionFile(home, canonical, []byte(markdown), 0o600, owner, chown); err != nil {
		return "", false, err
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(markdown))), true, nil
}

// reconcileInstructionLink points one agent slot at the canonical file, or
// removes the vmbox-owned link when instructions were cleared. Existing user
// files and non-vmbox links are conflicts: left untouched, reported, and never
// replaced — those agents then keep reading the pre-existing file instead.
func reconcileInstructionLink(home string, link InstructionLink, canonical string, empty bool, ledger instructionLedger, owner *Ownership, chown Chowner) (string, error) {
	target := filepath.Join(filepath.Dir(instructionCanonicalPath(home)), InstructionCanonicalName)
	current, err := os.Readlink(link.Path)
	isLink := err == nil
	isOurs := isLink && current == target
	if empty {
		if isOurs {
			if err := os.Remove(link.Path); err != nil {
				return "", fmt.Errorf("remove vmbox-managed %s instructions: %w", link.Agent, err)
			}
			return InstructionRemoved, nil
		}
		if _, statErr := os.Lstat(link.Path); statErr == nil {
			// Pre-existing content stays; applying "none" must not delete user files.
			if ledger.Links[link.Agent] == InstructionConflict {
				return InstructionConflict, nil
			}
			return InstructionAbsent, nil
		}
		return InstructionAbsent, nil
	}
	if isOurs {
		return InstructionUntouched, nil
	}
	if _, statErr := os.Lstat(link.Path); statErr == nil {
		// A real file or foreign link owns this slot. The canonical file must
		// not divert it, so record the conflict instead of overwriting it.
		return InstructionConflict, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	if err := EnsurePrivateDirectory(home, filepath.Dir(link.Path), owner, chown); err != nil {
		return "", err
	}
	if err := os.Symlink(target, link.Path); err != nil {
		return "", fmt.Errorf("link %s instructions: %w", link.Agent, err)
	}
	if owner != nil {
		if err := os.Lchown(link.Path, owner.UID, owner.GID); err != nil {
			_ = os.Remove(link.Path)
			return "", err
		}
	}
	return InstructionLinked, nil
}

// ApplyManagedInstructions installs the controller's markdown snapshot as the
// box's single canonical instruction source and reconciles every agent link.
// Pass an empty markdown to remove managed instructions (selected "none").
// Reapplying identical content is a no-op, including repeated restores.
func ApplyManagedInstructions(home, markdown string, owner *Ownership, chown Chowner) (InstructionApplyResult, error) {
	result := InstructionApplyResult{Links: map[string]string{}}
	if home == "" || !filepath.IsAbs(home) {
		return result, fmt.Errorf("instructions require an absolute box home")
	}
	empty := strings.TrimSpace(markdown) == ""
	ledger, err := readInstructionLedger(home)
	if err != nil {
		return result, err
	}
	digest, changed, err := applyInstructionCanonical(home, markdown, owner, chown)
	if err != nil {
		return result, err
	}
	result.Changed, result.Empty, result.SHA256, result.Canonical = changed, empty, digest, instructionCanonicalPath(home)
	newLinks := map[string]string{}
	for _, link := range AgentInstructionLinks(home) {
		state, err := reconcileInstructionLink(home, link, digest, empty, ledger, owner, chown)
		if err != nil {
			return result, err
		}
		result.Links[link.Agent] = state
		if state == InstructionLinked || state == InstructionUntouched {
			newLinks[link.Agent] = InstructionLinked
		} else if state == InstructionConflict {
			newLinks[link.Agent] = InstructionConflict
		}
		if state == InstructionLinked || state == InstructionRemoved {
			result.Changed = true
		}
	}
	if err := EnsurePrivateDirectory(home, filepath.Dir(instructionLedgerPath(home)), owner, chown); err != nil {
		return result, err
	}
	if err := writeInstructionLedger(home, instructionLedger{SHA256: digest, Links: newLinks}, owner, chown); err != nil {
		return result, err
	}
	return result, nil
}

// RestoreManagedInstructions re-asserts agent links from the box's retained
// canonical file after hibernation, replacement, or recovery. It never invents
// content: without a canonical file it is a no-op, so boxes that predate the
// feature stay untouched. Restore runs over the control-plane data path, so
// workload ownership is resolved the same way sync-files resolves it.
func RestoreManagedInstructions(home string) error {
	data, err := os.ReadFile(instructionCanonicalPath(home))
	if err != nil {
		// No readable canonical file (absent, unreadable, home missing):
		// nothing managed to restore. Restore is best-effort reconcile, not a
		// startup gate — a weird home layout must never block tool restoration.
		return nil
	}
	owner, err := LookupWorkloadOwnership(os.Geteuid(), nil)
	if err != nil {
		return err
	}
	_, err = ApplyManagedInstructions(home, string(data), owner, nil)
	return err
}

// InstructionApplyReport formats one stable, low-noise progress line for
// restore/apply logs.
func (r InstructionApplyResult) InstructionApplyReport() string {
	if r.Empty {
		return "Managed agent instructions: none selected."
	}
	order := []string{"codex", "claude", "opencode"}
	parts := make([]string, 0, len(order))
	for _, agent := range order {
		state, ok := r.Links[agent]
		if !ok {
			continue
		}
		switch state {
		case InstructionLinked, InstructionUntouched:
			state = "ready"
		case InstructionConflict:
			state = "kept pre-existing file (left untouched)"
		}
		parts = append(parts, agent+": "+state)
	}
	sort.Strings(parts)
	return "Managed agent instructions applied to " + strings.Join(parts, ", ") + "."
}
