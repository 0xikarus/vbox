package taskflowruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// AgentChild receives only public task context. Its parent holds callback secrets
// outside the child's filesystem allowlist. It emits dedicated process evidence.
func AgentChild(ctx context.Context, input io.Reader, output io.Writer) error {
	b, e := io.ReadAll(io.LimitReader(input, 200001))
	if e != nil || len(b) > 200000 {
		return fmt.Errorf("child input too large")
	}
	var r Request
	if strictJSON(b, &r) != nil || validateRequest(r) != nil {
		return fmt.Errorf("invalid child request")
	}
	if e = restrictAgent(r.Workspace); e != nil {
		return e
	}
	result, e := runAgentLocal(ctx, r)
	if e != nil && result.Failure == "" {
		result.Failure = "agent_start_or_result_unavailable"
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return err
	}
	return nil
}

func agentEnvironment() []string {
	// Saved login stays in its normal home. No inherited controller, callback,
	// provider, GitHub, API-key or model override environment enters the child.
	env := []string{"LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
	for _, k := range []string{"PATH", "HOME", "USER", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func runIsolated(ctx context.Context, r Request) (AgentResult, error) {
	var result AgentResult
	exe, e := os.Executable()
	if e != nil {
		return result, e
	}
	stage, e := os.MkdirTemp("", "vmbox-task-agent-")
	if e != nil {
		return result, e
	}
	defer os.RemoveAll(stage)
	if e = os.Mkdir(filepath.Join(stage, "workspace"), 0700); e != nil {
		return result, e
	}
	for i, p := range r.Images {
		f, e := openPath(p, false)
		if e != nil {
			return result, e
		}
		b, e := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
		f.Close()
		if e != nil || len(b) > MaxImageBytes {
			return result, fmt.Errorf("invalid image")
		}
		p = filepath.Join(stage, fmt.Sprintf("image-%d", i))
		if e = os.WriteFile(p, b, 0600); e != nil {
			return result, e
		}
		r.Images[i] = p
	}
	// Copy the trusted executable out of the directory hidden from the child.
	binary, e := openPath(exe, false)
	if e != nil {
		return result, e
	}
	defer binary.Close()
	childPath := filepath.Join(stage, "runner")
	child, e := os.OpenFile(childPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		return result, e
	}
	_, e = io.Copy(child, binary)
	ce := child.Close()
	if e != nil || ce != nil {
		return result, fmt.Errorf("child staging failed")
	}
	r.Workspace = filepath.Join(stage, "workspace")
	r.ResultPath = filepath.Join(stage, "result.json")
	b, _ := json.Marshal(r)
	cmd := exec.CommandContext(context.WithoutCancel(ctx), childPath, "--agent-child")
	cmd.Dir = r.Workspace
	cmd.Env = append(agentEnvironment(), "TMPDIR="+stage)
	cmd.Stdin = bytes.NewReader(b)
	cmd.Stderr = io.Discard
	out := boundedBuffer{limit: 2 << 20}
	cmd.Stdout = &out
	group := prepareProcess(cmd, 2*time.Second)
	if e = cmd.Start(); e != nil {
		return result, fmt.Errorf("agent isolation unavailable")
	}
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		signal := func(sig syscall.Signal) {
			group.Lock()
			defer group.Unlock()
			if group.live {
				_ = syscall.Kill(-cmd.Process.Pid, sig)
			}
		}
		// Let the child kill/reap its agent group and report actual terminal evidence.
		signal(syscall.SIGTERM)
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
			signal(syscall.SIGKILL)
		}
	}()
	e = group.wait(cmd)
	close(done)
	<-stopped
	if out.truncated || json.Unmarshal(out.Bytes(), &result) != nil {
		result = AgentResult{Failure: "agent_isolation_or_process_evidence_unavailable"}
	}
	if result.ExitCode == nil && result.Signal == 0 {
		// The inbox envelope requires terminal process evidence. On startup failure
		// report the actual trusted child status with AgentEvidence=false; Observe
		// must never substitute this launcher status for an agent exit.
		result.AgentEvidence = false
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				result.Signal = int(status.Signal())
			} else {
				code := status.ExitStatus()
				result.ExitCode = &code
			}
		}
		if result.Failure == "" {
			result.Failure = "agent_startup_failed"
		}
	}
	if e != nil && result.Failure == "" {
		result.Failure = "agent_child_failed"
	}
	if result.Failure != "" {
		return result, fmt.Errorf("agent failed")
	}
	return result, nil
}
