package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/components"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

var errSetupCancelled = errors.New("box setup cancelled")

type runOptions struct {
	name, region, workspace, notification, onSuccess, onFailure, maxTTL string
	detach, reuse, hibernateOnExit                                      bool
	cpu                                                                 float64
	memory, disk                                                        int64
	argv                                                                []string
	components                                                          []string
	profiles                                                            []config.ApplicationProfile
	instructions                                                        []string
	github                                                              *config.GitHubCredential
	componentsSet, resourcesSet, regionSet, workspaceSet                bool
}

func parseRunOptions(args []string) (runOptions, error) {
	var result runOptions
	if len(args) == 0 {
		return result, fmt.Errorf("box name is required")
	}
	result.name = args[0]
	value := func(i *int, arg, name string) (string, error) {
		if text, ok := strings.CutPrefix(arg, name+"="); ok {
			return text, nil
		}
		*i++
		if *i >= len(args) {
			return "", fmt.Errorf("%s requires a value", name)
		}
		return args[*i], nil
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			result.argv = append([]string(nil), args[i+1:]...)
			break
		}
		switch {
		case arg == "--detach" || arg == "-d":
			result.detach = true
		case arg == "--reuse":
			result.reuse = true
		case arg == "--hibernate-on-exit":
			result.hibernateOnExit = true
		case arg == "--component" || strings.HasPrefix(arg, "--component="):
			text, err := value(&i, arg, "--component")
			if err != nil {
				return result, err
			}
			if _, err := components.Get(text); err != nil {
				return result, err
			}
			result.components = append(result.components, text)
			result.componentsSet = true
		case arg == "--application-profile" || strings.HasPrefix(arg, "--application-profile="):
			text, err := value(&i, arg, "--application-profile")
			if err != nil {
				return result, err
			}
			application, path, ok := strings.Cut(text, "=")
			if !ok || application == "" || path == "" {
				return result, fmt.Errorf("--application-profile requires APPLICATION=PATH")
			}
			if _, err := components.Get(application); err != nil {
				return result, err
			}
			result.profiles = append(result.profiles, config.ApplicationProfile{Application: application, Path: path})
		case arg == "--github-credential" || strings.HasPrefix(arg, "--github-credential="):
			text, err := value(&i, arg, "--github-credential")
			if err != nil {
				return result, err
			}
			result.github, err = parseGitHubCredential(text)
			if err != nil {
				return result, err
			}
		case arg == "--instructions" || strings.HasPrefix(arg, "--instructions="):
			text, err := value(&i, arg, "--instructions")
			if err != nil {
				return result, err
			}
			result.instructions = append(result.instructions, text)
		case arg == "--region" || strings.HasPrefix(arg, "--region="):
			text, err := value(&i, arg, "--region")
			if err != nil {
				return result, err
			}
			result.region, result.regionSet = text, true
		case arg == "--cpu" || strings.HasPrefix(arg, "--cpu="):
			text, err := value(&i, arg, "--cpu")
			if err != nil {
				return result, err
			}
			result.cpu, err = strconv.ParseFloat(text, 64)
			if err != nil || result.cpu <= 0 {
				return result, fmt.Errorf("--cpu must be positive")
			}
			result.resourcesSet = true
		case arg == "--memory" || strings.HasPrefix(arg, "--memory="):
			text, err := value(&i, arg, "--memory")
			if err != nil {
				return result, err
			}
			result.memory, err = strconv.ParseInt(text, 10, 64)
			if err != nil || result.memory <= 0 {
				return result, fmt.Errorf("--memory must be positive MiB")
			}
			result.resourcesSet = true
		case arg == "--disk" || strings.HasPrefix(arg, "--disk="):
			text, err := value(&i, arg, "--disk")
			if err != nil {
				return result, err
			}
			result.disk, err = strconv.ParseInt(text, 10, 64)
			if err != nil || result.disk <= 0 {
				return result, fmt.Errorf("--disk must be positive GiB")
			}
			result.resourcesSet = true
		case arg == "--workspace" || strings.HasPrefix(arg, "--workspace="):
			text, err := value(&i, arg, "--workspace")
			if err != nil {
				return result, err
			}
			result.workspace, result.workspaceSet = text, true
		case arg == "--notification-policy" || strings.HasPrefix(arg, "--notification-policy="):
			text, err := value(&i, arg, "--notification-policy")
			if err != nil {
				return result, err
			}
			result.notification = text
		case arg == "--on-success" || strings.HasPrefix(arg, "--on-success="):
			text, err := value(&i, arg, "--on-success")
			if err != nil {
				return result, err
			}
			result.onSuccess = text
		case arg == "--on-failure" || strings.HasPrefix(arg, "--on-failure="):
			text, err := value(&i, arg, "--on-failure")
			if err != nil {
				return result, err
			}
			result.onFailure = text
		case arg == "--max-ttl" || strings.HasPrefix(arg, "--max-ttl="):
			text, err := value(&i, arg, "--max-ttl")
			if err != nil {
				return result, err
			}
			if _, err := time.ParseDuration(text); err != nil {
				return result, fmt.Errorf("--max-ttl: %w", err)
			}
			result.maxTTL = text
		default:
			return result, fmt.Errorf("unknown box option %q", arg)
		}
	}
	if result.detach && result.hibernateOnExit {
		return result, fmt.Errorf("--detach and --hibernate-on-exit cannot be combined")
	}
	return result, provider.ValidateName(result.name)
}

func defaultSetup(c config.Context) config.CreationSetup {
	ids := make([]string, 0, len(components.Registry))
	for id := range components.Registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return config.CreationSetup{Version: 1, Save: true, Region: c.Cluster, Resources: provider.Resources{CPU: 2, MemoryMiB: 4096, DiskGiB: 10}, Components: ids, Workspace: "/data/workspace", OnSuccess: "retain", OnFailure: "retain", MaxTTL: "8h"}
}

