package controller

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func presetColumns() []string {
	return []string{"name", "revision", "markdown", "length", "created_at", "updated_at", "default"}
}

func presetRow(name string, revision int64, markdown string, isDefault bool) []driver.Value {
	return []driver.Value{name, revision, markdown, len(markdown), time.Now(), time.Now(), false}
}

func TestInstructionPresetPutCreateUpdateAndIdempotentReapply(t *testing.T) {
	s, m := testStore(t)
	p := Principal{AccountID: "a", UserID: "u", Role: "owner"}
	// create
	m.ExpectBegin()
	m.ExpectQuery(`SELECT markdown,revision`).WithArgs("a", "general").WillReturnError(sql.ErrNoRows)
	m.ExpectExec(`INSERT INTO instruction_presets`).WithArgs("a", "general", "# one", int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery(`SELECT p.name,p.revision`).WillReturnRows(sqlmock.NewRows(presetColumns()).AddRow(presetRow("general", 1, "# one", true)...))
	m.ExpectCommit()
	saved, created, err := s.PutInstructionPreset(context.Background(), p, "general", v1.PutInstructionPresetRequest{Markdown: "# one"})
	if err != nil || !created || saved.Revision != 1 {
		t.Fatalf("create: %v %+v", err, saved)
	}
	// update to new content bumps the revision
	m.ExpectBegin()
	m.ExpectQuery(`SELECT markdown,revision`).WithArgs("a", "general").WillReturnRows(sqlmock.NewRows([]string{"markdown", "revision"}).AddRow("# one", int64(1)))
	m.ExpectExec(`INSERT INTO instruction_presets`).WithArgs("a", "general", "# two", int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery(`SELECT p.name,p.revision`).WillReturnRows(sqlmock.NewRows(presetColumns()).AddRow(presetRow("general", 2, "# two", true)...))
	m.ExpectCommit()
	saved, created, err = s.PutInstructionPreset(context.Background(), p, "general", v1.PutInstructionPresetRequest{Markdown: "# two"})
	if err != nil || created || saved.Revision != 2 {
		t.Fatalf("update: %v %+v", err, saved)
	}
	// reapplying unchanged content keeps its revision (idempotent reapply)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT markdown,revision`).WithArgs("a", "general").WillReturnRows(sqlmock.NewRows([]string{"markdown", "revision"}).AddRow("# two", int64(2)))
	m.ExpectQuery(`SELECT p.name,p.revision`).WillReturnRows(sqlmock.NewRows(presetColumns()).AddRow(presetRow("general", 2, "# two", true)...))
	m.ExpectCommit()
	saved, created, err = s.PutInstructionPreset(context.Background(), p, "general", v1.PutInstructionPresetRequest{Markdown: "# two"})
	if err != nil || created || saved.Revision != 2 {
		t.Fatalf("idempotent reapply: %v %+v", err, saved)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInstructionPresetExpectedRevisionConflicts(t *testing.T) {
	s, m := testStore(t)
	stale := int64(1)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT markdown,revision`).WithArgs("a", "general").WillReturnRows(sqlmock.NewRows([]string{"markdown", "revision"}).AddRow("# two", int64(3)))
	m.ExpectRollback()
	_, _, err := s.PutInstructionPreset(context.Background(), Principal{AccountID: "a", Role: "owner"}, "general", v1.PutInstructionPresetRequest{Markdown: "# three", ExpectedRevision: &stale})
	if !errors.Is(err, errInstructionPresetConflict) {
		t.Fatalf("stale revision must conflict: %v", err)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInstructionSelectionWithoutDefaultResolvesNone(t *testing.T) {
	s, m := testStore(t)
	p := Principal{AccountID: "a", Role: "owner"}
	m.ExpectQuery(`SELECT preset_name FROM instruction_preset_defaults`).WithArgs("a").WillReturnError(sql.ErrNoRows)
	resolved, err := s.resolveInstructionSelection(context.Background(), p, nil, true)
	if err != nil || resolved.Source != "none" || resolved.Markdown != "" {
		t.Fatalf("nil selection without default must resolve to none: %v %+v", err, resolved)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInstructionSelectionAccountDefaultResolvesPreset(t *testing.T) {
	s, m := testStore(t)
	p := Principal{AccountID: "a", Role: "owner"}
	m.ExpectQuery(`SELECT preset_name FROM instruction_preset_defaults`).WithArgs("a").WillReturnRows(sqlmock.NewRows([]string{"preset_name"}).AddRow("general"))
	m.ExpectQuery(`SELECT p.name,p.revision`).WillReturnRows(sqlmock.NewRows(presetColumns()).AddRow(presetRow("general", 3, "# rules", true)...))
	resolved, err := s.resolveInstructionSelection(context.Background(), p, nil, true)
	if err != nil || resolved.Source != "preset" || resolved.Preset != "general" || resolved.PresetRevision != 3 || resolved.Markdown != "# rules" || resolved.Modified {
		t.Fatalf("default resolution: %v %+v", err, resolved)
	}
	// explicit none beats the account default... via a non-nil selection
	none := &v1.InstructionSelection{None: true}
	resolved, err = s.resolveInstructionSelection(context.Background(), p, none, true)
	if err != nil || resolved.Source != "none" || resolved.Markdown != "" {
		t.Fatalf("explicit none must win over any default: %v %+v", err, resolved)
	}
	// preset with an edited box copy keeps provenance and marks the edit
	m.ExpectQuery(`SELECT p.name,p.revision`).WillReturnRows(sqlmock.NewRows(presetColumns()).AddRow(presetRow("general", 3, "# rules", true)...))
	resolved, err = s.resolveInstructionSelection(context.Background(), p, &v1.InstructionSelection{Preset: "general", Markdown: "# rules\nextra"}, false)
	if err != nil || resolved.Source != "preset" || !resolved.Modified || resolved.Preset != "general" || resolved.PresetRevision != 3 || resolved.Markdown != "# rules\nextra" {
		t.Fatalf("edited copy: %v %+v", err, resolved)
	}
	// verbatim copy is not flagged modified
	m.ExpectQuery(`SELECT p.name,p.revision`).WillReturnRows(sqlmock.NewRows(presetColumns()).AddRow(presetRow("general", 3, "# rules", true)...))
	resolved, err = s.resolveInstructionSelection(context.Background(), p, &v1.InstructionSelection{Preset: "general", Markdown: "# rules"}, false)
	if err != nil || resolved.Modified {
		t.Fatalf("verbatim copy must not be marked modified: %v %+v", err, resolved)
	}
	// custom markdown without a preset
	resolved, err = s.resolveInstructionSelection(context.Background(), p, &v1.InstructionSelection{Markdown: "# custom"}, false)
	if err != nil || resolved.Source != "custom" || resolved.Markdown != "# custom" || resolved.Preset != "" {
		t.Fatalf("custom selection: %v %+v", err, resolved)
	}
	// missing preset is a clean 400-class error
	m.ExpectQuery(`SELECT p.name,p.revision`).WillReturnError(sql.ErrNoRows)
	if _, err := s.resolveInstructionSelection(context.Background(), p, &v1.InstructionSelection{Preset: "ghost"}, false); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing preset must produce a not-found error: %v", err)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInstructionSnapshotMissingRowBehavesAsNone(t *testing.T) {
	s, m := testStore(t)
	p := Principal{AccountID: "a", Role: "owner"}
	m.ExpectQuery(`SELECT source,COALESCE`).WithArgs("a", "legacy").WillReturnError(sql.ErrNoRows)
	legacy, err := s.InstructionSnapshot(context.Background(), p, "legacy")
	if err != nil || legacy.Source != "none" || legacy.Markdown != "" {
		t.Fatalf("legacy boxes must read as explicit none: %v %+v", err, legacy)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestInstructionPresetsPostgres proves the durability contract against a real
// disposable database: box snapshots survive preset edits and deletion, the
// account default follows the preset lifecycle, and account boundaries hold.
// Run with VMBOX_TEST_DATABASE_URL pointing at a disposable database.
func TestInstructionPresetsPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ns := "instruction_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = s.DB.ExecContext(ctx, "CREATE SCHEMA "+ns); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(context.Background(), "DROP SCHEMA "+ns+" CASCADE")
	if _, err = s.DB.ExecContext(ctx, "SET search_path TO "+ns); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.Bootstrap(ctx, "instructions-a", "owner-a", uuid())
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.Bootstrap(ctx, "instructions-b", "owner-b", uuid())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.PutInstructionPreset(ctx, p, "general", v1.PutInstructionPresetRequest{Markdown: "# first"}); err != nil {
		t.Fatal(err)
	}
	box := uuid()
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,'instructions-box','railway','hibernated','vol-1','vol-1')`, box, p.AccountID, p.UserID); err != nil {
		t.Fatal(err)
	}
	// snapshot a box against preset revision 1
	resolved, err := s.resolveInstructionSelection(ctx, p, &v1.InstructionSelection{Preset: "general"}, false)
	if err != nil || resolved.PresetRevision != 1 {
		t.Fatalf("resolve: %v %+v", err, resolved)
	}
	if err = s.PutInstructionSnapshot(ctx, p, box, resolved); err != nil {
		t.Fatal(err)
	}
	// New boxes carry a generated section separately from the user's snapshot.
	// Editing their instructions must retain it; old boxes above stay empty.
	newBox := uuid()
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,'new-instructions-box','railway','hibernated','vol-2','vol-2')`, newBox, p.AccountID, p.UserID); err != nil {
		t.Fatal(err)
	}
	guidance := newBoxToolGuidance([]string{"desktop", "blender"})
	if err = s.PutNewBoxInstructionSnapshot(ctx, p, newBox, resolved, guidance); err != nil {
		t.Fatal(err)
	}
	newSnapshot, err := s.InstructionSnapshot(ctx, p, newBox)
	if err != nil || newSnapshot.Markdown != "# first" || newSnapshot.ToolGuidance != guidance {
		t.Fatalf("new box guidance must be separate: %v %+v", err, newSnapshot)
	}
	if err = s.PutInstructionSnapshot(ctx, p, newBox, v1.InstructionResolution{Source: "custom", Markdown: "# revised"}); err != nil {
		t.Fatal(err)
	}
	newSnapshot, err = s.InstructionSnapshot(ctx, p, newBox)
	if err != nil || newSnapshot.Markdown != "# revised" || newSnapshot.ToolGuidance != guidance {
		t.Fatalf("box edit must preserve generated guidance: %v %+v", err, newSnapshot)
	}
	// editing the preset bumps the revision but must not touch the box snapshot
	if _, _, err = s.PutInstructionPreset(ctx, p, "general", v1.PutInstructionPresetRequest{Markdown: "# second version"}); err != nil {
		t.Fatal(err)
	}
	revised, err := s.GetInstructionPreset(ctx, p, "general")
	if err != nil || revised.Revision != 2 {
		t.Fatalf("edit must bump the revision: %v %+v", err, revised)
	}
	snapshot, err := s.InstructionSnapshot(ctx, p, box)
	if err != nil || snapshot.Markdown != "# first" || snapshot.PresetRevision != 1 {
		t.Fatalf("preset edits must not silently modify existing boxes: %v %+v", err, snapshot)
	}
	// deleting the preset preserves the box snapshot and clears the default
	if err = s.DeleteInstructionPreset(ctx, p, "general"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = s.InstructionSnapshot(ctx, p, box)
	if err != nil || snapshot.Source != "preset" || snapshot.Preset != "general" || snapshot.PresetRevision != 1 || snapshot.Markdown != "# first" {
		t.Fatalf("preset deletion must never alter an existing box snapshot: %v %+v", err, snapshot)
	}
	var defaults int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM instruction_preset_defaults WHERE account_id=$1`, p.AccountID).Scan(&defaults); err != nil || defaults != 0 {
		t.Fatalf("deleting the preset must clear the account default: %v %d", err, defaults)
	}
	// cross-account isolation for box snapshots
	other, err := s.InstructionSnapshot(ctx, q, box)
	if err != nil || other.Source != "none" || other.Markdown != "" {
		t.Fatalf("cross-account snapshot read must behave as none: %v %+v", err, other)
	}
	// revising the default path again without a preset: nil selection now resolves to none
	resolved, err = s.resolveInstructionSelection(ctx, p, nil, true)
	if err != nil || resolved.Source != "none" {
		t.Fatalf("deleted default must fall back to none: %v %+v", err, resolved)
	}
}

func TestInstructionPresetDeleteClearsNothingButDefault(t *testing.T) {
	s, m := testStore(t)
	p := Principal{AccountID: "a", Role: "owner"}
	m.ExpectExec(`DELETE FROM instruction_presets`).WithArgs("a", "ghost").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := s.DeleteInstructionPreset(context.Background(), p, "ghost"); !errors.Is(err, errInstructionPresetNotFound) {
		t.Fatalf("missing preset must be a not-found: %v", err)
	}
	m.ExpectExec(`DELETE FROM instruction_presets`).WithArgs("a", "general").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.DeleteInstructionPreset(context.Background(), p, "general"); err != nil {
		t.Fatal(err)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
