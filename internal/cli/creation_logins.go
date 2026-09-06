package cli

import (
	"fmt"
	"path/filepath"
	"sort"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/components"
)

func (a *App) discoverCreationLogins(saved []v1.LoginProfile, refs []v1.LoginProfileRef) ([]creationProfileFields, error) {
	local, err := components.DiscoverConfigured(a.Environ["HOME"], a.Environ)
	if err != nil {
		return nil, fmt.Errorf("could not discover local login profiles: %w", err)
	}
	var result []creationProfileFields
	for _, app := range []string{"claude", "codex", "github"} {
		selection := &formField{Value: "Skip", Choices: []string{"Skip", "Upload local"}}
		paths := map[string]string{}
		usedNames := map[string]bool{}
		var savedNames []string
		for _, p := range saved {
			if p.Application == app {
				savedNames = append(savedNames, p.Name)
				usedNames[p.Name] = true
			}
		}
		sort.Strings(savedNames)
		for _, name := range savedNames {
			selection.Choices = append(selection.Choices, "Saved: "+name)
		}
		defaultPath := filepath.Join(a.Environ["HOME"], "."+app)
		if app == "github" {
			defaultPath = "github.com:YOUR-USER"
		}
		for _, p := range local {
			if p.Component != app {
				continue
			}
			// Config-only directories are not logins. Discovery inspects file
			// metadata only; validity/expiry is not inferred from file presence.
			hasAuth := false
			for _, file := range p.Files {
				for _, auth := range components.Registry[app].AuthFiles {
					if filepath.Base(file) == auth {
						hasAuth = true
					}
				}
			}
			if !hasAuth {
				continue
			}
			label := "Local: " + p.Directory
			if p.Active {
				label += " (active)"
				defaultPath = p.Directory
			}
			selection.Choices = append(selection.Choices, label)
			paths[label] = p.Directory
		}
		selection.Label = fmt.Sprintf("%s login (%d saved, %d local)", app, len(savedNames), len(paths))
		for _, ref := range refs {
			if ref.Application == app {
				selection.Value = "Saved: " + ref.Name
			}
		}
		path := &formField{Label: "  Local path", Value: defaultPath, When: func() bool { return selection.Value == "Upload local" }}
		if app == "github" {
			path.Label = "  Account HOST:USER"
		}
		name := "personal"
		for i := 2; usedNames[name]; i++ {
			name = fmt.Sprintf("personal-%d", i)
		}
		profileName := &formField{Label: "  Save as", Value: name, When: func() bool { return selection.Value == "Upload local" || paths[selection.Value] != "" }}
		result = append(result, creationProfileFields{app: app, selection: selection, path: path, name: profileName, localPaths: paths})
	}
	return result, nil
}
