package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"

	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type upsertNativeCase struct {
	Case        string
	Value       json.RawMessage
	Updates     int      `json:"update_statements"`
	Commands    []string `json:"transaction_commands"`
	LockClauses []string `json:"select_lock_clauses"`
}

type upsertNativeReference struct {
	Cases      []upsertNativeCase
	Contention []json.RawMessage
	FinalRows  json.RawMessage `json:"final_rows"`
}

func readUpsertReference(t *testing.T, postgres bool) upsertNativeReference {
	t.Helper()
	backend := "sqlite"
	if postgres {
		backend = "postgres"
	}
	data, err := os.ReadFile("update_or_create-django61-" + backend + ".json")
	check(t, err)
	var result upsertNativeReference
	check(t, json.Unmarshal(data, &result))
	if len(result.Cases) != 14 || len(result.Contention) != 2 {
		t.Fatal("incomplete independent reference")
	}
	return result
}

func compareNativeJSON(t *testing.T, expected json.RawMessage, actual any) {
	t.Helper()
	data, err := json.Marshal(actual)
	check(t, err)
	var left, right any
	check(t, json.Unmarshal(expected, &left))
	check(t, json.Unmarshal(data, &right))
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("native result differs (%s): Go=%v Django=%v", t.Name(), right, left)
	}
}

func referenceError(t *testing.T, err error) map[string]any {
	t.Helper()
	for code, name := range map[string]string{
		query.CodeDoesNotExist: "DoesNotExist", query.CodeMultipleObjectsReturned: "MultipleObjectsReturned",
		query.CodeUniqueConstraint: "IntegrityError", query.CodeUnsupported: "NotSupportedError",
		query.CodeTransactionRequired: "TransactionManagementError", query.CodeInvalidValue: "ValueError",
	} {
		if errors.Is(err, &query.Error{Code: code}) {
			return map[string]any{"error": name}
		}
	}
	t.Fatalf("unexpected reference error: %v", err)
	return nil
}

func upsertSnapshot(value owners.UpsertItem, created bool) map[string]any {
	return map[string]any{"name": value.Name, "detail": value.Detail, "revision": value.Revision, "created": created}
}

