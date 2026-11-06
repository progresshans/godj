package cascadetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/cascadefixture/details"
	"github.com/progresshans/godj/conformance/cascadefixture/parents"
	"github.com/progresshans/godj/conformance/cascadefixture/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// These are native coordination checks. They do not claim that Django exposes
// the same Go callback or provisional result API.
func runCollectedDeleteEffects(t *testing.T, backend ProductBackend, models []referenceModel, snapshot func(*testing.T) referenceSnapshot, clear func(*testing.T), setStoredRootKey func(context.Context, int64, int64) error) {
	t.Helper()
	policies, err := project.BindRelationDeleters()
	checkReference(t, err)
	for _, scenario := range []string{"commit", "root_zero", "root_negative", "protected", "before_error", "outer_error", "after_null_error", "late_delete_error", "wrong_null_count", "second_batch_error", "topology_added", "topology_retargeted", "commit_unknown", "rollback_unknown"} {
		t.Run("collected_effects_"+scenario, func(t *testing.T) {
			t.Cleanup(func() { clear(t) })
			ctx := t.Context()
			root, err := parents.RootObjects.Create(ctx, backend, parents.NewRootCreate("collected"))
			checkReference(t, err)
			if scenario == "root_zero" || scenario == "root_negative" {
				key := int64(0)
				if scenario == "root_negative" {
					key = -9
				}
				// Establish an existing, unreferenced row before loading it through
				// generated objects. This checks deletion key presence independently
				// of each backend's explicit generated-key insertion capability.
				checkReference(t, setStoredRootKey(ctx, root.ID, key))
				root, err = parents.RootObjects.Using(backend).Filter(parents.RootFields.ID.Exact(key)).Get(ctx)
				checkReference(t, err)
			}
			child, err := details.ChildObjects.Create(ctx, backend, details.NewChildCreate(root.ID))
			checkReference(t, err)
			grand, err := details.GrandchildObjects.Create(ctx, backend, details.NewGrandchildCreate(child.ID))
			checkReference(t, err)
			watcher, err := details.WatcherObjects.Create(ctx, backend, details.NewWatcherCreate().WithChildID(child.ID))
			checkReference(t, err)
			watchers := []int64{watcher.ID}
			if scenario == "second_batch_error" {
				for range query.MaximumRelationSetNullRows {
					row, err := details.WatcherObjects.Create(ctx, backend, details.NewWatcherCreate().WithChildID(child.ID))
					checkReference(t, err)
					watchers = append(watchers, row.ID)
				}
			}
			if scenario == "protected" {
				_, err := details.ProtectedObjects.Create(ctx, backend, details.NewProtectedCreate(grand.ID))
				checkReference(t, err)
			}
			other, err := parents.RootObjects.Create(ctx, backend, parents.NewRootCreate("unrelated"))
			checkReference(t, err)
			otherChild, err := details.ChildObjects.Create(ctx, backend, details.NewChildCreate(other.ID))
			checkReference(t, err)
			before, caller := snapshot(t), root
			failure := errors.New("native collected effects " + scenario)
			owner := &effectsNativeOwner{backend: backend, scenario: scenario, failure: failure}
			calls := 0
			var retained orm.RelationDeleteEffects
			var retainedScope db.SessionValidator
			count, resultErr := db.CoordinatedRelationAtomicResult(ctx, owner, func(work context.Context, scope db.RelationSession) (int64, error) {
				retainedScope = scope.(db.SessionValidator)
				count, err := policies.ParentsRoot.DeleteWithEffectsInSession(work, scope, root, func(work context.Context, effects orm.RelationDeleteEffects) error {
					calls++
					retained = effects
					if !reflect.DeepEqual(readReferenceSnapshot(t, work, scope, models), before) {
						t.Fatal("observer ran after durable relation writes")
					}
					want := map[effectRow]orm.RelationDeleteEffect{}
					add := func(app, model string, key int64, deleted bool, fields []string) {
						identity := ir.ModelIdentity{AppLabel: app, ModelName: model}
						want[effectRow{identity, key}] = orm.RelationDeleteEffect{Model: identity, Key: key, Deleted: deleted, NullFields: fields}
					}
					add("cascadeparents", "root", root.ID, true, nil)
					add("cascadedetails", "child", child.ID, true, nil)
					add("cascadedetails", "grandchild", grand.ID, true, nil)
					for _, key := range watchers {
						add("cascadedetails", "watcher", key, false, []string{"child"})
					}
					got := map[effectRow]orm.RelationDeleteEffect{}
					for _, row := range effects.Rows() {
						key := effectRow{row.Model, row.Key}
						if _, duplicate := got[key]; duplicate {
							t.Fatal("native collector repeated an effect", key)
						}
						got[key] = row
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatal("native effect set differs from rows", got, want)
					}
					// A companion write represents the transaction composition used
					// by lifecycle revision/audit/session owners.
					if _, err := parents.LabelObjects.Create(work, scope, parents.NewLabelCreate("effect-companion")); err != nil {
						return err
					}
					switch scenario {
					case "before_error":
						return failure
					case "topology_added":
						_, err := details.WatcherObjects.Create(work, scope, details.NewWatcherCreate().WithChildID(child.ID))
						return err
					case "topology_retargeted":
						_, err := details.WatcherObjects.Patch(work, scope, watcher, details.WatcherPatch{}.WithChildID(otherChild.ID))
						return err
					}
					return nil
				})
				if err == nil && scenario == "outer_error" {
					return 0, failure
				}
				return count, err
			})
			committed := scenario == "commit" || scenario == "root_zero" || scenario == "root_negative" || scenario == "commit_unknown"
			if root != caller || owner.calls != 1 {
				t.Fatal("borrowed operation changed caller state or retried")
			}
			if retainedScope == nil || retainedScope.ValidateSession(ctx) == nil {
				t.Fatal("native observer scope outlived its transaction")
			}
			wantCalls := 1
			if scenario == "protected" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatal("partial or repeated native effects callback", calls)
			}
			if committed && scenario != "commit_unknown" {
				if resultErr != nil || count != 3 {
					t.Fatal("native effect composition did not commit", count, resultErr)
				}
			} else if resultErr == nil || count != 0 {
				t.Fatal("failed or uncertain effects were published", count, resultErr)
			}
			switch scenario {
			case "before_error", "outer_error", "after_null_error", "late_delete_error", "second_batch_error", "commit_unknown", "rollback_unknown":
				if !errors.Is(resultErr, failure) {
					t.Fatal("native failure cause lost", resultErr)
				}
			case "wrong_null_count", "topology_retargeted":
				if !errors.Is(resultErr, &query.Error{Code: query.CodeUnexpectedRows}) {
					t.Fatal("changed collected rows were accepted", resultErr)
				}
			case "protected":
				var protected *query.ProtectedForeignKeyError
				if !errors.As(resultErr, &protected) || protected.ProtectedSourceRows() != 1 {
					t.Fatal("protection did not precede the observer", resultErr)
				}
			}
			if scenario != "protected" && scenario != "before_error" {
				expected := int64(1)
				if scenario == "second_batch_error" {
					expected = query.MaximumRelationSetNullRows
				}
				if scenario == "topology_retargeted" {
					expected = 0
				}
				if len(owner.nullCounts) == 0 || owner.nullCounts[0] != expected {
					t.Fatal("native SET_NULL prefix did not affect its exact collected rows", owner.nullCounts)
				}
			}
			if scenario == "second_batch_error" && (owner.nullCalls != 2 || owner.nullCounts[1] != 1) {
				t.Fatal("second native batch did not execute", owner.nullCounts)
			}
			if scenario == "before_error" && owner.nullCalls != 0 {
				t.Fatal("failed observer allowed relation mutation")
			}
			after := snapshot(t)
			if committed {
				if hasReferenceKey(after["cascadeparents.Root"], caller.ID) || hasReferenceKey(after["cascadedetails.Child"], child.ID) || len(after["cascadedetails.Grandchild"]) != 0 || len(after["cascadeparents.Label"]) != 1 {
					t.Fatal("native commit lost companion or graph changes", after)
				}
				if !hasReferenceKey(after["cascadeparents.Root"], other.ID) || !hasReferenceKey(after["cascadedetails.Child"], otherChild.ID) {
					t.Fatal("delete changed an uncollected row")
				}
				for _, row := range after["cascadedetails.Watcher"] {
					if row[1] != nil {
						t.Fatal("collected survivor was not nulled")
					}
				}
				if len(retained.Rows()) != 4 {
					t.Fatal("retained effects changed after commit")
				}
			} else if !reflect.DeepEqual(after, before) {
				t.Fatal("native failure did not roll back companion and relation writes", resultErr, after, before)
			}
		})
	}
}

