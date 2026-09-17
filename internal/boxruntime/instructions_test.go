package boxruntime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testInstructionHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// readLink resolves a link safely for assertions; empty string when missing.
func readLinkOrEmpty(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("readlink %s: %v", path, err)
	}
	return target
}

func TestApplyManagedInstructionsLinksEveryAgent(t *testing.T) {
	home := testInstructionHome(t)
	markdown := "# Team rules\nEvery answer starts with READY.\n"
	result, err := ApplyManagedInstructions(home, markdown, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Empty {
		t.Fatalf("first apply must report one change: %+v", result)
	}
	canonical := filepath.Join(home, ".config", "vmbox", "instructions.md")
	data, err := os.ReadFile(canonical)
	if err != nil || string(data) != markdown {
		t.Fatalf("canonical content mismatch: %v %q", err, data)
	}
	if result.SHA256 == "" || result.Canonical != canonical {
		t.Fatalf("result should name the canonical file and its digest: %+v", result)
	}
	if !strings.Contains(result.InstructionApplyReport(), "codex: ready") {
		t.Fatalf("report should summarize agent states: %q", result.InstructionApplyReport())
	}
	for _, link := range AgentInstructionLinks(home) {
		if target := readLinkOrEmpty(t, link.Path); target != canonical {
			t.Fatalf("%s must link to the canonical file: %q", link.Agent, target)
		}
		if result.Links[link.Agent] != "linked" {
			t.Fatalf("%s state should be linked: %+v", link.Agent, result.Links)
		}
		parent, err := os.Stat(filepath.Dir(link.Path))
		if err != nil || parent.Mode().Perm() != 0o700 {
			t.Fatalf("%s slot must live in a private directory: %v %v", link.Agent, parent.Mode(), err)
		}
	}
	// One canonical source: no divergent materialized copies.
	content, err := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
	if err != nil || string(content) != markdown {
		t.Fatalf("codex slot must resolve to canonical content: %v %q", err, content)
	}
	content, err = os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil || string(content) != markdown {
		t.Fatalf("claude slot must resolve to canonical content: %v %q", err, content)
	}
	content, err = os.ReadFile(filepath.Join(home, ".config", "opencode", "AGENTS.md"))
	if err != nil || string(content) != markdown {
		t.Fatalf("opencode slot must resolve to canonical content: %v %q", err, content)
	}
}

func TestApplyManagedInstructionsIsIdempotent(t *testing.T) {
	home := testInstructionHome(t)
	markdown := "Stay terse."
	first, err := ApplyManagedInstructions(home, markdown, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed {
		t.Fatal("first apply must report a change")
	}
	second, err := ApplyManagedInstructions(home, markdown, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed {
		t.Fatalf("reapplying identical instructions must be a no-op: %+v", second)
	}
	updated, err := ApplyManagedInstructions(home, markdown+"One more rule.", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Changed || updated.SHA256 == first.SHA256 {
		t.Fatalf("content change must be detected with a new digest")
	}
	data, _ := os.ReadFile(filepath.Join(home, ".config", "vmbox", "instructions.md"))
	if string(data) != markdown+"One more rule." {
		t.Fatalf("canonical must hold the new contents: %q", data)
	}
}

func TestApplyManagedInstructionsNoneRemovesOnlyOwnedLinks(t *testing.T) {
	home := testInstructionHome(t)
	if _, err := ApplyManagedInstructions(home, "Be helpful.", nil, nil); err != nil {
		t.Fatal(err)
	}
	// A pre-existing user file in the claude slot must survive even "none".
	claude := filepath.Join(home, ".claude", "CLAUDE.md")
	os.Remove(claude)
	if err := os.WriteFile(claude, []byte("user-authored memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyManagedInstructions(home, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Empty {
		t.Fatalf("clearing must report the removal: %+v", result)
	}
	if _, err := os.Lstat(filepath.Join(home, ".config", "vmbox", "instructions.md")); !os.IsNotExist(err) {
		t.Fatalf("canonical must be removed when instructions are cleared: %v", err)
	}
	if result.Links["claude"] != "absent" {
		t.Fatalf("claude must report absent (foreign file left alone), got %q", result.Links["claude"])
	}
	data, err := os.ReadFile(claude)
	if err != nil || string(data) != "user-authored memory" {
		t.Fatalf("user-authored agent memory must never be removed: %v %q", err, data)
	}
	if result.Links["codex"] != "removed" || result.Links["opencode"] != "removed" {
		t.Fatalf("owned links must be removed on none: %+v", result.Links)
	}
	// Applying nothing to a clean home is a no-op.
	clean, err := ApplyManagedInstructions(home, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Changed {
		t.Fatalf("idempotent none must report no change: %+v", clean)
	}
}

func TestApplyManagedInstructionsPreservesForeignFiles(t *testing.T) {
	home := testInstructionHome(t)
	preexisting := filepath.Join(home, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(preexisting), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preexisting, []byte("user-managed codex instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyManagedInstructions(home, "controller-managed content", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Links["codex"] != "conflict" {
		t.Fatalf("pre-existing user file must be reported as a conflict: %+v", result.Links)
	}
	data, err := os.ReadFile(preexisting)
	if err != nil || string(data) != "user-managed codex instructions" {
		t.Fatalf("pre-existing user-managed file must never be overwritten: %v %q", err, data)
	}
	if !strings.Contains(result.InstructionApplyReport(), "kept pre-existing file") {
		t.Fatalf("report must surface the preserved file: %q", result.InstructionApplyReport())
	}
	for _, agent := range []string{"claude", "opencode"} {
		if result.Links[agent] != "linked" {
			t.Fatalf("%s should still be linked: %+v", agent, result.Links)
		}
	}
	// A later apply with the conflict still reported, not silently resolved.
	second, err := ApplyManagedInstructions(home, "controller-managed content", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Links["codex"] != "conflict" {
		t.Fatalf("conflict must persist across re-apply: %+v", second.Links)
	}
	data, err = os.ReadFile(preexisting)
	if err != nil || string(data) != "user-managed codex instructions" {
		t.Fatalf("conflict resolution must never overwrite the user's file: %v %q", err, data)
	}
}

func TestApplyManagedInstructionsRecordsOwnershipLedger(t *testing.T) {
	home := testInstructionHome(t)
	if _, err := ApplyManagedInstructions(home, "ledger content", nil, nil); err != nil {
		t.Fatal(err)
	}
	ledger, err := readInstructionLedger(home)
	if err != nil {
		t.Fatal(err)
	}
	if digest := ledger.SHA256; digest == "" {
		t.Fatal("ledger must persist the canonical digest")
	}
	for agent, state := range ledger.Links {
		if state != "linked" {
			t.Fatalf("ledger must record %s as linked, got %q", agent, state)
		}
	}
}

func TestRestoreManagedInstructionsReassertsLinks(t *testing.T) {
	home := testInstructionHome(t)
	markdown := "restored content"
	if _, err := ApplyManagedInstructions(home, markdown, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Simulate an agent config wipe (for example, a fresh provisioned slot).
	os.Remove(filepath.Join(home, ".codex", "AGENTS.md"))
	if err := RestoreManagedInstructions(home); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(home, ".config", "vmbox", "instructions.md")
	if target := readLinkOrEmpty(t, filepath.Join(home, ".codex", "AGENTS.md")); target != canonical {
		t.Fatalf("restore must re-assert the codex link: %q", target)
	}
	data, err := os.ReadFile(canonical)
	if err != nil || string(data) != markdown {
		t.Fatalf("restore must keep canonical content: %v %q", err, data)
	}
}

func TestRestoreManagedInstructionsIsANoopWithoutCanonical(t *testing.T) {
	home := testInstructionHome(t)
	if err := RestoreManagedInstructions(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".config", "vmbox")); !os.IsNotExist(err) {
		t.Fatalf("restore must not invent content on boxes that predate managed instructions")
	}
}

func TestApplyManagedInstructionsValidatesMarkdown(t *testing.T) {
	home := testInstructionHome(t)
	if _, err := ApplyManagedInstructions(home, "text\x00with-nul", nil, nil); err == nil {
		t.Fatal("NUL bytes must be rejected")
	}
	if _, err := ApplyManagedInstructions(home, strings.Repeat("x", 65<<10), nil, nil); err == nil {
		t.Fatal("oversized markdown must be rejected")
	}
	if _, err := ApplyManagedInstructions(home, string([]byte{0xff, 0xfe, 0xfd}), nil, nil); err == nil {
		t.Fatal("invalid UTF-8 must be rejected")
	}
}

func TestApplyManagedInstructionsOwnershipRecorder(t *testing.T) {
	home := testInstructionHome(t)
	var chowned []string
	recorder := func(path string, uid, gid int) error {
		chowned = append(chowned, path)
		return nil
	}
	owner := &Ownership{UID: os.Getuid(), GID: os.Getgid()}
	if _, err := ApplyManagedInstructions(home, "managed", owner, recorder); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(chowned, "\n")
	if !strings.Contains(joined, ".vmbox-instructions-") {
		t.Fatalf("canonical file creation must hand ownership to the workload user: %s", joined)
	}
}
