package boxruntime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const TmuxSnapshotVersion = 1

type TmuxSnapshot struct {
	Version  int           `json:"version"`
	SavedAt  time.Time     `json:"savedAt"`
	Sessions []TmuxSession `json:"sessions"`
}

type TmuxSession struct {
	Name    string       `json:"name"`
	Windows []TmuxWindow `json:"windows"`
}

type TmuxWindow struct {
	Index  int        `json:"index"`
	Name   string     `json:"name"`
	Layout string     `json:"layout,omitempty"`
	Active bool       `json:"active,omitempty"`
	Panes  []TmuxPane `json:"panes"`
}

type TmuxPane struct {
	Index            int      `json:"index"`
	Title            string   `json:"title,omitempty"`
	WorkingDirectory string   `json:"workingDirectory"`
	CurrentCommand   string   `json:"currentCommand"`
	ProcessArgv      []string `json:"processArgv,omitempty"`
	ResumeArgv       []string `json:"resumeArgv,omitempty"`
	ResumeStrategy   string   `json:"resumeStrategy,omitempty"`
	ScrollbackFile   string   `json:"scrollbackFile,omitempty"`
	Active           bool     `json:"active,omitempty"`
}

type TmuxRestoreResult struct {
	SnapshotPath string `json:"snapshotPath"`
	Live         bool   `json:"live"`
	Restored     int    `json:"restoredSessions"`
}

type WorkspaceProcess struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

type HibernateResult struct {
	SnapshotPath string             `json:"snapshotPath"`
	StoppedAt    time.Time          `json:"stoppedAt"`
	Blockers     []WorkspaceProcess `json:"blockers,omitempty"`
}

func tmuxRoot(root string) string {
	return filepath.Join(root, "tmux")
}

func TmuxSnapshotPath(root string) string {
	return filepath.Join(tmuxRoot(root), "snapshot.json")
}

