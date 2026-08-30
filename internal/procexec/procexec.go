package procexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type Runner interface {
	Run(context.Context, []string, io.Reader, io.Writer, io.Writer) (Result, error)
}

type OSRunner struct {
	// Env overlays the current process environment for this runner only. It is
	// used by account-scoped provider instances without changing global state.
	Env map[string]string
}

func (r OSRunner) Run(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) (Result, error) {
	if len(argv) == 0 || argv[0] == "" {
		return Result{}, fmt.Errorf("empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if len(r.Env) > 0 {
		cmd.Env = os.Environ()
		for key, value := range r.Env {
			prefix := key + "="
			filtered := cmd.Env[:0]
			for _, item := range cmd.Env {
				if !strings.HasPrefix(item, prefix) {
					filtered = append(filtered, item)
				}
			}
			cmd.Env = filtered
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Stdin = stdin
	var outBuf, errBuf bytes.Buffer
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	cmd.Stdout = io.MultiWriter(stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(stderr, &errBuf)
	err := cmd.Run()
	result := Result{Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

func CommandExists(name string) error {
	_, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("required command %q is unavailable: %w", name, err)
	}
	return nil
}
