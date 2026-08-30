package controller

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

type Store struct{ DB *sql.DB }
type Principal struct{ AccountID, UserID, Role, Subject string }

func Open(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, schema)
	return err
}

func (s *Store) Authenticate(ctx context.Context, token string) (Principal, error) {
	var p Principal
	if token == "" {
		return p, fmt.Errorf("missing bearer token")
	}
	err := s.DB.QueryRowContext(ctx, `SELECT t.account_id::text,t.user_id::text,u.role,u.subject FROM access_tokens t JOIN users u ON u.id=t.user_id AND u.account_id=t.account_id WHERE t.token_hash=$1 AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>now())`, secrets.TokenHash(token)).Scan(&p.AccountID, &p.UserID, &p.Role, &p.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("invalid or expired bearer token")
	}
	return p, err
}

func (s *Store) AuthenticateBox(ctx context.Context, runID, lease string) (Principal, error) {
	var p Principal
	err := s.DB.QueryRowContext(ctx, `SELECT account_id::text FROM runs WHERE id=$1 AND lease=$2 AND state IN ('preparing','running','needs_input','resuming')`, runID, lease).Scan(&p.AccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("invalid or stale box identity")
	}
	p.Subject = "box:" + runID
	return p, err
}

func (s *Store) Bootstrap(ctx context.Context, accountName, subject, token string) (Principal, error) {
	p := Principal{AccountID: boxruntime.ID("00000000-0000-4000-8000-"), UserID: boxruntime.ID("00000000-0000-4000-8000-"), Role: "owner", Subject: subject}
	// Generate syntactically valid UUIDs rather than exposing sequential IDs.
	p.AccountID = uuid()
	p.UserID = uuid()
	tokenID := uuid()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO accounts(id,name) VALUES($1,$2)`, p.AccountID, accountName); err != nil {
		return p, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users(id,account_id,subject,role) VALUES($1,$2,$3,'owner')`, p.UserID, p.AccountID, subject); err != nil {
		return p, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO access_tokens(id,account_id,user_id,token_hash) VALUES($1,$2,$3,$4)`, tokenID, p.AccountID, p.UserID, secrets.TokenHash(token)); err != nil {
		return p, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id) VALUES($1,$2,'account.bootstrap','account',$2)`, p.AccountID, p.UserID); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

func uuid() string {
	raw := boxruntime.ID("")
	if len(raw) < 24 {
		raw += "000000000000000000000000"
	}
	return fmt.Sprintf("%s-%s-4%s-8%s-%s", raw[0:8], raw[8:12], raw[13:16], raw[17:20], raw[20:24]+"00000000")
}

