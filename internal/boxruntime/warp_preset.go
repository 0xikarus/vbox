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
	if warpInstalled() {
		return nil
	}
	return installWARPPackage(ctx, progress)
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
	fmt.Fprintln(progress, "Installing Cloudflare WARP client from the official package repository (no registration or connection)…")
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
