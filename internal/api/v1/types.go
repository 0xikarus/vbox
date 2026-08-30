package v1

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

const CompatibilityVersion = "v1alpha1"

type JobState string

const (
	JobQueued       JobState = "queued"
	JobProvisioning JobState = "provisioning"
	JobPreparing    JobState = "preparing"
	JobRunning      JobState = "running"
	JobNeedsInput   JobState = "needs_input"
	JobResuming     JobState = "resuming"
	JobSucceeded    JobState = "succeeded"
	JobFailed       JobState = "failed"
	JobCancelled    JobState = "cancelled"
	JobCleaningUp   JobState = "cleaning_up"
	JobDeleted      JobState = "deleted"
	JobRetained     JobState = "retained"
)

type LifecycleAction string

const (
	LifecycleDelete LifecycleAction = "delete"
	LifecycleRetain LifecycleAction = "retain"
	LifecycleStop   LifecycleAction = "stop"
)

type LifecyclePolicy struct {
	OnSuccess       LifecycleAction `json:"onSuccess"`
	OnFailure       LifecycleAction `json:"onFailure"`
	MaxTTL          time.Duration   `json:"-"`
	MaxTTLText      string          `json:"maxTtl,omitempty"`
	GracePeriod     time.Duration   `json:"-"`
	GracePeriodText string          `json:"gracePeriod,omitempty"`
}

func (p *LifecyclePolicy) Normalize() error {
	if p.MaxTTLText != "" {
		value, err := time.ParseDuration(p.MaxTTLText)
		if err != nil {
			return fmt.Errorf("maxTtl: %w", err)
		}
		p.MaxTTL = value
	}
	if p.GracePeriodText != "" {
		value, err := time.ParseDuration(p.GracePeriodText)
		if err != nil {
			return fmt.Errorf("gracePeriod: %w", err)
		}
		p.GracePeriod = value
	}
	if p.OnSuccess == "" {
		p.OnSuccess = LifecycleDelete
	}
	if p.OnFailure == "" {
		p.OnFailure = LifecycleRetain
	}
	if p.MaxTTL == 0 {
		p.MaxTTL = 8 * time.Hour
	}
	if p.GracePeriod == 0 {
		p.GracePeriod = 5 * time.Minute
	}
	return nil
}

type CreateRunRequest struct {
	Provider             string             `json:"provider"`
	Box                  string             `json:"box,omitempty"`
	Resources            provider.Resources `json:"resources"`
	Region               string             `json:"region,omitempty"`
	Image                string             `json:"image,omitempty"`
	Components           []string           `json:"components,omitempty"`
	Command              []string           `json:"command"`
	ExternalReference    string             `json:"externalReference,omitempty"`
	CredentialReferences []string           `json:"credentialReferences,omitempty"`
	Lifecycle            LifecyclePolicy    `json:"lifecycle"`
	NotificationPolicy   string             `json:"notificationPolicy,omitempty"`
}

type Run struct {
	ID                string           `json:"id"`
	AccountID         string           `json:"accountId"`
	BoxID             string           `json:"boxId,omitempty"`
	Provider          string           `json:"provider"`
	State             JobState         `json:"state"`
	Request           CreateRunRequest `json:"request"`
	ExternalReference string           `json:"externalReference,omitempty"`
	CreatedAt         time.Time        `json:"createdAt"`
	UpdatedAt         time.Time        `json:"updatedAt"`
	StartedAt         *time.Time       `json:"startedAt,omitempty"`
	FinishedAt        *time.Time       `json:"finishedAt,omitempty"`
	ExitCode          *int             `json:"exitCode,omitempty"`
	Summary           string           `json:"summary,omitempty"`
	LastOutputAt      *time.Time       `json:"lastOutputAt,omitempty"`
	LastHeartbeatAt   *time.Time       `json:"lastHeartbeatAt,omitempty"`
	LastActivity      string           `json:"lastActivity,omitempty"`
	Lease             string           `json:"-"`
}

type Event struct {
	ID        string          `json:"id"`
	AccountID string          `json:"accountId"`
	RunID     string          `json:"runId"`
	Sequence  uint64          `json:"sequence"`
	Type      string          `json:"type"`
	State     JobState        `json:"state,omitempty"`
	Stream    string          `json:"stream,omitempty"`
	Message   string          `json:"message,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Signature string          `json:"signature,omitempty"`
}

type Question struct {
	ID         string     `json:"id"`
	AccountID  string     `json:"accountId"`
	RunID      string     `json:"runId"`
	Prompt     string     `json:"prompt"`
	State      string     `json:"state"`
	Blocking   bool       `json:"blocking"`
	AskedAt    time.Time  `json:"askedAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	Answer     string     `json:"answer,omitempty"`
	AnsweredAt *time.Time `json:"answeredAt,omitempty"`
	AnsweredBy string     `json:"answeredBy,omitempty"`
}

type AnswerRequest struct {
	Answer string `json:"answer"`
}
type Error struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}
