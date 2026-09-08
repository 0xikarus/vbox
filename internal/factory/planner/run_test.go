package planner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const fixtureDocument = `{"version":1,"response":"Controlled executable fixture; no real agent or verification.","plan":{"markdown":"Proposed work only.","questions":[],"features":[]}}`

// Controlled shell executables test transport mechanics only, never agent capability.
func controlled(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "controlled")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func request(t *testing.T, agent string) Request {
	return Request{Agent: agent, Workspace: t.TempDir(), Prompt: "Plan $(touch injected); 'quoted'", ResultPath: filepath.Join(t.TempDir(), "result.json")}
}

const outputArg = `while [ "$#" -gt 0 ]; do
 if [ "$1" = "--output-last-message" ]; then shift; out="$1"; fi
 shift
done
cat >/dev/null
`

func TestControlledSuccess(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			r := request(t, agent)
			body := outputArg + "cat >\"$out\" <<'JSON'\n" + fixtureDocument + "\nJSON\n"
			if agent == "claude" {
				body = "cat >/dev/null\ncat <<'JSON'\n" + `{"type":"result","subtype":"success","is_error":false,"structured_output":` + fixtureDocument + "}\nJSON\n"
			}
			got, err := run(context.Background(), r, controlled(t, body))
			if err != nil {
				t.Fatal(err)
			}
			if got.ExitCode == nil || *got.ExitCode != 0 || got.Signal != 0 || got.Truncated {
				t.Fatalf("evidence: %+v", got)
			}
			b, err := os.ReadFile(r.ResultPath)
			if err != nil || string(b) != string(got.Document) {
				t.Fatal("not persisted exactly")
			}
			st, _ := os.Stat(r.ResultPath)
			if st.Mode().Perm() != 0600 {
				t.Fatal("result permissions")
			}
		})
	}
}
func TestControlledFailure(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		code, signal int
	}{{"exit", "exit 23", 23, 0}, {"signal", "kill -TERM $$", 0, 15}} {
		t.Run(tc.name, func(t *testing.T) {
			r := request(t, "codex")
			got, err := run(context.Background(), r, controlled(t, tc.body))
			if err == nil {
				t.Fatal("expected failure")
			}
			if tc.signal != 0 {
				if got.Signal != tc.signal || got.ExitCode != nil {
					t.Fatal(got)
				}
			} else if got.ExitCode == nil || *got.ExitCode != tc.code {
				t.Fatal(got)
			}
			if _, err = os.Stat(r.ResultPath); !os.IsNotExist(err) {
				t.Fatal("persisted failure")
			}
		})
	}
}
func TestControlledCancelOwnedGroup(t *testing.T) {
	r := request(t, "codex")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	got, err := run(ctx, r, controlled(t, "sleep 60 &\nwait"))
	if err != context.DeadlineExceeded || got.Signal != int(syscall.SIGKILL) || time.Since(start) > 4*time.Second {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestControlledDocuments(t *testing.T) {
	for name, doc := range map[string]string{"invalid": "not JSON", "trailing": fixtureDocument + " {}", "state": strings.Replace(fixtureDocument, `"features":[]`, `"features":[],"state":"passed"`, 1), "null": strings.Replace(fixtureDocument, `"questions":[]`, `"questions":null`, 1), "oversize": strings.Repeat("x", maxDocument+10)} {
		t.Run(name, func(t *testing.T) {
			r := request(t, "codex")
			got, err := run(context.Background(), r, controlled(t, outputArg+"cat >\"$out\" <<'JSON'\n"+doc+"\nJSON\n"))
			if err == nil {
				t.Fatal("accepted invalid document")
			}
			if len(got.Document) > maxDocument || got.Truncated != (name == "oversize") {
				t.Fatal("wrong bound")
			}
			if _, err = os.Stat(r.ResultPath); !os.IsNotExist(err) {
				t.Fatal("persisted invalid result")
			}
		})
	}
}
func TestUnsafePaths(t *testing.T) {
	r := request(t, "codex")
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(r.Workspace, link)
	for _, p := range []string{"relative", r.Workspace + "/../x", link} {
		r.Workspace = p
		if _, err := run(context.Background(), r, "must-not-run"); err == nil {
			t.Fatal(p)
		}
	}
}
func TestUnsupportedAndExisting(t *testing.T) {
	for _, kind := range []string{"agent", "claude-image", "result-exists", "result-symlink", "image-relative", "image-corrupt"} {
		t.Run(kind, func(t *testing.T) {
			r := request(t, "codex")
			switch kind {
			case "agent":
				r.Agent = "sh"
			case "claude-image":
				r.Agent = "claude"
				r.Images = []string{"/x"}
			case "result-exists":
				os.WriteFile(r.ResultPath, []byte("preserve"), 0600)
			case "result-symlink":
				os.Symlink("/nonexistent", r.ResultPath)
			case "image-relative":
				r.Images = []string{"x"}
			case "image-corrupt":
				p := filepath.Join(t.TempDir(), "image.png")
				os.WriteFile(p, []byte("not an image"), 0600)
				r.Images = []string{p}
			}
			if _, err := run(context.Background(), r, controlled(t, "exit 0")); err == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
}
func TestSchemaExcludesEvidence(t *testing.T) {
	var s map[string]any
	if json.Unmarshal(outputSchema(), &s) != nil {
		t.Fatal("schema")
	}
	for _, field := range []string{`"state"`, `"prUrl"`, `"revision"`, `"attemptId"`, `"baseSha"`} {
		if strings.Contains(string(outputSchema()), field) {
			t.Fatal(field)
		}
	}
}
func TestAtomicNoReplace(t *testing.T) {
	dir := t.TempDir()
	f, err := openPath(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.WriteFile(filepath.Join(dir, "result"), []byte("original"), 0600)
	if persist(f, "result", []byte("new")) == nil {
		t.Fatal("overwritten")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "result"))
	if string(b) != "original" {
		t.Fatal("changed")
	}
}
func TestBoundedEnvelope(t *testing.T) {
	b := boundedBuffer{limit: 5}
	n, e := b.Write([]byte("123456789"))
	if n != 9 || e != nil || b.Len() != 5 || !b.truncated {
		t.Fatal("bound")
	}
}

func TestControlledInvocation(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			r := request(t, agent)
			// Save argv and stdin as data. Prompt metacharacters must never be interpreted.
			capture := filepath.Join(r.Workspace, "argv")
			body := "printf '%s\\n' \"$@\" > '" + capture + "'\ncat > '" + filepath.Join(r.Workspace, "stdin") + "'\nexit 12\n"
			_, _ = run(context.Background(), r, controlled(t, body))
			b, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			args := string(b)
			for _, bad := range []string{"--model", "--dangerously", "bypassPermissions", "--approve-for-me"} {
				if strings.Contains(args, bad) {
					t.Fatal("unsafe argv", bad)
				}
			}
			required := []string{"--permission-mode\nplan\n", "--permission-prompts\nnone\n", "--tools\nRead,Glob,Grep\n", "--json-schema\n"}
			if agent == "codex" {
				required = []string{"exec\n", "--sandbox\nread-only\n", "approval_policy=\"never\"", "--output-schema\n", "--output-last-message\n", "--disable\nmulti_agent\n"}
			}
			for _, flag := range required {
				if !strings.Contains(args, flag) {
					t.Fatal("missing flag", flag)
				}
			}
			b, _ = os.ReadFile(filepath.Join(r.Workspace, "stdin"))
			encoded, _ := json.Marshal(r.Prompt)
			if !strings.Contains(string(b), string(encoded)) {
				t.Fatal("missing quoted request")
			}
			if _, err := os.Stat(filepath.Join(r.Workspace, "injected")); !os.IsNotExist(err) {
				t.Fatal("command injection")
			}
		})
	}
}
func TestControlledClaudeRejectsEnvelope(t *testing.T) {
	for _, body := range []string{`{"type":"result","subtype":"success","is_error":true,"structured_output":` + fixtureDocument + `}`, `{"type":"result","subtype":"error_max_turns","structured_output":` + fixtureDocument + `}`, fixtureDocument, strings.Repeat("x", maxEnvelope+1)} {
		r := request(t, "claude")
		got, err := run(context.Background(), r, controlled(t, "cat >/dev/null\ncat <<'JSON'\n"+body+"\nJSON\n"))
		if err == nil {
			t.Fatal("accepted envelope")
		}
		if len(body) > maxEnvelope && !got.Truncated {
			t.Fatalf("missing truncation: exit=%v signal=%d err=%v", exitText(got), got.Signal, err)
		}
	}
}
