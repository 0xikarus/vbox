package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// Starting the desktop launches Xtigervnc, openbox, the panel, the icon set and
// one viewer per managed session. The worker's own budget for the VNC socket is
// 15s, so a 25s request budget used to expire while the worker was still making
// progress and reported that as a component problem.
const (
	desktopStartBudget  = 75 * time.Second
	desktopStatusBudget = 25 * time.Second
	desktopEnableBudget = 3 * time.Minute
)

func (s *Server) startDesktop(w http.ResponseWriter, r *http.Request, p Principal) {
	s.desktopAction(w, r, p, "desktop-start")
}
func (s *Server) enableDesktop(w http.ResponseWriter, r *http.Request, p Principal) {
	s.desktopAction(w, r, p, "desktop-enable")
}
func (s *Server) desktopStatus(w http.ResponseWriter, r *http.Request, p Principal) {
	s.desktopAction(w, r, p, "desktop-status")
}

// desktopFailureDetail turns worker stderr into one short, printable sentence.
// The desktop commands only ever write diagnostics there, never credentials.
func desktopFailureDetail(stderr string) string {
	lines := strings.FieldsFunc(stderr, func(r rune) bool { return r == '\n' || r == '\r' })
	detail := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			detail = trimmed
			break
		}
	}
	detail = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, detail)
	if len(detail) > 300 {
		detail = detail[:300] + "…"
	}
	return detail
}

// desktopExhaustionHint recognises a worker that cannot fork. The desktop needs
// several new processes, so an exhausted cgroup pid limit surfaces here first
// even though nothing about the box or its packages is wrong.
func desktopExhaustionHint(detail string) string {
	lowered := strings.ToLower(detail)
	for _, marker := range []string{"resource temporarily unavailable", "eagain", "pthread_create", "cannot fork", "fork failed", "too many processes"} {
		if strings.Contains(lowered, marker) {
			return "; the worker cannot start new processes (its cgroup pid limit is exhausted) — restart the worker"
		}
	}
	return ""
}

func (s *Server) desktopAction(w http.ResponseWriter, r *http.Request, p Principal, command string) {
	duration := desktopStartBudget
	switch command {
	case "desktop-enable":
		duration = desktopEnableBudget
	case "desktop-status":
		duration = desktopStatusBudget
	}
	ctx, cancel := context.WithTimeout(r.Context(), duration)
	defer cancel()
	box, err := s.Store.LogicalBox(ctx, p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || a.Box.State != "running" {
		writeError(w, 409, fmt.Errorf("resume the box before starting its desktop"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	verb := strings.TrimPrefix(command, "desktop-")
	argv := []string{"vmbox-runtime", command, nativeFence(a)}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, argv, provider.ExecOptions{})
	// A restarted worker keeps the box running but loses the tmux server that
	// carried its assignment, so every desktop command fails until the fence is
	// put back. Repair it once and retry rather than making the reader run an
	// unrelated-looking "enable sessions" call to get their desktop back.
	if err == nil && result.ExitCode != 0 && workerFenceLost(result.Stderr) {
		if repairErr := s.rebindWorkerAssignment(ctx, p.AccountID, a, prov); repairErr != nil {
			writeError(w, 409, fmt.Errorf("desktop %s failed: the worker no longer holds this box's assignment and it could not be repaired: %w", verb, repairErr))
			return
		}
		result, err = prov.Exec(ctx, a.Slot.ServiceID, argv, provider.ExecOptions{})
	}
	if err != nil {
		// A cancelled request and an unreachable worker are different problems;
		// reporting either as a missing desktop component sends the reader to
		// the wrong place.
		if ctx.Err() != nil && r.Context().Err() == nil {
			writeError(w, 504, fmt.Errorf("desktop %s did not finish within %s; the worker may still be starting it — retry, and inspect the vmbox-desktop session if it keeps timing out", verb, duration))
			return
		}
		writeError(w, 502, fmt.Errorf("desktop %s could not reach the worker: %w", verb, err))
		return
	}
	if result.ExitCode != 0 {
		// The worker already explains itself ("desktop components unavailable",
		// "desktop startup timed out", a package manager error). Forward that
		// instead of replacing every cause with one guess.
		detail := desktopFailureDetail(result.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit status %d with no diagnostics; inspect the vmbox-desktop session", result.ExitCode)
		}
		writeError(w, 409, fmt.Errorf("desktop %s failed on the worker: %s%s", verb, detail, desktopExhaustionHint(detail)))
		return
	}
	current, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || nativeFence(current) != nativeFence(a) {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	if command == "desktop-status" {
		var status struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
			writeError(w, 502, fmt.Errorf("invalid desktop status from worker"))
			return
		}
		writeJSON(w, 200, status)
		return
	}
	w.WriteHeader(204)
}
