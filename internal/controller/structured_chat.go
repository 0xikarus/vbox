package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

const (
	// chatDrainLimit bounds one outbox drain so a chatty agent cannot stall a
	// request; the next poll continues where this one stopped.
	chatDrainLimit = 20
	// chatDrainTasks bounds how many tasks of one box a chat read drains.
	chatDrainTasks = 4
	// chatDrainInterval is the minimum gap between two outbox polls of a task.
	chatDrainInterval = 2 * time.Second
)

func (s *Server) chatInboundPayload(ctx context.Context, accountID string, task v1.BoxTask, message v1.BoxMessage) ([]byte, error) {
	text, err := s.boxMessagePrompt(ctx, accountID, task.Agent, message)
	if err != nil {
		return nil, err
	}
	inbound := boxruntime.ChatInbound{ID: message.ID, Text: text}
	if s.Store == nil || s.Store.DB == nil {
		return json.Marshal(inbound)
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT i.media_type,i.data
		FROM box_message_images j JOIN run_once_images i ON i.id=j.image_id AND i.account_id=j.account_id
		WHERE j.account_id=$1 AND j.message_id=$2 ORDER BY j.ordinal`, accountID, message.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var image boxruntime.ChatEventImage
		var data []byte
		if err := rows.Scan(&image.MediaType, &data); err != nil {
			return nil, err
		}
		image.Data = base64.StdEncoding.EncodeToString(data)
		inbound.Images = append(inbound.Images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(inbound)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func (s *Server) deliverNativeAgentChat(ctx context.Context, prov provider.Provider, serviceID, accountID, command string, task v1.BoxTask, message v1.BoxMessage) (provider.ExecResult, error) {
	payload, err := s.chatInboundPayload(ctx, accountID, task, message)
	if err != nil {
		return provider.ExecResult{}, err
	}
	return prov.Exec(ctx, serviceID, []string{"vmbox-runtime", command, task.Session}, provider.ExecOptions{Stdin: bytes.NewReader(payload)})
}

func (s *Store) attachAgentChatImages(ctx context.Context, accountID, messageID string, images []boxruntime.ChatEventImage) error {
	if len(images) == 0 {
		return nil
	}
	if len(images) > 8 {
		return fmt.Errorf("agent attached too many images")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM box_message_images WHERE account_id=$1 AND message_id=$2`, accountID, messageID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return tx.Commit()
	}
	var used int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(octet_length(data)),0) FROM run_once_images WHERE account_id=$1`, accountID).Scan(&used); err != nil {
		return err
	}
	for index, image := range images {
		data, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			return fmt.Errorf("agent returned invalid image data")
		}
		media, err := validateRunOnceImage(data)
		if err != nil || media != image.MediaType {
			return fmt.Errorf("agent returned invalid image")
		}
		used += int64(len(data))
		if used > maxAccountAttachmentBytes {
			return fmt.Errorf("saved attachments reached the 1 GiB account storage limit")
		}
		id := uuid()
		if _, err = tx.ExecContext(ctx, `INSERT INTO run_once_images(id,account_id,media_type,data,download_token,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '7 days')`, id, accountID, media, data, rand.Text()+rand.Text()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO box_message_images(message_id,account_id,image_id,ordinal) VALUES($1,$2,$3,$4)`, messageID, accountID, id, index+1); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Server) pullChatEvent(ctx context.Context, prov provider.Provider, serviceID string, task v1.BoxTask) (boxruntime.ChatEvent, bool, error) {
	var event boxruntime.ChatEvent
	result, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "chat-pull", task.Session}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) == "" {
		return event, false, err
	}
	if err := json.Unmarshal([]byte(result.Stdout), &event); err != nil {
		return event, false, fmt.Errorf("invalid structured chat reply")
	}
	return event, true, nil
}

func (s *Server) ackChatEvent(ctx context.Context, prov provider.Provider, serviceID, session, eventID string) error {
	ack, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "chat-ack", session, eventID}, provider.ExecOptions{})
	if err != nil {
		return err
	}
	if ack.ExitCode != 0 {
		return fmt.Errorf("structured chat acknowledgement failed")
	}
	return nil
}

