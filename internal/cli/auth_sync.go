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
	selected, err := a.selectApplicationProfiles(args[1:], true)
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
	return a.uploadSelectedApplicationProfiles(ctx, name, selected, func(ctx context.Context, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
		return p.Exec(ctx, name, argv, opts)
	})
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
	for _, profile := range selected {
		fmt.Fprintf(a.Err, "vmbox: selected %s profile %s\n", profile.Application, profile.Path)
	}
	prepared, err := a.prepareSetup(ctx, config.CreationSetup{Workspace: "/data/workspace", ApplicationProfiles: selected})
	if err != nil {
		return err
	}
	return a.uploadPreparedWith(ctx, name, prepared, execute)
}
