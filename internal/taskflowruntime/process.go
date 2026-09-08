package taskflowruntime

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Each command owns its leader until wait reaps it. Cancel and cleanup
// share this gate; neither may signal a group after ownership is released.
type processGroup struct {
	sync.Mutex
	live bool
}

func prepareProcess(cmd *exec.Cmd, delay time.Duration) *processGroup {
	group := &processGroup{live: true}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		group.Lock()
		defer group.Unlock()
		if !group.live {
			return os.ErrProcessDone
		}
		e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if e == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return e
	}
	cmd.WaitDelay = delay
	return group
}
func (group *processGroup) wait(cmd *exec.Cmd) error {
	var info unix.Siginfo
	var e error
	for {
		e = unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if e != syscall.EINTR {
			break
		}
	}
	group.Lock()
	if e == nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	group.live = false
	group.Unlock()
	return errors.Join(e, cmd.Wait())
}