func applyRunOptions(setup *config.CreationSetup, opts runOptions) {
	if opts.componentsSet {
		setup.Components = append([]string(nil), opts.components...)
	}
	if opts.regionSet {
		setup.Region = opts.region
	}
	if opts.resourcesSet {
		if opts.cpu > 0 {
			setup.Resources.CPU = opts.cpu
		}
		if opts.memory > 0 {
			setup.Resources.MemoryMiB = opts.memory
		}
		if opts.disk > 0 {
			setup.Resources.DiskGiB = opts.disk
		}
	}
	if opts.workspaceSet {
		setup.Workspace = opts.workspace
	}
	if len(opts.profiles) > 0 {
		setup.ApplicationProfiles = append([]config.ApplicationProfile(nil), opts.profiles...)
	}
	if opts.github != nil {
		copy := *opts.github
		setup.GitHub = &copy
	}
	if len(opts.instructions) > 0 {
		setup.Instructions = append([]string(nil), opts.instructions...)
	}
	if opts.notification != "" {
		setup.NotificationPolicy = opts.notification
	}
	if opts.onSuccess != "" {
		setup.OnSuccess = opts.onSuccess
	}
	if opts.onFailure != "" {
		setup.OnFailure = opts.onFailure
	}
	if opts.maxTTL != "" {
		setup.MaxTTL = opts.maxTTL
	}
}

type githubAccount struct {
	Host, User, Protocol string
	Active               bool
}

func (a *App) discoverGitHub(ctx context.Context) []githubAccount {
	if a.Runner == nil {
		return nil
	}
	result, err := a.Runner.Run(ctx, []string{"gh", "auth", "status"}, nil, nil, nil)
	if err != nil && len(result.Stdout) == 0 && len(result.Stderr) == 0 {
		return nil
	}
	text := string(append(append([]byte(nil), result.Stdout...), result.Stderr...))
	var accounts []githubAccount
	host := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			host = strings.TrimSuffix(trimmed, ":")
			continue
		}
		if host == "" {
			continue
		}
		var user string
		if marker := " as "; strings.Contains(trimmed, marker) {
			user = strings.Fields(strings.SplitN(trimmed, marker, 2)[1])[0]
		} else if marker := "account "; strings.Contains(trimmed, marker) {
			user = strings.Fields(strings.SplitN(trimmed, marker, 2)[1])[0]
		}
		if user != "" {
			accounts = append(accounts, githubAccount{Host: host, User: strings.Trim(user, "()"), Protocol: "https"})
			continue
		}
		if len(accounts) > 0 && strings.Contains(strings.ToLower(trimmed), "active account:") {
			accounts[len(accounts)-1].Active = strings.EqualFold(strings.TrimSpace(strings.SplitN(trimmed, ":", 2)[1]), "true")
		}
		if len(accounts) > 0 && strings.Contains(trimmed, "protocol:") {
			protocol := strings.TrimSpace(strings.SplitN(trimmed, "protocol:", 2)[1])
			if protocol == "ssh" || protocol == "https" {
				accounts[len(accounts)-1].Protocol = protocol
			}
		}
	}
	return accounts
}

func instructionCandidates(environ map[string]string) []string {
	seen := make(map[string]bool)
	var result []string
	add := func(path string) {
		if path == "" || seen[path] || strings.ToLower(filepath.Ext(path)) != ".md" {
			return
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			absolute, _ := filepath.Abs(path)
			seen[absolute] = true
			result = append(result, absolute)
		}
	}
	add(environ["VMBOX_INSTRUCTIONS_FILE"])
	matches, _ := filepath.Glob("*.[mM][dD]")
	for _, path := range matches {
		add(path)
	}
	sort.Strings(result)
	return result
}

type setupRow struct {
	value string
	kind  string
	index int
	label string
}

type crlfWriter struct{ io.Writer }

func (w crlfWriter) Write(data []byte) (int, error) {
	converted := bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	if _, err := w.Writer.Write(converted); err != nil {
		return 0, err
	}
	return len(data), nil
}

func readMenuKey(reader *bufio.Reader) (byte, error) {
	key, err := reader.ReadByte()
	if err != nil {
		return 0, errSetupCancelled
	}
	switch key {
	case 'q', 'Q', 0x03, 0x04:
		return 0, errSetupCancelled
	case 0x1b:
		// Arrow keys normally arrive in one buffered ESC [ A/B sequence. A bare
		// Escape must cancel immediately instead of blocking for two more bytes.
		if reader.Buffered() < 2 {
			return 0, errSetupCancelled
		}
		first, err := reader.ReadByte()
		if err != nil || first != '[' {
			return 0, errSetupCancelled
		}
		second, err := reader.ReadByte()
		if err != nil {
			return 0, errSetupCancelled
		}
		switch second {
		case 'A':
			return 'k', nil
		case 'B':
			return 'j', nil
		default:
			return 0, errSetupCancelled
		}
	default:
		return key, nil
	}
}

