package migrations

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/migrationgraph"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func constraintHistory(t *testing.T, self bool) (ProjectState, []Migration, ir.UniqueConstraint) {
	t.Helper()
	fields := []schema.Field{schema.IntegerField("category", "Category"), schema.CharField("name", "Name", 40)}
	if self {
		fields[0] = schema.ForeignKey("category", "Category", schema.Target("scoped", "label"), schema.NoReverse(), schema.Protect, schema.Nullable())
	}
	s, err := schema.Build(schema.Definition{AppLabel: "scoped", Models: []schema.Model{{Name: "label", GoName: "Label", Fields: fields}}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := NewProjectState(s)
	if err != nil {
		t.Fatal(err)
	}
	constraint := ir.UniqueConstraint{Name: "scope_name", Fields: []string{"category", "name"}}
	initial := Migration{App: "scoped", Name: "0001_initial", Operations: []Operation{CreateModel{AppLabel: "scoped", Model: s.Models[0]}}}
	change := Migration{App: "scoped", Name: "0002_scope", Dependencies: []MigrationKey{initial.Key()}, Operations: []Operation{&AddConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint.Clone()}}}
	return base, []Migration{initial, change}, constraint
}

func TestNamedConstraintOperationsRequireExactPreimagesAndOwnState(t *testing.T) {
	base, history, constraint := constraintHistory(t, false)
	add := *history[1].Operations[0].(*AddConstraint)
	withConstraint, err := add.stateForward(base)
	if err != nil || withConstraint.Equal(base) {
		t.Fatal("constraint was not added", err)
	}
	remove := RemoveConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint.Clone()}
	for _, operation := range []Operation{add, remove} {
		from, want := base, withConstraint
		if operation.Kind() == "RemoveConstraint" {
			from, want = withConstraint, base
		}
		to, err := operation.stateForward(from)
		if err != nil || !to.Equal(want) {
			t.Fatal(operation.Kind(), "forward", err)
		}
		restored, err := operation.stateBackward(to)
		if err != nil || !restored.Equal(from) {
			t.Fatal(operation.Kind(), "backward", err)
		}
		if unchanged, err := operation.stateForward(to); err == nil || !unchanged.Equal(to) {
			t.Fatal("stale forward preimage accepted or mutated")
		}
		if unchanged, err := operation.stateBackward(from); err == nil || !unchanged.Equal(from) {
			t.Fatal("stale backward preimage accepted or mutated")
		}
	}
	for _, invalid := range []ir.UniqueConstraint{
		{Name: "scope_name", Fields: []string{"name", "category"}},
		{Name: "scope_name", Fields: []string{"id", "name"}},
		{Name: "scope_name"},
		{Name: "missing", Fields: []string{"category", "name"}},
	} {
		bad := remove
		bad.Constraint = invalid
		if unchanged, err := bad.stateForward(withConstraint); err == nil || !unchanged.Equal(withConstraint) {
			t.Fatal("remove guessed historical fields", invalid)
		}
	}
	for _, invalid := range []ir.UniqueConstraint{
		{Name: "bad", Fields: []string{"missing"}}, {Name: "bad", Fields: []string{"name", "name"}}, {Name: "bad"}, {Name: "bad.name", Fields: []string{"name"}},
	} {
		bad := add
		bad.Constraint = invalid
		if unchanged, err := bad.stateForward(base); err == nil || !unchanged.Equal(base) {
			t.Fatal("invalid addition changed state", invalid)
		}
	}
	add.Constraint.Fields[0] = "id"
	got, _ := withConstraint.Model("scoped", "label")
	if !got.UniqueConstraints[0].Equal(constraint) {
		t.Fatal("state retained caller's constraint member list")
	}
	got.UniqueConstraints[0].Fields[0] = "id"
	got, _ = withConstraint.Model("scoped", "label")
	if !got.UniqueConstraints[0].Equal(constraint) {
		t.Fatal("state accessor exposes constraint storage")
	}
}

