package controller

import (
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type contactConversation struct {
	BoxAID   string    `json:"boxAId"`
	BoxBID   string    `json:"boxBId"`
	BoxAName string    `json:"boxAName"`
	BoxBName string    `json:"boxBName"`
	LastAt   time.Time `json:"lastAt"`
	LastText string    `json:"lastText"`
}

// The owner sees each unordered pair once. This is a read-only transcript of
// direct contact messages, separate from each box's owner conversation.
func (s *Server) contactConversationsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT pair.a::text,pair.b::text,a.name,b.name,pair.created_at,pair.body FROM (
		SELECT DISTINCT ON (LEAST(m.sender_box_id,t.logical_box_id),GREATEST(m.sender_box_id,t.logical_box_id))
			LEAST(m.sender_box_id,t.logical_box_id) AS a,GREATEST(m.sender_box_id,t.logical_box_id) AS b,m.created_at,m.body
		FROM box_messages m JOIN box_tasks t ON t.id=m.task_id AND t.account_id=m.account_id
		WHERE m.account_id=$1 AND m.direction='box' AND m.sender_box_id IS NOT NULL
		ORDER BY LEAST(m.sender_box_id,t.logical_box_id),GREATEST(m.sender_box_id,t.logical_box_id),m.created_at DESC,m.id DESC
	) pair JOIN logical_boxes a ON a.id=pair.a AND a.account_id=$1 JOIN logical_boxes b ON b.id=pair.b AND b.account_id=$1
	ORDER BY pair.created_at DESC LIMIT 200`, p.AccountID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("contact conversations unavailable"))
		return
	}
	defer rows.Close()
	values := []contactConversation{}
	for rows.Next() {
		var value contactConversation
		if err := rows.Scan(&value.BoxAID, &value.BoxBID, &value.BoxAName, &value.BoxBName, &value.LastAt, &value.LastText); err != nil {
			writeError(w, 500, fmt.Errorf("contact conversations unavailable"))
			return
		}
		values = append(values, value)
	}
	if rows.Err() != nil {
		writeError(w, 500, fmt.Errorf("contact conversations unavailable"))
		return
	}
	writeJSON(w, 200, values)
}

type contactConversationMessage struct {
	v1.BoxMessage
	RecipientBoxID string `json:"recipientBoxId"`
}

func (s *Server) contactConversationMessagesHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	a, b := r.PathValue("a"), r.PathValue("b")
	if !historyMessageID.MatchString(a) || !historyMessageID.MatchString(b) || a == b {
		writeError(w, 400, fmt.Errorf("two distinct box IDs required"))
		return
	}
	for _, id := range []string{a, b} {
		if _, err := s.Store.LogicalBox(r.Context(), p, id); err != nil {
			writeError(w, 404, fmt.Errorf("box unavailable"))
			return
		}
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT m.id::text,m.task_id::text,COALESCE(m.user_id::text,''),
		CASE WHEN m.direction='agent' THEN 'box' ELSE m.direction END,m.body,m.state,m.created_at,m.updated_at,COALESCE(m.chat_key,''),
		CASE WHEN m.direction='agent' THEN t.logical_box_id::text ELSE COALESCE(m.sender_box_id::text,'') END,
		COALESCE(m.parent_message_id::text,''),COALESCE(m.thread_id,m.id)::text
		FROM box_messages m JOIN box_tasks t ON t.id=m.task_id AND t.account_id=m.account_id
		LEFT JOIN box_messages parent ON parent.id=m.parent_message_id AND parent.account_id=m.account_id
		WHERE m.account_id=$1 AND (
			(m.direction='box' AND ((m.sender_box_id=$2 AND t.logical_box_id=$3) OR (m.sender_box_id=$3 AND t.logical_box_id=$2)))
			OR (m.direction='agent' AND parent.direction='box' AND ((parent.sender_box_id=$2 AND t.logical_box_id=$3) OR (parent.sender_box_id=$3 AND t.logical_box_id=$2)))
		)
		ORDER BY m.created_at DESC,m.id DESC LIMIT 500`, p.AccountID, a, b)
	if err != nil {
		writeError(w, 500, fmt.Errorf("contact messages unavailable"))
		return
	}
	defer rows.Close()
	values := []v1.BoxMessage{}
	for rows.Next() {
		value, err := scanBoxMessage(rows)
		if err != nil {
			writeError(w, 500, fmt.Errorf("contact messages unavailable"))
			return
		}
		values = append(values, value)
	}
	if rows.Err() != nil {
		writeError(w, 500, fmt.Errorf("contact messages unavailable"))
		return
	}
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
	if err := s.Store.loadBoxMessageImages(r.Context(), p.AccountID, values); err != nil {
		writeError(w, 500, fmt.Errorf("contact images unavailable"))
		return
	}
	result := make([]contactConversationMessage, 0, len(values))
	for _, value := range values {
		recipient := a
		if value.SenderBoxID == a {
			recipient = b
		}
		result = append(result, contactConversationMessage{BoxMessage: value, RecipientBoxID: recipient})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
