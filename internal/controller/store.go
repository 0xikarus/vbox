package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
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

type Store struct {
	DB       *sql.DB
	Envelope *secrets.Envelope
}
type Principal struct{ AccountID, UserID, Role, Subject string }

type DecryptedProviderCredential struct {
	v1.ProviderCredential
	Secret json.RawMessage
}

type DecryptedNotification struct {
	v1.NotificationDestination
	Secret json.RawMessage
}

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
	err := s.DB.QueryRowContext(ctx, `SELECT t.account_id::text,t.user_id::text,u.role,u.subject FROM access_tokens t JOIN users u ON u.id=t.user_id AND u.account_id=t.account_id WHERE t.token_hash=$1 AND t.revoked_at IS NULL AND u.disabled_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>now())`, secrets.TokenHash(token)).Scan(&p.AccountID, &p.UserID, &p.Role, &p.Subject)
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
	return s.bootstrap(ctx, accountName, subject, secrets.TokenHash(token))
}

func (s *Store) BootstrapHash(ctx context.Context, accountName, subject string, tokenHash []byte) (Principal, error) {
	if len(tokenHash) != sha256.Size {
		return Principal{}, fmt.Errorf("bootstrap token hash must be SHA-256")
	}
	return s.bootstrap(ctx, accountName, subject, tokenHash)
}

func (s *Store) bootstrap(ctx context.Context, accountName, subject string, tokenHash []byte) (Principal, error) {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO access_tokens(id,account_id,user_id,token_hash) VALUES($1,$2,$3,$4)`, tokenID, p.AccountID, p.UserID, tokenHash); err != nil {
		return p, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id) VALUES($1,$2,'account.bootstrap','account',$2)`, p.AccountID, p.UserID); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

