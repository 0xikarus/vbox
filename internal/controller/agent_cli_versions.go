package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

// AgentCLIVersions are account defaults for newly created boxes. Empty values
// use the version bundled in the worker image.
type AgentCLIVersions struct {
	Claude   string `json:"claude"`
	Codex    string `json:"codex"`
	OpenCode string `json:"opencode"`
}

func (v AgentCLIVersions) ForAgent(agent string) string {
	switch agent {
	case "claude":
		return v.Claude
	case "codex":
		return v.Codex
	case "opencode":
		return v.OpenCode
	default:
		return ""
	}
}

func (v AgentCLIVersions) validate() error {
	for _, entry := range []struct{ name, version string }{{"Claude Code", v.Claude}, {"Codex CLI", v.Codex}, {"OpenCode", v.OpenCode}} {
		if entry.version != "" && !boxruntime.ValidAgentCLIVersion(entry.version) {
			return fmt.Errorf("%s version must be an exact release version such as 2.1.280", entry.name)
		}
	}
	return nil
}

func (s *Store) AgentCLIVersions(ctx context.Context, accountID string) (AgentCLIVersions, error) {
	var value AgentCLIVersions
	err := s.DB.QueryRowContext(ctx, `SELECT claude_version,codex_version,opencode_version FROM agent_cli_versions WHERE account_id=$1`, accountID).Scan(&value.Claude, &value.Codex, &value.OpenCode)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	return value, err
}

func (s *Server) agentCLIVersionsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	switch r.Method {
	case http.MethodGet:
		value, err := s.Store.AgentCLIVersions(r.Context(), p.AccountID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	case http.MethodPut:
		var value AgentCLIVersions
		if err := decodeJSON(r, &value); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := value.validate(); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		_, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO agent_cli_versions(account_id,claude_version,codex_version,opencode_version)
			VALUES($1,$2,$3,$4) ON CONFLICT(account_id) DO UPDATE SET
			claude_version=excluded.claude_version,codex_version=excluded.codex_version,
			opencode_version=excluded.opencode_version,updated_at=now()`, p.AccountID, value.Claude, value.Codex, value.OpenCode)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	default:
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
	}
}
