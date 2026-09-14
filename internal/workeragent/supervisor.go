package workeragent

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Lock prevents a second supervisor/agent from displacing a healthy process.
// The lock lives outside the workspace and is never inherited by exec children.
func Lock(path string) (io.Closer, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("worker process lock unavailable")
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		file.Close()
		return nil, errors.New("worker process lock must be private")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("worker process already running")
	}
	return file, nil
}

// Supervise restarts only the agent child. Workspace runtime processes are not
// children of this supervisor and no service/volume lifecycle calls occur here.
func Supervise(ctx context.Context, configPath string, report func(string)) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return supervise(ctx, configPath, []string{executable, "--config", configPath}, report)
}

func supervise(ctx context.Context, configPath string, argv []string, report func(string)) error {
	lock, err := Lock(configPath + ".supervisor.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		child := exec.CommandContext(ctx, argv[0], argv[1:]...)
		child.Stdin = nil
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		// Workers need their private config and system trust store, not Railway
		// management tokens or workload login credentials inherited from startup.
		child.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C.UTF-8"}
		child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
		child.WaitDelay = 5 * time.Second
		_ = child.Run()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if report != nil {
			report("worker agent exited; restarting")
		}
		if time.Since(started) >= time.Minute {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return ctx.Err()
}
