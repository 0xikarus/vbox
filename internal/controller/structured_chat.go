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
	text, err := s.boxMessageNativePrompt(ctx, accountID, task.Agent, message)
	if err != nil {
		return nil, err
	}
	inbound := boxruntime.ChatInbound{ID: message.ID, Text: text, ParentMessageID: message.ParentMessageID, ThreadID: message.ThreadID}
	if s.Store == nil || s.Store.DB == nil {
		return json.Marshal(inbound)
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT i.media_type,i.data
		FROM box_message_images j JOIN run_once_images i ON i.id=j.image_id AND i.account_id=j.account_id
		WHERE j.account_id=$1 AND j.message_id=$2 AND i.media_type LIKE 'image/%' ORDER BY j.ordinal`, accountID, message.ID)
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
	if err := s.pruneStaleUnusedAttachments(ctx, accountID); err != nil {
		return err
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
	ordinal := 0
	for _, image := range images {
		data, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			// Permanently invalid content: skip this image; the text stays.
			continue
		}
		media, err := validateRunOnceImage(data)
		if err != nil || media != image.MediaType {
			continue
		}
		data, media, err = optimizeStoredImage(ctx, data, media)
		if err != nil {
			// Transient processing failure: retry the whole event later.
			return err
		}
		used += int64(len(data))
		if used > maxAccountAttachmentBytes {
			// Quota is transient state too; a later retry may fit.
			return errAccountAttachmentQuota
		}
		ordinal++
		id := uuid()
		if _, err = tx.ExecContext(ctx, `INSERT INTO run_once_images(id,account_id,media_type,data,download_token,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '7 days')`, id, accountID, media, data, rand.Text()+rand.Text()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO box_message_images(message_id,account_id,image_id,ordinal) VALUES($1,$2,$3,$4)`, messageID, accountID, id, ordinal); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// saveContactImages prepares attachment references before the recipient's
