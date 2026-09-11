package controller

import (
	"context"
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type limitsTestProvider struct {
	fakeProvider
	calls     int
	resources provider.Resources
}

func (p *limitsTestProvider) ResourceLimits(context.Context, string) (provider.Resources, error) {
	return p.resources, nil
}
func (p *limitsTestProvider) SetResourceLimits(_ context.Context, id string, r provider.Resources) error {
	if id != "service-1" {
		panic("wrong slot")
	}
	p.calls++
	p.resources = r
	return nil
}
func TestBoxResourceUpdatePinsAssignment(t *testing.T) {
	for _, tc := range []struct {
		state      v1.LogicalBoxState
		generation int64
	}{{v1.LogicalBoxRunning, 3}, {v1.LogicalBoxHibernating, 3}, {v1.LogicalBoxRunning, 2}} {
		state := tc.state
		valid := state == v1.LogicalBoxRunning && tc.generation == 3
		t.Run(fmt.Sprintf("%s-%d", state, tc.generation), func(t *testing.T) {
			store, mock := testStore(t)
			prov := &limitsTestProvider{}
			server := NewServer(store, provider.NewRegistry())
			server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return prov, nil }
			mock.ExpectBegin()
			mock.ExpectQuery("FROM logical_boxes.*FOR UPDATE").WithArgs("account-a", "box-1").WillReturnRows(logicalBoxRowWithSlot(state, "shell", "slot-1"))
			if valid {
				mock.ExpectQuery("FROM compute_slots.*FOR UPDATE OF s").WithArgs("account-a", "slot-1").WillReturnRows(occupiedSlotRow(time.Now()))
				mock.ExpectExec("INSERT INTO audit_log").WithArgs("account-a", "user-a", "slot-1", float64(4), int64(12288)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			req := httptest.NewRequest("PUT", "/", strings.NewReader(fmt.Sprintf(`{"slotId":"slot-1","assignmentGeneration":%d,"cpu":4,"memoryMiB":12288}`, tc.generation)))
			req.SetPathValue("id", "box-1")
			rec := httptest.NewRecorder()
			server.setBoxResources(rec, req, Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"})
			if valid {
				if rec.Code != 200 || prov.calls != 1 {
					t.Fatalf("%d %s calls=%d", rec.Code, rec.Body, prov.calls)
				}
			} else if rec.Code != 409 || prov.calls != 0 {
				t.Fatalf("stale assignment mutated: %d calls=%d", rec.Code, prov.calls)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestBoxResourcesOwnerOnly(t *testing.T) {
	for _, method := range []string{"GET", "PUT"} {
		store, mock := testStore(t)
		mock.ExpectQuery(`SELECT t.account_id::text`).WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"account_id", "user_id", "role", "subject"}).AddRow("account-a", "user-a", "user", "person"))
		req := httptest.NewRequest(method, "/v1/logical-boxes/box-1/resources", nil)
		req.Header.Set("Authorization", "Bearer token")
		rec := httptest.NewRecorder()
		NewServer(store, provider.NewRegistry()).Handler().ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("%s: %d", method, rec.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}
