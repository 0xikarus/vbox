package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) requestVolumeDeletion(ctx context.Context, c config.Context, token string, box v1.LogicalBox) error {
	status, err := a.request(ctx, c, token, http.MethodDelete, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/volume", map[string]string{"confirmation": box.Name}, nil, nil)
	if err != nil {
		return err
	}
	if status == http.StatusAccepted {
		fmt.Fprintf(a.Err, "%s · deletion queued; continues in background. Check: vmbox status %s\n", tuiLabel(box.Name, 100), tuiLabel(box.Name, 100))
	} else {
		fmt.Fprintf(a.Err, "%s · deletion completed; fleet size unchanged\n", tuiLabel(box.Name, 100))
	}
	return nil
}
