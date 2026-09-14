package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider/railway"
)

func TestRailwayBudgetPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	first.DB.SetMaxOpenConns(1)
	schema := "railway_budget_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err := first.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer first.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	if _, err := first.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	second.DB.SetMaxOpenConns(1)
	if _, err := second.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	budgets := []*railway.PostgresRequestBudget{{DB: first.DB, Interval: time.Nanosecond, Hourly: 1}, {DB: second.DB, Interval: time.Nanosecond, Hourly: 1}}
	scope := strings.Repeat("a", 64)
	var granted atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			attempt, stop := context.WithTimeout(ctx, 150*time.Millisecond)
			defer stop()
			err := budgets[index%2].Acquire(attempt, scope)
			if err == nil {
				granted.Add(1)
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("unexpected quota error: %v", err)
			}
		}(i)
	}
	group.Wait()
	if granted.Load() != 1 {
		t.Fatalf("concurrent controllers granted %d hourly permits", granted.Load())
	}
	var count int
	if err := first.DB.QueryRowContext(ctx, `SELECT count(*) FROM railway_api_requests WHERE quota_scope=$1`, scope).Scan(&count); err != nil || count != 1 {
		t.Fatalf("persisted permit count %d: %v", count, err)
	}
	// Recreate the gate: a new process must inherit the old process's usage.
	restarted := &railway.PostgresRequestBudget{DB: second.DB, Interval: time.Nanosecond, Hourly: 1}
	attempt, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	err = restarted.Acquire(attempt, scope)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("restart lost hourly usage", err)
	}
	if err := restarted.Acquire(ctx, strings.Repeat("b", 64)); err != nil {
		t.Fatal("independent scope blocked", err)
	}
	// HTTP clients on separate DB connections share a provider-issued cooldown.
	var requests atomic.Int32
	client := &http.Client{Transport: budgetHTTPFixture(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		headers := http.Header{}
		headers.Set("Retry-After", "3600")
		return &http.Response{StatusCode: 429, Header: headers, Body: io.NopCloser(strings.NewReader("private error body"))}, nil
	})}
	a := &railway.HTTPAPI{Token: "disposable-quota-token", TokenEnvironment: "RAILWAY_API_TOKEN", Client: client, Budget: budgets[0]}
	_, err = a.Do(ctx, "mutation { test }", nil)
	var limited *railway.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatal("rate limit not reported", err)
	}
	copy := *a
	copy.Budget = budgets[1]
	attempt, stop = context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = copy.Do(attempt, "query { test }", nil)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) || requests.Load() != 1 {
		t.Fatal("persisted cooldown was bypassed", err)
	}
	// A later successful response cannot erase another request's cooldown.
	if err := budgets[0].Observe(ctx, strings.Repeat("c", 64), 429, http.Header{"Retry-After": {"3600"}}); err != nil {
		t.Fatal(err)
	}
	if err := budgets[1].Observe(ctx, strings.Repeat("c", 64), 200, http.Header{}); err != nil {
		t.Fatal(err)
	}
	attempt, stop = context.WithTimeout(ctx, 20*time.Millisecond)
	err = budgets[1].Acquire(attempt, strings.Repeat("c", 64))
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("out-of-order success erased cooldown", err)
	}
	// A response without a quota header must preserve the learned limit across
	// controller instances; a later larger limit must not relax it either.
	limitScope := strings.Repeat("d", 64)
	for _, header := range []http.Header{{"X-Ratelimit-Limit": {"12"}}, {}, {"X-Ratelimit-Limit": {"20"}}} {
		if err := budgets[0].Observe(ctx, limitScope, 200, header); err != nil {
			t.Fatal(err)
		}
	}
	// The gate retains 80% of the reported 12 requests, rounded down.
	var learned int
	if err := second.DB.QueryRowContext(ctx, `SELECT remote_hourly_limit FROM railway_api_budgets WHERE quota_scope=$1`, limitScope).Scan(&learned); err != nil || learned != 9 {
		t.Fatalf("learned limit was lost or relaxed: %d, %v", learned, err)
	}
	// Reserve capacity in both directions: inventory cannot exhaust user permits,
	// and sustained user work cannot starve all background reconciliation.
	priority := &railway.PostgresRequestBudget{DB: first.DB, Interval: time.Nanosecond, Hourly: 5}
	for _, backgroundFirst := range []bool{true, false} {
		priorityScope := strings.Repeat("e", 64)
		firstCtx, reservedCtx := railway.BackgroundRequestContext(ctx), ctx
		if !backgroundFirst {
			priorityScope = strings.Repeat("f", 64)
			firstCtx, reservedCtx = ctx, railway.BackgroundRequestContext(ctx)
		}
		for i := 0; i < 4; i++ {
			if err := priority.Acquire(firstCtx, priorityScope); err != nil {
				t.Fatal("priority permit unavailable", err)
			}
		}
		limitedCtx, stop := context.WithTimeout(firstCtx, 20*time.Millisecond)
		limitedErr := priority.Acquire(limitedCtx, priorityScope)
		stop()
		if !errors.Is(limitedErr, context.DeadlineExceeded) {
			t.Fatal("request class consumed reserved capacity", limitedErr)
		}
		if err := priority.Acquire(reservedCtx, priorityScope); err != nil {
			t.Fatal("reserved request class was starved", err)
		}
		limitedCtx, stop = context.WithTimeout(reservedCtx, 20*time.Millisecond)
		limitedErr = priority.Acquire(limitedCtx, priorityScope)
		stop()
		if !errors.Is(limitedErr, context.DeadlineExceeded) {
			t.Fatal("combined classes exceeded total quota", limitedErr)
		}
	}
	// Once the window ages out, permits resume. Only disposable test rows change.
	if _, err := first.DB.ExecContext(ctx, `UPDATE railway_api_requests SET requested_at=now()-interval '61 minutes' WHERE quota_scope=$1`, scope); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Acquire(ctx, scope); err != nil {
		t.Fatal("expired hourly window did not release permit", err)
	}
}

type budgetHTTPFixture func(*http.Request) (*http.Response, error)

func (f budgetHTTPFixture) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
