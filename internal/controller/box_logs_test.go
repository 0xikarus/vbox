package controller

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type logsTestProvider struct {
	fakeProvider
	service string
	options provider.LogOptions
}

func (p *logsTestProvider) Logs(_ context.Context, service string, options provider.LogOptions, output io.Writer) error {
	p.service, p.options = service, options
	_, _ = io.WriteString(output, "worker ready\n")
	return nil
}

func TestLogicalBoxLogsUseAssignedServiceWithoutStartingBox(t *testing.T) {
	store, mock := testStore(t)
	prov := &logsTestProvider{}
	server := NewServer(store, provider.NewRegistry())
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) { return prov, nil }
	now := time.Now().UTC()
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(logicalBoxRowWithSlot(v1.LogicalBoxAttaching, "opencode", "slot-1"))
	mock.ExpectQuery("FROM logical_boxes").WithArgs("account-a", "box-1").WillReturnRows(logicalBoxRowWithSlot(v1.LogicalBoxAttaching, "opencode", "slot-1"))
	mock.ExpectQuery("SELECT COALESCE\\(fencing_token").WithArgs("account-a", "box-1").WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow("fence-1"))
	mock.ExpectQuery("FROM compute_slots").WithArgs("account-a", "slot-1").WillReturnRows(occupiedSlotRow(now))

	request := httptest.NewRequest("GET", "/v1/logical-boxes/box-1/logs?tail=321", nil)
	request.SetPathValue("id", "box-1")
	response := httptest.NewRecorder()
	server.logicalBoxLogs(response, request, Principal{AccountID: "account-a", UserID: "user-a", Role: "user"})
	if response.Code != 200 || response.Body.String() != "worker ready\n" || prov.service != "service-1" || prov.options.Tail != 321 || prov.options.Follow {
		t.Fatalf("status=%d body=%q service=%q options=%+v", response.Code, response.Body.String(), prov.service, prov.options)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
