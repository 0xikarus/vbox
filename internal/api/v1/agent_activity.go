package v1

import "time"

// AgentActivity is a controller-derived view of what a box's managed agent is
// doing. It is attached to agent-facing box and contact summaries so a
// coordinator can distinguish a running box from a working, idle, waiting or
// stalled agent without taking a screenshot.
type AgentActivity struct {
	State              string     `json:"state"`
	Since              *time.Time `json:"since,omitempty"`
	Phrase             string     `json:"phrase,omitempty"`
	LastAgentMessageAt *time.Time `json:"lastAgentMessageAt,omitempty"`
	LastInboundAt      *time.Time `json:"lastInboundAt,omitempty"`
	Unanswered         int        `json:"unanswered"`
	LastDelivery       string     `json:"lastDelivery,omitempty"`
}

// Agent activity states.
const (
	AgentActivityWorking    = "working"
	AgentActivityIdle       = "idle"
	AgentActivityWaiting    = "waiting"
	AgentActivityStalled    = "stalled"
	AgentActivityHibernated = "hibernated"
	AgentActivityStopped    = "stopped"
	AgentActivityUnknown    = "unknown"
)

// Delivery states for the newest submitted inbound message.
const (
	DeliverySent        = "sent"
	DeliveryDelivered   = "delivered"
	DeliveryRead        = "read"
	DeliveryFailed      = "failed"
	DeliveryUnconfirmed = "unconfirmed"
)
