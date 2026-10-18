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
		err := verifyProvisionedLogin(context.Background(), p, "service", tc.app, "github.com", "alice", "")
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
	if err := verifyProvisionedLogin(context.Background(), p, "service", "claude", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if len(p.commands) != 1 || strings.Join(p.commands[0], " ") != "claude auth status --json" {
		t.Fatalf("Claude creation must only check login status, got commands %v", p.commands)
	}
}

func TestOpenCodeCreationChecksModelWithoutGeneratingAnAnswer(t *testing.T) {
	for _, tc := range []struct {
		model, inventory string
		valid            bool
	}{
		{"openrouter/fixture", "openrouter/fixture\nother/model\n", true},
		{"openrouter/fixture", "openrouter/fixture-new\n", false},
		{"stealth/removed", "openrouter/fixture\n", false},
		{"openrouter/fixture", "openrouter/fixture\r\n", true},
	} {
		p := &loginCheckProvider{results: []provider.ExecResult{{}, {Stdout: tc.inventory}}}
		err := verifyProvisionedLogin(context.Background(), p, "service", "opencode", "", "", tc.model)
		var rejected *loginVerificationError
		if (err == nil) != tc.valid || (!tc.valid && !errors.As(err, &rejected)) {
			t.Fatalf("model %s err=%v", tc.model, err)
		}
		if len(p.commands) != 2 || strings.Join(p.commands[1], " ") != "opencode models" {
			t.Fatalf("model check generated an answer: %v", p.commands)
		}
	}
}

func TestLoginVerificationDoesNotLeakInventoryErrors(t *testing.T) {
	for _, r := range []provider.ExecResult{{ExitCode: 1, Stderr: "secret-token"}, {Stdout: ""}} {
		p := &loginCheckProvider{results: []provider.ExecResult{{}, r}}
		err := verifyProvisionedLogin(context.Background(), p, "service", "opencode", "", "", "openrouter/fixture")
		var terminal *loginVerificationError
		if err == nil || errors.As(err, &terminal) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe or terminal inventory error: %v", err)
		}
	}
}

func TestLoginVerificationTransportFailureRemainsRecoverable(t *testing.T) {
	p := &loginCheckProvider{execErr: context.DeadlineExceeded}
	err := verifyProvisionedLogin(context.Background(), p, "service", "opencode", "", "", "openrouter/fixture")
	var terminal *loginVerificationError
	if err == nil || errors.As(err, &terminal) {
		t.Fatalf("transport error must stay recoverable: %v", err)
	}
}
