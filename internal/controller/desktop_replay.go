package controller

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"sync"
	"time"
)

const (
	desktopReplayInterval = 30 * time.Second
	desktopReplayWindow   = 30 * time.Minute
	desktopReplayMaxBytes = 256 << 10
)

type desktopReplayFrameInfo struct {
	ID         string    `json:"id"`
	CapturedAt time.Time `json:"capturedAt"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
}

// The worker captures the PNG from its own desktop. Keeping a bounded JPEG
// avoids multiplying full-resolution PNGs by 60 frames per active box.
func encodeDesktopReplay(pixels []byte) ([]byte, int, int, error) {
	source, err := png.Decode(bytes.NewReader(pixels))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode desktop capture: %w", err)
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width < 1 || height < 1 || width > 4096 || height > 4096 {
		return nil, 0, 0, fmt.Errorf("invalid desktop capture dimensions")
	}
	if width > 960 {
		height = max(1, height*960/width)
		width = 960
	}
	scaled := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			scaled.Set(x, y, source.At(bounds.Min.X+x*bounds.Dx()/width, bounds.Min.Y+y*bounds.Dy()/height))
		}
	}
	for _, quality := range []int{72, 55, 38, 24} {
		var output bytes.Buffer
		if err := jpeg.Encode(&output, scaled, &jpeg.Options{Quality: quality}); err != nil {
			return nil, 0, 0, err
		}
		if output.Len() <= desktopReplayMaxBytes {
			return output.Bytes(), width, height, nil
		}
	}
	return nil, 0, 0, fmt.Errorf("desktop replay frame exceeds storage limit")
}

func (s *Server) startDesktopReplayRecorder(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(desktopReplayInterval)
		defer ticker.Stop()
		for {
			if err := s.ReconcileDesktopReplayNow(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Warn("desktop replay reconciliation failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Server) ReconcileDesktopReplayNow(ctx context.Context) error {
	if _, err := s.Store.DB.ExecContext(ctx, `DELETE FROM desktop_replay_frames WHERE captured_at < $1`, time.Now().UTC().Add(-desktopReplayWindow)); err != nil {
		return err
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT b.account_id::text,b.id::text FROM logical_boxes b
 WHERE b.state='running' AND b.slot_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM desktop_replay_frames f WHERE f.box_id=b.id AND f.captured_at>now()-interval '25 seconds'
 ) ORDER BY b.updated_at,b.id LIMIT 64`)
	if err != nil {
		return err
	}
	type candidate struct{ accountID, boxID string }
	var candidates []candidate
	for rows.Next() {
		var value candidate
		if err := rows.Scan(&value.accountID, &value.boxID); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var work sync.WaitGroup
	capacity := make(chan struct{}, 4)
	for _, value := range candidates {
		select {
		case <-ctx.Done():
			work.Wait()
			return ctx.Err()
		case capacity <- struct{}{}:
		}
		work.Add(1)
		go func(value candidate) {
			defer work.Done()
			defer func() { <-capacity }()
			attempt, cancel := context.WithTimeout(ctx, 18*time.Second)
			defer cancel()
			if err := s.captureDesktopReplay(attempt, value.accountID, value.boxID); err != nil && attempt.Err() == nil {
				s.Logger.Debug("desktop replay capture unavailable", "box", value.boxID, "error", err)
			}
		}(value)
	}
	work.Wait()
	return nil
}

func (s *Server) captureDesktopReplay(ctx context.Context, accountID, boxID string) error {
	assignment, err := s.Store.assignment(ctx, accountID, boxID)
	if err != nil || assignment.Box.State != "running" || assignment.Slot.ID == "" {
		return fmt.Errorf("desktop is offline")
	}
	prov, err := s.provider(ctx, accountID, assignment.Box.Provider, assignment.Box.ProviderCredential)
	if err != nil {
		return err
	}
	pixels, err := readDesktopCapture(ctx, prov, assignment.Slot.ServiceID, nativeFence(assignment), false)
	if err != nil {
		return err
	}
	frame, width, height, err := encodeDesktopReplay(pixels)
	if err != nil {
		return err
	}
	current, err := s.Store.assignment(ctx, accountID, boxID)
	if err != nil || current.Box.State != "running" || nativeFence(current) != nativeFence(assignment) {
		return fmt.Errorf("desktop assignment changed")
	}
	_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO desktop_replay_frames(id,account_id,box_id,captured_at,width,height,data)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(box_id,captured_at) DO NOTHING`, uuid(), accountID, boxID, time.Now().UTC().Truncate(desktopReplayInterval), width, height, frame)
	return err
}

func (s *Server) desktopReplayList(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("box unavailable"))
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id::text,captured_at,width,height FROM desktop_replay_frames
 WHERE account_id=$1 AND box_id=$2 AND captured_at>=$3
 ORDER BY captured_at DESC LIMIT 61`, p.AccountID, box.ID, time.Now().UTC().Add(-desktopReplayWindow))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("desktop replay unavailable"))
		return
	}
	defer rows.Close()
	frames := []desktopReplayFrameInfo{}
	for rows.Next() {
		var frame desktopReplayFrameInfo
		if err := rows.Scan(&frame.ID, &frame.CapturedAt, &frame.Width, &frame.Height); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("desktop replay unavailable"))
			return
		}
		frames = append(frames, frame)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("desktop replay unavailable"))
		return
	}
	for i, j := 0, len(frames)-1; i < j; i, j = i+1, j-1 {
		frames[i], frames[j] = frames[j], frames[i]
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, frames)
}

func (s *Server) desktopReplayFrame(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil || !historyMessageID.MatchString(r.PathValue("frame")) {
		writeError(w, http.StatusNotFound, fmt.Errorf("replay frame unavailable"))
		return
	}
	var pixels []byte
	var capturedAt time.Time
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT data,captured_at FROM desktop_replay_frames
 WHERE account_id=$1 AND box_id=$2 AND id=$3 AND captured_at>=$4`, p.AccountID, box.ID, r.PathValue("frame"), time.Now().UTC().Add(-desktopReplayWindow)).Scan(&pixels, &capturedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("replay frame unavailable"))
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Captured-At", capturedAt.UTC().Format(time.RFC3339Nano))
	w.Write(pixels)
}
