package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/modelcatalog"
)

// Query the same Codex binary as boxes, in a disposable resource-capped process.
// Profile credentials travel over stdin, never argv, env, mounts or logs.
func (e *DockerEngine) CodexModels(ctx context.Context, files map[string][]byte) ([]modelcatalog.Choice, error) {
	name := "vbox-catalog-" + Token()[:20]
	payload, err := json.Marshal(map[string][]byte{"auth.json": files["auth.json"], "config.toml": files["config.toml"]})
	if err != nil {
		return nil, fmt.Errorf("invalid Codex profile")
	}
	defer clear(payload)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result, err := e.Runner.Run(cleanup, []string{"docker", "inspect", "--format", `{{index .Config.Labels "dev.vmbox.catalog"}}`, name}, nil, nil, nil)
		if err == nil && result.ExitCode == 0 && strings.TrimSpace(string(result.Stdout)) == e.HubID {
			_, _ = e.Runner.Run(cleanup, []string{"docker", "rm", "-f", name}, nil, nil, nil)
		}
	}()
	argv := []string{"docker", "run", "--rm", "-i", "--name", name, "--label", "dev.vmbox.catalog=" + e.HubID, "--user", "10001:10001", "--cpus", "0.5", "--memory", "256m", "--memory-swap", "256m", "--pids-limit", "64", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,nosuid,size=64m", "--entrypoint", "/usr/local/bin/vmbox-standalone", e.Image, "--profile-models=codex"}
	result, err := e.Runner.Run(ctx, argv, bytes.NewReader(payload), nil, nil)
	defer clear(result.Stdout)
	defer clear(result.Stderr)
	if err != nil || result.ExitCode != 0 {
		return nil, fmt.Errorf("Codex account catalog unavailable")
	}
	var models []modelcatalog.Choice
	if len(result.Stdout) > 8<<20 || json.Unmarshal(result.Stdout, &models) != nil || len(models) == 0 {
		return nil, fmt.Errorf("invalid Codex model catalog")
	}
	return models, nil
}
