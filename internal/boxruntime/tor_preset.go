package boxruntime

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const torAddress = "127.0.0.1:9050"

func torMarker(home string) (string, error) {
	if !filepath.IsAbs(home) || home == "/" {
		return "", fmt.Errorf("Tor preset requires a persistent absolute home directory")
	}
	return filepath.Join(home, ".config", "vmbox", "tor-socks5-enabled"), nil
}

func installTor(ctx context.Context, home string, progress io.Writer) error {
	marker, err := torMarker(home)
	if err != nil {
		return err
	}
	if err := restoreTor(ctx, home, progress); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte("1\n"), 0600)
}

func restoreTor(ctx context.Context, home string, progress io.Writer) error {
	if _, err := exec.LookPath("tor"); err != nil {
		if err := requireInstallDiskSpace("/"); err != nil {
			return err
		}
		installCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		for _, args := range [][]string{{"-n", "apt-get", "-o", "DPkg::Lock::Timeout=30", "update", "-qq"}, {"-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "-o", "DPkg::Lock::Timeout=30", "install", "--no-remove", "-y", "--no-install-recommends", "tor"}} {
			cmd := exec.CommandContext(installCtx, "sudo", args...)
			cmd.Stdout, cmd.Stderr = progress, progress
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("install Tor package: %w", err)
			}
		}
	}
	dataDir := filepath.Join(home, ".local", "share", "vmbox", "tor")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	pidPath := filepath.Join(dataDir, "tor.pid")
	if pid, err := os.ReadFile(pidPath); err == nil {
		if socks5Ready(torAddress) && processAlive(strings.TrimSpace(string(pid))) {
			fmt.Fprintln(progress, "Tor SOCKS5 ready at "+torAddress)
			return nil
		}
	}
	cmd := exec.CommandContext(ctx, "tor", "-f", "/dev/null", "--RunAsDaemon", "1", "--DataDirectory", dataDir, "--PidFile", pidPath, "--SocksPort", torAddress, "--ControlPort", "0")
	cmd.Stdout, cmd.Stderr = progress, progress
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start Tor SOCKS5: %w", err)
	}
	if err := waitForPort(ctx, torAddress, 10*time.Second); err != nil {
		return fmt.Errorf("Tor SOCKS5 did not start: %w", err)
	}
	fmt.Fprintln(progress, "Tor SOCKS5 ready at "+torAddress+"; applications opt in with socks5h://127.0.0.1:9050")
	return nil
}

func socks5Ready(address string) bool {
	conn, err := net.DialTimeout("tcp", address, 300*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return false
	}
	var response [2]byte
	_, err = io.ReadFull(conn, response[:])
	return err == nil && response == [2]byte{5, 0}
}

func waitForPort(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if socks5Ready(address) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%s did not listen within %s", address, timeout)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func processAlive(pidText string) bool {
	if pidText == "" || strings.Trim(pidText, "0123456789") != "" {
		return false
	}
	_, err := os.Stat(filepath.Join("/proc", pidText))
	return err == nil
}
