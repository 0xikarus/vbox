package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"strings"
	"time"
)

// A completed CLI check with a rejected login/model cannot be repaired by
// replaying volume setup. Keep transport interruptions recoverable separately.
type loginVerificationError struct{ message string }

func (e *loginVerificationError) Error() string { return e.message }

// Checks execute as the workload user through the provider's Exec transport.
// Never include CLI output in errors: auth tools may print account/token details.
func verifyProvisionedLogin(ctx context.Context, p provider.Provider, service, app, host, user, model string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var argv []string
	switch app {
	case "claude":
		argv = []string{"claude", "auth", "status", "--json"}
	case "codex":
		argv = []string{"codex", "login", "status"}
	case "opencode":
		argv = []string{"opencode", "auth", "list"}
	case "github":
		argv = []string{"gh", "api", "--hostname", host, "user", "--jq", ".login"}
	default:
		return fmt.Errorf("unsupported authentication check")
	}
	r, err := p.Exec(ctx, service, argv, provider.ExecOptions{})
	valid := err == nil && r.ExitCode == 0
	if app == "claude" {
		var status struct {
			LoggedIn bool `json:"loggedIn"`
		}
		valid = valid && json.Unmarshal([]byte(r.Stdout), &status) == nil && status.LoggedIn
	}
	if app == "github" {
		valid = valid && strings.EqualFold(strings.TrimSpace(r.Stdout), user)
	}
	if !valid {
		if err != nil {
			return fmt.Errorf("%s authentication check could not complete; retry when the worker is reachable", app)
		}
		return &loginVerificationError{fmt.Sprintf("%s authentication failed inside the box; refresh the local login and save a new profile (credential contents withheld)", app)}
	}
	// OpenCode's local model inventory detects invalid provider/model selections
	// without generating a paid answer or waiting behind provider rate limits.
	// Model existence does not establish provider credit/quota availability;
	// that is checked when the managed agent actually sends a request.
	if app == "opencode" && model != "" {
		r, err = p.Exec(ctx, service, []string{"opencode", "models"}, provider.ExecOptions{})
		if err != nil || r.ExitCode != 0 || strings.TrimSpace(r.Stdout) == "" {
			return fmt.Errorf("OpenCode model availability check could not complete; retry when the worker is reachable")
		}
		if !openCodeInventoryContains(r.Stdout, model) {
			// OpenCode's cached models.dev inventory can lag behind a provider's
			// live catalog. Refresh it once before rejecting a newly added model.
			r, err = p.Exec(ctx, service, []string{"opencode", "models", "--refresh"}, provider.ExecOptions{})
			if err != nil || r.ExitCode != 0 || strings.TrimSpace(r.Stdout) == "" {
				return fmt.Errorf("OpenCode model availability check could not complete; retry when the worker is reachable")
			}
			if !openCodeInventoryContains(r.Stdout, model) {
				return &loginVerificationError{"selected OpenCode model is unavailable; choose an available provider/model in the profile settings"}
			}
		}
	}
	// Claude's auth status is sufficient for its interactive TUI. Codex keeps
	// its bounded provider preflight; OpenCode uses the inventory check above.
	if app == "codex" {
		prompt := "Reply briefly to confirm this connection. Do not use tools, read files, or make changes."
		r, err = p.Exec(ctx, service, []string{"codex", "exec", "--skip-git-repo-check", prompt}, provider.ExecOptions{})
		if err != nil {
			return fmt.Errorf("codex provider access check could not complete; retry when the worker and provider are reachable")
		}
		if r.ExitCode != 0 {
			return &loginVerificationError{"codex provider access check failed inside the box; check model availability, login, subscription/API credits, and connectivity; retry with a working profile"}
		}
	}
	if app == "github" {
		r, err = p.Exec(ctx, service, []string{"gh", "auth", "setup-git", "--hostname", host}, provider.ExecOptions{})
		if err != nil || r.ExitCode != 0 {
			return fmt.Errorf("GitHub login verified, but git credential-helper setup failed")
		}
	}
	return nil
}

func openCodeInventoryContains(inventory, model string) bool {
	for _, id := range strings.Split(inventory, "\n") {
		if strings.TrimSpace(id) == model {
			return true
		}
	}
	return false
}
