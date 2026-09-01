package provider

import (
	"fmt"
	"sort"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{providers: make(map[string]Provider)}
	for _, p := range providers {
		r.Register(p)
	}
	return r
}

func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	p, ok := r.providers[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (available: %v)", name, r.Names())
	}
	return p, nil
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
