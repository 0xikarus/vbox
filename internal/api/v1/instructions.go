package v1

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Instruction presets are reusable, named Markdown instruction sets owned by an
// account. They are trusted user-authored agent guidance, stored separately
// from encrypted login credentials: instructions are not secrets and are never
// executed as installation scripts.
const MaxInstructionMarkdownBytes = 64 << 10

// Managed files also contain runtime-generated guidance beside the user's
// 64 KiB preset. Keep separate headroom so a valid preset remains applicable.
const MaxEffectiveInstructionMarkdownBytes = 128 << 10

// InstructionPresetMeta describes one preset without its Markdown body.
type InstructionPresetMeta struct {
	Name      string    `json:"name"`
	Revision  int64     `json:"revision"`
	SizeBytes int       `json:"sizeBytes"`
	Default   bool      `json:"default"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// InstructionPreset is one named preset together with its Markdown body.
type InstructionPreset struct {
	Name      string    `json:"name"`
	Revision  int64     `json:"revision"`
	Markdown  string    `json:"markdown"`
	SizeBytes int       `json:"sizeBytes"`
	Default   bool      `json:"default"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type InstructionPresetList struct {
	DefaultName string                  `json:"defaultName"`
	Presets     []InstructionPresetMeta `json:"presets"`
}

type InstructionPresetResponse struct {
	Preset InstructionPreset `json:"preset"`
}

type PutInstructionPresetRequest struct {
	Markdown string `json:"markdown"`
	// ExpectedRevision guards concurrent edits: when set, the update applies
	// only if the stored preset is still at that revision.
	ExpectedRevision *int64 `json:"expectedRevision,omitempty"`
}

type SetInstructionDefaultRequest struct {
	// Name selects the account-default preset; "" clears the default.
	Name string `json:"name"`
}

// InstructionSelection picks one instruction source for a box. A nil selection
// falls back to the account-default preset (or no instructions when no default
// is configured). An explicit selection always says what it means:
//   - None: no managed instructions, even when the account has a default preset
//   - Preset without Markdown: snapshot the preset's current Markdown
//   - Preset with Markdown: a box-specific copy that keeps preset provenance
//     (marked modified when the text differs from the preset at that revision)
//   - Markdown without preset: a fully custom box-specific instruction set
type InstructionSelection struct {
	None     bool   `json:"none,omitempty"`
	Preset   string `json:"preset,omitempty"`
	Markdown string `json:"markdown,omitempty"`
}

// InstructionResolution is the validated, provenance-annotated snapshot a box
// keeps. It carries preset provenance but never references live preset
// contents: editing or deleting a preset cannot change an existing box.
type InstructionResolution struct {
	Source         string `json:"source"` // none | preset | custom
	Preset         string `json:"preset,omitempty"`
	PresetRevision int64  `json:"presetRevision,omitempty"`
	Modified       bool   `json:"modified,omitempty"`
	Markdown       string `json:"markdown"`
}

// BoxInstructions is one box's snapshotted instruction state.
type BoxInstructions struct {
	Source         string     `json:"source"` // none | preset | custom
	Preset         string     `json:"preset,omitempty"`
	PresetRevision int64      `json:"presetRevision,omitempty"`
	Modified       bool       `json:"modified,omitempty"`
	Markdown       string     `json:"markdown"`
	ToolGuidance   string     `json:"-"` // generated for new boxes; never part of the user's editable preset
	UpdatedAt      time.Time  `json:"updatedAt"`
	AppliedAt      *time.Time `json:"appliedAt,omitempty"`
}

// PresetState reports how the snapshot's provenance preset looks right now.
type PresetState struct {
	Exists       bool  `json:"exists"`
	Revision     int64 `json:"revision,omitempty"`
	Stale        bool  `json:"stale,omitempty"`
	ContentDrift bool  `json:"contentDrift,omitempty"`
}

// BoxInstructionsResponse pairs the snapshot with its live-preset state and
// materialization status.
type BoxInstructionsResponse struct {
	Instructions      BoxInstructions `json:"instructions"`
	EffectiveMarkdown string          `json:"effectiveMarkdown"`
	Preset            *PresetState    `json:"preset,omitempty"`
	Pending           bool            `json:"pending"`
	Note              string          `json:"note,omitempty"`
}

func ValidateInstructionPresetName(name string) error {
	if name == "" || len(name) > 64 || !matchesInstructionName(name) {
		return fmt.Errorf("preset names use 1–64 letters, digits, dots, underscores or hyphens and start with a letter or digit")
	}
	return nil
}

func matchesInstructionName(name string) bool {
	for i, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if !ok || (i == 0 && (r == '.' || r == '_' || r == '-')) {
			return false
		}
	}
	return true
}

// ValidateInstructionMarkdown bounds trusted user-authored Markdown. It must be
// valid UTF-8 text without NUL bytes and fit the preset size budget.
func ValidateInstructionMarkdown(markdown string) error {
	return validateInstructionMarkdown(markdown, MaxInstructionMarkdownBytes)
}

func ValidateEffectiveInstructionMarkdown(markdown string) error {
	return validateInstructionMarkdown(markdown, MaxEffectiveInstructionMarkdownBytes)
}

func validateInstructionMarkdown(markdown string, maxBytes int) error {
	if len(markdown) > maxBytes {
		return fmt.Errorf("instructions are limited to %d KiB of Markdown", maxBytes/1024)
	}
	if !utf8.ValidString(markdown) || strings.ContainsRune(markdown, 0) {
		return fmt.Errorf("instructions must be UTF-8 text without NUL bytes")
	}
	return nil
}

// Validate enforces the selection grammar: fields are mutually exclusive,
// except that a preset may carry an edited box-specific copy in Markdown.
func (s InstructionSelection) Validate() error {
	if err := ValidateInstructionMarkdown(s.Markdown); err != nil {
		return err
	}
	if s.None {
		if s.Preset != "" || s.Markdown != "" {
			return fmt.Errorf("none cannot be combined with a preset or Markdown")
		}
		return nil
	}
	if s.Preset != "" {
		return ValidateInstructionPresetName(s.Preset)
	}
	if strings.TrimSpace(s.Markdown) == "" {
		return fmt.Errorf("choose a preset, enter Markdown, or select none")
	}
	return nil
}

// PresetSnapshot converts a preset plus an optional box-specific Markdown copy
// into the stored snapshot. markdown=="" means take the preset's current
// content verbatim.
func PresetSnapshot(preset InstructionPreset, markdown string) InstructionResolution {
	if markdown == "" {
		return InstructionResolution{Source: "preset", Preset: preset.Name, PresetRevision: preset.Revision, Markdown: preset.Markdown}
	}
	return InstructionResolution{Source: "preset", Preset: preset.Name, PresetRevision: preset.Revision, Modified: markdown != preset.Markdown, Markdown: markdown}
}
