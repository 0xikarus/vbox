package v1

import "time"

// BoxContact is one directed contact edge between two boxes of an account.
type BoxContact struct {
	BoxID        string             `json:"boxId"`
	BoxName      string             `json:"boxName"`
	ContactBoxID string             `json:"contactBoxId"`
	ContactName  string             `json:"contactName"`
	ContactRoles []AgentRoleSummary `json:"contactRoles"`
	ContactAgent string             `json:"contactAgent,omitempty"`
	ContactState string             `json:"contactState,omitempty"`
	Override     string             `json:"override"`
	CanMessage   bool               `json:"canMessage"`
	Reason       string             `json:"reason"`
	Protected    bool               `json:"protected"`
	UpdatedAt    time.Time          `json:"updatedAt,omitempty"`
}

// PutBoxContactRequest sets a directed override. Inherit removes the override;
// Allow and Block write an explicit decision. TwoWay applies the same state in
// the reverse direction atomically.
type PutBoxContactRequest struct {
	Contact string `json:"contact"`
	State   string `json:"state"`
	TwoWay  bool   `json:"twoWay,omitempty"`
}

// ContactEntry is the agent-facing view of one authorized contact.
type ContactEntry struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Roles      []AgentRoleSummary `json:"roles"`
	Agent      string             `json:"agent,omitempty"`
	State      string             `json:"state,omitempty"`
	CanMessage bool               `json:"canMessage"`
	Reason     string             `json:"reason,omitempty"`
}
