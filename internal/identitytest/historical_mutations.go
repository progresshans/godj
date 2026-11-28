package identitytest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

// The caller sees a top-level result only when its own historical owner
// confirms the transaction. A second physical connection observes what was
// actually committed, including the two possible outcomes of a lost reply.
func historicalMutationResults(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	backend, observer := open(t)
	f := newHistoricalIdentityFixture(t, backend, 0)
	boundary := &handoffBackend{TransitionBackend: backend}
	runtime, err := systemstate.OpenIdentity(t.Context(), boundary, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher})
	handoffMust(t, err)
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	label := query.NewFieldRef("name", "name", query.FieldString, false)
	revision := query.NewFieldRef("revision", "revision", query.FieldInteger, false)
	const table = "godj_identity_group"
	groups := func(t *testing.T) []models.Group {
		return handoffRows(t, models.GroupObjects.Using(observer).OrderBy(models.GroupFields.ID.Asc()))
	}
	for _, mode := range []string{"confirmed", "rollback", "unknown_commit", "unknown_rollback", "write_missing", "write_twice"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			edit, err := models.GroupObjects.Create(ctx, backend, models.NewGroupCreate(mode+" before"))
			handoffMust(t, err)
			remove, err := models.GroupObjects.Create(ctx, backend, models.NewGroupCreate(mode+" remove"))
			handoffMust(t, err)
			before := groups(t)
			boundary.mode = mode
			start := boundary.writes
			addedName, editedName := mode+" inserted", mode+" updated"
			inserted, insertErr := runtime.Insert(ctx, query.NewInsertPlanReturningKey(table, []query.Assignment{
				query.NewAssignment(label, query.String(addedName)), query.NewAssignment(revision, query.Integer(1)),
			}, id))
			updated, updateErr := runtime.Update(ctx, query.NewUpdatePlan(table, []query.Assignment{query.NewAssignment(label, query.String(editedName))}, id, query.Integer(edit.ID)))
			deleted, deleteErr := runtime.Delete(ctx, query.NewDeletePlan(table, id, query.Integer(remove.ID)))
			if boundary.writes != start+3 {
				t.Fatal("top-level writes did not use exactly one fence each")
			}
			var wantErr error
			switch mode {
			case "rollback":
				wantErr = handoffFailure
			case "unknown_commit", "unknown_rollback":
				wantErr = &query.Error{Code: query.CodeCommitOutcomeUnknown}
			case "write_missing", "write_twice":
				wantErr = &query.Error{Code: query.CodeTransactionOutcomeUnknown}
			}
			for _, err := range []error{insertErr, updateErr, deleteErr} {
				if !errors.Is(err, wantErr) {
					t.Fatalf("incorrect write outcome: %v", err)
				}
			}
			if wantErr != nil && (inserted != 0 || updated != 0 || deleted != 0) {
				t.Fatal("unconfirmed mutation published a key or affected count")
			}
			after := groups(t)
			if mode != "confirmed" && mode != "unknown_commit" {
				if !reflect.DeepEqual(after, before) {
					t.Fatal("uncommitted writes changed the table")
				}
				return
			}
			added := slices.IndexFunc(after, func(g models.Group) bool { return g.Name == addedName })
			if added < 0 || after[added].ID <= 0 || after[added].Revision != 1 {
				t.Fatal("independent connection did not find the committed insert")
			}
			if mode == "confirmed" && (inserted != after[added].ID || updated != 1 || deleted != 1) {
				t.Fatal("confirmed mutation lost its native result")
			}
			want := slices.DeleteFunc(before, func(g models.Group) bool { return g.ID == remove.ID })
			for i := range want {
				if want[i].ID == edit.ID {
					want[i].Name = editedName
				}
			}
			if !reflect.DeepEqual(slices.Delete(after, added, added+1), want) {
				t.Fatal("committed group contents differ from the three writes")
			}
			if mode == "confirmed" {
				affected, err := runtime.Delete(ctx, query.NewDeletePlan(table, id, query.Integer(remove.ID)))
				if err != nil || affected != 0 || boundary.writes != start+4 {
					t.Fatal("confirmed no-match was changed or retried", err)
				}
			}
		})
	}
	t.Run("ownership_recheck", func(t *testing.T) { historicalMarkerWrites(t, runtime, f.runtime, observer, boundary) })
}

func historicalMarkerWrites(t *testing.T, runtime, native *systemstate.Runtime, observer TransitionBackend, boundary *handoffBackend) {
	ctx := t.Context()
	before := handoffMarker(t, observer)
	const table = "godj_system_credential"
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	key := query.Integer(before[0].(int64))
	active := query.NewFieldRef("active", "active", query.FieldBoolean, false)
	// All three are valid SQL writes. Each would violate ownership after the
	// statement, so the final recheck must roll it back and suppress the result.
	copyMarker := []query.Assignment{query.NewAssignment(active, query.Boolean(before[4].(bool)))}
	for _, field := range []struct {
		name  string
		value any
	}{
		{"principal_id", before[1]}, {"username", before[2]}, {"encoded_password", before[3]},
		{"permissions", before[5]}, {"definition_digest", before[6]},
	} {
		copyMarker = append(copyMarker, query.NewAssignment(query.NewFieldRef(field.name, field.name, query.FieldString, false), query.String(field.value.(string))))
	}
	update := query.NewUpdatePlan(table, []query.Assignment{query.NewAssignment(active, query.Boolean(true))}, id, key)
	ownership := &systemstate.Error{Code: systemstate.CodeCorruptState, Field: "credential"}
	for _, mode := range []string{"confirmed_rollback", "swallowed_recheck"} {
		boundary.mode = mode
		for _, write := range []func() (int64, error){
			func() (int64, error) {
				return runtime.Insert(ctx, query.NewInsertPlanReturningKey(table, copyMarker, id))
			},
			func() (int64, error) { return runtime.Update(ctx, update) },
			func() (int64, error) { return runtime.Delete(ctx, query.NewDeletePlan(table, id, key)) },
		} {
			start := boundary.writes
			value, err := write()
			unknown := errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})
			if value != 0 || !errors.Is(err, ownership) || unknown != (mode == "swallowed_recheck") || boundary.writes != start+1 ||
				!reflect.DeepEqual(handoffMarker(t, observer), before) {
				t.Fatal("historical write lost its ownership failure or published a mutation", mode, err)
			}
		}
	}
	boundary.mode = ""
	boundary.afterMarker = func(context.Context, db.Session) error { return handoffFailure }
	start, faults := boundary.writes, boundary.faults
	value, failure := runtime.Update(ctx, update)
	boundary.afterMarker = nil
	if value != 0 || failure != handoffFailure || boundary.writes != start+1 || boundary.faults != faults+1 ||
		!reflect.DeepEqual(handoffMarker(t, observer), before) {
		t.Fatal("statement result escaped alongside its error", failure)
	}
	// Borrowed statement results are provisional inside either public callback.
	for _, relation := range []bool{false, true} {
		calls := 0
		write := func(scope db.Session) error {
			calls++
			affected, err := scope.Update(ctx, update)
			if err != nil {
				return err
			}
			if affected != 1 {
				return errors.New("ownership-changing statement did not affect one row")
			}
			return nil
		}
		var err error
		if relation {
			err = native.AtomicRelation(ctx, func(scope db.RelationSession) error { return write(scope) })
		} else {
			err = native.Atomic(ctx, write)
		}
		if !errors.Is(err, ownership) || calls != 1 || !reflect.DeepEqual(handoffMarker(t, observer), before) {
			t.Fatal("callback transaction did not roll back its ownership change", err)
		}
	}
}
