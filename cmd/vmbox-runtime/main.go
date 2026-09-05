package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/coworker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vmbox-runtime:", err)
		os.Exit(1)
	}
}
func run() error {
	runtime := boxruntime.New("")
	args := os.Args[1:]
	switch filepath.Base(os.Args[0]) {
	case "vmbox-report":
		args = append([]string{"report"}, args...)
	case "vmbox-finish":
		args = append([]string{"finish"}, args...)
	case "vmbox-ask":
		args = append([]string{"ask"}, args...)
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: vmbox-runtime health | idle | image-info | tmux-snapshot | tmux-context BOX SLOT STATE HEALTH | tmux-restore | prepare-hibernate | run [--detach] -- COMMAND [ARG...] | exec-json DATA | direct-json DATA | put-file PATH MODE | sync-files | setup | tmux-help | welcome | report | ask")
	}
	if handled, err := runTmuxInteraction(args, runtime); handled {
		return err
	}
	switch args[0] {
	case "coworker-run":
		if len(args) != 1 {
			return fmt.Errorf("coworker-run accepts no arguments")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return coworker.RunConfigured(ctx, os.Stdin, os.Stdout, os.Stderr)
	case "coworker-codex":
		if len(args) != 1 {
			return fmt.Errorf("coworker-codex accepts prompt on stdin only")
		}
		prompt, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
		if err != nil || len(prompt) > 65536 {
			return fmt.Errorf("coworker prompt must be at most 64 KiB")
		}
		stateDir := "/data/home/.vmbox-coworker"
		if err := os.MkdirAll(stateDir, 0700); err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return (coworker.Client{URL: os.Getenv("VMBOX_COWORKER_URL"), Token: os.Getenv("VMBOX_COWORKER_TOKEN")}).Codex(ctx, filepath.Join(stateDir, "codex-state.json"), string(prompt), os.Stdout)
	case "coworker-channel":
		if len(args) != 1 {
			return fmt.Errorf("coworker-channel accepts no arguments")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return (coworker.Client{URL: os.Getenv("VMBOX_COWORKER_URL"), Token: os.Getenv("VMBOX_COWORKER_TOKEN")}).ClaudeChannel(ctx, os.Stdin, os.Stdout)
	case "health":
		fmt.Println("ok")
		return nil
	case "idle":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer cancel()
		marker := "/tmp/vmbox-idle-ready"
		if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644); err != nil {
			return err
		}
		defer os.Remove(marker)
		<-ctx.Done()
		return nil
	case "image-info":
		if len(args) != 1 {
			return fmt.Errorf("image-info accepts no arguments")
		}
		return json.NewEncoder(os.Stdout).Encode(readImageInfo())
	case "tmux-snapshot":
		if len(args) != 1 {
			return fmt.Errorf("tmux-snapshot accepts no arguments")
		}
		snapshot, err := boxruntime.SaveTmuxState(context.Background(), runtime.Root)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(snapshot)
	case "tmux-restore":
		if len(args) != 1 {
			return fmt.Errorf("tmux-restore accepts no arguments")
		}
		result, err := boxruntime.RestoreTmuxState(context.Background(), runtime.Root)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "prepare-hibernate", "prepare-idle-hibernate":
		if len(args) != 1 {
			return fmt.Errorf("prepare-hibernate accepts no arguments")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer cancel()
		prepare := boxruntime.PrepareHibernate
		if args[0] == "prepare-idle-hibernate" {
			prepare = boxruntime.PrepareIdleHibernate
		}
		result, err := prepare(ctx, runtime.Root)
		if err != nil {
			if len(result.Blockers) > 0 {
				_ = json.NewEncoder(os.Stderr).Encode(result)
			}
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "interrupted-pane":
		if len(args) != 2 {
			return fmt.Errorf("interrupted-pane requires encoded pane metadata")
		}
		return boxruntime.DecodeInterruptedPane(args[1], os.Stdout)
	case "tmux-context":
		if len(args) != 5 {
			return fmt.Errorf("tmux-context requires BOX SLOT STATE HEALTH")
		}
		return boxruntime.SetTmuxContext(context.Background(), runtime.Root, args[1], args[2], args[3], args[4])
	case "put-file":
		if len(args) != 3 {
			return fmt.Errorf("put-file requires PATH MODE")
		}
		owner, err := boxruntime.LookupWorkloadOwnership(os.Geteuid(), nil)
		if err != nil {
			return err
		}
		digest, err := writeSyncedFile("/data", args[1], args[2], io.LimitReader(os.Stdin, 16<<20), owner, nil)
		if err != nil {
			return err
		}
		fmt.Println(digest)
		return nil
	case "sync-files":
		if len(args) != 1 {
			return fmt.Errorf("sync-files accepts its request only on stdin")
		}
		owner, err := boxruntime.LookupWorkloadOwnership(os.Geteuid(), nil)
		if err != nil {
			return err
		}
		digest, err := receiveFiles(os.Stdin, "/data", owner, nil)
		if err != nil {
			return err
		}
		fmt.Println(digest)
		return nil
	case "setup":
		if len(args) != 1 {
			return fmt.Errorf("setup accepts its request only on stdin")
		}
		data, err := readLimited(os.Stdin, 2<<20)
		if err != nil {
			return err
		}
		var request boxruntime.SetupRequest
		if err := json.Unmarshal(data, &request); err != nil {
			return fmt.Errorf("decode setup request: %w", err)
		}
		result, err := performSetup(context.Background(), request, executeSetupCommand, os.Geteuid())
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "tmux-help":
		fmt.Print(`vmbox tmux help (German QWERTZ friendly)

Write and scroll
  Ctrl-a s       Enter scroll/copy mode
  Mouse wheel    Scroll; tmux enters scroll mode automatically
  Arrows/PgUp/PgDn
                 Move through scrollback
  Ctrl-f         Search forward in scrollback
  Space, Enter   Start selection, then copy and return to writing
  q or Escape    Leave scroll mode and return to writing mode

Windows and panes
  Ctrl-a c       Create a window
  Ctrl-a n / p   Next / previous window
  Ctrl-a arrows  Switch panes
  Ctrl-a Alt-arrows
                 Resize panes

Safe disconnect and lifecycle
  Ctrl-a d disconnects you, but the box and its tasks keep running.
  Reconnect with: vmbox NAME
  Detach only disconnects the terminal; every live process keeps running.
  Hibernate saves restorable state, stops live processes, retains the volume,
  and frees its compute slot. Arbitrary processes do not survive hibernation.
  Delete volume permanently deletes the logical box and workspace data; it
  requires a separate destructive confirmation and does not delete the slot.

Keyboard input is passed through unchanged. vmbox never swaps Y and Z and
never applies a remote QWERTY mapping.
`)
		return nil
	case "welcome":
		welcome := filepath.Join(os.Getenv("HOME"), ".vmbox-welcome")
		if data, err := os.ReadFile(welcome); err == nil {
			_, _ = os.Stdout.Write(data)
			if len(data) == 0 || data[len(data)-1] != '\n' {
				fmt.Println()
			}
		} else {
			fmt.Printf("vmbox %s is ready\nProvider: %s  Region: %s\nSpecs: %s CPU / %s MiB RAM / %s GiB disk\nWorkspace: %s\nCost: %s\nDetach safely: press Ctrl-a, release both keys, then press d\n\n",
				os.Getenv("VMBOX_NAME"), os.Getenv("VMBOX_PROVIDER"), os.Getenv("VMBOX_REGION"), os.Getenv("VMBOX_CPU"), os.Getenv("VMBOX_MEMORY_MIB"), os.Getenv("VMBOX_DISK_GIB"), os.Getenv("VMBOX_WORKSPACE"), os.Getenv("VMBOX_COST"))
		}
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		cmd := exec.Command(shell, "-l")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	case "exec-json":
		if len(args) != 2 {
			return fmt.Errorf("exec-json requires one encoded argv")
		}
		argv, err := boxruntime.DecodeArgv(args[1])
		if err != nil {
			return err
		}
		run, err := runtime.Run(context.Background(), argv, os.Stdout, os.Stderr)
		if err != nil {
			return err
		}
		if run.ExitCode != nil {
			os.Exit(*run.ExitCode)
		}
		return nil
	case "direct-json":
		if len(args) != 2 {
			return fmt.Errorf("direct-json requires one encoded argv")
		}
		argv, err := boxruntime.DecodeArgv(args[1])
		if err != nil {
			return err
		}
		if len(argv) == 0 {
			return fmt.Errorf("direct-json command cannot be empty")
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	case "run":
		detach := false
		internal := false
		i := 1
		for i < len(args) && args[i] != "--" {
			switch args[i] {
			case "--detach":
				detach = true
			case "--internal-background":
				internal = true
			default:
				return fmt.Errorf("unknown run option %q", args[i])
			}
			i++
		}
		if i >= len(args) || i+1 >= len(args) {
			return fmt.Errorf("run requires -- COMMAND [ARG...]")
		}
		argv := args[i+1:]
		if detach && !internal {
			id, err := runtime.RunDetached(argv)
			if err == nil {
				fmt.Println(id)
			}
			return err
		}
		run, err := runtime.Run(context.Background(), argv, os.Stdout, os.Stderr)
		if err != nil {
			return err
		}
		if run.ExitCode != nil {
			os.Exit(*run.ExitCode)
		}
		return nil
	case "report":
		kind, message, err := parseReport(args[1:])
		if err != nil {
			return err
		}
		return runtime.Report(os.Getenv("VMBOX_RUN_ID"), kind, message)
	case "finish":
		kind := "success"
		i := 1
		if i < len(args) && (args[i] == "--failed" || args[i] == "--success") {
			if args[i] == "--failed" {
				kind = "failed"
			}
			i++
		}
		if i < len(args) && args[i] == "--summary" {
			i++
		}
		return runtime.Report(os.Getenv("VMBOX_RUN_ID"), kind, strings.Join(args[i:], " "))
	case "ask":
		timeout := time.Duration(0)
		i := 1
		if i+1 < len(args) && args[i] == "--timeout" {
			value, err := time.ParseDuration(args[i+1])
			if err != nil {
				return err
			}
			timeout = value
			i += 2
		}
		if i >= len(args) {
			return fmt.Errorf("ask requires a prompt")
		}
		answer, err := runtime.Ask(context.Background(), os.Getenv("VMBOX_RUN_ID"), strings.Join(args[i:], " "), timeout)
		if err == nil {
			fmt.Println(answer)
		}
		return err
	case "answer":
		if len(args) != 4 {
			return fmt.Errorf("answer RUN_ID QUESTION_ID TEXT")
		}
		path := runtime.Root + "/runs/" + args[1] + "/questions/" + args[2] + ".answer"
		return os.WriteFile(path, []byte(args[3]+"\n"), 0600)
	case "status":
		id := ""
		if len(args) > 1 {
			id = args[1]
		}
		store, err := runtime.StoreFor(id)
		if err != nil {
			return err
		}
		status, err := store.ReadStatus()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(status)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type imageInfo struct {
	Version     string `json:"version"`
	Components  string `json:"components"`
	Fingerprint string `json:"fingerprint"`
}

func readImageInfo() imageInfo {
	read := func(path string) string {
		data, _ := os.ReadFile(path)
		return strings.TrimSpace(string(data))
	}
	return imageInfo{
		Version:     read("/usr/local/lib/vmbox-image-version"),
		Components:  read("/usr/local/lib/vmbox-bootstrap-components"),
		Fingerprint: read("/usr/local/lib/vmbox-component-fingerprint"),
	}
}

const maxSyncedFileSize = 16 << 20

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("request exceeds %d bytes", limit)
	}
	return data, nil
}

// writeSyncedFile installs one file received over the control plane. The SSH
// data path connects as root, so every directory it creates and every file it
// writes is handed to the unprivileged workload user before it becomes visible.
func writeSyncedFile(root, destination, modeText string, reader io.Reader, owner *boxruntime.Ownership, chown boxruntime.Chowner) (string, error) {
	root = filepath.Clean(root)
	destination = filepath.Clean(destination)
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("file destination must be below %s", root)
	}
	mode, err := strconv.ParseUint(modeText, 8, 9)
	if err != nil {
		return "", fmt.Errorf("invalid file mode: %w", err)
	}
	if err := boxruntime.CheckSyncedFileMode(destination, os.FileMode(mode)); err != nil {
		return "", err
	}
	if err := boxruntime.EnsurePrivateDirectory(root, filepath.Dir(destination), owner, chown); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".vmbox-upload-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(os.FileMode(mode)); err != nil {
		_ = tmp.Close()
		return "", err
	}
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, digest), reader); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := owner.Apply(tmpPath, chown); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func receiveFiles(reader io.Reader, root string, owner *boxruntime.Ownership, chown boxruntime.Chowner) (string, error) {
	payload, err := readLimited(reader, 64<<20)
	if err != nil {
		return "", err
	}
	var request boxruntime.SyncRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return "", fmt.Errorf("decode sync request: %w", err)
	}
	if len(request.Files) == 0 || len(request.Files) > 128 {
		return "", fmt.Errorf("sync request must contain 1 to 128 files")
	}
	seen := make(map[string]bool, len(request.Files))
	for _, file := range request.Files {
		path := filepath.Clean(file.Path)
		if seen[path] {
			return "", fmt.Errorf("duplicate sync destination %s", path)
		}
		seen[path] = true
		if len(file.Data) > maxSyncedFileSize {
			return "", fmt.Errorf("sync file %s exceeds %d bytes", path, maxSyncedFileSize)
		}
		if _, err := writeSyncedFile(root, path, file.Mode, bytes.NewReader(file.Data), owner, chown); err != nil {
			return "", fmt.Errorf("write %s: %w", path, err)
		}
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:]), nil
}

