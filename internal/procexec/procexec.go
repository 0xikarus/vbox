package procexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type Runner interface {
	Run(context.Context, []string, io.Reader, io.Writer, io.Writer) (Result, error)
}

type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) (Result, error) {
	if len(argv) == 0 || argv[0] == "" {
		return Result{}, fmt.Errorf("empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
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
