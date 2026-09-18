package controller

// Instruction presets: reusable, named Markdown instruction sets owned by an
// account, kept separately from encrypted login profiles. Boxes keep immutable
// snapshots with preset/version provenance, so preset edits or deletion never
// silently change an existing box. Instructions are trusted user-authored
// agent guidance, not secrets and not installation scripts.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

var errInstructionPresetNotFound = errors.New("instruction preset not found")
var errInstructionPresetConflict = errors.New("instruction preset changed concurrently; reload and retry")

func (s *Store) ListInstructionPresets(ctx context.Context, p Principal) (v1.InstructionPresetList, error) {
	list := v1.InstructionPresetList{Presets: []v1.InstructionPresetMeta{}}
	_ = s.DB.QueryRowContext(ctx, `SELECT preset_name FROM instruction_preset_defaults WHERE account_id=$1`, p.AccountID).Scan(&list.DefaultName)
	rows, err := s.DB.QueryContext(ctx, `SELECT name,revision,length(markdown::text),created_at,updated_at FROM instruction_presets WHERE account_id=$1 ORDER BY name`, p.AccountID)
	if err != nil {
		return list, err
	}
	defer rows.Close()
	for rows.Next() {
		var meta v1.InstructionPresetMeta
		if err := rows.Scan(&meta.Name, &meta.Revision, &meta.SizeBytes, &meta.CreatedAt, &meta.UpdatedAt); err != nil {
			return list, err
		}
		meta.Default = meta.Name == list.DefaultName
		list.Presets = append(list.Presets, meta)
	}
	return list, rows.Err()
}

func (s *Store) GetInstructionPreset(ctx context.Context, p Principal, name string) (v1.InstructionPreset, error) {
	var preset v1.InstructionPreset
	err := s.DB.QueryRowContext(ctx, `SELECT p.name,p.revision,p.markdown,length(p.markdown::text),p.created_at,p.updated_at,(d.preset_name IS NOT NULL)
		FROM instruction_presets p LEFT JOIN instruction_preset_defaults d ON d.account_id=p.account_id AND d.preset_name=p.name
		WHERE p.account_id=$1 AND p.name=$2`, p.AccountID, name).
		Scan(&preset.Name, &preset.Revision, &preset.Markdown, &preset.SizeBytes, &preset.CreatedAt, &preset.UpdatedAt, &preset.Default)
	if errors.Is(err, sql.ErrNoRows) {
		return preset, errInstructionPresetNotFound
	}
	return preset, err
}

func (s *Store) PutInstructionPreset(ctx context.Context, p Principal, name string, request v1.PutInstructionPresetRequest) (v1.InstructionPreset, bool, error) {
	if err := v1.ValidateInstructionPresetName(name); err != nil {
		return v1.InstructionPreset{}, false, err
	}
	if err := v1.ValidateInstructionMarkdown(request.Markdown); err != nil {
		return v1.InstructionPreset{}, false, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return v1.InstructionPreset{}, false, err
	}
	defer tx.Rollback()
	var current string
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT markdown,revision FROM instruction_presets WHERE account_id=$1 AND name=$2 FOR UPDATE`, p.AccountID, name).Scan(&current, &revision)
	created := errors.Is(err, sql.ErrNoRows)
	if err != nil && !created {
		return v1.InstructionPreset{}, false, err
	}
	if created {
		revision = 0
		current = "\x00never-equal\x00"
	}
	if request.ExpectedRevision != nil && *request.ExpectedRevision != revision {
		return v1.InstructionPreset{}, false, errInstructionPresetConflict
	}
	if current == request.Markdown {
		// Unchanged content keeps its revision: reapplying is idempotent.
	} else {
		revision++
		result, execErr := tx.ExecContext(ctx, `INSERT INTO instruction_presets(account_id,name,markdown,revision) VALUES($1,$2,$3,$4)
			ON CONFLICT(account_id,name) DO UPDATE SET markdown=$3,revision=$4,updated_at=now()`, p.AccountID, name, request.Markdown, revision)
		if execErr != nil {
			return v1.InstructionPreset{}, false, execErr
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return v1.InstructionPreset{}, false, fmt.Errorf("could not save instruction preset")
		}
	}
	preset, err := scanInstructionPreset(tx.QueryRowContext(ctx, `SELECT p.name,p.revision,p.markdown,length(p.markdown::text),p.created_at,p.updated_at,(d.preset_name IS NOT NULL)
		FROM instruction_presets p LEFT JOIN instruction_preset_defaults d ON d.account_id=p.account_id AND d.preset_name=p.name
		WHERE p.account_id=$1 AND p.name=$2`, p.AccountID, name))
	if err != nil {
		return v1.InstructionPreset{}, false, err
	}
	return preset, created, tx.Commit()
}

func scanInstructionPreset(row *sql.Row) (v1.InstructionPreset, error) {
	var preset v1.InstructionPreset
	err := row.Scan(&preset.Name, &preset.Revision, &preset.Markdown, &preset.SizeBytes, &preset.CreatedAt, &preset.UpdatedAt, &preset.Default)
	return preset, err
}

func (s *Store) DeleteInstructionPreset(ctx context.Context, p Principal, name string) error {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM instruction_presets WHERE account_id=$1 AND name=$2`, p.AccountID, name)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errInstructionPresetNotFound
	}
	return nil
}

