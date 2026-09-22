package controller

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

func (s *Server) agentEmailHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	key, err := requireIdempotency(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	boxID := r.PathValue("id")
	caps, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, boxID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	grant := caps.CreateEmail
	if err := requireCapability(grant.Enabled, "create_email_address"); err != nil {
		writeError(w, 403, err)
		return
	}
	var request struct {
		Domain      string `json:"domain"`
		AddressType string `json:"addressType"`
		LocalPart   string `json:"localPart,omitempty"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	request.Domain = strings.ToLower(strings.TrimSpace(request.Domain))
	request.AddressType = strings.ToLower(strings.TrimSpace(request.AddressType))
	request.LocalPart = strings.TrimSpace(request.LocalPart)
	if !slices.Contains(grant.Domains, request.Domain) || !slices.Contains(grant.AddressTypes, request.AddressType) {
		writeError(w, 403, fmt.Errorf("domain or address type is not granted by this box's roles"))
		return
	}
	if strings.ContainsAny(request.LocalPart, "@\r\n") || len(request.LocalPart) > 64 {
		writeError(w, 400, fmt.Errorf("invalid localPart"))
		return
	}
	id := ""
	var address, providerRef, state, failure, storedType, storedDomain, storedLocalPart string
	tx, err := s.Store.DB.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	var locked string
	// The box row is the per-actor quota lock shared by every email request.
	// Count and reservation insertion therefore happen as one decision.
	if err := tx.QueryRowContext(r.Context(), `SELECT id::text FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='running' FOR UPDATE`, p.AccountID, boxID).Scan(&locked); err != nil {
		writeError(w, 409, fmt.Errorf("creator box is unavailable"))
		return
	}
	err = tx.QueryRowContext(r.Context(), `SELECT id::text,COALESCE(address,''),COALESCE(provider_ref,''),state,COALESCE(failure_reason,''),address_type,domain,local_part FROM agent_email_addresses WHERE account_id=$1 AND creator_box_id=$2 AND idempotency_key=$3`, p.AccountID, boxID, key).Scan(&id, &address, &providerRef, &state, &failure, &storedType, &storedDomain, &storedLocalPart)
	existing := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, err)
		return
	}
	if existing && (storedType != request.AddressType || storedDomain != request.Domain || storedLocalPart != request.LocalPart) {
		writeError(w, 409, fmt.Errorf("idempotency key was already used with different email parameters"))
		return
	}
	if existing && state == "ready" {
		if err := tx.Commit(); err != nil {
			writeError(w, 500, err)
			return
		}
		w.Header().Set("Idempotency-Replayed", "true")
		writeJSON(w, 200, map[string]any{"id": id, "address": address, "providerRef": providerRef, "state": state})
		return
	}
	if strings.TrimSpace(s.EmailProvisionURL) == "" {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("email provider is not configured"))
		return
	}
	if !existing {
		var count int
		if err := tx.QueryRowContext(r.Context(), `SELECT count(*) FROM agent_email_addresses WHERE account_id=$1 AND creator_box_id=$2 AND state<>'failed'`, p.AccountID, boxID).Scan(&count); err != nil {
			writeError(w, 500, err)
			return
		}
		if count >= grant.MaxAddresses {
			writeError(w, 403, fmt.Errorf("email address limit reached"))
			return
		}
		id = uuid()
		err = tx.QueryRowContext(r.Context(), `INSERT INTO agent_email_addresses(id,account_id,creator_box_id,address_type,domain,local_part,state,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,'provisioning',$7) ON CONFLICT(account_id,creator_box_id,idempotency_key) DO NOTHING RETURNING id::text,state`, id, p.AccountID, boxID, request.AddressType, request.Domain, request.LocalPart, key).Scan(&id, &state)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(r.Context(), `SELECT id::text,address_type,domain,local_part,state FROM agent_email_addresses WHERE account_id=$1 AND creator_box_id=$2 AND idempotency_key=$3`, p.AccountID, boxID, key).Scan(&id, &storedType, &storedDomain, &storedLocalPart, &state)
			if err == nil && (storedType != request.AddressType || storedDomain != request.Domain || storedLocalPart != request.LocalPart) {
				err = fmt.Errorf("idempotency key was already used with different email parameters")
			}
		}
		if err != nil {
			writeError(w, 409, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 500, err)
		return
	}
	payload, _ := json.Marshal(map[string]any{"requestId": id, "accountId": p.AccountID, "creatorBoxId": boxID, "domain": request.Domain, "addressType": request.AddressType, "localPart": request.LocalPart})
	providerRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.EmailProvisionURL, bytes.NewReader(payload))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	providerRequest.Header.Set("Content-Type", "application/json")
	providerRequest.Header.Set("Idempotency-Key", id)
	if s.EmailProvisionToken != "" {
		providerRequest.Header.Set("Authorization", "Bearer "+s.EmailProvisionToken)
	}
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(providerRequest)
	if err != nil {
		s.failEmailProvision(r, p, id, err)
		writeError(w, 502, fmt.Errorf("email provider request failed"))
		return
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var result struct {
		Address string `json:"address"`
		ID      string `json:"id"`
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || json.Unmarshal(body, &result) != nil || strings.TrimSpace(result.Address) == "" {
		err = fmt.Errorf("email provider rejected provisioning")
		s.failEmailProvision(r, p, id, err)
		writeError(w, 502, err)
		return
	}
	_, err = s.Store.DB.ExecContext(r.Context(), `UPDATE agent_email_addresses SET address=$3,provider_ref=$4,state='ready',failure_reason=NULL,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, id, result.Address, result.ID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "address": result.Address, "providerRef": result.ID, "state": "ready"})
}

func (s *Server) failEmailProvision(r *http.Request, p Principal, id string, failure error) {
	_, _ = s.Store.DB.ExecContext(r.Context(), `UPDATE agent_email_addresses SET state='failed',failure_reason=$3,updated_at=now() WHERE account_id=$1 AND id=$2`, p.AccountID, id, failure.Error())
}