func TestNamedConstraintReplayRetainsEachOperationBoundaryAndReversesFieldsInOrder(t *testing.T) {
	base, history, original := constraintHistory(t, true)
	changed := original.Clone()
	slices.Reverse(changed.Fields)
	replace := Migration{App: "scoped", Name: "0003_replace", Dependencies: []MigrationKey{history[1].Key()}, Operations: []Operation{
		&RemoveConstraint{AppLabel: "scoped", ModelName: "label", Constraint: original.Clone()},
		&AddConstraint{AppLabel: "scoped", ModelName: "label", Constraint: changed.Clone()},
	}}
	history = append(history, replace)
	loaded := testLoadedDefinitionSet(t, history)
	// Mutating all caller-owned operation forms cannot change the admitted set.
	history[1].Operations[0].(*AddConstraint).Constraint.Fields[0] = "id"
	replace.Operations[0].(*RemoveConstraint).Constraint.Fields[0] = "id"
	replace.Operations[1].(*AddConstraint).Constraint.Fields[0] = "id"
	for _, reverse := range []bool{false, true} {
		transactions := []backend.RevisionFencedTransaction{
			&constraintRecordingTransaction{lifecycleTestTransaction: newLifecycleTestTransaction()},
			&constraintRecordingTransaction{lifecycleTestTransaction: newLifecycleTestTransaction()},
		}
		records := lifecycleRecords(history[0].Key())
		request := LatestLifecycleRequest()
		if reverse {
			records = lifecycleRecords(history[0].Key(), history[1].Key(), history[2].Key())
			request = TargetedLifecycleRequest(NamedTarget(history[0].Key()))
		}
		session := newLifecycleTestSession(records, transactions)
		fake := newLifecycleTestBackend(session)
		fake.capabilities = lifecycleAllRelationCapabilities()
		fake.capabilities.UniqueConstraints = true
		state, err := (Executor{Backend: fake}).Migrate(t.Context(), loaded, request)
		if err != nil {
			t.Fatal("loaded constraint lifecycle", reverse, err)
		}
		model, _ := state.Model("scoped", "label")
		if reverse {
			if !state.Equal(base) {
				t.Fatal("reverse failed to restore exact initial model")
			}
		} else if len(model.UniqueConstraints) != 1 || !model.UniqueConstraints[0].Equal(changed) {
			t.Fatal("forward constraint changed")
		}
		if len(session.intents) != 2 {
			t.Fatal("constraint step inventory", len(session.intents))
		}
		for step, intent := range session.intents {
			calls := transactions[step].(*constraintRecordingTransaction).arguments
			if len(calls) != len(intent.Operations) {
				t.Fatal("schema editor did not execute every sealed constraint")
			}
			for index, operation := range intent.Operations {
				constraint, err := operation.ChangedConstraint()
				if err != nil || constraint.Name != original.Name {
					t.Fatal("sealed delta was overwritten by a later operation", err)
				}
				if index > 0 && !intent.Operations[index-1].After.Equal(operation.Before) {
					t.Fatal("same-step boundary continuity was lost")
				}
				if calls[index].kind != operation.Kind || !calls[index].model.Equal(operation.Before) || !calls[index].constraint.Equal(constraint) {
					t.Fatal("schema editor arguments differ from the sealed historical delta")
				}
				if len(operation.Targets) != 1 || !operation.Targets[0].TargetModel.Equal(operation.After) {
					t.Fatal("self target did not bind the exact resulting constraint metadata")
				}
				if _, err := migrationgraph.ResolveMigrationGraph("scoped", operation); err != nil {
					t.Fatal("self graph mismatch", err)
				}
			}
		}
	}
	reconstructor, err := loaded.Reconstructor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	latest, err := reconstructor.Reconstruct(LatestStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	model, _ := latest.Model("scoped", "label")
	if len(model.UniqueConstraints) != 1 || !model.UniqueConstraints[0].Equal(changed) {
		t.Fatal("backend argument mutation leaked into loaded history")
	}
	// Reversing AddField while its constraint remains must fail. Removing the
	// dependent constraint first makes the same field reversal valid.
	plain, _, constraint := constraintHistory(t, false)
	field := ir.Field{Name: "extra", GoName: "Extra", Column: "extra", Kind: ir.FieldText, Nullable: true}
	addField := AddField{AppLabel: "scoped", ModelName: "label", Field: field}
	withField, err := addField.stateForward(plain)
	if err != nil {
		t.Fatal(err)
	}
	constraint.Fields = []string{"extra", "name"}
	add := AddConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint}
	constrained, err := add.stateForward(withField)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := addField.stateBackward(constrained); err == nil || !got.Equal(constrained) {
		t.Fatal("field removal ignored a retained constraint")
	}
	freed, err := add.stateBackward(constrained)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := addField.stateBackward(freed); err != nil || !got.Equal(plain) {
		t.Fatal("ordered reversal failed", err)
	}
}

type constraintRecordingTransaction struct {
	*lifecycleTestTransaction
	arguments []constraintArguments
}

type constraintArguments struct {
	kind       backend.MigrationOperationKind
	model      ir.Model
	constraint ir.UniqueConstraint
}

