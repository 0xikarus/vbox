package railway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type recordingBudget struct {
	acquired int
	observed int
}

func (b *recordingBudget) Acquire(context.Context, string) error {
	b.acquired++
	return nil
}

func (b *recordingBudget) Observe(context.Context, string, int, http.Header) error {
	b.observed++
	return nil
}

func TestConfiguredBudgetGatesCallerSuppliedHTTPAPI(t *testing.T) {
	budget := &recordingBudget{}
	api := &HTTPAPI{
		Token:            "configured-budget-token",
		TokenEnvironment: "RAILWAY_API_TOKEN",
		Client: &http.Client{Transport: apiRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":{"ok":true}}`))}, nil
		})},
	}
	p := New(Config{API: api, APIBudget: budget}, &procexec.FakeRunner{})
	if _, err := p.api(context.Background(), "query { ok }", nil); err != nil {
		t.Fatal(err)
	}
	if budget.acquired != 1 || budget.observed != 1 {
		t.Fatalf("configured persistent budget was bypassed: acquired=%d observed=%d", budget.acquired, budget.observed)
	}
	if api.Budget != nil {
		t.Fatal("provider mutated shared caller API configuration")
	}
}

func TestVariablesUseSinglePrivateBatchAndHTTPRead(t *testing.T) {
	var writes, reads int
	api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                     `json:"query"`
			Variables map[string]json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		switch body.Query {
		case variablesUpsertMutation:
			writes++
			var input struct {
				Project     string            `json:"projectId"`
				Environment string            `json:"environmentId"`
				Service     string            `json:"serviceId"`
				Values      map[string]string `json:"variables"`
				Replace     bool              `json:"replace"`
				Skip        bool              `json:"skipDeploys"`
			}
			if json.Unmarshal(body.Variables["input"], &input) != nil || input.Project != "project" || input.Environment != "environment" || input.Service != "service-id" || input.Replace || !input.Skip || len(input.Values) != 2 || input.Values["PRIVATE"] != "private-value" || input.Values["EMPTY"] != "" {
				t.Error("invalid bulk variable mutation")
			}
			io.WriteString(w, `{"data":{"variableCollectionUpsert":true}}`)
		case variablesQuery:
			reads++
			if string(body.Variables["serviceId"]) != `"service-id"` {
				t.Error("variable lookup did not use stable service ID")
			}
			io.WriteString(w, `{"data":{"variables":{"PRIVATE":"private-value","EXISTING":"retained","SEALED":null}}}`)
		default:
			t.Error("unexpected API operation")
		}
	})
	runner := &procexec.FakeRunner{}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", API: api}, runner)
	if err := p.upsertVariables(context.Background(), "service-id", map[string]string{"PRIVATE": "private-value", "EMPTY": ""}); err != nil {
		t.Fatal(err)
	}
	values, err := p.variables(context.Background(), "service-id")
	if err != nil || values["EXISTING"] != "retained" || writes != 1 || reads != 1 || len(runner.Calls) != 0 {
		t.Fatalf("variable operations: writes=%d reads=%d CLI=%d err=%v", writes, reads, len(runner.Calls), err)
	}
}

func TestHTTPDeploymentSubmissionTracksExactIDWithoutReplay(t *testing.T) {
	for _, scenario := range []string{"returned-id", "ambiguous-one-new", "ambiguous-two-new"} {
		t.Run(scenario, func(t *testing.T) {
			lists, submits, polls := 0, 0, 0
			api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Query     string                     `json:"query"`
					Variables map[string]json.RawMessage `json:"variables"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid deployment request")
					return
				}
				switch body.Query {
				case deploymentsQuery:
					lists++
					var input map[string]string
					_ = json.Unmarshal(body.Variables["input"], &input)
					if input["serviceId"] != "service-id" || input["environmentId"] != "environment" || input["projectId"] != "project" {
						t.Error("deployment list scope mismatch")
					}
					edges := []map[string]any{{"node": map[string]string{"id": "old", "status": "SUCCESS"}}}
					if lists > 1 {
						edges = append(edges, map[string]any{"node": map[string]string{"id": "submitted", "status": "BUILDING"}})
					}
					if lists > 1 && scenario == "ambiguous-two-new" {
						edges = append(edges, map[string]any{"node": map[string]string{"id": "unrelated", "status": "SUCCESS"}})
					}
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"deployments": map[string]any{"edges": edges}}})
				case serviceDeployMutation:
					submits++
					if string(body.Variables["serviceId"]) != `"service-id"` {
						t.Error("submission used a service name")
					}
					if scenario != "returned-id" {
						w.WriteHeader(503)
						return
					}
					io.WriteString(w, `{"data":{"serviceInstanceDeployV2":"submitted"}}`)
				case deploymentStatusQuery:
					polls++
					if string(body.Variables["id"]) != `"submitted"` {
						t.Error("polled wrong deployment")
					}
					io.WriteString(w, `{"data":{"deployment":{"id":"submitted","status":"SUCCESS"}}}`)
				default:
					t.Error("unexpected deployment operation")
				}
			})
			runner := &procexec.FakeRunner{}
			p := New(Config{API: api, ProjectID: "project", EnvironmentID: "environment", ReadyTimeout: time.Second, PollInterval: time.Millisecond}, runner)
			err := p.submitAndWaitDeployment(context.Background(), "service-id")
			if scenario == "ambiguous-two-new" {
				if err == nil || polls != 0 {
					t.Fatal("ambiguous deployments guessed", err)
				}
			} else if err != nil || polls != 1 {
				t.Fatal("exact deployment was not observed", err)
			}
			if submits != 1 || len(runner.Calls) != 0 {
				t.Fatalf("deployment replay or CLI use: mutations=%d CLI=%d", submits, len(runner.Calls))
			}
			if scenario == "returned-id" && lists != 1 {
				t.Fatal("known deployment caused repeated full-list polls")
			}
		})
	}
}

func TestHTTPDeploymentInventoryRejectsPartialResponse(t *testing.T) {
	api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"data":{"deployments":null}}`) })
	p := New(Config{API: api}, &procexec.FakeRunner{})
	if _, err := p.deployments(context.Background(), "service-id"); err == nil {
		t.Fatal("unavailable inventory treated as empty")
	}
}

