// Package factory coordinates engineering work without owning provider resources.
package factory

import (
	"fmt"
	"strings"
	"time"
)

const ContractVersion = 1

type Check struct {
	Argv           []string `json:"argv"`
	Cwd            string   `json:"cwd"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

type Feature struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
	DependsOn          []string `json:"dependsOn"`
	Files              []string `json:"files"`
	Checks             []Check  `json:"checks"`
	State              string   `json:"state,omitempty"`
	IssueURL           string   `json:"issueUrl,omitempty"`
	PRURL              string   `json:"prUrl,omitempty"`
	BoxID              string   `json:"boxId,omitempty"`
}

type Plan struct {
	InputRevision int       `json:"inputRevision"`
	Revision      int       `json:"revision"`
	Markdown      string    `json:"markdown"`
	Questions     []string  `json:"questions"`
	Features      []Feature `json:"features"`
	CreatedAt     time.Time `json:"createdAt"`
	AttemptID     string    `json:"attemptId"`
	BaseSHA       string    `json:"baseSha"`
}

type Message struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	AssetIDs  []string  `json:"assetIds"`
	CreatedAt time.Time `json:"createdAt"`
	AttemptID string    `json:"attemptId,omitempty"`
}

type Repository struct {
	ID             string `json:"id"`
	FullName       string `json:"fullName"`
	DefaultBranch  string `json:"defaultBranch"`
	InstallationID int64  `json:"installationId"`
}

type CreateWork struct {
	RepositoryID string   `json:"repositoryId"`
	BaseRef      string   `json:"baseRef"`
	Idea         string   `json:"idea"`
	Agent        string   `json:"agent"`
	Profile      string   `json:"profile"`
	AssetIDs     []string `json:"assetIds"`
}

func (r CreateWork) Validate() error {
	if strings.TrimSpace(r.RepositoryID) == "" || strings.TrimSpace(r.Idea) == "" || len(r.Idea) > 100000 {
		return fmt.Errorf("repository and idea (1–100000 bytes) required")
	}
	if (r.Agent != "codex" && r.Agent != "claude") || strings.TrimSpace(r.Profile) == "" {
		return fmt.Errorf("select a Claude or Codex planner and saved profile")
	}
	if len(r.AssetIDs) > 8 {
		return fmt.Errorf("at most 8 image attachments permitted")
	}
	seen := map[string]bool{}
	for _, id := range r.AssetIDs {
		if id == "" || seen[id] {
			return fmt.Errorf("attachment IDs must be nonempty and unique")
		}
		seen[id] = true
	}
	return nil
}

// ValidateApproval checks executable structure, not the truth of an agent report.
// Passing it never grants publishing permissions or proves a build passed.
func (p Plan) ValidateApproval() error {
	if p.Revision < 1 || strings.TrimSpace(p.Markdown) == "" || p.BaseSHA == "" {
		return fmt.Errorf("a versioned plan and resolved source revision are required")
	}
	if len(p.Questions) > 0 {
		return fmt.Errorf("answer outstanding planning questions before approval")
	}
	if len(p.Features) == 0 || len(p.Features) > 100 {
		return fmt.Errorf("plan requires 1–100 features")
	}
	byID := make(map[string]Feature, len(p.Features))
	for _, f := range p.Features {
		if f.ID == "" || strings.TrimSpace(f.Title) == "" {
			return fmt.Errorf("feature ID and title required")
		}
		if _, ok := byID[f.ID]; ok {
			return fmt.Errorf("duplicate feature ID %q", f.ID)
		}
		if len(f.AcceptanceCriteria) == 0 || len(f.Checks) == 0 {
			return fmt.Errorf("feature %q needs acceptance criteria and verification checks", f.ID)
		}
		for _, criterion := range f.AcceptanceCriteria {
			if strings.TrimSpace(criterion) == "" {
				return fmt.Errorf("empty acceptance criterion")
			}
		}
		for _, check := range f.Checks {
			if len(check.Argv) == 0 || strings.TrimSpace(check.Argv[0]) == "" || check.Cwd == "" || check.TimeoutSeconds < 1 || check.TimeoutSeconds > 3600 {
				return fmt.Errorf("feature %q needs exact argv, working directory and bounded check timeout", f.ID)
			}
			for _, arg := range check.Argv {
				if strings.ContainsRune(arg, 0) {
					return fmt.Errorf("check argv contains NUL")
				}
			}
		}
		byID[f.ID] = f
	}
	visited, active := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		f, ok := byID[id]
		if !ok {
			return fmt.Errorf("unknown feature dependency %q", id)
		}
		if active[id] {
			return fmt.Errorf("dependency cycle at %q", id)
		}
		if visited[id] {
			return nil
		}
		active[id] = true
		dependencies := map[string]bool{}
		for _, dep := range f.DependsOn {
			if dependencies[dep] {
				return fmt.Errorf("duplicate dependency %q", dep)
			}
			dependencies[dep] = true
			if err := visit(dep); err != nil {
				return err
			}
		}
		active[id] = false
		visited[id] = true
		return nil
	}
	for _, f := range p.Features {
		if err := visit(f.ID); err != nil {
			return err
		}
	}
	return nil
}
