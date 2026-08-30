package hostd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

type Server struct {
	Provider provider.Provider
	Token    string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/capacity", s.auth(func(w http.ResponseWriter, r *http.Request) {
		cap, err := s.Provider.Validate(r.Context())
		respond(w, cap, err)
	}))
	mux.HandleFunc("GET /v1/boxes", s.auth(func(w http.ResponseWriter, r *http.Request) {
		boxes, err := s.Provider.List(r.Context())
		respond(w, boxes, err)
	}))
	mux.HandleFunc("POST /v1/boxes", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req provider.CreateRequest
		if err := decode(r, &req); err != nil {
			respond(w, nil, err)
			return
		}
		box, err := s.Provider.Create(r.Context(), req)
		respond(w, box, err)
	}))
	mux.HandleFunc("POST /v1/boxes/{id}/exec", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Command []string `json:"command"`
			Detach  bool     `json:"detach"`
		}
		if err := decode(r, &req); err != nil {
			respond(w, nil, err)
			return
		}
		result, err := s.Provider.Exec(r.Context(), r.PathValue("id"), req.Command, provider.ExecOptions{Detach: req.Detach})
		respond(w, result, err)
	}))
	mux.HandleFunc("DELETE /v1/boxes/{id}", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var owner provider.Owner
		if err := decode(r, &owner); err != nil {
			respond(w, nil, err)
			return
		}
		respond(w, map[string]bool{"deleted": true}, s.Provider.Delete(r.Context(), r.PathValue("id"), owner))
	}))
	return mux
}
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token == "" || strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != s.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
func decode(r *http.Request, v any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}
func respond(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if value == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := json.NewEncoder(w).Encode(value); err != nil {
		fmt.Fprintln(w, err)
	}
}
