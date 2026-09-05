package v1

import "time"

type SessionUpdate struct {
	LogicalBoxID string    `json:"logicalBoxId"`
	Session      string    `json:"session"`
	Incarnation  string    `json:"incarnation"`
	Revision     string    `json:"revision"`
	State        string    `json:"state"`
	ObservedAt   time.Time `json:"observedAt"`
	Partial      bool      `json:"partial"`
}

type SessionUpdates struct {
	Inventory SessionInventory `json:"inventory"`
	Updates   []SessionUpdate  `json:"updates"`
	Limit     string           `json:"limit"`
}