func (a *App) configureSetup(ctx context.Context, c config.Context, p provider.Provider, boxName string, setup config.CreationSetup, argv []string) (config.CreationSetup, error) {
	restore, err := makeRaw(a.In)
	if err != nil {
		return setup, fmt.Errorf("configure setup terminal: %w", err)
	}
	defer restore()
	output := a.Out
	if charDevice(a.Out) {
		output = crlfWriter{a.Out}
	}
	home := a.Environ["HOME"]
	profiles, _ := components.DiscoverConfigured(home, a.Environ)
	github := a.discoverGitHub(ctx)
	instructions := instructionCandidates(a.Environ)
	instructionSeen := make(map[string]bool)
	for _, path := range instructions {
		instructionSeen[path] = true
	}
	for _, path := range setup.Instructions {
		absolute, _ := filepath.Abs(path)
		if info, err := os.Stat(absolute); err == nil && info.Mode().IsRegular() && !instructionSeen[absolute] {
			instructions = append(instructions, absolute)
			instructionSeen[absolute] = true
		}
	}
	sort.Strings(instructions)
	regions := setupRegions(ctx, c, setup.Region, p)
	regionSelected := -1
	for i, region := range regions {
		if region.ID == setup.Region {
			regionSelected = i
		}
	}
	if regionSelected < 0 && len(regions) > 0 {
		regionSelected = 0
	}
	selectedComponents := make(map[string]bool)
	for _, id := range setup.Components {
		selectedComponents[id] = true
	}
	selectedProfiles := make(map[string]bool)
	for _, profile := range setup.ApplicationProfiles {
		selectedProfiles[profile.Application+"\x00"+profile.Path] = true
	}
	// Active profiles are visibly preselected on a new setup. Nothing leaves
	// the laptop until the user reaches the confirmation row.
	if len(setup.ApplicationProfiles) == 0 {
		for _, profile := range profiles {
			selectedProfiles[profile.Component+"\x00"+profile.Directory] = profile.Active
		}
	}
	selectedGitHub := -1
	if setup.GitHub != nil {
		for i, account := range github {
			if account.Host == setup.GitHub.Host && account.User == setup.GitHub.User && account.Protocol == setup.GitHub.Protocol {
				selectedGitHub = i
			}
		}
	}
	selectedInstructions := make(map[string]bool)
	for _, path := range setup.Instructions {
		absolute, _ := filepath.Abs(path)
		selectedInstructions[absolute] = true
	}
	resourcePresets := []provider.Resources{{CPU: 1, MemoryMiB: 2048, DiskGiB: 10}, {CPU: 2, MemoryMiB: 4096, DiskGiB: 10}, {CPU: 4, MemoryMiB: 8192, DiskGiB: 20}}
	resourceNames := []string{"Small", "Standard", "Large"}
	resourceSelected := -1
	for i, value := range resourcePresets {
		if value == setup.Resources {
			resourceSelected = i
		}
	}
	if resourceSelected < 0 {
		resourcePresets = append([]provider.Resources{setup.Resources}, resourcePresets...)
		resourceNames = append([]string{"Custom"}, resourceNames...)
		resourceSelected = 0
	}
	var rows []setupRow
	ids := make([]string, 0, len(components.Registry))
	for id := range components.Registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i, region := range regions {
		rows = append(rows, setupRow{kind: "region", index: i, value: region.ID, label: region.Label + "  [" + region.ID + "]"})
	}
	for i, id := range ids {
		rows = append(rows, setupRow{kind: "component", index: i, label: id})
	}
	for i, name := range resourceNames {
		rows = append(rows, setupRow{kind: "resource", index: i, label: name})
	}
	for i, profile := range profiles {
		label := profile.Component + "  " + profile.Directory
		rows = append(rows, setupRow{kind: "profile", index: i, label: label})
	}
	for i, account := range github {
		rows = append(rows, setupRow{kind: "github", index: i, label: account.User + "@" + account.Host + " (" + account.Protocol + ")"})
	}
	for i, path := range instructions {
		rows = append(rows, setupRow{kind: "instructions", index: i, label: path})
	}
	rows = append(rows, setupRow{kind: "save", label: "Save this setup for --reuse in this working directory"})
	rows = append(rows, setupRow{kind: "cancel", label: "Cancel"})
	rows = append(rows, setupRow{kind: "confirm", label: "Confirm and create"})
	cursor := 0
	reader := bufio.NewReader(a.In)
	for {
		fmt.Fprint(output, "\033[2J\033[H")
		fmt.Fprintf(output, "New box configuration: %s\n", boxName)
		fmt.Fprintln(output, "↑/↓ or j/k: move  Space: toggle/select  Enter: Cancel/Confirm  q/Esc/Ctrl-C: cancel")
		command, _ := json.Marshal(argv)
		if len(argv) == 0 {
			command, _ = json.Marshal(defaultSession(false))
		}
		displayRegion := setup.Region
		if regionSelected >= 0 {
			displayRegion = regions[regionSelected].ID
		}
		fmt.Fprintf(output, "Context  %s / %s    Region  %s    Workspace  %s\n", c.Name, c.Provider, displayRegion, setup.Workspace)
		fmt.Fprintf(output, "Exact argv  %s    Save for --reuse  %t\n", command, setup.Save)
		last := ""
		for i, row := range rows {
			if row.kind != last {
				switch row.kind {
				case "region":
					fmt.Fprintln(output, "Location")
				case "component":
					fmt.Fprintln(output, "Components")
				case "resource":
					fmt.Fprintln(output, "Resources")
				case "profile":
					fmt.Fprintln(output, "Application profiles (auth/config only; never Markdown)")
				case "github":
					fmt.Fprintln(output, "GitHub credential")
				case "instructions":
					fmt.Fprintln(output, "Markdown instructions")
				case "save":
					fmt.Fprintln(output, "Reuse")
				case "confirm":
					fmt.Fprintf(output, "Notifications  %s    Lifecycle  %s/%s max %s\n", emptyLabel(setup.NotificationPolicy), setup.OnSuccess, setup.OnFailure, setup.MaxTTL)
				}
			}
			last = row.kind
			marker := " "
			switch row.kind {
			case "region":
				if regionSelected == row.index {
					marker = "x"
				}
			case "save":
				if setup.Save {
					marker = "x"
				}
			case "component":
				if selectedComponents[row.label] {
					marker = "x"
				}
			case "resource":
				if resourceSelected == row.index {
					marker = "x"
				}
				value := resourcePresets[row.index]
				row.label = fmt.Sprintf("%-9s %.0f CPU / %d MiB / %d GiB", row.label, value.CPU, value.MemoryMiB, value.DiskGiB)
			case "profile":
				if selectedProfiles[profiles[row.index].Component+"\x00"+profiles[row.index].Directory] {
					marker = "x"
				}
			case "github":
				if selectedGitHub == row.index {
					marker = "x"
				}
			case "instructions":
				if selectedInstructions[instructions[row.index]] {
					marker = "x"
				}
			case "cancel", "confirm":
				marker = ">"
			}
			prefix := "  "
			if i == cursor {
				prefix = "> "
			}
			if row.kind == "cancel" || row.kind == "confirm" {
				fmt.Fprintf(output, "%s[ %s ]\n", prefix, row.label)
			} else {
				fmt.Fprintf(output, "%s[%s] %s\n", prefix, marker, row.label)
			}
		}
		key, err := readMenuKey(reader)
		if err != nil {
			fmt.Fprint(output, "\033[2J\033[H")
			return setup, errSetupCancelled
		}
		switch key {
		case 'j':
			cursor = (cursor + 1) % len(rows)
		case 'k':
			cursor = (cursor - 1 + len(rows)) % len(rows)
		case '\n', '\r':
			if rows[cursor].kind == "cancel" {
				fmt.Fprint(output, "\033[2J\033[H")
				return setup, errSetupCancelled
			}
			if rows[cursor].kind != "confirm" {
				continue
			}
			setup.Components = setup.Components[:0]
			for _, id := range ids {
				if selectedComponents[id] {
					setup.Components = append(setup.Components, id)
				}
			}
			setup.Resources = resourcePresets[resourceSelected]
			if regionSelected >= 0 {
				setup.Region = regions[regionSelected].ID
			}
			setup.ApplicationProfiles = nil
			for _, profile := range profiles {
				if selectedProfiles[profile.Component+"\x00"+profile.Directory] {
					setup.ApplicationProfiles = append(setup.ApplicationProfiles, config.ApplicationProfile{Application: profile.Component, Path: profile.Directory})
				}
			}
			setup.GitHub = nil
			if selectedGitHub >= 0 {
				account := github[selectedGitHub]
				setup.GitHub = &config.GitHubCredential{Host: account.Host, User: account.User, Protocol: account.Protocol}
			}
			setup.Instructions = nil
			for _, path := range instructions {
				if selectedInstructions[path] {
					setup.Instructions = append(setup.Instructions, path)
				}
			}
			fmt.Fprint(output, "\033[2J\033[H")
			return setup, nil
		case ' ':
			row := rows[cursor]
			switch row.kind {
			case "region":
				regionSelected = row.index
			case "save":
				setup.Save = !setup.Save
			case "component":
				selectedComponents[row.label] = !selectedComponents[row.label]
			case "resource":
				resourceSelected = row.index
			case "profile":
				profile := profiles[row.index]
				key := profile.Component + "\x00" + profile.Directory
				selecting := !selectedProfiles[key]
				if selecting {
					for _, candidate := range profiles {
						if candidate.Component == profile.Component {
							selectedProfiles[candidate.Component+"\x00"+candidate.Directory] = false
						}
					}
				}
				selectedProfiles[key] = selecting
			case "github":
				if selectedGitHub == row.index {
					selectedGitHub = -1
				} else {
					selectedGitHub = row.index
				}
			case "instructions":
				path := instructions[row.index]
				selectedInstructions[path] = !selectedInstructions[path]
			}
		}
	}
}

