package consumer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type rowLockNativeCase struct {
	Case    string
	Value   json.RawMessage
	Probes  json.RawMessage
	Result  json.RawMessage
	Durable bool `json:"durable_child"`
}

type rowLockNativeReference struct {
	Terminals, Targets, Contention []rowLockNativeCase
	Final                          json.RawMessage `json:"final_rows"`
}

func readRowLockReference(t *testing.T, postgres bool) rowLockNativeReference {
	t.Helper()
	backend := "sqlite"
	if postgres {
		backend = "postgres"
	}
	data, err := os.ReadFile("row_lock-django61-" + backend + ".json")
	check(t, err)
	var result rowLockNativeReference
	check(t, json.Unmarshal(data, &result))
	if len(result.Terminals) != 10 || len(result.Targets) != 8 || postgres && len(result.Contention) != 8 || !postgres && len(result.Contention) != 0 {
		t.Fatal("incomplete row lock reference")
	}
	return result
}

func optionalReferenceInteger(value orm.Optional[int64]) any {
	if integer, present := value.Get(); present {
		return integer
	}
	return nil
}

func rowLockReferenceError(t *testing.T, err error) map[string]any {
	t.Helper()
	if errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || errors.Is(err, &query.Error{Code: query.CodeUnknownField}) || errors.Is(err, &query.Error{Code: query.CodeUnknownRelation}) {
		return map[string]any{"error": "FieldError", "sqlstate": nil}
	}
	result := referenceError(t, err)
	result["sqlstate"] = nil
	return result
}

