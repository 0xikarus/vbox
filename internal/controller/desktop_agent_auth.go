package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// IssueDesktopAgentToken rotates a credential bound to one current assignment.
// Only its hash is stored. Provision the return value directly into the worker's
// private runtime configuration; never return it in chat or agent tool results.
func (s *Store) IssueDesktopAgentToken(ctx context.Context, p Principal, box, fence string) (string, error) {
	return issueDesktopAgentToken(ctx, s.DB, p, box, fence)
}

type desktopTokenExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func issueDesktopAgentToken(ctx context.Context, executor desktopTokenExecutor, p Principal, box, fence string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("desktop credential generation failed")
	}
	defer clear(raw)
	token := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	result, err := executor.ExecContext(ctx, `INSERT INTO desktop_agent_tokens(account_id,box_id,user_id,fencing_token,token_hash)
 SELECT account_id,id,$3,fencing_token,$5 FROM logical_boxes WHERE account_id=$1 AND id=$2 AND fencing_token=$4 AND state IN ('attaching','running')
 ON CONFLICT(box_id) DO UPDATE SET user_id=excluded.user_id,fencing_token=excluded.fencing_token,token_hash=excluded.token_hash,created_at=now()`, p.AccountID, box, p.UserID, fence, hash[:])
	if err != nil {
		return "", fmt.Errorf("desktop credential could not be installed")
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return "", fmt.Errorf("desktop assignment changed")
	}
	return token, nil
}

type desktopAgentScopeKey struct{}
type desktopAgentScope struct {
	Fence     string
	TokenHash [32]byte
}

// desktopAgentAuth deliberately does not use account owner authentication. This
// credential authorizes only explicitly registered agent-desktop routes and the
// box identity comes from the database, never from a caller-supplied parameter.
func (s *Server) desktopAgentAuth(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		value, ok := authorizationValue(r.Header.Get("Authorization"), "DesktopAgent")
		if !ok || len(value) != 64 {
			writeError(w, 401, fmt.Errorf("desktop agent authorization required"))
			return
		}
		if _, err := hex.DecodeString(value); err != nil {
			writeError(w, 401, fmt.Errorf("desktop agent authorization required"))
			return
		}
		hash := sha256.Sum256([]byte(value))
		var p Principal
		var box, fence string
		// During allocation, the managed agent needs its tool inventory before
		// the box becomes ready. Only that read-only policy route is available
		// while attaching; every tool call still requires the running state.
		err := s.Store.DB.QueryRowContext(r.Context(), `SELECT t.account_id::text,t.user_id::text,t.box_id::text,t.fencing_token FROM desktop_agent_tokens t JOIN logical_boxes b ON b.id=t.box_id AND b.account_id=t.account_id JOIN users u ON u.id=t.user_id AND u.account_id=t.account_id WHERE t.token_hash=$1 AND (b.state='running' OR (b.state='attaching' AND $2='/v1/agent-desktop/tool-policy')) AND b.fencing_token=t.fencing_token AND u.disabled_at IS NULL`, hash[:], r.URL.Path).Scan(&p.AccountID, &p.UserID, &box, &fence)
		if err != nil || strings.TrimSpace(box) == "" {
			writeError(w, 401, fmt.Errorf("desktop assignment authorization expired"))
			return
		}
		p.Role = "desktop-agent"
		p.Subject = "desktop-box:" + box
		r.SetPathValue("id", box)
		scope := desktopAgentScope{Fence: fence, TokenHash: hash}
		next(w, r.WithContext(context.WithValue(r.Context(), desktopAgentScopeKey{}, scope)), p)
	}
}
