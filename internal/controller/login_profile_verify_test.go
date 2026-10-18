package controller

import (
	"context"
	"errors"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"strings"
	"testing"
)

type loginCheckProvider struct {
	provider.Provider
	result   provider.ExecResult
	commands [][]string
	results  []provider.ExecResult
	execErr  error
}

func (p *loginCheckProvider) Exec(_ context.Context, _ string, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	p.commands = append(p.commands, argv)
	if len(p.results) >= len(p.commands) {
		return p.results[len(p.commands)-1], p.execErr
	}
	return p.result, p.execErr
}
func TestVerifyProvisionedLogin(t *testing.T) {
	for _, tc := range []struct {
		app, output string
		exit        int
		valid       bool
	}{
		{"claude", `{"loggedIn":true}`, 0, true}, {"claude", `{"loggedIn":false}`, 0, false},
		{"claude", `synthetic-secret`, 1, false}, {"codex", "", 0, true}, {"codex", "synthetic-secret", 1, false},
		{"github", "alice\n", 0, true}, {"github", "someone-else", 0, false}, {"github", "synthetic-secret", 1, false},
	} {
		p := &loginCheckProvider{result: provider.ExecResult{Stdout: tc.output, ExitCode: tc.exit}}
		err := verifyProvisionedLogin(context.Background(), p, "service", tc.app, "github.com", "alice")
		if (err == nil) != tc.valid {
			t.Fatalf("%s valid=%t err=%v", tc.app, tc.valid, err)
		}
		if err != nil && strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("CLI output leaked")
		}
		if tc.app == "github" && tc.valid && len(p.commands) != 2 {
			t.Fatal("git helper not configured")
		}
	}
}

func TestClaudeCreationDoesNotRequireOneShotCompletion(t *testing.T) {
	p := &loginCheckProvider{result: provider.ExecResult{Stdout: `{"loggedIn":true}`}}
	if err := verifyProvisionedLogin(context.Background(), p, "service", "claude", "", ""); err != nil {
		t.Fatal(err)
	}
	if len(p.commands) != 1 || strings.Join(p.commands[0], " ") != "claude auth status --json" {
		t.Fatalf("Claude creation must only check login status, got commands %v", p.commands)
	}
}

func TestOpenCodeCreationUsesNonInteractiveRun(t *testing.T) {
	p := &loginCheckProvider{result: provider.ExecResult{ExitCode: 0}}
	if err := verifyProvisionedLogin(context.Background(), p, "service", "opencode", "", ""); err != nil {
		t.Fatal(err)
	}
	if len(p.commands) != 2 {
		t.Fatalf("OpenCode creation must check auth and provider access, got commands %v", p.commands)
	}
	if got := strings.Join(p.commands[1], " "); got != "opencode run --auto --print-logs -- Reply briefly to confirm this connection. Do not use tools, read files, or make changes." {
		t.Fatalf("OpenCode provider check must use non-interactive mode, got %q", got)
	}
}

func TestLoginVerificationRejectsUnavailableModelWithoutLeakingLogs(t *testing.T) {
	p := &loginCheckProvider{results: []provider.ExecResult{{}, {ExitCode: 1, Stderr: "secret-token ProviderModelNotFoundError: secret-model"}}}
	err := verifyProvisionedLogin(context.Background(), p, "service", "opencode", "", "")
	var terminal *loginVerificationError
	if !errors.As(err, &terminal) || !strings.Contains(err.Error(), "model is unavailable") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe or nonterminal error: %v", err)
	}
}

func TestLoginVerificationTransportFailureRemainsRecoverable(t *testing.T) {
	p := &loginCheckProvider{execErr: context.DeadlineExceeded}
	err := verifyProvisionedLogin(context.Background(), p, "service", "opencode", "", "")
	var terminal *loginVerificationError
	if err == nil || errors.As(err, &terminal) {
		t.Fatalf("transport error must stay recoverable: %v", err)
	}
}
