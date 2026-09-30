package controller

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

type chatSidebarGroup struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Collapsed bool   `json:"collapsed"`
}

type chatSidebarLayout struct {
	Exists  bool               `json:"exists"`
	Groups  []chatSidebarGroup `json:"groups"`
	Members map[string]string  `json:"members"`
}

func validChatSidebarLayout(layout chatSidebarLayout) bool {
	if len(layout.Groups) > 100 || len(layout.Members) > 5000 {
		return false
	}
	ids, names := make(map[string]bool), make(map[string]bool)
	for _, group := range layout.Groups {
		name := strings.TrimSpace(group.Name)
		if len(group.ID) < 1 || len(group.ID) > 64 || strings.TrimSpace(group.ID) != group.ID ||
			name != group.Name || utf8.RuneCountInString(name) > 48 || name == "" ||
			strings.ContainsRune(name, '\x00') || strings.ContainsRune(group.ID, '\x00') ||
			ids[group.ID] || names[strings.ToLower(name)] {
			return false
		}
		ids[group.ID], names[strings.ToLower(name)] = true, true
	}
	for key, groupID := range layout.Members {
		if len(key) > 160 || !ids[groupID] || !(strings.HasPrefix(key, "box:") || strings.HasPrefix(key, "pair:")) {
			return false
		}
	}
	return true
}

func (s *Server) chatSidebarLayoutHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		layout := chatSidebarLayout{Groups: []chatSidebarGroup{}, Members: map[string]string{}}
		var groups, members []byte
		err := s.Store.DB.QueryRowContext(r.Context(), `SELECT groups_json,members_json FROM chat_sidebar_layouts WHERE account_id=$1`, p.AccountID).Scan(&groups, &members)
		if err != nil && err != sql.ErrNoRows {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not load chat groups"))
			return
		}
		if err == nil {
			layout.Exists = true
			if json.Unmarshal(groups, &layout.Groups) != nil || json.Unmarshal(members, &layout.Members) != nil {
				writeError(w, http.StatusInternalServerError, fmt.Errorf("invalid saved chat groups"))
				return
			}
		}
		writeJSON(w, http.StatusOK, layout)
		return
	}
	var layout chatSidebarLayout
	if err := decodeJSON(r, &layout); err != nil || !validChatSidebarLayout(layout) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid chat groups or members"))
		return
	}
	if layout.Groups == nil {
		layout.Groups = []chatSidebarGroup{}
	}
	if layout.Members == nil {
		layout.Members = map[string]string{}
	}
	groups, _ := json.Marshal(layout.Groups)
	members, _ := json.Marshal(layout.Members)
	_, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO chat_sidebar_layouts(account_id,groups_json,members_json) VALUES($1,$2::jsonb,$3::jsonb)
		ON CONFLICT(account_id) DO UPDATE SET groups_json=excluded.groups_json,members_json=excluded.members_json,updated_at=now()`, p.AccountID, string(groups), string(members))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not save chat groups"))
		return
	}
	layout.Exists = true
	writeJSON(w, http.StatusOK, layout)
}
