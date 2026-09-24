package controller

import (
	"fmt"
	"net/http"
	"slices"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// Agent-created boxes may use saved profile references, never credential bytes.
// This catalog is scoped to the creator's create grant and contains only choices
// that create_agent_box can accept.
type agentBoxConfigs struct {
	AllowedAgents   []string              `json:"allowedAgents"`
	MaxBoxes        int                   `json:"maxBoxes"`
	MaxDiskGiB      int                   `json:"maxDiskGiB"`
	LoginProfiles   []v1.LoginProfile     `json:"loginProfiles"`
	AssignableRoles []v1.AgentRoleSummary `json:"assignableRoles"`
}

func (s *Server) agentBoxConfigsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, agentBoxID(p))
	if err != nil {
		writeError(w, 500, fmt.Errorf("creation options unavailable"))
		return
	}
	grant := capabilities.CreateAgentBox
	if err := requireCapability(grant.Enabled, "create_agent_box"); err != nil {
		writeError(w, 403, err)
		return
	}
	profiles, err := s.Store.ListLoginProfiles(r.Context(), ownerPrincipal(p))
	if err != nil {
		writeError(w, 500, fmt.Errorf("saved profile options unavailable"))
		return
	}
	profiles = agentBoxProfileOptions(grant.AllowedAgents, profiles)
	if r.PathValue("application") != "" || r.PathValue("name") != "" {
		application, name := r.PathValue("application"), r.PathValue("name")
		if application != "codex" && application != "opencode" {
			writeError(w, 400, fmt.Errorf("live model catalogs are available for Codex and OpenCode profiles only"))
			return
		}
		if !slices.ContainsFunc(profiles, func(profile v1.LoginProfile) bool {
			return profile.Application == application && profile.Name == name
		}) {
			writeError(w, 404, fmt.Errorf("saved profile is not available for this creator"))
			return
		}
		r.SetPathValue("application", application)
		r.SetPathValue("name", name)
		s.getLoginProfileModels(w, r, ownerPrincipal(p))
		return
	}
	roles, err := s.Store.AgentRoles(r.Context(), ownerPrincipal(p))
	if err != nil {
		writeError(w, 500, fmt.Errorf("assignable role options unavailable"))
		return
	}
	assignable := make([]v1.AgentRoleSummary, 0, len(grant.AssignableRoleIDs))
	for _, role := range roles {
		if slices.Contains(grant.AssignableRoleIDs, role.ID) {
			assignable = append(assignable, v1.AgentRoleSummary{ID: role.ID, Name: role.Name})
		}
	}
	writeJSON(w, 200, agentBoxConfigs{
		AllowedAgents:   grant.AllowedAgents,
		MaxBoxes:        grant.MaxBoxes,
		MaxDiskGiB:      min(grant.MaxDiskGiB, 1000),
		LoginProfiles:   profiles,
		AssignableRoles: assignable,
	})
}

func agentBoxProfileOptions(agents []string, profiles []v1.LoginProfile) []v1.LoginProfile {
	allowed := make([]v1.LoginProfile, 0, len(profiles))
	for _, profile := range profiles {
		if profile.Application == "github" || slices.Contains(agents, profile.Application) {
			allowed = append(allowed, profile)
		}
	}
	return allowed
}
