package v1

import "time"

type CreateBoxTaskRequest struct {
	Agent   string `json:"agent"`
	Prompt  string `json:"prompt"`
	Session string `json:"session,omitempty"`
}

type BoxTask struct {
	ID            string    `json:"id"`
	LogicalBoxID  string    `json:"logicalBoxId"`
	BoxName       string    `json:"boxName"`
	UserID        string    `json:"userId"`
	RequestedRole string    `json:"-"`
	Agent         string    `json:"agent"`
	Session       string    `json:"session"`
	Prompt        string    `json:"prompt"`
	State         string    `json:"state"`
	Failure       string    `json:"failureReason,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type SendBoxMessageRequest struct {
	Text   string `json:"text"`
	Submit *bool  `json:"submit,omitempty"`
}

type BoxMessage struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	UserID    string    `json:"userId,omitempty"`
	Direction string    `json:"direction"`
	Text      string    `json:"text"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type TerminalSnapshot struct {
	BoxName    string    `json:"boxName"`
	Session    string    `json:"session"`
	Pane       string    `json:"pane"`
	Title      string    `json:"title,omitempty"`
	Command    string    `json:"command,omitempty"`
	Width      int       `json:"width,omitempty"`
	Height     int       `json:"height,omitempty"`
	Content    string    `json:"content"`
	CapturedAt time.Time `json:"capturedAt"`
}
