package controller

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestValidateAgentRoleRequestNormalizesContactScope(t *testing.T) {
	request, err := validateAgentRoleRequest(v1.PutAgentRoleRequest{Name: "  Researcher  ", Description: "  Finds sources  ", ContactScope: "SELECTED", ContactBoxIDs: []string{"box-a", "box-a", ""}})
	if err != nil {
		t.Fatal(err)
	}
	if request.Name != "Researcher" || request.Description != "Finds sources" || request.ContactScope != "selected" || len(request.ContactBoxIDs) != 1 {
		t.Fatalf("normalized request=%+v", request)
	}
	request, err = validateAgentRoleRequest(v1.PutAgentRoleRequest{Name: "All", ContactScope: "all", ContactBoxIDs: []string{"ignored"}})
	if err != nil || len(request.ContactBoxIDs) != 0 {
		t.Fatalf("all-scope request=%+v err=%v", request, err)
	}
}

func TestPutRoleAssignmentsRejectsCrossAccountRoleAtomically(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "account-a", UserID: "owner-a", Role: "owner"}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM logical_boxes").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM agent_roles").WithArgs("account-a", "role-other-account").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()
	err := store.PutRoleAssignments(context.Background(), p, v1.PutRoleAssignmentsRequest{Assignments: []v1.BoxRoleAssignment{{BoxID: "box-a", RoleIDs: []string{"role-other-account"}}}})
	if err == nil || !strings.Contains(err.Error(), "not found in this account") {
		t.Fatalf("cross-account assignment error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPutRoleAssignmentsRequiresOwner(t *testing.T) {
	store, _ := testStore(t)
	err := store.PutRoleAssignments(context.Background(), Principal{AccountID: "account-a", UserID: "user-a", Role: "user"}, v1.PutRoleAssignmentsRequest{})
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("non-owner error=%v", err)
	}
}