type effectRow struct {
	model ir.ModelIdentity
	key   int64
}
type effectsNativeOwner struct {
	backend          db.CoordinatedRelationAtomic
	scenario         string
	failure          error
	calls, nullCalls int
	nullCounts       []int64
}

func (owner *effectsNativeOwner) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	owner.calls++
	err := owner.backend.CoordinatedAtomicRelation(ctx, func(scope db.RelationSession) error { return callback(&effectsNativeSession{scope, owner}) })
	if owner.scenario == "commit_unknown" {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown, Cause: owner.failure})
	}
	if owner.scenario == "rollback_unknown" {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown, Cause: owner.failure})
	}
	return err
}

type effectsNativeSession struct {
	db.RelationSession
	owner *effectsNativeOwner
}

func (scope *effectsNativeSession) ValidateSession(ctx context.Context) error {
	return scope.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *effectsNativeSession) RelationSetNull(ctx context.Context, plan query.RelationSetNullPlan) (int64, error) {
	count, err := scope.RelationSession.RelationSetNull(ctx, plan)
	scope.owner.nullCalls++
	scope.owner.nullCounts = append(scope.owner.nullCounts, count)
	if err == nil {
		switch scope.owner.scenario {
		case "after_null_error", "rollback_unknown":
			return 0, scope.owner.failure
		case "wrong_null_count":
			return count + 1, nil
		case "second_batch_error":
			if scope.owner.nullCalls == 2 {
				return 0, scope.owner.failure
			}
		}
	}
	return count, err
}
func (scope *effectsNativeSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	count, err := scope.RelationSession.Delete(ctx, plan)
	if err == nil && scope.owner.scenario == "late_delete_error" && plan.Table() == "cascade_child" {
		if count != 1 {
			return 0, fmt.Errorf("late failure did not follow one actual child delete: %d", count)
		}
		return 0, scope.owner.failure
	}
	return count, err
}
