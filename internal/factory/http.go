package factory

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type Identity struct{ AccountID, UserID string }
type Profile struct {
	Application string `json:"application"`
	Name        string `json:"name"`
}
type AssetBackend interface {
	Put(context.Context, string, string, io.Reader) (AssetRef, error)
	Open(context.Context, string, string) (*os.File, AssetRef, error)
}
type RepositoryBackend interface {
	List(context.Context, string) ([]Repository, error)
	Resolve(context.Context, string, string, string) (Repository, string, error)
}

type Service struct {
	ExecutionReady   bool
	PublicationReady bool
	Store            *Store
	GatewayToken     string
	Assets           AssetBackend
	Repositories     RepositoryBackend
	Profiles         func(context.Context, string) ([]Profile, error)
	WorkerLimit      int
	// Images is enabled per agent only after its installed runtime adapter can
	// deliver actual visual inputs. File staging alone is not this capability.
	Images map[string]bool
}

func replyJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, message string) {
	replyJSON(w, status, map[string]string{"error": message})
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/factory/capabilities", s.auth(func(w http.ResponseWriter, r *http.Request, p Identity) {
		replyJSON(w, 200, map[string]any{"enabled": true, "executionReady": s.ExecutionReady, "publicationReady": s.PublicationReady, "imageTypes": []string{"image/png", "image/jpeg"}, "githubConfigured": s.Repositories != nil, "agents": []any{map[string]any{"name": "codex", "images": s.Images["codex"]}, map[string]any{"name": "claude", "images": s.Images["claude"]}}})
	}))
	mux.HandleFunc("GET /v1/factory/repositories", s.auth(func(w http.ResponseWriter, r *http.Request, p Identity) {
		if s.Repositories == nil {
			apiError(w, 503, "Connect a GitHub App installation first")
			return
		}
		v, err := s.Repositories.List(r.Context(), p.AccountID)
		if err != nil {
			apiError(w, 502, "Could not read authorized repositories")
			return
		}
		replyJSON(w, 200, v)
	}))
	mux.HandleFunc("GET /v1/factory/profiles", s.auth(func(w http.ResponseWriter, r *http.Request, p Identity) {
		if s.Profiles == nil {
			apiError(w, 503, "Planner profiles are not configured")
			return
		}
		v, err := s.Profiles(r.Context(), p.AccountID)
		if err != nil {
			apiError(w, 502, "Could not read planner profiles")
			return
		}
		replyJSON(w, 200, v)
	}))
	mux.HandleFunc("POST /v1/factory/assets", s.auth(s.upload))
	mux.HandleFunc("GET /v1/factory/assets/{id}", s.auth(s.download))
	mux.HandleFunc("GET /v1/factory/work-items", s.auth(func(w http.ResponseWriter, r *http.Request, p Identity) {
		v, err := s.Store.List(r.Context(), p.AccountID)
		if err != nil {
			apiError(w, 500, "Could not read work items")
			return
		}
		replyJSON(w, 200, v)
	}))
	mux.HandleFunc("GET /v1/factory/work-items/{id}", s.auth(func(w http.ResponseWriter, r *http.Request, p Identity) {
		v, err := s.Store.Get(r.Context(), p.AccountID, r.PathValue("id"))
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		replyJSON(w, 200, v)
	}))
	mux.HandleFunc("POST /v1/factory/work-items", s.auth(s.createWork))
	mux.HandleFunc("POST /v1/factory/work-items/{id}/messages", s.auth(s.addReply))
	mux.HandleFunc("POST /v1/factory/work-items/{id}/approve", s.auth(s.approve))
	return mux
}

func (s *Service) auth(next func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		got := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		want := sha256.Sum256([]byte("Bearer " + s.GatewayToken))
		p := Identity{AccountID: r.Header.Get("X-Vmbox-Account"), UserID: r.Header.Get("X-Vmbox-User")}
		if s.GatewayToken == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 || p.AccountID == "" || p.UserID == "" {
			apiError(w, 401, "Authenticated factory gateway required")
			return
		}
		next(w, r, p)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 300000)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		apiError(w, 400, "Invalid request JSON")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		apiError(w, 400, "Only one JSON object is allowed")
		return false
	}
	return true
}

func (s *Service) manifest(ctx context.Context, account string, ids []string) ([]AssetRef, error) {
	if len(ids) > 8 {
		return nil, fmt.Errorf("at most 8 images allowed")
	}
	out := []AssetRef{}
	seen := map[string]bool{}
	var total int64
	for _, id := range ids {
		if s.Assets == nil || id == "" || seen[id] {
			return nil, fmt.Errorf("invalid attachment selection")
		}
		seen[id] = true
		f, a, err := s.Assets.Open(ctx, account, id)
		if err != nil {
			return nil, fmt.Errorf("attachment unavailable")
		}
		f.Close()
		total += a.Size
		out = append(out, a)
	}
	if total > 40<<20 {
		return nil, fmt.Errorf("attachments exceed 40 MiB")
	}
	return out, nil
}

