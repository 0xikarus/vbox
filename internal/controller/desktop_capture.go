package controller

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

const desktopCaptureLimit = 16 << 20

type boundedCapture struct{ bytes.Buffer }

func (b *boundedCapture) Write(p []byte) (int, error) {
	if len(p) > desktopCaptureLimit-b.Len() {
		return 0, fmt.Errorf("desktop capture exceeds limit")
	}
	return b.Buffer.Write(p)
}

func readDesktopCapture(ctx context.Context, prov provider.Provider, service, assignment string, thumbnail bool) ([]byte, error) {
	command := "desktop-screenshot"
	if thumbnail {
		command = "desktop-thumbnail"
	}
	var capture boundedCapture
	result, err := prov.Exec(ctx, service, []string{"vmbox-runtime", command, assignment}, provider.ExecOptions{Stdout: &capture, Stderr: io.Discard})
	if err != nil || result.ExitCode != 0 {
		return nil, fmt.Errorf("desktop capture unavailable")
	}
	// Some providers return collected output rather than writing to the sink.
	if capture.Len() == 0 {
		if _, err = capture.Write([]byte(result.Stdout)); err != nil {
			return nil, err
		}
	}
	config, err := png.DecodeConfig(bytes.NewReader(capture.Bytes()))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
		return nil, fmt.Errorf("invalid worker screenshot")
	}
	if thumbnail && (config.Width > 320 || config.Height > 200) {
		return nil, fmt.Errorf("invalid worker thumbnail dimensions")
	}
	return capture.Bytes(), nil
}

func (s *Server) desktopScreenshot(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	box, err := s.Store.LogicalBox(ctx, p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || a.Box.State != "running" {
		writeError(w, 409, fmt.Errorf("desktop is offline; preview does not wake the box"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, fmt.Errorf("worker unavailable"))
		return
	}
	pixels, err := readDesktopCapture(ctx, prov, a.Slot.ServiceID, nativeFence(a), r.URL.Query().Get("thumbnail") == "true")
	if err != nil {
		writeError(w, 409, err)
		return
	}
	current, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || current.Box.State != "running" || nativeFence(current) != nativeFence(a) {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Captured-At", time.Now().UTC().Format(time.RFC3339Nano))
	w.Write(pixels)
}
