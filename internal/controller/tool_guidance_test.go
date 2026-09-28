package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type guidanceSyncProvider struct {
	provider.Provider
	payload []byte
	argv    []string
}

func (p *guidanceSyncProvider) Exec(_ context.Context, _ string, argv []string, options provider.ExecOptions) (provider.ExecResult, error) {
	p.argv = argv
	var err error
	p.payload, err = io.ReadAll(options.Stdin)
	if err != nil {
		return provider.ExecResult{}, err
	}
	return provider.ExecResult{Stdout: fmt.Sprintf("%x\n", sha256.Sum256(p.payload))}, nil
}

func TestNewBoxToolGuidanceFollowsSelectedOptions(t *testing.T) {
	if got := newBoxToolGuidance(nil); got != "" {
		t.Fatalf("unselected tools must not add guidance: %q", got)
	}
	foundry := newBoxToolGuidance([]string{"foundry"})
	if !strings.Contains(foundry, "~/bin/forge") || strings.Contains(foundry, "Chromium") || strings.Contains(foundry, "Blender") {
		t.Fatalf("Foundry-only guidance is wrong: %q", foundry)
	}
	blender := newBoxToolGuidance([]string{"blender"})
	if !strings.Contains(blender, "~/bin/blender") || !strings.Contains(blender, "Chromium") || !strings.Contains(blender, "~/.config/vmbox/chromium") {
		t.Fatalf("Blender must include Blender and desktop guidance: %q", blender)
	}
	desktop := newBoxToolGuidance([]string{"desktop"})
	if !strings.Contains(desktop, "~/.config/vmbox/mcp-tools.md") {
		t.Fatalf("desktop guidance must point agents at the MCP guide: %q", desktop)
	}
	all := newBoxToolGuidance([]string{"foundry", "blender", "desktop"})
	for _, name := range []string{"Chromium", "Blender", "Foundry"} {
		if n := strings.Count(all, name+":"); n != 1 {
			t.Fatalf("%s should appear once, got %d: %q", name, n, all)
		}
	}
}

func TestComposeInstructionMarkdownPreservesPresetAndBounds(t *testing.T) {
	base := "# User rules\nKeep answers short.\n"
	guide := newBoxToolGuidance([]string{"desktop"})
	got, err := composeInstructionMarkdown(base, guide)
	if err != nil || !strings.HasPrefix(got, base) || !strings.Contains(got, "Chromium:") {
		t.Fatalf("compose: %v %q", err, got)
	}
	if got, err := composeInstructionMarkdown("", ""); err != nil || got != "" {
		t.Fatalf("empty instructions: %v %q", err, got)
	}
	if got, err := composeInstructionMarkdown("", guide); err != nil || got != guide {
		t.Fatalf("tool-only instructions: %v %q", err, got)
	}
	if _, err := composeInstructionMarkdown(strings.Repeat("x", v1.MaxInstructionMarkdownBytes), guide); err != nil {
		t.Fatalf("a valid full-size preset must retain room for generated guidance: %v", err)
	}
	_, err = composeInstructionMarkdown(strings.Repeat("x", v1.MaxEffectiveInstructionMarkdownBytes), guide)
	if err == nil {
		t.Fatal("combined instructions over the effective runtime limit must be rejected")
	}
}

func TestManagedChatConventionsStayFocusedOnVmboxCalls(t *testing.T) {
	got, err := composeChatConventions("# Owner preset\n")
	if err != nil || !strings.HasPrefix(got, "## Response style\n\n") || !strings.Contains(got, "\n\n# Owner preset\n\n## vmbox chat\n") {
		t.Fatalf("compose: %v %q", err, got)
	}
	if !strings.Contains(got, "few tokens as needed for a complete, correct answer") {
		t.Fatal("managed concise response guidance is missing")
	}
	if empty, err := composeChatConventions(""); err != nil || !strings.Contains(empty, "## Response style\n\n") || !strings.Contains(empty, "\n\n## vmbox chat\n") {
		t.Fatalf("empty snapshot must retain managed guidance: %v %q", err, empty)
	}
	for _, example := range []string{
		`Every reply to an incoming Chat message must be sent with the vmbox-desktop MCP tool chat_message.`,
		`A response in terminal output, the agent's final answer, or a file does not reach Chat.`,
		`chat_message {"replyTo":"KEY","text":"..."}`,
		`chat_message {"contact":"BOX_ID","text":"..."}`,
		`"files":["/absolute/image.png"]`,
		`chat_ask {"replyTo":"KEY","question":"...","choices":["A","B"],"multiple":false}`,
		`get_contacts {}`,
	} {
		if !strings.Contains(got, example) {
			t.Fatalf("missing exact MCP example %q", example)
		}
	}
	if strings.Contains(got, "Long-running local servers") || strings.Contains(got, "npm run dev") {
		t.Fatal("unrelated server instructions remain in managed chat guidance")
	}
}

func TestSyncBoxInstructionsIncludesOnlyNewBoxToolReferences(t *testing.T) {
	for _, tc := range []struct {
		name, user, guidance, want string
	}{
		{name: "new-tool-only-box", guidance: newBoxToolGuidance([]string{"desktop", "blender"}), want: newBoxToolGuidance([]string{"desktop", "blender"})},
		{name: "existing-box", user: "# User preset", want: "# User preset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := testStore(t)
			mock.ExpectQuery(`SELECT source,COALESCE\(preset_name`).WithArgs("account", "box").
				WillReturnRows(sqlmock.NewRows([]string{"source", "preset_name", "preset_revision", "modified", "markdown", "tool_guidance", "updated_at", "applied_at"}).
					AddRow("none", "", int64(0), false, tc.user, tc.guidance, time.Now(), nil))
			mock.ExpectExec(`INSERT INTO box_instruction_snapshots\(account_id,box_id,source,markdown,applied_at\)`).WithArgs("account", "box").WillReturnResult(sqlmock.NewResult(0, 1))
			transport := &guidanceSyncProvider{}
			server := &Server{Store: store}
			if err := server.syncBoxInstructions(context.Background(), transport, "account", "box", "worker"); err != nil {
				t.Fatal(err)
			}
			if len(transport.argv) != 2 || transport.argv[0] != "vmbox-runtime" || transport.argv[1] != "sync-instructions" {
				t.Fatalf("wrong runtime command: %v", transport.argv)
			}
			var body struct {
				Markdown string `json:"markdown"`
			}
			want, composeErr := composeChatConventions(tc.want)
			if composeErr != nil {
				t.Fatal(composeErr)
			}
			if err := json.Unmarshal(transport.payload, &body); err != nil || body.Markdown != want {
				t.Fatalf("synced markdown: %v %q, want %q", err, body.Markdown, want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
