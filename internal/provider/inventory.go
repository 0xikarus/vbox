package provider

import (
	"context"
	"time"
)

// InventoryObservation is status evidence, never authority for destructive work.
// Available distinguishes an observed empty inventory from no observation yet.
type InventoryObservation struct {
	Boxes         []Box     `json:"-"`
	Available     bool      `json:"available"`
	ObservedAt    time.Time `json:"observedAt"`
	Stale         bool      `json:"stale"`
	Refreshing    bool      `json:"refreshing"`
	RefreshFailed bool      `json:"refreshFailed"`
}
type InventoryObserver interface {
	ObserveInventory(context.Context) (InventoryObservation, error)
}
