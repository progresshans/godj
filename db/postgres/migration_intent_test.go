package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestNewPostgresMigrationSchemaClonesAndSealsIntent(t *testing.T) {
	t.Parallel()

	model := postgresMigrationTestPostModel(false)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationCreateModel,
		After:          model.Clone(),
		Targets:        []migrationbackend.MigrationTarget{},
	}}}
	schema, err := newPostgresMigrationSchema(postgresMigrationTestTransition(), intent)
	if err != nil {
		t.Fatalf("newPostgresMigrationSchema() error = %v", err)
	}
	intent.Operations[0].After.Fields[1].Column = "mutated_input"
	if got := schema.intent.Operations[0].After.Fields[1].Column; got != "title" {
		t.Fatalf("sealed intent retained caller mutation: column=%q", got)
	}
	if err := schema.verifySeal(); err != nil {
		t.Fatalf("verifySeal() after caller mutation = %v", err)
	}
	schema.intent.Operations[0].After.Fields[1].Column = "mutated_seal"
	assertPostgresMigrationIntegrity(t, schema.verifySeal())
}

func TestPostgresMigrationIntentRejectsForgedStructureAsIntegrity(t *testing.T) {
	t.Parallel()

	model := postgresMigrationTestPostModel(false)
	tests := []struct {
		name       string
		transition migrationbackend.HistoryTransition
		intent     migrationbackend.MigrationIntent
	}{
		{
			name:       "empty transition identity",
			transition: migrationbackend.HistoryTransition{Kind: migrationbackend.HistoryTransitionApply},
			intent:     migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}},
		},
		{
			name:       "invalid transition kind",
			transition: migrationbackend.HistoryTransition{Migration: migrationbackend.AppliedMigration{App: "blog", Name: "0001"}},
			intent:     migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}},
		},
		{
			name:       "missing operations",
			transition: postgresMigrationTestTransition(),
			intent:     migrationbackend.MigrationIntent{},
		},
		{
			name:       "forged cursor index",
			transition: postgresMigrationTestTransition(),
			intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
				OperationIndex: 1, Kind: migrationbackend.MigrationCreateModel, After: model.Clone(), Targets: []migrationbackend.MigrationTarget{},
			}}},
		},
		{
			name:       "wrong transition operation",
			transition: postgresMigrationTestTransition(),
			intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
				OperationIndex: 0, Kind: migrationbackend.MigrationDeleteModel, Before: model.Clone(), Targets: []migrationbackend.MigrationTarget{},
			}}},
		},
		{
			name:       "reserved control table",
			transition: postgresMigrationTestTransition(),
			intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
				OperationIndex: 0,
				Kind:           migrationbackend.MigrationCreateModel,
				After: ir.Model{
					Name: "reserved", GoName: "Reserved", DBTable: postgresMigrationRecorderTable,
					Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
				},
				Targets: []migrationbackend.MigrationTarget{},
			}}},
		},
		{
			name:       "reserved control primary index relation",
			transition: postgresMigrationTestTransition(),
			intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
				OperationIndex: 0,
				Kind:           migrationbackend.MigrationCreateModel,
				After: ir.Model{
					Name: "reserved_index", GoName: "ReservedIndex", DBTable: postgresMigrationRecorderPrimaryKey,
					Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
				},
				Targets: []migrationbackend.MigrationTarget{},
			}}},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := newPostgresMigrationSchema(test.transition, test.intent)
			assertPostgresMigrationIntegrity(t, err)
			if migrationbackend.IsCapabilityError(err) {
				t.Fatalf("forged intent leaked through capability taxonomy: %v", err)
			}
		})
	}
}