func SaveTmuxState(ctx context.Context, root string) (TmuxSnapshot, error) {
	snapshot := TmuxSnapshot{Version: TmuxSnapshotVersion, SavedAt: time.Now().UTC(), Sessions: []TmuxSession{}}
	directory := tmuxRoot(root)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return snapshot, fmt.Errorf("create tmux state directory: %w", err)
	}
	format := strings.Join([]string{
		"#{session_name}", "#{window_index}", "#{window_name}", "#{window_layout}",
		"#{pane_index}", "#{pane_title}", "#{pane_current_path}", "#{pane_current_command}",
		"#{pane_pid}", "#{window_active}", "#{pane_active}",
	}, "\x1f")
	output, err := tmuxOutput(ctx, "list-panes", "-a", "-F", format)
	if err != nil && !tmuxServerAbsent(err) {
		return snapshot, err
	}
	if err == nil {
		snapshot, err = parseTmuxPanes(output, snapshot.SavedAt)
		if err != nil {
			return snapshot, err
		}
		for sessionIndex := range snapshot.Sessions {
			for windowIndex := range snapshot.Sessions[sessionIndex].Windows {
				window := &snapshot.Sessions[sessionIndex].Windows[windowIndex]
				for paneIndex := range window.Panes {
					pane := &window.Panes[paneIndex]
					target := fmt.Sprintf("%s:%d.%d", snapshot.Sessions[sessionIndex].Name, window.Index, pane.Index)
					name := fmt.Sprintf("%x.log", sha256.Sum256([]byte(target)))
					path := filepath.Join(directory, name)
					captured, captureErr := tmuxOutput(ctx, "capture-pane", "-p", "-e", "-S", "-", "-E", "-", "-t", target)
					if captureErr == nil {
						if err := os.WriteFile(path, captured, 0600); err != nil {
							return snapshot, fmt.Errorf("write tmux scrollback for %s: %w", target, err)
						}
						pane.ScrollbackFile = path
					}
				}
			}
		}
	}
	if err := writeJSONAtomic(TmuxSnapshotPath(root), snapshot, 0600); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func parseTmuxPanes(data []byte, savedAt time.Time) (TmuxSnapshot, error) {
	snapshot := TmuxSnapshot{Version: TmuxSnapshotVersion, SavedAt: savedAt, Sessions: []TmuxSession{}}
	sessions := make(map[string]int)
	type windowPosition struct{ session, window int }
	windows := make(map[string]windowPosition)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\x1f")
		if len(fields) != 11 {
			return snapshot, fmt.Errorf("decode tmux pane row: expected 11 fields, got %d", len(fields))
		}
		windowIndex, err := strconv.Atoi(fields[1])
		if err != nil {
			return snapshot, fmt.Errorf("decode tmux window index: %w", err)
		}
		paneIndex, err := strconv.Atoi(fields[4])
		if err != nil {
			return snapshot, fmt.Errorf("decode tmux pane index: %w", err)
		}
		panePID, _ := strconv.Atoi(fields[8])
		sessionIndex, exists := sessions[fields[0]]
		if !exists {
			snapshot.Sessions = append(snapshot.Sessions, TmuxSession{Name: fields[0], Windows: []TmuxWindow{}})
			sessionIndex = len(snapshot.Sessions) - 1
			sessions[fields[0]] = sessionIndex
		}
		windowKey := fields[0] + "\x00" + fields[1]
		position, exists := windows[windowKey]
		if !exists {
			snapshot.Sessions[sessionIndex].Windows = append(snapshot.Sessions[sessionIndex].Windows, TmuxWindow{Index: windowIndex, Name: fields[2], Layout: fields[3], Active: fields[9] == "1", Panes: []TmuxPane{}})
			position = windowPosition{session: sessionIndex, window: len(snapshot.Sessions[sessionIndex].Windows) - 1}
			windows[windowKey] = position
		}
		argv := foregroundArgv(panePID)
		resume, strategy := agentResume(fields[7], argv)
		window := &snapshot.Sessions[position.session].Windows[position.window]
		window.Panes = append(window.Panes, TmuxPane{
			Index: paneIndex, Title: fields[5], WorkingDirectory: cleanWorkingDirectory(fields[6]),
			CurrentCommand: fields[7], ProcessArgv: argv, ResumeArgv: resume,
			ResumeStrategy: strategy, Active: fields[10] == "1",
		})
	}
	if err := scanner.Err(); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func cleanWorkingDirectory(value string) string {
	if value == "" || !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "/data/workspace"
	}
	return filepath.Clean(value)
}

func agentResume(command string, argv []string) ([]string, string) {
	switch strings.ToLower(filepath.Base(command)) {
	case "codex":
		if id := flagValue(argv, "resume", ""); id != "" {
			return []string{"codex", "resume", id}, "codex-session-id"
		}
		return []string{"codex", "resume", "--last"}, "codex-latest-in-directory"
	case "claude":
		if id := flagValue(argv, "--resume", "-r"); id != "" {
			return []string{"claude", "--resume", id}, "claude-session-id"
		}
		return []string{"claude", "--continue"}, "claude-latest-in-directory"
	case "opencode":
		if id := flagValue(argv, "--session", "-s"); id != "" {
			return []string{"opencode", "--session", id}, "opencode-session-id"
		}
		return []string{"opencode", "--continue"}, "opencode-latest-in-directory"
	case "bash":
		return []string{"/bin/bash", "-l"}, "shell"
	case "sh":
		return []string{"/bin/sh", "-l"}, "shell"
	case "zsh":
		return []string{"/bin/zsh", "-l"}, "shell"
	case "fish":
		return []string{"/usr/bin/fish", "-l"}, "shell"
	default:
		return nil, "manual-restart-required"
	}
}

