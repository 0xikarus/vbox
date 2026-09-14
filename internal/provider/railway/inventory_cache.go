package railway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// InventoryCache shares complete List observations across short-lived providers.
// Inspect and storage reads deliberately bypass it. A failed refresh retains the
// prior observation internally but returns an error: absence is never inferred
// from a partial inventory or a provider cooldown.
type InventoryCache struct {
	ctx     context.Context
	mu      sync.Mutex
	entries map[string]*inventoryEntry
	ttl     time.Duration
}
type inventoryEntry struct {
	data          []byte
	expires       time.Time
	observedAt    time.Time
	refreshFailed bool
	generation    uint64
	lastUsed      time.Time
	read          func(context.Context) ([]provider.Box, error)
	flight        *inventoryFlight
}
type inventoryFlight struct {
	done     chan struct{}
	data     []byte
	err      error
	targeted bool
}

func NewInventoryCache(ctx context.Context) *InventoryCache {
	return &InventoryCache{ctx: ctx, entries: make(map[string]*inventoryEntry), ttl: 5 * time.Minute}
}

// SetInventoryCache is called before publishing a provider to concurrent users.
func (p *Provider) SetInventoryCache(cache *InventoryCache) { p.cfg.Inventory = cache }

func (p *Provider) inventoryKey() string {
	encoded, _ := json.Marshal([]string{p.cfg.API.Token, p.cfg.API.TokenEnvironment, p.cfg.ProjectID, p.cfg.EnvironmentID})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func (c *InventoryCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.entries[key]; entry != nil {
		entry.generation++
		entry.expires = time.Time{}
		// New callers must not join a read started before this mutation.
		entry.flight = nil
	}
}

func (c *InventoryCache) list(ctx context.Context, key string, read func(context.Context) ([]provider.Box, error)) ([]provider.Box, error) {
	return c.get(ctx, key, read, true)
}

func (c *InventoryCache) get(ctx context.Context, key string, read func(context.Context) ([]provider.Box, error), foreground bool) ([]provider.Box, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	entry := c.entries[key]
	if entry == nil {
		entry = &inventoryEntry{}
		c.entries[key] = entry
	}
	if foreground {
		entry.lastUsed = time.Now()
		entry.read = read
	}
	if time.Now().Before(entry.expires) {
		data := entry.data
		c.mu.Unlock()
		return decodeInventory(data)
	}
	flight := entry.flight
	if flight == nil {
		flight = &inventoryFlight{done: make(chan struct{})}
		entry.flight = flight
		generation := entry.generation
		c.launch(entry, flight, generation, read, !foreground)
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-flight.done:
		if flight.err != nil {
			return nil, flight.err
		}
		return decodeInventory(flight.data)
	}
}

func (c *InventoryCache) launch(entry *inventoryEntry, flight *inventoryFlight, generation uint64, read func(context.Context) ([]provider.Box, error), background bool) {
	go func() {
		// A cancelled caller cannot cancel another caller's shared read.
		readCtx, cancel := context.WithTimeout(c.ctx, time.Minute)
		defer cancel()
		if background {
			readCtx = BackgroundRequestContext(readCtx)
		}
		boxes, err := read(readCtx)
		var data []byte
		if err == nil {
			data, err = json.Marshal(boxes)
		}
		c.mu.Lock()
		flight.data, flight.err = data, err
		if entry.generation == generation && entry.flight == flight {
			entry.flight = nil
			entry.refreshFailed = err != nil
			if err == nil {
				entry.data = data
				entry.observedAt = time.Now()
				entry.expires = time.Now().Add(c.ttl*9/10 + time.Duration(rand.Int64N(int64(c.ttl/5)+1)))
			}
		}
		close(flight.done)
		c.mu.Unlock()
	}()
}

// RefreshInventory invalidates this provider's shared infrastructure snapshot
// and performs one coalesced, background-budgeted refresh. It is a safe target
// for webhook hints: the result is observation only and authorizes no mutation.
func (p *Provider) RefreshInventory(ctx context.Context) error {
	c := p.cfg.Inventory
	if c == nil {
		return provider.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	key := p.inventoryKey()
	c.mu.Lock()
	entry := c.entries[key]
	if entry == nil {
		entry = &inventoryEntry{}
		c.entries[key] = entry
	}
	entry.lastUsed = time.Now()
	entry.read = p.listFresh
	flight := entry.flight
	if flight == nil || !flight.targeted {
		entry.generation++
		entry.expires = time.Time{}
		flight = &inventoryFlight{done: make(chan struct{}), targeted: true}
		entry.flight = flight
		c.launch(entry, flight, entry.generation, entry.read, true)
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-flight.done:
		return flight.err
	}
}

func decodeInventory(data []byte) ([]provider.Box, error) {
	// Decode each result separately so callers cannot mutate shared map/pointer
	// fields such as connection metadata and storage.
	var boxes []provider.Box
	err := json.Unmarshal(data, &boxes)
	return boxes, err
}

// Run refreshes recently used scopes in the background. The controller starts
// one loop for its shared cache; cancellation ends pending HTTP budget waits too.
func (c *InventoryCache) Run() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case now := <-ticker.C:
			c.refreshDue(now)
		}
	}
}

func (c *InventoryCache) refreshDue(now time.Time) {
	type refresh struct {
		key  string
		read func(context.Context) ([]provider.Box, error)
	}
	var pending []refresh
	c.mu.Lock()
	for key, entry := range c.entries {
		if now.Sub(entry.lastUsed) > 15*time.Minute && entry.flight == nil {
			delete(c.entries, key)
			continue
		}
		if entry.flight == nil && entry.read != nil && !now.Before(entry.expires) {
			pending = append(pending, refresh{key, entry.read})
		}
	}
	c.mu.Unlock()
	for _, item := range pending {
		go func() {
			_, _ = c.get(c.ctx, item.key, item.read, false)
		}()
	}
}

// ObserveInventory never starts or waits for HTTP work. Registering a scope
// lets the shared background scheduler refresh it on its next pass.
func (p *Provider) ObserveInventory(ctx context.Context) (provider.InventoryObservation, error) {
	c := p.cfg.Inventory
	if c == nil {
		return provider.InventoryObservation{}, provider.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return provider.InventoryObservation{}, err
	}
	if err := c.ctx.Err(); err != nil {
		return provider.InventoryObservation{}, err
	}
	c.mu.Lock()
	key := p.inventoryKey()
	entry := c.entries[key]
	if entry == nil {
		entry = &inventoryEntry{}
		c.entries[key] = entry
	}
	entry.lastUsed = time.Now()
	entry.read = p.listFresh
	observation := provider.InventoryObservation{
		Available: entry.data != nil, ObservedAt: entry.observedAt,
		Stale:      !time.Now().Before(entry.expires),
		Refreshing: entry.flight != nil, RefreshFailed: entry.refreshFailed,
	}
	data := entry.data
	c.mu.Unlock()
	if data != nil {
		boxes, err := decodeInventory(data)
		if err != nil {
			return provider.InventoryObservation{}, err
		}
		observation.Boxes = boxes
	}
	return observation, nil
}
