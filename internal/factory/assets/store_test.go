package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

var ctx = context.Background()

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private")
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, root
}
func picture(t *testing.T, format string) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 17, 11))
	for y := 0; y < 11; y++ {
		for x := 0; x < 17; x++ {
			im.SetNRGBA(x, y, color.NRGBA{uint8(x * 15), uint8(y * 20), 91, 255})
		}
	}
	var b bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&b, im)
	} else {
		err = jpeg.Encode(&b, im, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func put(t *testing.T, s *Store, account string) Asset {
	t.Helper()
	a, err := s.Put(ctx, account, "photo.png", bytes.NewReader(picture(t, "png")))
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func requireError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}
func chunk(kind string, data []byte) []byte {
	b := make([]byte, len(data)+12)
	binary.BigEndian.PutUint32(b, uint32(len(data)))
	copy(b[4:], kind)
	copy(b[8:], data)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[4:len(b)-4]))
	return b
}
func withMetadata(b []byte, format string) []byte {
	if format == "png" {
		c := chunk("tEXt", []byte("Comment\x00PRIVATE-LOCATION-SECRET"))
		return append(append(append([]byte{}, b[:33]...), c...), b[33:]...)
	}
	payload := []byte("Exif\x00\x00PRIVATE-LOCATION-SECRET")
	c := []byte{0xff, 0xe1, 0, byte(len(payload) + 2)}
	c = append(c, payload...)
	return append(append(append([]byte{}, b[:2]...), c...), b[2:]...)
}
func TestRoundTripRestartAndMetadataStripping(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			s, root := newStore(t)
			raw := withMetadata(picture(t, format), format)
			raw = append(raw, []byte("TRAILING-PRIVATE-SECRET")...)
			a, err := s.Put(ctx, "account-a", "display."+format, bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if !validID(a.ID) || a.MediaType != "image/"+format {
				t.Fatalf("bad asset: %+v", a)
			}
			s.Close()
			s, err = New(root)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			f, got, err := s.Open(ctx, "account-a", a.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if got != a {
				t.Fatalf("metadata mismatch: %+v != %+v", got, a)
			}
			b, err := io.ReadAll(f)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(b)
			if a.Size != int64(len(b)) || a.SHA256 != hex.EncodeToString(sum[:]) {
				t.Fatal("size/hash mismatch")
			}
			if bytes.Contains(b, []byte("SECRET")) || bytes.Contains(b, []byte("Exif")) {
				t.Fatal("metadata leaked")
			}
			im, typ, err := image.Decode(bytes.NewReader(b))
			if err != nil || typ != format {
				t.Fatalf("decode: %s %v", typ, err)
			}
			if im.Bounds() != image.Rect(0, 0, 17, 11) {
				t.Fatal("dimensions changed")
			}
			if format == "png" {
				original, _, _ := image.Decode(bytes.NewReader(raw))
				for y := 0; y < 11; y++ {
					for x := 0; x < 17; x++ {
						if color.NRGBAModel.Convert(im.At(x, y)) != color.NRGBAModel.Convert(original.At(x, y)) {
							t.Fatal("PNG pixels changed")
						}
					}
				}
			}
			if _, err = f.Write([]byte("x")); err == nil {
				t.Fatal("returned writable file")
			}
			for _, p := range []string{root, filepath.Join(root, "account-a"), filepath.Join(root, "account-a", a.ID), filepath.Join(root, "account-a", a.ID, "image"), filepath.Join(root, "account-a", a.ID, "metadata.json")} {
				st, err := os.Stat(p)
				if err != nil {
					t.Fatal(err)
				}
				want := os.FileMode(0600)
				if st.IsDir() {
					want = 0700
				}
				if st.Mode().Perm() != want {
					t.Fatalf("mode %s: %o", p, st.Mode().Perm())
				}
			}
			entries, _ := os.ReadDir(filepath.Join(root, "account-a"))
			if len(entries) != 1 || entries[0].Name() != a.ID {
				t.Fatal("staging residue")
			}
		})
	}
}
func TestIsolationAndIdentifiers(t *testing.T) {
	s, _ := newStore(t)
	a := put(t, s, "account-a")
	put(t, s, "account-b")
	for _, account := range []string{"account-b", "unknown"} {
		f, _, err := s.Open(ctx, account, a.ID)
		requireError(t, err, ErrNotFound)
		if f != nil {
			t.Fatal("cross-account file")
		}
	}
	for _, id := range []string{"", ".", "..", "../account-a", "a/b", "a\\b", "/absolute", "%2e%2e", "a\x00b", strings.Repeat("a", 129), "å"} {
		_, err := s.Put(ctx, id, "a.png", bytes.NewReader(picture(t, "png")))
		requireError(t, err, ErrInvalidID)
		_, _, err = s.Open(ctx, id, a.ID)
		requireError(t, err, ErrInvalidID)
		_, _, err = s.Open(ctx, "account-a", id)
		requireError(t, err, ErrInvalidID)
	}
	for _, name := range []string{"", "..", "../a.png", "a/b", "a\\b", "a\n.png", strings.Repeat("x", 256), string([]byte{0xff})} {
		_, err := s.Put(ctx, "account-a", name, bytes.NewReader(picture(t, "png")))
		requireError(t, err, ErrInvalidName)
	}
}
func TestRejectInput(t *testing.T) {
	s, root := newStore(t)
	pngBytes := picture(t, "png")
	cases := []struct {
		name string
		b    []byte
		want error
	}{
		{"empty", nil, ErrUnsupported},
		{"mime-only", []byte("image/png"), ErrUnsupported},
		{"webp", []byte("RIFF\x16\x00\x00\x00WEBPVP8L\x09\x00\x00\x00\x2f\x00\x00\x00\x00\x07\x10\x11\xfd\x0f\x00\x00"), ErrUnsupported},
		{"gif", []byte("GIF89a"), ErrUnsupported},
		{"png-header", pngBytes[:33], ErrInvalidImage},
		{"png-truncated", pngBytes[:len(pngBytes)-15], ErrInvalidImage},
		{"jpeg-truncated", picture(t, "jpeg")[:30], ErrInvalidImage},
		{"oversized-input", make([]byte, MaxBytes+1), ErrTooLarge},
	}
	corrupt := append([]byte{}, pngBytes...)
	corrupt[45] ^= 0xff
	cases = append(cases, struct {
		name string
		b    []byte
		want error
	}{"bad-crc", corrupt, ErrInvalidImage})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Put(ctx, "a", "a.png", bytes.NewReader(tc.b))
			requireError(t, err, tc.want)
		})
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("invalid upload created storage")
	}
}
func largeHeader(width, height uint32) []byte {
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data, width)
	binary.BigEndian.PutUint32(data[4:], height)
	data[8] = 8
	data[9] = 2
	return append([]byte("\x89PNG\r\n\x1a\n"), chunk("IHDR", data)...)
}
func TestDecodedPixelLimit(t *testing.T) {
	s, _ := newStore(t)
	for _, wh := range [][2]uint32{{5001, 5000}, {100_000, 100_000}, {25_000_001, 1}} {
		_, err := s.Put(ctx, "a", "a.png", bytes.NewReader(largeHeader(wh[0], wh[1])))
		requireError(t, err, ErrTooLarge)
	}
	// Real image at the exact 25MP boundary must roundtrip.
	im := image.NewGray(image.Rect(0, 0, 5000, 5000))
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	a, err := s.Put(ctx, "a", "large.png", &b)
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := s.Open(ctx, "a", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}
func TestEncodedByteBoundary(t *testing.T) {
	s, _ := newStore(t)
	b := picture(t, "png")
	b = append(b, make([]byte, int(MaxBytes)-len(b))...)
	if _, err := s.Put(ctx, "a", "a.png", bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	b = append(b, 0)
	_, err := s.Put(ctx, "a", "a.png", bytes.NewReader(b))
	requireError(t, err, ErrTooLarge)
	w := &boundedBuffer{ctx: ctx}
	if _, err := w.Write(make([]byte, MaxBytes)); err != nil {
		t.Fatal(err)
	}
	_, err = w.Write([]byte{0})
	requireError(t, err, ErrTooLarge)
}
func TestCorruptPersistence(t *testing.T) {
	for _, what := range []string{"image-byte", "image-missing", "image-oversize", "metadata-missing", "metadata-json", "metadata-oversize", "id", "size", "hash", "type", "name", "unknown-field", "trailing-json", "forged-image"} {
		t.Run(what, func(t *testing.T) {
			s, root := newStore(t)
			a := put(t, s, "a")
			dir := filepath.Join(root, "a", a.ID)
			mp := filepath.Join(dir, "metadata.json")
			ip := filepath.Join(dir, "image")
			write := func(p string, b []byte) {
				t.Helper()
				if err := os.WriteFile(p, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch what {
			case "image-byte":
				b, _ := os.ReadFile(ip)
				b[len(b)/2] ^= 1
				write(ip, b)
			case "image-missing":
				os.Remove(ip)
			case "image-oversize":
				if err := os.Truncate(ip, MaxBytes+1); err != nil {
					t.Fatal(err)
				}
			case "metadata-missing":
				os.Remove(mp)
			case "metadata-json":
				write(mp, []byte("{"))
			case "metadata-oversize":
				write(mp, make([]byte, 4097))
			case "unknown-field":
				b, _ := json.Marshal(a)
				b = append(b[:len(b)-1], []byte(",\"other\":1}")...)
				write(mp, b)
			case "trailing-json":
				b, _ := json.Marshal(a)
				write(mp, append(b, []byte(" {}")...))
			default:
				switch what {
				case "id":
					a.ID = strings.Repeat("0", 32)
				case "size":
					a.Size++
				case "hash":
					a.SHA256 = strings.Repeat("0", 64)
				case "type":
					a.MediaType = "image/jpeg"
				case "name":
					a.Name = "../x"
				case "forged-image":
					b := []byte("not an image")
					write(ip, b)
					a.Size = int64(len(b))
					sum := sha256.Sum256(b)
					a.SHA256 = hex.EncodeToString(sum[:])
				}
				b, _ := json.Marshal(a)
				write(mp, b)
			}
			_, _, err := s.Open(ctx, "a", filepath.Base(dir))
			requireError(t, err, ErrCorrupt)
		})
	}
}
func TestSymlinksAndSpecialFiles(t *testing.T) {
	for _, target := range []string{"root", "ancestor", "account", "asset", "image", "metadata.json", "hardlink", "fifo", "permissions"} {
		t.Run(target, func(t *testing.T) {
			s, root := newStore(t)
			a := put(t, s, "a")
			outside := t.TempDir()
			switch target {
			case "root":
				p := filepath.Join(t.TempDir(), "link")
				os.Symlink(root, p)
				_, err := New(p)
				requireError(t, err, ErrUnsafePath)
				return
			case "ancestor":
				p := filepath.Join(t.TempDir(), "link")
				os.Symlink(filepath.Dir(root), p)
				_, err := New(filepath.Join(p, "private"))
				requireError(t, err, ErrUnsafePath)
				return
			case "account":
				os.Rename(filepath.Join(root, "a"), filepath.Join(root, "old"))
				os.Symlink(filepath.Join(root, "old"), filepath.Join(root, "a"))
			case "asset":
				p := filepath.Join(root, "a", a.ID)
				os.Rename(p, filepath.Join(outside, "asset"))
				os.Symlink(filepath.Join(outside, "asset"), p)
			case "image", "metadata.json":
				p := filepath.Join(root, "a", a.ID, target)
				os.Rename(p, filepath.Join(outside, target))
				os.Symlink(filepath.Join(outside, target), p)
			case "hardlink":
				if err := os.Link(filepath.Join(root, "a", a.ID, "image"), filepath.Join(outside, "linked")); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				p := filepath.Join(root, "a", a.ID, "image")
				os.Remove(p)
				if err := unix.Mkfifo(p, 0600); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				os.Chmod(filepath.Join(root, "a", a.ID, "image"), 0644)
			}
			_, _, err := s.Open(ctx, "a", a.ID)
			requireError(t, err, ErrUnsafePath)
			if target == "account" {
				_, err = s.Put(ctx, "a", "a.png", bytes.NewReader(picture(t, "png")))
				requireError(t, err, ErrUnsafePath)
			}
		})
	}
}
func TestContextAndReaderErrors(t *testing.T) {
	s, root := newStore(t)
	a := put(t, s, "a")
	c, cancel := context.WithCancel(ctx)
	cancel()
	_, err := s.Put(c, "a", "a.png", bytes.NewReader(picture(t, "png")))
	requireError(t, err, context.Canceled)
	_, _, err = s.Open(c, "a", a.ID)
	requireError(t, err, context.Canceled)
	_, err = s.Put(ctx, "a", "a.png", errorReader{})
	requireError(t, err, ErrIO)
	if strings.Contains(err.Error(), "sensitive") {
		t.Fatal("reader error leaked")
	}
	c, cancel = context.WithCancel(ctx)
	_, err = s.Put(c, "a", "a.png", cancelReader{cancel: cancel})
	requireError(t, err, context.Canceled)
	entries, _ := os.ReadDir(filepath.Join(root, "a"))
	if len(entries) != 1 {
		t.Fatal("failed upload published")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("sensitive source path") }

type cancelReader struct{ cancel context.CancelFunc }

func (r cancelReader) Read(p []byte) (int, error) { r.cancel(); p[0] = 0; return 1, nil }
func TestConcurrentPublication(t *testing.T) {
	s, root := newStore(t)
	raw := picture(t, "png")
	second, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var wg sync.WaitGroup
	ids := make(chan string, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			writer := s
			if i%2 == 0 {
				writer = second
			}
			a, err := writer.Put(ctx, "a", "photo.png", bytes.NewReader(raw))
			if err != nil {
				t.Error(err)
				return
			}
			f, got, err := s.Open(ctx, "a", a.ID)
			if err != nil {
				t.Error(err)
				return
			}
			f.Close()
			if got != a {
				t.Error("metadata mismatch")
			}
			ids <- a.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("duplicate ID")
		}
		seen[id] = true
	}
	if len(seen) != 24 {
		t.Fatal("missing writes")
	}
	entries, _ := os.ReadDir(filepath.Join(root, "a"))
	if len(entries) != 24 {
		t.Fatal("partial publication or staging residue")
	}
	// Crash residue is invisible and does not prevent restart or new uploads.
	os.Mkdir(filepath.Join(root, "a", ".tmp-abandoned"), 0700)
	_, _, err = s.Open(ctx, "a", ".tmp-abandoned")
	requireError(t, err, ErrInvalidID)
	restarted, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	put(t, restarted, "a")
}

// This context cancels at the pre-publication check, after both files exist.
type publicationCancel struct {
	context.Context
	root string
}

func (c publicationCancel) Err() error {
	entries, _ := os.ReadDir(filepath.Join(c.root, "a"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			if _, err := os.Stat(filepath.Join(c.root, "a", e.Name(), "metadata.json")); err == nil {
				return context.Canceled
			}
		}
	}
	return nil
}
func TestCanceledPublicationCleansStaging(t *testing.T) {
	s, root := newStore(t)
	_, err := s.Put(publicationCancel{ctx, root}, "a", "a.png", bytes.NewReader(picture(t, "png")))
	requireError(t, err, context.Canceled)
	entries, err := os.ReadDir(filepath.Join(root, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("canceled upload left files")
	}
}
func TestPinnedRootAndAccountSymlinkRace(t *testing.T) {
	s, root := newStore(t)
	moved := root + "-moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	// Replacing the configured path cannot redirect an already open store.
	a := put(t, s, "a")
	f, _, err := s.Open(ctx, "a", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("escaped pinned root")
	}
	raw := picture(t, "png")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			os.Rename(filepath.Join(moved, "a"), filepath.Join(moved, "parked"))
			os.Symlink(outside, filepath.Join(moved, "a"))
			os.Remove(filepath.Join(moved, "a"))
			os.Rename(filepath.Join(moved, "parked"), filepath.Join(moved, "a"))
		}
	}()
	for i := 0; i < 40; i++ {
		_, err := s.Put(ctx, "a", "a.png", bytes.NewReader(raw))
		if err != nil && !errors.Is(err, ErrUnsafePath) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrIO) {
			t.Error(err)
		}
	}
	close(stop)
	<-done
	entries, _ = os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("symlink race escaped account")
	}
}
func TestNewRejectsUnsafeRoot(t *testing.T) {
	for _, p := range []string{"", "/", filepath.Join(t.TempDir(), "x") + "/../private"} {
		_, err := New(p)
		requireError(t, err, ErrUnsafePath)
	}
	root := filepath.Join(t.TempDir(), "public")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	_, err := New(root)
	requireError(t, err, ErrUnsafePath)
}
