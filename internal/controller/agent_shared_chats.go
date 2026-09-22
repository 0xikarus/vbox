package controller

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func requireIdempotency(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 128 {
		return "", fmt.Errorf("Idempotency-Key is required")
	}
	return key, nil
}

func (s *Server) agentSharedChatsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	boxID := r.PathValue("id")
	caps, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, boxID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if r.Method == http.MethodGet {
		if err := requireCapability(caps.SharedChats.Discover, "shared_chats.discover"); err != nil {
			writeError(w, 403, err)
			return
		}
		rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT g.id::text,g.name,count(m.logical_box_id),EXISTS(SELECT 1 FROM chat_group_members self WHERE self.account_id=g.account_id AND self.group_id=g.id AND self.logical_box_id=$2) FROM chat_groups g LEFT JOIN chat_group_members m ON m.account_id=g.account_id AND m.group_id=g.id WHERE g.account_id=$1 GROUP BY g.id,g.name,g.updated_at ORDER BY g.updated_at DESC,g.id`, p.AccountID, boxID)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		defer rows.Close()
		values := []map[string]any{}
		for rows.Next() {
			var id, name string
			var count int
			var member bool
			if err := rows.Scan(&id, &name, &count, &member); err != nil {
				writeError(w, 500, err)
				return
			}
			values = append(values, map[string]any{"id": id, "name": name, "memberCount": count, "member": member})
		}
		writeJSON(w, 200, map[string]any{"chats": values})
		return
	}
	if err := requireCapability(caps.SharedChats.Create, "shared_chats.create"); err != nil {
		writeError(w, 403, err)
		return
	}
	key, err := requireIdempotency(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	request.Name, err = validateChatGroupName(request.Name)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var groupID, storedName string
	var reused bool
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(r.Context(), `INSERT INTO chat_groups(id,account_id,created_by,name,idempotency_key) VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING RETURNING id::text`, uuid(), p.AccountID, p.UserID, request.Name, key).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		reused = true
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,name FROM chat_groups WHERE account_id=$1 AND idempotency_key=$2`, p.AccountID, key).Scan(&groupID, &storedName)
	}
	if err != nil {
		writeError(w, 409, err)
		return
	}
	if reused && storedName != request.Name {
		writeError(w, 409, fmt.Errorf("idempotency key was already used with a different shared-chat name"))
		return
	}
	if !reused {
		var agent string
		if err := tx.QueryRowContext(r.Context(), `SELECT default_agent FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, boxID).Scan(&agent); err != nil {
			writeError(w, 409, err)
			return
		}
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO chat_group_members(group_id,account_id,logical_box_id,agent,can_receive,subscription_mode) VALUES($1,$2,$3,$4,true,'every_message')`, groupID, p.AccountID, boxID, agent); err != nil {
			writeError(w, 409, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 409, err)
		return
	}
	if reused {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, 201, map[string]any{"id": groupID, "name": request.Name})
}

func (s *Server) agentSharedChatSubscribeHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if _, err := requireIdempotency(r); err != nil {
		writeError(w, 400, err)
		return
	}
	boxID := r.PathValue("id")
	caps, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, boxID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if err := requireCapability(caps.SharedChats.Subscribe, "shared_chats.subscribe"); err != nil {
		writeError(w, 403, err)
		return
	}
	var request struct {
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	if request.Mode != "following" && request.Mode != "mentions" && request.Mode != "every_message" {
		writeError(w, 400, fmt.Errorf("mode must be following, mentions, or every_message"))
		return
	}
	result, err := s.Store.DB.ExecContext(r.Context(), `UPDATE chat_group_members SET subscription_mode=$4 WHERE account_id=$1 AND group_id=$2 AND logical_box_id=$3`, p.AccountID, r.PathValue("group"), boxID, request.Mode)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		writeError(w, 404, fmt.Errorf("shared chat membership not found"))
		return
	}
	writeJSON(w, 200, map[string]any{"mode": request.Mode})
}

func (s *Server) agentSharedChatInviteHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if _, err := requireIdempotency(r); err != nil {
		writeError(w, 400, err)
		return
	}
	boxID := r.PathValue("id")
	caps, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, boxID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if err := requireCapability(caps.SharedChats.Invite, "shared_chats.invite"); err != nil {
		writeError(w, 403, err)
		return
	}
	var request struct {
		BoxID string `json:"boxId"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	var agent string
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT target.default_agent FROM chat_group_members self JOIN logical_boxes target ON target.account_id=self.account_id AND target.id=$4 LEFT JOIN box_protection protected ON protected.account_id=target.account_id AND protected.box_id=target.id WHERE self.account_id=$1 AND self.group_id=$2 AND self.logical_box_id=$3 AND protected.box_id IS NULL`, p.AccountID, r.PathValue("group"), boxID, request.BoxID).Scan(&agent)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 403, fmt.Errorf("shared chat or target unavailable"))
		return
	}
	if err != nil {
		writeError(w, 409, err)
		return
	}
	_, err = s.Store.DB.ExecContext(r.Context(), `INSERT INTO chat_group_members(group_id,account_id,logical_box_id,agent,can_receive,subscription_mode) VALUES($1,$2,$3,$4,true,'following') ON CONFLICT(group_id,logical_box_id) DO NOTHING`, r.PathValue("group"), p.AccountID, request.BoxID, agent)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"boxId": request.BoxID, "subscriptionMode": "following"})
}

func (s *Server) agentSharedChatMessagesHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	boxID, groupID := r.PathValue("id"), r.PathValue("group")
	caps, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, boxID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if err := requireCapability(caps.SharedChats.Read, "shared_chats.read"); err != nil {
		writeError(w, 403, err)
		return
	}
	var member bool
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM chat_group_members WHERE account_id=$1 AND group_id=$2 AND logical_box_id=$3)`, p.AccountID, groupID, boxID).Scan(&member); err != nil || !member {
		writeError(w, 403, fmt.Errorf("shared chat membership required"))
		return
	}
	if r.Method == http.MethodGet {
		rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id::text,user_id::text,COALESCE(source_box_id::text,''),body,created_at,COALESCE(parent_message_id::text,''),COALESCE(thread_id,id)::text FROM chat_group_messages WHERE account_id=$1 AND group_id=$2 ORDER BY created_at,id LIMIT 500`, p.AccountID, groupID)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		defer rows.Close()
		values := []v1.GroupMessage{}
		for rows.Next() {
			var value v1.GroupMessage
			value.GroupID = groupID
			if err := rows.Scan(&value.ID, &value.UserID, &value.SourceBoxID, &value.Text, &value.CreatedAt, &value.ParentMessageID, &value.ThreadID); err != nil {
				writeError(w, 500, err)
				return
			}
			values = append(values, value)
		}
		writeJSON(w, 200, map[string]any{"messages": values})
		return
	}
	key, err := requireIdempotency(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var request v1.SendGroupMessageRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" || len(request.Text) > 100000 {
		writeError(w, 400, fmt.Errorf("message text is required"))
		return
	}
	id, threadID, parentID := uuid(), "", strings.TrimSpace(request.ParentMessageID)
	threadID = id
	if parentID != "" {
		if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT COALESCE(thread_id,id)::text FROM chat_group_messages WHERE account_id=$1 AND group_id=$2 AND id::text=$3`, p.AccountID, groupID, parentID).Scan(&threadID); err != nil {
			writeError(w, 400, fmt.Errorf("parent message is not in this shared chat"))
			return
		}
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	var created string
	err = tx.QueryRowContext(r.Context(), `INSERT INTO chat_group_messages(id,account_id,group_id,user_id,source_box_id,body,idempotency_key,parent_message_id,thread_id) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,$9) ON CONFLICT(account_id,idempotency_key) DO NOTHING RETURNING id::text`, id, p.AccountID, groupID, p.UserID, boxID, request.Text, key, parentID, threadID).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		var storedText, storedSource string
		if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT id::text,body,COALESCE(source_box_id::text,''),COALESCE(parent_message_id::text,''),COALESCE(thread_id,id)::text FROM chat_group_messages WHERE account_id=$1 AND group_id=$2 AND idempotency_key=$3`, p.AccountID, groupID, key).Scan(&id, &storedText, &storedSource, &parentID, &threadID); err != nil {
			writeError(w, 409, err)
			return
		}
		if storedText != request.Text || storedSource != boxID || parentID != strings.TrimSpace(request.ParentMessageID) {
			writeError(w, 409, fmt.Errorf("idempotency key was already used with a different shared-chat message"))
			return
		}
		w.Header().Set("Idempotency-Replayed", "true")
		writeJSON(w, 200, map[string]any{"id": id, "threadId": threadID, "parentMessageId": parentID})
		return
	}
	if err != nil {
		writeError(w, 409, err)
		return
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT m.logical_box_id::text,b.name,m.subscription_mode FROM chat_group_members m JOIN logical_boxes b ON b.id=m.logical_box_id AND b.account_id=m.account_id WHERE m.account_id=$1 AND m.group_id=$2 AND m.logical_box_id<>$3 AND m.can_receive`, p.AccountID, groupID, boxID)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	for rows.Next() {
		var target, name, mode string
		if err := rows.Scan(&target, &name, &mode); err != nil {
			rows.Close()
			writeError(w, 409, err)
			return
		}
		deliver := mode == "every_message" || (mode == "mentions" && (hasExactMention(request.Text, name) || hasExactMention(request.Text, target)))
		if deliver {
			if _, err := tx.ExecContext(r.Context(), `INSERT INTO chat_group_deliveries(message_id,account_id,logical_box_id,state) VALUES($1,$2,$3,'queued')`, id, p.AccountID, target); err != nil {
				rows.Close()
				writeError(w, 409, err)
				return
			}
		}
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "threadId": threadID, "parentMessageId": parentID})
}

func hasExactMention(text, target string) bool {
	needle := "@" + target
	for start := 0; ; {
		index := strings.Index(text[start:], needle)
		if index < 0 {
			return false
		}
		end := start + index + len(needle)
		if end == len(text) {
			return true
		}
		next, _ := utf8.DecodeRuneInString(text[end:])
		if !unicode.IsLetter(next) && !unicode.IsNumber(next) && next != '_' && next != '-' {
			return true
		}
		start += index + len(needle)
	}
}
