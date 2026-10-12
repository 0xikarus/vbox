package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type localBrowserProfile struct {
	Browser, Name, Path, UserDataDir, ProfileDir string
}

func browserEnvValue(env map[string]string, name string) string {
	if value := env[name]; value != "" {
		return value
	}
	for key, value := range env {
		if value != "" && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func browserInWSL(env map[string]string) bool {
	return runtime.GOOS == "linux" && (browserEnvValue(env, "WSL_INTEROP") != "" || browserEnvValue(env, "WSL_DISTRO_NAME") != "")
}

func wslPath(flag, value string) string {
	if value == "" {
		return ""
	}
	output, err := exec.Command("wslpath", flag, value).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func browserLocalAppData(env map[string]string) string {
	local := browserEnvValue(env, "LOCALAPPDATA")
	if browserInWSL(env) {
		if local == "" {
			output, err := exec.Command("cmd.exe", "/C", "echo", "%LOCALAPPDATA%").Output()
			if err == nil {
				local = strings.TrimSpace(string(output))
			}
		}
		if len(local) >= 2 && local[1] == ':' {
			return wslPath("-u", local)
		}
	}
	return local
}

func discoverLocalBrowserProfiles(env map[string]string) []localBrowserProfile {
	home := browserEnvValue(env, "HOME")
	if home == "" {
		home = browserEnvValue(env, "USERPROFILE")
	}
	type root struct{ browser, path string }
	roots := []root{}
	if home != "" {
		for _, item := range []struct{ browser, dir string }{
			{"chrome", "google-chrome"}, {"chrome", "google-chrome-beta"},
			{"chrome", "google-chrome-unstable"}, {"chromium", "chromium"},
		} {
			roots = append(roots, root{item.browser, filepath.Join(home, ".config", item.dir)})
		}
		roots = append(roots, root{"chromium", filepath.Join(home, "snap", "chromium", "common", "chromium")})
	}
	if local := browserLocalAppData(env); local != "" {
		roots = append(roots,
			root{"chrome", filepath.Join(local, "Google", "Chrome", "User Data")},
			root{"chromium", filepath.Join(local, "Chromium", "User Data")},
		)
	}
	profiles := []localBrowserProfile{}
	seen := map[string]bool{}
	for _, root := range roots {
		root.path = filepath.Clean(root.path)
		if seen[root.path] {
			continue
		}
		seen[root.path] = true
		entries, err := os.ReadDir(root.path)
		if err != nil {
			continue
		}
		friendly := browserProfileNames(root.path)
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
				(entry.Name() != "Default" && !strings.HasPrefix(entry.Name(), "Profile ")) {
				continue
			}
			path := filepath.Join(root.path, entry.Name())
			info, err := os.Lstat(filepath.Join(path, "Preferences"))
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			name := friendly[entry.Name()]
			if name == "" {
				name = entry.Name()
			}
			profiles = append(profiles, localBrowserProfile{Browser: root.browser, Name: name, Path: path, UserDataDir: root.path, ProfileDir: entry.Name()})
		}
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Path < profiles[j].Path })
	return profiles
}

func browserProfileNames(root string) map[string]string {
	names := map[string]string{}
	path := filepath.Join(root, "Local State")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return names
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return names
	}
	var state struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if json.Unmarshal(data, &state) == nil {
		for key, value := range state.Profile.InfoCache {
			if value.Name != "" && len(value.Name) <= 80 && !strings.ContainsAny(value.Name, "\r\n\x1b") {
				names[key] = value.Name
			}
		}
	}
	return names
}

func savedBrowserProfileName(p localBrowserProfile) string {
	source := p.Browser
	switch filepath.Base(p.UserDataDir) {
	case "google-chrome-beta":
		source = "chrome-beta"
	case "google-chrome-unstable":
		source = "chrome-dev"
	}
	if strings.Contains(filepath.ToSlash(p.UserDataDir), "/snap/chromium/") {
		source = "chromium-snap"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(source + "-" + p.ProfileDir) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
