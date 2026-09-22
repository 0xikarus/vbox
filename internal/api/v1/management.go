package v1

import (
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

type ConnectedBox struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	Provider           string             `json:"provider"`
	ProviderCredential string             `json:"providerCredential,omitempty"`
	State              provider.State     `json:"state"`
	ProviderState      string             `json:"providerState,omitempty"`
	Region             string             `json:"region,omitempty"`
	Image              string             `json:"image,omitempty"`
	Resources          provider.Resources `json:"resources"`
	Storage            *provider.Storage  `json:"storage,omitempty"`
	Management         string             `json:"management"`
	ChatCapable        bool               `json:"chatCapable"`
}

type BoxInventory struct {
	LogicalBoxes   []LogicalBox                   `json:"logicalBoxes"`
	ConnectedBoxes []ConnectedBox                 `json:"connectedBoxes"`
	Infrastructure *provider.InventoryObservation `json:"infrastructure,omitempty"`
}

type DirectBoxMessageRequest struct {
	Text            string               `json:"text"`
	Agent           string               `json:"agent,omitempty"`
	Session         string               `json:"session,omitempty"`
	Images          []BoxMessageImageRef `json:"images,omitempty"`
	ParentMessageID string               `json:"parentMessageId,omitempty"`
	// SenderBoxID marks an inter-box message; controller-set only.
	SenderBoxID string `json:"-"`
}

type DirectBoxMessageResponse struct {
	Task     BoxTask    `json:"task"`
	Message  BoxMessage `json:"message"`
	Started  bool       `json:"started"`
	BoxState string     `json:"boxState"`
}

type PutChatGroupRequest struct {
	Name    string                      `json:"name"`
	Members []PutChatGroupMemberRequest `json:"members"`
}

type PutChatGroupMemberRequest struct {
	LogicalBoxID string `json:"logicalBoxId"`
	Agent        string `json:"agent,omitempty"`
	CanReceive   *bool  `json:"canReceive,omitempty"`
}

type ChatGroupMember struct {
	LogicalBoxID     string `json:"logicalBoxId"`
	BoxName          string `json:"boxName"`
	Agent            string `json:"agent"`
	CanReceive       bool   `json:"canReceive"`
	SubscriptionMode string `json:"subscriptionMode,omitempty"`
}

type ChatGroup struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Members   []ChatGroupMember `json:"members"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

type SendGroupMessageRequest struct {
	Text            string   `json:"text"`
	RecipientBoxIDs []string `json:"recipientBoxIds,omitempty"`
	SourceBoxID     string   `json:"sourceBoxId,omitempty"`
	ParentMessageID string   `json:"parentMessageId,omitempty"`
}

type GroupMessageDelivery struct {
	LogicalBoxID string    `json:"logicalBoxId"`
	BoxName      string    `json:"boxName"`
	TaskID       string    `json:"taskId,omitempty"`
	BoxMessageID string    `json:"boxMessageId,omitempty"`
	State        string    `json:"state"`
	Failure      string    `json:"failureReason,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type GroupMessage struct {
	ID              string                 `json:"id"`
	GroupID         string                 `json:"groupId"`
	UserID          string                 `json:"userId"`
	SourceBoxID     string                 `json:"sourceBoxId,omitempty"`
	SourceBoxName   string                 `json:"sourceBoxName,omitempty"`
	Text            string                 `json:"text"`
	ParentMessageID string                 `json:"parentMessageId,omitempty"`
	ThreadID        string                 `json:"threadId"`
	Deliveries      []GroupMessageDelivery `json:"deliveries"`
	CreatedAt       time.Time              `json:"createdAt"`
}
