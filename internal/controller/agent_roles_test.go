package controller

import (
	"context"
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
