package railway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestHTTPStopSelectsNewestSuccessfulDeployment(t *testing.T) {
	reads, writes := 0, 0
	api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                     `json:"query"`
			Variables map[string]json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch body.Query {
		case deploymentsQuery:
			reads++
			var input map[string]string
			if json.Unmarshal(body.Variables["input"], &input) != nil || input["serviceId"] != "service-id" || input["projectId"] != "project" || input["environmentId"] != "environment" {
				t.Error("unscoped deployment read")
			}
			io.WriteString(w, `{"data":{"deployments":{"edges":[{"node":{"id":"older","status":"SUCCESS","createdAt":"2026-09-01T00:00:00Z"}},{"node":{"id":"building","status":"BUILDING","createdAt":"2026-09-03T00:00:00Z"}},{"node":{"id":"serving","status":"SUCCESS","createdAt":"2026-09-02T00:00:00Z"}}]}}}`)
		case deploymentRemoveMutation:
			writes++
			if string(body.Variables["id"]) != `"serving"` || len(body.Variables) != 1 {
				t.Error("removed wrong deployment")
			}
			io.WriteString(w, `{"data":{"deploymentRemove":true}}`)
		default:
			t.Errorf("unexpected operation: %s", body.Query)
		}
	})
	runner := &procexec.FakeRunner{}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", API: api}, runner)
	if err := p.removeLatestSuccessfulDeployment(context.Background(), "service-id"); err != nil {
		t.Fatal(err)
	}
	if reads != 1 || writes != 1 || len(runner.Calls) != 0 {
		t.Fatalf("reads=%d writes=%d CLI=%d", reads, writes, len(runner.Calls))
	}
}

func TestHTTPStopDoesNotMutateWithoutSuccessfulDeployment(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(`[{"id":"building","status":"BUILDING"}]`)}}}
	p := newTestProvider(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	if err := p.removeLatestSuccessfulDeployment(context.Background(), "service-id"); err == nil {
		t.Fatal("missing successful deployment accepted")
	}
	if len(runner.Calls) != 1 || runner.Calls[0].Argv[2] != deploymentsQuery {
		t.Fatal("stop mutated infrastructure without a successful deployment")
	}
}

func TestHTTPServiceDeletePreservesOwnerGuard(t *testing.T) {
	for _, account := range []string{"owner", "other-account"} {
		t.Run(account, func(t *testing.T) {
			runner := &procexec.FakeRunner{Results: []procexec.Result{
				{Stdout: []byte(`[{"id":"service-id","name":"vmbox-box","status":"NO_DEPLOYMENT"}]`)},
				{Stdout: []byte(`{"VMBOX_ACCOUNT_ID":"owner","VMBOX_BOX_ID":"box"}`)},
				{}, // Optional resource limits.
				{Stdout: []byte(`{"volumes":[]}`)},
				{}, // Confirmed serviceDelete response in the fixture adapter.
			}}
			p := newTestProvider(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
			err := p.Delete(context.Background(), "box", provider.Owner{AccountID: account, BoxID: "box"})
			if (err == nil) != (account == "owner") {
				t.Fatalf("owner guard: %v", err)
			}
			mutations := 0
			for _, call := range runner.Calls {
				if call.Argv[2] == serviceDeleteMutation {
					mutations++
					if !strings.Contains(strings.Join(call.Argv, " "), `"id":"service-id"`) {
						t.Fatal("service deletion did not use exact ID")
					}
				}
			}
			want := 0
			if account == "owner" {
				want = 1
			}
			if mutations != want {
				t.Fatalf("service deletions=%d, want %d", mutations, want)
			}
		})
	}
}

func TestHTTPDestructiveMutationRequiresConfirmationWithoutReplay(t *testing.T) {
	for _, operation := range []struct{ query, field string }{{deploymentRemoveMutation, "deploymentRemove"}, {serviceDeleteMutation, "serviceDelete"}} {
		for _, response := range []string{`false`, `null`, `{}`, `true`} {
			t.Run(operation.field+response, func(t *testing.T) {
				calls := 0
				api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					var body struct {
						Query     string            `json:"query"`
						Variables map[string]string `json:"variables"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || body.Query != operation.query || body.Variables["id"] != "exact-id" || len(body.Variables) != 1 {
						t.Error("incorrect mutation scope")
					}
					io.WriteString(w, `{"data":{"`+operation.field+`":`+response+`}}`)
				})
				runner := &procexec.FakeRunner{}
				p := New(Config{API: api}, runner)
				err := p.confirmBooleanMutation(context.Background(), operation.query, operation.field, "exact-id")
				if response == "true" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var apiErr *APIError
					if !errors.As(err, &apiErr) || !apiErr.Ambiguous {
						t.Fatalf("missing ambiguous result: %v", err)
					}
				}
				if calls != 1 || len(runner.Calls) != 0 {
					t.Fatalf("replayed mutation or invoked CLI: HTTP=%d CLI=%d", calls, len(runner.Calls))
				}
			})
		}
	}
}