func (s *Store) HasAccounts(ctx context.Context) (bool, error) {
	var exists bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts)`).Scan(&exists)
	return exists, err
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
		requested := append([]byte(nil), data...)
		var existing v1.Run
		queryErr := s.DB.QueryRowContext(ctx, `SELECT id::text,provider,state,request,COALESCE(external_reference,''),lease,created_at,updated_at FROM runs WHERE account_id=$1 AND idempotency_key=$2`, p.AccountID, idempotency).Scan(&existing.ID, &existing.Provider, &existing.State, &data, &existing.ExternalReference, &existing.Lease, &existing.CreatedAt, &existing.UpdatedAt)
		if queryErr == nil {
			if !bytes.Equal(requested, data) {
				return run, false, fmt.Errorf("idempotency key was already used with a different request")
			}
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
	if err := run.Request.Lifecycle.Normalize(); err != nil {
		return run, fmt.Errorf("stored lifecycle policy: %w", err)
	}
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
	result, err := s.DB.ExecContext(ctx, `UPDATE runs SET state=$3,box_id=COALESCE(NULLIF($4,''),box_id),summary=COALESCE(NULLIF($5,''),summary),exit_code=COALESCE($6,exit_code),started_at=CASE WHEN $3='running' THEN COALESCE(started_at,now()) ELSE started_at END,finished_at=CASE WHEN $3 IN ('succeeded','failed','cancelled') THEN now() ELSE finished_at END,updated_at=now() WHERE account_id=$1 AND id=$2`, accountID, id, state, boxID, summary, exit)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("run not found")
	}
	return nil
}

func (s *Store) AppendEvent(ctx context.Context, p Principal, event v1.Event) (bool, error) {
	event.AccountID = p.AccountID
	if event.ID == "" {
		event.ID = uuid()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO events(id,account_id,run_id,sequence,type,state,stream,message,data,signature,timestamp) SELECT $1,$2,r.id,$4,$5,NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),$9,NULLIF($10,''),$11 FROM runs r WHERE r.id=$3 AND r.account_id=$2 ON CONFLICT(run_id,sequence) DO NOTHING`, event.ID, p.AccountID, event.RunID, event.Sequence, event.Type, event.State, event.Stream, event.Message, nullJSON(event.Data), event.Signature, event.Timestamp)
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if inserted == 0 {
		return false, nil
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
			return false, err
		}
		_, _ = s.DB.ExecContext(ctx, `UPDATE runs SET state='needs_input',updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, event.RunID)
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE runs SET last_output_at=CASE WHEN $3='output' THEN $4 ELSE last_output_at END,last_heartbeat_at=CASE WHEN $3='heartbeat' THEN $4 ELSE last_heartbeat_at END,last_activity=CASE WHEN $3='output' THEN $5 ELSE last_activity END,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, event.RunID, event.Type, event.Timestamp, event.Message)
	if err == nil && event.State != "" {
		_, err = s.DB.ExecContext(ctx, `UPDATE runs SET state=$3,finished_at=CASE WHEN $3 IN ('succeeded','failed','cancelled') THEN COALESCE(finished_at,now()) ELSE finished_at END,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, event.RunID, event.State)
	}
	return err == nil, err
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
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET state='running',updated_at=now() WHERE account_id=$1 AND id=(SELECT run_id FROM questions WHERE account_id=$1 AND id=$2) AND state='needs_input'`, p.AccountID, id); err != nil {
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
func (s *Store) GetQuestion(ctx context.Context, accountID, id string) (v1.Question, error) {
	var runID string
	err := s.DB.QueryRowContext(ctx, `SELECT run_id::text FROM questions WHERE account_id=$1 AND id=$2`, accountID, id).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return v1.Question{}, fmt.Errorf("question not found")
	}
	if err != nil {
		return v1.Question{}, err
	}
	return s.QuestionAnswer(ctx, accountID, runID, id)
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

func (s *Store) ListUsers(ctx context.Context, p Principal) ([]v1.User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text,account_id::text,subject,role,created_at FROM users WHERE account_id=$1 AND disabled_at IS NULL ORDER BY created_at,id`, p.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []v1.User
	for rows.Next() {
		var user v1.User
		if err := rows.Scan(&user.ID, &user.AccountID, &user.Subject, &user.Role, &user.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) CreateUser(ctx context.Context, p Principal, req v1.CreateUserRequest) (v1.CreatedUser, error) {
	if req.Subject == "" {
		return v1.CreatedUser{}, fmt.Errorf("subject is required")
	}
	if req.Role != "owner" && req.Role != "user" {
		return v1.CreatedUser{}, fmt.Errorf("role must be exactly owner or user")
	}
	token, err := secretToken()
	if err != nil {
		return v1.CreatedUser{}, err
	}
	created := v1.CreatedUser{User: v1.User{ID: uuid(), AccountID: p.AccountID, Subject: req.Subject, Role: req.Role}, Token: token}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return created, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `INSERT INTO users(id,account_id,subject,role) VALUES($1,$2,$3,$4) RETURNING created_at`, created.ID, p.AccountID, req.Subject, req.Role).Scan(&created.CreatedAt); err != nil {
		return created, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO access_tokens(id,account_id,user_id,token_hash) VALUES($1,$2,$3,$4)`, uuid(), p.AccountID, created.ID, secrets.TokenHash(token)); err != nil {
		return created, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'user.create','user',$3,jsonb_build_object('role',$4))`, p.AccountID, p.UserID, created.ID, req.Role); err != nil {
		return created, err
	}
	return created, tx.Commit()
}

