// Package loginprofile validates portable credentials without exposing their contents.
package loginprofile

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type GitHub struct {
	Host  string `json:"host"`
	User  string `json:"user"`
	Token string `json:"token"`
}

func Validate(app string, files map[string][]byte, now time.Time) error {
	switch app {
	case "opencode":
		var providers map[string]struct {
			Type    string `json:"type"`
			Key     string `json:"key"`
			Token   string `json:"token"`
			Access  string `json:"access"`
			Refresh string `json:"refresh"`
			Expires int64  `json:"expires"`
		}
		invalid := fmt.Errorf("OpenCode profile has no usable credentials; run opencode auth login locally and upload again")
		if json.Unmarshal(files["auth.json"], &providers) != nil || len(providers) == 0 {
			return invalid
		}
		for name, credential := range providers {
			if strings.TrimSpace(name) == "" {
				return invalid
			}
			switch credential.Type {
			case "api":
				if strings.TrimSpace(credential.Key) == "" {
					return invalid
				}
			case "oauth":
				if credential.Access == "" || credential.Refresh == "" || credential.Expires <= now.UnixMilli() {
					return invalid
				}
			case "wellknown":
				if credential.Key == "" || credential.Token == "" {
					return invalid
				}
			default:
				return invalid
			}
		}
	case "github":
		var c GitHub
		if json.Unmarshal(files["credential.json"], &c) != nil || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`).MatchString(c.Host) || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`).MatchString(c.User) || strings.TrimSpace(c.Token) == "" {
			return fmt.Errorf("GitHub credential is missing or invalid; run gh auth login and upload again")
		}
	case "claude":
		var c struct {
			OAuth struct {
				Access  string `json:"accessToken"`
				Refresh string `json:"refreshToken"`
				Expiry  int64  `json:"expiresAt"`
			} `json:"claudeAiOauth"`
		}
		if json.Unmarshal(files[".credentials.json"], &c) != nil || strings.TrimSpace(c.OAuth.Access) == "" {
			return fmt.Errorf("Claude profile has no usable access token; run claude auth login locally, then save a new profile name")
		}
		if c.OAuth.Expiry <= now.UnixMilli() && strings.TrimSpace(c.OAuth.Refresh) == "" {
			return fmt.Errorf("Claude profile is expired and has no refresh token; run claude auth login locally, then save a new profile name")
		}
	case "codex":
		var c struct {
			Key    string `json:"OPENAI_API_KEY"`
			Tokens struct {
				Access string `json:"access_token"`
			} `json:"tokens"`
		}
		if json.Unmarshal(files["auth.json"], &c) != nil {
			return fmt.Errorf("Codex profile is invalid; run codex login locally, then save a new profile name")
		}
		if strings.TrimSpace(c.Key) != "" {
			return nil
		}
		parts := strings.Split(c.Tokens.Access, ".")
		if len(parts) != 3 {
			return fmt.Errorf("Codex profile has no usable access token; run codex login locally, then save a new profile name")
		}
		data, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return fmt.Errorf("Codex access token is malformed; run codex login and save a new profile name")
		}
		var claims struct {
			Exp int64 `json:"exp"`
		}
		if json.Unmarshal(data, &claims) != nil || claims.Exp <= now.Unix() {
			return fmt.Errorf("Codex profile is expired or has no expiry; run codex login locally, then save a new profile name")
		}
	default:
		return fmt.Errorf("unsupported login application")
	}
	return nil
}
