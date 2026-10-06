package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func (s *Store) ComputeSlotServiceIDs(ctx context.Context, accountID, providerName, credential string) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT service_id FROM compute_slots WHERE account_id=$1 AND provider=$2 AND provider_credential=$3 AND service_id IS NOT NULL`, accountID, providerName, credential)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		values[id] = true
	}
	return values, rows.Err()
}

func normalizeChatGroupMembers(ctx context.Context, store *Store, p Principal, members []v1.PutChatGroupMemberRequest) ([]v1.ChatGroupMember, error) {
	if len(members) < 2 || len(members) > 32 {
		return nil, fmt.Errorf("a group must contain between 2 and 32 logical boxes")
	}
	seen := make(map[string]bool)
	values := make([]v1.ChatGroupMember, 0, len(members))
	for _, requested := range members {
		if requested.LogicalBoxID == "" || seen[requested.LogicalBoxID] {
			return nil, fmt.Errorf("group members must contain unique logical box IDs")
		}
		seen[requested.LogicalBoxID] = true
		box, err := store.LogicalBox(ctx, p, requested.LogicalBoxID)
		if err != nil {
			return nil, err
		}
		agent := strings.ToLower(strings.TrimSpace(requested.Agent))
		if agent == "" {
			agent = "claude"
		}
		if !validAgent(agent) {
			return nil, fmt.Errorf("group member agent must be codex, claude, opencode, or shell")
		}
		canReceive := true
		if requested.CanReceive != nil {
			canReceive = *requested.CanReceive
		}
		values = append(values, v1.ChatGroupMember{LogicalBoxID: box.ID, BoxName: box.Name, Agent: agent, CanReceive: canReceive})
	}
	return values, nil
}

func validateChatGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return "", fmt.Errorf("group name must contain between 1 and 100 bytes")
	}
	return name, nil
}

func (s *Store) CreateChatGroup(ctx context.Context, p Principal, request v1.PutChatGroupRequest) (v1.ChatGroup, error) {
	name, err := validateChatGroupName(request.Name)
	if err != nil {
		return v1.ChatGroup{}, err
	}
	members, err := normalizeChatGroupMembers(ctx, s, p, request.Members)
	if err != nil {
		return v1.ChatGroup{}, err
	}
	group := v1.ChatGroup{ID: uuid(), Name: name, Members: members}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return group, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `INSERT INTO chat_groups(id,account_id,created_by,name) VALUES($1,$2,$3,$4) RETURNING created_at,updated_at`, group.ID, p.AccountID, p.UserID, group.Name).Scan(&group.CreatedAt, &group.UpdatedAt); err != nil {
		return group, err
	}
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_group_members(group_id,account_id,logical_box_id,agent,can_receive) VALUES($1,$2,$3,$4,$5)`, group.ID, p.AccountID, member.LogicalBoxID, member.Agent, member.CanReceive); err != nil {
			return group, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'chat_group.create','chat_group',$3,jsonb_build_object('name',$4::text,'members',$5::integer))`, p.AccountID, p.UserID, group.ID, group.Name, len(members)); err != nil {
		return group, err
	}
	return group, tx.Commit()
}

func (s *Store) UpdateChatGroup(ctx context.Context, p Principal, id string, request v1.PutChatGroupRequest) (v1.ChatGroup, error) {
	name, err := validateChatGroupName(request.Name)
	if err != nil {
		return v1.ChatGroup{}, err
	}
	members, err := normalizeChatGroupMembers(ctx, s, p, request.Members)
	if err != nil {
		return v1.ChatGroup{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return v1.ChatGroup{}, err
	}
	defer tx.Rollback()
	var group v1.ChatGroup
	group.ID, group.Name, group.Members = id, name, members
	err = tx.QueryRowContext(ctx, `UPDATE chat_groups SET name=$4,updated_at=now() WHERE account_id=$1 AND id=$2 AND (created_by=$3 OR $5='owner') RETURNING created_at,updated_at`, p.AccountID, id, p.UserID, name, p.Role).Scan(&group.CreatedAt, &group.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return group, fmt.Errorf("chat group not found")
	}
	if err != nil {
		return group, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_group_members WHERE account_id=$1 AND group_id=$2`, p.AccountID, id); err != nil {
		return group, err
	}
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_group_members(group_id,account_id,logical_box_id,agent,can_receive) VALUES($1,$2,$3,$4,$5)`, id, p.AccountID, member.LogicalBoxID, member.Agent, member.CanReceive); err != nil {
			return group, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'chat_group.update','chat_group',$3,jsonb_build_object('name',$4::text,'members',$5::integer))`, p.AccountID, p.UserID, id, name, len(members)); err != nil {
		return group, err
	}
	return group, tx.Commit()
}

