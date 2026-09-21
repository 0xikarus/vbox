package v1

import "time"

// BoxContact is one stored half of a two-way contact relationship.
type BoxContact struct {
	BoxID        string    `json:"boxId"`
	BoxName      string    `json:"boxName"`
	ContactBoxID string    `json:"contactBoxId"`
	ContactName  string    `json:"contactName"`
	ContactRole  string    `json:"contactRole,omitempty"`
	ContactAgent string    `json:"contactAgent,omitempty"`
	ContactState string    `json:"contactState,omitempty"`
	CanMessage   bool      `json:"canMessage"`
	CanReceive   bool      `json:"canReceive"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
}

// PutBoxContactRequest creates or updates one edge. Contact accepts a box name
// or identifier. Absent booleans keep their current value on update and default
// to true on creation.
type PutBoxContactRequest struct {
	Contact    string `json:"contact"`
	CanMessage *bool  `json:"canMessage,omitempty"`
	CanReceive *bool  `json:"canReceive,omitempty"`
}

// ContactEntry is the agent-facing view of one addressable contact. A manager
// sees every non-protected box; a worker sees only explicit edges.
type ContactEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role,omitempty"`
	Agent      string `json:"agent,omitempty"`
	State      string `json:"state,omitempty"`
	CanMessage bool   `json:"canMessage"`
	CanReceive bool   `json:"canReceive"`
}
