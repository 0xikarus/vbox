package boxruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const blenderVersion = "5.1.2"

// Blender and its MCP server are bundled in worker images; tests point these
// elsewhere so a host image does not short-circuit the download path.
var (
	pinnedBlenderImage    = "/opt/vmbox/blender-" + blenderVersion + "/blender"
	pinnedBlenderMCPImage = "/opt/vmbox/blender-mcp-" + blenderMCPVersion + "/bin/blender-mcp"
)

const blenderReleaseSHA = "aaccb355f50183979b698bcce7467103a76261b5fa59f4972295842662a285fb"
const blenderReleaseURL = "https://download.blender.org/release/Blender5.1/blender-5.1.2-linux-x64.tar.xz"

func installPinnedBlender(ctx context.Context, home string, progress io.Writer) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("Blender %s preset requires Linux amd64", blenderVersion)
	}
	if !filepath.IsAbs(home) || home == "/" {
		return fmt.Errorf("Blender requires a persistent absolute home")
	}
	imageBinary := pinnedBlenderImage
	if _, err := os.Stat(filepath.Join(home, "bin", "blender")); os.IsNotExist(err) {
		if output, err := exec.CommandContext(ctx, imageBinary, "--version").Output(); err == nil && strings.HasPrefix(string(output), "Blender "+blenderVersion+"\n") {
			return linkPinnedBlender(home, imageBinary)
		}
	}
	if target, err := os.Readlink(filepath.Join(home, "bin", "blender")); err == nil && target == imageBinary {
		output, err := exec.CommandContext(ctx, imageBinary, "--version").Output()
		if err != nil || !strings.HasPrefix(string(output), "Blender "+blenderVersion+"\n") {
			return fmt.Errorf("image Blender installation failed verification")
		}
		return nil
	}
	base := filepath.Join(home, ".local", "share", "vmbox", "tools")
	if err := os.MkdirAll(base, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(base, "blender-install.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another Blender installation is active")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	dest := filepath.Join(base, "blender-"+blenderVersion)
	binary := filepath.Join(dest, "blender")
	ready := false
	if stamp, err := os.ReadFile(filepath.Join(dest, ".sha256")); err == nil && strings.TrimSpace(string(stamp)) == blenderReleaseSHA {
		output, err := exec.CommandContext(ctx, binary, "--version").Output()
		ready = err == nil && strings.HasPrefix(string(output), "Blender "+blenderVersion+"\n")
	}
	if !ready {
		if WorkspaceRoot() != "/data" {
			return fmt.Errorf("shared worker image must include Blender %s and its dependencies", blenderVersion)
		}
		for _, args := range [][]string{{"update"}, {"install", "--no-remove", "-y", "--no-install-recommends", "pipx", "python3-venv", "xz-utils", "libxxf86vm1", "libxfixes3", "libxi6", "libxrender1", "libxkbcommon0", "libgl1", "libsm6", "libice6"}} {
			command := exec.CommandContext(ctx, "sudo", append([]string{"-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "-o", "DPkg::Lock::Timeout=30"}, args...)...)
			command.Stdout, command.Stderr = progress, progress
			if err := command.Run(); err != nil {
				return fmt.Errorf("install Blender dependencies: %w", err)
			}
		}
		if _, err := os.Lstat(dest); err == nil {
			stamp, stampErr := os.ReadFile(filepath.Join(dest, ".sha256"))
			output, versionErr := exec.CommandContext(ctx, binary, "--version").Output()
			if stampErr == nil && strings.TrimSpace(string(stamp)) == blenderReleaseSHA && versionErr == nil && strings.HasPrefix(string(output), "Blender "+blenderVersion+"\n") {
				return linkPinnedBlender(home, binary)
			}
			return fmt.Errorf("retained Blender installation failed verification; refusing to overwrite it")
		}
		staging, err := os.MkdirTemp(base, ".blender-install-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(staging)
		fmt.Fprintf(progress, "Downloading Blender %s from blender.org (verified SHA-256)…\n", blenderVersion)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, blenderReleaseURL, nil)
		if err != nil {
			return err
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("Blender download returned HTTP %d", response.StatusCode)
		}
		archive := filepath.Join(staging, "release.tar.xz")
		file, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		hash := sha256.New()
		count, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, (512<<20)+1))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if count > 512<<20 || hex.EncodeToString(hash.Sum(nil)) != blenderReleaseSHA {
			return fmt.Errorf("Blender release checksum or size verification failed")
		}
		unpacked := filepath.Join(staging, "unpacked")
		if err := os.Mkdir(unpacked, 0700); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, "tar", "--extract", "--xz", "--no-same-owner", "--no-same-permissions", "--strip-components=1", "--file", archive, "--directory", unpacked)
		command.Stdout, command.Stderr = progress, progress
		if err := command.Run(); err != nil {
			return fmt.Errorf("extract Blender: %w", err)
		}
		output, err := exec.CommandContext(ctx, filepath.Join(unpacked, "blender"), "--version").Output()
		if err != nil || !strings.HasPrefix(string(output), "Blender "+blenderVersion+"\n") {
			return fmt.Errorf("downloaded Blender version verification failed")
		}
		if err := os.WriteFile(filepath.Join(unpacked, ".sha256"), []byte(blenderReleaseSHA+"\n"), 0600); err != nil {
			return err
		}
		if err := os.Rename(unpacked, dest); err != nil {
			return err
		}
	}
	return linkPinnedBlender(home, binary)
}

func linkPinnedBlender(home, binary string) error {
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	link := filepath.Join(bin, "blender")
	if target, err := os.Readlink(link); err == nil && target == binary {
		return nil
	}
	if _, err := os.Lstat(link); err == nil {
		return fmt.Errorf("refusing to replace existing Blender executable or link")
	}
	return os.Symlink(binary, link)
}

func registerOpenCodeBlender(home, server string) error {
	lockDir := filepath.Join(home, ".config", "vmbox")
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(lockDir, "mcp-registration.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another MCP registration is active")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	configRoot := os.Getenv("XDG_CONFIG_HOME")
	if configRoot == "" {
		configRoot = filepath.Join(home, ".config")
	}
	path := filepath.Join(configRoot, "opencode", "opencode.json")
	if custom := os.Getenv("OPENCODE_CONFIG"); custom != "" {
		path = custom
	} else if _, err := os.Stat(filepath.Join(filepath.Dir(path), "opencode.jsonc")); err == nil {
		return fmt.Errorf("OpenCode JSONC configuration requires explicit Blender MCP registration")
	}
	config := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && (json.Unmarshal(data, &config) != nil || config == nil) {
		return fmt.Errorf("invalid OpenCode configuration; preserved unchanged")
	}
	entries := map[string]json.RawMessage{}
	if raw, exists := config["mcp"]; exists && (json.Unmarshal(raw, &entries) != nil || entries == nil) {
		return fmt.Errorf("invalid OpenCode MCP configuration; preserved unchanged")
	}
	if _, exists := entries["blender"]; exists {
		return nil
	}
	entries["blender"], _ = json.Marshal(map[string]any{"type": "local", "command": []string{server}, "enabled": true, "environment": map[string]string{"BLENDER_HOST": "127.0.0.1", "BLENDER_PORT": blenderMCPPort(), "BLENDER_MCP_SAFE_MODE": "1", "DISABLE_TELEMETRY": "true"}})
	config["mcp"], _ = json.Marshal(entries)
	return writeJSONAtomic(path, config, 0600)
}
