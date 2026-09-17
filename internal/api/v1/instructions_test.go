package v1

import (
	"strings"
	"testing"
)

func TestValidateInstructionPresetName(t *testing.T) {
	for _, name := range []string{"general", "code-review", "blender.artist", "My_Preset-2", "a"} {
		if err := ValidateInstructionPresetName(name); err != nil {
			t.Errorf("valid name %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"", ".hidden", "-dash", "_underline", ".dot", "has space", "has/slash", strings.Repeat("x", 65)} {
		if err := ValidateInstructionPresetName(name); err == nil {
			t.Errorf("invalid name %q accepted", name)
		}
	}
}

func TestValidateInstructionMarkdown(t *testing.T) {
	if err := ValidateInstructionMarkdown(""); err != nil {
		t.Errorf("empty markdown must be valid at the type level: %v", err)
	}
	if err := ValidateInstructionMarkdown("# Title\nSome **Markdown** body\n"); err != nil {
		t.Errorf("plain markdown rejected: %v", err)
	}
	if err := ValidateInstructionMarkdown("# 🎨 unicode OK ✓"); err != nil {
		t.Errorf("unicode content rejected: %v", err)
	}
	if err := ValidateInstructionMarkdown("has\x00nul"); err == nil {
		t.Error("NUL bytes must be rejected")
	}
	if err := ValidateInstructionMarkdown(string([]byte{0xff, 0xfe})); err == nil {
		t.Error("invalid UTF-8 must be rejected")
	}
	if err := ValidateInstructionMarkdown(string(make([]byte, MaxInstructionMarkdownBytes+1))); err == nil {
		t.Error("oversized markdown must be rejected")
	}
	if err := ValidateInstructionMarkdown(string(make([]rune, MaxInstructionMarkdownBytes/2))); err == nil {
		t.Errorf("NUL-only content must be rejected")
	}
}

func TestInstructionSelectionValidate(t *testing.T) {
	valid := []InstructionSelection{
		{None: true},
		{Preset: "general"},
		{Preset: "general", Markdown: "# edited copy"},
		{Markdown: "# custom"},
	}
	for _, selection := range valid {
		if err := selection.Validate(); err != nil {
			t.Errorf("valid selection %+v rejected: %v", selection, err)
		}
	}
	invalid := []InstructionSelection{
		{},                                // nothing chosen
		{None: true, Preset: "x"},         // none + preset
		{None: true, Markdown: "text"},    // none + markdown
		{Preset: "has space"},             // invalid preset name
		{Preset: "../escape"},             // path-ish preset name
		{Markdown: ""},                    // empty custom
		{Markdown: "   \n\t "},            // blank custom
		{Preset: "x", Markdown: "a\x00b"}, // NUL in markdown
		{Preset: "x", Markdown: string([]byte{0xff, 0xfe})}, // invalid UTF-8
	}
	for _, selection := range invalid {
		if err := selection.Validate(); err == nil {
			t.Errorf("invalid selection %+v accepted", selection)
		}
	}
}

func TestPresetSnapshotSemantics(t *testing.T) {
	preset := InstructionPreset{Name: "general", Revision: 7, Markdown: "# Rules"}
	verbatim := PresetSnapshot(preset, "")
	if verbatim.Source != "preset" || verbatim.Preset != "general" || verbatim.PresetRevision != 7 || verbatim.Markdown != "# Rules" || verbatim.Modified {
		t.Fatalf("verbatim snapshot must copy preset as-is: %+v", verbatim)
	}
	edited := PresetSnapshot(preset, "# Rules\nExtra line")
	if !edited.Modified || edited.Preset != "general" || edited.PresetRevision != 7 || edited.Markdown != "# Rules\nExtra line" {
		t.Fatalf("edited snapshot must keep provenance and mark the edit: %+v", edited)
	}
	verbatimBack := PresetSnapshot(preset, "# Rules")
	if verbatimBack.Modified {
		t.Fatalf("text equal to the preset must not be marked modified: %+v", verbatimBack)
	}
}
