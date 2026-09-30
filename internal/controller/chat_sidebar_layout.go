package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type chatSidebarGroup struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Collapsed bool   `json:"collapsed"`
}

type chatSidebarLayout struct {
	Exists   bool                  `json:"exists"`
	Groups   []chatSidebarGroup    `json:"groups"`
	Members  map[string]string     `json:"members"`
	Mutes    map[string]*time.Time `json:"mutes"`
	Pins     []string              `json:"pins"`
	Sections map[string]bool       `json:"sections"`
}

func validChatSidebarLayout(layout chatSidebarLayout) bool {
	if len(layout.Groups) > 100 || len(layout.Members) > 5000 || len(layout.Mutes) > 5000 || len(layout.Pins) > 5000 || len(layout.Sections) > 3 {
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
	for key := range layout.Mutes {
		if len(key) > 160 || strings.ContainsRune(key, '\x00') {
			return false
		}
		switch {
		case key == "section:pinned", key == "section:boxes":
		case strings.HasPrefix(key, "group:") && ids[strings.TrimPrefix(key, "group:")]:
		case strings.HasPrefix(key, "box:") && len(key) > len("box:"):
		default: // Pair chats are always muted and cannot be overridden.
			return false
		}
	}
	seenPins := make(map[string]bool, len(layout.Pins))
	for _, key := range layout.Pins {
		if len(key) > 160 || seenPins[key] || !(strings.HasPrefix(key, "box:") || strings.HasPrefix(key, "pair:")) {
			return false
		}
		seenPins[key] = true
	}
	for key := range layout.Sections {
		if key != "pinned" && key != "boxes" && key != "pairs" {
			return false
		}
	}
	return true
}

func activeChatMute(mutes map[string]*time.Time, key string, now time.Time) bool {
	until, exists := mutes[key]
	return exists && (until == nil || until.After(now))
}

// isBoxPushMuted uses the account's saved layout, including pin membership,
// so section and group mutes suppress pushes on every browser consistently.
func (s *Server) isBoxPushMuted(ctx context.Context, accountID, boxID string) (bool, error) {
	var membersJSON, mutesJSON, pinsJSON []byte
	err := s.Store.DB.QueryRowContext(ctx, `SELECT members_json,mutes_json,pins_json FROM chat_sidebar_layouts WHERE account_id=$1`, accountID).Scan(&membersJSON, &mutesJSON, &pinsJSON)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var members map[string]string
	var mutes map[string]*time.Time
	var pins []string
	if err := json.Unmarshal(membersJSON, &members); err != nil {
		return false, err
	}
	if err := json.Unmarshal(mutesJSON, &mutes); err != nil {
		return false, err
	}
	if err := json.Unmarshal(pinsJSON, &pins); err != nil {
		return false, err
	}
	key, now := "box:"+boxID, time.Now().UTC()
	if activeChatMute(mutes, key, now) {
		return true, nil
	}
	if groupID := members[key]; groupID != "" {
		return activeChatMute(mutes, "group:"+groupID, now), nil
	}
	for _, pin := range pins {
		if pin == key {
			return activeChatMute(mutes, "section:pinned", now), nil
		}
	}
	return activeChatMute(mutes, "section:boxes", now), nil
}

func (s *Server) chatSidebarLayoutHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		layout := chatSidebarLayout{Groups: []chatSidebarGroup{}, Members: map[string]string{}, Mutes: map[string]*time.Time{}, Pins: []string{}, Sections: map[string]bool{}}
		var groups, members, mutes, pins, sections []byte
		err := s.Store.DB.QueryRowContext(r.Context(), `SELECT groups_json,members_json,mutes_json,pins_json,sections_json FROM chat_sidebar_layouts WHERE account_id=$1`, p.AccountID).Scan(&groups, &members, &mutes, &pins, &sections)
		if err != nil && err != sql.ErrNoRows {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not load chat groups"))
			return
		}
		if err == nil {
			layout.Exists = true
			if json.Unmarshal(groups, &layout.Groups) != nil || json.Unmarshal(members, &layout.Members) != nil || json.Unmarshal(mutes, &layout.Mutes) != nil || json.Unmarshal(pins, &layout.Pins) != nil || json.Unmarshal(sections, &layout.Sections) != nil {
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
	if layout.Mutes == nil {
		layout.Mutes = map[string]*time.Time{}
	}
	if layout.Pins == nil {
		layout.Pins = []string{}
	}
	if layout.Sections == nil {
		layout.Sections = map[string]bool{}
	}
	groups, _ := json.Marshal(layout.Groups)
	members, _ := json.Marshal(layout.Members)
	mutes, _ := json.Marshal(layout.Mutes)
	pins, _ := json.Marshal(layout.Pins)
	sections, _ := json.Marshal(layout.Sections)
	_, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO chat_sidebar_layouts(account_id,groups_json,members_json,mutes_json,pins_json,sections_json) VALUES($1,$2::jsonb,$3::jsonb,$4::jsonb,$5::jsonb,$6::jsonb)
		ON CONFLICT(account_id) DO UPDATE SET groups_json=excluded.groups_json,members_json=excluded.members_json,mutes_json=excluded.mutes_json,pins_json=excluded.pins_json,sections_json=excluded.sections_json,updated_at=now()`, p.AccountID, string(groups), string(members), string(mutes), string(pins), string(sections))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not save chat groups"))
		return
	}
	layout.Exists = true
	writeJSON(w, http.StatusOK, layout)
}
