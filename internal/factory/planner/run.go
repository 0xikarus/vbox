// Package planner runs a local, authenticated planning CLI. It does not implement
// dispatch leases, asset authorization, remote staging, or verification.
package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const maxDocument = 300000
const maxEnvelope = 2 << 20

type Request struct {
	Agent, Workspace, Prompt, ResultPath string
	Images                               []string
}
type Result struct {
	ExitCode  *int
	Signal    int
	Document  []byte
	Truncated bool
}

// Run uses the CLI on the trusted process PATH, preserving its auth and model.
// A nonzero exit, cancellation, invalid/oversized output or persistence failure
// returns an error with any available process evidence. Only valid success is saved.
func Run(ctx context.Context, request Request) (Result, error) {
	return run(ctx, request, request.Agent)
}
func run(ctx context.Context, r Request, executable string) (result Result, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if r.Agent != "codex" && r.Agent != "claude" {
		return result, fmt.Errorf("unsupported planner agent")
	}
	if strings.TrimSpace(r.Prompt) == "" || len(r.Prompt) > 100000 || len(r.Images) > 8 {
		return result, fmt.Errorf("invalid prompt or image count")
	}
	if r.Agent == "claude" && len(r.Images) > 0 {
		return result, fmt.Errorf("Claude image input is not verified; unsupported")
	}
	workspace, err := openPath(r.Workspace, true)
	if err != nil {
		return result, err
	}
	defer workspace.Close()
	if !filepath.IsAbs(r.ResultPath) || filepath.Clean(r.ResultPath) != r.ResultPath || strings.ContainsRune(r.ResultPath, 0) || r.ResultPath == "/" {
		return result, fmt.Errorf("result path must be absolute and clean")
	}
	parent, err := openPath(filepath.Dir(r.ResultPath), true)
	if err != nil {
		return result, err
	}
	defer parent.Close()
	// Result must be a fresh name. This avoids stale success and overwriting source,
	// images, credentials or existing links. Callers allocate one path per attempt.
	if _, e := os.Lstat(r.ResultPath); !errors.Is(e, os.ErrNotExist) {
		return result, fmt.Errorf("result path must not exist")
	}
	stage, err := os.MkdirTemp("", "factory-planner-")
	if err != nil {
		return result, fmt.Errorf("create staging directory failed")
	}
	defer os.RemoveAll(stage)
	schema := outputSchema()
	schemaPath := filepath.Join(stage, "schema.json")
	if err = os.WriteFile(schemaPath, schema, 0600); err != nil {
		return result, fmt.Errorf("stage schema failed")
	}
	var images []string
	total := 0
	for i, path := range r.Images {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		f, e := openPath(path, false)
		if e != nil {
			return result, e
		}
		b, e := io.ReadAll(io.LimitReader(f, (10<<20)+1))
		f.Close()
		total += len(b)
		if e != nil || len(b) > 10<<20 || total > 40<<20 {
			return result, fmt.Errorf("image exceeds byte limit")
		}
		cfg, format, e := image.DecodeConfig(bytes.NewReader(b))
		if e != nil || (format != "png" && format != "jpeg") || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 25000000 {
			return result, fmt.Errorf("unsupported or oversized image; PNG/JPEG required")
		}
		if _, _, e = image.Decode(bytes.NewReader(b)); e != nil {
			return result, fmt.Errorf("corrupt image")
		}
		dest := filepath.Join(stage, fmt.Sprintf("input-%d.%s", i, format))
		if e = os.WriteFile(dest, b, 0600); e != nil {
			return result, fmt.Errorf("stage image failed")
		}
		images = append(images, dest)
	}
	prompt, _ := json.Marshal(r.Prompt)
	args := []string{}
	outPath := filepath.Join(stage, "output.json")
	if r.Agent == "codex" {
		args = []string{"exec", "--disable", "multi_agent", "--disable", "multi_agent_v2", "--sandbox", "read-only", "-c", "approval_policy=\"never\"", "--ephemeral", "--output-schema", schemaPath, "--output-last-message", outPath, "--color", "never"}
		for _, p := range images {
			args = append(args, "--image", p)
		}
		args = append(args, "-")
	} else {
		args = []string{"-p", "--permission-mode", "plan", "--permission-prompts", "none", "--tools", "Read,Glob,Grep", "--strict-mcp-config", "--no-session-persistence", "--output-format", "json", "--json-schema", string(schema), "--append-system-prompt", instructions}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	// Pin the working directory to the validated inode even if ancestors rename.
	cmd.Dir = fmt.Sprintf("/proc/self/fd/%d", workspace.Fd())
	cmd.Stdin = strings.NewReader(instructions + "\n" + string(prompt))
	var stdout boundedBuffer
	stdout.limit = maxEnvelope
	if r.Agent == "claude" {
		cmd.Stdout = &stdout
	} else {
		cmd.Stdout = io.Discard
	}
	cmd.Stderr = io.Discard // Never return CLI diagnostics that might expose secrets.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	if err = cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fmt.Errorf("planner could not start")
	}
	err = cmd.Wait()
	if st, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		if st.Signaled() {
			result.Signal = int(st.Signal())
		} else {
			code := st.ExitStatus()
			result.ExitCode = &code
		}
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		return result, fmt.Errorf("planner process failed")
	}
	if r.Agent == "claude" {
		result.Truncated = stdout.truncated
		if result.Truncated {
			return result, fmt.Errorf("planner envelope exceeds limit")
		}
		var envelope struct {
			Type       string          `json:"type"`
			Subtype    string          `json:"subtype"`
			IsError    bool            `json:"is_error"`
			Structured json.RawMessage `json:"structured_output"`
		}
		if json.Unmarshal(stdout.Bytes(), &envelope) != nil || envelope.Type != "result" || envelope.Subtype != "success" || envelope.IsError {
			return result, fmt.Errorf("Claude did not return successful structured output")
		}
		result.Document = envelope.Structured
	} else {
		f, e := openPath(outPath, false)
		if e != nil {
			return result, fmt.Errorf("planner result missing")
		}
		result.Document, e = io.ReadAll(io.LimitReader(f, maxDocument+1))
		f.Close()
		if e != nil {
			return result, fmt.Errorf("read planner result failed")
		}
	}
	if len(result.Document) > maxDocument {
		result.Document = result.Document[:maxDocument]
		result.Truncated = true
		return result, fmt.Errorf("planner document exceeds limit")
	}
	if err = validateDocument(result.Document); err != nil {
		return result, err
	}
	if err = persist(parent, filepath.Base(r.ResultPath), result.Document); err != nil {
		return result, err
	}
	return result, nil
}

type boundedBuffer struct {
	data      bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	b.data.Write(p)
	return n, nil
}

func (b *boundedBuffer) Len() int      { return b.data.Len() }
func (b *boundedBuffer) Bytes() []byte { return b.data.Bytes() }
