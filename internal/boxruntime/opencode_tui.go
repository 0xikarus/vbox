package boxruntime

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed opencode_tui_plugin.mjs
var openCodeTUIPlugin []byte

var openCodeVisibleClient = func(ctx context.Context, home, session string) (*http.Client, error) {
	pane, err := tmuxCommand(ctx, "", "display-message", "-p", "-t", "="+session+":", "#{pane_id}")
	if err != nil {
		return nil, fmt.Errorf("OpenCode visible terminal unavailable")
	}
	id := strings.TrimSpace(string(pane))
	if !strings.HasPrefix(id, "%") || len(id) < 2 || strings.Trim(id[1:], "0123456789") != "" {
		return nil, fmt.Errorf("invalid OpenCode pane identity")
	}
	socket := filepath.Join(home, ".local", "share", "vmbox", "opencode-tui", id[1:]+".sock")
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second}, nil
}

func registerOpenCodeTUI(home, configRoot string) error {
	pluginPath := filepath.Join(home, ".local", "share", "vmbox", "opencode-visible-chat.mjs")
	configPath := filepath.Join(configRoot, "opencode", "tui.json")
	config := map[string]json.RawMessage{}
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && (json.Unmarshal(data, &config) != nil || config == nil) {
		return fmt.Errorf("invalid OpenCode TUI config; preserved unchanged")
	}
	var plugins []json.RawMessage
	if raw, exists := config["plugin"]; exists {
		if json.Unmarshal(raw, &plugins) != nil {
			return fmt.Errorf("invalid OpenCode TUI plugins; preserved unchanged")
		}
	}
	if err := writeOpenCodeTUIConfig(pluginPath, openCodeTUIPlugin); err != nil {
		return err
	}
	for _, raw := range plugins {
		var name string
		if json.Unmarshal(raw, &name) == nil && name == pluginPath {
			return nil
		}
	}
	encoded, _ := json.Marshal(pluginPath)
	plugins = append(plugins, encoded)
	config["plugin"], _ = json.Marshal(plugins)
	data, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writeOpenCodeTUIConfig(configPath, append(data, '\n'))
}

func writeOpenCodeTUIConfig(path string, data []byte) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".vmbox-tui-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, err = temporary.Write(data)
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temporary.Name(), path)
}
