package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/0xikarus/vmbox-service/internal/browser"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

// captchaAnswer applies the owner's solution from the chat's embedded widget to
// the managed browser. Owner-only: the agent never receives the token, and the
// runtime fences the operation to the current assignment.
func (s *Server) captchaAnswer(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	var request browser.CaptchaAnswer
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, 400, fmt.Errorf("invalid captcha answer"))
		return
	}
	if err := request.Validate(); err != nil {
		writeError(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	box, err := s.Store.LogicalBox(ctx, p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || a.Box.State != "running" {
		writeError(w, 409, fmt.Errorf("box is offline"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, fmt.Errorf("worker unavailable"))
		return
	}
	payload, err := json.Marshal(request)
	if err != nil {
		writeError(w, 500, fmt.Errorf("captcha answer unavailable"))
		return
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "captcha-answer", nativeFence(a)}, provider.ExecOptions{Stdin: bytes.NewReader(payload), Stderr: io.Discard})
	if err != nil || result.ExitCode != 0 {
		writeError(w, 409, fmt.Errorf("captcha answer could not be applied; is a challenge still open?"))
		return
	}
	current, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || current.Box.State != "running" || nativeFence(current) != nativeFence(a) {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	pixels, err := readDesktopCapture(ctx, prov, a.Slot.ServiceID, nativeFence(a), false)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	current, err = s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || current.Box.State != "running" || nativeFence(current) != nativeFence(a) {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(pixels)
}
