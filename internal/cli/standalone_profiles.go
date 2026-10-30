package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/components"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
)

// standaloneProfileArchive uses the same component allowlist as profiles upload.
// Only portable data is copied; host hooks and external MCP executables are not
// started merely because a profile was selected.
func standaloneProfileArchive(sources []string, github ...func(string) ([]byte, error)) ([]byte, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	seen := map[string]bool{}
	for _, source := range sources {
		app, root, ok := strings.Cut(source, "=")
		if !ok || (app != "claude" && app != "opencode" && app != "codex" && app != "github") {
			return nil, fmt.Errorf("profile must be claude=PATH, opencode=PATH, codex=PATH or github=HOST:USER (or PATH)")
		}
		if seen[app] {
			return nil, fmt.Errorf("select only one %s profile", app)
		}
		seen[app] = true
		if app == "github" {
			load := readStandaloneGitHubHosts
			if len(github) > 0 {
				load = github[0]
			}
			data, err := load(root)
			if err != nil {
				return nil, err
			}
			for _, dir := range []string{".config/", ".config/gh/"} {
				if err = tw.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0700, Uid: 10001, Gid: 10001}); err != nil {
					clear(data)
					return nil, err
				}
			}
			err = tw.WriteHeader(&tar.Header{Name: ".config/gh/hosts.yml", Mode: 0600, Size: int64(len(data)), Uid: 10001, Gid: 10001})
			if err == nil {
				_, err = tw.Write(data)
			}
			clear(data)
			if err != nil {
				return nil, err
			}
			continue
		}
		profile, err := components.ProfileAt(app, root)
		if err != nil {
			return nil, err
		}
		dirs := []string{"." + app}
		if app == "opencode" {
			dirs = []string{".config", ".config/opencode", ".local", ".local/share", ".local/share/opencode"}
		}
		for _, dir := range dirs {
			if err = tw.WriteHeader(&tar.Header{Name: dir + "/", Typeflag: tar.TypeDir, Mode: 0700, Uid: 10001, Gid: 10001}); err != nil {
				return nil, err
			}
		}
		for _, file := range profile.Files {
			stat, e := os.Lstat(file)
			if e != nil || !stat.Mode().IsRegular() || stat.Size() > 512*1024 {
				return nil, fmt.Errorf("profile must contain regular files no larger than 512 KiB")
			}
			data, e := os.ReadFile(file)
			if e != nil {
				return nil, fmt.Errorf("cannot read profile")
			}
			name := filepath.Base(file)
			dest := "." + app + "/" + name
			if app == "opencode" {
				dest = ".config/opencode/" + name
				if name == "auth.json" {
					dest = ".local/share/opencode/auth.json"
				}
			}
			if app == "claude" && name == ".claude.json" {
				dest = name
			}
			if name == "settings.json" || name == ".claude.json" {
				var settings map[string]json.RawMessage
				if e = json.Unmarshal(data, &settings); e != nil {
					clear(data)
					return nil, fmt.Errorf("invalid %s profile JSON", app)
				}
				for _, key := range []string{"hooks", "statusLine", "enabledPlugins", "mcpServers", "projects"} {
					delete(settings, key)
				}
				clear(data)
				data, e = json.Marshal(settings)
				if e != nil {
					return nil, e
				}
			}
			e = tw.WriteHeader(&tar.Header{Name: dest, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data)), Uid: 10001, Gid: 10001})
			if e == nil {
				_, e = tw.Write(data)
			}
			clear(data)
			if e != nil {
				return nil, e
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readStandaloneGitHubHosts(root string) ([]byte, error) {
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		root = filepath.Join(root, "hosts.yml")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<10 {
		return nil, fmt.Errorf("choose a regular GitHub hosts.yml file, or use github=HOST:USER")
	}
	data, err := os.ReadFile(root)
	if err != nil {
		return nil, fmt.Errorf("cannot read GitHub profile")
	}
	defer clear(data)
	return loginprofile.PortableGitHubHosts(data)
}

func (a *App) standaloneProfileArchive(ctx context.Context, sources []string) ([]byte, error) {
	return standaloneProfileArchive(sources, func(source string) ([]byte, error) {
		if !strings.Contains(source, ":") || filepath.IsAbs(source) {
			return readStandaloneGitHubHosts(source)
		}
		host, user, _ := strings.Cut(source, ":")
		if err := loginprofile.Validate("github", map[string][]byte{"credential.json": []byte(fmt.Sprintf(`{"host":%q,"user":%q,"token":"validation"}`, host, user))}, time.Now()); err != nil {
			return nil, fmt.Errorf("GitHub source must be HOST:USER")
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		r, err := a.Runner.Run(ctx, []string{"gh", "auth", "token", "--hostname", host, "--user", user}, nil, nil, nil)
		defer clear(r.Stdout)
		defer clear(r.Stderr)
		if err != nil || r.ExitCode != 0 {
			return nil, fmt.Errorf("GitHub login unavailable; run gh auth login locally and retry")
		}
		data, err := json.Marshal(map[string]map[string]string{host: {"user": user, "oauth_token": strings.TrimSpace(string(r.Stdout))}})
		if err != nil {
			return nil, fmt.Errorf("cannot encode GitHub profile")
		}
		defer clear(data)
		return loginprofile.PortableGitHubHosts(data)
	})
}

func (a *App) standaloneGitHubProfiles(ctx context.Context) []components.Profile {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var profiles []components.Profile
	for _, account := range a.discoverGitHub(ctx) {
		profiles = append(profiles, components.Profile{Component: "github", Name: account.User + "@" + account.Host, Directory: account.Host + ":" + account.User, Active: account.Active})
	}
	return profiles
}
func (a *App) copyStandaloneProfile(ctx context.Context, i standaloneInstance, data []byte) error {
	return a.standaloneProvider(i).Import(ctx, i.ID, data)
}