func emptyLabel(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func defaultSession(detach bool) []string {
	if detach {
		return []string{"sh", "-c", `tmux has-session -t vmbox 2>/dev/null || exec tmux new-session -d -s vmbox -c /data/workspace vmbox-runtime welcome`}
	}
	return []string{"tmux", "new-session", "-A", "-s", "vmbox", "-c", "/data/workspace", "vmbox-runtime", "welcome"}
}

func interactiveSession(argv []string) []string {
	result := []string{"tmux", "new-session", "-A", "-s", "vmbox", "-c", "/data/workspace", "--"}
	return append(result, argv...)
}

type upload struct {
	path string
	mode string
	data []byte
}

type preparedSetup struct {
	setup        config.CreationSetup
	uploads      []upload
	githubToken  string
	githubName   string
	githubEmail  string
	applications []string
}

func profileDestination(application, source string) (string, error) {
	base := filepath.Base(source)
	switch application {
	case "codex":
		return filepath.Join("/data/home/.codex", base), nil
	case "claude":
		if base == ".claude.json" {
			return "/data/home/.claude.json", nil
		}
		return filepath.Join("/data/home/.claude", base), nil
	case "opencode":
		return filepath.Join("/data/home/.config/opencode", base), nil
	default:
		return "", fmt.Errorf("%s has no uploadable application profile", application)
	}
}

func (a *App) prepareSetup(ctx context.Context, setup config.CreationSetup) (preparedSetup, error) {
	prepared := preparedSetup{setup: setup}
	workspace := filepath.Clean(prepared.setup.Workspace)
	if workspace != "/data/workspace" && !strings.HasPrefix(workspace, "/data/workspace/") {
		return prepared, fmt.Errorf("workspace must be /data/workspace or a directory below it")
	}
	prepared.setup.Workspace = workspace
	for _, selected := range prepared.setup.ApplicationProfiles {
		profile, err := components.ProfileAt(selected.Application, selected.Path)
		if err != nil {
			fmt.Fprintf(a.Err, "vmbox: saved application profile missing; skipped: %s (%v)\n", selected.Path, err)
			continue
		}
		prepared.applications = append(prepared.applications, selected.Application)
		for _, path := range profile.Files {
			data, err := os.ReadFile(path)
			if err != nil {
				return prepared, err
			}
			destination, err := profileDestination(selected.Application, path)
			if err != nil {
				return prepared, err
			}
			prepared.uploads = append(prepared.uploads, upload{path: destination, mode: "0600", data: data})
		}
	}
	available, missing := components.ValidateInstructions(prepared.setup.Instructions)
	for _, path := range missing {
		fmt.Fprintf(a.Err, "vmbox: Markdown instruction file missing; skipped: %s\n", path)
	}
	if len(available) > 0 {
		var combined bytes.Buffer
		for _, path := range available {
			data, err := os.ReadFile(path)
			if err != nil {
				return prepared, err
			}
			fmt.Fprintf(&combined, "<!-- vmbox source: %s -->\n", path)
			combined.Write(data)
			if combined.Len() == 0 || combined.Bytes()[combined.Len()-1] != '\n' {
				combined.WriteByte('\n')
			}
		}
		prepared.uploads = append(prepared.uploads,
			upload{path: filepath.Join(workspace, "AGENTS.md"), mode: "0644", data: append([]byte(nil), combined.Bytes()...)},
			upload{path: filepath.Join(workspace, "CLAUDE.md"), mode: "0644", data: append([]byte(nil), combined.Bytes()...)})
	}
	if prepared.setup.GitHub != nil {
		credential := prepared.setup.GitHub
		result, err := a.Runner.Run(ctx, []string{"gh", "auth", "token", "--hostname", credential.Host, "--user", credential.User}, nil, nil, nil)
		if err != nil || result.ExitCode != 0 || len(bytes.TrimSpace(result.Stdout)) == 0 {
			fmt.Fprintf(a.Err, "vmbox: selected GitHub credential is unavailable; skipped: %s@%s\n", credential.User, credential.Host)
		} else {
			prepared.githubToken = strings.TrimSpace(string(result.Stdout))
			prepared.githubName, prepared.githubEmail = a.githubIdentity(ctx, *credential)
		}
	}
	return prepared, nil
}

// githubIdentity resolves the Git commit identity for a selected GitHub account.
// The lookup is local and the box only ever receives the resolved name and
// address. The answer is used only when the API confirms it describes the
// selected account: `gh api` answers for whichever account is active on that
// host, and attributing someone else's name to these commits would be worse
// than falling back to the account's own noreply address.
func (a *App) githubIdentity(ctx context.Context, credential config.GitHubCredential) (string, string) {
	fallbackEmail := credential.User + "@users.noreply.github.com"
	field := func(query string) string {
		result, err := a.Runner.Run(ctx, []string{"gh", "api", "user", "--hostname", credential.Host, "--jq", query}, nil, nil, nil)
		if err != nil || result.ExitCode != 0 {
			return ""
		}
		return strings.TrimSpace(string(result.Stdout))
	}
	if field(".login") != credential.User {
		return credential.User, fallbackEmail
	}
	name := field(".name // .login")
	if name == "" {
		name = credential.User
	}
	email := field(".email // empty")
	if email == "" {
		email = fallbackEmail
	}
	return name, email
}

type setupExec func(context.Context, []string, provider.ExecOptions) (provider.ExecResult, error)

const (
	standaloneSetupStateEnv = "VMBOX_SETUP_STATE"
	standaloneSetupMarker   = "/data/home/.vmbox-setup-complete"
	standaloneSetupProbe    = `if [ "${VMBOX_SETUP_STATE:-}" != pending ] || [ -f /data/home/.vmbox-setup-complete ]; then printf 'complete\n'; else printf 'pending\n'; fi`
)

func (a *App) standaloneSetupPending(ctx context.Context, p provider.Provider, name string) (bool, error) {
	result, err := p.Exec(ctx, name, []string{"sh", "-c", standaloneSetupProbe}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		return false, fmt.Errorf("inspect saved setup state inside %q", name)
	}
	switch strings.TrimSpace(result.Stdout) {
	case "pending":
		return true, nil
	case "complete":
		return false, nil
	default:
		return false, fmt.Errorf("inspect saved setup state inside %q: unexpected response", name)
	}
}

func (a *App) markStandaloneSetupComplete(ctx context.Context, p provider.Provider, name string) error {
	data := []byte("complete\n")
	digest := sha256.Sum256(data)
	result, err := p.Exec(ctx, name, []string{"vmbox-runtime", "put-file", standaloneSetupMarker, "0600"}, provider.ExecOptions{Stdin: bytes.NewReader(data), Stderr: a.Err})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != fmt.Sprintf("%x", digest[:]) {
		return fmt.Errorf("record completed setup inside %q", name)
	}
	return nil
}

func (a *App) uploadPrepared(ctx context.Context, p provider.Provider, name string, prepared preparedSetup) error {
	return a.uploadPreparedWith(ctx, name, prepared, func(ctx context.Context, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
		return p.Exec(ctx, name, argv, opts)
	})
}

func (a *App) uploadPreparedWith(ctx context.Context, name string, prepared preparedSetup, execute setupExec) error {
	if len(prepared.uploads) > 0 {
		fmt.Fprintf(a.Err, "vmbox: syncing %d selected agent/instruction file(s)\n", len(prepared.uploads))
		request := boxruntime.SyncRequest{Files: make([]boxruntime.SyncFile, 0, len(prepared.uploads))}
		for index, item := range prepared.uploads {
			fmt.Fprintf(a.Err, "vmbox: batching file %d/%d: %s\n", index+1, len(prepared.uploads), item.path)
			request.Files = append(request.Files, boxruntime.SyncFile{Path: item.path, Mode: item.mode, Data: item.data})
		}
		payload, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("encode file sync: %w", err)
		}
		digest := sha256.Sum256(payload)
		expected := fmt.Sprintf("%x", digest[:])
		result, err := execute(ctx, []string{"vmbox-runtime", "sync-files"}, provider.ExecOptions{Stdin: bytes.NewReader(payload), Stdout: io.Discard, Stderr: a.Err})
		if err != nil {
			return fmt.Errorf("sync selected files: %w", err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("sync selected files exited with status %d", result.ExitCode)
		}
		if strings.TrimSpace(result.Stdout) != expected {
			return fmt.Errorf("batched file checksum verification failed")
		}
	}
	setupRequest := boxruntime.SetupRequest{Workspace: prepared.setup.Workspace, Applications: append([]string(nil), prepared.applications...)}
	if prepared.githubToken != "" && prepared.setup.GitHub != nil {
		credential := prepared.setup.GitHub
		fmt.Fprintf(a.Err, "vmbox: syncing GitHub credential for %s@%s\n", credential.User, credential.Host)
		setupRequest.GitHub = &boxruntime.GitHubSetup{Host: credential.Host, User: credential.User, Protocol: credential.Protocol, Token: prepared.githubToken, Name: prepared.githubName, Email: prepared.githubEmail}
	}
	fmt.Fprintf(a.Err, "vmbox: configuring agent trust for %s\n", prepared.setup.Workspace)
	for _, application := range uniqueVerifiableApplications(prepared.applications) {
		fmt.Fprintf(a.Err, "vmbox: verifying %s authentication inside %q\n", application, name)
	}
	payload, err := json.Marshal(setupRequest)
	if err != nil {
		return fmt.Errorf("encode remote setup: %w", err)
	}
	result, err := execute(ctx, []string{"vmbox-runtime", "setup"}, provider.ExecOptions{Stdin: bytes.NewReader(payload), Stderr: a.Err})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("configure credentials and workspace trust inside box")
	}
	var setupResult boxruntime.SetupResult
	if err := json.Unmarshal([]byte(result.Stdout), &setupResult); err != nil {
		return fmt.Errorf("decode remote setup result: %w", err)
	}
	if setupRequest.GitHub != nil {
		secured, err := execute(ctx, []string{"sh", "-c", boxruntime.SecureGitHubConfigScript}, provider.ExecOptions{Stdout: io.Discard, Stderr: a.Err})
		if err != nil || secured.ExitCode != 0 {
			return fmt.Errorf("secure GitHub configuration as workload user inside %q", name)
		}
		verification, err := execute(ctx, []string{"gh", "auth", "status", "--hostname", setupRequest.GitHub.Host}, provider.ExecOptions{Stdout: io.Discard, Stderr: io.Discard})
		if err != nil || verification.ExitCode != 0 {
			return fmt.Errorf("verify GitHub authentication as workload user inside %q", name)
		}
		fmt.Fprintf(a.Err, "vmbox: GitHub credential is ready\n")
	}
	fmt.Fprintf(a.Err, "vmbox: agent trust is ready\n")
	a.reportApplicationAuthentication(prepared.applications, setupResult.Authentication)
	return nil
}

