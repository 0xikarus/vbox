package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxBoxTags = 20

func normalizeBoxTags(values []string) ([]string, error) {
	if len(values) > maxBoxTags {
		return nil, fmt.Errorf("a box may have at most %d tags", maxBoxTags)
	}
	seen := map[string]bool{}
	tags := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || utf8.RuneCountInString(value) > 32 {
			return nil, fmt.Errorf("each tag must contain between 1 and 32 characters")
		}
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("tags cannot contain control characters")
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, value)
	}
	return tags, nil
}

func (s *Store) BoxTags(ctx context.Context, p Principal, boxRef string) ([]string, error) {
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(metadata->'tags','[]'::jsonb) FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, box.ID).Scan(&raw); err != nil {
		return nil, err
	}
	tags := []string{}
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil, fmt.Errorf("invalid stored box tags")
	}
	return tags, nil
}

func (s *Store) SetBoxTags(ctx context.Context, p Principal, boxRef string, values []string) ([]string, error) {
	if p.Role != "owner" {
		return nil, fmt.Errorf("only an account owner may change box tags")
	}
	tags, err := normalizeBoxTags(values)
	if err != nil {
		return nil, err
	}
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(tags)
	result, err := s.DB.ExecContext(ctx, `UPDATE logical_boxes SET metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{tags}',$3::jsonb,true),updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, box.ID, string(raw))
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return nil, fmt.Errorf("box not found")
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'logical_box.tags','logical_box',$3,jsonb_build_object('tags',$4::jsonb))`, p.AccountID, p.UserID, box.ID, string(raw))
	return tags, err
}

func (s *Server) boxTagsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodGet {
		tags, err := s.Store.BoxTags(r.Context(), p, r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
		return
	}
	var request struct {
		Tags []string `json:"tags"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tags, err := s.Store.SetBoxTags(r.Context(), p, r.PathValue("id"), request.Tags)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Server) agentBoxTagsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	actorID := agentBoxID(p)
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := requireCapability(capabilities.ManageAgentBoxes.Tag, "set_agent_box_tags"); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var request struct {
		Tags []string `json:"tags"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	target := strings.TrimSpace(r.PathValue("box"))
	box, err := s.Store.LogicalBox(r.Context(), ownerPrincipal(p), target)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	protected, err := s.Store.BoxProtection(r.Context(), ownerPrincipal(p), box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if protected {
		writeError(w, http.StatusForbidden, fmt.Errorf("protected boxes cannot be labeled by an agent"))
		return
	}
	tags, err := s.Store.SetBoxTags(r.Context(), ownerPrincipal(p), box.ID, request.Tags)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": box.ID, "name": box.Name, "tags": tags})
}
