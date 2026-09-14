package railway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestTargetedInventoryRefreshIsCoalescedAndBackgroundBudgeted(t *testing.T) {
	cache := NewInventoryCache(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	api := &HTTPAPI{
		Token:            "targeted-refresh-token",
		TokenEnvironment: "RAILWAY_API_TOKEN",
		Budget:           NewLocalRequestBudget(time.Nanosecond, 100),
		Client: &http.Client{Transport: apiRoundTripper(func(r *http.Request) (*http.Response, error) {
			if !isBackgroundRequest(r.Context()) {
				t.Error("targeted refresh did not use background budget")
			}
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
			body := `{"data":{"environment":{"id":"environment","projectId":"project","serviceInstances":{"edges":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}`
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
	}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment", API: api, Inventory: cache}, &procexec.FakeRunner{})
	errorsOut := make(chan error, 12)
	go func() { errorsOut <- p.RefreshInventory(context.Background()) }()
	<-started
	for i := 1; i < cap(errorsOut); i++ {
		go func() { errorsOut <- p.RefreshInventory(context.Background()) }()
	}
	close(release)
	for i := 0; i < cap(errorsOut); i++ {
		if err := <-errorsOut; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate webhook hints made %d inventory reads", calls.Load())
	}
	observation, err := p.ObserveInventory(context.Background())
	if err != nil || !observation.Available || observation.Stale || !observation.ObservedAt.After(time.Time{}) {
		t.Fatalf("targeted observation unavailable: %+v %v", observation, err)
	}
}

func TestInventoryCacheCoalescesAndCopiesResults(t *testing.T) {
	cache := NewInventoryCache(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	read := func(context.Context) ([]provider.Box, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []provider.Box{{ID: "one", Storage: &provider.Storage{ID: "volume"}, Connection: provider.Connection{Metadata: map[string]string{"key": "value"}}}}, nil
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() { _, err := cache.list(cancelCtx, "scope", read); cancelled <- err }()
	<-started
	cancel()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	results := make(chan []provider.Box, 20)
	for i := 0; i < 20; i++ {
		go func() {
			boxes, err := cache.list(context.Background(), "scope", read)
			if err != nil {
				t.Error(err)
			}
			results <- boxes
		}()
	}
	close(release)
	for i := 0; i < 20; i++ {
		boxes := <-results
		if len(boxes) != 1 {
			t.Fatal("missing result")
		}
		boxes[0].Storage.ID = "changed"
		boxes[0].Connection.Metadata["key"] = "changed"
	}
	boxes, err := cache.list(context.Background(), "scope", read)
	if err != nil || boxes[0].Storage.ID != "volume" || boxes[0].Connection.Metadata["key"] != "value" || calls.Load() != 1 {
		t.Fatalf("shared observation corrupted: calls=%d err=%v", calls.Load(), err)
	}
}

func TestInventoryCacheInvalidationFencesInflightRead(t *testing.T) {
	cache := NewInventoryCache(context.Background())
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = cache.list(context.Background(), "scope", func(context.Context) ([]provider.Box, error) {
			close(started)
			<-release
			return []provider.Box{{ID: "old"}}, nil
		})
	}()
	<-started
	cache.invalidate("scope")
	fresh := func(context.Context) ([]provider.Box, error) { return []provider.Box{{ID: "new"}}, nil }
	if _, err := cache.list(context.Background(), "scope", fresh); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-finished
	boxes, err := cache.list(context.Background(), "scope", func(context.Context) ([]provider.Box, error) { t.Error("unnecessary refresh"); return nil, nil })
	if err != nil || len(boxes) != 1 || boxes[0].ID != "new" {
		t.Fatalf("old read overwrote new generation: %v %v", boxes, err)
	}
}

func TestInventoryCacheFailureRetainsObservationWithoutReportingEmptyFleet(t *testing.T) {
	cache := NewInventoryCache(context.Background())
	_, err := cache.list(context.Background(), "scope", func(context.Context) ([]provider.Box, error) { return []provider.Box{{ID: "known"}}, nil })
	if err != nil {
		t.Fatal(err)
	}
	cache.invalidate("scope")
	limited := &RateLimitError{RetryAt: time.Now().Add(time.Minute)}
	boxes, err := cache.list(context.Background(), "scope", func(context.Context) ([]provider.Box, error) { return []provider.Box{}, limited })
	if !errors.Is(err, limited) || boxes != nil {
		t.Fatalf("failed refresh reported success: %v %v", boxes, err)
	}
	cache.mu.Lock()
	retained := append([]byte(nil), cache.entries["scope"].data...)
	cache.mu.Unlock()
	boxes, err = decodeInventory(retained)
	if err != nil || len(boxes) != 1 || boxes[0].ID != "known" {
		t.Fatal("last observation lost")
	}
}

func TestInventoryScopeIncludesCredentialProjectEnvironmentAndAuthMode(t *testing.T) {
	base := Config{ProjectID: "project", EnvironmentID: "env", Token: "private-fixture"}
	key := New(base, nil).inventoryKey()
	if key != New(base, nil).inventoryKey() {
		t.Fatal("provider aliases cannot share snapshot")
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Token = "different" },
		func(c *Config) { c.ProjectID = "different" },
		func(c *Config) { c.EnvironmentID = "different" },
		func(c *Config) { c.TokenEnvironment = "RAILWAY_TOKEN" },
	} {
		cfg := base
		change(&cfg)
		if key == New(cfg, nil).inventoryKey() {
			t.Fatal("snapshot crossed credential scope")
		}
	}
}

func TestInventoryBackgroundRefreshAndIdleEviction(t *testing.T) {
	cache := NewInventoryCache(context.Background())
	refreshed := make(chan struct{}, 1)
	var calls atomic.Int32
	read := func(ctx context.Context) ([]provider.Box, error) {
		if calls.Add(1) > 1 {
			if !isBackgroundRequest(ctx) {
				t.Error("scheduled refresh did not use background budget")
			}
			refreshed <- struct{}{}
		}
		return []provider.Box{{ID: "observed"}}, nil
	}
	if _, err := cache.list(context.Background(), "active", read); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	cache.entries["active"].expires = time.Now().Add(-time.Second)
	cache.entries["idle"] = &inventoryEntry{lastUsed: time.Now().Add(-16 * time.Minute), read: read}
	cache.mu.Unlock()
	cache.refreshDue(time.Now())
	select {
	case <-refreshed:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not run")
	}
	cache.mu.Lock()
	_, idle := cache.entries["idle"]
	cache.mu.Unlock()
	if idle {
		t.Fatal("inactive credential scope retained")
	}
}

func TestInventorySharedListStillUsesFreshInspectAndInvalidatesMutations(t *testing.T) {
	cache := NewInventoryCache(context.Background())
	inventory := procexec.Result{Stdout: []byte(`[{"id":"service","name":"vmbox-box","status":"NO_DEPLOYMENT"}]`)}
	variables := procexec.Result{Stdout: []byte(`{"VMBOX_ACCOUNT_ID":"account","VMBOX_BOX_ID":"box"}`)}
	first := &procexec.FakeRunner{Results: []procexec.Result{inventory, variables}}
	second := &procexec.FakeRunner{Results: []procexec.Result{
		inventory, variables, {}, // Inspect bypasses shared List.
		{},                   // Mutation confirmed by fixture adapter.
		inventory, variables, // Next List refreshes after mutation.
	}}
	cfg := Config{ProjectID: "project", EnvironmentID: "env", Inventory: cache}
	a, b := newTestProvider(cfg, first), newTestProvider(cfg, second)
	if _, err := a.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(first.Calls) != 2 || len(second.Calls) != 0 {
		t.Fatal("provider aliases did not share complete inventory")
	}
	if _, err := b.Inspect(context.Background(), "box"); err != nil {
		t.Fatal(err)
	}
	if len(second.Calls) != 3 {
		t.Fatal("Inspect reused stale List evidence")
	}
	if err := b.confirmBooleanMutation(context.Background(), deploymentRemoveMutation, "deploymentRemove", "deployment"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(second.Calls) != 6 {
		t.Fatalf("mutation failed to invalidate shared observation: calls=%d", len(second.Calls))
	}
}

func TestInventoryObservationNeverPerformsProviderReads(t *testing.T) {
	cache := NewInventoryCache(context.Background())
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(`[]`)}}}
	p := newTestProvider(Config{ProjectID: "project", EnvironmentID: "env", Inventory: cache}, runner)
	for i := 0; i < 20; i++ {
		observation, err := p.ObserveInventory(context.Background())
		if err != nil || observation.Available || !observation.Stale || !observation.ObservedAt.IsZero() {
			t.Fatalf("unobserved inventory: %+v %v", observation, err)
		}
	}
	if len(runner.Calls) != 0 {
		t.Fatal("status polling invoked provider")
	}
	// The background/lifecycle reader supplies a complete empty observation.
	if _, err := p.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	observation, err := p.ObserveInventory(context.Background())
	if err != nil || !observation.Available || observation.Stale || observation.ObservedAt.IsZero() || len(observation.Boxes) != 0 {
		t.Fatalf("observed empty inventory: %+v %v", observation, err)
	}
	observedAt := observation.ObservedAt
	cache.invalidate(p.inventoryKey())
	limited := &RateLimitError{RetryAt: time.Now().Add(time.Minute)}
	if _, err := cache.list(context.Background(), p.inventoryKey(), func(context.Context) ([]provider.Box, error) { return nil, limited }); !errors.Is(err, limited) {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		observation, err = p.ObserveInventory(context.Background())
		if err != nil || !observation.Available || !observation.Stale || !observation.RefreshFailed || !observation.ObservedAt.Equal(observedAt) {
			t.Fatalf("stale observation lost: %+v %v", observation, err)
		}
	}
	if len(runner.Calls) != 1 {
		t.Fatal("stale status polling invoked provider")
	}
}
