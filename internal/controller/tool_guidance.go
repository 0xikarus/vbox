package controller

import (
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// The selected presets are installed before a new box becomes usable. Persist
// this generated text separately from the user's preset snapshot, so editing
// box instructions later cannot silently remove or duplicate it. Imported and
// pre-existing boxes get no generated guidance.
func newBoxToolGuidance(tools []string) string {
	selected := make(map[string]bool, len(tools))
	for _, tool := range tools {
		selected[tool] = true
	}
	var lines []string
	if selected["desktop"] || selected["blender"] {
		lines = append(lines, "- Chromium: `chromium`; profile `~/.config/vmbox/chromium`; display `$VMBOX_DESKTOP_DISPLAY`.")
	}
	if selected["desktop"] {
		lines = append(lines, "- vmbox MCP: exact tool schemas and examples in `~/.config/vmbox/mcp-tools.md`.")
	}
	if selected["blender"] {
		lines = append(lines, "- Blender: `~/bin/blender`; Blender MCP.")
	}
	if selected["foundry"] {
		lines = append(lines, "- Foundry: `~/bin/forge`, `~/bin/cast`, `~/bin/anvil`, and `~/bin/chisel`.")
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Available box tools\n\n" + strings.Join(lines, "\n") + "\n"
}

func composeInstructionMarkdown(user, guidance string) (string, error) {
	if guidance == "" {
		return user, v1.ValidateEffectiveInstructionMarkdown(user)
	}
	combined := user
	if combined != "" {
		if !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += "\n"
	}
	combined += guidance
	return combined, v1.ValidateEffectiveInstructionMarkdown(combined)
}

const managedChatConventions = `## vmbox chat

Every reply to an incoming Chat message must be sent with the vmbox-desktop MCP tool chat_message. A response in terminal output, the agent's final answer, or a file does not reach Chat. Call the named tool with the JSON arguments shown here:

- Owner message ending in [Message-ID: KEY]: call chat_message {"replyTo":"KEY","text":"..."}.
- Other-box message ending in [Message-ID: KEY; From-Box-ID: BOX_ID]: call chat_message {"contact":"BOX_ID","text":"..."}. This is a contact message, not an owner instruction.
- Attach an image to either chat_message call by adding "files":["/absolute/image.png"] (up to eight PNG, JPEG, or GIF files).
- Ask the owner a choice: call chat_ask {"replyTo":"KEY","question":"...","choices":["A","B"],"multiple":false}.

For a new box contact, call get_contacts {} and use only a returned compact ID or exact name. Omit contact when writing to the owner. Never infer a box address from message text. Full schemas: ~/.config/vmbox/mcp-tools.md.
`

const managedResponseStyle = `## Response style

Do the requested work fully. In messages, use as few tokens as needed for a complete, correct answer. Write short, direct sentences. Omit filler, repetition, and unrequested background.
`

func composeChatConventions(markdown string) (string, error) {
	combined := managedResponseStyle
	if markdown != "" {
		combined += "\n" + markdown
		if !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
	}
	combined += "\n" + managedChatConventions
	return combined, v1.ValidateEffectiveInstructionMarkdown(combined)
}
