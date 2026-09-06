package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

const (
	creationConnect    = "Create and connect"
	creationDetached   = "Leave running"
	creationHibernated = "Leave hibernated"
)

type creationProfileFields struct {
	app                   string
	selection, path, name *formField
	localPaths            map[string]string
}

func (p creationProfileFields) uploadPath() string {
	if p.selection.Value == "Upload local" {
		return p.path.Value
	}
	return p.localPaths[p.selection.Value]
}

func (a *App) createWorkspace(ctx context.Context, c config.Context, token string, request v1.CreateLogicalBoxRequest, mode, startCLI string, dialog, noProfiles, asJSON bool) error {
	var box v1.LogicalBox
	var allocation v1.Allocation
	ambiguous := false
	acceptedSettings := ""
	var fields []*formField
	var profiles []creationProfileFields
	name := &formField{Label: "Name", Value: request.Name}
	disk := &formField{Label: "Disk GiB", Value: strconv.FormatInt(request.DiskGiB, 10)}
	after := &formField{Label: "After creation", Value: mode, Choices: []string{creationConnect, creationDetached, creationHibernated}}
	startup := &formField{Label: "Start CLI (optional)", Value: startCLI}
	providerField := &formField{Label: "Provider"}
	providerChoices := map[string]v1.ProviderCredential{}
	regionFields := map[string]*formField{}
	if dialog {
		var providers []v1.ProviderCredential
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/provider-credentials", nil, &providers, nil); err != nil {
			return err
		}
		if len(providers) == 0 {
			return fmt.Errorf("no controller provider configured; run vmbox providers create first")
		}
		var defaults v1.FleetConfig
		status, err := a.request(ctx, c, token, http.MethodGet, "/v1/controller-defaults", nil, &defaults, nil)
		if err != nil && status != 409 {
			return err
		}
		fields = []*formField{name, providerField}
		for _, p := range providers {
			label := p.Provider + " / " + p.Name
			providerField.Choices = append(providerField.Choices, label)
			providerChoices[label] = p
			if providerField.Value == "" || (p.Provider == defaults.Provider && p.Name == defaults.ProviderCredential) {
				providerField.Value = label
			}
			pc := c
			pc.Provider, pc.ProviderCredential = p.Provider, p.Name
			var locations []v1.LocationPreset
			if _, err := a.request(ctx, pc, token, http.MethodGet, "/v1/locations"+fleetQuery(pc), nil, &locations, nil); err != nil {
				return err
			}
			r := &formField{Label: "Location", Value: "Any available", Choices: []string{"Any available"}, When: func() bool { return providerField.Value == label }}
			for _, location := range locations {
				r.Choices = append(r.Choices, location.ID)
				if location.ID == request.Region || (request.Region == "" && location.ID == c.LocationPresets[p.Provider+"/"+p.Name]) {
					r.Value = location.ID
				}
			}
			if request.Region != "" {
				r.Value = request.Region
			}
			regionFields[label] = r
			fields = append(fields, r)
		}
		fields = append(fields, disk)
		if !noProfiles {
			var saved []v1.LoginProfile
			if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/login-profiles", nil, &saved, nil); err != nil {
				return err
			}
			profiles, err = a.discoverCreationLogins(saved, request.LoginProfiles)
			if err != nil {
				return err
			}
			for _, p := range profiles {
				fields = append(fields, p.selection, p.path, p.name)
			}
		}
		fields = append(fields, startup, after)
	}
	previousProgress := a.creationProgress
	defer func() { a.creationProgress = previousProgress }()
	submit := func(progress func(string)) error {
		a.creationProgress = progress
		if ambiguous {
			return fmt.Errorf("creation outcome unconfirmed; cancel and inspect vmbox boxes status %s before retrying", request.Name)
		}
		if dialog {
			p := providerChoices[providerField.Value]
			request.Provider, request.ProviderCredential = p.Provider, p.Name
			request.Name = name.Value
			size, err := strconv.ParseInt(disk.Value, 10, 64)
			if err != nil {
				return fmt.Errorf("disk must be a whole number of GiB")
			}
			request.DiskGiB = size
			request.Region = regionFields[providerField.Value].Value
			if request.Region == "Any available" {
				request.Region = ""
			}
			mode, startCLI = after.Value, startup.Value
		}
		if err := provider.ValidateName(request.Name); err != nil {
			return err
		}
		if request.DiskGiB < 1 || request.DiskGiB > 1000 {
			return fmt.Errorf("disk must be between 1 and 1000 GiB")
		}
		if mode == creationHibernated && startCLI != "" {
			return fmt.Errorf("a start CLI needs running compute; choose Connect or Leave running")
		}
		if len(startCLI) > 16384 || strings.ContainsRune(startCLI, 0) {
			return fmt.Errorf("start CLI must be at most 16 KiB and contain no NUL")
		}
		// Once creation is accepted, retain the exact request; never create a
		// second box or silently apply edited settings during a retry.
		settings := func() string {
			profileSettings := []string{}
			for _, p := range profiles {
				profileSettings = append(profileSettings, p.app, p.selection.Value)
				if path := p.uploadPath(); path != "" {
					profileSettings = append(profileSettings, path, p.name.Value)
				}
			}
			encoded, _ := json.Marshal([]any{request.Name, request.Provider, request.ProviderCredential, request.Region, request.DiskGiB, mode, startCLI, request.LoginProfiles, profileSettings})
			return string(encoded)
		}
		if acceptedSettings != "" && acceptedSettings != settings() {
			return fmt.Errorf("box already created; restore the submitted settings to continue, or cancel and open %s", box.Name)
		}
		if box.ID == "" {
			if dialog && !noProfiles {
				request.LoginProfiles = nil
				for _, p := range profiles {
					if path := p.uploadPath(); path != "" {
						progress("Uploading selected " + p.app + " profile…")
						profile, err := a.saveLocalLoginProfile(ctx, c, token, p.app, p.name.Value, path)
						if err != nil {
							return err
						}
						p.selection.Value = "Saved: " + profile.Name
						p.selection.Choices = append(p.selection.Choices, p.selection.Value)
					}
					if strings.HasPrefix(p.selection.Value, "Saved: ") {
						request.LoginProfiles = append(request.LoginProfiles, v1.LoginProfileRef{Application: p.app, Name: strings.TrimPrefix(p.selection.Value, "Saved: ")})
					}
				}
			}
			progress("Initializing persistent workspace…")
			status, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes", request, &box, map[string]string{"Idempotency-Key": request.AllocationRequestKey})
			if err != nil {
				if status == 0 || status >= 500 {
					ambiguous = true
				}
				return err
			}
			if box.ID == "" {
				ambiguous = true
				return fmt.Errorf("controller accepted creation without a box identity; inspect before retrying")
			}
			acceptedSettings = settings()
		}
		latest, err := a.waitLogicalBoxCreation(ctx, c, token, box)
		if latest.ID != "" {
			box = latest
		}
		if err != nil {
			return err
		}
		if mode == creationHibernated {
			return nil
		}
		if box.State != v1.LogicalBoxRunning {
			progress("requesting-allocation")
			if allocation.RequestID == "" {
				if _, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/allocate", map[string]string{"leaseOwner": "cli"}, &allocation, map[string]string{"Idempotency-Key": request.AllocationRequestKey + ":open"}); err != nil {
					return err
				}
			}
			allocation, err = a.waitAllocation(ctx, c, token, allocation)
			if err != nil {
				return err
			}
			refreshed, readErr := a.controllerLogicalBox(ctx, c, token, box.ID)
			err = readErr
			if err != nil {
				return err
			}
			box = refreshed
		}
		return nil
	}
	if dialog {
		if err := a.runForm(ctx, "Create box · persistent shell", fields, submit); err != nil {
			return err
		}
	} else {
		if err := submit(func(message string) {
			if !asJSON {
				fmt.Fprintln(a.Err, message)
			}
		}); err != nil {
			return err
		}
	}
	a.creationProgress = previousProgress
	c.Provider, c.ProviderCredential = request.Provider, request.ProviderCredential
	if dialog {
		if err := a.rememberLocation(c, request.Region); err != nil {
			fmt.Fprintln(a.Err, "Location preference could not be saved; box creation succeeded.")
		}
	}
	if mode == creationConnect {
		return a.openInteractiveStartup(ctx, c, token, box, "", "", false, startCLI)
	}
	if mode == creationDetached && startCLI != "" {
		var session struct{ Session string }
		if _, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/sessions/interactive", map[string]any{"agent": "shell", "startCli": startCLI}, &session, nil); err != nil {
			return err
		}
	}
	return a.logicalBoxOutput(box, asJSON)
}
