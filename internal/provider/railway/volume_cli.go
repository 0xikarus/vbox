package railway

import (
	"context"
	"io"

	"github.com/0xikarus/vmbox-service/internal/procexec"
)

// Volume selectors are options of `railway volume`, not of its subcommands.
// Keep them before list/add/delete so the provider remains directory-independent.
func (p *Provider) runVolume(ctx context.Context, serviceID string, stdin io.Reader, args ...string) (procexec.Result, error) {
	argv := []string{"railway", "volume", "--project", p.cfg.ProjectID, "--environment", p.cfg.EnvironmentID}
	if serviceID != "" {
		argv = append(argv, "--service", serviceID)
	}
	argv = append(argv, args...)
	return p.runner.Run(ctx, argv, stdin, nil, nil)
}