// applyChatEvent stores one pooled outbox event. A reply whose reference no
// longer resolves to an unanswered message is kept as its own agent message, so
// a late or repeated delivery is never dropped. The caller acknowledges the
// event only after the store succeeded; the outbox is therefore head-of-line
// safe even when events arrive long after their watcher expired.
func (s *Server) applyChatEvent(ctx context.Context, prov provider.Provider, serviceID, accountID string, task v1.BoxTask, event boxruntime.ChatEvent) (string, bool, error) {
	text := strings.TrimSpace(event.Text)
	switch event.Kind {
	case "reply":
		if text == "" {
			return "", false, fmt.Errorf("empty structured chat reply")
		}
	case "question":
		if event.Question == nil || strings.TrimSpace(event.Question.Text) == "" || len(event.Question.Choices) == 0 {
			return "", false, fmt.Errorf("invalid structured chat question")
		}
		text = encodeBoxMessageQuestion(v1.BoxMessageQuestion{Text: event.Question.Text, Choices: event.Question.Choices, Multiple: event.Question.Multiple})
	case "contact":
		// An inter-box message is never an answer to the sender's own chat. It is
		// routed into the contact's conversation and acknowledged here; a rejection
		// is recorded on the sender's task instead of blocking the outbox.
		return s.routeContactMessage(ctx, accountID, task, event), false, nil
	default:
		return "", false, fmt.Errorf("unknown structured chat event")
	}
	replyTo := ""
	activityMessageID := ""
	target, found, err := s.Store.BoxMessageByChatKey(ctx, accountID, task.ID, event.ReplyTo)
	if err != nil {
		return "", false, err
	}
	if found {
		activityMessageID = target.ID
		existing, answered, err := s.Store.AgentBoxMessage(ctx, accountID, target.ID)
		if err != nil {
			return "", false, err
		}
		if !answered || existing.State != "delivered" {
			replyTo = target.ID
		}
	}
	if replyTo == "" {
		message, _, err := s.Store.InsertAgentBoxMessage(ctx, accountID, task.ID, event.ID, text)
		if err != nil {
			return "", false, err
		}
		if err := s.Store.attachAgentChatImages(ctx, accountID, message.ID, event.Images); err != nil {
			return "", false, err
		}
		var busyErr error
		if activityMessageID != "" {
			busyErr = s.Store.SetBoxTaskIdleForMessage(ctx, accountID, task.ID, activityMessageID)
		} else {
			busyErr = s.Store.SetBoxTaskBusy(ctx, accountID, task.ID, false)
		}
		if busyErr != nil {
			return "", false, busyErr
		}
		s.pushAgentReply(ctx, accountID, task, text)
		return message.ID, false, nil
	}
	if task.Agent == "codex" {
		prepared, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "chat-codex-name", task.Session}, provider.ExecOptions{})
		if err != nil {
			return "", false, err
		}
		if prepared.ExitCode != 0 {
			return "", false, fmt.Errorf("codex chat thread naming failed")
		}
	}
	if _, err := s.Store.UpsertAgentBoxMessage(ctx, accountID, task.ID, replyTo, text, "delivered"); err != nil {
		return "", false, err
	}
	reply, found, err := s.Store.AgentBoxMessage(ctx, accountID, replyTo)
	if err != nil || !found {
		return "", false, err
	}
	if err := s.Store.attachAgentChatImages(ctx, accountID, reply.ID, event.Images); err != nil {
		return "", false, err
	}
	if err := s.Store.SetBoxTaskIdleForMessage(ctx, accountID, task.ID, activityMessageID); err != nil {
		return "", false, err
	}
	s.pushAgentReply(ctx, accountID, task, text)
	return replyTo, true, nil
}

func (s *Server) pullStructuredAgentReply(ctx context.Context, prov provider.Provider, serviceID, accountID string, task v1.BoxTask, request v1.BoxMessage) (bool, error) {
	for drained := 0; drained < chatDrainLimit; drained++ {
		event, found, err := s.pullChatEvent(ctx, prov, serviceID, task)
		if err != nil || !found {
			return false, err
		}
		messageID, _, err := s.applyChatEvent(ctx, prov, serviceID, accountID, task, event)
		if err != nil {
			return false, err
		}
		if err := s.ackChatEvent(ctx, prov, serviceID, task.Session, event.ID); err != nil {
			return false, err
		}
		if messageID == request.ID {
			return true, nil
		}
	}
	return false, nil
}

// drainAgentChat collects every queued agent message for a task. Chat windows
// and the reconciler call it, so agent messages are polled even when no reply
// is being awaited.
func (s *Server) drainAgentChat(ctx context.Context, accountID string, task v1.BoxTask) error {
	if task.Agent == "shell" {
		return nil
	}
	box, err := s.Store.LogicalBox(ctx, taskPrincipal(accountID, task), task.LogicalBoxID)
	if err != nil || box.State != v1.LogicalBoxRunning {
		return err
	}
	assignment, err := s.Store.assignment(ctx, accountID, box.ID)
	if err != nil {
		return err
	}
	prov, err := s.provider(ctx, accountID, box.Provider, box.ProviderCredential)
	if err != nil {
		return err
	}
	var failures []error
	for drained := 0; drained < chatDrainLimit; drained++ {
		event, found, err := s.pullChatEvent(ctx, prov, assignment.Slot.ServiceID, task)
		if err != nil {
			return err
		}
		if !found {
			break
		}
		if _, _, err := s.applyChatEvent(ctx, prov, assignment.Slot.ServiceID, accountID, task, event); err != nil {
			failures = append(failures, err)
			break
		}
		if err := s.ackChatEvent(ctx, prov, assignment.Slot.ServiceID, task.Session, event.ID); err != nil {
			failures = append(failures, err)
			break
		}
	}
	return errors.Join(failures...)
}

