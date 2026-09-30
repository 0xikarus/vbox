package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/components"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (a *App) syncApplicationProfiles(ctx context.Context, p provider.Provider, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("auth requires a box")
	}
	name := args[0]
	setup, err := a.selectAuthentication(ctx, args[1:])
	if err != nil {
		return err
	}
	box, err := p.Inspect(ctx, name)
	if err != nil {
		return err
	}
	if box.State != provider.StateRunning {
		return fmt.Errorf("box %q is not running; start it before syncing credentials", name)
	}
	return a.uploadSelectedAuthentication(ctx, name, setup, func(ctx context.Context, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
		return p.Exec(ctx, name, argv, opts)
	})
}

func (a *App) selectAuthentication(ctx context.Context, args []string) (config.CreationSetup, error) {
	var profileArgs []string
	var github *config.GitHubCredential
	githubDisabled := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--application-profile" || strings.HasPrefix(arg, "--application-profile="):
			profileArgs = append(profileArgs, arg)
			if arg == "--application-profile" {
				i++
				if i >= len(args) {
					return config.CreationSetup{}, fmt.Errorf("--application-profile requires APPLICATION=PATH")
				}
				profileArgs = append(profileArgs, args[i])
			}
		case arg == "--github-credential" || strings.HasPrefix(arg, "--github-credential="):
			if githubDisabled {
				return config.CreationSetup{}, fmt.Errorf("--github-credential cannot be combined with --no-github")
			}
			if github != nil {
				return config.CreationSetup{}, fmt.Errorf("select only one GitHub credential")
			}
			value := strings.TrimPrefix(arg, "--github-credential=")
			if value == arg {
				i++
				if i >= len(args) {
					return config.CreationSetup{}, fmt.Errorf("--github-credential requires HOST:USER[:ssh|https]")
				}
				value = args[i]
			}
			parsed, err := parseGitHubCredential(value)
			if err != nil {
				return config.CreationSetup{}, err
			}
			github = parsed
		case arg == "--no-github":
			if github != nil {
				return config.CreationSetup{}, fmt.Errorf("--no-github cannot be combined with --github-credential")
			}
			githubDisabled = true
		default:
			return config.CreationSetup{}, fmt.Errorf("unknown auth option %q", arg)
		}
	}
	profiles, err := a.selectApplicationProfiles(profileArgs, false)
	if err != nil {
		return config.CreationSetup{}, err
	}
	if github == nil && !githubDisabled {
		github, err = selectActiveGitHubCredential(a.discoverGitHub(ctx))
		if err != nil {
			return config.CreationSetup{}, err
		}
	}
	if len(profiles) == 0 && github == nil {
		return config.CreationSetup{}, fmt.Errorf("no active application profiles or GitHub credential found; use --application-profile APP=PATH or --github-credential HOST:USER[:ssh|https]")
	}
	return config.CreationSetup{Workspace: "/data/workspace", ApplicationProfiles: profiles, GitHub: github}, nil
}

func parseGitHubCredential(value string) (*config.GitHubCredential, error) {
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("--github-credential requires HOST:USER[:ssh|https]")
	}
	protocol := "https"
	if len(parts) == 3 {
		protocol = parts[2]
	}
	if protocol != "ssh" && protocol != "https" {
		return nil, fmt.Errorf("GitHub protocol must be ssh or https")
	}
	return &config.GitHubCredential{Host: parts[0], User: parts[1], Protocol: protocol}, nil
}

func selectActiveGitHubCredential(accounts []githubAccount) (*config.GitHubCredential, error) {
	var selected []githubAccount
	for _, account := range accounts {
		if account.Active {
			selected = append(selected, account)
		}
	}
	if len(selected) == 0 && len(accounts) == 1 {
		selected = accounts
	}
	if len(selected) == 0 {
		return nil, nil
	}
	if len(selected) > 1 {
		return nil, fmt.Errorf("multiple active GitHub credentials found; use --github-credential HOST:USER[:ssh|https]")
	}
	account := selected[0]
	return &config.GitHubCredential{Host: account.Host, User: account.User, Protocol: account.Protocol}, nil
}

func (a *App) selectApplicationProfiles(args []string, required bool) ([]config.ApplicationProfile, error) {
	var selected []config.ApplicationProfile
	seen := make(map[string]bool)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg != "--application-profile" && !strings.HasPrefix(arg, "--application-profile=") {
			return nil, fmt.Errorf("unknown auth option %q", arg)
		}
		value := strings.TrimPrefix(arg, "--application-profile=")
		if value == arg {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--application-profile requires APPLICATION=PATH")
			}
			value = args[i]
		}
		application, path, ok := strings.Cut(value, "=")
		if !ok || application == "" || path == "" {
			return nil, fmt.Errorf("--application-profile requires APPLICATION=PATH")
		}
		if seen[application] {
			return nil, fmt.Errorf("select only one %s profile", application)
		}
		seen[application] = true
		selected = append(selected, config.ApplicationProfile{Application: application, Path: path})
	}
	if len(selected) == 0 {
		profiles, err := components.DiscoverConfigured(a.Environ["HOME"], a.Environ)
		if err != nil {
			return nil, err
		}
		for _, profile := range profiles {
			if profile.Active {
				selected = append(selected, config.ApplicationProfile{Application: profile.Component, Path: profile.Directory})
			}
		}
	}
	if len(selected) == 0 && required {
		return nil, fmt.Errorf("no active application profiles found; use --application-profile APP=PATH")
	}
	return selected, nil
}

func (a *App) uploadSelectedApplicationProfiles(ctx context.Context, name string, selected []config.ApplicationProfile, execute setupExec) error {
	return a.uploadSelectedAuthentication(ctx, name, config.CreationSetup{Workspace: "/data/workspace", ApplicationProfiles: selected}, execute)
}

func (a *App) uploadSelectedAuthentication(ctx context.Context, name string, setup config.CreationSetup, execute setupExec) error {
	for _, profile := range setup.ApplicationProfiles {
		fmt.Fprintf(a.Err, "vbox: selected %s profile %s\n", profile.Application, profile.Path)
	}
	if setup.GitHub != nil {
		fmt.Fprintf(a.Err, "vbox: selected GitHub credential %s@%s (%s)\n", setup.GitHub.User, setup.GitHub.Host, setup.GitHub.Protocol)
	}
	prepared, err := a.prepareSetup(ctx, setup)
	if err != nil {
		return err
	}
	if setup.GitHub != nil && prepared.githubToken == "" {
		return fmt.Errorf("selected GitHub credential %s@%s is unavailable", setup.GitHub.User, setup.GitHub.Host)
	}
	return a.uploadPreparedWith(ctx, name, prepared, execute)
}
