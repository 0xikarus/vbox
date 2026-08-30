package cli

import (
	"fmt"

	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func railwayToken(environ map[string]string) (string, string, error) {
	project, account := environ["RAILWAY_TOKEN"], environ["RAILWAY_API_TOKEN"]
	if project != "" && account != "" {
		return "", "", fmt.Errorf("set only one of RAILWAY_TOKEN (project) or RAILWAY_API_TOKEN (account)")
	}
	if project != "" {
		return project, "RAILWAY_TOKEN", nil
	}
	if account != "" {
		return account, "RAILWAY_API_TOKEN", nil
	}
	return "", "", fmt.Errorf("RAILWAY_TOKEN or RAILWAY_API_TOKEN is required")
}

func railwayRunner(token, environment string) procexec.OSRunner {
	other := "RAILWAY_API_TOKEN"
	if environment == other {
		other = "RAILWAY_TOKEN"
	}
	return procexec.OSRunner{Env: map[string]string{environment: token}, Unset: []string{other}}
}
