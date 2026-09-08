// Package publishing durably publishes approved factory plans. It owns no
// controller resources and never interprets issue creation as implementation.
package publishing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/githubapp"
)

var (
	ErrPolicy  = errors.New("publishing: operator account binding and issuesWrite required")
	ErrBlocked = errors.New("publishing: publication blocked; inspect factory publication records")
	ErrFence   = errors.New("publishing: approved source or revision changed")
)

// Policy must come from trusted operator configuration, never work/request data.
// The GitHub client separately verifies repository visibility and issues:write.
type Policy struct {
	AccountID   string
	IssuesWrite bool
}

// Publisher is implemented by *githubapp.Client. Fixtures must honor the same
// ReconcileOnly contract: absent evidence is unknown, never permission to POST.
type Publisher interface {
	PublishMasterIssue(context.Context, githubapp.WriteAuthority, githubapp.PublicationOperation, factory.Work) (githubapp.Publication, error)
	PublishFeatureIssue(context.Context, githubapp.WriteAuthority, githubapp.PublicationOperation, factory.Work, string, int64, map[string]int64) (githubapp.Publication, error)
	PublishIssueComment(context.Context, githubapp.WriteAuthority, githubapp.PublicationOperation, int64, string) (githubapp.Publication, error)
}

// Coordinator performs at most one external publication operation per Step.
// Configure once before use; multiple instances may share the same database.
type Coordinator struct {
	DB     *sql.DB
	Client Publisher
	Policy Policy
}

func New(db *sql.DB, client *githubapp.Client, policy Policy) *Coordinator {
	var publisher Publisher
	if client != nil {
		publisher = client
	}
	return &Coordinator{DB: db, Client: publisher, Policy: policy}
}

// Migrate creates only package-owned factory tables; factory.Store.Migrate must
// have run first. No work documents or controller tables are migrated here.
func (c *Coordinator) Migrate(ctx context.Context) error {
	_, err := c.DB.ExecContext(ctx, `
 CREATE TABLE IF NOT EXISTS factory_publication_jobs (
 work_id TEXT PRIMARY KEY REFERENCES factory_work_items(id), account_id TEXT NOT NULL,
 approved_revision INTEGER NOT NULL, fence TEXT NOT NULL, snapshot JSONB NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('active','blocked','complete')),
 error TEXT NOT NULL DEFAULT '', lease TEXT, lease_until TIMESTAMPTZ,
 next_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS factory_publication_operations (
 work_id TEXT NOT NULL REFERENCES factory_publication_jobs(work_id),
 operation_key TEXT NOT NULL UNIQUE, ordinal INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('master','feature','comment')), feature_id TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','attempted','unknown','succeeded','blocked')),
 intent JSONB, attempts INTEGER NOT NULL DEFAULT 0,
 publication_id BIGINT, issue_number BIGINT, error TEXT NOT NULL DEFAULT '',
 lease TEXT, lease_until TIMESTAMPTZ, attempted_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
 PRIMARY KEY(work_id,ordinal));`)
	return err
}

type intent struct {
	Authority    githubapp.WriteAuthority
	Work         factory.Work
	Kind         string
	FeatureID    string
	Master       int64
	Dependencies map[string]int64
	Body         string
}
type task struct {
	intent
	key, token, fence string
	reconcile         bool
}
type record struct {
	kind, feature string
	result        githubapp.Publication
}

func token() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func hash(v any) string { b, _ := json.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(b)) }
func operationKey(w factory.Work, kind, feature string) string {
	return "factory-publication-v1:" + hash([]any{w.ID, w.ApprovedPlanRevision, kind, feature})
}
func fence(w factory.Work) string {
	return hash([]any{w.ID, w.Revision, w.RepositoryID, w.RepositoryName, w.BaseSHA, w.ApprovedPlanRevision, w.Plans})
}

