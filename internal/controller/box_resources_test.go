package controller

import (
	"context"
	"encoding/json"
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

type usageTestProvider struct {
	limitsTestProvider
	usage provider.ResourceUsage
}

func (p *usageTestProvider) ResourceUsage(context.Context, string) (provider.ResourceUsage, error) {
	return p.usage, nil
}

func TestBoxResourcesIncludesOptionalUsage(t *testing.T) {
	for _, withUsage := range []bool{false, true} {
		t.Run(fmt.Sprint(withUsage), func(t *testing.T) {
			store, mock := testStore(t)
			var prov provider.Provider = &limitsTestProvider{resources: provider.Resources{CPU: 1, MemoryMiB: 2048, DiskGiB: 2}}
			observed := time.Now().UTC()
			diskUsed, diskTotal := int64(1500000000), int64(2<<30)
			workspaceBytes, writableBytes := int64(1200000000), int64(300000000)
			hostTotal, hostUsed, hostFree := int64(100<<30), int64(92<<30), int64(8<<30)
			if withUsage {
				prov = &usageTestProvider{limitsTestProvider: limitsTestProvider{resources: provider.Resources{CPU: 1, MemoryMiB: 2048, DiskGiB: 2}}, usage: provider.ResourceUsage{MemoryUsedBytes: 1200000000, SwapUsedBytes: 100000000, DiskUsedBytes: &diskUsed, DiskWorkspaceBytes: &workspaceBytes, DiskWritableBytes: &writableBytes, DiskTotalBytes: &diskTotal, DiskObservedAt: &observed, HostDiskTotalBytes: &hostTotal, HostDiskUsedBytes: &hostUsed, HostDiskFreeBytes: &hostFree, ObservedAt: observed}}
			}
			server := NewServer(store, provider.NewRegistry())
			server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return prov, nil }
			for range 2 {
				mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(sharedResourceBoxRow())
				mock.ExpectQuery("SELECT COALESCE\\(fencing_token").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow("fence-1"))
				mock.ExpectQuery("FROM compute_slots").WithArgs("account-a", "slot-1").WillReturnRows(occupiedSlotRow(observed))
			}
			req := httptest.NewRequest("GET", "/v1/logical-boxes/box-1/resources", nil)
			req.SetPathValue("id", "box-1")
			rec := httptest.NewRecorder()
			server.boxResources(rec, req, Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"})
			if rec.Code != 200 {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var data map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			_, hasUsage := data["memoryUsedBytes"]
			if hasUsage != withUsage || data["resources"] == nil {
				t.Fatalf("usage presence=%v body=%s", hasUsage, rec.Body.String())
			}
			if withUsage && (data["diskTotalBytes"] != float64(diskTotal) || data["diskWorkspaceBytes"] != float64(workspaceBytes) || data["diskWritableBytes"] != float64(writableBytes) || data["hostDiskFreeBytes"] != float64(hostFree) || data["diskEnforced"] != false || data["diskObservedAt"] == nil) {
				t.Fatalf("usage=%s", rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func sharedResourceBoxRow() *sqlmock.Rows {
	now := time.Now().UTC()
	return sqlmock.NewRows([]string{"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "role", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools"}).
		AddRow("box-1", "account-a", "user-a", "research", "shared-worker", "primary", "claude", "worker", "running", "volume-1", "volume-name", "slot-1", int64(3), "", nil, "", "", now, now, "[]")
}

func TestBoxResourceUpdateRejectsLimitBelowLiveUsage(t *testing.T) {
	store, mock := testStore(t)
	prov := &usageTestProvider{usage: provider.ResourceUsage{MemoryUsedBytes: 1200000000}}
	server := NewServer(store, provider.NewRegistry())
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return prov, nil }
	mock.ExpectBegin()
	mock.ExpectQuery("FROM logical_boxes.*FOR UPDATE").WithArgs("account-a", "box-1").WillReturnRows(sharedResourceBoxRow())
	mock.ExpectQuery("FROM compute_slots.*FOR UPDATE OF s").WithArgs("account-a", "slot-1").WillReturnRows(occupiedSlotRow(time.Now()))
	mock.ExpectRollback()
	req := httptest.NewRequest("PUT", "/v1/logical-boxes/box-1/resources", strings.NewReader(`{"slotId":"slot-1","assignmentGeneration":3,"cpu":1,"memoryMiB":1024,"swapMiB":0}`))
	req.SetPathValue("id", "box-1")
	rec := httptest.NewRecorder()
	server.setBoxResources(rec, req, Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"})
	if rec.Code != 409 || prov.calls != 0 || !strings.Contains(rec.Body.String(), "below current usage") {
		t.Fatalf("status=%d calls=%d body=%s", rec.Code, prov.calls, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
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
				mock.ExpectExec("INSERT INTO audit_log").WithArgs("account-a", "user-a", "slot-1", float64(4), int64(12288), int64(0)).WillReturnResult(sqlmock.NewResult(0, 1))
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
