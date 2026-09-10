package boxruntime

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

const foundryVersion = "v1.8.1"

var foundrySHA = map[string]string{"amd64": "37b45855232e57624d90113b049ca54f0c92055bb5c1997fcbdc3076c7b89c10", "arm64": "27a32bd282d73018ab4d043de15ab0320b561c71b4bf3a549b130a0806e79f5c"}
var foundryBinaries = []string{"forge", "cast", "anvil", "chisel"}

func InstallTools(ctx context.Context, home string, tools []string, progress io.Writer) error {
	if err := v1.ValidateTools(tools); err != nil {
		return err
	}
	for _, tool := range tools {
		var err error
		switch tool {
		case "foundry":
			err = installFoundry(ctx, home, progress)
		case "blender":
			err = configureBlender(ctx, home, progress)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func installFoundry(ctx context.Context, home string, progress io.Writer) error {
	if runtime.GOOS != "linux" || foundrySHA[runtime.GOARCH] == "" {
		return fmt.Errorf("Foundry preset supports Linux amd64/arm64 workers")
	}
	if !filepath.IsAbs(home) || home == "/" {
		return fmt.Errorf("tool installation requires a persistent absolute home directory")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	base := filepath.Join(home, ".local", "share", "vmbox", "tools")
	if err := os.MkdirAll(base, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(base, "install.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another tool installation is active; retry after it completes")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	name := "foundry_" + foundryVersion + "_linux_" + runtime.GOARCH
	dest := filepath.Join(base, name)
	digest := foundrySHA[runtime.GOARCH]
	stamp, err := os.ReadFile(filepath.Join(dest, ".sha256"))
	if err != nil || strings.TrimSpace(string(stamp)) != digest {
		if _, err := os.Lstat(dest); err == nil {
			return fmt.Errorf("tool directory exists but does not match the pinned release: %s", dest)
		}
		temp, err := os.MkdirTemp(base, ".foundry-install-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(temp)
		fmt.Fprintf(progress, "Installing Foundry %s: downloading verified release (about 120 MiB)…\n", foundryVersion)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/foundry-rs/foundry/releases/download/"+foundryVersion+"/"+name+".tar.gz", nil)
		if err != nil {
			return err
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("download Foundry: %w", err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("Foundry download returned HTTP %d", res.StatusCode)
		}
		archive, err := os.OpenFile(filepath.Join(temp, "release.tar.gz"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer archive.Close()
		hash := sha256.New()
		n, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(res.Body, (128<<20)+1))
		if err != nil {
			return err
		}
		if n > 128<<20 || hex.EncodeToString(hash.Sum(nil)) != digest {
			return fmt.Errorf("Foundry release checksum or size verification failed")
		}
		if _, err = archive.Seek(0, 0); err != nil {
			return err
		}
		unpack := filepath.Join(temp, "unpacked")
		if err = os.Mkdir(unpack, 0700); err != nil {
			return err
		}
		fmt.Fprintln(progress, "Foundry checksum verified; installing forge, cast, anvil and chisel…")
		if err = extractFoundry(archive, unpack); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(unpack, ".sha256"), []byte(digest+"\n"), 0600); err != nil {
			return err
		}
		if err = os.Rename(unpack, dest); err != nil {
			return err
		}
	}
	bin := filepath.Join(home, "bin")
	if err = os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	for _, binary := range foundryBinaries {
		target := filepath.Join(dest, binary)
		info, err := os.Stat(target)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return fmt.Errorf("installed Foundry binary is unavailable: %s", binary)
		}
		link := filepath.Join(bin, binary)
		if existing, err := os.Readlink(link); err == nil {
			if existing == target {
				continue
			}
			return fmt.Errorf("refusing to replace existing %s link", binary)
		}
		if _, err := os.Lstat(link); err == nil {
			return fmt.Errorf("refusing to replace existing %s executable", binary)
		}
		if err = os.Symlink(target, link); err != nil {
			return err
		}
	}
	fmt.Fprintf(progress, "Foundry %s ready in %s (retained across hibernation).\n", foundryVersion, bin)
	return nil
}

func extractFoundry(source io.Reader, dest string) error {
	gzipReader, err := gzip.NewReader(source)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(header.Name)
		if name != filepath.Base(name) || name == ".." || filepath.IsAbs(name) {
			return fmt.Errorf("unsafe Foundry archive path")
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf("Foundry archive contains a non-regular file")
		}
		wanted := false
		for _, binary := range foundryBinaries {
			if name == binary {
				wanted = true
			}
		}
		if !wanted {
			continue
		}
		if seen[name] || header.Size <= 0 || header.Size > 256<<20 {
			return fmt.Errorf("invalid Foundry binary entry")
		}
		seen[name] = true
		file, err := os.OpenFile(filepath.Join(dest, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, reader)
		syncErr := file.Sync()
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if len(seen) != len(foundryBinaries) {
		return fmt.Errorf("Foundry archive is missing required binaries")
	}
	return nil
}
