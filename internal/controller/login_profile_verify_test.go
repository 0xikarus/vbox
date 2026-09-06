package controller

import (
	"context"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"strings"
	"testing"
)

type loginCheckProvider struct {
	provider.Provider
	result   provider.ExecResult
	commands [][]string
}

func (p *loginCheckProvider) Exec(_ context.Context, _ string, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	p.commands = append(p.commands, argv)
	return p.result, nil
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
