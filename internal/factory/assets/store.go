// Package assets stores validated private images on a local Linux filesystem.
package assets

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	MaxBytes  int64 = 10 << 20
	MaxPixels int64 = 25_000_000
)

var (
	ErrInvalidID    = errors.New("assets: invalid identifier")
	ErrInvalidName  = errors.New("assets: invalid name")
	ErrNotFound     = errors.New("assets: not found")
	ErrUnsupported  = errors.New("assets: unsupported image format")
	ErrInvalidImage = errors.New("assets: invalid image")
	ErrTooLarge     = errors.New("assets: image exceeds limit")
	ErrCorrupt      = errors.New("assets: corrupt stored asset")
	ErrUnsafePath   = errors.New("assets: unsafe storage path")
	ErrIO           = errors.New("assets: storage I/O failure")
)

type Asset struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

// Store pins the root directory by descriptor. It is safe for concurrent use.
// Close releases the descriptor; callers must not race Close with operations.
type Store struct{ root *os.File }

func (s *Store) Close() error { return safeError(s.root.Close()) }

// New creates private directories as necessary and rejects symlinks in every
// root component. Existing storage directories must already have mode 0700.
func New(root string) (*Store, error) {
	if root == "" {
		return nil, ErrUnsafePath
	}
	for _, p := range strings.Split(root, string(os.PathSeparator)) {
		if p == ".." {
			return nil, ErrUnsafePath
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil || abs == "/" {
		return nil, ErrUnsafePath
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrIO
	}
	current := os.NewFile(uintptr(fd), "assets-root")
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i, p := range parts {
		next, e := openDir(current, p, true, i == len(parts)-1)
		current.Close()
		if e != nil {
			return nil, e
		}
		current = next
	}
	return &Store{root: current}, nil
}

func validAccount(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func validID(s string) bool { return len(s) == 32 && lowerHex(s) }
func lowerHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validName(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 255 || !utf8.ValidString(s) || strings.ContainsAny(s, "/\\") {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func safeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.ENOENT) {
		return ErrNotFound
	}
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return ErrUnsafePath
	}
	return ErrIO
}
func openDir(parent *os.File, name string, create, private bool) (*os.File, error) {
	if create {
		err := unix.Mkdirat(int(parent.Fd()), name, 0700)
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, safeError(err)
		}
		// Sync even for EEXIST: another Put may still be creating this account.
		if err := parent.Sync(); err != nil {
			return nil, ErrIO
		}
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, safeError(err)
	}
	f := os.NewFile(uintptr(fd), "assets-directory")
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, ErrIO
	}
	if private && info.Mode().Perm() != 0700 {
		f.Close()
		return nil, ErrUnsafePath
	}
	return f, nil
}
func openFile(parent *os.File, name string, create bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if create {
		flags = unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0600)
	if err != nil {
		return nil, safeError(err)
	}
	f := os.NewFile(uintptr(fd), "assets-file")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil {
		f.Close()
		return nil, ErrIO
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Nlink != 1 {
		f.Close()
		return nil, ErrUnsafePath
	}
	return f, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func readBounded(ctx context.Context, r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(contextReader{ctx, r}, max+1))
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if err != nil {
		return nil, ErrIO
	}
	if int64(len(b)) > max {
		return nil, ErrTooLarge
	}
	return b, nil
}

// decode checks dimensions before allocating the full decoded image. Only
// explicitly supported decoders are used, regardless of global registrations.
func decode(ctx context.Context, b []byte) (image.Image, string, error) {
	var config func(io.Reader) (image.Config, error)
	var full func(io.Reader) (image.Image, error)
	media := ""
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		config, full, media = png.DecodeConfig, png.Decode, "image/png"
	case bytes.HasPrefix(b, []byte{0xff, 0xd8}):
		config, full, media = jpeg.DecodeConfig, jpeg.Decode, "image/jpeg"
	default:
		return nil, "", ErrUnsupported
	}
	c, err := config(contextReader{ctx, bytes.NewReader(b)})
	if e := ctx.Err(); e != nil {
		return nil, "", e
	}
	if err != nil || c.Width <= 0 || c.Height <= 0 {
		return nil, "", ErrInvalidImage
	}
	if int64(c.Width) > MaxPixels/int64(c.Height) {
		return nil, "", ErrTooLarge
	}
	img, err := full(contextReader{ctx, bytes.NewReader(b)})
	if e := ctx.Err(); e != nil {
		return nil, "", e
	}
	if err != nil || img.Bounds().Dx() != c.Width || img.Bounds().Dy() != c.Height {
		return nil, "", ErrInvalidImage
	}
	return img, media, nil
}

type boundedBuffer struct {
	bytes.Buffer
	ctx context.Context
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(b.Len())+int64(len(p)) > MaxBytes {
		return 0, ErrTooLarge
	}
	return b.Buffer.Write(p)
}

