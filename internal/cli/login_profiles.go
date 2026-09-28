package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/components"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) pickCreationProfiles(ctx context.Context, c config.Context, token string) ([]v1.LoginProfileRef, error) {
	var saved []v1.LoginProfile
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/login-profiles", nil, &saved, nil); err != nil {
		return nil, err
	}
	var selected []v1.LoginProfileRef
	for _, app := range []string{"claude", "codex", "opencode"} {
		labels := []string{"Skip", "Upload a local profile"}
		var choices []v1.LoginProfile
		for _, profile := range saved {
			if profile.Application == app {
				choices = append(choices, profile)
				labels = append(labels, "Saved: "+profile.Name)
			}
		}
		i, err := a.selectTUI(ctx, app+" login for this box", labels, 0)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			continue
		}
		if i >= 2 {
			selected = append(selected, v1.LoginProfileRef{Application: app, Name: choices[i-2].Name})
			continue
		}
		local, err := components.DiscoverConfigured(a.Environ["HOME"], a.Environ)
		if err != nil {
			return nil, err
		}
		var profiles []components.Profile
		labels = nil
		for _, profile := range local {
			if profile.Component == app {
				profiles = append(profiles, profile)
				labels = append(labels, profile.Directory)
			}
		}
		if len(profiles) == 0 {
			return nil, fmt.Errorf("no local %s profile found; use vmbox profiles save %s NAME --from PATH, then new BOX --profile %s=NAME", app, app, app)
		}
		i, err = a.selectTUI(ctx, "Local "+app+" profile to store encrypted on controller", labels, 0)
		if err != nil {
			return nil, err
		}
		name, err := a.readControllerPrompt(bufio.NewReader(singleByteReader{a.In}), "Saved profile name", "personal")
		if err != nil {
			return nil, err
		}
		confirm, err := a.selectTUI(ctx, "Upload "+app+" profile as "+name+"?", []string{"Confirm", "Cancel"}, 1)
		if err != nil {
			return nil, err
		}
		if confirm != 0 {
			return nil, fmt.Errorf("profile upload cancelled; no box created")
		}
		profile, err := a.saveLocalLoginProfile(ctx, c, token, app, name, profiles[i].Directory)
		if err != nil {
			return nil, err
		}
		selected = append(selected, v1.LoginProfileRef{Application: app, Name: profile.Name})
	}
	return selected, nil
}

func (a *App) controllerLoginProfiles(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) == 1 && args[0] == "upload" {
		return a.uploadProfilesDialog(ctx, c, token)
	}
	asJSON := len(args) > 0 && args[len(args)-1] == "--json"
	if asJSON {
		args = args[:len(args)-1]
	}
	if len(args) == 0 {
		args = []string{"list"}
	}
	if args[0] == "list" && len(args) == 1 {
		var profiles []v1.LoginProfile
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/login-profiles", nil, &profiles, nil); err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(a.Out).Encode(profiles)
		}
		if len(profiles) == 0 {
			fmt.Fprintln(a.Out, "No saved login profiles. Use: vmbox profiles save claude|codex|opencode NAME --from PATH, or profiles save github NAME --from HOST:USER")
		}
		for _, profile := range profiles {
			fmt.Fprintf(a.Out, "%s · %s\n", tuiLabel(profile.Application, 30), tuiLabel(profile.Name, 64))
		}
		return nil
	}
	if len(args) != 5 || args[0] != "save" || args[3] != "--from" {
		return fmt.Errorf("usage: vmbox profiles [list] [--json] | profiles save claude|codex|opencode NAME --from PATH | profiles save github NAME --from HOST:USER [--json]")
	}
	profile, err := a.saveLocalLoginProfile(ctx, c, token, args[1], args[2], args[4])
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(a.Out).Encode(profile)
	}
	fmt.Fprintf(a.Out, "Saved %s · %s on the controller.\n", tuiLabel(profile.Application, 30), tuiLabel(profile.Name, 64))
	return nil
}

func (a *App) saveLocalLoginProfile(ctx context.Context, c config.Context, token, application, name, path string) (v1.LoginProfile, error) {
	return a.saveLocalLoginProfileWithModel(ctx, c, token, application, name, path, "", false)
}

func (a *App) saveLocalLoginProfileWithModel(ctx context.Context, c config.Context, token, application, name, path, model string, replaceExisting bool) (v1.LoginProfile, error) {
	var result v1.LoginProfile
	if application == "github" {
		return a.saveGitHubLoginProfile(ctx, c, token, name, path, replaceExisting)
	}
	if application != "claude" && application != "codex" && application != "opencode" {
		return result, fmt.Errorf("saved profiles support claude, codex or opencode")
	}
	profile, err := components.ProfileAt(application, path)
	if err != nil {
		return result, err
	}
	req := v1.SaveLoginProfileRequest{Files: map[string][]byte{}, ReplaceExisting: replaceExisting}
	defer func() {
		for _, data := range req.Files {
			clear(data)
		}
	}()
	for _, source := range profile.Files {
		info, err := os.Stat(source)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 512*1024 {
			return result, fmt.Errorf("profile file unreadable or exceeds 512 KiB")
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return result, fmt.Errorf("could not read selected profile file")
		}
		req.Files[filepath.Base(source)] = data
	}
	if model != "" {
		if err := applyProfileModel(application, req.Files, model); err != nil {
			return result, err
		}
	}
	total := 0
	for _, data := range req.Files {
		total += len(data)
	}
	if total > 512*1024 {
		return result, fmt.Errorf("profile exceeds 512 KiB")
	}
	_, err = a.request(ctx, c, token, http.MethodPut, "/v1/login-profiles/"+url.PathEscape(application)+"/"+url.PathEscape(name), req, &result, nil)
	return result, err
}
