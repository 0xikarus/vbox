package controller

import (
	"context"
	"database/sql"
	"errors"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// Reserve under a pair-specific database lock so concurrent controller replicas
// cannot each admit an action beyond the actor-to-target limit. Reservations,
// including failed actions, count toward the limit and retain metadata only.
func (s *Store) reserveRemoteControl(ctx context.Context, accountID, actorID, targetID string, request remoteControlRequest) (string, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, accountID+":"+actorID, targetID); err != nil {
		return "", false, err
	}
	var second, minute int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE created_at>now()-interval '1 second'),count(*) FROM remote_control_actions WHERE account_id=$1 AND actor_box_id=$2 AND target_box_id=$3 AND created_at>now()-interval '1 minute'`, accountID, actorID, targetID).Scan(&second, &minute); err != nil {
		return "", false, err
	}
	if second >= 10 || minute >= 300 {
		return "", true, nil
	}
	id := uuid()
	_, err = tx.ExecContext(ctx, `INSERT INTO remote_control_actions(id,account_id,actor_box_id,target_box_id,action,x,y,to_x,to_y,dx,dy,text_length) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, accountID, actorID, targetID, request.Action, request.X, request.Y, request.ToX, request.ToY, request.DX, request.DY, len(request.Text))
	if err != nil {
		return "", false, err
	}
	return id, false, tx.Commit()
}

// One durable session row and one message are updated while actions stay within
// five minutes of each other. The row lock from UPSERT serializes message edits.
func (s *Store) finishRemoteControl(ctx context.Context, accountID, actorID, actorName, targetID, actionID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE remote_control_actions SET status='done' WHERE id=$1 AND account_id=$2`, actionID, accountID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) SELECT account_id,NULL,'agent_box.remote_control','logical_box',target_box_id::text,jsonb_build_object('actorBoxId',actor_box_id::text,'action',action,'x',x,'y',y,'toX',to_x,'toY',to_y,'dx',dx,'dy',dy,'textLength',text_length) FROM remote_control_actions WHERE id=$1 AND account_id=$2`, actionID, accountID); err != nil {
		return err
	}
	var noticeID string
	var actions int
	var first, last time.Time
	err = tx.QueryRowContext(ctx, `INSERT INTO remote_control_sessions(account_id,actor_box_id,target_box_id,notice_id,action_count,started_at,last_at) VALUES($1,$2,$3,$4,1,now(),now()) ON CONFLICT(account_id,actor_box_id,target_box_id) DO UPDATE SET notice_id=CASE WHEN remote_control_sessions.last_at>now()-interval '5 minutes' THEN remote_control_sessions.notice_id ELSE excluded.notice_id END,action_count=CASE WHEN remote_control_sessions.last_at>now()-interval '5 minutes' THEN remote_control_sessions.action_count+1 ELSE 1 END,started_at=CASE WHEN remote_control_sessions.last_at>now()-interval '5 minutes' THEN remote_control_sessions.started_at ELSE now() END,last_at=now() RETURNING notice_id::text,action_count,started_at,last_at`, accountID, actorID, targetID, uuid()).Scan(&noticeID, &actions, &first, &last)
	if err != nil {
		return err
	}
	var taskID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM box_tasks WHERE account_id=$1 AND logical_box_id=$2 ORDER BY (state='active') DESC,created_at DESC LIMIT 1`, accountID, targetID).Scan(&taskID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		control := v1.BoxMessageControl{Kind: "remote_control", ActorBoxID: actorID, ActorName: actorName, Actions: actions, FirstAt: first.UTC(), LastAt: last.UTC()}
		_, err = tx.ExecContext(ctx, `INSERT INTO box_messages(id,account_id,task_id,direction,body,submit,state,idempotency_key,chat_key,thread_id) VALUES($1,$2,$3,'system',$4,false,'delivered',$5,$6,$1) ON CONFLICT(id) DO UPDATE SET body=excluded.body,updated_at=now()`, noticeID, accountID, taskID, encodeBoxMessageControl(control), "remote-control:"+noticeID, chatMessageKey())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) lastRemoteControl(ctx context.Context, accountID, targetID string) (*v1.LastRemoteControl, error) {
	var item v1.LastRemoteControl
	err := s.DB.QueryRowContext(ctx, `SELECT r.actor_box_id::text,COALESCE(b.name,'Former box'),r.action_count,r.started_at,r.last_at FROM remote_control_sessions r LEFT JOIN logical_boxes b ON b.id=r.actor_box_id AND b.account_id=r.account_id WHERE r.account_id=$1 AND r.target_box_id=$2 ORDER BY r.last_at DESC LIMIT 1`, accountID, targetID).Scan(&item.ActorBoxID, &item.ActorName, &item.Actions, &item.StartedAt, &item.EndedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) lastRemoteControls(ctx context.Context, accountID string) (map[string]*v1.LastRemoteControl, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT ON (r.target_box_id) r.target_box_id::text,r.actor_box_id::text,COALESCE(b.name,'Former box'),r.action_count,r.started_at,r.last_at FROM remote_control_sessions r LEFT JOIN logical_boxes b ON b.id=r.actor_box_id AND b.account_id=r.account_id WHERE r.account_id=$1 ORDER BY r.target_box_id,r.last_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]*v1.LastRemoteControl{}
	for rows.Next() {
		var targetID string
		var item v1.LastRemoteControl
		if err := rows.Scan(&targetID, &item.ActorBoxID, &item.ActorName, &item.Actions, &item.StartedAt, &item.EndedAt); err != nil {
			return nil, err
		}
		result[targetID] = &item
	}
	return result, rows.Err()
}