func TestUpdateOrCreateReference(t *testing.T) {
	withCollectionBackends(t, func(t *testing.T, backend collectionBackend, _ func() (collectionBackend, error), _ func(string) error, postgres bool) {
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateCollections(t, backend)
		ctx := t.Context()
		probe := &upsertProbe{collectionBackend: backend}
		reference := readUpsertReference(t, postgres)
		api, err := project.Using(probe)
		check(t, err)
		var plans []query.Plan
		probe.beforeQuery = func(_ context.Context, plan query.Plan) error { plans = append(plans, plan); return nil }
		base := api.OwnersUpsertItem
		byName := func(name string) project.OwnersUpsertItemQuery {
			return base.Filter(owners.UpsertItemFields.Name.Exact(name))
		}
		for _, native := range reference.Cases {
			t.Run(native.Case, func(t *testing.T) {
				calls := make([]map[string]any, 0)
				factory := func(label string) string {
					calls = append(calls, map[string]any{"label": label, "atomic": probe.activeScopes.Load() > 0})
					return label
				}
				updates, transactions, savepoints := probe.updates.Load(), probe.transactions.Load(), probe.savepoints.Load()
				plans = nil
				var actual any
				// Explicit SQLite locks and preserving configured upsert locks are
				// deliberate Go contracts, not a rewrite of the native fixture.
				sqliteLockDifference := !postgres && slices.Contains([]string{"for_update_outside_transaction", "no_key_skip_locked_and_of_self", "nullable_join_unqualified_lock", "nullable_join_locks_only_self", "native_upsert_resets_explicit_lock_options"}, native.Case)
				switch native.Case {
				case "separate_create_defaults", "existing_uses_update_defaults_only", "omitted_create_defaults_uses_update_defaults":
					name, createLabel, updateLabel, revision := "separate", "create", "update", int64(7)
					if native.Case == "existing_uses_update_defaults_only" {
						createLabel, updateLabel, revision = "unused", "updated", 99
					} else if native.Case == "omitted_create_defaults_uses_update_defaults" {
						// Go inputs are explicit; the same authored default is supplied
						// to both lazy branches, and only creation may evaluate it.
						name, createLabel, updateLabel, revision = "shared", "shared-value", "shared-value", 0
					}
					value, created, err := byName(name).UpdateOrCreate(ctx,
						singleCreate[owners.UpsertItem](func() orm.Mutation[owners.UpsertItem] {
							return owners.NewUpsertItemCreate(name).WithDetail(factory(createLabel)).WithRevision(revision).BuildCreate()
						}), singlePatch[owners.UpsertItem](func(current owners.UpsertItem) orm.Mutation[owners.UpsertItem] {
							return (owners.UpsertItemPatch{}.WithDetail(factory(updateLabel))).BuildPatch(current)
						}))
					check(t, err)
					result := upsertFacadeSnapshot(t, value, created)
					result["calls"] = calls
					actual = result
				case "existing_empty_defaults":
					value, created, err := byName("separate").UpdateOrCreate(ctx, nil, owners.UpsertItemPatch{})
					check(t, err)
					actual = upsertFacadeSnapshot(t, value, created)
				case "existing_update_unique_failure":
					_, _, err := byName("separate").UpdateOrCreate(ctx, nil, singlePatch[owners.UpsertItem](func(current owners.UpsertItem) orm.Mutation[owners.UpsertItem] {
						return (owners.UpsertItemPatch{}.WithName(factory("shared")).WithDetail("uncommitted")).BuildPatch(current)
					}))
					result := referenceError(t, err)
					result["calls"] = calls
					actual = result
				case "outer_rollback_undoes_update_and_creation":
					provisional := make([]map[string]any, 0, 2)
					rollback := errors.New("authored parent rollback")
					err := probe.Atomic(ctx, func(session db.Session) error {
						bound := owners.UpsertItemObjects.Using(session)
						value, created, err := bound.Filter(owners.UpsertItemFields.Name.Exact("separate")).UpdateOrCreate(ctx, nil, owners.UpsertItemPatch{}.WithDetail("parent-change"))
						if err != nil {
							return err
						}
						provisional = append(provisional, upsertSnapshot(value, created))
						value, created, err = bound.Filter(owners.UpsertItemFields.Name.Exact("parent-new")).UpdateOrCreate(ctx, owners.NewUpsertItemCreate("parent-new").WithDetail("parent-create"), nil)
						if err != nil {
							return err
						}
						provisional = append(provisional, upsertSnapshot(value, created))
						return rollback
					})
					if !errors.Is(err, rollback) {
						t.Fatal("parent outcome changed", err)
					}
					old, err := byName("separate").Get(ctx)
					check(t, err)
					count, err := byName("parent-new").Count(ctx)
					check(t, err)
					actual = map[string]any{"provisional": provisional, "old_detail": old.Detail, "new_row_exists": count != 0}
				case "filtered_create_can_leave_predicate":
					value, created, err := byName("outside-filter").Filter(owners.UpsertItemFields.Detail.Exact("required")).UpdateOrCreate(ctx, owners.NewUpsertItemCreate("outside-filter").WithDetail("outside"), nil)
					check(t, err)
					actual = upsertFacadeSnapshot(t, value, created)
				case "ambiguous_never_resolves_defaults":
					for _, name := range []string{"ambiguous-a", "ambiguous-b"} {
						_, err := owners.UpsertItemObjects.Create(ctx, backend, owners.NewUpsertItemCreate(name).WithDetail("ambiguous"))
						check(t, err)
					}
					_, _, err := base.Filter(owners.UpsertItemFields.Detail.Exact("ambiguous")).UpdateOrCreate(ctx,
						singleCreate[owners.UpsertItem](func() orm.Mutation[owners.UpsertItem] {
							return owners.NewUpsertItemCreate(factory("also-not-run")).BuildCreate()
						}),
						singlePatch[owners.UpsertItem](func(current owners.UpsertItem) orm.Mutation[owners.UpsertItem] {
							return (owners.UpsertItemPatch{}.WithName(factory("not-run"))).BuildPatch(current)
						}))
					result := referenceError(t, err)
					result["calls"] = calls
					actual = result
				case "for_update_outside_transaction":
					_, err := byName("separate").SelectForUpdate(orm.RowLockOptions{}).All(ctx)
					actual = referenceError(t, err)
				case "nowait_and_skip_locked_rejected_at_construction":
					_, err := base.SelectForUpdatePaths(orm.RowLockOptions{NoWait: true, SkipLocked: true}, "self")
					actual = referenceError(t, err)
				case "no_key_skip_locked_and_of_self", "nullable_join_unqualified_lock", "nullable_join_locks_only_self", "native_upsert_resets_explicit_lock_options":
					err := probe.Atomic(ctx, func(session db.Session) error {
						bound, err := project.UsingSession(session)
						if err != nil {
							return err
						}
						q := bound.OwnersUpsertItem.Filter(owners.UpsertItemFields.Name.Exact("separate"))
						if native.Case == "no_key_skip_locked_and_of_self" {
							value, err := q.SelectForUpdate(orm.RowLockOptions{NoKey: true, SkipLocked: true}, q.LockTarget()).Get(ctx)
							if err != nil {
								return err
							}
							actual = map[string]any{"name": value.Name}
							return nil
						}
						if native.Case == "native_upsert_resets_explicit_lock_options" {
							value, created, err := q.SelectForUpdate(orm.RowLockOptions{NoKey: true, NoWait: true}, q.LockTarget()).UpdateOrCreate(ctx, nil, owners.UpsertItemPatch{}.WithDetail("after configured lock"))
							if err != nil {
								return err
							}
							actual = upsertFacadeSnapshot(t, value, created)
							return nil
						}
						eager := q.SelectRelated(q.Related.Category)
						targets := []orm.RowLockTarget[owners.UpsertItem]{}
						if native.Case == "nullable_join_locks_only_self" {
							targets = append(targets, q.LockTarget())
						}
						value, err := eager.SelectForUpdate(orm.RowLockOptions{}, targets...).Get(ctx)
						if err != nil {
							return err
						}
						actual = map[string]any{"name": value.Name, "category": value.CategoryID}
						return nil
					})
					if err != nil {
						actual = referenceError(t, err)
					}
				default:
					t.Fatal("unconsumed native case", native.Case)
				}
				if sqliteLockDifference {
					if _, nativeFailed := mapFromJSON(t, native.Value)["error"]; nativeFailed {
						t.Fatal("native SQLite no longer ignores the explicit lock")
					}
					if !reflect.DeepEqual(actual, map[string]any{"error": "NotSupportedError"}) {
						t.Fatal("Go SQLite must reject every explicit lock", actual)
					}
					if probe.updates.Load() != updates {
						t.Fatal("unsupported SQLite request changed storage")
					}
				} else {
					compareNativeJSON(t, native.Value, actual)
					if probe.updates.Load()-updates != int64(native.Updates) {
						t.Fatal("UPDATE statement count differs", native.Updates, probe.updates.Load()-updates)
					}
					count := func(command string) int64 {
						var n int64
						for _, value := range native.Commands {
							if value == command {
								n++
							}
						}
						return n
					}
					if probe.transactions.Load()-transactions != count("BEGIN") || probe.savepoints.Load()-savepoints != count("SAVEPOINT") {
						t.Fatal("transaction/savepoint ownership differs from native branch")
					}
				}
				if postgres && native.Case == "native_upsert_resets_explicit_lock_options" {
					if !slices.Equal(native.LockClauses, []string{"FOR UPDATE"}) {
						t.Fatal("native configured-lock behavior changed")
					}
					if len(plans) != 1 {
						t.Fatal("configured upsert re-read unexpectedly", len(plans))
					}
					lock, ok := plans[0].RowLock()
					if !ok || lock.Strength() != query.LockForNoKeyUpdate || lock.WaitPolicy() != query.LockNoWait || len(lock.Targets()) != 1 || !lock.Targets()[0].Self() {
						t.Fatal("Go upsert discarded explicit lock options")
					}
				}
			})
		}
		t.Run("final_rows", func(t *testing.T) {
			rows, err := base.OrderBy(owners.UpsertItemFields.Name.Asc()).All(ctx)
			check(t, err)
			actual := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				actual = append(actual, map[string]any{"name": row.Name, "detail": row.Detail, "revision": row.Revision})
			}
			var expected []map[string]any
			check(t, json.Unmarshal(reference.FinalRows, &expected))
			if !postgres {
				for _, row := range expected {
					if row["name"] == "separate" {
						if row["detail"] != "after configured lock" {
							t.Fatal("native final state changed")
						}
						row["detail"] = "updated"
					}
				}
			}
			data, err := json.Marshal(expected)
			check(t, err)
			compareNativeJSON(t, data, actual)
		})
	})
}

