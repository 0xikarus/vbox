package railway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const railwayAPIEndpoint = "https://backboard.railway.com/graphql/v2"

// RequestBudget is shared by credential quota scope, independent of provider
// aliases, project IDs and short-lived Provider instances. Controller deployments
// can supply a database-backed implementation of the same gate.
type RequestBudget interface {
	Acquire(context.Context, string) error
	Observe(context.Context, string, int, http.Header) error
}

type requestPriorityKey struct{}

// BackgroundRequestContext marks provider work that may use only the
// non-reserved share of a credential's hourly quota. Cooldowns, pacing and the
// total provider limit still apply unchanged.
func BackgroundRequestContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestPriorityKey{}, true)
}

func isBackgroundRequest(ctx context.Context) bool {
	background, _ := ctx.Value(requestPriorityKey{}).(bool)
	return background
}

// Keep twenty percent (at least one request) available for user lifecycle and
// recovery work when the effective quota can support both classes. A quota of
// one cannot reserve capacity without starving background inventory forever, so
// both classes share that single permit.
func backgroundHourlyLimit(hourly int) int {
	if hourly <= 1 {
		return max(0, hourly)
	}
	reserved := max(1, (hourly+4)/5)
	return max(0, hourly-reserved)
}

func foregroundHourlyLimit(hourly int) int {
	if hourly <= 1 {
		return max(0, hourly)
	}
	// Leave one permit for inventory even if foreground lifecycle traffic is
	// sustained for the full rolling window.
	return hourly - 1
}

type APIError struct {
	Status    int
	Ambiguous bool
}

func (e *APIError) Error() string {
	if e.Ambiguous {
		return "Railway API operation outcome is ambiguous; reconcile before retrying"
	}
	return fmt.Sprintf("Railway API request failed (HTTP %d)", e.Status)
}

type RateLimitError struct{ RetryAt time.Time }

func (e *RateLimitError) Error() string {
	return "Railway API rate limited; infrastructure work must wait for cooldown"
}

type HTTPAPI struct {
	Token            string
	TokenEnvironment string
	Client           *http.Client
	Budget           RequestBudget
}

