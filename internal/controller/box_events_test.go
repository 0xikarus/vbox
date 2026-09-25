package controller

import (
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestPolicyEventTextDescribesToolChanges(t *testing.T) {
	before := v1.AgentRoleCapabilities{MCPTools: v1.MCPToolsGrant{Enabled: true, AllowedTools: []string{"click_mouse", "press_keys"}}}
	after := v1.AgentRoleCapabilities{MCPTools: v1.MCPToolsGrant{Enabled: true, AllowedTools: []string{"press_keys", "take_screenshot"}}}
	text := policyEventText(before, after)
	if !strings.Contains(text, "MCP tools added · take_screenshot") || !strings.Contains(text, "MCP tools removed · click_mouse") {
		t.Fatalf("permission event failed to describe changes: %q", text)
	}
}