func scanChatGroup(scanner interface{ Scan(...any) error }) (v1.ChatGroup, error) {
	var value v1.ChatGroup
	err := scanner.Scan(&value.ID, &value.Name, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

func (s *Store) ChatGroup(ctx context.Context, p Principal, id string) (v1.ChatGroup, error) {
	group, err := scanChatGroup(s.DB.QueryRowContext(ctx, `SELECT id::text,name,created_at,updated_at FROM chat_groups WHERE account_id=$1 AND id=$2 AND (created_by=$3 OR $4='owner')`, p.AccountID, id, p.UserID, p.Role))
	if errors.Is(err, sql.ErrNoRows) {
		return group, fmt.Errorf("chat group not found")
	}
	if err != nil {
		return group, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT m.logical_box_id::text,b.name,m.agent,m.can_receive FROM chat_group_members m JOIN logical_boxes b ON b.id=m.logical_box_id AND b.account_id=m.account_id WHERE m.account_id=$1 AND m.group_id=$2 ORDER BY b.name,b.id`, p.AccountID, id)
	if err != nil {
		return group, err
	}
	defer rows.Close()
	for rows.Next() {
		var member v1.ChatGroupMember
		if err := rows.Scan(&member.LogicalBoxID, &member.BoxName, &member.Agent, &member.CanReceive); err != nil {
			return group, err
		}
		group.Members = append(group.Members, member)
	}
	return group, rows.Err()
}

func (s *Store) ListChatGroups(ctx context.Context, p Principal) ([]v1.ChatGroup, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text FROM chat_groups WHERE account_id=$1 AND (created_by=$2 OR $3='owner') ORDER BY updated_at DESC,name,id`, p.AccountID, p.UserID, p.Role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]v1.ChatGroup, 0, len(ids))
	for _, id := range ids {
		group, err := s.ChatGroup(ctx, p, id)
		if err != nil {
			return nil, err
		}
		values = append(values, group)
	}
	return values, nil
}

func (s *Store) DeleteChatGroup(ctx context.Context, p Principal, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM chat_groups WHERE account_id=$1 AND id=$2 AND (created_by=$3 OR $4='owner')`, p.AccountID, id, p.UserID, p.Role)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("chat group not found")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id) VALUES($1,$2,'chat_group.delete','chat_group',$3)`, p.AccountID, p.UserID, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateGroupMessage(ctx context.Context, p Principal, groupID, idempotency string, request v1.SendGroupMessageRequest) (v1.GroupMessage, bool, error) {
	if idempotency == "" {
		return v1.GroupMessage{}, false, fmt.Errorf("Idempotency-Key is required")
	}
	if strings.TrimSpace(request.Text) == "" || len(request.Text) > 100_000 {
		return v1.GroupMessage{}, false, fmt.Errorf("message must contain between 1 and 100000 bytes")
	}
	group, err := s.ChatGroup(ctx, p, groupID)
	if err != nil {
		return v1.GroupMessage{}, false, err
	}
	members := make(map[string]v1.ChatGroupMember, len(group.Members))
	for _, member := range group.Members {
		members[member.LogicalBoxID] = member
	}
	if request.SourceBoxID != "" {
		if _, ok := members[request.SourceBoxID]; !ok {
			return v1.GroupMessage{}, false, fmt.Errorf("source box is not a member of this group")
		}
	}
	recipients := request.RecipientBoxIDs
	if len(recipients) == 0 {
		for _, member := range group.Members {
			if member.CanReceive && member.LogicalBoxID != request.SourceBoxID {
				recipients = append(recipients, member.LogicalBoxID)
			}
		}
	}
	seen := make(map[string]bool)
	normalized := make([]string, 0, len(recipients))
	for _, id := range recipients {
		member, ok := members[id]
		if !ok || !member.CanReceive {
			return v1.GroupMessage{}, false, fmt.Errorf("recipient box is not an enabled member of this group")
		}
		if id == request.SourceBoxID || seen[id] {
			continue
		}
		seen[id] = true
		normalized = append(normalized, id)
	}
	if len(normalized) == 0 {
		return v1.GroupMessage{}, false, fmt.Errorf("select at least one enabled recipient other than the source")
	}
	message := v1.GroupMessage{ID: uuid(), GroupID: groupID, UserID: p.UserID, SourceBoxID: request.SourceBoxID, Text: request.Text}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return message, false, err
	}
	defer tx.Rollback()
	message.ThreadID = message.ID
	if request.ParentMessageID != "" {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(thread_id,id)::text FROM chat_group_messages WHERE account_id=$1 AND group_id=$2 AND id::text=$3`, p.AccountID, groupID, request.ParentMessageID).Scan(&message.ThreadID); err != nil {
			return message, false, fmt.Errorf("parent message is not in this shared chat")
		}
		message.ParentMessageID = request.ParentMessageID
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO chat_group_messages(id,account_id,group_id,user_id,source_box_id,body,idempotency_key,parent_message_id,thread_id) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,NULLIF($8,'')::uuid,$9) ON CONFLICT(account_id,idempotency_key) DO NOTHING RETURNING created_at`, message.ID, p.AccountID, groupID, p.UserID, request.SourceBoxID, request.Text, idempotency, message.ParentMessageID, message.ThreadID).Scan(&message.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return message, false, rollbackErr
		}
		existing, loadErr := s.groupMessageByIdempotency(ctx, p, idempotency)
		return existing, true, loadErr
	}
	if err != nil {
		return message, false, err
	}
	for _, boxID := range normalized {
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_group_deliveries(message_id,account_id,logical_box_id,state) VALUES($1,$2,$3,'queued')`, message.ID, p.AccountID, boxID); err != nil {
			return message, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'chat_group.message','chat_group_message',$3,jsonb_build_object('group_id',$4::text,'recipients',$5::integer,'source_box_id',NULLIF($6::text,'')))`, p.AccountID, p.UserID, message.ID, groupID, len(normalized), request.SourceBoxID); err != nil {
		return message, false, err
	}
	if err := tx.Commit(); err != nil {
		return message, false, err
	}
	loaded, err := s.GroupMessage(ctx, p, message.ID)
	return loaded, false, err
}

