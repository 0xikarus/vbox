package railway

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// PostgresRequestBudget serializes permits by credential digest using database
// time. Waiting never holds a transaction or reserves future permits that could
// burst after cancellation/restart. A failed database operation denies the call.
type PostgresRequestBudget struct {
	DB       *sql.DB
	Interval time.Duration
	Hourly   int
}

var quotaScopePattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (b *PostgresRequestBudget) Acquire(ctx context.Context, scope string) error {
	if b.DB == nil || !quotaScopePattern.MatchString(scope) {
		return errors.New("Railway request budget unavailable")
	}
	for {
		wait, err := b.permit(ctx, scope)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("Railway request budget unavailable")
		}
		if wait <= 0 {
			return nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (b *PostgresRequestBudget) permit(ctx context.Context, scope string) (time.Duration, error) {
	tx, err := b.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO railway_api_budgets(quota_scope) VALUES($1) ON CONFLICT DO NOTHING`, scope); err != nil {
		return 0, err
	}
	var next, cooldown, now time.Time
	var remote sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT next_request_at,cooldown_until,remote_hourly_limit FROM railway_api_budgets WHERE quota_scope=$1 FOR UPDATE`, scope).Scan(&next, &cooldown, &remote); err != nil {
		return 0, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM railway_api_requests WHERE quota_scope=$1 AND requested_at<=$2`, scope, now.Add(-time.Hour)); err != nil {
		return 0, err
	}
	var count, backgroundCount, foregroundCount int
	var oldest, backgroundOldest, foregroundOldest sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT count(*),min(requested_at),count(*) FILTER (WHERE background),min(requested_at) FILTER (WHERE background),count(*) FILTER (WHERE NOT background),min(requested_at) FILTER (WHERE NOT background) FROM railway_api_requests WHERE quota_scope=$1`, scope).Scan(&count, &oldest, &backgroundCount, &backgroundOldest, &foregroundCount, &foregroundOldest); err != nil {
		return 0, err
	}
	hourly := b.Hourly
	if hourly <= 0 {
		hourly = 80
	}
	if remote.Valid {
		hourly = min(hourly, int(remote.Int64))
	}
	ready := next
	if cooldown.After(ready) {
		ready = cooldown
	}
	if count >= hourly && oldest.Valid && oldest.Time.Add(time.Hour).After(ready) {
		ready = oldest.Time.Add(time.Hour)
	}
	background := isBackgroundRequest(ctx)
	classCount, classLimit, classOldest := foregroundCount, foregroundHourlyLimit(hourly), foregroundOldest
	if background {
		classCount, classLimit, classOldest = backgroundCount, backgroundHourlyLimit(hourly), backgroundOldest
	}
	if classCount >= classLimit {
		if !classOldest.Valid {
			if classReady := now.Add(time.Hour); classReady.After(ready) {
				ready = classReady
			}
		} else if classOldest.Time.Add(time.Hour).After(ready) {
			ready = classOldest.Time.Add(time.Hour)
		}
	}
	if ready.After(now) {
		return ready.Sub(now), tx.Commit()
	}
	interval := b.Interval
	if interval <= 0 {
		interval = time.Second
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO railway_api_requests(quota_scope,requested_at,background) VALUES($1,$2,$3)`, scope, now, background); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE railway_api_budgets SET next_request_at=$2 WHERE quota_scope=$1`, scope, now.Add(interval)); err != nil {
		return 0, err
	}
	return 0, tx.Commit()
}

func (b *PostgresRequestBudget) Observe(ctx context.Context, scope string, status int, headers http.Header) error {
	if b.DB == nil || !quotaScopePattern.MatchString(scope) {
		return errors.New("Railway request budget unavailable")
	}
	var now time.Time
	if err := b.DB.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return errors.New("Railway request budget unavailable")
	}
	until := time.Unix(0, 0)
	if status == 429 || headers.Get("X-RateLimit-Remaining") == "0" {
		until = apiRetryAt(headers, now)
	}
	var limit any
	if reported, err := strconv.Atoi(headers.Get("X-RateLimit-Limit")); err == nil && reported > 0 && reported <= 100000000 {
		limit = max(1, reported*8/10)
	}
	_, err := b.DB.ExecContext(ctx, `INSERT INTO railway_api_budgets(quota_scope,cooldown_until,remote_hourly_limit) VALUES($1,$2,$3) ON CONFLICT(quota_scope) DO UPDATE SET cooldown_until=GREATEST(railway_api_budgets.cooldown_until,EXCLUDED.cooldown_until),remote_hourly_limit=LEAST(railway_api_budgets.remote_hourly_limit,EXCLUDED.remote_hourly_limit)`, scope, until, limit)
	if err != nil {
		return errors.New("Railway request budget unavailable")
	}
	return nil
}
