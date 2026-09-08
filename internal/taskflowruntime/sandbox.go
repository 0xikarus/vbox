package taskflowruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// restrictAgent uses Landlock on the calling OS thread. The subsequent fork/exec
// inherits this irreversible filesystem boundary; no privileged helper is needed.
// Keep the thread locked until the child exits. The private job root and other
// processes' /proc files are deliberately absent from the allowlist.
func restrictAgent(workspace string) error {
	runtime.LockOSThread()
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 || abi < 3 {
		return fmt.Errorf("Landlock ABI 3 required")
	}
	const read = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR
	const all = read | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK | unix.LANDLOCK_ACCESS_FS_MAKE_SYM | unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE
	attr := unix.LandlockRulesetAttr{Access_fs: all}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), 8, 0)
	if errno != 0 {
		return fmt.Errorf("Landlock ruleset unavailable")
	}
	defer unix.Close(int(fd))
	add := func(p string, access uint64) error {
		p, e := filepath.EvalSymlinks(p)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		f, e := unix.Open(p, unix.O_PATH|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		defer unix.Close(f)
		var st unix.Stat_t
		if e = unix.Fstat(f, &st); e != nil {
			return e
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			access &= unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_TRUNCATE
		}
		rule := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(f)}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		if errno != 0 {
			return errno
		}
		return nil
	}
	// Descriptor-based no-symlink path walks open ancestor directories. Directory
	// enumeration does not grant file reads, including private job or /proc data.
	if e := add("/", unix.LANDLOCK_ACCESS_FS_READ_DIR); e != nil {
		return e
	}
	for _, p := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt", "/proc/cpuinfo", "/proc/meminfo", "/proc/sys/kernel/random/uuid"} {
		if e := add(p, read); e != nil {
			return fmt.Errorf("sandbox read rule failed")
		}
	}
	home, e := filepath.EvalSymlinks(os.Getenv("HOME"))
	if e != nil || home == "/" || strings.HasPrefix(taskRoot, home+"/") {
		return fmt.Errorf("unsafe authentication home")
	}
	// The saved selected login remains in its ordinary home, with CLI state writes.
	for _, p := range []string{home, filepath.Dir(workspace), "/dev/null", "/dev/urandom", "/dev/random"} {
		if e := add(p, all); e != nil {
			return fmt.Errorf("sandbox write rule failed")
		}
	}
	if e := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); e != nil {
		return e
	}
	_, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0)
	if errno != 0 {
		return fmt.Errorf("Landlock unavailable")
	}
	return nil
}