func (s *Store) RemoveUser(ctx context.Context, p Principal, id string) error {
	if id == p.UserID {
		return fmt.Errorf("owners cannot remove their own active identity")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE account_id=$1 AND id=$2 AND disabled_at IS NULL FOR UPDATE`, p.AccountID, id).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("user not found")
		}
		return err
	}
	if role == "owner" {
		var owners int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE account_id=$1 AND role='owner' AND disabled_at IS NULL`, p.AccountID).Scan(&owners); err != nil {
			return err
		}
		if owners <= 1 {
			return fmt.Errorf("cannot remove the last owner")
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET disabled_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE access_tokens SET revoked_at=now() WHERE account_id=$1 AND user_id=$2 AND revoked_at IS NULL`, p.AccountID, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id) VALUES($1,$2,'user.remove','user',$3)`, p.AccountID, p.UserID, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListProviderCredentials(ctx context.Context, accountID string) ([]v1.ProviderCredential, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text,account_id::text,provider,name,config,created_at,updated_at FROM provider_credentials WHERE account_id=$1 ORDER BY provider,name`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []v1.ProviderCredential
	for rows.Next() {
		var value v1.ProviderCredential
		if err := rows.Scan(&value.ID, &value.AccountID, &value.Provider, &value.Name, &value.Config, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) PutProviderCredential(ctx context.Context, p Principal, providerName, name string, req v1.PutProviderCredentialRequest) (v1.ProviderCredential, error) {
	if providerName == "" || name == "" {
		return v1.ProviderCredential{}, fmt.Errorf("provider and credential name are required")
	}
	if s.Envelope == nil {
		return v1.ProviderCredential{}, fmt.Errorf("controller encryption key is not configured")
	}
	if !validJSONObject(req.Secret, true) {
		return v1.ProviderCredential{}, fmt.Errorf("secret must be a non-empty JSON object")
	}
	if len(req.Config) == 0 {
		req.Config = json.RawMessage(`{}`)
	}
	if !validJSONObject(req.Config, false) {
		return v1.ProviderCredential{}, fmt.Errorf("config must be a JSON object")
	}
	sealed, err := s.Envelope.Seal(p.AccountID, req.Secret)
	if err != nil {
		return v1.ProviderCredential{}, err
	}
	value := v1.ProviderCredential{ID: uuid(), AccountID: p.AccountID, Provider: providerName, Name: name, Config: req.Config}
	err = s.DB.QueryRowContext(ctx, `INSERT INTO provider_credentials(id,account_id,provider,name,encrypted_value,config) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(account_id,provider,name) DO UPDATE SET encrypted_value=excluded.encrypted_value,config=excluded.config,updated_at=now() RETURNING id::text,created_at,updated_at`, value.ID, p.AccountID, providerName, name, sealed, req.Config).Scan(&value.ID, &value.CreatedAt, &value.UpdatedAt)
	if err != nil {
		return value, err
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'provider_credential.put','provider_credential',$3,jsonb_build_object('provider',$4,'name',$5))`, p.AccountID, p.UserID, value.ID, providerName, name)
	return value, nil
}

func (s *Store) ProviderCredential(ctx context.Context, accountID, providerName, name string) (DecryptedProviderCredential, error) {
	if s.Envelope == nil {
		return DecryptedProviderCredential{}, fmt.Errorf("controller encryption key is not configured")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text,account_id::text,provider,name,encrypted_value,config,created_at,updated_at FROM provider_credentials WHERE account_id=$1 AND provider=$2 AND ($3='' OR name=$3) ORDER BY name LIMIT 2`, accountID, providerName, name)
	if err != nil {
		return DecryptedProviderCredential{}, err
	}
	defer rows.Close()
	var values []DecryptedProviderCredential
	var sealed []string
	for rows.Next() {
		var value DecryptedProviderCredential
		var encrypted string
		if err := rows.Scan(&value.ID, &value.AccountID, &value.Provider, &value.Name, &encrypted, &value.Config, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return value, err
		}
		values = append(values, value)
		sealed = append(sealed, encrypted)
	}
	if err := rows.Err(); err != nil {
		return DecryptedProviderCredential{}, err
	}
	if len(values) == 0 {
		return DecryptedProviderCredential{}, fmt.Errorf("provider credential not found")
	}
	if name == "" && len(values) != 1 {
		return DecryptedProviderCredential{}, fmt.Errorf("provider credential name is required when multiple %s credentials exist", providerName)
	}
	plain, err := s.Envelope.Open(accountID, sealed[0])
	if err != nil {
		return DecryptedProviderCredential{}, fmt.Errorf("decrypt provider credential: %w", err)
	}
	values[0].Secret = append(json.RawMessage(nil), plain...)
	return values[0], nil
}

func (s *Store) DeleteProviderCredential(ctx context.Context, p Principal, providerName, name string) error {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM provider_credentials WHERE account_id=$1 AND provider=$2 AND name=$3`, p.AccountID, providerName, name)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("provider credential not found")
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'provider_credential.delete','provider_credential',$3,jsonb_build_object('provider',$4,'name',$5))`, p.AccountID, p.UserID, providerName+":"+name, providerName, name)
	return nil
}

func (s *Store) ListNotifications(ctx context.Context, accountID string, decrypt bool) ([]DecryptedNotification, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text,account_id::text,kind,name,encrypted_secret,config,allowed_users,allowed_chats,enabled,created_at,updated_at FROM notification_destinations WHERE account_id=$1 ORDER BY kind,name`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []DecryptedNotification
	for rows.Next() {
		var value DecryptedNotification
		var encrypted string
		var users, chats []byte
		if err := rows.Scan(&value.ID, &value.AccountID, &value.Kind, &value.Name, &encrypted, &value.Config, &users, &chats, &value.Enabled, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(users, &value.AllowedUsers)
		_ = json.Unmarshal(chats, &value.AllowedChats)
		if decrypt {
			if s.Envelope == nil {
				return nil, fmt.Errorf("controller encryption key is not configured")
			}
			plain, err := s.Envelope.Open(accountID, encrypted)
			if err != nil {
				return nil, fmt.Errorf("decrypt notification destination: %w", err)
			}
			value.Secret = append(json.RawMessage(nil), plain...)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) PutNotification(ctx context.Context, p Principal, kind, name string, req v1.PutNotificationRequest) (v1.NotificationDestination, error) {
	if kind != "webhook" && kind != "telegram" && kind != "discord" {
		return v1.NotificationDestination{}, fmt.Errorf("notification kind must be webhook, telegram, or discord")
	}
	if name == "" || !validJSONObject(req.Secret, true) {
		return v1.NotificationDestination{}, fmt.Errorf("name and a non-empty secret JSON object are required")
	}
	if len(req.Config) == 0 {
		req.Config = json.RawMessage(`{}`)
	}
	if !validJSONObject(req.Config, false) || s.Envelope == nil {
		return v1.NotificationDestination{}, fmt.Errorf("valid config and controller encryption key are required")
	}
	sealed, err := s.Envelope.Seal(p.AccountID, req.Secret)
	if err != nil {
		return v1.NotificationDestination{}, err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	users, _ := json.Marshal(req.AllowedUsers)
	chats, _ := json.Marshal(req.AllowedChats)
	value := v1.NotificationDestination{ID: uuid(), AccountID: p.AccountID, Kind: kind, Name: name, Config: req.Config, AllowedUsers: req.AllowedUsers, AllowedChats: req.AllowedChats, Enabled: enabled}
	err = s.DB.QueryRowContext(ctx, `INSERT INTO notification_destinations(id,account_id,kind,name,encrypted_secret,config,allowed_users,allowed_chats,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(account_id,kind,name) DO UPDATE SET encrypted_secret=excluded.encrypted_secret,config=excluded.config,allowed_users=excluded.allowed_users,allowed_chats=excluded.allowed_chats,enabled=excluded.enabled,updated_at=now() RETURNING id::text,created_at,updated_at`, value.ID, p.AccountID, kind, name, sealed, req.Config, users, chats, enabled).Scan(&value.ID, &value.CreatedAt, &value.UpdatedAt)
	if err != nil {
		return value, err
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'notification.put','notification',$3,jsonb_build_object('kind',$4,'name',$5))`, p.AccountID, p.UserID, value.ID, kind, name)
	return value, nil
}

func (s *Store) DeleteNotification(ctx context.Context, p Principal, kind, name string) error {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM notification_destinations WHERE account_id=$1 AND kind=$2 AND name=$3`, p.AccountID, kind, name)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("notification destination not found")
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id) VALUES($1,$2,'notification.delete','notification',$3)`, p.AccountID, p.UserID, kind+":"+name)
	return nil
}

func (s *Store) NotificationAttempt(ctx context.Context, accountID, id string, sendErr error) {
	message := ""
	if sendErr != nil {
		message = sendErr.Error()
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE notification_destinations SET last_attempt_at=now(),last_error=NULLIF($3,'') WHERE account_id=$1 AND id=$2`, accountID, id, message)
}

func (s *Store) ListReconcileRuns(ctx context.Context) ([]v1.Run, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT account_id::text,id::text FROM runs WHERE state NOT IN ('deleted') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	type key struct{ account, id string }
	var keys []key
	for rows.Next() {
		var item key
		if err := rows.Scan(&item.account, &item.id); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	values := make([]v1.Run, 0, len(keys))
	for _, item := range keys {
		run, err := s.GetRun(ctx, item.account, item.id)
		if err != nil {
			return nil, err
		}
		values = append(values, run)
	}
	return values, nil
}

func secretToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func validJSONObject(value json.RawMessage, requireValue bool) bool {
	var object map[string]any
	return len(value) > 0 && json.Unmarshal(value, &object) == nil && (!requireValue || len(object) > 0)
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