// Put creates an account namespace on first successful upload. Account
// authorization belongs to the caller. Name is a display filename, never a path.
// Hash and size describe the re-encoded bytes, with embedded metadata removed.
func (s *Store) Put(ctx context.Context, accountID, name string, r io.Reader) (Asset, error) {
	var zero Asset
	if !validAccount(accountID) {
		return zero, ErrInvalidID
	}
	if !validName(name) {
		return zero, ErrInvalidName
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if r == nil {
		return zero, ErrInvalidImage
	}
	raw, err := readBounded(ctx, r, MaxBytes)
	if err != nil {
		return zero, err
	}
	img, media, err := decode(ctx, raw)
	if err != nil {
		return zero, err
	}
	encoded := &boundedBuffer{ctx: ctx}
	if media == "image/png" {
		err = png.Encode(encoded, img)
	} else {
		err = jpeg.Encode(encoded, img, &jpeg.Options{Quality: 95})
	}
	if err != nil {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		if errors.Is(err, ErrTooLarge) {
			return zero, ErrTooLarge
		}
		return zero, ErrInvalidImage
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return zero, ErrIO
	}
	sum := sha256.Sum256(encoded.Bytes())
	a := Asset{ID: hex.EncodeToString(random[:]), Name: name, MediaType: media, Size: int64(encoded.Len()), SHA256: hex.EncodeToString(sum[:])}
	account, err := openDir(s.root, accountID, true, true)
	if err != nil {
		return zero, err
	}
	defer account.Close()
	stageName := ".tmp-" + a.ID
	if err := unix.Mkdirat(int(account.Fd()), stageName, 0700); err != nil {
		return zero, safeError(err)
	}
	stage, err := openDir(account, stageName, false, true)
	if err != nil {
		unix.Unlinkat(int(account.Fd()), stageName, unix.AT_REMOVEDIR)
		return zero, err
	}
	defer stage.Close()
	published := false
	defer func() {
		if published {
			return
		}
		unix.Unlinkat(int(stage.Fd()), "image", 0)
		unix.Unlinkat(int(stage.Fd()), "metadata.json", 0)
		unix.Unlinkat(int(account.Fd()), stageName, unix.AT_REMOVEDIR)
	}()
	metadata, _ := json.Marshal(a)
	if err := writeDurable(stage, "image", encoded.Bytes()); err != nil {
		return zero, err
	}
	if err := writeDurable(stage, "metadata.json", metadata); err != nil {
		return zero, err
	}
	if err := stage.Sync(); err != nil {
		return zero, ErrIO
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	// A directory rename publishes the binary and metadata together. Never replace
	// a prior ID, even in the astronomically unlikely event of a random collision.
	if err := unix.Renameat2(int(account.Fd()), stageName, int(account.Fd()), a.ID, unix.RENAME_NOREPLACE); err != nil {
		return zero, safeError(err)
	}
	published = true
	if err := account.Sync(); err != nil {
		return zero, ErrIO
	}
	return a, nil
}
func writeDurable(dir *os.File, name string, data []byte) error {
	f, err := openFile(dir, name, true)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrIO
	}
	return nil
}

// Open validates persisted metadata, digest and decoded content before returning
// a read-only file positioned at offset zero. The caller owns the returned file.
func (s *Store) Open(ctx context.Context, accountID, id string) (*os.File, Asset, error) {
	var zero Asset
	if !validAccount(accountID) || !validID(id) {
		return nil, zero, ErrInvalidID
	}
	if err := ctx.Err(); err != nil {
		return nil, zero, err
	}
	account, err := openDir(s.root, accountID, false, true)
	if err != nil {
		return nil, zero, err
	}
	defer account.Close()
	dir, err := openDir(account, id, false, true)
	if err != nil {
		return nil, zero, err
	}
	defer dir.Close()
	meta, err := openFile(dir, "metadata.json", false)
	if err != nil {
		return nil, zero, storedError(err)
	}
	data, err := readBounded(ctx, meta, 4096)
	meta.Close()
	if err != nil {
		return nil, zero, storedError(err)
	}
	var a Asset
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&a) != nil || d.Decode(new(any)) != io.EOF || a.ID != id || !validName(a.Name) || a.Size <= 0 || a.Size > MaxBytes || len(a.SHA256) != 64 || !lowerHex(a.SHA256) || (a.MediaType != "image/png" && a.MediaType != "image/jpeg") {
		return nil, zero, ErrCorrupt
	}
	f, err := openFile(dir, "image", false)
	if err != nil {
		return nil, zero, storedError(err)
	}
	success := false
	defer func() {
		if !success {
			f.Close()
		}
	}()
	st, err := f.Stat()
	if err != nil {
		return nil, zero, ErrIO
	}
	if st.Size() != a.Size {
		return nil, zero, ErrCorrupt
	}
	raw, err := readBounded(ctx, f, MaxBytes)
	if err != nil {
		return nil, zero, storedError(err)
	}
	sum := sha256.Sum256(raw)
	if int64(len(raw)) != a.Size || hex.EncodeToString(sum[:]) != a.SHA256 {
		return nil, zero, ErrCorrupt
	}
	_, media, err := decode(ctx, raw)
	if err != nil {
		return nil, zero, storedError(err)
	}
	if media != a.MediaType {
		return nil, zero, ErrCorrupt
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, zero, ErrIO
	}
	if err := ctx.Err(); err != nil {
		return nil, zero, err
	}
	success = true
	return f, a, nil
}
func storedError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrUnsafePath) || errors.Is(err, ErrIO) {
		return err
	}
	return ErrCorrupt
}
