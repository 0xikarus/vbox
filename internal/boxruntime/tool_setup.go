package boxruntime

import (
	"context"
	"errors"
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func toolSetupPath(home string) (string, error) {
	if !filepath.IsAbs(home) || home == "/" {
		return "", fmt.Errorf("tool setup requires a persistent absolute home")
	}
	return filepath.Join(home, ".config", "vmbox", "tool-setup.sh"), nil
}

// Custom installs are explicitly trusted workload code, never controller code.
// Keep the recipe on the volume so replacing compute can reinstall system tools.
func ConfigureToolSetup(ctx context.Context, home, script string, progress io.Writer) error {
	if err := v1.ValidateSetupScript(script); err != nil {
		return err
	}
	if strings.TrimSpace(script) == "" {
		return nil
	}
	path, err := toolSetupPath(home)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tool-setup-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(script)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	return RestoreToolSetup(ctx, home, progress)
}

func RestoreToolSetup(ctx context.Context, home string, progress io.Writer) error {
	if err := restoreDesktop(ctx, home, progress); err != nil {
		return err
	}
	if err := restoreBlender(ctx, home, progress); err != nil {
		return err
	}
	path, err := toolSetupPath(home)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, 32769))
	file.Close()
	if err != nil {
		return err
	}
	if err = v1.ValidateSetupScript(string(data)); err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	logPath := filepath.Join(filepath.Dir(path), "tool-setup.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	output := &cappedOutput{file: log, display: progress}
	fmt.Fprintln(output, "Installing custom tools (trusted box commands; timeout 5 minutes)…")
	cmd := exec.CommandContext(ctx, "/bin/bash", "-e", "-o", "pipefail", "-c", string(data))
	cmd.Dir = filepath.Join(filepath.Dir(home), "workspace")
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+filepath.Join(home, "bin")+":"+os.Getenv("PATH"))
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 3 * time.Second
	err = cmd.Run()
	syncErr := log.Sync()
	if err != nil {
		return fmt.Errorf("custom tool installation failed: %w; log: %s", errors.Join(err, ctx.Err()), logPath)
	}
	if syncErr != nil {
		return syncErr
	}
	fmt.Fprintln(progress, "Custom tools ready. Setup recipe retained and rerun on resume; keep commands idempotent.")
	return nil
}
