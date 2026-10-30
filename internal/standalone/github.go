package standalone

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/0xikarus/vmbox-service/internal/loginprofile"
)

func configureGitHub(ctx context.Context, home string) error {
	file := filepath.Join(home, ".config", "gh", "hosts.yml")
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read imported GitHub profile")
	}
	defer clear(data)
	clean, err := loginprofile.PortableGitHubHosts(data)
	if err != nil {
		return err
	}
	defer clear(clean)
	if err = os.WriteFile(file, clean, 0600); err != nil {
		return fmt.Errorf("cannot prepare imported GitHub profile")
	}
	if err = os.Chmod(file, 0600); err != nil {
		return err
	}
	var hosts map[string]json.RawMessage
	if err = json.Unmarshal(clean, &hosts); err != nil {
		return fmt.Errorf("invalid GitHub profile")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for host := range hosts {
		cmd := exec.CommandContext(ctx, "gh", "auth", "setup-git", "--hostname", host)
		cmd.Env = append(os.Environ(), "HOME="+home, "GH_CONFIG_DIR="+filepath.Dir(file), "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
		if err = cmd.Run(); err != nil {
			return fmt.Errorf("cannot configure GitHub HTTPS authentication; standalone image must include gh")
		}
	}
	return nil
}
