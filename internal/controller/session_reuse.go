package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

// existingInteractiveSession never installs a runtime or locks the box for
// writing. Both the observed inventory and the current assignment must agree;
// uncertain observations fail closed instead of launching a second agent.
func (s *Server) existingInteractiveSession(ctx context.Context, p Principal, a fleetAssignment, prov provider.Provider) (string, error) {
	if a.Box.State != v1.LogicalBoxRunning {
		return "", fmt.Errorf("resume the box before connecting")
	}
	var preferred string
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(metadata->>'primarySession','') FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, a.Box.ID).Scan(&preferred); err != nil {
		return "", fmt.Errorf("managed session unavailable: %w", err)
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "native-sessions", nativeFence(a)}, provider.ExecOptions{})
	var inv v1.SessionInventory
	if err != nil || result.ExitCode != 0 || json.Unmarshal([]byte(result.Stdout), &inv) != nil || inv.State != "live" || inv.Assignment != nativeFence(a) || inv.Partial {
		return "", fmt.Errorf("managed session inventory unavailable")
	}
	current, err := s.Store.assignment(ctx, p.AccountID, a.Box.ID)
	if err != nil || current.Box.State != v1.LogicalBoxRunning || nativeFence(current) != nativeFence(a) {
		return "", fmt.Errorf("box assignment changed; reconnect")
	}
	return reusableInteractiveSession(inv.Sessions, preferred), nil
}

func reusableInteractiveSession(sessions []v1.Session, preferred string) string {
	selected := ""
	for _, session := range sessions {
		if session.Name == "" || session.Partial || strings.HasPrefix(session.Name, "task-") || session.Name == "vmbox-desktop" || strings.HasPrefix(session.Name, "vmbox-internal-") {
			continue
		}
		if session.Name == preferred {
			return session.Name
		}
		if selected == "" || session.Name < selected {
			selected = session.Name
		}
	}
	return selected
}
