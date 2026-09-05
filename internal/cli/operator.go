//go:build vmbox_operator

package cli

import (
	"context"
	"github.com/0xikarus/vmbox-service/internal/config"
)

// Bootstrap is only linked into the separately built operator binary.
func (a *App) Bootstrap(ctx context.Context, args []string) error {
	file, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	c, err := file.Active("")
	if err != nil {
		return err
	}
	return a.provisionController(ctx, file, c, args)
}