func (a *HTTPAPI) Do(ctx context.Context, document string, variables any) ([]byte, error) {
	if a.Token == "" || (a.TokenEnvironment != "RAILWAY_TOKEN" && a.TokenEnvironment != "RAILWAY_API_TOKEN") {
		return nil, errors.New("explicit Railway API credential required")
	}
	mutation := strings.HasPrefix(strings.TrimSpace(document), "mutation")
	body, err := json.Marshal(map[string]any{"query": document, "variables": variables})
	if err != nil {
		return nil, errors.New("invalid Railway API request")
	}
	if len(body) > 8*1024*1024 {
		return nil, errors.New("Railway API request too large")
	}
	digest := sha256.Sum256([]byte(a.Token))
	scope := hex.EncodeToString(digest[:])
	budget := a.Budget
	if budget == nil {
		budget = sharedAPIBudget
	}
	if err := budget.Acquire(ctx, scope); err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 30 * time.Second}
	if a.Client != nil {
		client = *a.Client
		if client.Timeout == 0 {
			client.Timeout = 30 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, railwayAPIEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid Railway API endpoint")
	}
	request.Header.Set("Content-Type", "application/json")
	if a.TokenEnvironment == "RAILWAY_TOKEN" {
		request.Header.Set("Project-Access-Token", a.Token)
	} else {
		request.Header.Set("Authorization", "Bearer "+a.Token)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, &APIError{Ambiguous: mutation}
	}
	defer response.Body.Close()
	// Record cooldown before parsing any provider-controlled body. Do not permit a
	// failed shared-budget write to silently authorize subsequent requests.
	observeCtx, stopObserve := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	err = budget.Observe(observeCtx, scope, response.StatusCode, response.Header)
	stopObserve()
	if err != nil {
		return nil, &APIError{Status: response.StatusCode, Ambiguous: mutation}
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, &RateLimitError{RetryAt: apiRetryAt(response.Header, time.Now())}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &APIError{Status: response.StatusCode, Ambiguous: mutation}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 {
		return nil, &APIError{Status: response.StatusCode, Ambiguous: mutation}
	}
	var envelope struct {
		Data   json.RawMessage   `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(data, &envelope) != nil || len(envelope.Errors) > 0 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, &APIError{Status: response.StatusCode, Ambiguous: mutation}
	}
	return data, nil
}

func apiRetryAt(headers http.Header, now time.Time) time.Time {
	until := now.Add(time.Minute)
	if seconds, err := strconv.ParseInt(headers.Get("Retry-After"), 10, 64); err == nil && seconds >= 0 && seconds <= int64((365*24*time.Hour)/time.Second) {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(headers.Get("Retry-After")); err == nil && date.After(now) {
		until = date
	}
	if epoch, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil && epoch > 0 {
		reset := time.Unix(epoch, 0)
		if reset.After(until) {
			until = reset
		}
	}
	return until
}

type quotaPermit struct {
	at         time.Time
	background bool
}

type quotaWindow struct {
	next, cooldown time.Time
	requests       []quotaPermit
	limit          int
}
type LocalRequestBudget struct {
	mu       sync.Mutex
	scopes   map[string]*quotaWindow
	interval time.Duration
	hourly   int
}

// Conservative defaults fit the smallest documented hourly quota, with recovery
// headroom. Responses can lower the budget; higher configured limits are explicit.
var sharedAPIBudget = RequestBudget(NewLocalRequestBudget(time.Second, 80))

func NewLocalRequestBudget(interval time.Duration, hourly int) *LocalRequestBudget {
	if interval <= 0 {
		interval = time.Second
	}
	if hourly <= 0 {
		hourly = 80
	}
	return &LocalRequestBudget{scopes: map[string]*quotaWindow{}, interval: interval, hourly: hourly}
}
func (b *LocalRequestBudget) window(scope string) *quotaWindow {
	w := b.scopes[scope]
	if w == nil {
		w = &quotaWindow{limit: b.hourly}
		b.scopes[scope] = w
	}
	return w
}
func (b *LocalRequestBudget) Acquire(ctx context.Context, scope string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		now := time.Now()
		w := b.window(scope)
		for len(w.requests) > 0 && !w.requests[0].at.After(now.Add(-time.Hour)) {
			w.requests = w.requests[1:]
		}
		ready := w.next
		if w.cooldown.After(ready) {
			ready = w.cooldown
		}
		if len(w.requests) >= w.limit && w.requests[0].at.Add(time.Hour).After(ready) {
			ready = w.requests[0].at.Add(time.Hour)
		}
		background := isBackgroundRequest(ctx)
		classLimit := foregroundHourlyLimit(w.limit)
		if background {
			classLimit = backgroundHourlyLimit(w.limit)
		}
		classCount := 0
		var classOldest time.Time
		for _, permit := range w.requests {
			if permit.background == background {
				classCount++
				if classOldest.IsZero() {
					classOldest = permit.at
				}
			}
		}
		if classCount >= classLimit {
			if classOldest.IsZero() {
				if classReady := now.Add(time.Hour); classReady.After(ready) {
					ready = classReady
				}
			} else if classOldest.Add(time.Hour).After(ready) {
				ready = classOldest.Add(time.Hour)
			}
		}
		if !ready.After(now) {
			w.requests = append(w.requests, quotaPermit{at: now, background: background})
			w.next = now.Add(b.interval)
			b.mu.Unlock()
			return nil
		}
		b.mu.Unlock()
		timer := time.NewTimer(time.Until(ready))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (b *LocalRequestBudget) Observe(ctx context.Context, scope string, status int, headers http.Header) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	w := b.window(scope)
	if limit, err := strconv.Atoi(headers.Get("X-RateLimit-Limit")); err == nil && limit > 0 {
		w.limit = min(w.limit, max(1, limit*8/10))
	}
	if status == 429 || headers.Get("X-RateLimit-Remaining") == "0" {
		until := apiRetryAt(headers, time.Now())
		if until.After(w.cooldown) {
			w.cooldown = until
		}
	}
	return nil
}
