package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/secrets"
)

// typeDesktopSecret never returns a secret, even to an owner. The only output is
// insertion status; the worker verifies the actual focused browser destination.
func (s *Server) typeDesktopSecret(w http.ResponseWriter, r *http.Request, p Principal) {
	s.useDesktopSecret(w, r, p, false, secrets.PasswordPolicy{})
}

func (s *Server) ensureAgentDesktopSecret(w http.ResponseWriter, r *http.Request, p Principal) {
	var request struct {
		Purpose  string `json:"purpose"`
		Length   int    `json:"length,omitempty"`
		Alphabet string `json:"alphabet,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || request.Purpose != "new_account_password" || decoder.Decode(new(any)) != io.EOF {
		writeError(w, 400, fmt.Errorf("purpose must be new_account_password; existing credentials must be requested privately"))
		return
	}
	policy := secrets.PasswordPolicy{Length: request.Length, Alphabet: request.Alphabet}
	if err := policy.Validate(); err != nil {
		writeError(w, 400, err)
		return
	}
	s.useDesktopSecret(w, r, p, true, policy)
}

func (s *Server) useDesktopSecret(w http.ResponseWriter, r *http.Request, p Principal, ensure bool, policy secrets.PasswordPolicy) {
	w.Header().Set("Cache-Control", "no-store")
	key := r.PathValue("key")
	if !loginProfileName.MatchString(key) {
		writeError(w, 400, fmt.Errorf("invalid secret reference"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	box, err := s.Store.LogicalBox(ctx, p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || a.Box.State != "running" {
		writeError(w, 409, fmt.Errorf("desktop is offline"))
		return
	}
	scope, agentRequest := ctx.Value(desktopAgentScopeKey{}).(desktopAgentScope)
	if p.Role == "desktop-agent" && (!agentRequest || scope.Fence != a.FencingToken) {
		writeError(w, 401, fmt.Errorf("desktop assignment authorization expired"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, fmt.Errorf("worker unavailable"))
		return
	}
	if s.Store.Envelope == nil {
		writeError(w, 503, fmt.Errorf("secret encryption unavailable"))
		return
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, 503, fmt.Errorf("secret service unavailable"))
		return
	}
	defer tx.Rollback()
	// Keep reassignment and deletion from racing the private worker transport.
	var locked string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM logical_boxes WHERE id=$1 AND account_id=$2 AND state='running' AND fencing_token=$3 AND assignment_generation=$4 FOR UPDATE`, box.ID, p.AccountID, a.FencingToken, a.Box.AssignmentGeneration).Scan(&locked)
	if err != nil {
		writeError(w, 409, fmt.Errorf("box assignment changed"))
		return
	}
	if agentRequest {
		var bound string
		err = tx.QueryRowContext(ctx, `SELECT box_id::text FROM desktop_agent_tokens WHERE box_id=$1 AND account_id=$2 AND fencing_token=$3 AND token_hash=$4 FOR SHARE`, box.ID, p.AccountID, scope.Fence, scope.TokenHash[:]).Scan(&bound)
		if err != nil {
			writeError(w, 401, fmt.Errorf("desktop authorization changed"))
			return
		}
	}
	if ensure {
		var output boundedCapture
		result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "desktop-password-origin", nativeFence(a)}, provider.ExecOptions{Stdout: &output, Stderr: io.Discard})
		if output.Len() == 0 && len(result.Stdout) <= 4096 {
			output.WriteString(result.Stdout)
		}
		var destination struct {
			Origin string `json:"origin"`
		}
		if err != nil || result.ExitCode != 0 || output.Len() > 4096 || json.Unmarshal(output.Bytes(), &destination) != nil {
			writeError(w, 409, fmt.Errorf("focus the destination password field before requesting a new password"))
			return
		}
		origin, err := secretOrigin(destination.Origin)
		if err != nil {
			writeError(w, 409, fmt.Errorf("secure browser destination unavailable"))
			return
		}
		if requested, _ := ctx.Value(privateSecretRequestKey{}).(bool); requested {
			_, err = tx.ExecContext(ctx, `INSERT INTO desktop_secret_requests(account_id,box_id,secret_key,origin) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, p.AccountID, box.ID, key, origin)
			if err != nil {
				writeError(w, 500, fmt.Errorf("could not request private credential"))
				return
			}
			var bound, status string
			err = tx.QueryRowContext(ctx, `SELECT origin,status FROM desktop_secret_requests WHERE account_id=$1 AND box_id=$2 AND secret_key=$3`, p.AccountID, box.ID, key).Scan(&bound, &status)
			if err != nil || bound != origin {
				writeError(w, 409, fmt.Errorf("request reference belongs to another destination"))
				return
			}
			if tx.Commit() != nil {
				writeError(w, 409, fmt.Errorf("request outcome uncertain; retry the same reference"))
				return
			}
			writeJSON(w, 200, map[string]string{"key": key, "origin": origin, "status": status})
			return
		}
		secret, created, err := s.Store.ensureDesktopSecret(ctx, tx, p, box.ID, key, origin, nil, policy)
		if err != nil {
			writeError(w, 409, fmt.Errorf("secret reference conflicts or could not be saved"))
			return
		}
		if err = tx.Commit(); err != nil {
			writeError(w, 409, fmt.Errorf("secret save outcome uncertain; retry the same reference"))
			return
		}
		writeJSON(w, 200, map[string]any{"key": secret.Key, "origin": secret.Origin, "created": created, "status": secret.Status})
		return
	}
	var origin, sealed string
	err = tx.QueryRowContext(ctx, `SELECT origin,encrypted_value FROM desktop_secrets WHERE account_id=$1 AND box_id=$2 AND secret_key=$3 FOR SHARE`, p.AccountID, box.ID, key).Scan(&origin, &sealed)
	if err != nil {
		writeError(w, 404, fmt.Errorf("secret reference unavailable"))
		return
	}
	plain, err := s.Store.Envelope.Open(desktopSecretScope(p.AccountID, box.ID, key, origin), sealed)
	if err != nil {
		writeError(w, 503, fmt.Errorf("secret could not be resolved"))
		return
	}
	defer clear(plain)
	payload, err := json.Marshal(boxruntime.DesktopPassword{Origin: origin, Value: string(plain)})
	if err != nil {
		writeError(w, 500, fmt.Errorf("private transport unavailable"))
		return
	}
	defer clear(payload)
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "desktop-type-secret", nativeFence(a)}, provider.ExecOptions{Stdin: bytes.NewReader(payload), Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || result.ExitCode != 0 {
		writeError(w, 409, fmt.Errorf("password entry failed; check desktop control, site and focused password field"))
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 409, fmt.Errorf("entry outcome uncertain; inspect the browser before retrying"))
		return
	}
	// Insertion is not evidence that a website accepted an account/password change.
	writeJSON(w, 200, map[string]bool{"inserted": true})
}
