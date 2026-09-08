// Package staging transfers private planning inputs over controller-resolved SSH.
package staging

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/transport"
	"golang.org/x/sys/unix"
)

const (
	MaxImageBytes  = 10 << 20
	MaxImagesBytes = 40 << 20
	MaxJobBytes    = 200000
	MaxBinaryBytes = 128 << 20
)

type Stager struct {
	SSH        transport.SSH
	BinaryPath string
}
type Input struct {
	Connection                                  provider.Connection
	AttemptID, Repository, BaseSHA, SourceToken string
	Job                                         []byte
	Images                                      []Image
}
type Image struct {
	ID   string
	Data []byte
}

var identity = regexp.MustCompile(`^[0-9a-f]{32}$`)
var revision = regexp.MustCompile(`^[0-9a-f]{40}$`)
var repository = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$`)
var token = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

//go:embed stage.sh
var remoteScript string

// Stage never captures remote output or puts payloads or credentials in argv.
// SourceToken must be a controller-issued token scoped to read this repository;
// its scope cannot be inferred from the opaque token itself.
func (s Stager) Stage(ctx context.Context, in Input) error {
	if !identity.MatchString(in.AttemptID) || !repository.MatchString(in.Repository) || !revision.MatchString(in.BaseSHA) || len(in.SourceToken) > 4096 || !token.MatchString(in.SourceToken) {
		return fmt.Errorf("invalid staging identity or source credential")
	}
	if len(in.Job) == 0 || len(in.Job) > MaxJobBytes {
		return fmt.Errorf("job exceeds staging bounds")
	}
	images := append([]Image(nil), in.Images...)
	sort.Slice(images, func(i, j int) bool { return images[i].ID < images[j].ID })
	total := 0
	// Bound even empty-image metadata.
	if len(images) > 8 {
		return fmt.Errorf("too many images")
	}
	for i, im := range images {
		if !identity.MatchString(im.ID) || (i > 0 && images[i-1].ID == im.ID) {
			return fmt.Errorf("invalid or duplicate image identity")
		}
		if len(im.Data) > MaxImageBytes {
			return fmt.Errorf("image exceeds staging bounds")
		}
		total += len(im.Data)
		if total > MaxImagesBytes {
			return fmt.Errorf("images exceed staging bounds")
		}
	}
	// The binary is trusted controller input, but must still be a bounded regular
	// file. O_NONBLOCK avoids hanging on a substituted FIFO; O_NOFOLLOW rejects links.
	f, err := openBinary(s.BinaryPath)
	if err != nil {
		return fmt.Errorf("open staging binary failed")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > MaxBinaryBytes {
		return fmt.Errorf("invalid staging binary")
	}
	binary, err := io.ReadAll(io.LimitReader(f, MaxBinaryBytes+1))
	if err != nil || len(binary) == 0 || len(binary) > MaxBinaryBytes {
		return fmt.Errorf("read staging binary failed")
	}
	hash := func(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
	var header strings.Builder
	fmt.Fprintf(&header, "%s\n%s\n%s\n%d %s\n%d %s\n%d\n", in.AttemptID, in.Repository, in.BaseSHA, len(binary), hash(binary), len(in.Job), hash(in.Job), len(images))
	for _, im := range images {
		fmt.Fprintf(&header, "%s %d %s\n", im.ID, len(im.Data), hash(im.Data))
	}
	// The header is the stable manifest: sorted images, hashes, lengths and source
	// identity. The rotating credential is deliberately outside this digest.
	digest := hash([]byte(header.String()))
	readers := []io.Reader{strings.NewReader(digest + "\n" + header.String() + in.SourceToken + "\n"), bytes.NewReader(binary), bytes.NewReader(in.Job)}
	for _, im := range images {
		readers = append(readers, bytes.NewReader(im.Data))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	// Railway's SSH transport enters as the platform user. Staged paths must be
	// owned by the same unprivileged user that executes controller process tasks.
	remote := provider.AsWorkloadUser([]string{"/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "/bin/bash", "--noprofile", "--norc", "-c", remoteScript})
	result, err := s.SSH.StreamConnection(ctx, in.Connection, remote, provider.ExecOptions{Stdin: io.MultiReader(readers...), Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("private staging failed")
	}
	return nil
}

// Walk directory descriptors so even intermediate symlinks are rejected.
func openBinary(path string) (*os.File, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}
