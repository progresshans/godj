package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"example.com/godj-decimal/models"
	"example.com/godj-decimal/nextmodels"
	"example.com/godj-decimal/nextproject"
	"example.com/godj-decimal/project"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed precision_reference.json
var precisionReference []byte

type precisionProfile struct {
	Name          string
	Before, After [2]int
	Input         []*string
}

func TestDecimalPrecisionMigration(t *testing.T) {
	var reference struct {
		Django   string
		Profiles []precisionProfile
	}
	if err := json.Unmarshal(precisionReference, &reference); err != nil || reference.Django != "6.1" || len(reference.Profiles) != 12 {
		t.Fatal("precision migration reference is incomplete", err)
	}
	// These exact selectors require rounding or lose whole digits. Their
	// rejection is the lossless GoDj policy, not Django's successful SQLite DDL.
	rejected := map[string]bool{"reduce_scale_rounding": true, "reduce_whole_overflow": true, "scale_uses_existing_whole_capacity": true, "zero_whole_digits": true, "zero_scale_rounding": true}
	for _, profile := range reference.Profiles {
		t.Run(profile.Name, func(t *testing.T) {
			forEachDecimalBackend(t, func(t *testing.T, open func(context.Context) (decimalBackend, error)) {
				runDecimalPrecisionProfile(t, open, profile, rejected[profile.Name])
			})
		})
	}
}

func precisionDefinitions(t *testing.T, before, after ir.Model, related ...ir.Model) (migrations.LoadedDefinitionSet, migrations.MigrationKey) {
	t.Helper()
	initial := migrations.Migration{App: "decimalref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "decimalref", Model: before}}}
	for _, model := range related {
		initial.Operations = append(initial.Operations, migrations.CreateModel{AppLabel: initial.App, Model: model})
	}
	values := []migrations.Migration{initial}
	key := migrations.MigrationKey{App: initial.App, Name: initial.Name}
	if !reflect.DeepEqual(before, after) {
		oldField, newField, _, err := mb.ChangedField(before, after)
		if err != nil {
			t.Fatal(err)
		}
		key.Name = "0002_precision"
		values = append(values, migrations.Migration{App: initial.App, Name: key.Name, Dependencies: []migrations.MigrationKey{{App: initial.App, Name: initial.Name}}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: initial.App, ModelName: before.Name, Before: oldField, After: newField}}})
	}
	var sources []definition.Source
	for _, value := range values {
		wire, err := definition.Encode(definition.Producer{Name: "precision-consumer", Version: "1"}, value)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: value.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return loaded, key
}

