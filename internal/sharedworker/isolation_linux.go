package sharedworker

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"
)

// ProbeCapabilities runs the exact unprivileged pattern used for workloads and
// records which primitives worked. It is deliberately conservative: any failure
// leaves the namespace tier unavailable and attaches a reason.
func ProbeCapabilities(ctx context.Context) Capabilities {
	caps := Capabilities{}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		caps.Reason = "bubblewrap is not installed"
		return caps
	}
	caps.Bubblewrap = true
	caps.BubblewrapPath = bwrap
	if data, err := os.ReadFile("/proc/sys/user/max_user_namespaces"); err == nil {
		if limit, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && limit <= 0 {
			caps.Reason = "user namespaces are disabled (user.max_user_namespaces=0)"
			return caps
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var command *exec.Cmd
	if os.Geteuid() == 0 {
		if _, err := exec.LookPath("setpriv"); err != nil {
			caps.Reason = "setpriv is not installed"
			return caps
		}
		account, ok := probeAccount()
		if !ok {
			caps.Reason = "no unprivileged probe account is available"
			return caps
		}
		args := append([]string{
			"--reuid", account.Uid, "--regid", account.Gid, "--clear-groups",
			"--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", bwrap,
		}, namespaceProbeArgs()...)
		command = exec.CommandContext(probeCtx, "setpriv", args...)
	} else {
		// Already unprivileged: probe in place rather than dropping to another uid.
		command = exec.CommandContext(probeCtx, bwrap, namespaceProbeArgs()...)
	}
	command.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	output, err := command.CombinedOutput()
	if err != nil {
		caps.Reason = "bubblewrap namespace smoke test failed: " + probeFailure(output, err)
		return caps
	}
	caps.UserNamespace, caps.MountNamespace, caps.PIDNamespace = true, true, true
	return caps
}

func namespaceProbeArgs() []string {
	return []string{
		"--die-with-parent", "--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup-try",
		"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/dev/shm",
		"--", "sh", "-c", `test "$(id -u)" = 0 && test ! -w /usr`,
	}
}

func probeFailure(output []byte, err error) string {
	if text := strings.TrimSpace(string(output)); text != "" {
		return text
	}
	return err.Error()
}

// probeAccount returns an existing unprivileged account. The workload UID range
// is only allocated per workspace, so the probe uses the image's nobody account
// to exercise unprivileged user-namespace creation.
func probeAccount() (*user.User, bool) {
	for _, id := range []string{"65534", "nobody"} {
		account, err := user.LookupId(id)
		if err == nil && account.Uid != "" && account.Gid != "" {
			return account, true
		}
		account, err = user.Lookup(id)
		if err == nil && account.Uid != "" && account.Gid != "" {
			return account, true
		}
	}
	return nil, false
}
