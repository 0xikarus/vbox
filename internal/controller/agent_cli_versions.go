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
		if entry.version != "" && entry.version != "latest" && !boxruntime.ValidAgentCLIVersion(entry.version) {
			return fmt.Errorf("%s version must be latest or an exact release version such as 2.1.280", entry.name)
		}
	}
	return nil
}

func (s *Store) checkAgentCLIPackageVersion(ctx context.Context, agent, version string) error {
	if version == "" {
		return nil
	}
	if s.agentCLIPackageVersionCheck != nil {
		return s.agentCLIPackageVersionCheck(ctx, agent, version)
	}
	return checkAgentCLIPackageVersion(ctx, agent, version)
}

// Resolve the account setting before allocating a slot. A latest setting is
// re-read from npm for every new box; the resulting exact version is stored on
// that box, so later npm tag changes cannot alter an existing box.
func (s *Store) resolveAgentCLIVersion(ctx context.Context, agent, setting string) (string, error) {
	if setting == "latest" {
		var resolved string
		var err error
		if s.agentCLILatestResolve != nil {
			resolved, err = s.agentCLILatestResolve(ctx, agent)
		} else {
			resolved, err = resolveLatestAgentCLIPackageVersion(ctx, agent)
		}
		if err != nil {
			return "", err
		}
		if !boxruntime.ValidAgentCLIVersion(resolved) {
			return "", fmt.Errorf("%w (invalid latest version)", errAgentCLIRegistryUnavailable)
		}
		return resolved, nil
	}
	if err := s.checkAgentCLIPackageVersion(ctx, agent, setting); err != nil {
		return "", err
	}
	return setting, nil
}

func (s *Store) agentCLIVersionCatalog(ctx context.Context, agent string) (AgentCLIVersionCatalog, error) {
	if s.agentCLIPackageCatalog != nil {
		return s.agentCLIPackageCatalog(ctx, agent)
	}
	return listAgentCLIPackageVersions(ctx, agent)
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
		for _, entry := range []struct{ agent, version string }{{"claude", value.Claude}, {"codex", value.Codex}, {"opencode", value.OpenCode}} {
			if _, err := s.Store.resolveAgentCLIVersion(r.Context(), entry.agent, entry.version); err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, errAgentCLIRegistryUnavailable) {
					status = http.StatusBadGateway
				}
				writeError(w, status, err)
				return
			}
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

func (s *Server) agentCLIVersionCatalogHandler(w http.ResponseWriter, r *http.Request, _ Principal) {
	agent := r.PathValue("agent")
	if _, err := boxruntime.AgentCLIPackage(agent); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	catalog, err := s.Store.agentCLIVersionCatalog(r.Context(), agent)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}
