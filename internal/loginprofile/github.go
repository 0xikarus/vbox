package loginprofile

import (
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// PortableGitHubHosts retains only active HTTPS credentials, never host aliases,
// commands, SSH keys or other accounts from the owner's GitHub CLI settings.
func PortableGitHubHosts(data []byte) ([]byte, error) {
	type account struct {
		Token string `yaml:"oauth_token"`
	}
	var hosts map[string]struct {
		User  string             `yaml:"user"`
		Token string             `yaml:"oauth_token"`
		Users map[string]account `yaml:"users"`
	}
	if len(data) > 512<<10 || yaml.Unmarshal(data, &hosts) != nil || len(hosts) == 0 {
		return nil, fmt.Errorf("invalid GitHub hosts.yml")
	}
	out := map[string]map[string]string{}
	for host, entry := range hosts {
		token := entry.Token
		if selected := entry.Users[entry.User]; selected.Token != "" {
			token = selected.Token
		}
		credential := GitHub{Host: host, User: entry.User, Token: token}
		encoded, _ := json.Marshal(credential)
		err := Validate("github", map[string][]byte{"credential.json": encoded}, time.Now())
		clear(encoded)
		if err != nil {
			return nil, fmt.Errorf("GitHub profile needs an active account and portable token; for keyring logins use github=HOST:USER in the CLI")
		}
		out[host] = map[string]string{"user": entry.User, "oauth_token": token, "git_protocol": "https"}
	}
	// JSON is also YAML, and avoids interpolation/quoting of credential values.
	return json.Marshal(out)
}