func (a *App) ensureBootstrap(ctx context.Context, p provider.Provider, name string, selected []string, restoreComponents bool) error {
	bootstrapper, ok := p.(provider.Bootstrapper)
	if !ok {
		return nil
	}
	request, err := a.bootstrapRequest(selected)
	if err != nil {
		return err
	}
	request.RestoreComponents = restoreComponents
	selection := strings.Join(selected, ", ")
	if restoreComponents {
		selection = "saved component selection"
	} else if selection == "" {
		selection = "core runtime only"
	}
	started := time.Now()
	fmt.Fprintf(a.Err, "vmbox: bootstrapping %q (%s); first install may take a few minutes\n", name, selection)
	done := make(chan error, 1)
	go func() {
		done <- bootstrapper.Bootstrap(ctx, name, request)
	}()
	interval := a.ProgressInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("bootstrap box %q: %w", name, err)
			}
			fmt.Fprintf(a.Err, "vmbox: runtime and tools are ready in %q (%s)\n", name, elapsedLabel(time.Since(started)))
			return nil
		case <-ticker.C:
			fmt.Fprintf(a.Err, "vmbox: still bootstrapping %q (%s elapsed)\n", name, elapsedLabel(time.Since(started)))
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func elapsedLabel(elapsed time.Duration) string {
	if elapsed < time.Second {
		return "<1s"
	}
	return elapsed.Round(time.Second).String()
}

