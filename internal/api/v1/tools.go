package v1

import (
	"fmt"
	"strings"
)

func ValidateSetupScript(script string) error {
	if len(script) > 32768 || strings.ContainsRune(script, 0) {
		return fmt.Errorf("custom install commands must be at most 32 KiB and contain no NUL bytes")
	}
	return nil
}

// Tool presets are provider-independent, explicitly selected, and versioned.
type ToolPreset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

func ToolPresets() []ToolPreset {
	return []ToolPreset{
		{ID: "foundry", Name: "Foundry", Version: "v1.8.1", Description: "forge, cast, anvil and chisel · about 500 MiB installed"},
		{ID: "blender", Name: "Blender", Version: "5.1.2 + Blender MCP 1.9.1", Description: "Pinned 5.1 release + desktop + MCP for Codex/Claude/OpenCode · extra download/disk/RAM"},
		{ID: "desktop", Name: "Enable desktop", Version: "worker packages", Description: "Desktop and Chromium browser without Blender; required for Blender"},
		{ID: "tor-socks5", Name: "Tor SOCKS5", Version: "distribution package", Description: "Start a local SOCKS5 proxy at 127.0.0.1:9050; apps opt in with socks5h://127.0.0.1:9050."},
		{ID: "cloudflare-warp", Name: "Cloudflare WARP", Version: "stable package", Description: "Start and connect a local SOCKS5 proxy at 127.0.0.1:40000 when Cloudflare UDP is reachable; connection status is saved in ~/.config/vmbox/warp-status."},
	}
}
func ValidateTools(tools []string) error {
	seen := map[string]bool{}
	for _, tool := range tools {
		if (tool != "foundry" && tool != "blender" && tool != "desktop" && tool != "tor-socks5" && tool != "cloudflare-warp") || seen[tool] {
			return fmt.Errorf("select each supported tool preset at most once (foundry, blender, desktop, tor-socks5, cloudflare-warp)")
		}
		seen[tool] = true
	}
	if seen["tor-socks5"] && seen["cloudflare-warp"] {
		return fmt.Errorf("choose Tor SOCKS5 or Cloudflare WARP, not both")
	}
	return nil
}