func (s *Store) groupMessageByIdempotency(ctx context.Context, p Principal, key string) (v1.GroupMessage, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT m.id::text FROM chat_group_messages m JOIN chat_groups g ON g.id=m.group_id AND g.account_id=m.account_id WHERE m.account_id=$1 AND m.idempotency_key=$2 AND (g.created_by=$3 OR $4='owner')`, p.AccountID, key, p.UserID, p.Role).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return v1.GroupMessage{}, fmt.Errorf("group message not found")
	}
	if err != nil {
		return v1.GroupMessage{}, err
	}
	return s.GroupMessage(ctx, p, id)
}

func (s *Store) GroupMessage(ctx context.Context, p Principal, id string) (v1.GroupMessage, error) {
	var message v1.GroupMessage
	err := s.DB.QueryRowContext(ctx, `SELECT m.id::text,m.group_id::text,m.user_id::text,COALESCE(m.source_box_id::text,''),COALESCE(source.name,''),m.body,m.created_at,COALESCE(m.parent_message_id::text,''),COALESCE(m.thread_id,m.id)::text FROM chat_group_messages m JOIN chat_groups g ON g.id=m.group_id AND g.account_id=m.account_id LEFT JOIN logical_boxes source ON source.id=m.source_box_id AND source.account_id=m.account_id WHERE m.account_id=$1 AND m.id=$2 AND (g.created_by=$3 OR $4='owner')`, p.AccountID, id, p.UserID, p.Role).Scan(&message.ID, &message.GroupID, &message.UserID, &message.SourceBoxID, &message.SourceBoxName, &message.Text, &message.CreatedAt, &message.ParentMessageID, &message.ThreadID)
	if errors.Is(err, sql.ErrNoRows) {
		return message, fmt.Errorf("group message not found")
	}
	if err != nil {
		return message, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT d.logical_box_id::text,b.name,COALESCE(d.task_id::text,''),COALESCE(d.box_message_id::text,''),COALESCE(bm.state,d.state),COALESCE(d.failure_reason,''),GREATEST(d.updated_at,bm.updated_at) FROM chat_group_deliveries d JOIN logical_boxes b ON b.id=d.logical_box_id AND b.account_id=d.account_id LEFT JOIN box_messages bm ON bm.id=d.box_message_id AND bm.account_id=d.account_id WHERE d.account_id=$1 AND d.message_id=$2 ORDER BY b.name,b.id`, p.AccountID, id)
	if err != nil {
		return message, err
	}
	defer rows.Close()
	for rows.Next() {
		var delivery v1.GroupMessageDelivery
		if err := rows.Scan(&delivery.LogicalBoxID, &delivery.BoxName, &delivery.TaskID, &delivery.BoxMessageID, &delivery.State, &delivery.Failure, &delivery.UpdatedAt); err != nil {
			return message, err
		}
		message.Deliveries = append(message.Deliveries, delivery)
	}
	return message, rows.Err()
}

