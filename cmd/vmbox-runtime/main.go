package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vmbox-runtime:", err)
		os.Exit(1)
	}
}
func run() error {
	if err := boxruntime.ValidateWorkspaceEnvironment(); err != nil {
		return err
	}
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
		return fmt.Errorf("usage: vmbox-runtime health | idle | image-info | tmux-snapshot | tmux-context BOX SLOT STATE HEALTH | tmux-restore | prepare-hibernate | run [--detach] -- COMMAND [ARG...] | exec-json DATA | direct-json DATA | put-file PATH MODE | sync-files | sync-instructions | setup | tmux-help | welcome | report | ask")
	}
	if handled, err := runTmuxInteraction(args, runtime); handled {
		return err
	}
	switch args[0] {
	case "desktop-register":
		if len(args) != 2 {
			return fmt.Errorf("desktop-register requires AGENT")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		return boxruntime.RegisterDesktopMCP(ctx, home, args[1])
	case "desktop-idle":
		if len(args) != 2 {
			return fmt.Errorf("desktop-idle requires ASSIGNMENT")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		seconds, err := boxruntime.DesktopIdleSeconds(ctx, args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]int64{"seconds": seconds})
	case "desktop-import-state":
		if len(args) != 2 {
			return fmt.Errorf("desktop-import-state requires ASSIGNMENT")
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
		if err != nil {
			return fmt.Errorf("private browser state unavailable")
		}
		defer clear(data)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return boxruntime.ImportDesktopBrowserState(ctx, args[1], data)
	case "desktop-password-origin":
		if len(args) != 2 {
			return fmt.Errorf("desktop-password-origin requires ASSIGNMENT")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return boxruntime.DesktopPasswordOrigin(ctx, args[1], os.Stdout)
	case "desktop-type-secret":
		if len(args) != 2 {
			return fmt.Errorf("desktop-type-secret requires ASSIGNMENT")
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 32769))
		if err != nil {
			return fmt.Errorf("private password input unavailable")
		}
		defer clear(data)
		request, err := boxruntime.DecodeDesktopPassword(data)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return boxruntime.TypeDesktopPassword(ctx, args[1], request)
	case "desktop-terminal", "desktop-terminal-attach":
		if len(args) != 4 {
			return fmt.Errorf("desktop terminal requires assignment, session ID and incarnation")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer cancel()
		if args[0] == "desktop-terminal-attach" {
			return boxruntime.NativeAttach(ctx, runtime.Root, args[1], args[2], args[3])
		}
		return boxruntime.RunDesktopTerminal(ctx, runtime.Root, args[1], args[2], args[3])
	case "desktop-browser":
		if len(args) != 1 {
			return fmt.Errorf("desktop-browser accepts no arguments")
		}
		return boxruntime.RunDesktopBrowser(context.Background())
	case "desktop-mcp":
		if len(args) != 1 {
			return fmt.Errorf("desktop-mcp accepts no arguments")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		fence, err := exec.CommandContext(ctx, "tmux", "show-option", "-gv", "@vmbox_assignment").Output()
		if err != nil {
			return fmt.Errorf("worker assignment unavailable")
		}
		return boxruntime.ServeDesktopMCP(ctx, strings.TrimSpace(string(fence)), os.Stdin, os.Stdout)
	case "desktop-mcp-http":
		if len(args) != 1 {
			return fmt.Errorf("desktop-mcp-http accepts no arguments")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		fence, err := exec.CommandContext(ctx, "tmux", "show-option", "-gv", "@vmbox_assignment").Output()
		if err != nil {
			return fmt.Errorf("worker assignment unavailable")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		return boxruntime.ServeDesktopMCPHTTP(ctx, strings.TrimSpace(string(fence)), home)
	case "desktop-input":
		if len(args) != 2 {
			return fmt.Errorf("desktop-input requires ASSIGNMENT")
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 32769))
		if err != nil {
			return err
		}
		action, err := boxruntime.DecodeDesktopAction(data)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return boxruntime.DesktopInput(ctx, args[1], action)
	case "desktop-enable", "desktop-start", "desktop-run", "desktop-folders", "desktop-stream", "desktop-status", "desktop-screenshot", "desktop-thumbnail":
		if len(args) != 2 && !(args[0] == "desktop-start" && len(args) == 3) {
			return fmt.Errorf("desktop command requires ASSIGNMENT")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer cancel()
		switch args[0] {
		case "desktop-thumbnail":
			return boxruntime.CaptureDesktopThumbnail(ctx, args[1], os.Stdout)
		case "desktop-screenshot":
			return boxruntime.CaptureDesktop(ctx, args[1], os.Stdout)
		case "desktop-status":
			enabled, err := boxruntime.DesktopEnabled(ctx, args[1])
			if err != nil {
				return err
			}
			fmt.Printf("{\"enabled\":%t}\n", enabled)
			return nil
		case "desktop-enable":
			return boxruntime.EnableDesktop(ctx, args[1])
		case "desktop-start":
			if len(args) == 3 {
				return boxruntime.StartDesktop(ctx, args[1], args[2])
			}
			return boxruntime.StartDesktop(ctx, args[1])
		case "desktop-run":
			return boxruntime.RunDesktop(ctx, args[1])
		case "desktop-folders":
			return boxruntime.RunDesktopFolders(ctx, args[1])
		default:
			return boxruntime.StreamDesktop(ctx, args[1], os.Stdin, os.Stdout)
		}
	case "web-terminal":
		if len(args) != 4 {
			return fmt.Errorf("web-terminal requires ASSIGNMENT SESSION_ID INCARNATION")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer cancel()
		return boxruntime.WebTerminal(ctx, args[1], args[2], args[3], os.Stdin, os.Stdout)
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
	case "install-tools":
		return boxruntime.InstallTools(context.Background(), os.Getenv("HOME"), args[1:], os.Stdout)
	case "install-agent-cli":
		if len(args) != 3 {
			return fmt.Errorf("install-agent-cli requires agent and exact version")
		}
		return boxruntime.InstallAgentCLI(context.Background(), os.Getenv("HOME"), args[1], args[2], os.Stdout)
	case "configure-tools":
		if len(args) != 1 {
			return fmt.Errorf("configure-tools reads install commands from stdin")
		}
		script, err := io.ReadAll(io.LimitReader(os.Stdin, 32769))
		if err != nil {
			return err
		}
		return boxruntime.ConfigureToolSetup(context.Background(), os.Getenv("HOME"), string(script), os.Stdout)
	case "restore-tools":
		return boxruntime.RestoreToolSetup(context.Background(), os.Getenv("HOME"), os.Stdout)
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
		digest, err := writeSyncedFile(boxruntime.WorkspaceRoot(), boxruntime.WorkspacePath(args[1]), args[2], io.LimitReader(os.Stdin, 16<<20), owner, nil)
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
		digest, err := receiveFiles(os.Stdin, boxruntime.WorkspaceRoot(), owner, nil)
		if err != nil {
			return err
		}
		fmt.Println(digest)
		return nil
	case "sync-instructions":
		if len(args) != 1 {
			return fmt.Errorf("sync-instructions accepts its request only on stdin")
		}
		owner, err := boxruntime.LookupWorkloadOwnership(os.Geteuid(), nil)
		if err != nil {
			return err
		}
		payload, err := readLimited(os.Stdin, 1<<20)
		if err != nil {
			return err
		}
		var request boxruntime.InstructionSyncRequest
		if err := json.Unmarshal(payload, &request); err != nil {
			return fmt.Errorf("decode instruction sync request: %w", err)
		}
		report, err := boxruntime.ApplyManagedInstructions(boxruntime.WorkloadHome(), request.Markdown, owner, nil)
		if err != nil {
			return fmt.Errorf("apply managed instructions: %w", err)
		}
		fmt.Fprintln(os.Stderr, report.InstructionApplyReport())
		digest := sha256.Sum256(payload)
		fmt.Printf("%x\n", digest[:])
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
  Mouse drag     Select text; release to copy to the tmux buffer
  Ctrl-a v       Paste the tmux buffer (bracketed paste when supported)
  Shift + drag   Select with your laptop terminal instead of tmux
  Ctrl-Shift-C/V Copy/paste locally in Linux terminals (Cmd-C/V on macOS)
                 Remote clipboard copy requires terminal OSC 52 support;
                 if unsupported, use Shift-drag and local copy instead.
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
  Delete box permanently deletes the logical box and workspace data; it
  requires a separate destructive confirmation and does not delete the slot.

Keyboard input is passed through unchanged. vmbox never swaps Y and Z and
never applies a remote QWERTY mapping.
`)
		return nil
	case "welcome":
		if len(args) != 1 && !(len(args) == 3 && args[1] == "--start-cli") {
			return fmt.Errorf("usage: welcome [--start-cli COMMAND]")
		}
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
		if len(args) == 3 {
			// Explicit user startup commands run once, before the persistent
			// shell. Their exit must not close the interactive workspace.
			start := exec.Command(shell, "-lc", args[2])
			start.Stdin, start.Stdout, start.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := start.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Startup command exited: %v\n", err)
			}
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
	if len(request.Files)+len(request.Remove) == 0 || len(request.Files)+len(request.Remove) > 256 || len(request.Files) > 128 || len(request.Remove) > 128 {
		return "", fmt.Errorf("sync request must contain 1 to 256 file operations")
	}
	seen := make(map[string]bool, len(request.Files)+len(request.Remove))
	remove := make([]string, 0, len(request.Remove))
	for _, requested := range request.Remove {
		path := filepath.Clean(boxruntime.WorkspacePath(requested))
		rootPath := filepath.Clean(root)
		relative, err := filepath.Rel(rootPath, path)
		if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return "", fmt.Errorf("file removal must be below %s", rootPath)
		}
		if seen[path] {
			return "", fmt.Errorf("duplicate sync destination %s", path)
		}
		seen[path] = true
		info, err := os.Lstat(path)
		if err == nil && info.IsDir() {
			return "", fmt.Errorf("refuse to remove directory %s", path)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect removal %s: %w", path, err)
		}
		remove = append(remove, path)
	}
	for _, file := range request.Files {
		path := filepath.Clean(boxruntime.WorkspacePath(file.Path))
		if seen[path] {
			return "", fmt.Errorf("duplicate sync destination %s", path)
		}
		seen[path] = true
		if len(file.Data) > maxSyncedFileSize {
			return "", fmt.Errorf("sync file %s exceeds %d bytes", path, maxSyncedFileSize)
		}
	}
	for _, path := range remove {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("remove %s: %w", path, err)
		}
	}
	for _, file := range request.Files {
		path := filepath.Clean(boxruntime.WorkspacePath(file.Path))
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
	if err := asWorkload(ctx, nil, io.Discard, os.Stderr, "vmbox-entrypoint", "--configure-agent-trust", boxruntime.WorkspacePath(request.Workspace)); err != nil {
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
