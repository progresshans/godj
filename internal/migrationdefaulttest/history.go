package migrationdefaulttest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func expectedHistoryDigest(t *testing.T, loaded migrations.LoadedDefinitionSet, key migrations.MigrationKey) []byte {
	t.Helper()
	for _, source := range loaded.Sources() {
		if source.Migration == key {
			suffix, ok := strings.CutPrefix(source.HistoryDigest, "sha256:")
			raw, err := hex.DecodeString(suffix)
			if !ok || err != nil || len(raw) != 32 {
				t.Fatalf("invalid loader digest for %v: %q", key, source.HistoryDigest)
			}
			return raw
		}
	}
	t.Fatalf("missing source for %v", key)
	return nil
}

// Observe both physical recorder formats independently of the fenced reader.
func (m fieldMigration) checkRecorder(t *testing.T, format int, binding []byte) {
	t.Helper()
	var version int
	if err := m.SQL.QueryRowContext(t.Context(), "SELECT format_version FROM "+m.table("godj_migration_revision")).Scan(&version); err != nil || version != format {
		t.Fatalf("recorder version = %d, want %d: %v", version, format, err)
	}
	rows := m.tableRows(t, "godj_migrations", "app, name")
	defer rows.Close()
	wantColumns := []string{"app", "name"}
	if format == 2 {
		wantColumns = append(wantColumns, "definition_digest")
	}
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, wantColumns) {
		t.Fatalf("recorder columns = %v, want %v: %v", columns, wantColumns, err)
	}
	wantKeys := []migrations.MigrationKey{m.initial}
	if binding != nil {
		wantKeys = append(wantKeys, m.change)
	}
	count := 0
	for rows.Next() {
		var key migrations.MigrationKey
		var raw []byte
		targets := []any{&key.App, &key.Name}
		if format == 2 {
			targets = append(targets, &raw)
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		if count >= len(wantKeys) || key != wantKeys[count] || (key == m.initial && raw != nil) || (key == m.change && !bytes.Equal(raw, binding)) {
			t.Fatalf("unexpected recorder row %d: %v %x", count, key, raw)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != len(wantKeys) {
		t.Fatalf("read %d recorder rows, want %d: %v", count, len(wantKeys), err)
	}
}

func checkDefinitionHistory(t *testing.T, open func() Fixture, constraint func(error) string) {
	t.Helper()
	m := newFieldMigration(t, open(), constraint, "external_id", false, false, nil)
	m.checkRecorder(t, 1, nil)
	m.fail(t, m.change, migrations.DirectionForward, "not_null")
	m.checkRecorder(t, 1, nil)
	m.exec(t, "UPDATE "+m.table("m1_ticket")+" SET external_id='filled' WHERE external_id IS NULL")
	if !m.SQLite {
		peer := open()
		reader, err := peer.SQL.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Rollback()
		var name string
		if err := reader.QueryRowContext(t.Context(), "SELECT name FROM "+m.table("godj_migrations")).Scan(&name); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		_, err = (migrations.Executor{Backend: m.Backend}).Migrate(ctx, m.loaded, migrations.LatestLifecycleRequest())
		cancel()
		var owner *migrations.Error
		if !errors.As(err, &owner) || owner.Code != migrations.CodeHistoryRevisionContended {
			t.Fatal("an existing PostgreSQL recorder reader did not report bounded lock contention", err)
		}
		if err := reader.Rollback(); err != nil {
			t.Fatal(err)
		}
		m.checkRecorder(t, 1, nil)
	}
	m.apply(t, m.change)
	receipt := expectedHistoryDigest(t, m.loaded, m.change)
	if err := m.SQL.Close(); err != nil {
		t.Fatal(err)
	}
	m.Fixture = open()
	m.checkRecorder(t, 2, receipt)
	for _, kind := range []mb.HistoryTransitionKind{mb.HistoryTransitionApply, mb.HistoryTransitionUnapply} {
		session, err := m.Backend.OpenRevisionFencedSession(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		recorded, err := session.ReadAppliedMigrations(t.Context())
		if err != nil || len(recorded) != 2 || recorded[0].HistoryDigest != "" || recorded[1].HistoryDigest != "sha256:"+hex.EncodeToString(receipt) {
			t.Fatal("fenced session lost the stored definition", recorded, err)
		}
		transition := mb.HistoryTransition{Kind: kind, Migration: mb.AppliedMigration{App: m.change.App, Name: m.change.Name}}
		tx, err := session.BeginMigration(t.Context(), transition, mb.MigrationIntent{Operations: []mb.MigrationOperation{}})
		if tx != nil {
			t.Fatal("an unbound transition opened a transaction against a bound row", tx.Rollback(t.Context()))
		}
		var fence *mb.RevisionFenceError
		if !errors.As(err, &fence) || fence.Kind != mb.RevisionFenceFailureIntegrity {
			t.Fatal("an unbound transition did not reject the existing bound identity", kind, err)
		}
		if err := session.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	latest := migrations.LatestLifecycleRequest()
	reverse := migrations.TargetedLifecycleRequest(migrations.NamedTarget(m.initial))
	executor := migrations.Executor{Backend: m.Backend}
	stable := m.state(t)
	expectRejection := func(loaded migrations.LoadedDefinitionSet, request migrations.LifecycleRequest, code migrations.ErrorCode) {
		t.Helper()
		_, planErr := executor.Plan(t.Context(), loaded, request)
		_, migrateErr := executor.Migrate(t.Context(), loaded, request)
		for _, err := range []error{planErr, migrateErr} {
			var owner *migrations.Error
			if !errors.As(err, &owner) || owner.Category != migrations.CategoryHistory || owner.Code != code {
				t.Fatalf("expected %s from both plan and migrate, got %v", code, err)
			}
			if code == migrations.CodeDefinitionHistoryMismatch && (owner.App != m.change.App || owner.Migration != m.change.Name) {
				t.Fatalf("history mismatch attributed to %s.%s, want %v", owner.App, owner.Migration, m.change)
			}
		}
		if !reflect.DeepEqual(m.state(t), stable) {
			t.Fatal("history rejection changed database state or revision")
		}
	}
	original := m.loaded.Definitions()
	expectRejection(loadChanges(t, original[0]), latest, migrations.CodeDefinitionHistoryMismatch)
	edited := m.loaded.Definitions()
	alter := edited[1].Operations[0].(migrations.AlterField)
	alter.After.Default = &ir.Scalar{Kind: ir.ScalarString, String: "changed default"}
	edited[1].Operations[0] = alter
	expectRejection(loadChanges(t, edited...), latest, migrations.CodeDefinitionHistoryMismatch)
	changed := m.loaded.Definitions()
	for i, op := range changed[0].Operations {
		create := op.(migrations.CreateModel)
		if create.Model.Name == "category" {
			for j := range create.Model.Fields {
				if create.Model.Fields[j].Name == "legacy_code" {
					create.Model.Fields[j].Blank = true
				}
			}
			changed[0].Operations[i] = create
		}
	}
	drifted := loadChanges(t, changed...)
	expectRejection(drifted, latest, migrations.CodeDefinitionHistoryMismatch)
	expectRejection(drifted, reverse, migrations.CodeDefinitionHistoryMismatch)
	m.checkRecorder(t, 2, receipt)
	extension := loadChanges(t, append(original,
		migrations.Migration{App: m.change.App, Name: "0003_later", Dependencies: []migrations.MigrationKey{m.change}},
		migrations.Migration{App: "independent", Name: "0001_unrelated"})...)
	steps, err := executor.Plan(t.Context(), extension, latest)
	if err != nil || len(steps) != 2 {
		t.Fatalf("extended catalog plan = %v, want only the two new migrations: %v", steps, err)
	}
	_, err = executor.Migrate(t.Context(), extension, migrations.TargetedLifecycleRequest(migrations.NamedTarget(m.change)))
	if err != nil || !reflect.DeepEqual(m.state(t), stable) {
		t.Fatal("adding descendants or an unrelated definition changed the applied prefix", err)
	}
	updateDigest := "UPDATE " + m.table("godj_migrations") + " SET definition_digest=$1 WHERE app=$2 AND name=$3"
	wrong := append([]byte(nil), receipt...)
	wrong[0] ^= 0xff
	for _, corrupt := range [][]byte{wrong, append(wrong, 0)} {
		m.exec(t, updateDigest, corrupt, m.change.App, m.change.Name)
		expectRejection(m.loaded, latest, migrations.CodeHistoryRevisionIntegrity)
		m.checkRecorder(t, 2, corrupt)
	}
	m.exec(t, updateDigest, receipt, m.change.App, m.change.Name)
	emptyBlob := "pg_catalog.decode('', 'hex')"
	if m.SQLite {
		emptyBlob = "zeroblob(0)"
	}
	legacyRow := " WHERE app=$1 AND name=$2"
	m.exec(t, "UPDATE "+m.table("godj_migrations")+" SET definition_digest="+emptyBlob+legacyRow, m.initial.App, m.initial.Name)
	expectRejection(m.loaded, latest, migrations.CodeHistoryRevisionIntegrity)
	m.exec(t, "UPDATE "+m.table("godj_migrations")+" SET definition_digest=NULL"+legacyRow, m.initial.App, m.initial.Name)
	m.apply(t, m.initial)
	m.checkRecorder(t, 2, nil)
	m.apply(t, m.change)
	m.checkRecorder(t, 2, receipt)
}

func checkBoundBootstrap(t *testing.T, f Fixture) {
	t.Helper()
	initial := migrations.Migration{App: "labels", Name: "0001", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "labels", Model: ir.Model{
			Name: "label", GoName: "Label", DBTable: "m1_label",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
		migrations.AddField{AppLabel: "labels", ModelName: "label", Field: ir.Field{
			Name: "enabled", GoName: "Enabled", Column: "enabled", Kind: ir.FieldBoolean, Default: &ir.Scalar{Kind: ir.ScalarBoolean, Boolean: true},
		}},
	}}
	loaded := loadChanges(t, initial)
	executor := migrations.Executor{Backend: f.Backend}
	apply := func(request migrations.LifecycleRequest) {
		t.Helper()
		if _, err := executor.Migrate(t.Context(), loaded, request); err != nil {
			t.Fatal(err)
		}
	}
	for attempt := int64(0); attempt < 2; attempt++ {
		apply(migrations.LatestLifecycleRequest())
		var raw []byte
		err := f.SQL.QueryRowContext(t.Context(), "SELECT definition_digest FROM "+f.table("godj_migrations")+" WHERE app='labels' AND name='0001'").Scan(&raw)
		if err != nil || "sha256:"+hex.EncodeToString(raw) != loaded.Sources()[0].HistoryDigest {
			t.Fatal("first migration did not store its exact source", raw, err)
		}
		apply(migrations.TargetedLifecycleRequest(migrations.ZeroTarget(initial.App)))
		var format int
		var revision int64
		err = f.SQL.QueryRowContext(t.Context(), "SELECT format_version, revision FROM "+f.table("godj_migration_revision")).Scan(&format, &revision)
		if err != nil || format != 2 || revision != 2+2*attempt {
			t.Fatal("empty recorder lost its format or revision", format, revision, err)
		}
		records, err := f.Backend.ReadAppliedMigrations(t.Context())
		if err != nil || len(records) != 0 {
			t.Fatal("reverse left an applied migration", records, err)
		}
	}
}

// A v1 writer passed only the key and the schema intent at BeginMigration.
// This fixture sends precisely those inputs through the native transaction.
type identityOnlyWriter struct{ mb.RevisionFencedBackend }

func (b identityOnlyWriter) OpenRevisionFencedSession(ctx context.Context) (mb.RevisionFencedSession, error) {
	s, err := b.RevisionFencedBackend.OpenRevisionFencedSession(ctx)
	if err != nil {
		return nil, err
	}
	return identityOnlySession{s}, nil
}

type identityOnlySession struct{ mb.RevisionFencedSession }

func (s identityOnlySession) BeginMigration(ctx context.Context, transition mb.HistoryTransition, intent mb.MigrationIntent) (mb.RevisionFencedTransaction, error) {
	transition.Migration.HistoryDigest = ""
	return s.RevisionFencedSession.BeginMigration(ctx, transition, intent)
}

func checkLegacyDefinitionHistory(t *testing.T, open func() Fixture, constraint func(error) string) {
	t.Helper()
	m := newFieldMigration(t, open(), constraint, "external_id", false, false, &ir.Scalar{Kind: ir.ScalarString, String: "legacy"})
	_, err := (migrations.Executor{Backend: identityOnlyWriter{m.Backend}}).Migrate(t.Context(), m.loaded, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatal("write original two-column history", err)
	}
	if err := m.SQL.Close(); err != nil {
		t.Fatal(err)
	}
	m.Fixture = open()
	stable := m.state(t)
	m.apply(t, m.change)
	if !reflect.DeepEqual(stable, m.state(t)) {
		t.Fatal("no-op migrate stamped a legacy record or changed the database")
	}
	m.apply(t, m.initial)
	// The observer keeps its own connection across the two- to three-column upgrade.
	observer := m
	observer.Fixture = open()
	observer.SQL.SetMaxOpenConns(1)
	observer.checkRecorder(t, 1, nil)
	m.apply(t, m.change)
	observer.checkRecorder(t, 2, expectedHistoryDigest(t, m.loaded, m.change))
}
