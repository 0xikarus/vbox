package v1

import "time"

// BoxContact describes one destination and the sender's effective access to it.
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

// ContactEntry is the agent-facing view of one authorized contact. ID is a
// compact, unambiguous handle derived from the internal UUID; Name is the
// account-unique box name. Agent tools accept either one.
type ContactEntry struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Group      string             `json:"group,omitempty"`
	Roles      []AgentRoleSummary `json:"roles"`
	Agent      string             `json:"agent,omitempty"`
	State      string             `json:"state,omitempty"`
	CanMessage bool               `json:"canMessage"`
	Reason     string             `json:"reason,omitempty"`
	Usage      ContactUsage       `json:"usage"`
}

// ContactUsage is a cached subscription estimate. It is never a live quota
// guarantee; observedAt lets callers judge how old the measurement is.
type ContactUsage struct {
	Status           string     `json:"status"`
	RemainingPercent *float64   `json:"remainingPercent,omitempty"`
	RemainingAmount  *float64   `json:"remainingAmount,omitempty"`
	Unit             string     `json:"unit,omitempty"`
	ObservedAt       *time.Time `json:"observedAt,omitempty"`
}