var shaRE = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var repoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func approved(w factory.Work) (factory.Plan, error) {
	if len(w.Plans) == 0 {
		return factory.Plan{}, ErrFence
	}
	p := w.Plans[len(w.Plans)-1]
	if w.ID == "" || w.ApprovedPlanRevision != p.Revision || p.BaseSHA != w.BaseSHA || !shaRE.MatchString(w.BaseSHA) || p.InputRevision < 1 || p.InputRevision >= w.Revision || !repoRE.MatchString(w.RepositoryName) {
		return p, ErrFence
	}
	for _, part := range strings.Split(w.RepositoryName, "/") {
		if part == "." || part == ".." {
			return p, ErrFence
		}
	}
	return p, p.ValidateApproval()
}
func ordered(p factory.Plan) []factory.Feature {
	byID := map[string]factory.Feature{}
	for _, f := range p.Features {
		byID[f.ID] = f
	}
	seen := map[string]bool{}
	out := []factory.Feature{}
	var visit func(string)
	visit = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		f := byID[id]
		for _, d := range f.DependsOn {
			visit(d)
		}
		out = append(out, f)
	}
	for _, f := range p.Features {
		visit(f.ID)
	}
	return out
}

// Step returns sql.ErrNoRows when no eligible, unleased work is ready. Unknown
// writes return ErrPublicationUncertain and are only reconciled on later Steps.
// Fixed failures return ErrBlocked and require explicit operator investigation.
func (c *Coordinator) Step(ctx context.Context) error {
	if c.DB == nil || c.Client == nil || strings.TrimSpace(c.Policy.AccountID) == "" || !c.Policy.IssuesWrite {
		return ErrPolicy
	}
	t, err := c.claim(ctx)
	if err != nil || t == nil {
		return err
	}
	op := githubapp.PublicationOperation{Key: t.key, ReconcileOnly: t.reconcile}
	var result githubapp.Publication
	switch t.Kind {
	case "master":
		result, err = c.Client.PublishMasterIssue(ctx, t.Authority, op, t.Work)
	case "feature":
		result, err = c.Client.PublishFeatureIssue(ctx, t.Authority, op, t.Work, t.FeatureID, t.Master, t.Dependencies)
	case "comment":
		result, err = c.Client.PublishIssueComment(ctx, t.Authority, op, t.Master, t.Body)
	}
	// A failed persistence (including cancellation) deliberately leaves attempted
	// durable. The next owner must reconcile even if the network never started.
	return c.finish(ctx, t, result, err)
}

