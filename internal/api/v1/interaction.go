package v1

import (
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

type CreateBoxTaskRequest struct {
	SetupScript     string               `json:"setupScript,omitempty"`
	Tools           []string             `json:"tools,omitempty"`
	Agent           string               `json:"agent"`
	Prompt          string               `json:"prompt"`
	Model           string               `json:"model,omitempty"`
	Args            []string             `json:"args,omitempty"`
	Session         string               `json:"session,omitempty"`
	Images          []BoxMessageImageRef `json:"images,omitempty"`
	ParentMessageID string               `json:"parentMessageId,omitempty"`
	// SenderBoxID marks an inter-box message; it is never accepted from API
	// callers and only set by the controller after contact validation.
	SenderBoxID string `json:"-"`
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
	Text            string               `json:"text"`
	Submit          *bool                `json:"submit,omitempty"`
	Images          []BoxMessageImageRef `json:"images,omitempty"`
	ParentMessageID string               `json:"parentMessageId,omitempty"`
	// SenderBoxID marks an inter-box message; controller-set only.
	SenderBoxID string `json:"-"`
}

type BoxMessageImageRef struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
}

type BoxMessageImage struct {
	ID        string `json:"id"`
	Number    int    `json:"number"`
	MediaType string `json:"mediaType"`
}

type BoxMessageQuestion struct {
	Text     string   `json:"text"`
	Choices  []string `json:"choices"`
	Multiple bool     `json:"multiple,omitempty"`
}

type TerminalInputRequest struct {
	Text   string   `json:"text,omitempty"`
	Keys   []string `json:"keys,omitempty"`
	Submit *bool    `json:"submit,omitempty"`
}

type BoxMessage struct {
	ID              string              `json:"id"`
	TaskID          string              `json:"taskId"`
	UserID          string              `json:"userId,omitempty"`
	Direction       string              `json:"direction"`
	ChatKey         string              `json:"chatKey,omitempty"`
	SenderBoxID     string              `json:"senderBoxId,omitempty"`
	ParentMessageID string              `json:"parentMessageId,omitempty"`
	ThreadID        string              `json:"threadId"`
	Text            string              `json:"text"`
	State           string              `json:"state"`
	CreatedAt       time.Time           `json:"createdAt"`
	UpdatedAt       time.Time           `json:"updatedAt"`
	Images          []BoxMessageImage   `json:"images,omitempty"`
	Question        *BoxMessageQuestion `json:"question,omitempty"`
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
	ID          string                 `json:"id"`
	Text        string                 `json:"text"`
	Choices     []TerminalPromptChoice `json:"choices"`
	ResumeInput bool                   `json:"resumeInput,omitempty"`
}

type TerminalPromptChoice struct {
	Value  string `json:"value"`
	Label  string `json:"label"`
	Input  string `json:"input"`
	Submit bool   `json:"submit"`
}

type LogicalBoxConnection struct {
	LogicalBoxID string              `json:"logicalBoxId"`
	BoxName      string              `json:"boxName"`
	Session      string              `json:"session"`
	Connection   provider.Connection `json:"connection"`
}
