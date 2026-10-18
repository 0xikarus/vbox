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
func verifyProvisionedLogin(ctx context.Context, p provider.Provider, service, app, host, user string) error {
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
	// Claude's login-status result is enough to provision its interactive TUI.
	// A one-shot -p request uses a separate path and can fail for a temporary or
	// model-specific reason even when the imported login is usable in chat.
	// Codex and OpenCode still verify provider access with a bounded one-shot.
	if app == "codex" || app == "opencode" {
		prompt := "Reply briefly to confirm this connection. Do not use tools, read files, or make changes."
		// This short provider preflight is not the managed chat session. Provider
		// exec has no interactive permission prompt, so use non-interactive mode.
		command := []string{"opencode", "run", "--auto", "--print-logs", "--", prompt}
		if app == "codex" {
			command = []string{"codex", "exec", "--skip-git-repo-check", prompt}
		}
		r, err = p.Exec(ctx, service, command, provider.ExecOptions{})
		if err != nil {
			return fmt.Errorf("%s provider access check could not complete; retry when the worker and provider are reachable", app)
		}
		if r.ExitCode != 0 {
			// OpenCode's public response can hide this behind UnknownError.
			// Inspect only a fixed diagnostic marker; never expose CLI logs.
			if app == "opencode" && strings.Contains(r.Stdout+"\n"+r.Stderr, "ProviderModelNotFoundError") {
				return &loginVerificationError{"selected OpenCode model is unavailable; choose an available provider/model in the profile settings"}
			}
			return &loginVerificationError{fmt.Sprintf("%s provider access check failed inside the box; check model availability, login, subscription/API credits, and connectivity; retry with a working profile", app)}
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