type setupCommand func(context.Context, io.Reader, io.Writer, io.Writer, string, ...string) error

func executeSetupCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	return command.Run()
}

// performSetup runs every credential and trust step inside the box. euid is the
// effective UID of the runtime: Railway's SSH data path lands as root, and root
// must never write or read agent credentials on its own behalf, so each step is
// rewritten to run as vmbox with HOME=/data/home before it is executed.
func performSetup(ctx context.Context, request boxruntime.SetupRequest, execute setupCommand, euid int) (boxruntime.SetupResult, error) {
	if execute == nil {
		return boxruntime.SetupResult{}, fmt.Errorf("setup command executor is unavailable")
	}
	asWorkload := func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
		name, args = boxruntime.WorkloadArgv(euid, name, args)
		return execute(ctx, stdin, stdout, stderr, name, args...)
	}
	if request.GitHub != nil {
		github := request.GitHub
		if github.Host == "" || github.User == "" || github.Token == "" || (github.Protocol != "ssh" && github.Protocol != "https") {
			return boxruntime.SetupResult{}, fmt.Errorf("invalid GitHub setup request")
		}
		if err := asWorkload(ctx, strings.NewReader(github.Token+"\n"), io.Discard, os.Stderr, "gh", "auth", "login", "--hostname", github.Host, "--git-protocol", github.Protocol, "--with-token"); err != nil {
			return boxruntime.SetupResult{}, fmt.Errorf("configure GitHub authentication: %w", err)
		}
		if err := asWorkload(ctx, nil, io.Discard, os.Stderr, "gh", "auth", "setup-git", "--hostname", github.Host); err != nil {
			return boxruntime.SetupResult{}, fmt.Errorf("configure GitHub Git protocol: %w", err)
		}
		for _, identity := range boxruntime.GitIdentityArguments(github) {
			if err := asWorkload(ctx, nil, io.Discard, os.Stderr, "git", identity...); err != nil {
				return boxruntime.SetupResult{}, fmt.Errorf("configure Git commit identity: %w", err)
			}
		}
		if err := asWorkload(ctx, nil, io.Discard, os.Stderr, "sh", "-c", boxruntime.SecureGitHubConfigScript); err != nil {
			return boxruntime.SetupResult{}, fmt.Errorf("secure GitHub configuration: %w", err)
		}
		if err := asWorkload(ctx, nil, io.Discard, io.Discard, "gh", "auth", "status", "--hostname", github.Host); err != nil {
			return boxruntime.SetupResult{}, fmt.Errorf("verify GitHub authentication as workload user: %w", err)
		}
	}
	if request.Workspace == "" {
		return boxruntime.SetupResult{}, fmt.Errorf("setup workspace is required")
	}
	if err := asWorkload(ctx, nil, io.Discard, os.Stderr, "vmbox-entrypoint", "--configure-agent-trust", request.Workspace); err != nil {
		return boxruntime.SetupResult{}, fmt.Errorf("configure agent trust: %w", err)
	}
	result := boxruntime.SetupResult{Authentication: make(map[string]bool)}
	seen := make(map[string]bool)
	for _, application := range request.Applications {
		if seen[application] {
			continue
		}
		seen[application] = true
		switch application {
		case "codex":
			result.Authentication[application] = asWorkload(ctx, nil, io.Discard, io.Discard, "codex", "login", "status") == nil
		case "claude":
			// A fresh task box carried a correctly owned 0600 credential whose
			// token had gone stale. `claude auth status --json` exited zero and
			// reported the failure only in its payload, as {"loggedIn":false}.
			// The exit status is therefore not evidence on its own: the parsed
			// field is the verdict, and output that will not parse is no answer.
			var output bytes.Buffer
			err := asWorkload(ctx, nil, &output, io.Discard, "claude", "auth", "status", "--json")
			var status struct {
				LoggedIn bool `json:"loggedIn"`
			}
			result.Authentication[application] = err == nil && json.Unmarshal(output.Bytes(), &status) == nil && status.LoggedIn
		}
	}
	return result, nil
}

func parseReport(args []string) (kind, message string, err error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("report requires a message")
	}
	kind = "progress"
	if strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--progress":
		case "--needs-input":
			kind = "needs_input"
		default:
			return "", "", fmt.Errorf("unknown report option %q", args[0])
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return "", "", fmt.Errorf("report requires a message")
	}
	return kind, strings.Join(args, " "), nil
}
