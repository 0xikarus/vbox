package cli

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestStandaloneCreationOptions(t *testing.T) {
	if _, err := standaloneTools("blender,foundry", "tor"); err != nil {
		t.Fatal(err)
	}
	if _, err := standaloneTools("tor-socks5", "warp"); err == nil {
		t.Fatal("conflicting proxies accepted")
	}
	if _, err := standaloneTools("arbitrary-package", "none"); err == nil {
		t.Fatal("unknown package accepted")
	}
	b, err := standalonePasswordArchive("fixture-only-password")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("fixture-only-password")) {
		t.Fatal("plaintext password in archive")
	}
	tr := tar.NewReader(bytes.NewReader(b))
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(h.Name, "web-password.json") {
			found = true
			if h.Mode != 0600 || h.Uid != 10001 {
				t.Fatal("password hash permissions")
			}
		}
	}
	if !found {
		t.Fatal("password hash missing")
	}
}

func TestStandalonePasswordArchiveAcceptsAnyLength(t *testing.T) {
	for _, password := range []string{"", "x", strings.Repeat("a", 2048)} {
		if _, err := standalonePasswordArchive(password); err != nil {
			t.Fatal(err)
		}
	}
}