func flagValue(argv []string, long, short string) string {
	for index, value := range argv {
		if value == long || (short != "" && value == short) {
			if index+1 < len(argv) && argv[index+1] != "" {
				return argv[index+1]
			}
		}
		if strings.HasPrefix(value, long+"=") {
			return strings.TrimPrefix(value, long+"=")
		}
	}
	return ""
}

func foregroundArgv(pid int) []string {
	if pid <= 0 {
		return nil
	}
	current := pid
	for depth := 0; depth < 32; depth++ {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", current, current))
		if err != nil {
			break
		}
		children := strings.Fields(string(data))
		if len(children) == 0 {
			break
		}
		next, err := strconv.Atoi(children[len(children)-1])
		if err != nil || next <= 0 {
			break
		}
		current = next
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", current))
	if err != nil || len(data) == 0 {
		return nil
	}
	values := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	return append([]string(nil), values...)
}

func RestoreTmuxState(ctx context.Context, root string) (TmuxRestoreResult, error) {
	result := TmuxRestoreResult{SnapshotPath: TmuxSnapshotPath(root)}
	if output, err := tmuxOutput(ctx, "list-sessions", "-F", "#{session_name}"); err == nil && len(strings.TrimSpace(string(output))) > 0 {
		result.Live = true
		_ = os.Remove(filepath.Join(root, "hibernate-ready.json"))
		return result, nil
	} else if err != nil && !tmuxServerAbsent(err) {
		return result, err
	}
	data, err := os.ReadFile(result.SnapshotPath)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var snapshot TmuxSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return result, fmt.Errorf("decode tmux snapshot: %w", err)
	}
	if snapshot.Version != TmuxSnapshotVersion {
		return result, fmt.Errorf("unsupported tmux snapshot version %d", snapshot.Version)
	}
	for _, session := range snapshot.Sessions {
		if err := restoreTmuxSession(ctx, snapshot, session); err != nil {
			return result, err
		}
		result.Restored++
	}
	_ = os.Remove(filepath.Join(root, "hibernate-ready.json"))
	return result, nil
}

func restoreTmuxSession(ctx context.Context, snapshot TmuxSnapshot, session TmuxSession) error {
	if session.Name == "" || len(session.Windows) == 0 || len(session.Windows[0].Panes) == 0 {
		return nil
	}
	for windowOffset, window := range session.Windows {
		if len(window.Panes) == 0 {
			continue
		}
		first := window.Panes[0]
		command := restoredPaneCommand(snapshot, first)
		var args []string
		if windowOffset == 0 {
			args = []string{"new-session", "-d", "-s", session.Name, "-n", window.Name, "-c", first.WorkingDirectory, command}
		} else {
			args = []string{"new-window", "-d", "-t", fmt.Sprintf("%s:%d", session.Name, window.Index), "-n", window.Name, "-c", first.WorkingDirectory, command}
		}
		if _, err := tmuxOutput(ctx, args...); err != nil {
			return fmt.Errorf("restore tmux window %s:%d: %w", session.Name, window.Index, err)
		}
		for paneOffset, pane := range window.Panes {
			target := fmt.Sprintf("%s:%d", session.Name, window.Index)
			if paneOffset > 0 {
				if _, err := tmuxOutput(ctx, "split-window", "-d", "-t", target, "-c", pane.WorkingDirectory, restoredPaneCommand(snapshot, pane)); err != nil {
					return fmt.Errorf("restore tmux pane %s.%d: %w", target, pane.Index, err)
				}
			}
			if pane.Title != "" {
				_, _ = tmuxOutput(ctx, "select-pane", "-t", fmt.Sprintf("%s.%d", target, paneOffset), "-T", pane.Title)
			}
		}
		if window.Layout != "" {
			_, _ = tmuxOutput(ctx, "select-layout", "-t", fmt.Sprintf("%s:%d", session.Name, window.Index), window.Layout)
		}
	}
	_, _ = tmuxOutput(ctx, "source-file", "/etc/vmbox/tmux.conf")
	return nil
}

