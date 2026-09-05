package v1

import "time"

// ProcessTask describes execution, not the semantic completion of the prompt.
type ProcessTask struct {
	ID              string     `json:"id"`
	LogicalBoxID    string     `json:"logicalBoxId"`
	BoxName         string     `json:"boxName"`
	Agent           string     `json:"agent"`
	Prompt          string     `json:"prompt"`
	Session         string     `json:"session"`
	State           string     `json:"state"`
	CreatedAt       time.Time  `json:"createdAt"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	ExitCode        *int       `json:"exitCode"`
	Signal          int        `json:"signal,omitempty"`
	Output          string     `json:"output,omitempty"`
	OutputTruncated bool       `json:"outputTruncated"`
}