func (a *App) bootstrapRequest(selected []string) (provider.BootstrapRequest, error) {
	assetDir := a.Environ["VMBOX_RUNTIME_ASSET_DIR"]
	if assetDir == "" {
		dataHome := a.Environ["XDG_DATA_HOME"]
		if dataHome == "" {
			home := a.Environ["HOME"]
			if home == "" {
				var err error
				home, err = os.UserHomeDir()
				if err != nil {
					return provider.BootstrapRequest{}, err
				}
			}
			dataHome = filepath.Join(home, ".local", "share")
		}
		assetDir = filepath.Join(dataHome, "vmbox", "runtime")
	}
	request := provider.BootstrapRequest{Components: append([]string(nil), selected...), RuntimeBinaries: make(map[string][]byte)}
	for _, arch := range []string{"amd64", "arm64"} {
		path := filepath.Join(assetDir, "vmbox-runtime-linux-"+arch)
		data, err := os.ReadFile(path)
		if err == nil {
			request.RuntimeBinaries[arch] = data
		} else if !os.IsNotExist(err) {
			return provider.BootstrapRequest{}, fmt.Errorf("read runtime asset %s: %w", path, err)
		}
	}
	entrypointPath := filepath.Join(assetDir, "vmbox-entrypoint")
	entrypoint, err := os.ReadFile(entrypointPath)
	if err != nil {
		return provider.BootstrapRequest{}, fmt.Errorf("read runtime asset %s: %w (reinstall with ./install.sh)", entrypointPath, err)
	}
	request.Entrypoint = entrypoint
	if len(request.RuntimeBinaries) == 0 {
		return provider.BootstrapRequest{}, fmt.Errorf("no workload runtime assets found in %s; reinstall with ./install.sh", assetDir)
	}
	return request, nil
}

