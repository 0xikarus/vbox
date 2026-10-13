package boxruntime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const warpKeyring = "/usr/share/keyrings/cloudflare-warp-archive-keyring.gpg"
const warpSource = "/etc/apt/sources.list.d/cloudflare-client.list"

var warpCodename = regexp.MustCompile(`^[a-z]+$`)

func warpAptEntry(release []byte) (string, error) {
	values := map[string]string{}
	for _, line := range strings.Split(string(release), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = strings.Trim(value, `"'`)
		}
	}
	if (values["ID"] != "debian" && values["ID"] != "ubuntu") || !warpCodename.MatchString(values["VERSION_CODENAME"]) {
		return "", fmt.Errorf("Cloudflare WARP preset requires a supported Debian or Ubuntu worker image")
	}
	return "deb [signed-by=" + warpKeyring + "] https://pkg.cloudflareclient.com/ " + values["VERSION_CODENAME"] + " main\n", nil
}

func warpMarker(home string) (string, error) {
	if !filepath.IsAbs(home) || home == "/" {
		return "", fmt.Errorf("WARP preset requires a persistent absolute home directory")
	}
	return filepath.Join(home, ".config", "vmbox", "warp-enabled"), nil
}

func installWARP(ctx context.Context, home string, progress io.Writer) error {
	marker, err := warpMarker(home)
	if err != nil {
		return err
	}
	if err := restoreWARP(ctx, home, progress); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte("1\n"), 0600)
}

func restoreWARP(ctx context.Context, home string, progress io.Writer) error {
	marker, err := warpMarker(home)
	if err != nil {
		return err
	}
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		// Initial installation calls installWARP before it creates the marker.
	} else if err != nil {
		return err
	}
	if !warpInstalled() {
		if err := installWARPPackage(ctx, progress); err != nil {
			return err
		}
	}
	statusPath := filepath.Join(home, ".config", "vmbox", "warp-status")
	if !warpConnected(ctx) {
		if info, err := os.Stat(statusPath); err == nil && time.Since(info.ModTime()) < 10*time.Minute {
			previous, _ := os.ReadFile(statusPath)
			if strings.HasPrefix(string(previous), "unavailable:") && exec.CommandContext(ctx, "warp-cli", "--accept-tos", "status").Run() == nil {
				// A failed edge handshake should not delay every later task.
				return nil
			}
		}
	}
	connectionErr := connectWARP(ctx, home, progress)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	status := "connected\n"
	if connectionErr != nil {
		status = "unavailable: " + connectionErr.Error() + "\n"
		fmt.Fprintln(progress, "Cloudflare WARP proxy unavailable: "+connectionErr.Error()+"; the box can still use its direct connection. Check warp-cli status.")
	}
	if err := os.MkdirAll(filepath.Dir(statusPath), 0700); err != nil {
		return err
	}
	return os.WriteFile(statusPath, []byte(status), 0600)
}

