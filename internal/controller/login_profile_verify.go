package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"strings"
	"time"
)

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
		return fmt.Errorf("%s authentication failed inside the box; refresh the local login and save a new profile (credential contents withheld)", app)
	}
	// Login status alone can accept revoked credentials. A bounded, read-only
	// one-shot verifies actual provider access; output is never logged or parsed
	// for a canned answer. This may consume a small amount of agent usage.
	if app == "claude" || app == "codex" || app == "opencode" {
		prompt := "Reply briefly to confirm this connection. Do not use tools, read files, or make changes."
		command := []string{"claude", "-p", prompt}
		if app == "codex" {
			command = []string{"codex", "exec", "--skip-git-repo-check", prompt}
		}
		if app == "opencode" {
			command = []string{"opencode", "run", "--", prompt}
		}
		r, err = p.Exec(ctx, service, command, provider.ExecOptions{})
		if err != nil || r.ExitCode != 0 {
			return fmt.Errorf("%s provider access check failed inside the box; check login, subscription/API credits, and connectivity; save a refreshed profile if needed", app)
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
