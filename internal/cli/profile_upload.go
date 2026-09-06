package cli

import (
	"context"
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

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
	for _, p := range profiles {
		p.selection.List = true
		p.selection.Label = p.app + " login"
		fields = append(fields, p.selection, p.path, p.name)
	}
	savedCount := 0
	err = a.runFormButton(ctx, "Upload login profiles · controller only", "Upload", fields, func(progress func(string)) error {
		selected := false
		for _, p := range profiles {
			path := p.uploadPath()
			if path == "" {
				continue
			}
			selected = true
			progress("Uploading " + p.app + " · " + p.name.Value + "…")
			profile, err := a.saveLocalLoginProfile(ctx, c, token, p.app, p.name.Value, path)
			if err != nil {
				return err
			}
			savedCount++
			// A retry after a later application's failure must not upload this
			// successful immutable profile a second time.
			p.selection.Value = "Saved: " + profile.Name
			p.selection.Choices = append(p.selection.Choices, p.selection.Value)
		}
		if !selected && savedCount == 0 {
			return fmt.Errorf("select a Local login or Custom local path to upload; Saved entries are already on the controller")
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "Uploaded %d login profile(s). No box created.\n", savedCount)
	return nil
}