func connectWARP(ctx context.Context, home string, progress io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if warpConnected(ctx) {
		return nil
	}
	// Proxy mode avoids a device-wide route and NET_ADMIN, which worker
	// containers intentionally do not have.
	status := exec.CommandContext(ctx, "warp-cli", "--accept-tos", "status")
	if err := status.Run(); err != nil {
		logPath := filepath.Join(home, ".config", "vmbox", "warp-svc.log")
		if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
			return err
		}
		log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		cmd := exec.Command("sudo", "-n", "warp-svc")
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			log.Close()
			return fmt.Errorf("start WARP service: %w", err)
		}
		cmd.Process.Release()
		log.Close()
		for i := 0; i < 30; i++ {
			if exec.CommandContext(ctx, "warp-cli", "--accept-tos", "status").Run() == nil {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("WARP service unavailable: %w", ctx.Err())
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "warp-cli", append([]string{"--accept-tos"}, args...)...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("warp-cli %s: %w", strings.Join(args, " "), err)
		}
		return nil
	}
	if err := exec.CommandContext(ctx, "warp-cli", "--accept-tos", "registration", "show").Run(); err != nil {
		// warp-cli status may succeed while the daemon still reports "Daemon
		// Startup" and rejects registration. Give it a short, bounded retry.
		for attempt := 0; attempt < 20; attempt++ {
			err := run("registration", "new")
			if err == nil {
				break
			}
			if exec.CommandContext(ctx, "warp-cli", "--accept-tos", "registration", "show").Run() == nil {
				break
			}
			if attempt == 19 {
				return err
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("WARP registration unavailable: %w", ctx.Err())
			case <-time.After(time.Second):
			}
		}
	}
	for _, args := range [][]string{{"tunnel", "protocol", "set", "MASQUE"}, {"mode", "proxy"}, {"proxy", "port", "40000"}, {"connect"}} {
		if err := run(args...); err != nil {
			return err
		}
	}
	for i := 0; i < 30; i++ {
		if warpConnected(ctx) {
			fmt.Fprintln(progress, "Cloudflare WARP SOCKS5 connected at 127.0.0.1:40000; applications must opt in")
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Cloudflare WARP could not connect: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("Cloudflare WARP could not connect; check Cloudflare edge UDP access with warp-cli status")
}

func warpConnected(ctx context.Context) bool {
	output, err := exec.CommandContext(ctx, "warp-cli", "--accept-tos", "status").Output()
	return err == nil && strings.Contains(string(output), "Connected") && socks5Ready("127.0.0.1:40000")
}

func warpInstalled() bool {
	for _, name := range []string{"warp-cli", "warp-svc"} {
		if _, err := exec.LookPath(name); err != nil {
			return false
		}
	}
	return true
}

func installWARPPackage(ctx context.Context, progress io.Writer) error {
	if err := requireInstallDiskSpace("/"); err != nil {
		return err
	}
	release, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return err
	}
	entry, err := warpAptEntry(release)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	run := func(stdin io.Reader, name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, progress, progress
		return cmd.Run()
	}
	fmt.Fprintln(progress, "Installing Cloudflare WARP client from the official package repository…")
	// A previous attempt may have created these files under a restrictive sudo
	// umask. Repair them before apt reads the repository on a retry.
	for _, path := range []string{warpKeyring, warpSource} {
		if _, err := os.Stat(path); err == nil {
			if err := run(nil, "sudo", "-n", "chmod", "0644", path); err != nil {
				return fmt.Errorf("make Cloudflare repository readable: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := run(nil, "sudo", "-n", "apt-get", "-o", "DPkg::Lock::Timeout=30", "update", "-qq"); err != nil {
		return fmt.Errorf("refresh package index: %w", err)
	}
	if err := run(nil, "sudo", "-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "-o", "DPkg::Lock::Timeout=30", "install", "--no-remove", "-y", "--no-install-recommends", "ca-certificates", "curl", "gnupg"); err != nil {
		return fmt.Errorf("install WARP repository prerequisites: %w", err)
	}
	key, err := os.CreateTemp("", "vbox-warp-key-*.gpg")
	if err != nil {
		return err
	}
	defer os.Remove(key.Name())
	defer key.Close()
	if err := run(nil, "curl", "-fsSL", "-o", key.Name(), "https://pkg.cloudflareclient.com/pubkey.gpg"); err != nil {
		return fmt.Errorf("download Cloudflare package key: %w", err)
	}
	if err := run(nil, "sudo", "-n", "gpg", "--yes", "--dearmor", "--output", warpKeyring, key.Name()); err != nil {
		return fmt.Errorf("install Cloudflare package key: %w", err)
	}
	if err := run(strings.NewReader(entry), "sudo", "-n", "tee", warpSource); err != nil {
		return fmt.Errorf("configure Cloudflare package repository: %w", err)
	}
	if err := run(nil, "sudo", "-n", "chmod", "0644", warpKeyring, warpSource); err != nil {
		return fmt.Errorf("make Cloudflare repository readable: %w", err)
	}
	if err := run(nil, "sudo", "-n", "apt-get", "-o", "DPkg::Lock::Timeout=30", "update", "-qq"); err != nil {
		return fmt.Errorf("refresh Cloudflare package index: %w", err)
	}
	if err := run(nil, "sudo", "-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "-o", "DPkg::Lock::Timeout=30", "install", "--no-remove", "-y", "--no-install-recommends", "cloudflare-warp"); err != nil {
		return fmt.Errorf("install cloudflare-warp: %w", err)
	}
	if !warpInstalled() {
		return fmt.Errorf("cloudflare-warp package did not provide warp-cli and warp-svc")
	}
	return nil
}