func TestValidateAgentRoleCapabilitiesUsesExplicitTypesAndBounds(t *testing.T) {
	request, err := validateAgentRoleRequest(v1.PutAgentRoleRequest{Name: "Release coordinator", Capabilities: v1.AgentRoleCapabilities{CreateAgentBox: v1.CreateAgentBoxGrant{Enabled: true, MaxBoxes: 2, MaxDiskGiB: 50, AllowedAgents: []string{"codex", "codex", "opencode"}}, RequestMoreTime: v1.RequestMoreTimeGrant{Enabled: true, MaxExtensionMinutes: 30, MaxTotalMinutes: 120}, MCPTools: v1.MCPToolsGrant{Enabled: true, AllowedTools: []string{"desktop_click", "click_mouse"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Capabilities.CreateAgentBox.AllowedAgents) != 2 {
		t.Fatalf("agents=%v", request.Capabilities.CreateAgentBox.AllowedAgents)
	}
	if len(request.Capabilities.MCPTools.AllowedTools) != 1 {
		t.Fatalf("MCP tools=%v", request.Capabilities.MCPTools.AllowedTools)
	}
	request.Capabilities.CreateAgentBox.AllowedAgents = []string{"made-up-agent"}
	if _, err = validateAgentRoleRequest(request); err == nil {
		t.Fatal("arbitrary agent type accepted")
	}
	request.Capabilities.CreateAgentBox.AllowedAgents = []string{"codex"}
	request.Capabilities.MCPTools.AllowedTools = []string{"shell"}
	if _, err = validateAgentRoleRequest(request); err == nil {
		t.Fatal("unknown MCP tool accepted")
	}
}

func TestTeamRolePresetUsesEditableExplicitCapabilities(t *testing.T) {
	normal, manager, err := teamRolePresetRequests("normal-role-id")
	if err != nil {
		t.Fatal(err)
	}
	computerTools := []string{"take_screenshot", "capture_window", "move_mouse", "click_mouse", "drag_mouse", "scroll_mouse", "type_text", "press_keys"}
	if normal.Name != "Normal" || normal.ContactScope != v1.ContactScopeAll || !normal.Capabilities.MCPTools.Enabled || !slices.Equal(normal.Capabilities.MCPTools.AllowedTools, computerTools) {
		t.Fatalf("normal preset=%+v", normal)
	}
	if manager.Name != "Manager" || manager.ContactScope != v1.ContactScopeAll {
		t.Fatalf("manager preset=%+v", manager)
	}
	create := manager.Capabilities.CreateAgentBox
	if !create.Enabled || create.MaxBoxes != 3 || create.MaxDiskGiB != 50 || !slices.Equal(create.AllowedAgents, []string{"codex", "claude", "opencode"}) || !slices.Equal(create.AssignableRoleIDs, []string{"normal-role-id"}) {
		t.Fatalf("manager create grant=%+v", create)
	}
	manage := manager.Capabilities.ManageAgentBoxes
	if !manage.List || !manage.Inspect || !manage.Restart || !manage.Delete {
		t.Fatalf("manager lifecycle grant=%+v", manage)
	}
	for _, tool := range []string{"list_agent_boxes", "get_agent_box", "create_agent_box", "restart_agent_box", "delete_agent_box"} {
		if !slices.Contains(manager.Capabilities.MCPTools.AllowedTools, tool) {
			t.Fatalf("manager preset lacks %s: %+v", tool, manager)
		}
	}
}

func TestEffectiveAgentCapabilitiesUnionsAssignedRoles(t *testing.T) {
	store, mock := testStore(t)
	moreA, _ := json.Marshal(v1.RequestMoreTimeGrant{Enabled: true, MaxExtensionMinutes: 30, MaxTotalMinutes: 60})
	moreB, _ := json.Marshal(v1.RequestMoreTimeGrant{Enabled: true, MaxExtensionMinutes: 90, MaxTotalMinutes: 240})
	boxes, _ := json.Marshal(v1.CreateAgentBoxGrant{Enabled: true, MaxBoxes: 2, MaxDiskGiB: 50, AllowedAgents: []string{"codex"}, AssignableRoleIDs: []string{"role-a"}})
	mcp, _ := json.Marshal(v1.MCPToolsGrant{Enabled: true, AllowedTools: []string{"desktop_click"}})
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionRequestMoreTime, moreA).AddRow(v1.RolePermissionRequestMoreTime, moreB).AddRow(v1.RolePermissionCreateAgentBox, boxes).AddRow(v1.RolePermissionMCPTools, mcp))
	capabilities, err := store.EffectiveAgentCapabilities(context.Background(), "account-a", "box-a")
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.RequestMoreTime.MaxExtensionMinutes != 90 || capabilities.RequestMoreTime.MaxTotalMinutes != 240 || !capabilities.CreateAgentBox.Enabled || !slices.Equal(capabilities.MCPTools.AllowedTools, []string{"click_mouse"}) {
		t.Fatalf("capabilities=%+v", capabilities)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveAgentToolNamesLayersAllowlistOverTypedCapabilities(t *testing.T) {
	store, mock := testStore(t)
	boxes, _ := json.Marshal(v1.CreateAgentBoxGrant{Enabled: true, MaxBoxes: 1, MaxDiskGiB: 20, AllowedAgents: []string{"codex"}})
	manage, _ := json.Marshal(v1.ManageAgentBoxesGrant{List: true, Inspect: true})
	mcp, _ := json.Marshal(v1.MCPToolsGrant{Enabled: true, AllowedTools: []string{"click_mouse", "drag_mouse", "list_agent_boxes", "get_agent_box", "restart_agent_box", "delete_agent_box", "create_agent_box", "request_more_time"}})
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionCreateAgentBox, boxes).AddRow(v1.RolePermissionManageAgentBoxes, manage).AddRow(v1.RolePermissionMCPTools, mcp))
	tools, err := store.EffectiveAgentToolNames(context.Background(), "account-a", "box-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"chat_message", "get_thread_history", "click_mouse", "drag_mouse", "list_agent_boxes", "get_agent_box", "create_agent_box"} {
		if !slices.Contains(tools, name) {
			t.Fatalf("expected %s in %v", name, tools)
		}
	}
	if slices.Contains(tools, "request_more_time") {
		t.Fatalf("allowlist bypassed typed request-more-time permission: %v", tools)
	}
	if slices.Contains(tools, "delete_agent_box") {
		t.Fatalf("allowlist bypassed typed delete permission: %v", tools)
	}
	if slices.Contains(tools, "restart_agent_box") {
		t.Fatalf("allowlist bypassed typed restart permission: %v", tools)
	}
	if slices.Contains(tools, "type_text") {
		t.Fatalf("unselected optional tool was advertised: %v", tools)
	}
}

func TestEffectiveAgentToolNamesDefaultsOptionalToolsToDenied(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}))
	tools, err := store.EffectiveAgentToolNames(context.Background(), "account-a", "box-a")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tools, v1.BasicAgentMCPTools) {
		t.Fatalf("tools without an optional grant=%v", tools)
	}
}

func TestSharedChatMentionsRequireAnExactBoxNameOrID(t *testing.T) {
	for _, test := range []struct {
		text, target string
		want         bool
	}{
		{"please check this @researcher", "researcher", true},
		{"please check this @researcher.", "researcher", true},
		{"please check this @researcher-two", "researcher", false},
		{"mail researcher@example.com", "researcher", false},
		{"hello @box with spaces!", "box with spaces", true},
	} {
		if got := hasExactMention(test.text, test.target); got != test.want {
			t.Fatalf("hasExactMention(%q, %q)=%t want %t", test.text, test.target, got, test.want)
		}
	}
}

func TestAgentBoxIdempotencyComparesEveryCreationParameter(t *testing.T) {
	stored := []byte(`["role-a","role-b"]`)
	if !sameAgentBoxRequest("alpha", "codex", 20, []string{"role-a", "role-b"}, "alpha", "codex", 20, stored) {
		t.Fatal("identical create-agent-box request was not reusable")
	}
	if sameAgentBoxRequest("alpha", "opencode", 20, []string{"role-a", "role-b"}, "alpha", "codex", 20, stored) {
		t.Fatal("agent type was ignored during idempotency comparison")
	}
	if sameAgentBoxRequest("alpha", "codex", 20, []string{"role-a"}, "alpha", "codex", 20, stored) {
		t.Fatal("starting roles were ignored during idempotency comparison")
	}
}
