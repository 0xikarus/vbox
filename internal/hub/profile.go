package hub

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"io"
	"path"
	"strings"
)

// Rebuild archives with safe ownership and the same portable profile destinations
// as CLI imports. The hub never accepts arbitrary host paths or tar links.
func sanitizeArchive(data []byte) ([]byte, error) {
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("profile archive too large")
	}
	tr := tar.NewReader(bytes.NewReader(data))
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	count := 0
	seen := map[string]bool{}
	allowed := map[string]bool{".claude": true, ".codex": true, ".config": true, ".config/opencode": true, ".local": true, ".local/share": true, ".local/share/opencode": true}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid profile archive")
		}
		count++
		if count > 80 {
			return nil, fmt.Errorf("too many profile files")
		}
		n := strings.TrimSuffix(h.Name, "/")
		if n != path.Clean(n) || strings.HasPrefix(n, "/") {
			return nil, fmt.Errorf("unsafe profile path")
		}
		if h.Typeflag == tar.TypeDir {
			if !allowed[n] {
				return nil, fmt.Errorf("unsupported profile directory")
			}
			err = tw.WriteHeader(&tar.Header{Name: n + "/", Typeflag: tar.TypeDir, Mode: 0700, Uid: 10001, Gid: 10001})
		} else if h.Typeflag == tar.TypeReg {
			parent, base := path.Dir(n), path.Base(n)
			ok := false
			switch parent {
			case ".claude":
				ok = base == ".credentials.json" || base == "settings.json"
			case ".codex":
				ok = base == "auth.json" || base == "config.toml"
			case ".config/opencode":
				ok = base == "opencode.json" || base == "opencode.jsonc"
			case ".local/share/opencode":
				ok = base == "auth.json"
			case ".":
				ok = base == ".claude.json"
			}
			if !ok || h.Size < 0 || h.Size > 512<<10 {
				return nil, fmt.Errorf("unsupported profile file")
			}

			content, e := io.ReadAll(io.LimitReader(tr, (512<<10)+1))
			if e != nil || int64(len(content)) != h.Size {
				return nil, fmt.Errorf("invalid profile file")
			}
			if parent == ".config/opencode" {
				content, e = loginprofile.OpenCodeJSON(content)
				if e != nil {
					return nil, e
				}
				n = ".config/opencode/opencode.json"
			}
			if seen[n] {
				return nil, fmt.Errorf("choose only one configuration for each destination")
			}
			seen[n] = true
			if n == ".claude/settings.json" || n == ".claude.json" {
				var settings map[string]json.RawMessage
				if json.Unmarshal(content, &settings) != nil {
					return nil, fmt.Errorf("invalid Claude profile JSON")
				}
				for _, k := range []string{"hooks", "statusLine", "enabledPlugins", "mcpServers", "projects"} {
					delete(settings, k)
				}
				content, e = json.Marshal(settings)
				if e != nil {
					return nil, e
				}
			}
			err = tw.WriteHeader(&tar.Header{Name: n, Mode: 0600, Size: int64(len(content)), Uid: 10001, Gid: 10001})
			if err == nil {
				_, err = tw.Write(content)
			}

		} else {
			return nil, fmt.Errorf("profile links are not allowed")
		}
		if err != nil {
			return nil, err
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("empty profile")
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ProfileFilesArchive accepts only portable configuration filenames from browser
// file uploads. It does not accept browser paths or arbitrary tar destinations.
func ProfileFilesArchive(agent string, files map[string][]byte) ([]byte, error) {
	if len(files) == 0 || len(files) > 8 {
		return nil, fmt.Errorf("choose profile configuration/login files")
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	total := 0
	dirs := map[string]bool{}
	for name, data := range files {
		if path.Base(name) != name || len(data) > 512<<10 {
			return nil, fmt.Errorf("profile files must use supported filenames and be at most 512 KiB each")
		}
		dest := ""
		switch agent {
		case "opencode":
			switch name {
			case "auth.json":
				dest = ".local/share/opencode/auth.json"
			case "opencode.json", "opencode.jsonc":
				dest = ".config/opencode/" + name
			}
		case "claude":
			switch name {
			case ".credentials.json", "settings.json":
				dest = ".claude/" + name
			case ".claude.json":
				dest = name
			}
		case "codex":
			if name == "auth.json" || name == "config.toml" {
				dest = ".codex/" + name
			}
		}
		if dest == "" {
			return nil, fmt.Errorf("unsupported file %s for %s", name, agent)
		}
		var parents []string
		for parent := path.Dir(dest); parent != "."; parent = path.Dir(parent) {
			parents = append(parents, parent)
		}
		for i := len(parents) - 1; i >= 0; i-- {
			if !dirs[parents[i]] {
				dirs[parents[i]] = true
				if err := tw.WriteHeader(&tar.Header{Name: parents[i] + "/", Typeflag: tar.TypeDir, Mode: 0700, Uid: 10001, Gid: 10001}); err != nil {
					return nil, err
				}
			}
		}
		total += len(data)
		if total > 4<<20 {
			return nil, fmt.Errorf("profile archive too large")
		}
		if err := tw.WriteHeader(&tar.Header{Name: dest, Mode: 0600, Size: int64(len(data)), Uid: 10001, Gid: 10001}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
