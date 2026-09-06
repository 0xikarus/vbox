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
	application, path, choice *formField
	saved                     bool
}

func (a *App) uploadProfilesDialog(ctx context.Context, c config.Context, token string) error {
	if a.IsTerminal == nil || !a.IsTerminal() {
		return fmt.Errorf("interactive upload needs a terminal; use vmbox profiles save APPLICATION NAME --from SOURCE")
	}
	var saved []v1.LoginProfile
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/login-profiles", nil, &saved, nil); err != nil {
		return err
	}
	profiles, err := a.discoverCreationLogins(saved, nil)
	if err != nil {
		return err
	}
	a.addCreationGitHubAccounts(ctx, profiles)
	var fields []*formField
	var entries []*uploadEntry
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
			entry := &uploadEntry{application: &formField{Value: p.app}, path: &formField{Value: path}, choice: &formField{Label: p.app + " / " + profileAccountName(p.app, path), Value: "Skip", Choices: []string{"Skip", "Upload"}}}
			entries = append(entries, entry)
			entry.choice.Checkbox = true
			account := profileAccountName(p.app, path)
			entry.choice.TableHeader = fmt.Sprintf("    %-8s %-*s %s", "Agent", accountWidth, "Account", "Source")
			entry.choice.RenderRow = func(width int) string {
				mark := "[ ]"
				if entry.choice.Value == "Upload" {
					mark = "[x]"
				}
				if entry.saved {
					mark = "[✓]"
				}
				return fmt.Sprintf("%s %-8s %-*s %s", mark, entry.application.Value, accountWidth, tuiLabel(account, accountWidth), strings.TrimPrefix(label, "Local: "))
			}
			fields = append(fields, entry.choice)
		}
	}
	add := &formField{Label: "[ Add entry ]"}
	add.AddFields = func() []*formField {
		entry := &uploadEntry{application: &formField{Label: "Application", Value: "claude", Choices: []string{"claude", "codex", "github"}}, path: &formField{Label: "Path / HOST:USER"}, choice: &formField{Label: "Upload entry", Value: "Upload", Choices: []string{"Skip", "Upload"}}}
		entry.choice.Checkbox = true
		entries = append(entries, entry)
		return []*formField{entry.application, entry.path, entry.choice}
	}
	fields = append(fields, add)
	savedCount := 0
	err = a.runFormButton(ctx, "Upload profiles · select entries; names use account identity", "Upload", fields, func(progress func(string)) error {
		for _, entry := range entries {
			if entry.saved || entry.choice.Value != "Upload" {
				continue
			}
			app, path := entry.application.Value, entry.path.Value
			if strings.TrimSpace(path) == "" {
				return fmt.Errorf("enter a path (Claude/Codex) or HOST:USER (GitHub)")
			}
			base := profileAccountName(app, path)
			name := base
			for i := 2; ; i++ {
				used := false
				for _, p := range saved {
					if p.Application == app && p.Name == name {
						used = true
						break
					}
				}
				if !used {
					break
				}
				name = fmt.Sprintf("%s-%d", base, i)
			}
			progress("Uploading " + app + " / " + name + "…")
			profile, err := a.saveLocalLoginProfile(ctx, c, token, app, name, path)
			if err != nil {
				return err
			}
			saved = append(saved, profile)
			entry.saved = true
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
	fmt.Fprintf(a.Out, "Uploaded %d login profile(s). No box created.\n", savedCount)
	return nil
}
