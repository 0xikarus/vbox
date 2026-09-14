package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type freshBootstrapProvider struct {
	fakeProvider
	connection  provider.Connection
	connections []provider.Connection
	failInstall bool
}

func (p *freshBootstrapProvider) ExecConnection(_ context.Context, connection provider.Connection, argv []string, options provider.ExecOptions) (provider.ExecResult, error) {
	p.connections = append(p.connections, connection)
	if connection.Metadata["deploymentInstanceId"] != "fresh-deployment" {
		return provider.ExecResult{}, errors.New("wrong deployment")
	}
	if len(argv) == 3 && argv[0] == "vmbox-runtime" && argv[1] == "native-sessions" {
		data, _ := json.Marshal(v1.SessionInventory{Assignment: argv[2], State: "live", Sessions: []v1.Session{}})
		return provider.ExecResult{Stdout: string(data)}, nil
	}
	if len(argv) == 4 && argv[0] == "sudo" && argv[1] == "-n" && argv[2] == "sh" && argv[3] == "-s" && options.Stdin != nil {
		_, _ = io.Copy(io.Discard, options.Stdin)
		if p.failInstall {
			return provider.ExecResult{}, errors.New("ambiguous bootstrap disconnect")
		}
		return provider.ExecResult{Stdout: "worker-agent-installed\n"}, nil
	}
	return provider.ExecResult{}, errors.New("unexpected bootstrap operation")
}

func TestAttachingWorkerInstallationUsesOneFreshConnectionAndPreservesAmbiguity(t *testing.T) {
	for _, failInstall := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "ambiguous"}[failInstall], func(t *testing.T) {
			store, mock := testStore(t)
			server := NewServer(store, nil)
			server.DirectWorkersEnabled = true
			server.WorkerAgent = []byte("agent-binary-fixture")
			server.PublicURL = "https://controller.example"
			a := fleetAssignment{
				Box:          v1.LogicalBox{ID: "box", AccountID: "account", Provider: "railway", State: "attaching", AssignmentGeneration: 4},
				Slot:         v1.ComputeSlot{ID: "slot", ServiceID: "service", DeploymentInstanceID: "stale-deployment"},
				FencingToken: "fence",
			}
			connection := provider.Connection{Transport: "openssh", Endpoint: "fresh-deployment@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "fresh-deployment"}}
			prov := &freshBootstrapProvider{connection: connection, failInstall: failInstall}

			mock.ExpectQuery(`INSERT INTO direct_workers`).
				WithArgs(sqlmock.AnyArg(), "slot", "account", sqlmock.AnyArg()).
				WillReturnRows(sqlmock.NewRows([]string{"service_id"}).AddRow("service"))
			mock.ExpectExec(`UPDATE direct_workers SET migration_sessions`).
				WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "account", "fresh-deployment").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT b.assignment_generation`).
				WithArgs("box", "account", "slot", "fence", v1.LogicalBoxState("attaching")).
				WillReturnRows(sqlmock.NewRows([]string{"assignment_generation"}).AddRow(int64(4)))
			mock.ExpectRollback()

			_, err := server.installWorkerAgentForAssignment(context.Background(), "account", a, prov, &connection)
			if failInstall {
				var recoverable workerInstallationError
				if !errors.As(err, &recoverable) || !recoverable.recoverable {
					t.Fatalf("ambiguous installation was not preserved for recovery: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(prov.connections) != 1 || prov.connections[0].Endpoint != connection.Endpoint {
				t.Fatalf("bootstrap did not remain on one resolved deployment: %+v", prov.connections)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
