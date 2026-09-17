package controller

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
)

func validateRunOnceImage(data []byte) (string, error) {
	if len(data) == 0 || len(data) > 8<<20 {
		return "", fmt.Errorf("image must be 1 byte–8 MiB")
	}
	cfg, kind, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
		return "", fmt.Errorf("use PNG, JPEG or GIF up to 40 megapixels")
	}
	media := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}[kind]
	if media == "" {
		return "", fmt.Errorf("unsupported image format")
	}
	return media, nil
}

func (s *Server) uploadRunOnceImage(w http.ResponseWriter, r *http.Request, p Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 413, fmt.Errorf("image exceeds 8 MiB"))
		return
	}
	media, err := validateRunOnceImage(data)
	if err != nil {
		writeError(w, 400, err)
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
	if used+int64(len(data)) > 256<<20 {
		writeError(w, 409, fmt.Errorf("saved images reached the 256 MiB account storage limit"))
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

// Capability grants read access to this image only, never controller access.
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
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}
