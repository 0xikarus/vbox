package resultinbox

import (
	"errors"
	"io"
	"net/http"
	"strings"
)

// Handler exposes only POST /result. Mount behind TLS. The Authorization bearer
// is an attempt capability; it never grants controller or account access.
func (i *Inbox) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/result" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", 405)
			return
		}
		auth := r.Header.Values("Authorization")
		if len(auth) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		parts := strings.Split(auth[0], " ")
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) != 43 {
			http.Error(w, "unauthorized", 401)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var large *http.MaxBytesError
			if errors.As(err, &large) {
				http.Error(w, "body too large", 413)
			} else {
				http.Error(w, "invalid body", 400)
			}
			return
		}
		err = i.Accept(r.Context(), parts[1], body)
		status := http.StatusNoContent
		switch err {
		case nil:
		case ErrUnauthorized:
			status = 401
		case ErrExpired:
			status = 410
		case ErrConflict:
			status = 409
		case ErrInvalid:
			status = 400
		case ErrTooLarge:
			status = 413
		default:
			status = 503
		}
		if status != http.StatusNoContent {
			http.Error(w, http.StatusText(status), status)
			return
		}
		w.WriteHeader(status)
	})
}
