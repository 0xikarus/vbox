package controller

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// Status notices are durable until an interactive task is running again. They
// are not a wake signal and do not depend on an inbox subscription.
func (s *Server) notifyBoxMailStatus(accountID, boxID, outboxID string, to []string, subject, status, reason string) {
	if s.Store == nil || s.Store.DB == nil {
		return
	}
	mail := v1.BoxMessageMail{Kind: "outbox_status", OutboxID: outboxID, To: strings.Join(to, ", "), Subject: subject, Status: status, Reason: reason}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := s.Store.DB.ExecContext(ctx, `INSERT INTO mail_notice_queue(id,account_id,box_id,notice_key,body) VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,notice_key) DO NOTHING`, uuid(), accountID, boxID, "outbox:"+outboxID+":"+status, encodeBoxMessageMail(mail))
	if err != nil {
		s.Logger.Warn("mail status notice could not queue", "box", boxID, "error", err)
	}
}

// ReconcileMailNoticesNow stores at most one batch of 20 inbox items per box
// after a 30-second collection window, and forwards durable status notices.
// Only running boxes with an active interactive task are considered.
func (s *Server) ReconcileMailNoticesNow(ctx context.Context) error {
	if mailDomain() == "" || s.Store == nil || s.Store.DB == nil {
		return nil
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT m.account_id::text,m.box_id::text FROM box_mail_settings m JOIN logical_boxes b ON b.id=m.box_id AND b.account_id=m.account_id WHERE m.enabled AND b.state='running' AND (m.subscribed OR EXISTS(SELECT 1 FROM mail_notice_queue n WHERE n.account_id=m.account_id AND n.box_id=m.box_id AND n.delivered_at IS NULL)) ORDER BY m.updated_at LIMIT 200`)
	if err != nil {
		return err
	}
	type target struct{ account, box string }
	var targets []target
	for rows.Next() {
		var item target
		if err := rows.Scan(&item.account, &item.box); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range targets {
		if err := s.reconcileBoxMailNotices(ctx, item.account, item.box); err != nil {
			return fmt.Errorf("mail notice for box %s: %w", item.box, err)
		}
	}
	return nil
}

func (s *Server) reconcileBoxMailNotices(ctx context.Context, accountID, boxID string) error {
	// Resolve policy before opening a transaction; small connection pools may
	// otherwise wait on the transaction's own connection.
	tools, err := s.Store.EffectiveAgentToolNames(ctx, accountID, boxID)
	if err != nil {
		return err
	}
	// A row lock prevents two controllers from claiming the same mail batch.
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var subscribed bool
	var senderFilter, subjectFilter string
	err = tx.QueryRowContext(ctx, `SELECT m.subscribed,m.sender_filter,m.subject_filter FROM box_mail_settings m JOIN logical_boxes b ON b.id=m.box_id AND b.account_id=m.account_id WHERE m.account_id=$1 AND m.box_id=$2 AND m.enabled AND b.state='running' FOR UPDATE OF m SKIP LOCKED`, accountID, boxID).Scan(&subscribed, &senderFilter, &subjectFilter)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var taskID, userID string
	err = tx.QueryRowContext(ctx, `SELECT id::text,user_id::text FROM box_tasks WHERE account_id=$1 AND logical_box_id=$2 AND state='active' AND agent<>'shell' ORDER BY created_at DESC LIMIT 1`, accountID, boxID).Scan(&taskID, &userID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if subscribed {
		if slices.Contains(tools, "list_emails") {
			items, ids, more, err := selectMailBatch(ctx, tx, accountID, boxID, senderFilter, subjectFilter)
			if err != nil {
				return err
			}
			if len(items) > 0 {
				mail := v1.BoxMessageMail{Kind: "mail_batch", Items: items, More: more}
				if err := insertMailBoxMessage(ctx, tx, accountID, taskID, userID, "mail-batch:"+ids[0], encodeBoxMessageMail(mail)); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE mail_messages SET notified_at=now() WHERE account_id=$1 AND box_id=$2 AND id::text=ANY($3)`, accountID, boxID, ids); err != nil {
					return err
				}
			}
		}
	}
	statusRows, err := tx.QueryContext(ctx, `SELECT id::text,notice_key,body FROM mail_notice_queue WHERE account_id=$1 AND box_id=$2 AND delivered_at IS NULL ORDER BY created_at,id LIMIT 20 FOR UPDATE SKIP LOCKED`, accountID, boxID)
	if err != nil {
		return err
	}
	type notice struct{ id, key, body string }
	var notices []notice
	for statusRows.Next() {
		var n notice
		if err := statusRows.Scan(&n.id, &n.key, &n.body); err != nil {
			statusRows.Close()
			return err
		}
		notices = append(notices, n)
	}
	if err := statusRows.Err(); err != nil {
		statusRows.Close()
		return err
	}
	statusRows.Close()
	for _, n := range notices {
		if err := insertMailBoxMessage(ctx, tx, accountID, taskID, userID, "mail-status:"+n.key, n.body); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mail_notice_queue SET delivered_at=now() WHERE account_id=$1 AND id=$2`, accountID, n.id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// The normal interaction reconciler delivers queued messages.
	return nil
}

func selectMailBatch(ctx context.Context, tx *sql.Tx, accountID, boxID, senderFilter, subjectFilter string) ([]v1.BoxMessageMailItem, []string, int, error) {
	// A message from this box's own address is excluded to avoid reply loops.
	rows, err := tx.QueryContext(ctx, `SELECT id::text,header_from,from_name,subject,preview,quarantined FROM mail_messages WHERE account_id=$1 AND box_id=$2 AND notified_at IS NULL AND expires_at>now() AND received_at<=now()-interval '30 seconds' AND NOT quarantined AND lower(envelope_from)<>lower(envelope_to) AND ($3='' OR lower(header_from)=ANY(string_to_array(lower($3),','))) AND ($4='' OR position(lower($4) in lower(subject))>0) ORDER BY received_at,id LIMIT 21`, accountID, boxID, senderFilter, subjectFilter)
	if err != nil {
		return nil, nil, 0, err
	}
	defer rows.Close()
	items := []v1.BoxMessageMailItem{}
	ids := []string{}
	for rows.Next() {
		var item v1.BoxMessageMailItem
		if err := rows.Scan(&item.ID, &item.From, &item.FromName, &item.Subject, &item.Preview, &item.Quarantined); err != nil {
			return nil, nil, 0, err
		}
		if len(items) < 20 {
			items = append(items, item)
			ids = append(ids, item.ID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, err
	}
	more := 0
	if len(items) == 20 {
		// Count the remaining messages so the notice accurately reports backlog.
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM mail_messages WHERE account_id=$1 AND box_id=$2 AND notified_at IS NULL AND expires_at>now() AND received_at<=now()-interval '30 seconds' AND NOT quarantined AND lower(envelope_from)<>lower(envelope_to) AND ($3='' OR lower(header_from)=ANY(string_to_array(lower($3),','))) AND ($4='' OR position(lower($4) in lower(subject))>0)`, accountID, boxID, senderFilter, subjectFilter).Scan(&more)
		if err != nil {
			return nil, nil, 0, err
		}
		more -= len(items)
	}
	return items, ids, more, nil
}

func insertMailBoxMessage(ctx context.Context, tx *sql.Tx, accountID, taskID, userID, key, body string) error {
	id := uuid()
	_, err := tx.ExecContext(ctx, `INSERT INTO box_messages(id,account_id,task_id,user_id,direction,body,submit,state,idempotency_key,chat_key,thread_id) VALUES($1,$2,$3,$4,'system',$5,true,'queued',$6,$7,$1) ON CONFLICT(account_id,idempotency_key) DO NOTHING`, id, accountID, taskID, userID, body, key, chatMessageKey())
	return err
}
