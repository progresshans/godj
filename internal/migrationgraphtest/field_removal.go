package migrationgraphtest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// RunFieldRemoval observes real persisted columns, values, constraints, and
// recorder state across a forward contraction and its schema-only reversal.
// Faults wrap actual native operations; they do not simulate network loss.
func RunFieldRemoval(t *testing.T, ctx context.Context, open func(string) Binding) {
	t.Helper()
	for _, mode := range []string{"roundtrip", "after_second_remove", "after_second_add", "record_applied", "record_unapplied", "commit_unknown", "commit_cleanup", "cancel_after_remove", "rollback_report_failure", "reverse_default_backfill", "reverse_required_without_default"} {
		t.Run(mode, func(t *testing.T) {
			loaded, initial, contracted, keys := fieldRemovalDefinitions(t, mode)
			binding := open(mode)
			expand := func(statement string) string {
				return strings.NewReplacer("{parent}", binding.Table("removal_parent"), "{record}", binding.Table("removal_record"), "{child}", binding.Table("removal_child"), "{history}", binding.Table("godj_migrations")).Replace(statement)
			}
			exec := func(statement string) {
				t.Helper()
				if _, err := binding.Database.ExecContext(ctx, expand(statement)); err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
			}
			count := func(statement string, want int) {
				t.Helper()
				var got int
				if err := binding.Database.QueryRowContext(ctx, expand(statement)).Scan(&got); err != nil || got != want {
					t.Fatalf("%s: got %d want %d: %v", statement, got, want, err)
				}
			}
			assertState := func(state migrations.ProjectState, want ir.Model) {
				t.Helper()
				actual, exists := state.Model("removal", "record")
				if !exists || !reflect.DeepEqual(actual, want) {
					t.Fatal("historical field declaration/order changed")
				}
			}
			migrate := func(index int) migrations.ProjectState {
				t.Helper()
				state, err := (migrations.Executor{Backend: binding.Backend}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[index])))
				if err != nil {
					t.Fatal(err)
				}
				assertState(state, []ir.Model{initial, contracted}[index])
				return state
			}
			columns := func(removed bool) {
				t.Helper()
				rows, err := binding.Database.QueryContext(ctx, expand(`SELECT * FROM {record} WHERE 1=0`))
				if err != nil {
					t.Fatal(err)
				}
				actual, columnsErr := rows.Columns()
				closeErr := rows.Close()
				if columnsErr != nil || closeErr != nil {
					t.Fatal(columnsErr, closeErr)
				}
				want := []string{"label", "key", "keep_parent_id", "tail"}
				if !removed {
					want = append(want, "old_code", "old_parent_id", "legacy_rank")
				}
				slices.Sort(actual)
				slices.Sort(want)
				if !slices.Equal(actual, want) {
					t.Fatalf("persisted columns %v want %v", actual, want)
				}
			}
			for _, key := range keys {
				statements, err := migrations.RenderMigrationSQL(ctx, loaded, key, binding.Renderer)
				if err != nil || len(statements) == 0 {
					t.Fatal("field-removal SQL projection missing", err)
				}
			}
			migrate(0)
			exec(`INSERT INTO {parent} ("label") VALUES ('p1'), ('p2')`)
			exec(`INSERT INTO {record} ("label", "old_code", "old_parent_id", "legacy_rank", "keep_parent_id", "tail") VALUES ('alpha', 'old-a', 1, 31, 2, 'tail-a'), ('beta', 'old-b', 2, 32, 1, 'tail-b')`)
			exec(`INSERT INTO {record} ("label", "legacy_rank") VALUES ('spare3', 17), ('spare4', 17), ('spare5', 17)`)
			exec(`DELETE FROM {record} WHERE "key">2`)
			exec(`INSERT INTO {child} ("owner_id") VALUES (2)`)
			backward := mode == "after_second_add" || mode == "record_unapplied" || mode == "reverse_required_without_default"
			if backward {
				migrate(1)
			}
			target := 1
			if backward {
				target = 0
			}
			faultCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			probe := &removalFault{mode: mode, cancel: cancel}
			state, err := (migrations.Executor{Backend: removalBackend{RevisionFencedBackend: binding.Backend, fault: probe}}).Migrate(faultCtx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[target])))
			successful := mode == "roundtrip" || mode == "reverse_default_backfill"
			if successful {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || (!probe.fired && mode != "reverse_required_without_default") {
					t.Fatal("native fault did not fail the requested lifecycle", err)
				}
				var typed *migrations.Error
				if !errors.As(err, &typed) {
					t.Fatal("migration error taxonomy lost", err)
				}
				switch mode {
				case "reverse_required_without_default":
					if typed.Category != migrations.CategoryCapability {
						t.Fatal("required restoration without a default did not fail as unsupported backfill", err)
					}
				case "commit_unknown":
					if typed.Code != migrations.CodeCommitOutcomeUnknown {
						t.Fatal("uncertain commit published as a definite result", err)
					}
				case "commit_cleanup":
					if typed.Code != migrations.CodeCommitCleanupFailed {
						t.Fatal("committed cleanup lost known durability", err)
					}
				case "record_applied", "record_unapplied":
					if typed.Category != migrations.CategoryRecorder {
						t.Fatal("recorder failure misclassified", err)
					}
				case "rollback_report_failure":
					if !errors.Is(typed.RollbackCause, errRemovalRollback) {
						t.Fatal("rollback failure was hidden", err)
					}
				case "cancel_after_remove":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("cancellation cause lost", err)
					}
				}
			}
			removed := backward || successful || mode == "commit_unknown" || mode == "commit_cleanup"
			// Unknown results retain the last confirmed state, even when this
			// injected probe knows that the underlying commit succeeded.
			wantState := initial
			if backward || successful || mode == "commit_cleanup" {
				wantState = contracted
			}
			assertState(state, wantState)
			if err := binding.Close(); err != nil {
				t.Fatal(err)
			}
			binding = open(mode)
			columns(removed)
			wantHistory := 1
			if removed {
				wantHistory = 2
			}
			count(`SELECT COUNT(*) FROM {history} WHERE app='removal'`, wantHistory)
			count(`SELECT COUNT(*) FROM {record} WHERE ("key"=1 AND "label"='alpha' AND "keep_parent_id"=2 AND "tail"='tail-a') OR ("key"=2 AND "label"='beta' AND "keep_parent_id"=1 AND "tail"='tail-b')`, 2)
			count(`SELECT COUNT(*) FROM {child} WHERE "owner_id"=2`, 1)
			if !removed {
				count(`SELECT COUNT(*) FROM {record} WHERE ("key"=1 AND "old_code"='old-a' AND "old_parent_id"=1 AND "legacy_rank"=31) OR ("key"=2 AND "old_code"='old-b' AND "old_parent_id"=2 AND "legacy_rank"=32)`, 2)
			}
			migrate(1) // Fresh history observation also reconciles unknown commit.
			columns(true)
			if mode == "reverse_required_without_default" {
				if err := binding.Close(); err != nil {
					t.Fatal(err)
				}
				return
			}
			migrate(0)
			columns(false)
			if mode == "reverse_default_backfill" {
				count(`SELECT COUNT(*) FROM {record} WHERE "old_code" IS NULL AND "old_parent_id" IS NULL AND "legacy_rank"=17`, 2)
			} else {
				count(`SELECT COUNT(*) FROM {record} WHERE "old_code" IS NULL AND "old_parent_id" IS NULL AND "legacy_rank" IS NULL`, 2)
			}
			exec(`INSERT INTO {record} ("label", "legacy_rank") VALUES ('next', 17)`)
			count(`SELECT "key" FROM {record} WHERE "label"='next'`, 6)
			if mode == "reverse_default_backfill" {
				if _, err := binding.Database.ExecContext(ctx, expand(`INSERT INTO {record} ("label") VALUES ('unexpected-database-default')`)); err == nil {
					t.Fatal("one-time restoration backfill left a persistent database default")
				}
			}
			for _, statement := range []string{`UPDATE {child} SET "owner_id"=999999`, `UPDATE {record} SET "keep_parent_id"=999999 WHERE "key"=1`, `UPDATE {record} SET "label"='alpha' WHERE "key"=2`} {
				if _, err := binding.Database.ExecContext(ctx, expand(statement)); err == nil {
					t.Fatal("retained constraint was lost", statement)
				}
			}
			exec(`UPDATE {record} SET "old_code"='restored', "old_parent_id"=1 WHERE "key"=1`)
			for _, statement := range []string{`UPDATE {record} SET "old_code"='restored' WHERE "key"=2`, `UPDATE {record} SET "old_parent_id"=1 WHERE "key"=2`, `UPDATE {record} SET "old_parent_id"=999999 WHERE "key"=2`} {
				if _, err := binding.Database.ExecContext(ctx, expand(statement)); err == nil {
					t.Fatal("restored unique or OneToOne constraint was lost", statement)
				}
			}
			migrate(1)
			if err := binding.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func fieldRemovalDefinitions(t *testing.T, mode string) (migrations.LoadedDefinitionSet, ir.Model, ir.Model, []migrations.MigrationKey) {
	t.Helper()
	fk := func(name, target string, cardinality ir.RelationCardinality) ir.Field {
		return ir.Field{Name: name, GoName: "Field" + name, Kind: ir.FieldForeignKey, Nullable: true, Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "removal", ModelName: target}, Cardinality: cardinality, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}}
	}
	rank := ir.Field{Name: "legacy_rank", GoName: "LegacyRank", Kind: ir.FieldInteger, Nullable: true}
	if mode == "reverse_required_without_default" || mode == "reverse_default_backfill" {
		rank.Nullable = false
	}
	if mode == "reverse_default_backfill" {
		rank.Default = &ir.Scalar{Kind: ir.ScalarInteger, Integer: 17}
	}
	schema, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "removal", Models: []ir.Model{
		{Name: "parent", GoName: "Parent", Fields: []ir.Field{{Name: "label", GoName: "Label", Kind: ir.FieldText}}},
		{Name: "record", GoName: "Record", Fields: []ir.Field{
			{Name: "label", GoName: "Label", Kind: ir.FieldText, Unique: true}, {Name: "key", GoName: "Key", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "old_code", GoName: "OldCode", Kind: ir.FieldChar, MaxLength: 30, Nullable: true, Unique: true}, fk("old_parent", "parent", ir.RelationOneToOne),
			rank,
			fk("keep_parent", "parent", ir.RelationManyToOne), {Name: "tail", GoName: "Tail", Kind: ir.FieldText, Nullable: true},
		}},
		{Name: "child", GoName: "Child", Fields: []ir.Field{fk("owner", "record", ir.RelationManyToOne)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	initial := schema.Models[1]
	contracted := initial.Clone()
	contracted.Fields = append(slices.Clone(contracted.Fields[:2]), contracted.Fields[5:]...)
	definitions := []migrations.Migration{{App: "removal", Name: "0001_initial"}, {App: "removal", Name: "0002_contract", Dependencies: []migrations.MigrationKey{{App: "removal", Name: "0001_initial"}}}}
	for _, model := range schema.Models {
		definitions[0].Operations = append(definitions[0].Operations, migrations.CreateModel{AppLabel: "removal", Model: model})
	}
	for i := 2; i < 5; i++ {
		definitions[1].Operations = append(definitions[1].Operations, migrations.RemoveField{AppLabel: "removal", ModelName: "record", Field: initial.Fields[i], BeforeField: initial.Fields[i+1].Name})
	}
	var sources []definition.Source
	var keys []migrations.MigrationKey
	for _, value := range definitions {
		wire, err := definition.Encode(definition.Producer{Name: "field-removal-product", Version: "1"}, value)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: value.Name, Document: wire})
		keys = append(keys, value.Key())
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return loaded, initial, contracted, keys
}

var errRemovalFault = errors.New("native field removal probe")
var errRemovalRollback = errors.New("native field removal rollback report")

type removalFault struct {
	mode   string
	cancel context.CancelFunc
	fired  bool
}
type removalBackend struct {
	migrationbackend.RevisionFencedBackend
	fault *removalFault
}

func (value removalBackend) OpenRevisionFencedSession(ctx context.Context) (migrationbackend.RevisionFencedSession, error) {
	session, err := value.RevisionFencedBackend.OpenRevisionFencedSession(ctx)
	if err != nil {
		return session, err
	}
	return removalSession{RevisionFencedSession: session, fault: value.fault}, nil
}

type removalSession struct {
	migrationbackend.RevisionFencedSession
	fault *removalFault
}

func (value removalSession) BeginMigration(ctx context.Context, transition migrationbackend.HistoryTransition, intent migrationbackend.MigrationIntent) (migrationbackend.RevisionFencedTransaction, error) {
	transaction, err := value.RevisionFencedSession.BeginMigration(ctx, transition, intent)
	if err != nil {
		return transaction, err
	}
	return &removalTransaction{RevisionFencedTransaction: transaction, fault: value.fault}, nil
}

type removalTransaction struct {
	migrationbackend.RevisionFencedTransaction
	fault          *removalFault
	removed, added int
}

func (value *removalTransaction) RemoveField(ctx context.Context, model ir.Model, field ir.Field) error {
	if err := value.RevisionFencedTransaction.RemoveField(ctx, model, field); err != nil {
		return err
	}
	value.removed++
	if value.removed == 2 {
		switch value.fault.mode {
		case "after_second_remove", "rollback_report_failure":
			value.fault.fired = true
			return errRemovalFault
		case "cancel_after_remove":
			value.fault.fired = true
			value.fault.cancel()
			return ctx.Err()
		}
	}
	return nil
}
func (value *removalTransaction) AddField(ctx context.Context, model ir.Model, field ir.Field) error {
	if err := value.RevisionFencedTransaction.AddField(ctx, model, field); err != nil {
		return err
	}
	value.added++
	if value.added == 2 && value.fault.mode == "after_second_add" {
		value.fault.fired = true
		return errRemovalFault
	}
	return nil
}
func (value *removalTransaction) RecordApplied(ctx context.Context, app, name string) error {
	if err := value.RevisionFencedTransaction.RecordApplied(ctx, app, name); err != nil {
		return err
	}
	if value.fault.mode == "record_applied" {
		value.fault.fired = true
		return errRemovalFault
	}
	return nil
}
func (value *removalTransaction) RecordUnapplied(ctx context.Context, app, name string) error {
	if err := value.RevisionFencedTransaction.RecordUnapplied(ctx, app, name); err != nil {
		return err
	}
	if value.fault.mode == "record_unapplied" {
		value.fault.fired = true
		return errRemovalFault
	}
	return nil
}
func (value *removalTransaction) CommitFenced(ctx context.Context) (migrationbackend.CommitOutcome, error) {
	outcome, err := value.RevisionFencedTransaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		return outcome, err
	}
	if value.fault.mode == "commit_unknown" {
		value.fault.fired = true
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, errRemovalFault
	}
	if value.fault.mode == "commit_cleanup" {
		value.fault.fired = true
		return outcome, errRemovalFault
	}
	return outcome, nil
}
func (value *removalTransaction) Rollback(ctx context.Context) error {
	err := value.RevisionFencedTransaction.Rollback(ctx)
	if value.fault.mode == "rollback_report_failure" {
		return errors.Join(err, errRemovalRollback)
	}
	return err
}