func (c *Coordinator) claim(ctx context.Context) (*task, error) {
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var raw []byte
	var id, state string
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT w.id,w.revision,w.state,w.document FROM factory_work_items w
 LEFT JOIN factory_publication_jobs j ON j.work_id=w.id
 WHERE w.account_id=$1 AND w.state IN ('approved_queued','publishing_issues')
 AND (j.work_id IS NULL OR (j.state='active' AND j.next_at<=now() AND (j.lease_until IS NULL OR j.lease_until<now())))
 AND NOT EXISTS (SELECT 1 FROM factory_publication_operations o WHERE o.work_id=w.id AND o.lease_until>now())
 ORDER BY w.updated_at,w.id FOR UPDATE OF w SKIP LOCKED LIMIT 1`, c.Policy.AccountID).Scan(&id, &revision, &state, &raw)
	if err != nil {
		return nil, err
	}
	var w factory.Work
	decodeErr := json.Unmarshal(raw, &w)
	p, validation := approved(w)
	if decodeErr != nil {
		validation = decodeErr
	}
	if w.ID != id || w.Revision != revision || w.State != state {
		validation = ErrFence
	}
	f := fence(w)
	inserted, err := tx.ExecContext(ctx, `INSERT INTO factory_publication_jobs(work_id,account_id,approved_revision,fence,snapshot,state) VALUES($1,$2,$3,$4,$5,'active') ON CONFLICT DO NOTHING`, id, c.Policy.AccountID, w.ApprovedPlanRevision, f, raw)
	if err != nil {
		return nil, err
	}
	created, err := inserted.RowsAffected()
	if err != nil {
		return nil, err
	}
	var savedFence, account string
	var snapshot []byte
	err = tx.QueryRowContext(ctx, `SELECT fence,account_id,snapshot FROM factory_publication_jobs WHERE work_id=$1 FOR UPDATE`, id).Scan(&savedFence, &account, &snapshot)
	if err != nil {
		return nil, err
	}
	if account != c.Policy.AccountID || f != savedFence {
		validation = ErrFence
	}
	if validation != nil {
		if err = block(ctx, tx, id, "Approved plan/source fence or executable plan is invalid."); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, ErrBlocked
	}
	// Always invoke the immutable original snapshot, including plan timestamps.
	w = factory.Work{}
	if err = json.Unmarshal(snapshot, &w); err != nil {
		return nil, err
	}
	kinds := []record{{kind: "master"}}
	for _, feature := range ordered(p) {
		kinds = append(kinds, record{kind: "feature", feature: feature.ID})
	}
	kinds = append(kinds, record{kind: "comment"})
	for i, r := range kinds {
		// Job and operation initialization commit atomically. Never recreate a
		// missing operation on recovery: doing so could authorize a second POST.
		if created == 0 {
			break
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO factory_publication_operations(work_id,operation_key,ordinal,kind,feature_id,state) VALUES($1,$2,$3,$4,$5,'pending') ON CONFLICT(work_id,ordinal) DO NOTHING`, id, operationKey(w, r.kind, r.feature), i, r.kind, r.feature)
		if err != nil {
			return nil, err
		}
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_publication_operations WHERE work_id=$1`, id).Scan(&count); err != nil {
		return nil, err
	}
	if count != len(kinds) {
		if err = block(ctx, tx, id, "Publication operation records are missing or inconsistent; operator investigation required."); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, ErrBlocked
	}
	records, err := readRecords(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	t := &task{intent: intent{Authority: githubapp.WriteAuthority{AccountID: account, RepositoryID: w.RepositoryID, IssuesWrite: true}, Work: w}, token: token(), fence: f}
	var prior string
	var oldIntent []byte
	err = tx.QueryRowContext(ctx, `SELECT operation_key,kind,feature_id,state,intent FROM factory_publication_operations WHERE work_id=$1 AND state!='succeeded' ORDER BY ordinal LIMIT 1 FOR UPDATE`, id).Scan(&t.key, &t.Kind, &t.FeatureID, &prior, &oldIntent)
	if errors.Is(err, sql.ErrNoRows) {
		if err = progress(ctx, tx, w, records, true, ""); err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE factory_publication_jobs SET state='complete',lease=NULL,lease_until=NULL,updated_at=now() WHERE work_id=$1`, id)
		if err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if prior == "blocked" {
		if err = block(ctx, tx, id, "Publication operation requires operator investigation."); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, ErrBlocked
	}
	t.reconcile = prior != "pending"
	if t.reconcile {
		expected := t.intent
		t.intent = intent{}
		decodeErr := json.Unmarshal(oldIntent, &t.intent)
		if decodeErr != nil || t.Authority != expected.Authority || fence(t.Work) != f || t.Kind != expected.Kind || t.FeatureID != expected.FeatureID || operationKey(t.Work, t.Kind, t.FeatureID) != t.key {
			if err = block(ctx, tx, id, "Stored publication intent failed its account/source/operation fence; operator investigation required."); err != nil {
				return nil, err
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			return nil, ErrBlocked
		}
	} else {
		numbers := map[string]int64{}
		for _, r := range records {
			if r.kind == "master" {
				t.Master = r.result.Number
			}
			if r.kind == "feature" {
				numbers[r.feature] = r.result.Number
			}
		}
		if t.Kind == "feature" {
			t.Dependencies = map[string]int64{}
			for _, feature := range p.Features {
				if feature.ID == t.FeatureID {
					for _, d := range feature.DependsOn {
						t.Dependencies[d] = numbers[d]
					}
				}
			}
		}
		if t.Kind == "comment" {
			t.Body = fmt.Sprintf("## Approved plan publication\n\nWork `%s`, approved revision %d, source `%s`.\n\nFeature issues in dependency order:\n", w.ID, w.ApprovedPlanRevision, w.BaseSHA)
			for _, feature := range ordered(p) {
				t.Body += fmt.Sprintf("\n- `%s`: %s — %s", feature.ID, feature.Title, issueURL(w, numbers[feature.ID]))
			}
			t.Body += "\n\nIssue publication is complete. Implementation and verification are still pending."
		}
	}
	payload, err := json.Marshal(t.intent)
	if err != nil {
		return nil, err
	}
	// Both leases are acquired in the same short transaction as the attempted
	// intent. Expiry can permit overlapping reads, but never another initial POST.
	_, err = tx.ExecContext(ctx, `UPDATE factory_publication_operations SET state='attempted',intent=$3,attempts=attempts+1,lease=$4,lease_until=now()+interval '2 minutes',attempted_at=now() WHERE work_id=$1 AND operation_key=$2`, id, t.key, payload, t.token)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE factory_publication_jobs SET lease=$2,lease_until=now()+interval '2 minutes',updated_at=now() WHERE work_id=$1`, id, t.token)
	if err != nil {
		return nil, err
	}
	message := ""
	if t.reconcile {
		message = "Publication outcome unknown; reconciling prior attempted write."
	}
	if err = progress(ctx, tx, w, records, false, message); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return t, nil
}

func readRecords(ctx context.Context, tx *sql.Tx, id string) ([]record, error) {
	rows, err := tx.QueryContext(ctx, `SELECT kind,feature_id,publication_id,issue_number FROM factory_publication_operations WHERE work_id=$1 AND state='succeeded' ORDER BY ordinal`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []record{}
	for rows.Next() {
		var r record
		if err = rows.Scan(&r.kind, &r.feature, &r.result.ID, &r.result.Number); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func issueURL(w factory.Work, n int64) string {
	return fmt.Sprintf("https://github.com/%s/issues/%d", w.RepositoryName, n)
}
func progress(ctx context.Context, tx *sql.Tx, w factory.Work, records []record, complete bool, message string) error {
	p := w.Plans[len(w.Plans)-1]
	features := append([]factory.Feature{}, p.Features...)
	master := ""
	numbers := map[string]int64{}
	comment := false
	for _, r := range records {
		if r.result.ID <= 0 {
			return ErrBlocked
		}
		switch r.kind {
		case "master":
			master = issueURL(w, r.result.Number)
		case "feature":
			numbers[r.feature] = r.result.Number
		case "comment":
			comment = true
		}
	}
	for i := range features {
		features[i].State = "pending"
		features[i].IssueURL = ""
		features[i].PRURL = ""
		features[i].BoxID = ""
		if n := numbers[features[i].ID]; n > 0 {
			features[i].IssueURL = issueURL(w, n)
			features[i].State = "issue_published"
		}
	}
	state := "publishing_issues"
	if complete {
		if master == "" || len(numbers) != len(features) || !comment {
			return ErrBlocked
		}
		state = "build_queued"
	}
	patch, _ := json.Marshal(map[string]any{"state": state, "masterIssueUrl": master, "features": features, "error": message, "updatedAt": time.Now().UTC()})
	_, err := tx.ExecContext(ctx, `UPDATE factory_work_items SET state=$2,document=document || $3::jsonb,updated_at=now() WHERE id=$1`, w.ID, state, patch)
	return err
}
func block(ctx context.Context, tx *sql.Tx, id, message string) error {
	_, err := tx.ExecContext(ctx, `UPDATE factory_publication_jobs SET state='blocked',error=$2,lease=NULL,lease_until=NULL,updated_at=now() WHERE work_id=$1`, id, message)
	if err != nil {
		return err
	}
	patch, _ := json.Marshal(map[string]any{"error": message, "updatedAt": time.Now().UTC()})
	_, err = tx.ExecContext(ctx, `UPDATE factory_work_items SET document=document || $2::jsonb,updated_at=now() WHERE id=$1`, id, patch)
	return err
}

func (c *Coordinator) finish(ctx context.Context, t *task, result githubapp.Publication, callErr error) error {
	state, message := "succeeded", ""
	var reported error
	if callErr == nil && (result.ID <= 0 || (t.Kind != "comment" && result.Number <= 0)) {
		callErr = githubapp.ErrPublicationUncertain
	}
	if callErr != nil {
		state = "unknown"
		message = "Publication outcome unknown; reconciliation only. An absent marker does not authorize another write."
		reported = githubapp.ErrPublicationUncertain
		if !errors.Is(callErr, githubapp.ErrPublicationUncertain) && (errors.Is(callErr, githubapp.ErrInvalidPublication) || errors.Is(callErr, githubapp.ErrPublicationConflict) || errors.Is(callErr, githubapp.ErrNotAllowed)) {
			state = "blocked"
			switch {
			case errors.Is(callErr, githubapp.ErrInvalidPublication):
				message = "Invalid publication payload; inspect the approved specification and GitHub title/body limits."
			case errors.Is(callErr, githubapp.ErrPublicationConflict):
				message = "Publication marker conflicts with existing GitHub evidence; operator investigation required."
			case errors.Is(callErr, githubapp.ErrNotAllowed):
				message = "Publication denied by account/repository binding or issuesWrite policy; operator investigation required."
			}
			reported = ErrBlocked
		}
		var apiErr *githubapp.APIError
		if errors.As(callErr, &apiErr) {
			message += fmt.Sprintf(" Upstream HTTP status %d.", apiErr.StatusCode)
			if !errors.Is(callErr, githubapp.ErrPublicationUncertain) {
				switch apiErr.StatusCode {
				case 400, 401, 403, 404, 422:
					state = "blocked"
					message = fmt.Sprintf("Publication rejected with HTTP status %d; operator investigation required.", apiErr.StatusCode)
					reported = ErrBlocked
				}
			}
		}
	}
	blocked := state == "blocked"
	if blocked && t.reconcile {
		// A fixed reconciliation failure says nothing about the earlier write.
		// Stop polling, but keep its external outcome explicitly unknown.
		state = "unknown"
		message = "Prior publication outcome remains unknown. " + message
	}
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	var account, workState string
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT account_id,revision,state,document FROM factory_work_items WHERE id=$1 FOR UPDATE`, t.Work.ID).Scan(&account, &revision, &workState, &raw)
	if err != nil {
		return err
	}
	var current factory.Work
	if err = json.Unmarshal(raw, &current); err != nil {
		return err
	}
	var owned bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_publication_jobs j JOIN factory_publication_operations o USING(work_id) WHERE j.work_id=$1 AND j.lease=$2 AND o.lease=$2 AND o.operation_key=$3 AND j.lease_until>now() AND o.lease_until>now())`, t.Work.ID, t.token, t.key).Scan(&owned)
	if err != nil {
		return err
	}
	if !owned {
		return factory.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE factory_publication_operations SET state=$3,publication_id=$4,issue_number=$5,error=$6,lease=NULL,lease_until=NULL,completed_at=CASE WHEN $3='succeeded' THEN now() ELSE NULL END WHERE work_id=$1 AND operation_key=$2`, t.Work.ID, t.key, state, result.ID, result.Number, message)
	if err != nil {
		return err
	}
	if account != t.Authority.AccountID || revision != t.Work.Revision || fence(current) != t.fence || current.State != workState || (workState != "approved_queued" && workState != "publishing_issues") {
		// Preserve late outcome evidence without overwriting the changed work document.
		_, err = tx.ExecContext(ctx, `UPDATE factory_publication_jobs SET state='blocked',error=$2,lease=NULL,lease_until=NULL,updated_at=now() WHERE work_id=$1`, t.Work.ID, ErrFence.Error())
		if err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return ErrFence
	}
	if blocked {
		if err = block(ctx, tx, t.Work.ID, message); err != nil {
			return err
		}
	} else {
		records, e := readRecords(ctx, tx, t.Work.ID)
		if e != nil {
			return e
		}
		complete := state == "succeeded" && t.Kind == "comment"
		if err = progress(ctx, tx, t.Work, records, complete, message); err != nil {
			return err
		}
		jobState := "active"
		if complete {
			jobState = "complete"
		}
		delay := 0
		if state == "unknown" {
			delay = 30
		}
		_, err = tx.ExecContext(ctx, `UPDATE factory_publication_jobs SET state=$2,error=$3,lease=NULL,lease_until=NULL,next_at=now()+$4*interval '1 second',updated_at=now() WHERE work_id=$1`, t.Work.ID, jobState, message, delay)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return reported
}
