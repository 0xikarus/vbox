package taskflowruntime

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Walk from / with directory FDs: no symlink component or traversal is accepted.
func openPath(path string, directory bool) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return nil, fmt.Errorf("path must be absolute and clean")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open path root failed")
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if path == "/" {
		parts = nil
	}
	for i, p := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, p, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, fmt.Errorf("path unavailable or unsafe")
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), path)
	st, e := f.Stat()
	if e != nil || (!directory && !st.Mode().IsRegular()) {
		f.Close()
		return nil, fmt.Errorf("path is not a regular file")
	}
	return f, nil
}
func persist(dir *os.File, name string, b []byte) error {
	tmp := fmt.Sprintf(".planner-%x", randomBytes())
	fd, err := unix.Openat(int(dir.Fd()), tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("create result failed")
	}
	defer unix.Unlinkat(int(dir.Fd()), tmp, 0)
	f := os.NewFile(uintptr(fd), tmp)
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write result failed")
	}
	if err = unix.Renameat2(int(dir.Fd()), tmp, int(dir.Fd()), name, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("replace result failed")
	}
	if err = dir.Sync(); err != nil {
		return fmt.Errorf("sync result directory failed")
	}
	return nil
}
func randomBytes() []byte {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic(err)
	}
	return b
}
