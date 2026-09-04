package v1

import (
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

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
	BoxName    string          `json:"boxName"`
	Session    string          `json:"session"`
	Pane       string          `json:"pane"`
	Title      string          `json:"title,omitempty"`
	Command    string          `json:"command,omitempty"`
	Width      int             `json:"width,omitempty"`
	Height     int             `json:"height,omitempty"`
	Content    string          `json:"content"`
	CapturedAt time.Time       `json:"capturedAt"`
	Prompt     *TerminalPrompt `json:"prompt,omitempty"`
}

type TerminalPrompt struct {
	ID      string                 `json:"id"`
	Text    string                 `json:"text"`
	Choices []TerminalPromptChoice `json:"choices"`
}

type TerminalPromptChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type LogicalBoxConnection struct {
	LogicalBoxID string              `json:"logicalBoxId"`
	BoxName      string              `json:"boxName"`
	Session      string              `json:"session"`
	Connection   provider.Connection `json:"connection"`
}