func TestPostgresMigrationRecorderIdentityCharacterBoundary(t *testing.T) {
	t.Parallel()

	base := postgresMigrationTestTransition()
	for _, test := range []struct {
		name          string
		app           string
		migrationName string
		wantErr       bool
		wantContains  string
	}{
		{name: "ascii_255", app: strings.Repeat("a", 255), migrationName: "0001"},
		{name: "ascii_256", app: strings.Repeat("a", 256), migrationName: "0001", wantErr: true},
		{name: "multibyte_255", app: strings.Repeat("한", 255), migrationName: "0001"},
		{name: "multibyte_256", app: strings.Repeat("한", 256), migrationName: "0001", wantErr: true},
		{name: "four_byte_255", app: strings.Repeat("\U0010FFFF", 255), migrationName: "0001"},
		{
			name: "four_byte_256", app: strings.Repeat("\U0010FFFF", 256), migrationName: "0001",
			wantErr: true, wantContains: "1024 bytes, maximum 1020",
		},
		{
			name: "invalid_over_byte_limit", app: strings.Repeat("\xff", 1021), migrationName: "0001",
			wantErr: true, wantContains: "1021 bytes, maximum 1020",
		},
		{
			name: "invalid_utf8", app: string([]byte{0xff}), migrationName: "0001",
			wantErr: true, wantContains: "not valid UTF-8",
		},
		{name: "app_nul", app: "app\x00suffix", migrationName: "0001", wantErr: true},
		{name: "migration_name_nul", app: "app", migrationName: "0001\x00suffix", wantErr: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transition := base
			transition.Migration.App = test.app
			transition.Migration.Name = test.migrationName
			_, err := newPostgresMigrationSchema(
				transition,
				migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}},
			)
			if test.wantErr {
				assertPostgresMigrationIntegrity(t, err)
				if test.wantContains != "" && !strings.Contains(err.Error(), test.wantContains) {
					t.Fatalf("newPostgresMigrationSchema() error = %v, want containing %q", err, test.wantContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("newPostgresMigrationSchema() error = %v", err)
			}
		})
	}
}

func TestPostgresMigrationPhysicalFieldLimitsArePreIOCapabilities(t *testing.T) {
	t.Parallel()

	modelWithFields := func(count int) ir.Model {
		fields := make([]ir.Field, count)
		fields[0] = ir.Field{
			Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true,
		}
		for index := 1; index < count; index++ {
			name := fmt.Sprintf("flag_%04d", index)
			fields[index] = ir.Field{
				Name: name, GoName: "Flag" + fmt.Sprintf("%04d", index), Column: name,
				Kind: ir.FieldBoolean,
			}
		}
		return ir.Model{Name: "wide", GoName: "Wide", DBTable: "wide", Fields: fields}
	}
	createIntent := func(model ir.Model) migrationbackend.MigrationIntent {
		return migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          model,
			Targets:        []migrationbackend.MigrationTarget{},
		}}}
	}

	if _, err := newPostgresMigrationSchema(
		postgresMigrationTestTransition(),
		createIntent(modelWithFields(postgresMigrationMaxModelFields)),
	); err != nil {
		t.Fatalf("maximum PostgreSQL field count rejected: %v", err)
	}
	if _, err := newPostgresMigrationSchema(
		postgresMigrationTestTransition(),
		createIntent(modelWithFields(postgresMigrationMaxModelFields+1)),
	); !migrationbackend.IsCapabilityError(err) {
		t.Fatalf("field-count overflow error = %v, want capability", err)
	}

	charModel := postgresMigrationTestPostModel(false)
	charModel.Fields[1].MaxLength = postgresMigrationMaxVarcharChars
	if _, err := newPostgresMigrationSchema(postgresMigrationTestTransition(), createIntent(charModel)); err != nil {
		t.Fatalf("maximum PostgreSQL VARCHAR length rejected: %v", err)
	}
	charModel.Fields[1].MaxLength++
	if _, err := newPostgresMigrationSchema(postgresMigrationTestTransition(), createIntent(charModel)); !migrationbackend.IsCapabilityError(err) {
		t.Fatalf("VARCHAR overflow error = %v, want capability", err)
	}

	created := postgresMigrationTestPostModel(false)
	complete := created.Clone()
	complete.Fields = append(complete.Fields, ir.Field{
		Name: "oversized", GoName: "Oversized", Column: "oversized", Kind: ir.FieldChar,
		Nullable: true, MaxLength: postgresMigrationMaxVarcharChars + 1,
	})
	compound := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{OperationIndex: 0, Kind: migrationbackend.MigrationCreateModel, After: created, Targets: []migrationbackend.MigrationTarget{}},
		{OperationIndex: 1, Kind: migrationbackend.MigrationAddField, Before: created, After: complete, Targets: []migrationbackend.MigrationTarget{}},
	}}
	session := &postgresRevisionFencedSession{state: postgresRevisionSessionReady}
	if _, err := session.BeginMigration(context.Background(), postgresMigrationTestTransition(), compound); !migrationbackend.IsCapabilityError(err) {
		t.Fatalf("compound VARCHAR overflow error = %v, want pre-I/O capability", err)
	}
	if session.state != postgresRevisionSessionReady {
		t.Fatalf("pre-I/O capability changed session state to %d", session.state)
	}
}

