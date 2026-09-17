package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
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
		if used > 256<<20 {
			return fmt.Errorf("saved images reached the 256 MiB account storage limit")
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

func (s *Server) pullStructuredAgentReply(ctx context.Context, prov provider.Provider, serviceID, accountID string, task v1.BoxTask, request v1.BoxMessage) (bool, error) {
	result, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "chat-pull", task.Session}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) == "" {
		return false, err
	}
	var event boxruntime.ChatEvent
	if err := json.Unmarshal([]byte(result.Stdout), &event); err != nil {
		return false, fmt.Errorf("invalid structured chat reply")
	}
	if event.ReplyTo != request.ID {
		return false, nil
	}
	text := strings.TrimSpace(event.Text)
	switch event.Kind {
	case "reply":
		if text == "" {
			return false, fmt.Errorf("empty structured chat reply")
		}
	case "question":
		if event.Question == nil || strings.TrimSpace(event.Question.Text) == "" || len(event.Question.Choices) == 0 {
			return false, fmt.Errorf("invalid structured chat question")
		}
		text = encodeBoxMessageQuestion(v1.BoxMessageQuestion{Text: event.Question.Text, Choices: event.Question.Choices, Multiple: event.Question.Multiple})
	default:
		return false, fmt.Errorf("unknown structured chat event")
	}
	if task.Agent == "codex" {
		prepared, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "chat-codex-name", task.Session}, provider.ExecOptions{})
		if err != nil {
			return false, err
		}
		if prepared.ExitCode != 0 {
			return false, fmt.Errorf("codex chat thread naming failed")
		}
	}
	if _, err := s.Store.UpsertAgentBoxMessage(ctx, accountID, task.ID, request.ID, text, "delivered"); err != nil {
		return false, err
	}
	reply, found, err := s.Store.AgentBoxMessage(ctx, accountID, request.ID)
	if err != nil || !found {
		return false, err
	}
	if err := s.Store.attachAgentChatImages(ctx, accountID, reply.ID, event.Images); err != nil {
		return false, err
	}
	ack, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "chat-ack", task.Session, event.ID}, provider.ExecOptions{})
	if err != nil {
		return false, err
	}
	if ack.ExitCode != 0 {
		return false, fmt.Errorf("structured chat acknowledgement failed")
	}
	s.pushAgentReply(ctx, accountID, task, text)
	return true, nil
}