func (s *Store) CreateRun(ctx context.Context, p Principal, req v1.CreateRunRequest, idempotency string) (v1.Run, bool, error) {
	if idempotency == "" {
		return v1.Run{}, false, fmt.Errorf("Idempotency-Key is required")
	}
	if err := req.Lifecycle.Normalize(); err != nil {
		return v1.Run{}, false, err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return v1.Run{}, false, err
	}
	run := v1.Run{ID: uuid(), AccountID: p.AccountID, Provider: req.Provider, State: v1.JobQueued, Request: req, ExternalReference: req.ExternalReference, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Lease: boxruntime.ID("lease_")}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO runs(id,account_id,user_id,provider,state,request,external_reference,idempotency_key,lease) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, run.ID, p.AccountID, p.UserID, run.Provider, run.State, data, null(req.ExternalReference), idempotency, run.Lease)
	if err != nil {
		var existing v1.Run
		queryErr := s.DB.QueryRowContext(ctx, `SELECT id::text,provider,state,request,COALESCE(external_reference,''),lease,created_at,updated_at FROM runs WHERE account_id=$1 AND idempotency_key=$2`, p.AccountID, idempotency).Scan(&existing.ID, &existing.Provider, &existing.State, &data, &existing.ExternalReference, &existing.Lease, &existing.CreatedAt, &existing.UpdatedAt)
		if queryErr == nil {
			existing.AccountID = p.AccountID
			_ = json.Unmarshal(data, &existing.Request)
			return existing, true, nil
		}
		return run, false, err
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'run.create','run',$3,$4)`, p.AccountID, p.UserID, run.ID, data)
	return run, false, nil
}

func (s *Store) GetRun(ctx context.Context, accountID, id string) (v1.Run, error) {
	var run v1.Run
	var request []byte
	var started, finished, lastOut, lastBeat sql.NullTime
	var exit sql.NullInt64
	var summary, lastActivity sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id::text,account_id::text,COALESCE(box_id,''),provider,state,request,COALESCE(external_reference,''),created_at,updated_at,started_at,finished_at,exit_code,summary,last_output_at,last_heartbeat_at,last_activity,lease FROM runs WHERE account_id=$1 AND id=$2`, accountID, id).Scan(&run.ID, &run.AccountID, &run.BoxID, &run.Provider, &run.State, &request, &run.ExternalReference, &run.CreatedAt, &run.UpdatedAt, &started, &finished, &exit, &summary, &lastOut, &lastBeat, &lastActivity, &run.Lease)
	if errors.Is(err, sql.ErrNoRows) {
		return run, fmt.Errorf("run not found")
	}
	if err != nil {
		return run, err
	}
	_ = json.Unmarshal(request, &run.Request)
	if started.Valid {
		run.StartedAt = &started.Time
	}
	if finished.Valid {
		run.FinishedAt = &finished.Time
	}
	if exit.Valid {
		value := int(exit.Int64)
		run.ExitCode = &value
	}
	run.Summary = summary.String
	run.LastActivity = lastActivity.String
	if lastOut.Valid {
		run.LastOutputAt = &lastOut.Time
	}
	if lastBeat.Valid {
		run.LastHeartbeatAt = &lastBeat.Time
	}
	return run, nil
}
func (s *Store) ListRuns(ctx context.Context, accountID string) ([]v1.Run, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text FROM runs WHERE account_id=$1 ORDER BY created_at DESC LIMIT 200`, accountID)
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
	runs := make([]v1.Run, 0, len(ids))
	for _, id := range ids {
		run, err := s.GetRun(ctx, accountID, id)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Store) SetRunState(ctx context.Context, accountID, id, boxID string, state v1.JobState, summary string, exit *int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET state=$3,box_id=COALESCE(NULLIF($4,''),box_id),summary=COALESCE(NULLIF($5,''),summary),exit_code=COALESCE($6,exit_code),started_at=CASE WHEN $3='running' THEN COALESCE(started_at,now()) ELSE started_at END,finished_at=CASE WHEN $3 IN ('succeeded','failed','cancelled') THEN now() ELSE finished_at END,updated_at=now() WHERE account_id=$1 AND id=$2`, accountID, id, state, boxID, summary, exit)
	return err
}

