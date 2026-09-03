package components

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Component struct {
	ID          string         `json:"id"`
	Executables []string       `json:"executables"`
	AuthFiles   []string       `json:"authFiles,omitempty"`
	ConfigFiles []string       `json:"configFiles,omitempty"`
	Defaults    map[string]any `json:"defaults,omitempty"`
	Warning     string         `json:"warning,omitempty"`
}

var Registry = map[string]Component{
	"codex":    {ID: "codex", Executables: []string{"codex"}, AuthFiles: []string{"auth.json"}, ConfigFiles: []string{"config.toml"}, Defaults: map[string]any{"approval_policy": "never", "sandbox_mode": "danger-full-access"}, Warning: "disposable-box full access"},
	"claude":   {ID: "claude", Executables: []string{"claude"}, AuthFiles: []string{".credentials.json"}, ConfigFiles: []string{"settings.json", ".claude.json"}, Defaults: map[string]any{"defaultMode": "bypassPermissions"}, Warning: "disposable-box permission bypass"},
	"opencode": {ID: "opencode", Executables: []string{"opencode"}, AuthFiles: []string{"auth.json"}, ConfigFiles: []string{"opencode.json"}, Warning: "review full-access configuration before upload"},
	"bun":      {ID: "bun", Executables: []string{"bun"}},
	"foundry":  {ID: "foundry", Executables: []string{"forge", "cast", "anvil", "chisel"}},
}

type Profile struct {
	Component string   `json:"component"`
	Name      string   `json:"name"`
	Directory string   `json:"directory"`
	Files     []string `json:"files"`
	Active    bool     `json:"active,omitempty"`
}

func Discover(home string) ([]Profile, error) {
	return DiscoverConfigured(home, nil)
}

// DiscoverConfigured finds portable application profiles and marks the
// profile currently selected by each application's environment variable.
func DiscoverConfigured(home string, environ map[string]string) ([]Profile, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	value := func(key string) string {
		if environ != nil {
			return environ[key]
		}
		return ""
	}
	active := map[string]string{
		"codex": value("CODEX_HOME"), "claude": value("CLAUDE_CONFIG_DIR"), "opencode": value("OPENCODE_CONFIG_DIR"),
	}
	if active["codex"] == "" {
		active["codex"] = filepath.Join(home, ".codex")
	}
	if active["claude"] == "" {
		active["claude"] = filepath.Join(home, ".claude")
	}
	if active["opencode"] == "" {
		active["opencode"] = filepath.Join(home, ".config", "opencode")
	}
	patterns := map[string][]string{
		"codex":    {active["codex"], filepath.Join(home, ".codex*"), filepath.Join(home, ".config", "codex*")},
		"claude":   {active["claude"], filepath.Join(home, ".claude*"), filepath.Join(home, ".config", "claude*")},
		"opencode": {active["opencode"], filepath.Join(home, ".config", "opencode*")},
	}
	var profiles []Profile
	seen := make(map[string]bool)
	for component, globs := range patterns {
		definition := Registry[component]
		var candidates []string
		for _, pattern := range globs {
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, matches...)
		}
		for _, root := range candidates {
			root = filepath.Clean(root)
			key := component + "\x00" + filepath.Clean(root)
			if seen[key] {
				continue
			}
			info, err := os.Stat(root)
			if err != nil || !info.IsDir() {
				continue
			}
			seen[key] = true
			files := safeFiles(root, append(append([]string{}, definition.AuthFiles...), definition.ConfigFiles...))
			// Claude stores the default profile's MCP/project configuration next
			// to ~/.claude rather than inside it.
			if component == "claude" && root == filepath.Join(home, ".claude") {
				if info, statErr := os.Lstat(filepath.Join(home, ".claude.json")); statErr == nil && info.Mode().IsRegular() {
					files = append(files, filepath.Join(home, ".claude.json"))
				}
			}
			if len(files) > 0 {
				profiles = append(profiles, Profile{Component: component, Name: filepath.Base(root), Directory: root, Files: files, Active: root == filepath.Clean(active[component])})
			}
		}
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].Component == profiles[j].Component {
			if profiles[i].Active != profiles[j].Active {
				return profiles[i].Active
			}
			return profiles[i].Directory < profiles[j].Directory
		}
		return profiles[i].Component < profiles[j].Component
	})
	return profiles, nil
}
func safeFiles(root string, names []string) []string {
	var result []string
	for _, name := range names {
		if strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() {
			result = append(result, path)
		}
	}
	return result
}
func ValidateInstructions(paths []string) (available, missing []string) {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			missing = append(missing, path)
			continue
		}
		available = append(available, path)
	}
	return
}

// ProfileAt validates one explicitly selected application profile. Only files
// declared by the application component are returned; Markdown files are
// excluded even if a profile directory contains them.
func ProfileAt(application, root string) (Profile, error) {
	definition, err := Get(application)
	if err != nil {
		return Profile{}, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return Profile{}, fmt.Errorf("application profile is not a readable directory: %s", root)
	}
	files := safeFiles(root, append(append([]string{}, definition.AuthFiles...), definition.ConfigFiles...))
	if application == "claude" && filepath.Base(filepath.Clean(root)) == ".claude" {
		sibling := filepath.Join(filepath.Dir(filepath.Clean(root)), ".claude.json")
		if info, statErr := os.Lstat(sibling); statErr == nil && info.Mode().IsRegular() {
			files = append(files, sibling)
		}
	}
	if len(files) == 0 {
		return Profile{}, fmt.Errorf("no supported %s profile files found in %s", application, root)
	}
	return Profile{Component: application, Name: filepath.Base(root), Directory: root, Files: files}, nil
}
func Get(id string) (Component, error) {
	component, ok := Registry[id]
	if !ok {
		return Component{}, fmt.Errorf("unknown component %q", id)
	}
	return component, nil
}
