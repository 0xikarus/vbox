package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) controllerWhoami(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--json") {
		return fmt.Errorf("usage: vmbox whoami [--json]")
	}
	var identity v1.Identity
	status, err := a.request(ctx, c, token, http.MethodGet, "/v1/whoami", nil, &identity, nil)
	if err != nil {
		if status == 404 {
			return fmt.Errorf("controller does not support whoami yet; upgrade the controller")
		}
		return err
	}
	if len(args) == 1 {
		return json.NewEncoder(a.Out).Encode(identity)
	}
	fmt.Fprintf(a.Out, "Account: %s (%s)\nUser:    %s (%s)\nRole:    %s\n", tuiLabel(identity.AccountName, 160), tuiLabel(identity.AccountID, 64), tuiLabel(identity.Subject, 160), tuiLabel(identity.UserID, 64), tuiLabel(identity.Role, 32))
	return nil
}
