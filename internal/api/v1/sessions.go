package v1

import "time"

// Session observations are bounded fingerprints, never transcripts or agent completion claims.
type Session struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Incarnation string `json:"incarnation"`
	Fingerprint string `json:"fingerprint"`
	Panes       int    `json:"panes"`
	Partial     bool   `json:"partial"`
	TaskID      string `json:"historicalTaskId,omitempty"`
	TaskAgent   string `json:"historicalTaskAgent,omitempty"`
	TaskState   string `json:"historicalTaskState,omitempty"`
}

type SessionInventory struct {
	LogicalBoxID string    `json:"logicalBoxId"`
	Assignment   string    `json:"assignment"`
	State        string    `json:"state"`
	ObservedAt   time.Time `json:"observedAt"`
	Partial      bool      `json:"partial"`
	Sessions     []Session `json:"sessions"`
}

type NativeConnection struct {
	LogicalBoxConnection
	Assignment  string `json:"assignment"`
	SessionID   string `json:"sessionId"`
	Incarnation string `json:"incarnation"`
}
