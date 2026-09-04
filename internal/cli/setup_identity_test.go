package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
)

// ghIdentityRunner answers `gh api user --jq QUERY` from a fixed table so that
// identity resolution can be exercised without a GitHub CLI.
type ghIdentityRunner struct {
	answers map[string]string
	calls   [][]string
}

func (r *ghIdentityRunner) Run(_ context.Context, argv []string, _ io.Reader, _, _ io.Writer) (procexec.Result, error) {
	r.calls = append(r.calls, append([]string(nil), argv...))
	query := argv[len(argv)-1]
	value, ok := r.answers[query]
	if !ok {
		return procexec.Result{ExitCode: 1}, nil
	}
	return procexec.Result{Stdout: []byte(value + "\n")}, nil
}

func TestGitHubIdentityUsesTheSelectedAccountOnly(t *testing.T) {
	tests := []struct {
		name               string
		answers            map[string]string
		wantName, wantMail string
	}{
		{
			name:     "the API describes the selected account",
			answers:  map[string]string{".login": "octocat", ".name // .login": "Octo Cat", ".email // empty": "octo@example.invalid"},
			wantName: "Octo Cat", wantMail: "octo@example.invalid",
		},
		{
			name:     "the account hides its email",
			answers:  map[string]string{".login": "octocat", ".name // .login": "Octo Cat"},
			wantName: "Octo Cat", wantMail: "octocat@users.noreply.github.com",
		},
		{
			name:     "the API answers for a different account",
			answers:  map[string]string{".login": "someone-else", ".name // .login": "Someone Else", ".email // empty": "someone@example.invalid"},
			wantName: "octocat", wantMail: "octocat@users.noreply.github.com",
		},
		{
			name:     "the API is unavailable",
			answers:  nil,
			wantName: "octocat", wantMail: "octocat@users.noreply.github.com",
		},
	}
	for _, test := range tests {
		runner := &ghIdentityRunner{answers: test.answers}
		app := &App{Err: &bytes.Buffer{}, Runner: runner}
		name, email := app.githubIdentity(context.Background(),
			config.GitHubCredential{Host: "github.com", User: "octocat", Protocol: "https"})
		if name != test.wantName || email != test.wantMail {
			t.Errorf("%s: name=%q email=%q, want %q/%q", test.name, name, email, test.wantName, test.wantMail)
		}
		for _, call := range runner.calls {
			joined := strings.Join(call, " ")
			if !strings.Contains(joined, "--hostname github.com") {
				t.Errorf("%s: identity lookup did not pin the host: %q", test.name, joined)
			}
		}
	}
}