func (s *Store) AppendEvent(ctx context.Context, p Principal, event v1.Event) error {
	event.AccountID = p.AccountID
	if event.ID == "" {
		event.ID = uuid()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO events(id,account_id,run_id,sequence,type,state,stream,message,data,signature,timestamp) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),$9,NULLIF($10,''),$11) ON CONFLICT(run_id,sequence) DO NOTHING`, event.ID, p.AccountID, event.RunID, event.Sequence, event.Type, event.State, event.Stream, event.Message, nullJSON(event.Data), event.Signature, event.Timestamp)
	if err != nil {
		return err
	}
	if event.Type == "needs_input" {
		questionID := event.ID
		if len(event.Data) > 0 {
			var data struct {
				QuestionID string `json:"questionId"`
			}
			_ = json.Unmarshal(event.Data, &data)
			if data.QuestionID != "" {
				questionID = data.QuestionID
			}
		}
		_, err = s.DB.ExecContext(ctx, `INSERT INTO questions(id,account_id,run_id,prompt,state,blocking,asked_at) VALUES($1,$2,$3,$4,'open',true,$5) ON CONFLICT(id) DO NOTHING`, questionID, p.AccountID, event.RunID, event.Message, event.Timestamp)
		if err != nil {
			return err
		}
		_, _ = s.DB.ExecContext(ctx, `UPDATE runs SET state='needs_input',updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, event.RunID)
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE runs SET last_output_at=CASE WHEN $3='output' THEN $4 ELSE last_output_at END,last_heartbeat_at=CASE WHEN $3='heartbeat' THEN $4 ELSE last_heartbeat_at END,last_activity=CASE WHEN $3='output' THEN $5 ELSE last_activity END,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, event.RunID, event.Type, event.Timestamp, event.Message)
	if err == nil && event.State != "" {
		_, err = s.DB.ExecContext(ctx, `UPDATE runs SET state=$3,finished_at=CASE WHEN $3 IN ('succeeded','failed','cancelled') THEN COALESCE(finished_at,now()) ELSE finished_at END,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, event.RunID, event.State)
	}
	return err
}

func (s *Store) ListQuestions(ctx context.Context, p Principal) ([]v1.Question, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text,account_id::text,run_id::text,prompt,state,blocking,asked_at,expires_at,COALESCE(answer,''),answered_at,COALESCE(answered_by::text,'') FROM questions WHERE account_id=$1 AND state='open' ORDER BY asked_at`, p.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []v1.Question
	for rows.Next() {
		var q v1.Question
		var expires, answered sql.NullTime
		if err := rows.Scan(&q.ID, &q.AccountID, &q.RunID, &q.Prompt, &q.State, &q.Blocking, &q.AskedAt, &expires, &q.Answer, &answered, &q.AnsweredBy); err != nil {
			return nil, err
		}
		if expires.Valid {
			q.ExpiresAt = &expires.Time
		}
		if answered.Valid {
			q.AnsweredAt = &answered.Time
		}
		result = append(result, q)
	}
	return result, rows.Err()
}
func (s *Store) Answer(ctx context.Context, p Principal, id, answer string) error {
	if answer == "" {
		return fmt.Errorf("answer cannot be empty")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE questions SET state='answered',answer=$4,answered_at=now(),answered_by=$2 WHERE account_id=$1 AND id=$3 AND state='open' AND (expires_at IS NULL OR expires_at>now())`, p.AccountID, p.UserID, id, answer)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("question is stale, completed, or unavailable")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id) VALUES($1,$2,'question.answer','question',$3)`, p.AccountID, p.UserID, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) QuestionAnswer(ctx context.Context, accountID, runID, id string) (v1.Question, error) {
	var q v1.Question
	var answered sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT id::text,account_id::text,run_id::text,prompt,state,blocking,asked_at,COALESCE(answer,''),answered_at,COALESCE(answered_by::text,'') FROM questions WHERE account_id=$1 AND run_id=$2 AND id=$3`, accountID, runID, id).Scan(&q.ID, &q.AccountID, &q.RunID, &q.Prompt, &q.State, &q.Blocking, &q.AskedAt, &q.Answer, &answered, &q.AnsweredBy)
	if errors.Is(err, sql.ErrNoRows) {
		return q, fmt.Errorf("question not found")
	}
	if answered.Valid {
		q.AnsweredAt = &answered.Time
	}
	return q, err
}
func (s *Store) HeartbeatHost(ctx context.Context, p Principal, id string, capabilities any) error {
	if id == "" {
		return fmt.Errorf("host id is required")
	}
	data, err := json.Marshal(capabilities)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO hosts(id,account_id,capabilities,last_seen_at) VALUES($1,$2,$3,now()) ON CONFLICT(account_id,id) DO UPDATE SET capabilities=excluded.capabilities,last_seen_at=now()`, id, p.AccountID, data)
	return err
}

func null(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