func (tx *constraintRecordingTransaction) AddConstraint(ctx context.Context, model ir.Model, constraint ir.UniqueConstraint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.captureConstraint(backend.MigrationAddConstraint, model, constraint)
	return tx.call("add_constraint")
}

func (tx *constraintRecordingTransaction) RemoveConstraint(ctx context.Context, model ir.Model, constraint ir.UniqueConstraint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.captureConstraint(backend.MigrationRemoveConstraint, model, constraint)
	return tx.call("remove_constraint")
}

func (tx *constraintRecordingTransaction) captureConstraint(kind backend.MigrationOperationKind, model ir.Model, constraint ir.UniqueConstraint) {
	tx.arguments = append(tx.arguments, constraintArguments{kind: kind, model: model.Clone(), constraint: constraint.Clone()})
	// Deliberately corrupt both argument ownership boundaries. No subsequent
	// operation, intent, returned state or reloaded history may see these edits.
	constraint.Fields[0] = "backend_changed"
	for index := range model.UniqueConstraints {
		model.UniqueConstraints[index].Fields[0] = "backend_changed"
	}
}

func TestNamedConstraintCapabilityFailureAndRollbackPreserveHistory(t *testing.T) {
	base, history, constraint := constraintHistory(t, false)
	loaded := testLoadedDefinitionSet(t, history)
	for _, reverse := range []bool{false, true} {
		records := lifecycleRecords(history[0].Key())
		request := LatestLifecycleRequest()
		if reverse {
			records = lifecycleRecords(history[0].Key(), history[1].Key())
			request = TargetedLifecycleRequest(NamedTarget(history[0].Key()))
		}
		session := newLifecycleTestSession(records, nil)
		fake := newLifecycleTestBackend(session)
		_, err := (Executor{Backend: fake}).Migrate(t.Context(), loaded, request)
		var capability *backend.CapabilityError
		if !errors.As(err, &capability) || !strings.Contains(capability.Detail, "UniqueConstraints") || session.beginCount != 0 || session.closeCount != 1 {
			t.Fatal("constraint capability did not precede mutation", err)
		}
	}
	for _, reverse := range []bool{false, true} {
		failure := errors.New("constraint operation failure")
		tx := newLifecycleTestTransaction()
		kind := "add_constraint"
		records := lifecycleRecords(history[0].Key())
		request := LatestLifecycleRequest()
		want := base
		if reverse {
			kind = "remove_constraint"
			records = lifecycleRecords(history[0].Key(), history[1].Key())
			request = TargetedLifecycleRequest(NamedTarget(history[0].Key()))
			var err error
			want, err = (AddConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint}).stateForward(base)
			if err != nil {
				t.Fatal(err)
			}
		}
		tx.failures[kind] = failure
		session := newLifecycleTestSession(records, []backend.RevisionFencedTransaction{tx})
		fake := newLifecycleTestBackend(session)
		fake.capabilities.UniqueConstraints = true
		got, err := (Executor{Backend: fake}).Migrate(t.Context(), loaded, request)
		if !errors.Is(err, failure) || !got.Equal(want) || !reflect.DeepEqual(tx.calls, []string{kind, "rollback"}) {
			t.Fatal("failed constraint changed durable state, recorded history or committed", err, tx.calls)
		}
	}
}

func TestNamedConstraintSQLCannotBeEmptyOrLoseOperationIdentity(t *testing.T) {
	_, history, constraint := constraintHistory(t, false)
	history = append(history, Migration{App: "scoped", Name: "0003_remove", Dependencies: []MigrationKey{history[1].Key()}, Operations: []Operation{RemoveConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint}}})
	loaded := testLoadedDefinitionSet(t, history)
	for _, migration := range history[1:] {
		for _, group := range [][]string{{"physical constraint statement"}, nil, {""}} {
			renderer := &migrationSQLRendererSpy{groups: [][]string{group}}
			statements, err := RenderMigrationSQL(t.Context(), loaded, migration.Key(), renderer)
			if renderer.calls != 1 || len(renderer.request.Intent.Operations) != 1 {
				t.Fatal("renderer missed constraint operation", err)
			}
			got, deltaErr := renderer.request.Intent.Operations[0].ChangedConstraint()
			if deltaErr != nil || !got.Equal(constraint) {
				t.Fatal("renderer lost historical constraint", deltaErr)
			}
			if len(group) == 0 || group[0] == "" {
				assertMigrationSQLError(t, err, CategorySQLRender, CodeInvalidRenderedSQL, migration.Key())
				if statements != nil {
					t.Fatal("invalid SQL leaked partial output")
				}
			} else if err != nil || !reflect.DeepEqual(statements, group) {
				t.Fatal("constraint SQL lost", err)
			}
		}
	}
}
