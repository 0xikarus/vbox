package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func (s *Server) agentThreadHistoryHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	threadID := r.URL.Query().Get("threadId")
	if !historyMessageID.MatchString(threadID) {
		writeError(w, 400, fmt.Errorf("threadId must identify a thread"))
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			writeError(w, 400, fmt.Errorf("limit must be between 1 and 100"))
			return
		}
		limit = value
	}
	var before, beforeID any
	if raw := r.URL.Query().Get("before"); raw != "" || r.URL.Query().Get("beforeId") != "" {
		value, err := time.Parse(time.RFC3339Nano, raw)
		id := r.URL.Query().Get("beforeId")
		if err != nil || !historyMessageID.MatchString(id) {
			writeError(w, 400, fmt.Errorf("before and beforeId must identify a message"))
			return
		}
		before, beforeID = value, id
	}
	if chatID := r.URL.Query().Get("chatId"); chatID != "" {
		s.agentSharedThreadHistory(w, r, p, chatID, threadID, before, beforeID, limit)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT m.id::text,m.task_id::text,COALESCE(m.user_id::text,''),m.direction,m.body,m.state,m.created_at,m.updated_at,COALESCE(m.chat_key,''),COALESCE(m.sender_box_id::text,''),COALESCE(m.parent_message_id::text,''),COALESCE(m.thread_id,m.id)::text FROM box_messages m JOIN box_tasks t ON t.id=m.task_id AND t.account_id=m.account_id WHERE m.account_id=$1 AND t.logical_box_id=$2 AND COALESCE(m.thread_id,m.id)=$3 AND ($4::timestamptz IS NULL OR (m.created_at,m.id)<($4::timestamptz,$5::uuid)) ORDER BY m.created_at DESC,m.id DESC LIMIT $6`, p.AccountID, r.PathValue("id"), threadID, before, beforeID, limit+1)
	if err != nil {
		writeError(w, 500, fmt.Errorf("thread history unavailable"))
		return
	}
	defer rows.Close()
	values := []v1.BoxMessage{}
	for rows.Next() {
		value, err := scanBoxMessage(rows)
		if err != nil {
			writeError(w, 500, fmt.Errorf("thread history unavailable"))
			return
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, fmt.Errorf("thread history unavailable"))
		return
	}
	hasMore := len(values) > limit
	if hasMore {
		values = values[:limit]
	}
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
	if err := s.Store.loadBoxMessageImages(r.Context(), p.AccountID, values); err != nil {
		writeError(w, 500, fmt.Errorf("thread images unavailable"))
		return
	}
	result := v1.ThreadHistory{ThreadID: threadID, Messages: values, HasMore: hasMore}
	if hasMore && len(values) > 0 {
		result.NextBefore = values[0].CreatedAt.UTC().Format(time.RFC3339Nano)
		result.NextBeforeID = values[0].ID
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) agentSharedThreadHistory(w http.ResponseWriter, r *http.Request, p Principal, chatID, threadID string, before, beforeID any, limit int) {
	if !historyMessageID.MatchString(chatID) {
		writeError(w, 400, fmt.Errorf("chatId must identify a shared chat"))
		return
	}
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, fmt.Errorf("thread history unavailable"))
		return
	}
	if err := requireCapability(capabilities.SharedChats.Read, "shared_chats.read"); err != nil {
		writeError(w, 403, err)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT m.id::text,m.user_id::text,COALESCE(m.source_box_id::text,''),COALESCE(source.name,''),m.body,m.created_at,COALESCE(m.parent_message_id::text,''),COALESCE(m.thread_id,m.id)::text
		FROM chat_group_messages m JOIN chat_group_members member ON member.account_id=m.account_id AND member.group_id=m.group_id AND member.logical_box_id=$3
		LEFT JOIN logical_boxes source ON source.account_id=m.account_id AND source.id=m.source_box_id
		WHERE m.account_id=$1 AND m.group_id=$2 AND COALESCE(m.thread_id,m.id)=$4 AND ($5::timestamptz IS NULL OR (m.created_at,m.id)<($5::timestamptz,$6::uuid))
		ORDER BY m.created_at DESC,m.id DESC LIMIT $7`, p.AccountID, chatID, r.PathValue("id"), threadID, before, beforeID, limit+1)
	if err != nil {
		writeError(w, 500, fmt.Errorf("thread history unavailable"))
		return
	}
	defer rows.Close()
	values := []v1.GroupMessage{}
	for rows.Next() {
		value := v1.GroupMessage{GroupID: chatID}
		if err := rows.Scan(&value.ID, &value.UserID, &value.SourceBoxID, &value.SourceBoxName, &value.Text, &value.CreatedAt, &value.ParentMessageID, &value.ThreadID); err != nil {
			writeError(w, 500, fmt.Errorf("thread history unavailable"))
			return
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, fmt.Errorf("thread history unavailable"))
		return
	}
	hasMore := len(values) > limit
	if hasMore {
		values = values[:limit]
	}
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
	result := v1.ThreadHistory{ThreadID: threadID, ChatID: chatID, GroupMessages: values, HasMore: hasMore}
	if hasMore && len(values) > 0 {
		result.NextBefore = values[0].CreatedAt.UTC().Format(time.RFC3339Nano)
		result.NextBeforeID = values[0].ID
	}
	writeJSON(w, http.StatusOK, result)
}
