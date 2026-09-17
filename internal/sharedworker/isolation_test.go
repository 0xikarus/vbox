package sharedworker

import (
	"context"
	"os/user"
	"strings"
	"testing"
)

func TestProbeCapabilitiesIsConservative(t *testing.T) {
	caps := ProbeCapabilities(context.Background())
	ready := caps.Bubblewrap && caps.UserNamespace && caps.MountNamespace && caps.PIDNamespace
	if ready {
		if caps.BubblewrapPath == "" {
			t.Fatal("bubblewrap reported working without a path")
		}
	} else if caps.Reason == "" {
		t.Fatal("unavailable namespace capabilities must carry a reason")
	}
	t.Logf("host isolation capabilities: %+v", caps)
}

func TestParseIsolationMode(t *testing.T) {
	cases := map[string]IsolationMode{
		"":           IsolationModeUID,
		"uid":        IsolationModeUID,
		"UID":        IsolationModeUID,
		" namespace": IsolationModeNamespace,
		"auto":       IsolationModeAuto,
	}
	for input, want := range cases {
		got, err := ParseIsolationMode(input)
		if err != nil || got != want {
			t.Fatalf("ParseIsolationMode(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := ParseIsolationMode("sandbox"); err == nil {
		t.Fatal("unsupported isolation mode accepted")
	}
}

func TestSelectIsolationNeverClaimsUnsupportedNamespace(t *testing.T) {
	ready := Capabilities{Bubblewrap: true, UserNamespace: true, MountNamespace: true, PIDNamespace: true}

	auto := SelectIsolation(IsolationModeAuto, ready)
	if auto.Tier != IsolationTierNamespace || auto.Reason != "" {
		t.Fatalf("auto with full capabilities = %+v", auto)
	}

	requested := SelectIsolation(IsolationModeNamespace, ready)
	if requested.Tier != IsolationTierNamespace {
		t.Fatalf("explicit namespace request = %+v", requested)
	}

	partial := SelectIsolation(IsolationModeNamespace, Capabilities{Bubblewrap: true, Reason: "no user namespaces"})
	if partial.Tier != IsolationTierUID || partial.Reason == "" {
		t.Fatalf("unsupported namespace request did not degrade explicitly: %+v", partial)
	}
	if !strings.Contains(partial.Reason, "no user namespaces") {
		t.Fatalf("downgrade reason lost probe detail: %q", partial.Reason)
	}

	disabled := SelectIsolation(IsolationModeUID, ready)
	if disabled.Tier != IsolationTierUID || disabled.Reason == "" {
		t.Fatalf("uid mode must report a reason: %+v", disabled)
	}
}

func TestIsolationMetadataDefaultsToUID(t *testing.T) {
	metadata := (IsolationStatus{}).Metadata()
	if metadata["isolationTier"] != string(IsolationTierUID) || metadata["isolationMode"] != string(IsolationModeUID) {
		t.Fatalf("empty status must report uid tier: %+v", metadata)
	}
	if metadata["isolationReason"] == "" {
		t.Fatal("uid tier must explain itself")
	}
}

func TestNamespaceArgvHidesHostAndKeepsPersistentState(t *testing.T) {
	root := "/data/workspaces/0123456789abcdef0123456789abcdef"
	env := []string{"HOME=/data/home", "TMPDIR=/tmp", "PATH=/usr/bin:/bin"}
	argv := []string{"sh", "-c", "true"}
	args := namespaceArgv(root, env, "/data/workspace", argv)

	mustContainSequence(t, args, "--unshare-user")
	mustContainSequence(t, args, "--unshare-pid")
	mustContainSequence(t, args, "--ro-bind", "/", "/")
	mustContainSequence(t, args, "--bind", root, "/data")
	// Sources must name this box's own directories on the host. bwrap resolves
	// them against the host root, so a literal "/data/tmp" would be the shared
	// host path, not this box's tmp inside the sandbox.
	mustContainSequence(t, args, "--bind", root+"/tmp", "/tmp")
	mustContainSequence(t, args, "--bind", root+"/run", "/run")
	mustContainSequence(t, args, "--bind", root+"/tmp", "/var/tmp")
	for index := 0; index+2 < len(args); index++ {
		if args[index] != "--bind" {
			continue
		}
		source, destination := args[index+1], args[index+2]
		if destination == "/data" {
			continue
		}
		if !strings.HasPrefix(source, root+"/") {
			t.Fatalf("bind source %q for %q is not inside the box root %q", source, destination, root)
		}
	}
	mustContainSequence(t, args, "--tmpfs", "/dev/shm")
	mustContainSequence(t, args, "--clearenv")
	mustContainSequence(t, args, "--setenv", "HOME", "/data/home")
	mustContainSequence(t, args, "--chdir", "/data/workspace")

	if len(args) < len(argv)+1 {
		t.Fatalf("argv not preserved: %v", args)
	}
	tail := args[len(args)-len(argv):]
	for index := range argv {
		if tail[index] != argv[index] {
			t.Fatalf("argv tail = %v, want %v", tail, argv)
		}
	}
	if args[len(args)-len(argv)-1] != "--" {
		t.Fatalf("command must follow a -- separator: %v", args)
	}
}

func TestNamespaceArgvRejectsBareEnvEntries(t *testing.T) {
	args := namespaceArgv("/data/workspaces/x", []string{"NO_EQUALS", "A=1"}, "/data/workspace", []string{"true"})
	if strings.Contains(strings.Join(args, " "), "NO_EQUALS") {
		t.Fatalf("malformed env entry was forwarded: %v", args)
	}
	mustContainSequence(t, args, "--setenv", "A", "1")
}

func TestWorkloadEnvUsesLogicalPathsInNamespaceTier(t *testing.T) {
	namespacePaths := workloadPaths{Root: "/data", Home: "/data/home", Workspace: "/data/workspace", Tmp: "/tmp", Run: "/run", RuntimeDir: "/data/.vmbox"}
	account := &user.User{Uid: "30000", Gid: "30000", Username: "vmw30000", HomeDir: "/data/home"}
	env := strings.Join(workloadEnv(namespacePaths, account, 1000), "\n")
	for _, want := range []string{
		"HOME=/data/home",
		"VMBOX_WORKSPACE_ROOT=/data",
		"VMBOX_RUNTIME_DIR=/data/.vmbox",
		"TMPDIR=/tmp",
		"TMUX_TMPDIR=/tmp",
		"XDG_RUNTIME_DIR=/run",
		"VMBOX_DESKTOP_DISPLAY=:1000",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("namespace env missing %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "/data/workspaces/") {
		t.Fatalf("namespace env leaked a host path:\n%s", env)
	}
}

func mustContainSequence(t *testing.T, haystack []string, needle ...string) {
	t.Helper()
	for start := 0; start+len(needle) <= len(haystack); start++ {
		match := true
		for offset := range needle {
			if haystack[start+offset] != needle[offset] {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
	t.Fatalf("missing sequence %v in %v", needle, haystack)
}