func restoredPaneCommand(snapshot TmuxSnapshot, pane TmuxPane) string {
	parts := []string{}
	if pane.ScrollbackFile != "" {
		parts = append(parts, "if [ -r "+shellQuote(pane.ScrollbackFile)+" ]; then cat -- "+shellQuote(pane.ScrollbackFile)+"; fi")
	}
	if len(pane.ResumeArgv) > 0 {
		parts = append(parts, "exec "+shellJoin(pane.ResumeArgv))
		return strings.Join(parts, "; ")
	}
	interrupted := struct {
		Command   string    `json:"command"`
		StoppedAt time.Time `json:"stoppedAt"`
		Log       string    `json:"log,omitempty"`
	}{Command: strings.Join(pane.ProcessArgv, " "), StoppedAt: snapshot.SavedAt, Log: pane.ScrollbackFile}
	if interrupted.Command == "" {
		interrupted.Command = pane.CurrentCommand
	}
	encoded, _ := json.Marshal(interrupted)
	parts = append(parts, "vmbox-runtime interrupted-pane "+base64.RawURLEncoding.EncodeToString(encoded), "exec ${SHELL:-/bin/bash} -l")
	return strings.Join(parts, "; ")
}

func PrepareHibernate(ctx context.Context, root string) (HibernateResult, error) {
	result := HibernateResult{StoppedAt: time.Now().UTC()}
	marker := filepath.Join(root, "hibernate-ready.json")
	if _, serverErr := tmuxOutput(ctx, "list-sessions", "-F", "#{session_name}"); tmuxServerAbsent(serverErr) {
		if data, readErr := os.ReadFile(marker); readErr == nil {
			var prior HibernateResult
			if json.Unmarshal(data, &prior) == nil {
				if _, snapshotErr := os.Stat(TmuxSnapshotPath(root)); snapshotErr == nil {
					return prior, nil
				}
			}
		}
	}
	_, _ = tmuxOutput(ctx, "display-message", "-a", "vmbox is hibernating: terminal state will be saved; live processes will stop")
	snapshot, err := SaveTmuxState(ctx, root)
	if err != nil {
		return result, err
	}
	result.SnapshotPath = TmuxSnapshotPath(root)
	_, killErr := tmuxOutput(ctx, "kill-server")
	if killErr != nil && !tmuxServerAbsent(killErr) {
		return result, killErr
	}
	blockers, err := stopWorkspaceProcesses(ctx, "/data", 10*time.Second)
	if err != nil {
		return result, err
	}
	result.Blockers = blockers
	if len(blockers) > 0 {
		return result, fmt.Errorf("workspace is still busy after saving %d tmux session(s): %s", len(snapshot.Sessions), describeProcesses(blockers))
	}
	if err := runSync(ctx); err != nil {
		return result, err
	}
	marker = filepath.Join(root, "hibernate-ready.json")
	if err := writeJSONAtomic(marker, result, 0600); err != nil {
		return result, err
	}
	return result, nil
}

func stopWorkspaceProcesses(ctx context.Context, root string, grace time.Duration) ([]WorkspaceProcess, error) {
	excluded := ancestorPIDs()
	processes := workspaceProcesses(root, excluded)
	for _, process := range processes {
		if err := syscall.Kill(process.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return processes, fmt.Errorf("stop workspace process %d: %w", process.PID, err)
		}
	}
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		remaining := workspaceProcesses(root, excluded)
		if len(remaining) == 0 {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return remaining, ctx.Err()
		case <-deadline.C:
			return remaining, nil
		case <-ticker.C:
		}
	}
}

func workspaceProcesses(root string, excluded map[int]bool) []WorkspaceProcess {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	uid := uint32(os.Getuid())
	var result []WorkspaceProcess
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || excluded[pid] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uid || !processUsesPath(pid, root) {
			continue
		}
		argv := foregroundArgv(pid)
		if infrastructureWorkspaceProcess(argv) {
			continue
		}
		command := strings.Join(argv, " ")
		if command == "" {
			command = entry.Name()
		}
		result = append(result, WorkspaceProcess{PID: pid, Command: command})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PID < result[j].PID })
	return result
}