func TestPostgresMigrationForeignKeyRemoveKeepsBeforeTargetAuthority(t *testing.T) {
	t.Parallel()

	author := postgresMigrationTestAuthorModel()
	before := postgresMigrationTestPostModel(true)
	after := postgresMigrationTestPostModel(false)
	removed := before.Fields[len(before.Fields)-1]
	target := postgresMigrationTestTarget(removed, author)
	schema, err := newPostgresMigrationSchema(
		migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "blog", Name: "0002_author"},
			Kind:      migrationbackend.HistoryTransitionUnapply,
		},
		migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationRemoveField,
			Before:         before,
			After:          after,
			Targets:        []migrationbackend.MigrationTarget{target},
		}}},
	)
	if err != nil {
		t.Fatalf("newPostgresMigrationSchema() error = %v", err)
	}
	initialTargets := schema.initial.targets[before.Name]
	if len(initialTargets) != 1 || !migrationFieldsEqual(initialTargets[0].SourceField, removed) ||
		!reflect.DeepEqual(initialTargets[0].TargetModel, author) {
		t.Fatalf("ForeignKey RemoveField initial targets = %+v, want sealed before relation", initialTargets)
	}
	if finalTargets := schema.final.targets[after.Name]; len(finalTargets) != 0 {
		t.Fatalf("ForeignKey RemoveField final targets = %+v, want none", finalTargets)
	}
}

func TestPostgresMigrationCreateThenAddKeepsInitialTableAbsent(t *testing.T) {
	t.Parallel()

	created := postgresMigrationTestPostModel(false)
	complete := created.Clone()
	complete.Fields = append(complete.Fields, ir.Field{
		Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200,
	})
	schema, err := newPostgresMigrationSchema(
		postgresMigrationTestTransition(),
		migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
			{
				OperationIndex: 0,
				Kind:           migrationbackend.MigrationCreateModel,
				After:          created,
				Targets:        []migrationbackend.MigrationTarget{},
			},
			{
				OperationIndex: 1,
				Kind:           migrationbackend.MigrationAddField,
				Before:         created,
				After:          complete,
				Targets:        []migrationbackend.MigrationTarget{},
			},
		}},
	)
	if err != nil {
		t.Fatalf("newPostgresMigrationSchema() error = %v", err)
	}
	if len(schema.initial.models) != 0 {
		t.Fatalf("CreateModel followed by AddField initial models = %+v, want absent", schema.initial.models)
	}
	final, exists := schema.final.models[complete.Name]
	if !exists || !reflect.DeepEqual(final, complete) {
		t.Fatalf("CreateModel followed by AddField final model = %+v, exists=%t, want %+v", final, exists, complete)
	}
	existing, absent, err := schema.postgresMigrationPreflightTables()
	if err != nil {
		t.Fatalf("postgresMigrationPreflightTables() error = %v", err)
	}
	if len(existing) != 0 || !reflect.DeepEqual(absent[created.DBTable], created) {
		t.Fatalf("CreateModel followed by AddField preflight = existing:%+v absent:%+v", existing, absent)
	}
}

