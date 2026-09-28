package cli

import (
	"context"
	"encoding/json"
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (a *App) addCreationGitHubAccounts(ctx context.Context, profiles []creationProfileFields) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	accounts := a.discoverGitHub(ctx)
	for _, p := range profiles {
		if p.app == "github" {
			p.selection.OnSelect = func(value string) {
				_, user, ok := strings.Cut(p.localPaths[value], ":")
				if !ok || user == "" {
					return
				}
				name := user
				for i := 2; ; i++ {
					used := false
					for _, option := range p.selection.Choices {
						if option == "Saved: "+name {
							used = true
							break
						}
					}
					if !used {
						break
					}
					name = fmt.Sprintf("%s-%d", user, i)
				}
				p.name.Value = name
			}
			for _, account := range accounts {
				label := "Local: " + account.User + "@" + account.Host
				if account.Active {
					label += " (active)"
				}
				if _, exists := p.localPaths[label]; exists {
					continue
				}
				p.selection.Choices = append(p.selection.Choices, label)
				p.localPaths[label] = account.Host + ":" + account.User
			}
			p.selection.Label = fmt.Sprintf("github login (%d saved, %d local)", len(p.selection.Choices)-2-len(p.localPaths), len(p.localPaths))
		}
	}
}

func (a *App) saveGitHubLoginProfile(ctx context.Context, c config.Context, token, name, source string, replaceExisting ...bool) (v1.LoginProfile, error) {
	var profile v1.LoginProfile
	host, user, ok := strings.Cut(source, ":")
	if !ok || host == "" || user == "" {
		return profile, fmt.Errorf("GitHub source must be HOST:USER, e.g. github.com:your-user")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := a.Runner.Run(ctx, []string{"gh", "auth", "token", "--hostname", host, "--user", user}, nil, nil, nil)
	defer clear(result.Stdout)
	defer clear(result.Stderr)
	if err != nil || result.ExitCode != 0 {
		return profile, fmt.Errorf("GitHub login unavailable; run gh auth login locally and retry")
	}
	data, err := json.Marshal(loginprofile.GitHub{Host: host, User: user, Token: strings.TrimSpace(string(result.Stdout))})
	if err != nil {
		return profile, fmt.Errorf("could not encode GitHub profile")
	}
	defer clear(data)
	req := v1.SaveLoginProfileRequest{Files: map[string][]byte{"credential.json": data}}
	if len(replaceExisting) > 0 {
		req.ReplaceExisting = replaceExisting[0]
	}
	if err = loginprofile.Validate("github", req.Files, time.Now()); err != nil {
		return profile, err
	}
	_, err = a.request(ctx, c, token, http.MethodPut, "/v1/login-profiles/github/"+url.PathEscape(name), req, &profile, nil)
	return profile, err
}