func infrastructureWorkspaceProcess(argv []string) bool {
	return len(argv) == 2 && filepath.Base(argv[0]) == "vmbox-runtime" && argv[1] == "idle"
}

func processUsesPath(pid int, root string) bool {
	root = filepath.Clean(root)
	within := func(path string) bool {
		path = strings.TrimSuffix(path, " (deleted)")
		return path == root || strings.HasPrefix(path, root+string(os.PathSeparator))
	}
	for _, name := range []string{"cwd", "root", "exe"} {
		if path, err := os.Readlink(fmt.Sprintf("/proc/%d/%s", pid, name)); err == nil && within(path) {
			return true
		}
	}
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if path, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, entry.Name())); err == nil && within(path) {
			return true
		}
	}
	return false
}

func ancestorPIDs() map[int]bool {
	// Railway can inject an SSH command into the container through a process
	// tree that does not descend from the container's PID 1. In a fleet slot,
	// PID 1 is always infrastructure. The entrypoint may leave sudo as PID 1
	// and run vmbox-runtime idle as its child; workspaceProcesses separately
	// protects that exact command. Terminating either aborts hibernation by
	// stopping the whole container.
	result := map[int]bool{1: true, os.Getpid(): true}
	pid := os.Getppid()
	for pid > 0 && !result[pid] {
		result[pid] = true
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			break
		}
		closeParen := strings.LastIndexByte(string(data), ')')
		if closeParen < 0 {
			break
		}
		fields := strings.Fields(string(data[closeParen+1:]))
		if len(fields) < 2 {
			break
		}
		next, err := strconv.Atoi(fields[1])
		if err != nil || next == pid {
			break
		}
		pid = next
	}
	return result
}

func describeProcesses(processes []WorkspaceProcess) string {
	values := make([]string, 0, len(processes))
	for _, process := range processes {
		values = append(values, fmt.Sprintf("pid %d (%s)", process.PID, process.Command))
	}
	return strings.Join(values, ", ")
}

func tmuxOutput(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "tmux", args...)
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return output, fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}

func tmuxServerAbsent(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	missingSocket := strings.Contains(text, "error connecting to") && strings.Contains(text, "no such file or directory")
	return strings.Contains(text, "no server running") || strings.Contains(text, "failed to connect to server") || strings.Contains(text, "no sessions") || missingSocket
}

func runSync(ctx context.Context) error {
	command := exec.CommandContext(ctx, "sync")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("flush workspace filesystem: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func shellJoin(argv []string) string {
	values := make([]string, len(argv))
	for index, value := range argv {
		values[index] = shellQuote(value)
	}
	return strings.Join(values, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func DecodeInterruptedPane(value string, destination io.Writer) error {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("decode interrupted pane: %w", err)
	}
	var pane struct {
		Command   string    `json:"command"`
		StoppedAt time.Time `json:"stoppedAt"`
		Log       string    `json:"log"`
	}
	if err := json.Unmarshal(data, &pane); err != nil {
		return fmt.Errorf("decode interrupted pane metadata: %w", err)
	}
	fmt.Fprintf(destination, "\n[vmbox] This pane's process did not remain alive through hibernation.\n")
	fmt.Fprintf(destination, "[vmbox] Interrupted: %s\n[vmbox] Stopped: %s\n", pane.Command, pane.StoppedAt.Format(time.RFC3339))
	if pane.Log != "" {
		fmt.Fprintf(destination, "[vmbox] Saved scrollback: %s\n", pane.Log)
	}
	fmt.Fprintln(destination, "[vmbox] Restart it explicitly when ready; vmbox never re-runs arbitrary commands.")
	return nil
}
