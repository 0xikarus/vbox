package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"time"
)

// pruneStaleUnusedAttachments releases uploads left outside chat messages for
// more than a day. This leaves time to submit an open composer draft. Keep
// referenced media even after its capability URL expires: chat history serves
// those attachments through the authenticated message endpoint.
func (s *Store) pruneStaleUnusedAttachments(ctx context.Context, accountID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM run_once_images i
		WHERE i.account_id=$1 AND (i.expires_at<=now() OR i.created_at<=now()-interval '24 hours')
		AND NOT EXISTS (SELECT 1 FROM box_message_images j WHERE j.image_id=i.id)`, accountID)
	return err
}

const (
	maxImageUpload = 25 << 20
	maxVideoUpload = 100 << 20
	// maxAccountAttachmentBytes bounds all attachments kept for one account.
	maxAccountAttachmentBytes = 1 << 30
)

var errAccountAttachmentQuota = errors.New("saved attachments reached the 1 GiB account limit; open Chat > Box details > Chat attachments to clear older media")

// mp4Brands are the `ftyp` brands the chat plays as MP4. Stills share the ISO
// base-media container — HEIC is `heic`, AVIF is `avif` — so the brand has to
// be checked: without it an AVIF would be stored as video/mp4 and, with
// nosniff, never render as either.
var mp4Brands = map[string]bool{
	"isom": true, "iso2": true, "iso4": true, "iso5": true, "iso6": true,
	"mp41": true, "mp42": true, "avc1": true, "dash": true, "mmp4": true, "M4V ": true,
}

// sniffVideo recognises the container formats the chat can play inline: an ISO
// base-media `ftyp` box with a video brand, or a Matroska/WebM header.
func sniffVideo(data []byte) string {
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) && mp4Brands[string(data[8:12])] {
		return "video/mp4"
	}
	if len(data) >= 4 && bytes.Equal(data[0:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		return "video/webm"
	}
	return ""
}

// validateRunOnceImage accepts the image and video formats the chat can show.
// Images are decoded and bounded; video is matched by container signature.
func validateRunOnceImage(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("attachment is empty")
	}
	if media := sniffVideo(data); media != "" {
		if len(data) > maxVideoUpload {
			return "", fmt.Errorf("video must be at most 100 MiB")
		}
		return media, nil
	}
	if len(data) > maxImageUpload {
		return "", fmt.Errorf("image must be at most 25 MiB")
	}
	cfg, kind, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
		return "", fmt.Errorf("use PNG, JPEG, GIF, MP4 or WebM up to 40 megapixels")
	}
	media := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}[kind]
	if media == "" {
		return "", fmt.Errorf("unsupported attachment format")
	}
	return media, nil
}

func (s *Server) uploadRunOnceImage(w http.ResponseWriter, r *http.Request, p Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, maxVideoUpload)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 413, fmt.Errorf("attachment exceeds 100 MiB"))
		return
	}
	media, err := validateRunOnceImage(data)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	data, media, err = optimizeStoredImage(r.Context(), data, media)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	// Commit cleanup separately so even a rejected over-quota upload frees old
	// unreferenced media for the next attempt.
	if err := s.Store.pruneStaleUnusedAttachments(r.Context(), p.AccountID); err != nil {
		writeError(w, 500, err)
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	var account string
	err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM accounts WHERE id=$1 FOR UPDATE`, p.AccountID).Scan(&account)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	var used int64
	err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(sum(octet_length(data)),0) FROM run_once_images WHERE account_id=$1`, account).Scan(&used)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if used+int64(len(data)) > maxAccountAttachmentBytes {
		writeError(w, 409, errAccountAttachmentQuota)
		return
	}
	id := uuid()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO run_once_images(id,account_id,media_type,data,download_token,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '7 days')`, id, account, media, data, rand.Text()+rand.Text())
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]string{"id": id})
}

// Capability grants read access to this media only, never controller access.
func (s *Server) downloadRunOnceImage(w http.ResponseWriter, r *http.Request) {
	var media string
	var data []byte
	token := r.URL.Query().Get("token")
	if len(token) < 32 {
		http.NotFound(w, r)
		return
	}
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT media_type,data FROM run_once_images WHERE id::text=$1 AND download_token=$2 AND expires_at>now()`, r.PathValue("id"), token).Scan(&media, &data)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// ServeContent adds Accept-Ranges and answers range requests (video seek).
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}