func welcome(box provider.Box, contextName, cost string) []byte {
	connection := box.Connection.Transport
	if box.Connection.Endpoint != "" {
		connection += " " + box.Connection.Endpoint
	}
	storage := "unavailable"
	if box.Storage != nil {
		storage = fmt.Sprintf("%d GiB at %s", box.Storage.SizeGiB, box.Storage.MountPath)
	}
	return []byte(fmt.Sprintf("vmbox %s is ready\nProvider: %s (%s)  Region: %s\nSpecs: %.2g CPU / %d MiB RAM / %s\nConnection: %s  Workspace: /data/workspace  State: %s\nCost: %s\nDetach: press Ctrl-a, release both keys, then press d\nUseful: vmbox status %s | vmbox stop %s | vmbox cost %s\n\n", box.Name, box.Provider, contextName, box.Region, box.Resources.CPU, box.Resources.MemoryMiB, storage, connection, box.State, cost, box.Name, box.Name, box.Name))
}

func (a *App) uploadWelcome(ctx context.Context, p provider.Provider, box provider.Box, contextName string) error {
	cost := "unavailable; use vmbox cost " + box.Name
	if usage, err := p.Usage(ctx, box.Name); err == nil {
		if usage.Cost.Available {
			cost = fmt.Sprintf("%.4f %s accrued", usage.Cost.Accrued, usage.Cost.Currency)
		} else if usage.Cost.Detail != "" {
			cost = usage.Cost.Detail
		}
	}
	result, err := p.Exec(ctx, box.Name, []string{"vmbox-runtime", "put-file", "/data/home/.vmbox-welcome", "0600"}, provider.ExecOptions{Stdin: bytes.NewReader(welcome(box, contextName, cost)), Stdout: io.Discard, Stderr: a.Err})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("write in-session welcome exited with status %d", result.ExitCode)
	}
	return nil
}

func normalizeWorkingDirectory(directory string) (string, error) {
	if strings.TrimSpace(directory) == "" {
		return "", fmt.Errorf("working directory is empty")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	return filepath.Clean(absolute), nil
}

func setupScopeKey(contextName, workingDirectory string) (string, string, error) {
	directory, err := normalizeWorkingDirectory(workingDirectory)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256([]byte(directory))
	return fmt.Sprintf("%s:%x", contextName, digest), directory, nil
}

func (a *App) workingDirectory() (string, error) {
	directory := a.WorkingDir
	if directory == "" {
		var err error
		directory, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
	}
	return normalizeWorkingDirectory(directory)
}

func loadSetup(file config.File, contextName, workingDirectory string) (config.CreationSetup, error) {
	key, directory, err := setupScopeKey(contextName, workingDirectory)
	if err != nil {
		return config.CreationSetup{}, err
	}
	setup, ok := file.LastSetups[key]
	if !ok || setup.Version != 1 {
		return setup, fmt.Errorf("no complete reusable setup is saved for context %q in %s", contextName, directory)
	}
	setup.Save = true
	return setup, nil
}

func saveSetup(path string, file config.File, contextName, workingDirectory string, setup config.CreationSetup) error {
	if file.LastSetups == nil {
		file.LastSetups = make(map[string]config.CreationSetup)
	}
	key, directory, err := setupScopeKey(contextName, workingDirectory)
	if err != nil {
		return err
	}
	setup.Version = 1
	setup.SavedAt = time.Now().UTC()
	setup.WorkingDirectory = directory
	file.LastSetups[key] = setup
	return config.Save(path, file)
}

func (a *App) selectStandaloneBox(ctx context.Context, p provider.Provider, title string) (string, error) {
	boxes, err := p.List(ctx)
	if err != nil {
		return "", err
	}
	if len(boxes) == 0 {
		return "", fmt.Errorf("no boxes are available")
	}
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].Name < boxes[j].Name })
	if a.IsTerminal == nil || !a.IsTerminal() {
		for _, box := range boxes {
			fmt.Fprintf(a.Out, "%s\t%s\n", box.Name, box.State)
		}
		return "", fmt.Errorf("selection requires an interactive terminal")
	}
	reader := bufio.NewReader(a.In)
	cursor, selected := 0, -1
	confirm := len(boxes)
	for {
		fmt.Fprint(a.Out, "\033[2J\033[H")
		fmt.Fprintln(a.Out, title)
		fmt.Fprintln(a.Out, "↑/↓ or j/k: move  Space: select  Enter: only on Confirm  q: cancel")
		for i, box := range boxes {
			prefix, marker := "  ", " "
			if cursor == i {
				prefix = "> "
			}
			if selected == i {
				marker = "x"
			}
			fmt.Fprintf(a.Out, "%s[%s] %-24s %s\n", prefix, marker, box.Name, box.State)
		}
		prefix := "  "
		if cursor == confirm {
			prefix = "> "
		}
		fmt.Fprintf(a.Out, "%s[ Confirm ]\n", prefix)
		key, readErr := readMenuKey(reader)
		if readErr != nil {
			return "", errSetupCancelled
		}
		switch key {
		case 'j':
			cursor = (cursor + 1) % (len(boxes) + 1)
		case 'k':
			cursor = (cursor - 1 + len(boxes) + 1) % (len(boxes) + 1)
		case ' ':
			if cursor < len(boxes) {
				if selected == cursor {
					selected = -1
				} else {
					selected = cursor
				}
			}
		case '\n', '\r':
			if cursor == confirm && selected >= 0 {
				fmt.Fprint(a.Out, "\033[2J\033[H")
				return boxes[selected].Name, nil
			}
		}
	}
}

func (a *App) selectStandaloneResize(ctx context.Context, p provider.Provider, args []string) (string, []string, error) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:], nil
	}
	name, err := a.selectStandaloneBox(ctx, p, "Select a box to resize")
	return name, args, err
}

