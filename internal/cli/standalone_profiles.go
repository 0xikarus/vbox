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

	"github.com/0xikarus/vmbox-service/internal/components"
)

// standaloneProfileArchive uses the same component allowlist as profiles upload.
// Only portable data is copied; host hooks and external MCP executables are not
// started merely because a profile was selected.
func standaloneProfileArchive(sources []string) ([]byte, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	seen := map[string]bool{}
	for _, source := range sources {
		app, root, ok := strings.Cut(source, "=")
		if !ok || (app != "claude" && app != "opencode" && app != "codex") {
			return nil, fmt.Errorf("profile must be claude=PATH, opencode=PATH or codex=PATH")
		}
		if seen[app] {
			return nil, fmt.Errorf("select only one %s profile", app)
		}
		seen[app] = true
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
func (a *App) copyStandaloneProfile(ctx context.Context, i standaloneInstance, data []byte) error {
	return a.standaloneProvider(i).Import(ctx, i.ID, data)
}