func mapFromJSON(t *testing.T, data json.RawMessage) map[string]any {
	t.Helper()
	var value map[string]any
	check(t, json.Unmarshal(data, &value))
	return value
}

func upsertFacadeSnapshot(t *testing.T, value *project.OwnersUpsertItem, created bool) map[string]any {
	t.Helper()
	model, err := value.Unwrap()
	check(t, err)
	return upsertSnapshot(model, created)
}

func compareUpsertLockOptions(t *testing.T, actual any) {
	t.Helper()
	var native struct {
		Options json.RawMessage `json:"lock_options_while_held"`
	}
	check(t, json.Unmarshal(readUpsertReference(t, true).Contention[0], &native))
	compareNativeJSON(t, native.Options, actual)
}

func compareUpsertContention(t *testing.T, postgres bool, index int, results map[int]upsertRaceResult, creates, patches [2]int64, probes [2]*upsertProbe, visible []int64, final owners.UpsertCounter) {
	t.Helper()
	var native struct {
		Case     string
		Results  []json.RawMessage
		Visible  json.RawMessage `json:"visible_before_release"`
		Final    json.RawMessage `json:"final_rows"`
		Blocking bool            `json:"native_blocking_observed"`
		Busy     bool            `json:"native_busy_observed"`
	}
	check(t, json.Unmarshal(readUpsertReference(t, postgres).Contention[index], &native))
	names := []string{"existing_update_reports_busy", "missing_create_reports_busy"}
	if postgres {
		names = []string{"existing_row_serializes_updates", "unique_creation_loser_locks_then_updates"}
	}
	if native.Case != names[index] || len(native.Results) != 2 || native.Blocking != postgres || native.Busy == postgres {
		t.Fatal("contention inventory changed", native)
	}
	compareNativeJSON(t, native.Visible, visible)
	compareNativeJSON(t, native.Final, []map[string]any{{"name": final.Name, "counter": final.Counter}})
	for index := range 2 {
		value := results[index]
		actual := map[string]any{"create_factory_calls": creates[index], "update_factory_calls": patches[index], "observed_before_update": value.observed}
		if value.err != nil {
			requireUpsertBusy(t, value.err)
			var code interface{ Code() int }
			if !errors.As(value.err, &code) {
				t.Fatal(value.err)
			}
			actual["error"] = "OperationalError"
			actual["sqlite_errorcode"] = code.Code() & 255
		} else {
			actual["counter"] = value.amount
			actual["created"] = value.created
			actual["locked_selects"] = probes[index].lockedReads.Load()
		}
		expected := mapFromJSON(t, native.Results[index])
		// Driver SQL spelling is not the Go contract. Count the owned scopes
		// while comparing every branch, value, native error and lock read.
		commands, ok := expected["transaction_commands"].([]any)
		if !ok {
			t.Fatal("missing native scope trace")
		}
		var begins, saves int64
		for _, command := range commands {
			if command == "BEGIN" {
				begins++
			}
			if command == "SAVEPOINT" {
				saves++
			}
		}
		if probes[index].transactions.Load() != begins || probes[index].savepoints.Load() != saves {
			t.Fatal("contention repeated or omitted an owned scope")
		}
		delete(expected, "transaction_commands")
		data, err := json.Marshal(expected)
		check(t, err)
		compareNativeJSON(t, data, actual)
	}
}
