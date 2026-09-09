package v1

import (
	"fmt"
	"strings"
	"time"
)

// ValidateProcessOptions bounds argv without interpreting it as shell source.
func ValidateProcessOptions(agent, model string, args []string) error {
	if agent == "shell" && (model != "" || len(args) != 0) {
		return fmt.Errorf("shell options belong in the shell command")
	}
	if len(model) > 256 || strings.ContainsRune(model, 0) || len(args) > 64 {
		return fmt.Errorf("model or argument limit exceeded")
	}
	for _, arg := range args {
		if arg == "--" || len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return fmt.Errorf("invalid CLI argument (no standalone --, NUL, or arguments over 4096 bytes)")
		}
	}
	return nil
}

// ProcessTask describes execution, not the semantic completion of the prompt.
type ProcessTask struct {
	SetupScript     string     `json:"setupScript,omitempty"`
	Tools           []string   `json:"tools,omitempty"`
	ID              string     `json:"id"`
	LogicalBoxID    string     `json:"logicalBoxId"`
	BoxName         string     `json:"boxName"`
	Agent           string     `json:"agent"`
	Prompt          string     `json:"prompt"`
	Model           string     `json:"model,omitempty"`
	Args            []string   `json:"args,omitempty"`
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
