package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

// provisionDesktopAgent runs while the caller holds the current logical-box row
// lock. Credential rotation commits with startup; a failed transaction leaves the
// transported credential unusable. A retry installs a fresh one.
func (s *Server) provisionDesktopAgent(ctx context.Context, tx *sql.Tx, p Principal, a fleetAssignment, prov provider.Provider) error {
	status, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "desktop-status", nativeFence(a)}, provider.ExecOptions{Stderr: io.Discard})
	var capability struct {
		Enabled bool `json:"enabled"`
	}
	if err != nil || status.ExitCode != 0 || json.Unmarshal([]byte(status.Stdout), &capability) != nil {
		return fmt.Errorf("desktop capability could not be verified")
	}
	if !capability.Enabled {
		return nil
	}
	u, err := url.Parse(s.PublicURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("desktop agents require a public HTTPS controller URL")
	}
	token, err := issueDesktopAgentToken(ctx, tx, p, a.Box.ID, a.FencingToken)
	if err != nil {
		return err
	}
	config, err := json.Marshal(boxruntime.DesktopAgentConfig{Controller: strings.TrimRight(s.PublicURL, "/"), Assignment: nativeFence(a), Token: token})
	if err != nil {
		return fmt.Errorf("desktop configuration could not be encoded")
	}
	defer clear(config)
	request := boxruntime.SyncRequest{Files: []boxruntime.SyncFile{{Path: "/data/home/.config/vmbox/desktop-agent.json", Mode: "0600", Data: config}}}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("desktop configuration could not be transported")
	}
	defer clear(payload)
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "sync-files"}, provider.ExecOptions{Stdin: bytes.NewReader(payload), Stderr: io.Discard})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != fmt.Sprintf("%x", sha256.Sum256(payload)) {
		return fmt.Errorf("desktop configuration transfer unconfirmed")
	}
	return nil
}

// provisionAssignedDesktopAgent installs a credential for the exact fenced
// assignment before a restored managed agent starts. The transaction commits
// before tmux restoration, so its MCP server can load its tool policy at
// startup even while the box is still attaching.
func (s *Server) provisionAssignedDesktopAgent(ctx context.Context, a fleetAssignment, prov provider.Provider, state string) error {
	if state != "attaching" && state != "running" {
		return fmt.Errorf("invalid desktop provisioning state")
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT owner_user_id::text FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state=$3 AND assignment_generation=$4 AND fencing_token=$5 FOR UPDATE`, a.Box.AccountID, a.Box.ID, state, a.Box.AssignmentGeneration, a.FencingToken).Scan(&owner); err != nil {
		return fmt.Errorf("desktop assignment changed: %w", err)
	}
	p := Principal{AccountID: a.Box.AccountID, UserID: owner, Role: "owner"}
	if err := s.provisionDesktopAgent(ctx, tx, p, a, prov); err != nil {
		return err
	}
	return tx.Commit()
}