func TestRowLockReference(t *testing.T) {
	withCollectionBackends(t, func(t *testing.T, backend collectionBackend, open func() (collectionBackend, error), exec func(string) error, postgres bool) {
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateCollections(t, backend)
		ctx := t.Context()
		native := readRowLockReference(t, postgres)
		first, err := owners.UpsertItemObjects.Create(ctx, backend, owners.NewUpsertItemCreate("first"))
		check(t, err)
		second, err := owners.UpsertItemObjects.Create(ctx, backend, owners.NewUpsertItemCreate("second").WithRevision(7))
		check(t, err)
		plain := owners.UpsertItemObjects.Using(backend)
		locked := plain.SelectForUpdate(orm.RowLockOptions{})
		t.Run("terminals", func(t *testing.T) {
			for _, entry := range native.Terminals {
				t.Run(entry.Case, func(t *testing.T) {
					var value any
					var failure error
					switch entry.Case {
					case "count_outside_transaction":
						value, failure = locked.Count(ctx)
					case "max_outside_transaction":
						value, failure = orm.AggregateInto(ctx, locked, orm.Aggregate1(orm.Max(owners.UpsertItemFields.Revision), func(v orm.Optional[int64]) map[string]any {
							return map[string]any{"highest": optionalReferenceInteger(v)}
						}))
					case "exists_outside_transaction":
						value, failure = locked.Exists(ctx)
					case "ordered_first_outside_transaction":
						row, found, err := locked.OrderBy(owners.UpsertItemFields.Revision.Desc()).First(ctx)
						failure = err
						if err == nil && !found {
							t.Fatal("seed missing")
						}
						value = row.Name
					case "empty_in_get_outside_transaction":
						_, failure = locked.Filter(owners.UpsertItemFields.ID.In()).Get(ctx)
					case "empty_in_exists_outside_transaction":
						value, failure = locked.Filter(owners.UpsertItemFields.ID.In()).Exists(ctx)
					case "empty_in_aggregate_outside_transaction":
						value, failure = orm.AggregateInto(ctx, locked.Filter(owners.UpsertItemFields.ID.In()), orm.Aggregate2(orm.Min(owners.UpsertItemFields.Revision), orm.CountRows[owners.UpsertItem](), func(low orm.Optional[int64], count int64) map[string]any {
							return map[string]any{"lowest": optionalReferenceInteger(low), "count": count}
						}))
					case "selected_values_lock_self_inside_transaction", "distinct_selected_values_inside_transaction":
						failure = backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
							q := owners.UpsertItemObjects.Using(session)
							if entry.Case == "selected_values_lock_self_inside_transaction" {
								q = q.Filter(owners.UpsertItemFields.Name.Exact("first")).SelectForUpdate(orm.RowLockOptions{}, q.LockTarget())
							} else {
								q = q.OrderBy(owners.UpsertItemFields.Name.Asc()).Distinct().SelectForUpdate(orm.RowLockOptions{})
							}
							values, err := orm.SelectInto(ctx, q, orm.Project1(owners.UpsertItemFields.Name, func(name string) map[string]any { return map[string]any{"name": name} }))
							if err != nil {
								return err
							}
							value = values
							if entry.Case == "selected_values_lock_self_inside_transaction" {
								if len(values) != 1 {
									return errors.New("projected row cardinality changed")
								}
								value = values[0]
							}
							return nil
						})
					case "locking_clone_does_not_reuse_warm_cache":
						failure = backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
							api, err := project.UsingSession(session)
							if err != nil {
								return err
							}
							base := api.OwnersUpsertItem.Filter(owners.UpsertItemFields.ID.Exact(first.ID))
							warm, err := base.All(ctx)
							if err != nil {
								return err
							}
							if len(warm) != 1 {
								return errors.New("warm source missing")
							}
							_, err = owners.UpsertItemObjects.Patch(ctx, session, first, owners.UpsertItemPatch{}.WithRevision(9))
							if err != nil {
								return err
							}
							current, lockErr := base.SelectForUpdate(orm.RowLockOptions{}).Get(ctx)
							cached, err := base.All(ctx)
							if err != nil {
								return err
							}
							if len(cached) != 1 || cached[0].Revision != 0 || warm[0].Revision != 0 {
								return errors.New("locking changed the source cache")
							}
							if lockErr != nil {
								return lockErr
							}
							value = map[string]any{"before": warm[0].Revision, "fresh": current.Revision, "base_still_cached": cached[0].Revision, "different_instance": warm[0] != current}
							return nil
						})
					default:
						t.Fatal("unconsumed native terminal", entry.Case)
					}
					if failure != nil {
						value = referenceError(t, failure)
					}
					// SQLite rejects even an explicit lock on empty membership.
					// PostgreSQL omits the empty SELECT, as the native oracle does.
					if !postgres && entry.Case != "count_outside_transaction" && entry.Case != "max_outside_transaction" && entry.Case != "empty_in_aggregate_outside_transaction" {
						data, err := json.Marshal(map[string]any{"error": "NotSupportedError"})
						check(t, err)
						compareNativeJSON(t, data, value)
					} else {
						compareNativeJSON(t, entry.Value, value)
					}
				})
			}
		})
		_, err = owners.UpsertItemObjects.Delete(ctx, backend, &first)
		check(t, err)
		_, err = owners.UpsertItemObjects.Delete(ctx, backend, &second)
		check(t, err)
		region, err := owners.UpsertRegionObjects.Create(ctx, backend, owners.NewUpsertRegionCreate("region"))
		check(t, err)
		category, err := owners.UpsertCategoryObjects.Create(ctx, backend, owners.NewUpsertCategoryCreate("category").WithRegionID(region.ID))
		check(t, err)
		joined, err := owners.UpsertItemObjects.Create(ctx, backend, owners.NewUpsertItemCreate("joined").WithCategoryID(category.ID))
		check(t, err)
		_, err = owners.UpsertItemObjects.Create(ctx, backend, owners.NewUpsertItemCreate("orphan"))
		check(t, err)
		relations, err := project.BindRelations()
		check(t, err)
		perform := func(session db.Session, name string) (any, error) {
			api, err := project.UsingSession(session)
			if err != nil {
				return nil, err
			}
			q := api.OwnersUpsertItem.Filter(owners.UpsertItemFields.Name.Exact("joined"), relations.OwnersUpsertItem.Category.IsNull(false))
			related := relations.OwnersUpsertItem.Category
			projection := func(target orm.RowLockTarget[owners.UpsertItem], root bool) (any, error) {
				locked := q.SelectForUpdate(orm.RowLockOptions{}, target)
				var values []map[string]any
				var err error
				if root {
					values, err = project.SelectOwnersUpsertItemInto(ctx, locked, orm.Project2(owners.UpsertItemFields.Name, related.Name, func(name string, category *string) map[string]any {
						return map[string]any{"name": name, "category__name": category}
					}))
				} else {
					values, err = project.SelectOwnersUpsertItemInto(ctx, locked, orm.Project1(related.Name, func(category *string) map[string]any { return map[string]any{"category__name": category} }))
				}
				if err != nil {
					return nil, err
				}
				if len(values) != 1 {
					return nil, errors.New("related projection cardinality changed")
				}
				return values[0], nil
			}
			if name == "relation_value_projection_lock_target" {
				return projection(related.LockTarget(), false)
			}
			if name == "self_lock_when_projection_omits_self" || name == "projection_omitting_self_actual_locks" {
				return projection(q.LockTarget(), false)
			}
			if name == "self_lock_when_projection_includes_self" || name == "projection_including_self_actual_locks" {
				return projection(q.LockTarget(), true)
			}
			if name == "filter_join_does_not_make_lock_target_selected" {
				value, err := q.Filter(related.Name.Exact("category")).SelectForUpdate(orm.RowLockOptions{}, related.LockTarget()).Get(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"name": value.Name}, nil
			}
			eager := q.SelectRelated(q.Related.Category)
			var targets []orm.RowLockTarget[owners.UpsertItem]
			options := orm.RowLockOptions{}
			switch name {
			case "default_selection_locks_root_and_related":
			case "self_only_leaves_related_available":
				targets = []orm.RowLockTarget[owners.UpsertItem]{q.LockTarget()}
			case "lock_selected_related_model", "related_only_leaves_root_available":
				targets = []orm.RowLockTarget[owners.UpsertItem]{related.LockTarget()}
			case "unknown_selected_lock_target":
				_, err := eager.SelectForUpdatePaths(options, "missing")
				if err == nil {
					return nil, errors.New("unknown target accepted")
				}
				return nil, err
			case "nested_selected_lock_target", "nested_target_only_locks_region":
				eager = q.Filter(related.Region().IsNull(false)).SelectRelated(q.Related.Category.WithChildren(api.OwnersUpsertCategory.Related.Region))
				targets = []orm.RowLockTarget[owners.UpsertItem]{related.Region().LockTarget()}
			case "no_key_update_for_two_selected_models":
				options.NoKey = true
				targets = []orm.RowLockTarget[owners.UpsertItem]{q.LockTarget(), related.LockTarget()}
			default:
				return nil, fmt.Errorf("unconsumed lock target %s", name)
			}
			value, err := eager.SelectForUpdate(options, targets...).Get(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"name": value.Name}, nil
		}
		t.Run("targets", func(t *testing.T) {
			for _, entry := range native.Targets {
				t.Run(entry.Case, func(t *testing.T) {
					var actual any
					err := backend.(db.Atomic).Atomic(ctx, func(session db.Session) error { var err error; actual, err = perform(session, entry.Case); return err })
					if err != nil {
						actual = rowLockReferenceError(t, err)
					}
					if !postgres {
						name := "NotSupportedError"
						if entry.Case == "unknown_selected_lock_target" {
							name = "FieldError"
						}
						data, err := json.Marshal(map[string]any{"error": name, "sqlstate": nil})
						check(t, err)
						compareNativeJSON(t, data, actual)
					} else if entry.Case == "filter_join_does_not_make_lock_target_selected" || entry.Case == "relation_value_projection_lock_target" {
						compareNativeJSON(t, entry.Value, map[string]any{"error": "FieldError", "sqlstate": nil})
						want := map[string]any{"name": "joined"}
						if entry.Case == "relation_value_projection_lock_target" {
							want = map[string]any{"category__name": "category"}
						}
						data, err := json.Marshal(want)
						check(t, err)
						compareNativeJSON(t, data, actual)
					} else {
						compareNativeJSON(t, entry.Value, actual)
					}
				})
			}
		})
		if postgres {
			contender, err := open()
			check(t, err)
			t.Cleanup(func() { check(t, contender.Close()) })
			check(t, exec("SET lock_timeout = '500ms'"))
			t.Run("contention", func(t *testing.T) {
				for _, entry := range native.Contention {
					t.Run(entry.Case, func(t *testing.T) {
						if entry.Case == "foreign_key_insert_with_update" || entry.Case == "foreign_key_insert_with_no_key_update" {
							noKey := entry.Case == "foreign_key_insert_with_no_key_update"
							name := "foreign_key_update"
							if noKey {
								name = "foreign_key_no_key"
							}
							var outcome map[string]any
							check(t, backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
								_, err := owners.UpsertCategoryObjects.Using(session).Filter(owners.UpsertCategoryFields.ID.Exact(category.ID)).SelectForUpdate(orm.RowLockOptions{NoKey: noKey}).Get(ctx)
								if err != nil {
									return err
								}
								err = exec(fmt.Sprintf("INSERT INTO gdj_upsert_item (name,detail,revision,category_id) VALUES ('%s','',0,%d)", name, category.ID))
								outcome = map[string]any{"committed": true}
								if err != nil {
									var native *pgconn.PgError
									if !errors.As(err, &native) || native.Code != "55P03" {
										return err
									}
									outcome = map[string]any{"error": "OperationalError", "sqlstate": native.Code}
								}
								return nil
							}))
							compareNativeJSON(t, entry.Result, outcome)
							rows, err := owners.UpsertItemObjects.Using(backend).Filter(owners.UpsertItemFields.Name.Exact(name)).All(ctx)
							check(t, err)
							if (len(rows) == 1) != entry.Durable || len(rows) > 1 {
								t.Fatal("FK contender durability differs", rows)
							}
							if len(rows) == 1 {
								_, err = owners.UpsertItemObjects.Delete(ctx, backend, &rows[0])
								check(t, err)
							}
							return
						}
						actual := make(map[string]any)
						check(t, backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
							_, err := perform(session, entry.Case)
							if err != nil {
								return err
							}
							for _, model := range []string{"Item", "Category", "Region"} {
								err := contender.(db.Atomic).Atomic(ctx, func(other db.Session) error {
									switch model {
									case "Item":
										_, err := owners.UpsertItemObjects.Using(other).Filter(owners.UpsertItemFields.ID.Exact(joined.ID)).SelectForUpdate(orm.RowLockOptions{NoWait: true}).Get(ctx)
										return err
									case "Category":
										_, err := owners.UpsertCategoryObjects.Using(other).Filter(owners.UpsertCategoryFields.ID.Exact(category.ID)).SelectForUpdate(orm.RowLockOptions{NoWait: true}).Get(ctx)
										return err
									default:
										_, err := owners.UpsertRegionObjects.Using(other).Filter(owners.UpsertRegionFields.ID.Exact(region.ID)).SelectForUpdate(orm.RowLockOptions{NoWait: true}).Get(ctx)
										return err
									}
								})
								if err == nil {
									actual[model] = map[string]any{"locked": false}
								} else {
									var native *pgconn.PgError
									if !errors.As(err, &native) || native.Code != "55P03" {
										return err
									}
									actual[model] = map[string]any{"error": "OperationalError", "sqlstate": native.Code}
								}
							}
							return nil
						}))
						if entry.Case == "projection_omitting_self_actual_locks" {
							expected := mapFromJSON(t, entry.Probes)
							category, err := json.Marshal(expected["Category"])
							check(t, err)
							compareNativeJSON(t, category, map[string]any{"error": "OperationalError", "sqlstate": "55P03"})
							// Go preserves OF self when only related columns are projected.
							expected["Category"] = map[string]any{"locked": false}
							data, err := json.Marshal(expected)
							check(t, err)
							compareNativeJSON(t, data, actual)
						} else {
							compareNativeJSON(t, entry.Probes, actual)
						}
					})
				}
			})
			check(t, exec("SET lock_timeout = DEFAULT"))
		}
		t.Run("final_rows", func(t *testing.T) {
			rows, err := plain.OrderBy(owners.UpsertItemFields.Name.Asc()).All(ctx)
			check(t, err)
			names := make([]string, 0, len(rows))
			for _, row := range rows {
				names = append(names, row.Name)
			}
			compareNativeJSON(t, native.Final, names)
		})
	})
}
