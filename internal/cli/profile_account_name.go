package cli

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Account metadata is a naming hint, not authentication proof. Never display
// tokens or arbitrary fallback JSON values.
func profileAccountName(app, path string) string {
	identity := profileAccountIdentity(app, path)
	var b strings.Builder
	for _, r := range strings.ToLower(identity) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
		if b.Len() >= 48 {
			break
		}
	}
	name := strings.Trim(b.String(), "-._")
	if name == "" {
		return app + "-account"
	}
	return name
}

func profileAccountIdentity(app, path string) string {
	identity := ""
	if app == "github" {
		_, identity, _ = strings.Cut(path, ":")
	} else {
		for _, file := range []string{"auth.json", ".claude.json", ".credentials.json"} {
			name := filepath.Join(path, file)
			info, err := os.Stat(name)
			if err != nil || !info.Mode().IsRegular() || info.Size() > 512*1024 {
				continue
			}
			data, err := os.ReadFile(name)
			if err != nil {
				continue
			}
			var meta struct {
				Email   string `json:"email"`
				Account struct {
					Email string `json:"emailAddress"`
					ID    string `json:"accountUuid"`
				} `json:"oauthAccount"`
				Tokens struct {
					IDToken   string `json:"id_token"`
					AccountID string `json:"account_id"`
				} `json:"tokens"`
			}
			_ = json.Unmarshal(data, &meta)
			clear(data)
			identity = meta.Email
			if identity == "" {
				identity = meta.Account.Email
			}
			if identity == "" && meta.Tokens.IDToken != "" {
				parts := strings.Split(meta.Tokens.IDToken, ".")
				if len(parts) == 3 {
					if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
						var claims struct {
							Email string `json:"email"`
						}
						_ = json.Unmarshal(payload, &claims)
						clear(payload)
						identity = claims.Email
					}
				}
			}
			if identity == "" {
				identity = meta.Account.ID
			}
			if identity == "" {
				identity = meta.Tokens.AccountID
			}
			if identity != "" {
				break
			}
		}
	}
	if identity == "" {
		identity = app + "-" + filepath.Base(filepath.Clean(path))
	}
	return identity
}

func profileNameWithModel(app, path, model string) string {
	value := profileAccountIdentity(app, path) + " (" + strings.TrimSpace(model) + ")"
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@+_.:() -", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
		if b.Len() >= 128 {
			break
		}
	}
	name := strings.Trim(b.String(), " -._")
	if name == "" {
		return profileAccountName(app, path)
	}
	return name
}