// routeContactMessage delivers one inter-box message into the contact's live
// conversation through the same path the owner chat uses, so there is still a
// single native conversation per task. It never returns an error: a rejected or
// undeliverable contact message is recorded on the sender's task and
// acknowledged, so one bad recipient cannot head-of-line block the sender's
// entire chat outbox.
func (s *Server) routeContactMessage(ctx context.Context, accountID string, task v1.BoxTask, event boxruntime.ChatEvent) string {
	text := strings.TrimSpace(event.Text)
	reject := func(reason string) string {
		_ = s.Store.AppendSystemBoxMessage(ctx, accountID, task.ID, "Contact message rejected: "+reason, "contact-reject:"+event.ID)
		return ""
	}
	if text == "" || len(text) > 100_000 {
		return reject("message must contain between 1 and 100000 bytes")
	}
	if len(event.Images) > 0 {
		return reject("contact messages do not support images yet")
	}
	ref := strings.TrimSpace(event.Contact)
	targetID, targetName, targetAgent, targetState, protected, err := s.Store.contactBox(ctx, accountID, ref)
	if err != nil {
		return reject("unknown contact")
	}
	if protected {
		return reject("contact box is protected")
	}
	if targetState != string(v1.LogicalBoxRunning) {
		return reject(targetName + " is " + targetState + "; only a running box can receive a message")
	}
	if err := s.Store.AuthorizeBoxMessage(ctx, accountID, task.LogicalBoxID, targetID); err != nil {
		return reject(err.Error())
	}
	var ownerID string
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT owner_user_id::text FROM logical_boxes WHERE account_id=$1 AND id=$2`, accountID, targetID).Scan(&ownerID); err != nil {
		return reject("contact box owner unavailable")
	}
	senderName, err := s.Store.contactBoxName(ctx, accountID, task.LogicalBoxID)
	if err != nil || strings.TrimSpace(senderName) == "" {
		senderName = task.BoxName
	}
	body := "[From " + senderName + " (" + task.LogicalBoxID + ")]\n\n" + text
	principal := Principal{AccountID: accountID, UserID: ownerID, Role: "owner", Subject: "box:" + task.LogicalBoxID}
	result, err := s.routeBoxMessage(ctx, principal, targetID, "contact:"+event.ID, v1.DirectBoxMessageRequest{Text: body, Agent: targetAgent, SenderBoxID: task.LogicalBoxID})
	if err != nil {
		return reject("delivery failed: " + err.Error())
	}
	_, _ = s.Store.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'box_contact.message','logical_box',$3,jsonb_build_object('sender_box_id',$4::text,'event_id',$5::text))`, accountID, ownerID, targetID, task.LogicalBoxID, event.ID)
	return result.Message.ID
}

// claimChatDrain rate limits outbox polling per task so that an open chat
// window cannot hammer the provider.
func (s *Server) claimChatDrain(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chatDrains == nil {
		s.chatDrains = map[string]time.Time{}
	}
	now := time.Now()
	for existing, at := range s.chatDrains {
		if now.Sub(at) > time.Minute {
			delete(s.chatDrains, existing)
		}
	}
	if at, ok := s.chatDrains[key]; ok && now.Sub(at) < chatDrainInterval {
		return false
	}
	s.chatDrains[key] = now
	return true
}

// drainBoxChat polls the active chat tasks of a box. It is best effort: a
// closed terminal or an unreachable worker must never fail a chat read.
func (s *Server) drainBoxChat(ctx context.Context, p Principal, box v1.LogicalBox) {
	if box.State != v1.LogicalBoxRunning {
		return
	}
	tasks, err := s.Store.ListBoxTasks(ctx, p, box.ID)
	if err != nil {
		return
	}
	claimed := 0
	for _, task := range tasks {
		if claimed >= chatDrainTasks {
			return
		}
		if task.State != "active" || task.Agent == "shell" {
			continue
		}
		if !s.claimChatDrain(p.AccountID + ":" + task.ID) {
			continue
		}
		claimed++
		if err := s.drainAgentChat(ctx, p.AccountID, task); err != nil {
			s.Logger.Warn("agent chat drain failed", "task", task.ID, "error", err)
		}
	}
}