func TestPostgresMigrationAddRequiresEmptyTable(t *testing.T) {
	t.Parallel()

	logicalDefault := &ir.ScalarDefault{Kind: ir.ScalarString, String: "backfilled"}
	tests := []struct {
		name  string
		field ir.Field
		want  bool
	}{
		{name: "nullable without default", field: ir.Field{Nullable: true}, want: false},
		{name: "required without default", field: ir.Field{}, want: true},
		{name: "nullable with logical default", field: ir.Field{Nullable: true, Default: logicalDefault}, want: true},
		{name: "required with logical default", field: ir.Field{Default: logicalDefault}, want: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := postgresMigrationAddRequiresEmptyTable(test.field); got != test.want {
				t.Fatalf("postgresMigrationAddRequiresEmptyTable() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestPostgresMigrationIntentResourceViolationIsIntegrity(t *testing.T) {
	t.Parallel()

	intent := migrationbackend.MigrationIntent{
		Operations: make([]migrationbackend.MigrationOperation, postgresMigrationMaxOperations+1),
	}
	_, err := newPostgresMigrationSchema(postgresMigrationTestTransition(), intent)
	assertPostgresMigrationIntegrity(t, err)
	if migrationbackend.IsCapabilityError(err) {
		t.Fatalf("resource violation leaked through capability taxonomy: %v", err)
	}
}

func TestRegisterPostgresConstraintNameRejectsCollisionAsIntegrity(t *testing.T) {
	t.Parallel()

	owners := map[string]string{}
	if err := registerPostgresConstraintName(owners, "godj_pk_fixed", "first.id"); err != nil {
		t.Fatal(err)
	}
	if err := registerPostgresConstraintName(owners, "godj_pk_fixed", "first.id"); err != nil {
		t.Fatalf("same owner was not idempotent: %v", err)
	}
	err := registerPostgresConstraintName(owners, "godj_pk_fixed", "second.id")
	assertPostgresMigrationIntegrity(t, err)
}

func TestRegisterPostgresRelationNameRejectsSequenceCollisionAsIntegrity(t *testing.T) {
	t.Parallel()

	owners := map[string]string{}
	if err := registerPostgresRelationName(owners, "godj_seq_fixed", "first.id sequence"); err != nil {
		t.Fatal(err)
	}
	err := registerPostgresRelationName(owners, "godj_seq_fixed", "second.id sequence")
	assertPostgresMigrationIntegrity(t, err)
}

func TestPostgresMigrationSchemaCursorAndArgumentsFailClosed(t *testing.T) {
	t.Parallel()

	model := postgresMigrationTestPostModel(false)
	schema, err := newPostgresMigrationSchema(
		postgresMigrationTestTransition(),
		migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0, Kind: migrationbackend.MigrationCreateModel, After: model.Clone(), Targets: []migrationbackend.MigrationTarget{},
		}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	schema.preflight = true
	schema.namespace = "product_schema"
	executor := &postgresMigrationRecordingExecutor{}

	assertPostgresMigrationIntegrity(t, schema.DeleteModel(context.Background(), executor, model))
	if executor.execCalls != 0 || schema.cursor != 0 {
		t.Fatalf("wrong operation reached I/O or advanced cursor: calls=%d cursor=%d", executor.execCalls, schema.cursor)
	}
	mutated := model.Clone()
	mutated.DBTable = "other_post"
	assertPostgresMigrationIntegrity(t, schema.CreateModel(context.Background(), executor, mutated))
	if executor.execCalls != 0 || schema.cursor != 0 {
		t.Fatalf("wrong argument reached I/O or advanced cursor: calls=%d cursor=%d", executor.execCalls, schema.cursor)
	}
	if err := schema.CreateModel(context.Background(), executor, model); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	if executor.execCalls != 1 || schema.cursor != 1 || !strings.HasPrefix(executor.lastStatement, `CREATE TABLE "product_schema"."blog_post"`) {
		t.Fatalf("executed state = calls=%d cursor=%d statement=%q", executor.execCalls, schema.cursor, executor.lastStatement)
	}
	assertPostgresMigrationIntegrity(t, schema.CreateModel(context.Background(), executor, model))
	if executor.execCalls != 1 || schema.cursor != 1 {
		t.Fatalf("past-end operation reached I/O: calls=%d cursor=%d", executor.execCalls, schema.cursor)
	}
}

func TestPostgresMigrationCapabilityRemainsDistinctFromIntegrity(t *testing.T) {
	t.Parallel()

	err := postgresMigrationCapability("populated required AddField", nil)
	if !migrationbackend.IsCapabilityError(err) {
		t.Fatalf("capability error = %T %v", err, err)
	}
	if migrationbackend.IsRevisionFenceError(err) {
		t.Fatalf("capability error is also a revision fence error: %v", err)
	}
}

func postgresMigrationTestTransition() migrationbackend.HistoryTransition {
	return migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "blog", Name: "0001_initial"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
}

func assertPostgresMigrationIntegrity(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want RevisionFenceFailureIntegrity")
	}
	var fence *migrationbackend.RevisionFenceError
	if !errors.As(err, &fence) || fence == nil || fence.Kind != migrationbackend.RevisionFenceFailureIntegrity {
		t.Fatalf("error = %T %v, want RevisionFenceFailureIntegrity", err, err)
	}
}

type postgresMigrationRecordingExecutor struct {
	execCalls     int
	lastStatement string
}

func (executor *postgresMigrationRecordingExecutor) ExecContext(
	_ context.Context,
	statement string,
	_ ...any,
) (sql.Result, error) {
	executor.execCalls++
	executor.lastStatement = statement
	return driver.RowsAffected(0), nil
}

func (*postgresMigrationRecordingExecutor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected QueryContext")
}

func (*postgresMigrationRecordingExecutor) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}
