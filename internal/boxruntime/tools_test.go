package boxruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func toolArchive(t *testing.T, names []string, kind byte) []byte {
	t.Helper()
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	w := tar.NewWriter(g)
	for _, name := range names {
		h := &tar.Header{Name: name, Mode: 0755, Size: 4, Typeflag: kind}
		if kind == tar.TypeSymlink {
			h.Linkname = "/tmp/outside"
			h.Size = 0
		}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			w.Write([]byte("tool"))
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestProcessToolSetupFailureDoesNotExecuteCommand(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, ".vmbox")
	dir, err := processDir(root, "tool-failure")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(base, "workspace"), 0700); err != nil {
		t.Fatal(err)
	}
	// A file in place of HOME forces a deterministic setup failure, without network.
	if err = os.WriteFile(filepath.Join(base, "home"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	task := v1.ProcessTask{ID: "tool-failure", Agent: "shell", Prompt: "touch should-not-exist", Tools: []string{"foundry"}}
	if err = writeProcessJSON(filepath.Join(dir, "task.json"), task); err != nil {
		t.Fatal(err)
	}
	if err = RunProcess(root, task.ID); err != nil {
		t.Fatal(err)
	}
	result, err := ReadProcess(root, task.ID)
	if err != nil || result.State != "setup_failed" || result.ExitCode != nil || result.FinishedAt == nil {
		t.Fatalf("result %+v, %v", result, err)
	}
	if _, err = os.Stat(filepath.Join(base, "workspace", "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("command ran after failed tool setup")
	}
}
func TestFoundryArchiveValidation(t *testing.T) {
	dir := t.TempDir()
	if err := extractFoundry(bytes.NewReader(toolArchive(t, foundryBinaries, tar.TypeReg)), dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range foundryBinaries {
		s, err := os.Stat(filepath.Join(dir, name))
		if err != nil || s.Mode().Perm() != 0700 {
			t.Fatalf("binary permissions %s: %v", name, err)
		}
	}
	for _, test := range []struct {
		names []string
		kind  byte
	}{{[]string{"../escape"}, tar.TypeReg}, {[]string{"/absolute"}, tar.TypeReg}, {[]string{"forge"}, tar.TypeSymlink}, {[]string{"forge"}, tar.TypeReg}, {[]string{"forge", "forge"}, tar.TypeReg}} {
		if err := extractFoundry(bytes.NewReader(toolArchive(t, test.names, test.kind)), t.TempDir()); err == nil {
			t.Fatal("unsafe or incomplete archive accepted")
		}
	}
}
func TestFoundryCachedInstallPreservesUserFiles(t *testing.T) {
	if runtime.GOOS != "linux" || foundrySHA[runtime.GOARCH] == "" {
		t.Skip("Linux worker preset")
	}
	home := t.TempDir()
	dest := filepath.Join(home, ".local", "share", "vmbox", "tools", "foundry_"+foundryVersion+"_linux_"+runtime.GOARCH)
	if err := os.MkdirAll(dest, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dest, ".sha256"), []byte(foundrySHA[runtime.GOARCH]), 0600)
	for _, name := range foundryBinaries {
		os.WriteFile(filepath.Join(dest, name), []byte("test fixture"), 0700)
	}
	os.Mkdir(filepath.Join(home, "bin"), 0700)
	custom := filepath.Join(home, "bin", "forge")
	os.WriteFile(custom, []byte("user-owned"), 0700)
	if err := InstallTools(context.Background(), home, []string{"foundry"}, io.Discard); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("overwrote custom binary: %v", err)
	}
	if b, _ := os.ReadFile(custom); string(b) != "user-owned" {
		t.Fatal("user binary changed")
	}
	os.Remove(custom)
	for i := 0; i < 2; i++ {
		if err := InstallTools(context.Background(), home, []string{"foundry"}, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range foundryBinaries {
		if link, err := os.Readlink(filepath.Join(home, "bin", name)); err != nil || link != filepath.Join(dest, name) {
			t.Fatalf("link %s: %v", name, err)
		}
	}
}
