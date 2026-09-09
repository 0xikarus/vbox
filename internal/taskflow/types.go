// Package taskflow coordinates general tasks independently of GitHub or builds.
package taskflow

import (
	"context"
	"time"
)

type Assignment struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Instruction        string   `json:"instruction"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
	DependsOn          []string `json:"dependsOn"`
}
type Plan struct {
	Revision    int          `json:"revision"`
	Summary     string       `json:"summary"`
	Questions   []string     `json:"questions"`
	Assignments []Assignment `json:"assignments"`
}
type Message struct {
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}
type Attempt struct {
	ID           string `json:"id"`
	Stage        string `json:"stage"` // plan, work, synthesize
	AssignmentID string `json:"assignmentId,omitempty"`
	State        string `json:"state"`
	BoxID        string `json:"boxId,omitempty"`
	TaskID       string `json:"taskId,omitempty"`
	ExitCode     *int   `json:"exitCode"`
	Signal       int    `json:"signal,omitempty"`
	Output       string `json:"output,omitempty"`
	Failure      string `json:"failure,omitempty"`
}
type Workflow struct {
	ID               string    `json:"id"`
	Version          int       `json:"version"`
	State            string    `json:"state"`
	Idea             string    `json:"idea"`
	Agent            string    `json:"agent"`
	Profile          string    `json:"profile"`
	GitHubProfile    string    `json:"githubProfile,omitempty"`
	AssetIDs         []string  `json:"assetIds"`
	MaxWorkers       int       `json:"maxWorkers"`
	Messages         []Message `json:"messages"`
	Plans            []Plan    `json:"plans"`
	ApprovedRevision int       `json:"approvedRevision"`
	Attempts         []Attempt `json:"attempts"`
	Final            string    `json:"final,omitempty"`
	Verdict          string    `json:"verdict,omitempty"`
	Failure          string    `json:"failure,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}
type Create struct {
	Idea          string   `json:"idea"`
	Agent         string   `json:"agent"`
	Profile       string   `json:"profile"`
	GitHubProfile string   `json:"githubProfile,omitempty"`
	AssetIDs      []string `json:"assetIds"`
	MaxWorkers    int      `json:"maxWorkers"`
}
type Input struct {
	AccountID string
	Workflow  Workflow
	Attempt   Attempt
}
type Submission struct {
	BoxID, TaskID, State string
	Pending              bool
}

// Result is a typed, dedicated runtime result, never a tmux screen or log scrape.
type Result struct {
	Text    string `json:"text"`
	Plan    *Plan  `json:"plan,omitempty"`
	Verdict string `json:"verdict,omitempty"` // accepted, needs_revision, blocked
}
type Observation struct {
	State    string
	Finished bool
	ExitCode *int
	Signal   int
	Result   Result
	Failure  string
}
type Runner interface {
	Start(context.Context, Input) (Submission, error)
	Observe(context.Context, Input) (Observation, error)
}
