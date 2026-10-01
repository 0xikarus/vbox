package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// addCreatedBoxToSidebarGroup runs in the box-creation transaction. A child
// inherits the creator's owner-organized sidebar group; an ungrouped creator
// gets a new group containing both boxes. Grouping never grants contact access.
func addCreatedBoxToSidebarGroup(ctx context.Context, tx *sql.Tx, accountID, creatorID, creatorName, childID string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO chat_sidebar_layouts(account_id) VALUES($1) ON CONFLICT(account_id) DO NOTHING`, accountID); err != nil {
		return fmt.Errorf("initialize chat sidebar: %w", err)
	}
	var groupsJSON, membersJSON []byte
	if err := tx.QueryRowContext(ctx, `SELECT groups_json,members_json FROM chat_sidebar_layouts WHERE account_id=$1 FOR UPDATE`, accountID).Scan(&groupsJSON, &membersJSON); err != nil {
		return fmt.Errorf("load chat sidebar: %w", err)
	}
	var layout chatSidebarLayout
	if err := json.Unmarshal(groupsJSON, &layout.Groups); err != nil {
		return fmt.Errorf("decode chat sidebar groups: %w", err)
	}
	if err := json.Unmarshal(membersJSON, &layout.Members); err != nil {
		return fmt.Errorf("decode chat sidebar members: %w", err)
	}
	if err := groupCreatedBox(&layout, creatorID, creatorName, childID); err != nil {
		return err
	}
	groupsJSON, _ = json.Marshal(layout.Groups)
	membersJSON, _ = json.Marshal(layout.Members)
	if _, err := tx.ExecContext(ctx, `UPDATE chat_sidebar_layouts SET groups_json=$2::jsonb,members_json=$3::jsonb,updated_at=now() WHERE account_id=$1`, accountID, string(groupsJSON), string(membersJSON)); err != nil {
		return fmt.Errorf("save chat sidebar group: %w", err)
	}
	return nil
}

func groupCreatedBox(layout *chatSidebarLayout, creatorID, creatorName, childID string) error {
	if layout.Members == nil {
		layout.Members = map[string]string{}
	}
	if !validChatSidebarLayout(*layout) {
		return fmt.Errorf("saved chat sidebar is invalid")
	}
	groupID := layout.Members["box:"+creatorID]
	if groupID == "" {
		groupID = uuid()
		layout.Groups = append(layout.Groups, chatSidebarGroup{ID: groupID, Name: createdBoxGroupName(creatorName, layout.Groups)})
		layout.Members["box:"+creatorID] = groupID
	}
	layout.Members["box:"+childID] = groupID
	if !validChatSidebarLayout(*layout) {
		return fmt.Errorf("chat sidebar group limit reached")
	}
	return nil
}

func createdBoxGroupName(creator string, groups []chatSidebarGroup) string {
	used := make(map[string]bool, len(groups))
	for _, group := range groups {
		used[strings.ToLower(group.Name)] = true
	}
	for number := 1; ; number++ {
		suffix := " team"
		if number > 1 {
			suffix = fmt.Sprintf(" team %d", number)
		}
		prefix := []rune(creator)
		if len(prefix) > 48-len([]rune(suffix)) {
			prefix = prefix[:48-len([]rune(suffix))]
		}
		name := string(prefix) + suffix
		if !used[strings.ToLower(name)] {
			return name
		}
	}
}