// message is created, so native delivery can include the images on its first
// attempt. The same validation and account quota apply as for owner replies.
func (s *Store) saveContactImages(ctx context.Context, accountID string, images []boxruntime.ChatEventImage) ([]v1.BoxMessageImageRef, error) {
	if len(images) > 8 {
		return nil, fmt.Errorf("attach at most 8 images")
	}
	if len(images) == 0 {
		return nil, nil
	}
	if err := s.pruneStaleUnusedAttachments(ctx, accountID); err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var lockedAccount string
	if err := tx.QueryRowContext(ctx, `SELECT id::text FROM accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&lockedAccount); err != nil {
		return nil, err
	}
	var used int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(octet_length(data)),0) FROM run_once_images WHERE account_id=$1`, accountID).Scan(&used); err != nil {
		return nil, err
	}
	refs := make([]v1.BoxMessageImageRef, 0, len(images))
	for index, image := range images {
		data, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			return nil, fmt.Errorf("contact attached invalid image data")
		}
		media, err := validateRunOnceImage(data)
		if err != nil || media != image.MediaType {
			return nil, fmt.Errorf("contact attached invalid image")
		}
		data, media, err = optimizeStoredImage(ctx, data, media)
		if err != nil {
			return nil, err
		}
		used += int64(len(data))
		if used > maxAccountAttachmentBytes {
			return nil, errAccountAttachmentQuota
		}
		id := uuid()
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_once_images(id,account_id,media_type,data,download_token,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '7 days')`, id, accountID, media, data, rand.Text()+rand.Text()); err != nil {
			return nil, err
		}
		refs = append(refs, v1.BoxMessageImageRef{ID: id, Number: index + 1})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return refs, nil
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
// longer resolves to an unanswered message is kept as its own agent message.
// An identical retry of the already delivered answer is idempotent, preventing
// threaded replies from also appearing as duplicate root bubbles. The caller
// acknowledges the event only after the store succeeded; the outbox is
// therefore head-of-line safe even when events arrive after a watcher expired.
func (s *Server) applyChatEvent(ctx context.Context, prov provider.Provider, serviceID, accountID string, task v1.BoxTask, event boxruntime.ChatEvent) (string, bool, error) {
	text := strings.TrimSpace(event.Text)
	switch event.Kind {
	case "reply":
		if text == "" {
			return "", false, fmt.Errorf("empty structured chat reply")
		}
		if event.Captcha != nil {
			text = encodeBoxMessageCaptcha(text, v1.BoxMessageCaptcha{Type: event.Captcha.Type, URL: event.Captcha.URL, SiteKey: event.Captcha.SiteKey})
		}
	case "question":
		if event.Question == nil || strings.TrimSpace(event.Question.Text) == "" || len(event.Question.Choices) == 0 {
			return "", false, fmt.Errorf("invalid structured chat question")
		}
		text = encodeBoxMessageQuestion(v1.BoxMessageQuestion{Text: event.Question.Text, Choices: event.Question.Choices, Multiple: event.Question.Multiple})
	case "contact":
		// An inter-box message is never an answer to the sender's own chat. It is
		// routed into the contact's conversation. Permanent rejections are
		// acknowledged; transient delivery errors leave the event in the outbox.
		messageID, _, err := s.routeContactMessage(ctx, accountID, task, event)
		return messageID, false, err
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
		if answered && existing.State == "delivered" && existing.Text == text {
			// Two drains (or two client retries) may enqueue the same structured
			// reply with different event IDs. Treat an identical answer to the same
			// message as idempotent instead of displaying a second root bubble.
			if err := s.Store.SetBoxTaskIdleForMessage(ctx, accountID, task.ID, target.ID); err != nil {
				return "", false, err
			}
			return target.ID, true, nil
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
			// Storage or quota failures leave the event retryable; invalid
			// content never reaches this path (it is skipped in the store).
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
		if message.Captcha != nil {
			go s.autoSolveCaptcha(accountID, task, prov, message.ID, *message.Captcha, firstChatEventPNG(event.Images))
		}
		return message.ID, false, nil
	}
	// Naming a Codex thread is cosmetic. It must not block an already queued
	// chat reply: a failed naming command otherwise leaves the outbox at its
	// head forever and the user never sees the response.
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
	if replyNotifiesOwner(target) {
		s.pushAgentReply(ctx, accountID, task, text)
	}
	return replyTo, true, nil
}

// Replies to a box contact belong only to the read-only box conversation.
// The account owner is notified only for replies in their own box chat.
func replyNotifiesOwner(target v1.BoxMessage) bool {
	return target.Direction != "box"
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
// conversation through the same path the owner chat uses. Permanent policy or
// state rejections are reported and acknowledged. A transient worker or store
// failure leaves the sender's durable event unacknowledged for later retry.
func (s *Server) routeContactMessage(ctx context.Context, accountID string, task v1.BoxTask, event boxruntime.ChatEvent) (string, string, error) {
	text := strings.TrimSpace(event.Text)
	reject := func(reason string) (string, string, error) {
		_ = s.Store.AppendSystemBoxMessage(ctx, accountID, task.ID, "Contact message rejected: "+reason, "contact-reject:"+event.ID)
		return "", reason, nil
	}
	if text == "" || len(text) > 100_000 {
		return reject("message must contain between 1 and 100000 bytes")
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
		return "", "", fmt.Errorf("contact box owner unavailable: %w", err)
	}
	body := text
	principal := Principal{AccountID: accountID, UserID: ownerID, Role: "owner", Subject: "box:" + task.LogicalBoxID}
	key := "contact:" + event.ID
	if _, existing, found, err := s.Store.DirectBoxMessageByKey(ctx, principal, targetID, key); err == nil && found {
		return existing.ID, "", nil
	} else if err != nil {
		return "", "", fmt.Errorf("contact delivery lookup failed: %w", err)
	}
	images, err := s.Store.saveContactImages(ctx, accountID, event.Images)
	if err != nil {
		return reject(err.Error())
	}
	result, err := s.routeBoxMessage(ctx, principal, targetID, key, v1.DirectBoxMessageRequest{Text: body, Agent: targetAgent, SenderBoxID: task.LogicalBoxID, Images: images})
	if err != nil {
		for _, image := range images {
			_, _ = s.Store.DB.ExecContext(ctx, `DELETE FROM run_once_images i WHERE i.id=$1 AND i.account_id=$2 AND NOT EXISTS (SELECT 1 FROM box_message_images j WHERE j.image_id=i.id AND j.account_id=i.account_id)`, image.ID, accountID)
		}
		return "", "", fmt.Errorf("contact delivery deferred: %w", err)
	}
	_, _ = s.Store.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'box_contact.message','logical_box',$3,jsonb_build_object('sender_box_id',$4::text,'event_id',$5::text))`, accountID, ownerID, targetID, task.LogicalBoxID, event.ID)
	return result.Message.ID, "", nil
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