func (s *Service) createWork(w http.ResponseWriter, r *http.Request, p Identity) {
	if !s.ExecutionReady {
		apiError(w, 503, "Planning execution is not configured on this factory yet")
		return
	}
	var input CreateWork
	if !decodeBody(w, r, &input) {
		return
	}
	if err := input.Validate(); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if s.Repositories == nil || s.Profiles == nil {
		apiError(w, 503, "Configure repository access and planner profiles first")
		return
	}
	profiles, err := s.Profiles(r.Context(), p.AccountID)
	if err != nil {
		apiError(w, 502, "Planner profile lookup failed")
		return
	}
	found := false
	for _, profile := range profiles {
		if profile.Application == input.Agent && profile.Name == input.Profile {
			found = true
		}
	}
	if !found {
		apiError(w, 400, "Selected planner profile is unavailable")
		return
	}
	if len(input.AssetIDs) > 0 && !s.Images[input.Agent] {
		apiError(w, 409, "Selected planner cannot yet receive image inputs")
		return
	}
	assets, err := s.manifest(r.Context(), p.AccountID, input.AssetIDs)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	repo, sha, err := s.Repositories.Resolve(r.Context(), p.AccountID, input.RepositoryID, input.BaseRef)
	if err != nil {
		apiError(w, 403, "Repository or revision is unavailable to this account")
		return
	}
	v, err := s.Store.Create(r.Context(), p.AccountID, p.UserID, r.Header.Get("Idempotency-Key"), input, repo, sha, assets)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	replyJSON(w, 202, v)
}

func (s *Service) addReply(w http.ResponseWriter, r *http.Request, p Identity) {
	if !s.ExecutionReady {
		apiError(w, 503, "Planning execution is not configured on this factory yet")
		return
	}
	var input Reply
	if !decodeBody(w, r, &input) {
		return
	}
	current, err := s.Store.Get(r.Context(), p.AccountID, r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if len(input.AssetIDs) > 0 && !s.Images[current.Agent] {
		apiError(w, 409, "Selected planner cannot yet receive image inputs")
		return
	}
	assets, err := s.manifest(r.Context(), p.AccountID, input.AssetIDs)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	v, err := s.Store.Reply(r.Context(), p.AccountID, current.ID, r.Header.Get("Idempotency-Key"), input, assets)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	replyJSON(w, 202, v)
}

func (s *Service) approve(w http.ResponseWriter, r *http.Request, p Identity) {
	if !s.ExecutionReady || !s.PublicationReady {
		apiError(w, 503, "Approved-plan issue publication is not configured")
		return
	}
	var input Approval
	if !decodeBody(w, r, &input) {
		return
	}
	v, err := s.Store.Approve(r.Context(), p.AccountID, r.PathValue("id"), r.Header.Get("Idempotency-Key"), input, s.WorkerLimit)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	replyJSON(w, 202, v)
}

func (s *Service) upload(w http.ResponseWriter, r *http.Request, p Identity) {
	if s.Assets == nil {
		apiError(w, 503, "Private asset storage unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
	reader, err := r.MultipartReader()
	if err != nil {
		apiError(w, 400, "Upload multipart field file")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" {
		apiError(w, 400, "Upload one file field")
		return
	}
	defer part.Close()
	a, err := s.Assets.Put(r.Context(), p.AccountID, part.FileName(), part)
	if err != nil {
		apiError(w, 400, "Image rejected: invalid format, dimensions or size")
		return
	}
	// Partial uploads are private unreferenced assets until a work item binds them.
	if _, err = reader.NextPart(); err != io.EOF {
		apiError(w, 400, "Upload one image at a time")
		return
	}
	replyJSON(w, 201, a)
}

func (s *Service) download(w http.ResponseWriter, r *http.Request, p Identity) {
	if s.Assets == nil {
		apiError(w, 503, "Private asset storage unavailable")
		return
	}
	f, a, err := s.Assets.Open(r.Context(), p.AccountID, r.PathValue("id"))
	if err != nil {
		apiError(w, 404, "Attachment unavailable")
		return
	}
	defer f.Close()
	if a.MediaType != "image/png" && a.MediaType != "image/jpeg" && a.MediaType != "image/webp" {
		apiError(w, 500, "Invalid stored media type")
		return
	}
	w.Header().Set("Content-Type", a.MediaType)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	info, err := f.Stat()
	if err != nil {
		apiError(w, 500, "Attachment unavailable")
		return
	}
	http.ServeContent(w, r, strings.ReplaceAll(a.Name, "/", "_"), info.ModTime(), f)
}

func (s *Service) writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrConflict):
		apiError(w, 409, "Work changed or a request key was reused; refresh before retrying")
	case errors.Is(err, sql.ErrNoRows):
		apiError(w, 404, "Work item unavailable")
	default:
		apiError(w, 400, "Operation could not be accepted; check the plan, revision and request fields")
	}
}