func (s *Store) SetInstructionDefault(ctx context.Context, p Principal, name string) error {
	if name == "" {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM instruction_preset_defaults WHERE account_id=$1`, p.AccountID)
		return err
	}
	if err := v1.ValidateInstructionPresetName(name); err != nil {
		return err
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO instruction_preset_defaults(account_id,preset_name) VALUES($1,$2)
		ON CONFLICT(account_id) DO UPDATE SET preset_name=$2,updated_at=now()`, p.AccountID, name)
	if err != nil {
		return fmt.Errorf("choose an existing instruction preset as the default")
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("could not save the default instruction preset")
	}
	return nil
}

// resolveInstructionSelection turns the validated selection into a snapshot,
// resolving preset content and the account default. A nil selection consults
// the account default when allowed (creation time only).
func (s *Store) resolveInstructionSelection(ctx context.Context, p Principal, selection *v1.InstructionSelection, allowAccountDefault bool) (v1.InstructionResolution, error) {
	if selection == nil {
		if !allowAccountDefault {
			return v1.InstructionResolution{Source: "none"}, nil
		}
		var name string
		err := s.DB.QueryRowContext(ctx, `SELECT preset_name FROM instruction_preset_defaults WHERE account_id=$1`, p.AccountID).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) || name == "" {
			return v1.InstructionResolution{Source: "none"}, nil
		}
		if err != nil {
			return v1.InstructionResolution{}, err
		}
		preset, err := s.GetInstructionPreset(ctx, p, name)
		if err != nil {
			return v1.InstructionResolution{}, fmt.Errorf("account default instruction preset %s is unavailable", name)
		}
		return v1.PresetSnapshot(preset, ""), nil
	}
	if selection.None {
		return v1.InstructionResolution{Source: "none"}, nil
	}
	if selection.Preset != "" {
		preset, err := s.GetInstructionPreset(ctx, p, selection.Preset)
		if errors.Is(err, errInstructionPresetNotFound) {
			return v1.InstructionResolution{}, fmt.Errorf("instruction preset %s not found", selection.Preset)
		}
		if err != nil {
			return v1.InstructionResolution{}, err
		}
		return v1.PresetSnapshot(preset, selection.Markdown), nil
	}
	return v1.InstructionResolution{Source: "custom", Markdown: selection.Markdown}, nil
}