func (a *App) selectResizeResources(title string) (provider.Resources, error) {
	presets := []provider.Resources{{CPU: 1, MemoryMiB: 2048}, {CPU: 2, MemoryMiB: 4096}, {CPU: 4, MemoryMiB: 8192}}
	labels := []string{"Small", "Standard", "Large"}
	if a.IsTerminal == nil || !a.IsTerminal() {
		return provider.Resources{}, fmt.Errorf("choosing resize limits requires an interactive terminal or explicit --cpu/--memory")
	}
	reader := bufio.NewReader(a.In)
	cursor, selected := 0, 1
	for {
		fmt.Fprint(a.Out, "\033[2J\033[H")
		fmt.Fprintln(a.Out, title)
		fmt.Fprintln(a.Out, "↑/↓ or j/k: move  Space: select  Enter: only on Confirm  q: cancel")
		for i, value := range presets {
			prefix, marker := "  ", " "
			if cursor == i {
				prefix = "> "
			}
			if selected == i {
				marker = "x"
			}
			fmt.Fprintf(a.Out, "%s[%s] %-10s %.0f CPU / %d MiB RAM\n", prefix, marker, labels[i], value.CPU, value.MemoryMiB)
		}
		prefix := "  "
		if cursor == len(presets) {
			prefix = "> "
		}
		fmt.Fprintf(a.Out, "%s[ Confirm ]\n", prefix)
		key, err := readMenuKey(reader)
		if err != nil {
			return provider.Resources{}, errSetupCancelled
		}
		switch key {
		case 'j':
			cursor = (cursor + 1) % (len(presets) + 1)
		case 'k':
			cursor = (cursor - 1 + len(presets) + 1) % (len(presets) + 1)
		case ' ':
			if cursor < len(presets) {
				selected = cursor
			}
		case '\n', '\r':
			if cursor == len(presets) {
				fmt.Fprint(a.Out, "\033[2J\033[H")
				return presets[selected], nil
			}
		}
	}
}

func (a *App) selectControllerRun(ctx context.Context, c config.Context, token, title string) (string, error) {
	var runs []v1.Run
	if _, err := a.request(ctx, c, token, "GET", "/v1/runs", nil, &runs, nil); err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return "", fmt.Errorf("no runs are available")
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	if a.IsTerminal == nil || !a.IsTerminal() {
		for _, run := range runs {
			fmt.Fprintf(a.Out, "%s\t%s\t%s\n", run.ID, run.Request.Box, run.State)
		}
		return "", fmt.Errorf("selection requires an interactive terminal")
	}
	reader := bufio.NewReader(a.In)
	cursor, selected := 0, -1
	for {
		fmt.Fprint(a.Out, "\033[2J\033[H")
		fmt.Fprintln(a.Out, title)
		fmt.Fprintln(a.Out, "↑/↓ or j/k: move  Space: select  Enter: only on Confirm  q: cancel")
		for i, run := range runs {
			prefix, marker := "  ", " "
			if cursor == i {
				prefix = "> "
			}
			if selected == i {
				marker = "x"
			}
			fmt.Fprintf(a.Out, "%s[%s] %-24s %-18s %s\n", prefix, marker, run.Request.Box, run.ID, run.State)
		}
		prefix := "  "
		if cursor == len(runs) {
			prefix = "> "
		}
		fmt.Fprintf(a.Out, "%s[ Confirm ]\n", prefix)
		key, err := readMenuKey(reader)
		if err != nil {
			return "", errSetupCancelled
		}
		switch key {
		case 'j':
			cursor = (cursor + 1) % (len(runs) + 1)
		case 'k':
			cursor = (cursor - 1 + len(runs) + 1) % (len(runs) + 1)
		case ' ':
			if cursor < len(runs) {
				if selected == cursor {
					selected = -1
				} else {
					selected = cursor
				}
			}
		case '\n', '\r':
			if cursor == len(runs) && selected >= 0 {
				fmt.Fprint(a.Out, "\033[2J\033[H")
				return runs[selected].ID, nil
			}
		}
	}
}

func (a *App) selectControllerLogicalBox(ctx context.Context, c config.Context, token, title string) (string, error) {
	var boxes []v1.LogicalBox
	if _, err := a.request(ctx, c, token, "GET", "/v1/logical-boxes"+fleetQuery(c), nil, &boxes, nil); err != nil {
		return "", err
	}
	if len(boxes) == 0 {
		return "", fmt.Errorf("no logical boxes are available")
	}
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].UpdatedAt.After(boxes[j].UpdatedAt) })
	if a.IsTerminal == nil || !a.IsTerminal() {
		for _, box := range boxes {
			fmt.Fprintf(a.Out, "%s\t%s\n", box.Name, box.State)
		}
		return "", fmt.Errorf("selection requires an interactive terminal")
	}
	reader := bufio.NewReader(a.In)
	cursor, selected := 0, -1
	for {
		fmt.Fprint(a.Out, "\033[2J\033[H")
		fmt.Fprintln(a.Out, title)
		fmt.Fprintln(a.Out, "↑/↓ or j/k: move  Space: select  Enter: only on Confirm  q: cancel")
		for index, box := range boxes {
			prefix, marker := "  ", " "
			if cursor == index {
				prefix = "> "
			}
			if selected == index {
				marker = "x"
			}
			fmt.Fprintf(a.Out, "%s[%s] %-24s %s\n", prefix, marker, box.Name, box.State)
		}
		prefix := "  "
		if cursor == len(boxes) {
			prefix = "> "
		}
		fmt.Fprintf(a.Out, "%s[ Confirm ]\n", prefix)
		key, err := readMenuKey(reader)
		if err != nil {
			return "", errSetupCancelled
		}
		switch key {
		case 'j':
			cursor = (cursor + 1) % (len(boxes) + 1)
		case 'k':
			cursor = (cursor - 1 + len(boxes) + 1) % (len(boxes) + 1)
		case ' ':
			if cursor < len(boxes) {
				if selected == cursor {
					selected = -1
				} else {
					selected = cursor
				}
			}
		case '\n', '\r':
			if cursor == len(boxes) && selected >= 0 {
				fmt.Fprint(a.Out, "\033[2J\033[H")
				return boxes[selected].Name, nil
			}
		}
	}
}
