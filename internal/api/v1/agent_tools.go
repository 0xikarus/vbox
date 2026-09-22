package v1

import "time"

type AgentRunBudget struct {
	BoxID                string     `json:"boxId"`
	State                string     `json:"state"`
	RemainingSeconds     int64      `json:"remainingSeconds"`
	DeadlineAt           *time.Time `json:"deadlineAt,omitempty"`
	ExtensionUsedMinutes int        `json:"extensionUsedMinutes"`
	MaxExtensionMinutes  int        `json:"maxExtensionMinutes"`
	MaxTotalMinutes      int        `json:"maxTotalMinutes"`
	CanRequestMoreTime   bool       `json:"canRequestMoreTime"`
}

type RequestMoreTimeRequest struct {
	Minutes int `json:"minutes"`
}

type QueueFollowupRequest struct {
	Text         string `json:"text"`
	DelaySeconds int    `json:"delaySeconds,omitempty"`
}

type AgentFollowup struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	DueAt     time.Time `json:"dueAt"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
}

type ThreadHistory struct {
	ThreadID      string         `json:"threadId"`
	ChatID        string         `json:"chatId,omitempty"`
	Messages      []BoxMessage   `json:"messages,omitempty"`
	GroupMessages []GroupMessage `json:"groupMessages,omitempty"`
	HasMore       bool           `json:"hasMore"`
	NextBefore    string         `json:"nextBefore,omitempty"`
	NextBeforeID  string         `json:"nextBeforeId,omitempty"`
}