func (s *Store) ListGroupMessages(ctx context.Context, p Principal, groupID string) ([]v1.GroupMessage, error) {
	if _, err := s.ChatGroup(ctx, p, groupID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text FROM chat_group_messages WHERE account_id=$1 AND group_id=$2 ORDER BY created_at,id LIMIT 500`, p.AccountID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]v1.GroupMessage, 0, len(ids))
	for _, id := range ids {
		message, err := s.GroupMessage(ctx, p, id)
		if err != nil {
			return nil, err
		}
		values = append(values, message)
	}
	return values, nil
}

func (s *Store) SetGroupDelivery(ctx context.Context, accountID, messageID, boxID, taskID, boxMessageID, state, failure string) error {
	switch state {
	case "queued", "dispatching", "delivered", "ambiguous", "failed":
	default:
		return fmt.Errorf("invalid group delivery state %q", state)
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE chat_group_deliveries SET task_id=NULLIF($4,'')::uuid,box_message_id=NULLIF($5,'')::uuid,state=$6,failure_reason=NULLIF($7,''),updated_at=now() WHERE account_id=$1 AND message_id=$2 AND logical_box_id=$3`, accountID, messageID, boxID, taskID, boxMessageID, state, failure)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("group delivery not found")
	}
	return nil
}

func (s *Store) ClaimGroupDelivery(ctx context.Context, accountID, messageID, boxID string) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `UPDATE chat_group_deliveries SET state='dispatching',updated_at=now() WHERE account_id=$1 AND message_id=$2 AND logical_box_id=$3 AND (state='queued' OR (state='dispatching' AND updated_at < now() - interval '2 minutes'))`, accountID, messageID, boxID)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (s *Store) PendingGroupDeliveries(ctx context.Context) ([]pendingGroupDelivery, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT d.account_id::text,m.id::text,d.logical_box_id::text,m.user_id::text,u.role,u.subject FROM chat_group_deliveries d JOIN chat_group_messages m ON m.id=d.message_id AND m.account_id=d.account_id JOIN users u ON u.id=m.user_id AND u.account_id=m.account_id WHERE d.state='queued' OR (d.state='dispatching' AND d.updated_at < now() - interval '2 minutes') ORDER BY m.id,d.logical_box_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []pendingGroupDelivery
	for rows.Next() {
		var value pendingGroupDelivery
		if err := rows.Scan(&value.AccountID, &value.MessageID, &value.BoxID, &value.Principal.UserID, &value.Principal.Role, &value.Principal.Subject); err != nil {
			return nil, err
		}
		value.Principal.AccountID = value.AccountID
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) DirectBoxMessageByKey(ctx context.Context, p Principal, logicalBoxID, key string) (v1.BoxTask, v1.BoxMessage, bool, error) {
	message, err := scanBoxMessage(s.DB.QueryRowContext(ctx, `SELECT m.id::text,m.task_id::text,COALESCE(m.user_id::text,''),m.direction,m.body,m.state,m.created_at,m.updated_at,COALESCE(m.chat_key,''),COALESCE(m.sender_box_id::text,''),COALESCE(m.parent_message_id::text,''),COALESCE(m.thread_id,m.id)::text FROM box_messages m JOIN box_tasks t ON t.id=m.task_id AND t.account_id=m.account_id JOIN logical_boxes b ON b.id=t.logical_box_id AND b.account_id=t.account_id WHERE m.account_id=$1 AND t.logical_box_id=$2 AND m.idempotency_key IN ($3,$4) AND (b.owner_user_id=$5 OR $6='owner') ORDER BY m.created_at LIMIT 1`, p.AccountID, logicalBoxID, key+":task:initial", key+":message", p.UserID, p.Role))
	if errors.Is(err, sql.ErrNoRows) {
		return v1.BoxTask{}, v1.BoxMessage{}, false, nil
	}
	if err != nil {
		return v1.BoxTask{}, v1.BoxMessage{}, false, err
	}
	task, err := s.BoxTask(ctx, p, message.TaskID)
	return task, message, err == nil, err
}

func (s *Store) AppendSystemBoxMessage(ctx context.Context, accountID, taskID, text, key string) error {
	messageID := uuid()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO box_messages(id,account_id,task_id,direction,body,submit,state,idempotency_key,chat_key,thread_id) VALUES($1,$2,$3,'system',$4,false,'delivered',$5,$6,$1) ON CONFLICT(account_id,idempotency_key) DO NOTHING`, messageID, accountID, taskID, text, key, chatMessageKey())
	return err
}
