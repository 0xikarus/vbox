package sharedworker

import (
	"errors"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// IsolationTier is the filesystem isolation actually in force for a workspace.
// It is reported to the controller and never inferred by clients.
type IsolationTier string

const (
	// IsolationTierUID is the legacy behavior: a distinct Unix UID, a clean
	// environment, dropped capabilities and DAC-protected private directories.
	// It is not filesystem isolation and must be reported as such.
	IsolationTierUID IsolationTier = "uid"
	// IsolationTierNamespace adds a per-exec user, mount and PID namespace.
	IsolationTierNamespace IsolationTier = "namespace"
)

// IsolationMode is the operator request. It is distinct from the tier that was
// actually achieved, so an unsupported request degrades explicitly.
type IsolationMode string

const (
	IsolationModeUID       IsolationMode = "uid"
	IsolationModeNamespace IsolationMode = "namespace"
	IsolationModeAuto      IsolationMode = "auto"
)

// Capabilities records which namespace primitives the probe observed working.
type Capabilities struct {
	UserNamespace  bool   `json:"userNamespace"`
	MountNamespace bool   `json:"mountNamespace"`
	PIDNamespace   bool   `json:"pidNamespace"`
	Bubblewrap     bool   `json:"bubblewrap"`
	BubblewrapPath string `json:"bubblewrapPath,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// IsolationStatus is the resolved, reportable isolation state of one worker.
type IsolationStatus struct {
	Mode         IsolationMode `json:"mode"`
	Tier         IsolationTier `json:"tier"`
	Reason       string        `json:"reason,omitempty"`
	Capabilities Capabilities  `json:"capabilities,omitempty"`
}

// ParseIsolationMode validates VMBOX_SHARED_ISOLATION. The empty string keeps the
// legacy uid behavior.
func ParseIsolationMode(value string) (IsolationMode, error) {
	switch IsolationMode(strings.ToLower(strings.TrimSpace(value))) {
	case "", IsolationModeUID:
		return IsolationModeUID, nil
	case IsolationModeNamespace:
		return IsolationModeNamespace, nil
	case IsolationModeAuto:
		return IsolationModeAuto, nil
	default:
		return "", errors.New("VMBOX_SHARED_ISOLATION must be uid, namespace or auto")
	}
}

// SelectIsolation resolves the requested mode against probed capabilities. It
// never returns the namespace tier unless every primitive it depends on works,
// and it always records why a request was not honored.
func SelectIsolation(mode IsolationMode, caps Capabilities) IsolationStatus {
	namespaceReady := caps.Bubblewrap && caps.UserNamespace && caps.MountNamespace && caps.PIDNamespace
	switch mode {
	case IsolationModeNamespace:
		if namespaceReady {
			return IsolationStatus{Mode: mode, Tier: IsolationTierNamespace, Capabilities: caps}
		}
		return IsolationStatus{Mode: mode, Tier: IsolationTierUID, Reason: "requested namespace isolation is unavailable: " + capabilityReason(caps), Capabilities: caps}
	case IsolationModeAuto:
		if namespaceReady {
			return IsolationStatus{Mode: mode, Tier: IsolationTierNamespace, Capabilities: caps}
		}
		return IsolationStatus{Mode: mode, Tier: IsolationTierUID, Reason: "namespace isolation not confirmed on this host: " + capabilityReason(caps), Capabilities: caps}
	default:
		return IsolationStatus{Mode: IsolationModeUID, Tier: IsolationTierUID, Reason: "namespace isolation disabled by configuration", Capabilities: caps}
	}
}

func capabilityReason(caps Capabilities) string {
	if caps.Reason != "" {
		return caps.Reason
	}
	switch {
	case !caps.Bubblewrap:
		return "bubblewrap is not installed"
	case !caps.UserNamespace:
		return "user namespaces are unavailable"
	case !caps.MountNamespace:
		return "mount namespaces are unavailable"
	case !caps.PIDNamespace:
		return "pid namespaces are unavailable"
	default:
		return "unknown"
	}
}

// Metadata is the per-box reporting contract. It is copied into every
// connection and box label so a client can tell an isolated box from a
// non-isolated one.
func (s IsolationStatus) Metadata() map[string]string {
	tier, mode := s.Tier, s.Mode
	if tier == "" {
		tier = IsolationTierUID
	}
	if mode == "" {
		mode = IsolationModeUID
	}
	reason := s.Reason
	if reason == "" && tier == IsolationTierUID {
		reason = "namespace isolation not active"
	}
	return map[string]string{
		"isolationTier":   string(tier),
		"isolationMode":   string(mode),
		"isolationReason": reason,
	}
}

// workloadPaths is the path layout as seen inside the box. The uid tier exposes
// the host workspace root; the namespace tier hides it behind /data.
type workloadPaths struct {
	Root       string
	Home       string
	Workspace  string
	Tmp        string
	Run        string
	RuntimeDir string
}

func (r *LinuxRuntime) workloadPaths(workspace Workspace, tier IsolationTier) workloadPaths {
	host := r.workspaceRoot(workspace)
	if tier == IsolationTierNamespace {
		return workloadPaths{
			Root:       "/data",
			Home:       "/data/home",
			Workspace:  "/data/workspace",
			Tmp:        "/tmp",
			Run:        "/run",
			RuntimeDir: "/data/.vmbox",
		}
	}
	return workloadPaths{
		Root:       host,
		Home:       filepath.Join(host, "home"),
		Workspace:  filepath.Join(host, "workspace"),
		Tmp:        filepath.Join(host, "tmp"),
		Run:        filepath.Join(host, "run"),
		RuntimeDir: filepath.Join(host, ".vmbox"),
	}
}

// workloadEnv builds the clean environment. It never inherits the supervisor's
// environment, so the supervisor token cannot leak into a box.
func workloadEnv(paths workloadPaths, account *user.User, display int) []string {
	return []string{
		"HOME=" + paths.Home,
		"USER=" + account.Username,
		"LOGNAME=" + account.Username,
		"SHELL=/bin/bash",
		"LANG=C.UTF-8",
		"TERM=xterm-256color",
		"PATH=" + paths.Home + "/bin:" + paths.Home + "/.local/bin:/opt/bun/bin:/opt/foundry/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"VMBOX_WORKSPACE_ROOT=" + paths.Root,
		"VMBOX_RUNTIME_DIR=" + paths.RuntimeDir,
		"VMBOX_DESKTOP_DISPLAY=:" + strconv.Itoa(display),
		"TMPDIR=" + paths.Tmp,
		"TMUX_TMPDIR=" + paths.Tmp,
		"XDG_RUNTIME_DIR=" + paths.Run,
	}
}

// namespaceArgv builds the bubblewrap invocation for the namespace tier. The
// host root is bound read-only, the box's own directory is bound at /data, and
// the box's persistent tmp/run directories are bound over the shared ones so
// tmux and VNC state survive separate executions. Everything else on the host,
// including sibling workspaces and /data/.shared-worker, is no longer reachable.
func namespaceArgv(hostRoot string, env []string, chdir string, argv []string) []string {
	args := []string{
		"--die-with-parent",
		"--unshare-user",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--unshare-cgroup-try",
		"--ro-bind", "/", "/",
		"--bind", hostRoot, "/data",
		// bwrap resolves bind sources against the host root, never against a
		// bind made earlier in the same invocation, so these name the box's own
		// directories on the host. Naming "/data/tmp" here would mean the host's
		// shared /data/tmp: absent on the current layout, which fails every exec,
		// and a directory shared by every box if it ever existed.
		"--bind", filepath.Join(hostRoot, "tmp"), "/tmp",
		"--bind", filepath.Join(hostRoot, "run"), "/run",
		"--bind", filepath.Join(hostRoot, "tmp"), "/var/tmp",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/dev/shm",
		"--clearenv",
	}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		args = append(args, "--setenv", key, value)
	}
	if chdir != "" {
		args = append(args, "--chdir", chdir)
	}
	args = append(args, "--")
	return append(args, argv...)
}