func precisionHistory(t *testing.T, backend decimalBackend) []mb.AppliedMigration {
	t.Helper()
	session, err := backend.OpenRevisionFencedSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	rows, err := session.ReadAppliedMigrations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func runDecimalPrecisionProfile(t *testing.T, open func(context.Context) (decimalBackend, error), profile precisionProfile, rejected bool) {
	t.Helper()
	ctx := t.Context()
	backend, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	}()
	makeModel := func(precision [2]int) ir.Model {
		built, err := schema.Build(schema.Definition{AppLabel: "decimalref", Models: []schema.Model{{Name: "cost", GoName: "Cost", Fields: []schema.Field{schema.DecimalField("value", "Value", precision[0], precision[1], schema.Nullable())}}}})
		if err != nil {
			t.Fatal(err)
		}
		return built.Models[0]
	}
	before, after := makeModel(profile.Before), makeModel(profile.After)
	loaded, target := precisionDefinitions(t, before, after)
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "decimalref", Name: "0001_initial"}))
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	for _, raw := range profile.Input {
		value := query.Null()
		if raw != nil {
			value = query.Decimal(number(t, *raw))
		}
		if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(before.DBTable, []query.Assignment{orm.NewAssignment(before.Fields[1], value)}, id)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(source decimalBackend, precision [2]int) []*decimal.Decimal {
		field := query.NewDecimalFieldRef("value", "value", true, precision[0], precision[1])
		rows, err := source.Query(ctx, query.NewPlan(before.DBTable, []query.FieldRef{id, field}).WithOrderings(query.NewOrdering(id, query.Ascending)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := rows.Close(); err != nil {
				t.Error(err)
			}
		}()
		values := []*decimal.Decimal{}
		for rows.Next() {
			var key int64
			scanner := orm.NewNullableDecimalScanner(precision[0], precision[1])
			if err := rows.Scan(&key, &scanner); err != nil {
				t.Fatal(err)
			}
			if scanner.Valid {
				value := scanner.Decimal
				values = append(values, &value)
			} else {
				values = append(values, nil)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return values
	}
	original := read(backend, profile.Before)
	if len(original) != len(profile.Input) {
		t.Fatal("initial precision rows missing")
	}
	for index, input := range profile.Input {
		value := original[index]
		if input == nil {
			if value != nil {
				t.Fatal("initial null changed")
			}
			continue
		}
		if value == nil || !value.Equal(number(t, *input)) {
			t.Fatal("initial exact value differs from independent input")
		}
	}
	held, err := backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := held.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	history, err := held.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	if rejected {
		if err == nil || !reflect.DeepEqual(original, read(backend, profile.Before)) || !reflect.DeepEqual(history, precisionHistory(t, backend)) {
			t.Fatal("unsafe precision transition changed values or history", err)
		}
		// A session holding the previous private revision token must still be
		// able to begin: a failed precision check cannot advance that token.
		tx, err := held.BeginMigration(ctx, mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: target.App, Name: target.Name}}, mb.MigrationIntent{Operations: []mb.MigrationOperation{{OperationIndex: 0, Kind: mb.MigrationAlterField, Before: before, After: after}}})
		if err != nil {
			t.Fatal("failed precision change advanced revision or physical schema", err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	model, found := state.Model("decimalref", before.Name)
	if !found || !reflect.DeepEqual(model, after) || !reflect.DeepEqual(original, read(backend, profile.After)) {
		t.Fatal("successful precision change lost model or values")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, read(second, profile.After)) {
		t.Fatal("precision changed after reopening")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, read(backend, profile.Before)) {
		t.Fatal("reverse precision changed exact values")
	}
}

func TestDecimalGeneratedPrecisionEvolution(t *testing.T) {
	forEachDecimalBackend(t, runGeneratedPrecisionEvolution)
}

var precisionRollbackProbe = errors.New("injected failure after precision schema edit")

type precisionGuardBackend struct {
	decimalBackend
	deny, failAfterAlter bool
	alters               int
}

func (backend *precisionGuardBackend) MigrationCapabilities() mb.MigrationCapabilities {
	capabilities := backend.decimalBackend.MigrationCapabilities()
	if backend.deny {
		capabilities.AlterFieldDecimalPrecision = false
	}
	return capabilities
}

func (backend *precisionGuardBackend) OpenRevisionFencedSession(ctx context.Context) (mb.RevisionFencedSession, error) {
	session, err := backend.decimalBackend.OpenRevisionFencedSession(ctx)
	if err != nil {
		return nil, err
	}
	return precisionGuardSession{RevisionFencedSession: session, owner: backend}, nil
}

type precisionGuardSession struct {
	mb.RevisionFencedSession
	owner *precisionGuardBackend
}

func (session precisionGuardSession) BeginMigration(ctx context.Context, transition mb.HistoryTransition, intent mb.MigrationIntent) (mb.RevisionFencedTransaction, error) {
	tx, err := session.RevisionFencedSession.BeginMigration(ctx, transition, intent)
	if err != nil {
		return nil, err
	}
	return precisionGuardTransaction{RevisionFencedTransaction: tx, owner: session.owner}, nil
}

type precisionGuardTransaction struct {
	mb.RevisionFencedTransaction
	owner *precisionGuardBackend
}

func (tx precisionGuardTransaction) AlterField(ctx context.Context, model ir.Model, before, after ir.Field) error {
	tx.owner.alters++
	if err := tx.RevisionFencedTransaction.AlterField(ctx, model, before, after); err != nil {
		return err
	}
	if tx.owner.failAfterAlter {
		return precisionRollbackProbe
	}
	return nil
}

func runGeneratedPrecisionEvolution(t *testing.T, open func(context.Context) (decimalBackend, error)) {
	t.Helper()
	ctx := t.Context()
	backend, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	}()
	before, after := (models.RecordDescriptor{}).Metadata(), (nextmodels.RecordDescriptor{}).Metadata()
	loaded, _ := precisionDefinitions(t, before, after)
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "decimalref", Name: "0001_initial"}))
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	old, err := models.RecordObjects.Create(ctx, backend, models.NewRecordCreate("existing", fraction).WithCost(fraction))
	if err != nil {
		t.Fatal(err)
	}
	for _, guard := range []*precisionGuardBackend{{decimalBackend: backend, deny: true}, {decimalBackend: backend, failAfterAlter: true}} {
		held, err := backend.OpenRevisionFencedSession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		oldHistory, err := held.ReadAppliedMigrations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = (migrations.Executor{Backend: guard}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
		if err == nil || guard.deny && guard.alters != 0 || guard.failAfterAlter && (guard.alters != 1 || !errors.Is(err, precisionRollbackProbe)) {
			t.Fatal("precision capability or rollback injection missed its boundary", err)
		}
		if !reflect.DeepEqual(oldHistory, precisionHistory(t, backend)) {
			t.Fatal("rejected precision change altered applied history")
		}
		tx, err := held.BeginMigration(ctx, mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: "decimalref", Name: "0002_precision"}}, mb.MigrationIntent{Operations: []mb.MigrationOperation{{OperationIndex: 0, Kind: mb.MigrationAlterField, Before: before, After: after}}})
		if err != nil {
			t.Fatal("failed schema edit changed revision or physical precision", err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if err := held.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	row, found, err := nextmodels.RecordObjects.Using(backend).Filter(nextmodels.RecordFields.ID.Exact(old.ID)).OrderBy(nextmodels.RecordFields.ID.Asc()).First(ctx)
	if err != nil || !found || row.Cost == nil || !row.Cost.Equal(fraction) {
		t.Fatal("new generated scanner lost existing cost", err)
	}
	large := number(t, "999999999999.99")
	row, err = nextmodels.RecordObjects.Update(ctx, backend, row, nextmodels.RecordPatch{}.WithCost(large))
	if err != nil {
		t.Fatal(err)
	}
	history := precisionHistory(t, backend)
	if _, err := executor.Migrate(ctx, loaded, initial); err == nil {
		t.Fatal("reverse rounded or truncated new larger cost")
	}
	if !reflect.DeepEqual(history, precisionHistory(t, backend)) {
		t.Fatal("failed reverse changed history")
	}
	second, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := nextmodels.RecordObjects.Using(second).Filter(nextmodels.RecordFields.ID.Exact(row.ID)).OrderBy(nextmodels.RecordFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Cost == nil || !stored.Cost.Equal(large) {
		t.Fatal("failed reverse lost durable larger value", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := nextmodels.RecordObjects.Update(ctx, backend, stored, nextmodels.RecordPatch{}.WithCost(fraction)); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := executor.Migrate(canceled, loaded, initial); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled precision transition ignored context", err)
	}
}

func TestDecimalPrecisionIncomingRelation(t *testing.T) {
	forEachDecimalBackend(t, func(t *testing.T, open func(context.Context) (decimalBackend, error)) {
		ctx := t.Context()
		backend, err := open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := backend.Close(); err != nil {
				t.Error(err)
			}
		}()
		before, after := (models.RecordDescriptor{}).Metadata(), (nextmodels.RecordDescriptor{}).Metadata()
		loaded, _ := precisionDefinitions(t, before, after, (models.LinkDescriptor{}).Metadata())
		executor := migrations.Executor{Backend: backend}
		initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "decimalref", Name: "0001_initial"}))
		if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
			t.Fatal(err)
		}
		record, err := models.RecordObjects.Create(ctx, backend, models.NewRecordCreate("related", fraction).WithCost(fraction))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate("link").WithRecordID(record.ID)); err != nil {
			t.Fatal(err)
		}
		oldFacade, err := project.Using(backend)
		if err != nil {
			t.Fatal(err)
		}
		cached, err := oldFacade.ModelsLink.OrderBy(models.LinkFields.ID.Asc()).SelectRelated(oldFacade.ModelsLink.Related.Record).All(ctx)
		if err != nil || len(cached) != 1 {
			t.Fatal("initial relation query did not warm its result descriptor", err)
		}
		for _, target := range []migrations.LifecycleRequest{migrations.LatestLifecycleRequest(), initial, migrations.LatestLifecycleRequest()} {
			if _, err := executor.Migrate(ctx, loaded, target); err != nil {
				t.Fatal("precision transition lost incoming relation authority", err)
			}
		}
		facade, err := nextproject.Using(backend)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := facade.ModelsLink.OrderBy(nextmodels.LinkFields.ID.Asc()).SelectRelated(facade.ModelsLink.Related.Record).All(ctx)
		if err != nil || len(rows) != 1 {
			t.Fatal("precision migration lost related row", err)
		}
		owner, present, err := rows[0].Record(ctx)
		if err != nil || !present || owner.ID != record.ID || owner.Cost == nil || !owner.Cost.Equal(fraction) {
			t.Fatal("new generated relation scanner lost exact cost", err)
		}
		if _, err := nextmodels.LinkObjects.Create(ctx, backend, nextmodels.NewLinkCreate("orphan").WithRecordID(record.ID+100)); err == nil {
			t.Fatal("precision migration disabled incoming foreign key")
		}
	})
}
