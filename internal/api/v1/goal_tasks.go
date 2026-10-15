package v1

import "time"

// TaskProfileRef uses an opaque catalog ID. Saved profile names never cross the
// service API boundary.
type TaskProfileRef struct {
	ProfileID       string `json:"profileId"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

type TaskLinkRequest struct {
	Kinds      []string `json:"kinds"`
	TTLSeconds int      `json:"ttlSeconds,omitempty"`
}

type CreateGoalTaskRequest struct {
	TaskProfile       string           `json:"taskProfile,omitempty"`
	Name              string           `json:"name,omitempty"`
	Goal              string           `json:"goal,omitempty"`
	Agent             string           `json:"agent,omitempty"`
	LoginProfiles     []TaskProfileRef `json:"loginProfiles,omitempty"`
	Tools             []string         `json:"tools,omitempty"`
	Instructions      string           `json:"instructions,omitempty"`
	SlotID            string           `json:"slotId,omitempty"`
	DiskGiB           int64            `json:"diskGiB,omitempty"`
	MemoryGiB         int64            `json:"memoryGiB,omitempty"`
	SwapGiB           *int64           `json:"swapGiB,omitempty"`
	DesktopResolution string           `json:"desktopResolution,omitempty"`
	RoleIDs           []string         `json:"roleIds,omitempty"`
	TimeLimitSeconds  int              `json:"timeLimitSeconds,omitempty"`
	RetentionSeconds  int              `json:"retentionSeconds,omitempty"`
	Links             *TaskLinkRequest `json:"links,omitempty"`
}

// GoalTaskProfile stores reusable task settings, with opaque login references.
// Settings excludes task-specific name, goal, links and taskProfile fields.
type GoalTaskProfile struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Settings  CreateGoalTaskRequest `json:"settings"`
	CreatedAt time.Time             `json:"createdAt"`
	UpdatedAt time.Time             `json:"updatedAt"`
}

type GoalTaskCreator struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type GoalTaskBox struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

type GoalTaskFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
}

type GoalTaskResult struct {
	Text  string         `json:"text"`
	Files []GoalTaskFile `json:"files"`
}

type GoalTaskFailure struct {
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

type GoalTaskLink struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type GoalTask struct {
	TaskProfile      string           `json:"taskProfile,omitempty"`
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	Goal             string           `json:"goal"`
	State            string           `json:"state"`
	Result           *GoalTaskResult  `json:"result"`
	Failure          *GoalTaskFailure `json:"failure"`
	Creator          GoalTaskCreator  `json:"creator"`
	Box              *GoalTaskBox     `json:"box"`
	Agent            string           `json:"agent"`
	TimeLimitSeconds int              `json:"timeLimitSeconds"`
	RetentionSeconds int              `json:"retentionSeconds"`
	QueuePosition    int              `json:"queuePosition,omitempty"`
	CreatedAt        time.Time        `json:"createdAt"`
	StartedAt        *time.Time       `json:"startedAt"`
	FinishedAt       *time.Time       `json:"finishedAt"`
	DeleteAt         *time.Time       `json:"deleteAt"`
	BoxDeletedAt     *time.Time       `json:"boxDeletedAt"`
	Links            []GoalTaskLink   `json:"links"`
}

func GoalTaskFinished(state string) bool {
	switch state {
	case "completed", "failed", "timed_out", "abandoned", "cancelled":
		return true
	}
	return false
}

// AgentGoalTaskOptions turns create_agent_box into a queued, single-goal task.
type AgentGoalTaskOptions struct {
	Goal             string           `json:"goal"`
	TimeLimitSeconds int              `json:"timeLimitSeconds,omitempty"`
	RetentionSeconds int              `json:"retentionSeconds,omitempty"`
	Links            *TaskLinkRequest `json:"links,omitempty"`
}