func fixtureAPI(t *testing.T, handler http.HandlerFunc) *HTTPAPI {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	base := server.Client().Transport
	target, _ := url.Parse(server.URL)
	client := &http.Client{Transport: apiRoundTripper(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		copy.URL = &u
		return base.RoundTrip(copy)
	})}
	return &HTTPAPI{Token: "test-api-token", TokenEnvironment: "RAILWAY_API_TOKEN", Client: client, Budget: NewLocalRequestBudget(time.Nanosecond, 1000)}
}

func TestHTTPAPIHeadersBodiesAndGraphQLErrors(t *testing.T) {
	for _, kind := range []string{"RAILWAY_TOKEN", "RAILWAY_API_TOKEN"} {
		t.Run(kind, func(t *testing.T) {
			api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/graphql/v2" || r.URL.RawQuery != "" {
					t.Error("invalid API route")
				}
				if kind == "RAILWAY_TOKEN" {
					if r.Header.Get("Project-Access-Token") != "test-api-token" || r.Header.Get("Authorization") != "" {
						t.Error("invalid project token headers")
					}
				} else if r.Header.Get("Authorization") != "Bearer test-api-token" || r.Header.Get("Project-Access-Token") != "" {
					t.Error("invalid account token headers")
				}
				var body struct {
					Query     string            `json:"query"`
					Variables map[string]string `json:"variables"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Variables["secret"] != "private-variable-value" {
					t.Error("missing private body variable")
				}
				io.WriteString(w, `{"data":null,"errors":[{"message":"private-variable-value"}]}`)
			})
			api.TokenEnvironment = kind
			_, err := api.Do(context.Background(), "mutation { test }", map[string]string{"secret": "private-variable-value"})
			var failed *APIError
			if !errors.As(err, &failed) || !failed.Ambiguous || strings.Contains(err.Error(), "private-variable-value") {
				t.Fatalf("unsafe GraphQL error: %v", err)
			}
		})
	}
}

func TestHTTPAPICooldownSharedAcrossClientsWithoutMutationReplay(t *testing.T) {
	var calls atomic.Int32
	api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
		io.WriteString(w, "private provider error")
	})
	_, err := api.Do(context.Background(), "mutation { test }", nil)
	var limited *RateLimitError
	if !errors.As(err, &limited) || calls.Load() != 1 {
		t.Fatalf("mutation was retried or rate limit lost: %v", err)
	}
	alias := *api
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := alias.Do(ctx, "query { test }", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("alias ignored cooldown: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("cooldown generated another HTTP request")
	}
	alias.Token = "unrelated-test-token"
	if _, err := alias.Do(context.Background(), "query { test }", nil); !errors.As(err, &limited) || calls.Load() != 2 {
		t.Fatal("unrelated credential scope blocked")
	}
}

func TestHTTPAPIRejectsRedirectAndDoesNotReplayAmbiguousMutation(t *testing.T) {
	for _, status := range []int{302, 503} {
		var calls atomic.Int32
		api := fixtureAPI(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Location", "https://redirect.example/graphql")
			w.WriteHeader(status)
		})
		_, err := api.Do(context.Background(), "mutation { test }", nil)
		var failed *APIError
		if !errors.As(err, &failed) || !failed.Ambiguous || calls.Load() != 1 {
			t.Fatalf("unsafe mutation retry or redirect: %v", err)
		}
	}
}

func TestLocalAPIBudgetHourlyLimitAndResetHeaders(t *testing.T) {
	budget := NewLocalRequestBudget(time.Nanosecond, 1)
	if err := budget.Acquire(context.Background(), "scope"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := budget.Acquire(ctx, "scope"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("hourly budget bypassed", err)
	}
	if err := budget.Acquire(context.Background(), "other"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	header := http.Header{"Retry-After": {now.Add(2 * time.Minute).Format(http.TimeFormat)}}
	if got := apiRetryAt(header, now); !got.Equal(now.Add(2 * time.Minute)) {
		t.Fatal("HTTP-date Retry-After lost", got)
	}
}

func TestLocalAPIBudgetReservesForegroundCapacity(t *testing.T) {
	for _, limit := range []int{2, 5} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			budget := NewLocalRequestBudget(time.Nanosecond, limit)
			background := BackgroundRequestContext(context.Background())
			for i := 0; i < backgroundHourlyLimit(limit); i++ {
				if err := budget.Acquire(background, "scope"); err != nil {
					t.Fatal(err)
				}
			}
			blocked, cancel := context.WithTimeout(background, 10*time.Millisecond)
			defer cancel()
			if err := budget.Acquire(blocked, "scope"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("background consumed foreground reserve: %v", err)
			}
			if err := budget.Acquire(context.Background(), "scope"); err != nil {
				t.Fatal("foreground could not use reserved permit", err)
			}
		})
	}
}

func TestLocalAPIBudgetSinglePermitDoesNotStarveBackground(t *testing.T) {
	budget := NewLocalRequestBudget(time.Nanosecond, 1)
	background := BackgroundRequestContext(context.Background())
	if err := budget.Acquire(background, "scope"); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(background, 10*time.Millisecond)
	if err := budget.Acquire(blocked, "scope"); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatalf("single-permit hourly limit bypassed: %v", err)
	}
	cancel()

	// Move the recorded permit past the rolling window without sleeping an hour.
	budget.mu.Lock()
	budget.scopes["scope"].requests[0].at = time.Now().Add(-time.Hour - time.Second)
	budget.mu.Unlock()
	if err := budget.Acquire(background, "scope"); err != nil {
		t.Fatal("background remained starved after the rolling window", err)
	}
}

func TestLocalAPIBudgetReservesBackgroundCapacityAndTotalLimit(t *testing.T) {
	const limit = 5
	budget := NewLocalRequestBudget(time.Nanosecond, limit)
	for i := 0; i < foregroundHourlyLimit(limit); i++ {
		if err := budget.Acquire(context.Background(), "scope"); err != nil {
			t.Fatal(err)
		}
	}
	blockedForeground, cancelForeground := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := budget.Acquire(blockedForeground, "scope"); !errors.Is(err, context.DeadlineExceeded) {
		cancelForeground()
		t.Fatalf("foreground consumed background reserve: %v", err)
	}
	cancelForeground()

	background := BackgroundRequestContext(context.Background())
	if err := budget.Acquire(background, "scope"); err != nil {
		t.Fatal("background could not use reserved permit", err)
	}
	blockedBackground, cancelBackground := context.WithTimeout(background, 10*time.Millisecond)
	if err := budget.Acquire(blockedBackground, "scope"); !errors.Is(err, context.DeadlineExceeded) {
		cancelBackground()
		t.Fatalf("total hourly limit exceeded: %v", err)
	}
	cancelBackground()

	budget.mu.Lock()
	permits := len(budget.scopes["scope"].requests)
	budget.mu.Unlock()
	if permits != limit {
		t.Fatalf("recorded permits = %d, want total limit %d", permits, limit)
	}
}

func TestObservedRemoteLimitAlsoReservesForegroundCapacity(t *testing.T) {
	budget := NewLocalRequestBudget(time.Nanosecond, 100)
	headers := http.Header{}
	headers.Set("X-RateLimit-Limit", "5")
	if err := budget.Observe(context.Background(), "scope", 200, headers); err != nil {
		t.Fatal(err)
	}
	background := BackgroundRequestContext(context.Background())
	for i := 0; i < backgroundHourlyLimit(4); i++ { // Observe retains 20% provider headroom.
		if err := budget.Acquire(background, "scope"); err != nil {
			t.Fatal(err)
		}
	}
	blocked, cancel := context.WithTimeout(background, 10*time.Millisecond)
	defer cancel()
	if err := budget.Acquire(blocked, "scope"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("remote limit did not constrain background", err)
	}
	if err := budget.Acquire(context.Background(), "scope"); err != nil {
		t.Fatal("remote-limit foreground reserve unavailable", err)
	}
}