// PutInstructionSnapshot records the box's resolved snapshot. applied_at resets
// so materialization happens on the next attach (or immediately when pushed).
func (s *Store) PutInstructionSnapshot(ctx context.Context, p Principal, boxID string, resolved v1.InstructionResolution) error {
	preset := sql.NullString{}
	if resolved.Preset != "" {
		preset = sql.NullString{String: resolved.Preset, Valid: true}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO box_instruction_snapshots(account_id,box_id,source,preset_name,preset_revision,modified,markdown,applied_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULL)
		ON CONFLICT(account_id,box_id) DO UPDATE SET source=$3,preset_name=$4,preset_revision=$5,modified=$6,markdown=$7,applied_at=NULL,updated_at=now()`,
		p.AccountID, boxID, resolved.Source, preset, resolved.PresetRevision, resolved.Modified, resolved.Markdown)
	return err
}

// InstructionSnapshot loads the box's snapshot; a missing row behaves like an
// explicit none and is exposed as such to older boxes.
func (s *Store) InstructionSnapshot(ctx context.Context, p Principal, boxID string) (v1.BoxInstructions, error) {
	value := v1.BoxInstructions{Source: "none"}
	var preset sql.NullString
	var revision sql.NullInt64
	var appliedAt sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT source,COALESCE(preset_name,''),COALESCE(preset_revision,0),modified,markdown,updated_at,applied_at
		FROM box_instruction_snapshots WHERE account_id=$1 AND box_id=$2`, p.AccountID, boxID).
		Scan(&value.Source, &preset, &revision, &value.Modified, &value.Markdown, &value.UpdatedAt, &appliedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	value.Preset, value.PresetRevision = preset.String, revision.Int64
	if appliedAt.Valid {
		t := appliedAt.Time
		value.AppliedAt = &t
	}
	return value, nil
}

func (s *Store) MarkInstructionsApplied(ctx context.Context, accountID, boxID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE box_instruction_snapshots SET applied_at=now() WHERE account_id=$1 AND box_id=$2`, accountID, boxID)
	return err
}

// syncBoxInstructions pushes the box's snapshot into the box filesystem. It is
// safe to run on every attach: the runtime reconciles idempotently from the
// same canonical content.
func (s *Server) syncBoxInstructions(ctx context.Context, prov provider.Provider, accountID, boxID, serviceID string) error {
	snapshot, err := s.Store.InstructionSnapshot(ctx, Principal{AccountID: accountID}, boxID)
	if err != nil {
		return err
	}
	if snapshot.Source == "none" && snapshot.Markdown == "" && snapshot.AppliedAt == nil && snapshot.UpdatedAt.IsZero() {
		// Boxes that predate this feature carry no row: leave their volume alone.
		return nil
	}
	payload, err := json.Marshal(struct {
		Markdown string `json:"markdown"`
	}{Markdown: snapshot.Markdown})
	if err != nil {
		return err
	}
	result, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "sync-instructions"}, provider.ExecOptions{Stdin: bytes.NewReader(payload)})
	expected := fmt.Sprintf("%x", sha256.Sum256(payload))
	if err != nil || result.ExitCode != 0 || trimLine(result.Stdout) != expected {
		return fmt.Errorf("instruction transfer failed integrity verification (status %d): %w", result.ExitCode, err)
	}
	return s.Store.MarkInstructionsApplied(ctx, accountID, boxID)
}

func trimLine(value string) string {
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r') {
		value = value[:len(value)-1]
	}
	return value
}

/* ---------- HTTP handlers ---------- */

func (s *Server) listInstructionPresets(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	list, err := s.Store.ListInstructionPresets(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not list instruction presets"))
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getInstructionPreset(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	preset, err := s.Store.GetInstructionPreset(r.Context(), p, r.PathValue("name"))
	if errors.Is(err, errInstructionPresetNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not load instruction preset"))
		return
	}
	writeJSON(w, http.StatusOK, v1.InstructionPresetResponse{Preset: preset})
}

func (s *Server) putInstructionPreset(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, v1.MaxInstructionMarkdownBytes+16*1024)
	var request v1.PutInstructionPresetRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid instruction preset payload"))
		return
	}
	preset, created, err := s.Store.PutInstructionPreset(r.Context(), p, r.PathValue("name"), request)
	if errors.Is(err, errInstructionPresetConflict) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, v1.InstructionPresetResponse{Preset: preset})
}

func (s *Server) deleteInstructionPreset(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	if err := s.Store.DeleteInstructionPreset(r.Context(), p, r.PathValue("name")); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setInstructionDefault(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	var request v1.SetInstructionDefaultRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Store.SetInstructionDefault(r.Context(), p, request.Name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// boxInstructions shows the box's snapshot plus how it relates to the live
// preset, so an explicit Apply action can preview exactly what would change.
func (s *Server) boxInstructions(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	response, err := s.boxInstructionsState(r.Context(), p, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not load box instructions"))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) boxInstructionsState(ctx context.Context, p Principal, boxID string) (v1.BoxInstructionsResponse, error) {
	var response v1.BoxInstructionsResponse
	snapshot, err := s.Store.InstructionSnapshot(ctx, p, boxID)
	if err != nil {
		return response, err
	}
	response.Instructions = snapshot
	if snapshot.Preset != "" {
		state := &v1.PresetState{}
		if preset, err := s.Store.GetInstructionPreset(ctx, p, snapshot.Preset); err == nil {
			state.Exists = true
			state.Revision = preset.Revision
			state.Stale = preset.Revision > snapshot.PresetRevision
			state.ContentDrift = preset.Markdown != snapshot.Markdown
		}
		response.Preset = state
	}
	response.Pending = snapshot.Source != "none" && (snapshot.AppliedAt == nil || snapshot.AppliedAt.Before(snapshot.UpdatedAt))
	return response, nil
}

// applyBoxInstructions is the explicit Apply instructions action for an
// existing box. It replaces the box snapshot and, when the box is running,
// materializes it immediately without restarting any agent process. Agents
// pick up changed instructions on their next conversation or process start;
// already-running sessions are never restarted automatically.
func (s *Server) applyBoxInstructions(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, v1.MaxInstructionMarkdownBytes+16*1024)
	var selection v1.InstructionSelection
	if err := decodeJSON(r, &selection); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid instruction selection"))
		return
	}
	if err := selection.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	resolved, err := s.Store.resolveInstructionSelection(r.Context(), p, &selection, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Store.PutInstructionSnapshot(r.Context(), p, box.ID, resolved); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not save box instructions"))
		return
	}
	response, err := s.boxInstructionsState(r.Context(), p, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not load box instructions"))
		return
	}
	if box.State != v1.LogicalBoxRunning {
		response.Note = "Saved. Instructions materialize automatically when the box starts; running agents are never restarted."
		writeJSON(w, http.StatusOK, response)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	assignment, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || assignment.Slot.ServiceID == "" {
		writeError(w, http.StatusConflict, fmt.Errorf("box assignment is unavailable; retry when the box is running"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if err := s.syncBoxInstructions(ctx, prov, p.AccountID, box.ID, assignment.Slot.ServiceID); err != nil {
		response, _ = s.boxInstructionsState(r.Context(), p, box.ID)
		response.Pending = true
		response.Note = "Saved, but the running box could not be updated (" + err.Error() + "). It applies automatically on the next start."
		writeJSON(w, http.StatusOK, response)
		return
	}
	current, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || nativeFence(current) != nativeFence(assignment) {
		writeError(w, http.StatusConflict, fmt.Errorf("assignment changed while applying instructions"))
		return
	}
	response, _ = s.boxInstructionsState(r.Context(), p, box.ID)
	response.Note = "Applied to the running box. New conversations and launched agents read it at process start; running sessions keep their loaded instructions until restarted by you."
	writeJSON(w, http.StatusOK, response)
}

// resyncBoxInstructions re-materializes the snapshot the box already carries. It
// stores nothing new: a worker replacement, a restored hibernation or a sync
// that failed earlier can leave a running box behind its saved instructions, and
// re-entering the same Markdown only to trigger a write is a poor way to fix it.
func (s *Server) resyncBoxInstructions(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	response, err := s.boxInstructionsState(r.Context(), p, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not load box instructions"))
		return
	}
	if box.State != v1.LogicalBoxRunning {
		response.Pending = true
		response.Note = "Nothing to re-sync while the box is " + string(box.State) + "; its instructions materialize when it starts."
		writeJSON(w, http.StatusOK, response)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	assignment, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || assignment.Slot.ServiceID == "" {
		writeError(w, http.StatusConflict, fmt.Errorf("box assignment is unavailable; retry when the box is running"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if err := s.syncBoxInstructions(ctx, prov, p.AccountID, box.ID, assignment.Slot.ServiceID); err != nil {
		response.Pending = true
		response.Note = "Re-sync failed (" + err.Error() + "). The instructions apply on the next start."
		writeJSON(w, http.StatusOK, response)
		return
	}
	current, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || nativeFence(current) != nativeFence(assignment) {
		writeError(w, http.StatusConflict, fmt.Errorf("assignment changed while re-syncing instructions"))
		return
	}
	response, _ = s.boxInstructionsState(r.Context(), p, box.ID)
	response.Note = "Re-synced to the running box. New conversations and launched agents read it at process start; running sessions keep what they loaded."
	writeJSON(w, http.StatusOK, response)
}
