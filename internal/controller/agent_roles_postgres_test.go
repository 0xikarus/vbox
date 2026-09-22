package controller

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// TestNativeAgentRolesPostgres exercises the retry-safe migration and the real
// authorization queries against an isolated schema in disposable PostgreSQL.
func TestNativeAgentRolesPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.DB.SetMaxOpenConns(1)
	ns := "native_roles_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = s.DB.ExecContext(ctx, "CREATE SCHEMA "+ns); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(context.Background(), "DROP SCHEMA "+ns+" CASCADE")
	if _, err = s.DB.ExecContext(ctx, "SET search_path TO "+ns); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.Bootstrap(ctx, "roles-a", "owner-a", uuid())
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.Bootstrap(ctx, "roles-b", "owner-b", uuid())
	if err != nil {
		t.Fatal(err)
	}
	boxIDs := map[string]string{"sender": uuid(), "target": uuid(), "other": uuid()}
	for name, id := range boxIDs {
		if _, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,$4,'railway','running',$5,$5)`, id, p.AccountID, p.UserID, name, "volume-"+name); err != nil {
			t.Fatal(err)
		}
	}
	foreignBox := uuid()
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,'foreign','railway','running','foreign-volume','foreign-volume')`, foreignBox, q.AccountID, q.UserID); err != nil {
		t.Fatal(err)
	}
	foreignRole, err := s.CreateAgentRole(ctx, q, v1.PutAgentRoleRequest{Name: "Foreign role", Capabilities: v1.AgentRoleCapabilities{AllContacts: v1.AllContactsGrant{Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	role, err := s.CreateAgentRole(ctx, p, v1.PutAgentRoleRequest{Name: "Normal", Description: "Uses direct contacts"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutRoleAssignments(ctx, p, v1.PutRoleAssignmentsRequest{Assignments: []v1.BoxRoleAssignment{{BoxID: boxIDs["sender"], RoleIDs: []string{role.ID}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutBoxContact(ctx, p, boxIDs["sender"], v1.PutBoxContactRequest{Contact: boxIDs["target"], State: "allow"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='hibernated' WHERE id=$1`, boxIDs["target"]); err != nil {
		t.Fatal(err)
	}
	boxes, err := s.ListLogicalBoxes(ctx, p, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var senderRoles []v1.AgentRoleSummary
	for _, box := range boxes {
		if box.ID == boxIDs["sender"] {
			senderRoles = box.Roles
		}
	}
	if len(senderRoles) != 1 || senderRoles[0].ID != role.ID {
		t.Fatalf("assigned role summaries=%+v", senderRoles)
	}
	entries, err := s.ContactEntries(ctx, p.AccountID, boxIDs["sender"])
	if err != nil || len(entries) != 1 || entries[0].ID != strings.ReplaceAll(boxIDs["target"], "-", "")[:8] || entries[0].Name != "target" {
		t.Fatalf("selected contacts=%+v err=%v", entries, err)
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["sender"], boxIDs["target"]); err == nil || !strings.Contains(err.Error(), "hibernated") {
		t.Fatalf("sleeping target unexpectedly accepted delivery: %v", err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='running' WHERE id=$1`, boxIDs["target"]); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["sender"], boxIDs["target"]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutBoxContact(ctx, p, boxIDs["sender"], v1.PutBoxContactRequest{Contact: boxIDs["target"], State: "block"}); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["sender"], boxIDs["target"]); err == nil || !strings.Contains(err.Error(), "explicit") {
		t.Fatalf("block did not beat role: %v", err)
	}
	if _, err = s.PutBoxContact(ctx, p, boxIDs["sender"], v1.PutBoxContactRequest{Contact: boxIDs["target"], State: "inherit"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetBoxProtection(ctx, p, boxIDs["target"], true); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["sender"], boxIDs["target"]); err == nil || !strings.Contains(strings.ToLower(err.Error()), "protected") {
		t.Fatalf("protection did not beat role: %v", err)
	}
	if err = s.SetBoxProtection(ctx, p, boxIDs["target"], false); err != nil {
		t.Fatal(err)
	}
	role, err = s.UpdateAgentRole(ctx, p, role.ID, v1.PutAgentRoleRequest{Name: "Manager", Capabilities: v1.AgentRoleCapabilities{AllContacts: v1.AllContactsGrant{Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	backupRole, err := s.CreateAgentRole(ctx, p, v1.PutAgentRoleRequest{Name: "Backup manager", Capabilities: v1.AgentRoleCapabilities{AllContacts: v1.AllContactsGrant{Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutRoleAssignments(ctx, p, v1.PutRoleAssignmentsRequest{Assignments: []v1.BoxRoleAssignment{{BoxID: boxIDs["sender"], RoleIDs: []string{role.ID, backupRole.ID}}}}); err != nil {
		t.Fatal(err)
	}
	future := uuid()
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,'future','railway','running','future-volume','future-volume')`, future, p.AccountID, p.UserID); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["sender"], future); err != nil {
		t.Fatalf("all-boxes role did not include future box: %v", err)
	}
	if _, err = s.PutBoxContact(ctx, p, boxIDs["sender"], v1.PutBoxContactRequest{Contact: boxIDs["other"], State: "allow", TwoWay: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["sender"], boxIDs["other"]); err != nil {
		t.Fatalf("forward half of two-way allowance failed: %v", err)
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["other"], boxIDs["sender"]); err != nil {
		t.Fatalf("reverse half of two-way allowance failed: %v", err)
	}
	if _, err = s.PutBoxContact(ctx, p, boxIDs["sender"], v1.PutBoxContactRequest{Contact: boxIDs["other"], State: "block", TwoWay: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["sender"], boxIDs["other"]); err == nil {
		t.Fatal("forward half of two-way block was ignored")
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["other"], boxIDs["sender"]); err == nil {
		t.Fatal("reverse half of two-way block was ignored")
	}
	if _, err = s.PutBoxContact(ctx, p, boxIDs["sender"], v1.PutBoxContactRequest{Contact: foreignBox, State: "allow"}); err == nil {
		t.Fatal("cross-account contact override was accepted")
	}
	if err = s.PutRoleAssignments(ctx, p, v1.PutRoleAssignmentsRequest{Assignments: []v1.BoxRoleAssignment{{BoxID: boxIDs["sender"], RoleIDs: []string{foreignRole.ID}}}}); err == nil {
		t.Fatal("cross-account or unknown role assignment was accepted")
	}
	if err = s.DeleteAgentRole(ctx, p, role.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["sender"], future); err != nil {
		t.Fatalf("removing one role removed a grant supplied by another: %v", err)
	}
	if _, err = s.PutBoxContact(ctx, p, boxIDs["sender"], v1.PutBoxContactRequest{Contact: boxIDs["target"], State: "allow"}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteAgentRole(ctx, p, backupRole.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeBoxMessage(ctx, p.AccountID, boxIDs["sender"], boxIDs["target"]); err != nil {
		t.Fatalf("manual allowance was lost with role deletion: %v", err)
	}
	// Reapplying schema must preserve explicit rows, create no roles, and never
	// recreate the removed legacy column.
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var roleCount, contactCount, legacyColumns int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_roles WHERE account_id=$1`, p.AccountID).Scan(&roleCount); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM box_contacts WHERE account_id=$1 AND box_id=$2 AND contact_box_id=$3 AND can_message`, p.AccountID, boxIDs["sender"], boxIDs["target"]).Scan(&contactCount); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='logical_boxes' AND column_name='role'`, ns).Scan(&legacyColumns); err != nil {
		t.Fatal(err)
	}
	if roleCount != 0 || contactCount != 1 || legacyColumns != 0 {
		t.Fatalf("retry migration roles=%d contacts=%d legacy role columns=%d", roleCount, contactCount, legacyColumns)
	}
	// Simulate upgrading a database from the scalar worker/manager release.
	// The manager box must retain fleet-wide contact access through an explicit
	// native role, and reapplying the migration must remain idempotent.
	if _, err = s.DB.ExecContext(ctx, `ALTER TABLE logical_boxes ADD COLUMN role text NOT NULL DEFAULT 'worker' CHECK (role IN ('worker','manager'))`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE logical_boxes SET role='manager' WHERE id=$1`, boxIDs["sender"]); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var migratedRoles, migratedAssignments, migratedGrants int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_roles WHERE account_id=$1 AND name='Manager'`, p.AccountID).Scan(&migratedRoles); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM box_role_assignments a JOIN agent_roles r ON r.id=a.role_id WHERE a.account_id=$1 AND a.box_id=$2 AND r.name='Manager'`, p.AccountID, boxIDs["sender"]).Scan(&migratedAssignments); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_role_permissions p JOIN agent_roles r ON r.id=p.role_id WHERE p.account_id=$1 AND r.name='Manager' AND p.permission='all_contacts' AND p.config->>'enabled'='true'`, p.AccountID).Scan(&migratedGrants); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='logical_boxes' AND column_name='role'`, ns).Scan(&legacyColumns); err != nil {
		t.Fatal(err)
	}
	if migratedRoles != 1 || migratedAssignments != 1 || migratedGrants != 1 || legacyColumns != 0 {
		t.Fatalf("legacy manager migration roles=%d assignments=%d grants=%d legacy columns=%d", migratedRoles, migratedAssignments, migratedGrants, legacyColumns)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_roles WHERE account_id=$1 AND name='Manager'`, p.AccountID).Scan(&migratedRoles); err != nil {
		t.Fatal(err)
	}
	if migratedRoles != 1 {
		t.Fatalf("retry created %d Manager roles", migratedRoles)
	}
}
