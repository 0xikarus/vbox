package controller

import (
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"testing"
)

func TestPrimaryRequiresExactLiveIncarnation(t *testing.T) {
	inv := v1.SessionInventory{State: "live", Sessions: []v1.Session{{ID: "$1", Name: "old ü session", Incarnation: "new"}}}
	if name, err := selectedPrimary(inv, "$1", "new"); err != nil || name != "old ü session" {
		t.Fatal(name, err)
	}
	for _, pair := range [][2]string{{"$1", "old"}, {"$2", "new"}, {"", "new"}} {
		if _, err := selectedPrimary(inv, pair[0], pair[1]); err == nil {
			t.Fatal("stale/absent selection accepted")
		}
	}
	inv.State = "offline"
	if _, err := selectedPrimary(inv, "$1", "new"); err == nil {
		t.Fatal("offline selection accepted")
	}
}
