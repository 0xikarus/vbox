package cli

import (
	"context"
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"net/http"
	"strings"
)

type uploadEntry struct {
	application, path, model, choice, target *formField
	saved                                    bool
}

func (a *App) uploadProfilesDialog(ctx context.Context, c config.Context, token string) error {
	if a.IsTerminal == nil || !a.IsTerminal() {
		return fmt.Errorf("interactive upload needs a terminal; use vmbox profiles save APPLICATION NAME --from SOURCE")
	}
	var saved []v1.LoginProfile
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/login-profiles", nil, &saved, nil); err != nil {
		return err
	}
	savedIndex := func(app, name string) int {
		for i, profile := range saved {
			if profile.Application == app && profile.Name == name {
				return i
			}
		}
		return -1
	}
	profiles, err := a.discoverCreationLogins(saved, nil)
	if err != nil {
		return err
	}
	a.addCreationGitHubAccounts(ctx, profiles)
	var fields []*formField
	var entries []*uploadEntry
	var apiEntries []*openCodeAPIUploadEntry
	accountWidth := 7
	for _, p := range profiles {
		for _, path := range p.localPaths {
			accountWidth = min(32, max(accountWidth, len(profileAccountName(p.app, path))))
		}
	}
	for _, p := range profiles {
		for _, label := range p.selection.Choices {
			path := p.localPaths[label]
			if path == "" {
				continue
			}
			choices := []string{"Skip", "Upload"}
			var savedNames []string
			for _, profile := range saved {
				if profile.Application == p.app {
					savedNames = append(savedNames, profile.Name)
				}
			}
			if len(savedNames) > 0 {
				choices = append(choices, "Replace")
			}
			entry := &uploadEntry{application: &formField{Value: p.app}, path: &formField{Value: path}, choice: &formField{Label: p.app + " / " + profileAccountName(p.app, path), Value: "Skip", Choices: choices}}
			entries = append(entries, entry)
			entry.choice.Checkbox = true
			if len(savedNames) > 0 {
				entry.target = &formField{Label: "Replace saved name", Value: savedNames[0], Choices: savedNames}
				entry.target.When = func() bool { return entry.choice.Value == "Replace" && !entry.saved }
			}
			account := profileAccountName(p.app, path)
			entry.choice.TableHeader = fmt.Sprintf("    %-8s %-*s %s", "Agent", accountWidth, "Account", "Source")
			entry.choice.RenderRow = func(width int) string {
				mark := "[ ]"
				if entry.choice.Value == "Upload" || entry.choice.Value == "Replace" {
					mark = "[x]"
					name := account
					if entry.model != nil && strings.TrimSpace(entry.model.Value) != "" {
						name = profileNameWithModel(entry.application.Value, entry.path.Value, entry.model.Value)
					}
					if entry.choice.Value == "Replace" || savedIndex(entry.application.Value, name) >= 0 {
						mark = "[↻]"
					}
				}
				if entry.saved {
					mark = "[✓]"
				}
				return fmt.Sprintf("%s %-8s %-*s %s", mark, entry.application.Value, accountWidth, tuiLabel(account, accountWidth), strings.TrimPrefix(label, "Local: "))
			}
			fields = append(fields, entry.choice)
			if entry.target != nil {
				fields = append(fields, entry.target)
			}
			if p.app == "claude" || p.app == "codex" {
				entry.model = &formField{Label: p.app + " model (optional)", Value: detectedProfileModel(p.app, path)}
				entry.model.When = func() bool {
					return (entry.choice.Value == "Upload" || entry.choice.Value == "Replace") && !entry.saved
				}
				fields = append(fields, entry.model)
			}
		}
	}
	add := &formField{Label: "[ Add entry ]"}
	add.AddFields = func() []*formField {
		entry := &uploadEntry{application: &formField{Label: "Application", Value: "claude", Choices: []string{"claude", "codex", "opencode", "github"}}, path: &formField{Label: "Path / HOST:USER"}, model: &formField{Label: "Agent model (optional)"}, choice: &formField{Label: "Upload entry", Value: "Upload", Choices: []string{"Skip", "Upload"}}}
		entry.model.When = func() bool {
			return (entry.application.Value == "claude" || entry.application.Value == "codex") && entry.choice.Value == "Upload" && !entry.saved
		}
		entry.choice.Checkbox = true
		entries = append(entries, entry)
		return []*formField{entry.application, entry.path, entry.model, entry.choice}
	}
	fields = append(fields, add)
	providers := a.openCodeAPIProviderChoices()
	addAPIKey := &formField{Label: "[ Add OpenCode API key ]"}
	addAPIKey.AddFields = func() []*formField {
		entry := newOpenCodeAPIUploadEntry(providers)
		entry.check.Action = func(progress func(string)) error {
			provider, ok := openCodeAPIProviderByLabel(providers, entry.provider.Value)
			if !ok {
				return fmt.Errorf("choose OpenRouter or Venice for the OpenCode profile")
			}
			key := strings.TrimSpace(entry.key.Value)
			if key == "" {
				return fmt.Errorf("enter the %s API key", provider.Label)
			}
			progress("Checking " + provider.Label + " and loading models…")
			models, err := a.queryOpenCodeAPIModels(ctx, provider, key)
			if err != nil {
				return err
			}
			entry.verifiedInput = openCodeAPIInputFingerprint(provider, key)
			entry.model.Choices = models
			entry.model.Value = models[0]
			progress(fmt.Sprintf("%s key verified · %d tool-capable models", provider.Label, len(models)))
			return nil
		}
		apiEntries = append(apiEntries, entry)
		return []*formField{entry.provider, entry.name, entry.key, entry.check, entry.model, entry.choice}
	}
	fields = append(fields, addAPIKey)
	defer func() {
		for _, entry := range apiEntries {
			entry.key.Value = ""
			entry.verifiedInput = ""
		}
	}()
	savedCount, replacedCount := 0, 0
	err = a.runFormButton(ctx, "Upload profiles · choose Replace for a saved name", "Upload", fields, func(progress func(string)) error {
		for _, entry := range entries {
			if entry.saved || (entry.choice.Value != "Upload" && entry.choice.Value != "Replace") {
				continue
			}
			app, path := entry.application.Value, entry.path.Value
			if strings.TrimSpace(path) == "" {
				return fmt.Errorf("enter a path (Claude/Codex) or HOST:USER (GitHub)")
			}
			model := ""
			if app == "claude" || app == "codex" {
				if entry.model != nil {
					model = strings.TrimSpace(entry.model.Value)
				}
			}
			base := profileAccountName(app, path)
			if model != "" {
				base = profileNameWithModel(app, path, model)
			}
			name := base
			if entry.choice.Value == "Replace" {
				if entry.target == nil || entry.target.Value == "" {
					return fmt.Errorf("choose a saved %s profile to replace", app)
				}
				name = entry.target.Value
			}
			index := savedIndex(app, name)
			if entry.choice.Value == "Replace" && index < 0 {
				return fmt.Errorf("saved %s profile %s is no longer available", app, name)
			}
			verb := "Uploading "
			if index >= 0 {
				verb = "Replacing "
			}
			progress(verb + app + " / " + name + "…")
			profile, err := a.saveLocalLoginProfileWithModel(ctx, c, token, app, name, path, model, index >= 0)
			if err != nil {
				return err
			}
			if index >= 0 {
				saved[index] = profile
				replacedCount++
			} else {
				saved = append(saved, profile)
			}
			entry.saved = true
			entry.choice.Value = "Saved"
			entry.choice.Choices = []string{"Saved"}
			savedCount++
		}
		for _, entry := range apiEntries {
			if entry.saved || entry.choice.Value != "Upload" {
				continue
			}
			provider, ok := openCodeAPIProviderByLabel(providers, entry.provider.Value)
			if !ok {
				return fmt.Errorf("choose OpenRouter or Venice for the OpenCode profile")
			}
			key := strings.TrimSpace(entry.key.Value)
			if key == "" {
				return fmt.Errorf("enter the %s API key", provider.Label)
			}
			verifiedInput := openCodeAPIInputFingerprint(provider, key)
			if entry.verifiedInput != verifiedInput {
				return fmt.Errorf("check the %s API key, then choose a model", provider.Label)
			}
			if !containsString(entry.model.Choices, entry.model.Value) {
				return fmt.Errorf("choose one of the available %s models", provider.Label)
			}
			base := strings.TrimSpace(entry.name.Value)
			if base == "" {
				base = "opencode-" + provider.ID
			}
			name := base
			index := savedIndex("opencode", name)
			verb := "Uploading "
			if index >= 0 {
				verb = "Replacing "
			}
			progress(verb + "opencode / " + name + "…")
			profile, err := a.saveOpenCodeAPIKeyProfile(ctx, c, token, name, provider, key, entry.model.Value, index >= 0)
			if err != nil {
				return err
			}
			if index >= 0 {
				saved[index] = profile
				replacedCount++
			} else {
				saved = append(saved, profile)
			}
			entry.saved = true
			entry.key.Value = ""
			entry.verifiedInput = ""
			entry.provider.Hidden = true
			entry.name.Hidden = true
			entry.key.Hidden = true
			entry.check.Hidden = true
			entry.model.Hidden = true
			entry.choice.Label = "opencode / " + name
			entry.choice.Value = "Saved"
			entry.choice.Choices = []string{"Saved"}
			savedCount++
		}
		if savedCount == 0 {
			return fmt.Errorf("select an entry for Upload, or add a custom entry")
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "Uploaded %d login profile(s), replacing %d saved copy/copies. No box created or refreshed.\n", savedCount, replacedCount)
	return nil
}
