package consumer

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

// json.Number retains Django/SQLite's promoted REAL output in the cases where
// Go deliberately rejects loss of the model's exact integer domain.
type queryUpdateReferenceObject struct {
	ID      int64
	Name    string
	Amount  json.Number
	Other   int64
	Rank    *int64
	Note    *string
	Enabled bool
	Score   float64
	Price   string
	GroupID *int64 `json:"group_id"`
}

type queryUpdateReferenceResult struct {
	Count    *int64
	Error    string
	SQLState *string `json:"sqlstate"`
}

type queryUpdateReferenceCase struct {
	Case      string
	Value     json.RawMessage
	Rows      []queryUpdateReferenceObject
	ReadError *queryUpdateReferenceResult `json:"read_error"`
	Callbacks []string
	Commands  []string `json:"transaction_commands"`
	Updates   int      `json:"update_statements"`
	Selects   int      `json:"select_statements"`
}

func queryUpdateReferenceObjects(values []owners.QueryUpdateItem) []queryUpdateReferenceObject {
	result := make([]queryUpdateReferenceObject, len(values))
	for index, value := range values {
		result[index] = queryUpdateReferenceObject{
			ID: value.ID, Name: value.Name, Amount: json.Number(strconv.FormatInt(value.Amount, 10)), Other: value.Other,
			Rank: value.Rank, Note: value.Note, Enabled: value.Enabled, Score: value.Score, Price: value.Price.String(), GroupID: value.GroupID,
		}
	}
	return result
}

func queryUpdateReferenceEqualRows(t *testing.T, got, want []queryUpdateReferenceObject) {
	t.Helper()
	canonical := func(values []queryUpdateReferenceObject) []queryUpdateReferenceObject {
		values = slices.Clone(values)
		for index := range values {
			price, err := decimal.Parse(values[index].Price)
			check(t, err)
			values[index].Price = price.String()
		}
		return values
	}
	if !reflect.DeepEqual(canonical(got), canonical(want)) {
		actual, _ := json.Marshal(got)
		expected, _ := json.Marshal(want)
		t.Fatalf("stored/model values differ: Go=%s expected=%s", actual, expected)
	}
}

func queryUpdateReferenceError(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	if errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
		return "CommitOutcomeUnknown"
	}
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) {
		if strings.HasPrefix(postgres.Code, "23") {
			return "IntegrityError"
		}
		if strings.HasPrefix(postgres.Code, "22") {
			return "DataError"
		}
	}
	var sqlite interface{ Code() int }
	if errors.As(err, &sqlite) && sqlite.Code()&255 == 19 {
		return "IntegrityError"
	}
	switch {
	case errors.Is(err, &query.Error{Code: query.CodeUnknownField}):
		return "FieldDoesNotExist"
	case errors.Is(err, &query.Error{Code: query.CodeUnsupported}):
		return "TypeError"
	case errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}), errors.Is(err, &query.Error{Code: query.CodeInvalidValue}):
		return "ValueError"
	}
	t.Fatalf("unclassified query update error: %v", err)
	return ""
}

func TestQueryUpdateReference(t *testing.T) {
	withCollectionBackends(t, func(t *testing.T, backend collectionBackend, _ func() (collectionBackend, error), exec func(string) error, postgres bool) {
		t.Cleanup(func() { check(t, backend.Close()) })
		vendor := "sqlite"
		if postgres {
			vendor = "postgres"
		}
		data, err := os.ReadFile("query_update-django61-" + vendor + ".json")
		check(t, err)
		var reference struct{ Cases []queryUpdateReferenceCase }
		check(t, json.Unmarshal(data, &reference))
		if len(reference.Cases) != 81 {
			t.Fatal("incomplete native query update reference")
		}
		for _, native := range reference.Cases {
			// The owning external test proves these unavailable Go expressions
			// fail compilation and binds them to the independent native cases.
			if native.Case == "distinct_fields" || native.Case == "union" || native.Case == "projection_read_shape" {
				continue
			}
			t.Run(native.Case, func(t *testing.T) {
				seedQueryUpdate(t, backend, exec, postgres)
				ctx := t.Context()
				fields := owners.QueryUpdateItemFields
				probe := &queryUpdateProbe{collectionBackend: backend}
				api, err := project.Using(probe)
				check(t, err)
				relations, err := project.BindRelations()
				check(t, err)
				source := api.OwnersQueryUpdateItem
				assignments := []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.Assign(fields.Amount, int64(11))}
				var dynamic []orm.DynamicUpdateInput
				useDynamic := false
				setDynamic := func(field string, value any) {
					useDynamic = true
					dynamic = []orm.DynamicUpdateInput{{Field: field, Value: value}}
				}
				amount, other, rank, score := orm.F(fields.Amount), orm.F(fields.Other), orm.F(fields.Rank), orm.F(fields.Score)
				switch native.Case {
				case "empty_assignments", "cached_empty_assignments":
					assignments = nil
				case "unknown_field", "empty_query_unknown_field":
					setDynamic("missing", int64(1))
				case "many_to_many_field":
					setDynamic("peers", int64(1))
				case "related_assignment":
					setDynamic("group__name", "new")
				case "pk_alias_assignment":
					setDynamic("pk", int64(41))
				case "primary_key_constant", "primary_key_referenced":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.Assign(fields.ID, int64(41))}
				case "primary_key_expression":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.ID, orm.Add(orm.F(fields.ID), 10))}
				case "primary_key_collision":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.Assign(fields.ID, int64(1))}
				case "constant":
					assignments = append(assignments, orm.AssignNull(fields.Note))
				case "unchanged_count":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, amount)}
				case "no_matches":
					source = source.Filter(fields.ID.Exact(999))
				case "empty_query":
					source = source.Filter(fields.ID.In())
				case "empty_query_invalid_value":
					setDynamic("amount", "not-integer")
				case "query_filter":
					source = source.Filter(fields.Amount.GreaterThan(0))
				case "forward_filter":
					source = source.Filter(relations.OwnersQueryUpdateItem.Group.Name.Exact("first"))
				case "many_to_many_filter":
					source = source.Filter(relations.OwnersQueryUpdateItem.Peers.Name.In("two", "three"))
				case "boolean_filter":
					source = source.Filter(orm.Or(fields.Name.Exact("one"), relations.OwnersQueryUpdateItem.Group.Name.Exact("second")))
				case "ordered_distinct":
					source = source.OrderBy(fields.Name.Desc()).Distinct()
				case "sliced", "sliced_empty_assignments":
					source, err = source.OrderBy(fields.ID.Asc()).Limit(1)
					check(t, err)
					if native.Case == "sliced_empty_assignments" {
						assignments = nil
					}
				case "row_lock_read_shape":
					source = source.SelectForUpdate(orm.RowLockOptions{NoWait: true})
				case "eager_prefetch_read_shape":
				case "original_row_swap":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, other), orm.AssignExpression(fields.Other, amount)}
				case "original_row_order", "original_row_reverse_order":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Add(amount, 1)), orm.AssignExpression(fields.Other, orm.Add(amount, 5))}
					if native.Case == "original_row_reverse_order" {
						slices.Reverse(assignments)
					}
				case "add":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Add(amount, 2))}
				case "subtract":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Subtract(amount, 2))}
				case "multiply":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Multiply(amount, 3))}
				case "divide":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Divide(amount, 2))}
				case "remainder":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Remainder(amount, 2))}
				case "negate":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Negate(amount))}
				case "nested":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Subtract(orm.Multiply(orm.AddExpressions(amount, other), 2), 1))}
				case "nullable_add":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Rank, orm.Add(rank, 1))}
				case "nullable_copy":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Rank, amount)}
				case "null_to_nonnullable":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, rank)}
				case "literal_null":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignNull(fields.Rank)}
				case "null_expression":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Rank, orm.AddExpressions(rank, orm.NullValue[owners.QueryUpdateItem, int64]()))}
				case "field_string":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Note, orm.F(fields.Name))}
				case "field_boolean":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Enabled, orm.F(fields.Enabled))}
				case "field_fk":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.GroupID, orm.F(fields.GroupID))}
				case "set_fk_id":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.Assign(fields.GroupID, int64(1))}
				case "set_fk_null":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignNull(fields.GroupID)}
				case "missing_fk":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.Assign(fields.GroupID, int64(999))}
				case "joined_reference":
					setDynamic("note", orm.DynamicF("group__name"))
				case "missing_reference":
					setDynamic("amount", orm.DynamicF("missing"))
				case "aggregate_reference":
					setDynamic("amount", orm.Max(fields.Amount))
				case "failure_unique", "cached_failure":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.Assign(fields.Name, "same")}
				case "failure_check":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Add(amount, 1)), orm.AssignExpression(fields.Other, orm.Subtract(amount, 4))}
				case "failure_not_null":
					setDynamic("name", nil)
				case "cached_success", "parent_commit", "parent_rollback":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Add(amount, 1))}
				case "borrowed_failure", "savepoint_failure":
				case "overflow_add", "max_exact_add":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Add(amount, 1))}
				case "overflow_subtract", "min_exact_subtract":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Subtract(amount, 1))}
				case "overflow_multiply":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Multiply(amount, 2))}
				case "overflow_negate":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Negate(amount))}
				case "overflow_divide":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Divide(amount, -1))}
				case "minimum_remainder":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Remainder(amount, -1))}
				case "divide_by_zero":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Amount, orm.Divide(amount, 0))}
				case "nullable_divide_by_zero":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Rank, orm.Divide(rank, 0))}
				case "remainder_by_zero":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Rank, orm.Remainder(rank, 0))}
				case "float_add":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Score, orm.Add(score, 0.5))}
				case "float_divide":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Score, orm.Divide(score, 2.0))}
				case "float_divide_by_zero":
					assignments = []orm.UpdateAssignment[owners.QueryUpdateItem]{orm.AssignExpression(fields.Score, orm.Divide(score, 0.0))}
				case "float_to_integer":
					setDynamic("amount", orm.DynamicF("score"))
				case "integer_mixed_float":
					setDynamic("amount", orm.DynamicF("amount").Add(0.5))
				case "decimal_add", "decimal_multiply", "decimal_divide_by_zero", "decimal_overflow":
					value, err := decimal.Parse(map[string]string{"decimal_add": "0.25", "decimal_multiply": "1.5", "decimal_divide_by_zero": "0", "decimal_overflow": "10000000"}[native.Case])
					check(t, err)
					expression := orm.DynamicF("price")
					switch native.Case {
					case "decimal_add":
						expression = expression.Add(value)
					case "decimal_divide_by_zero":
						expression = expression.Divide(value)
					default:
						expression = expression.Multiply(value)
					}
					setDynamic("price", expression)
				default:
					t.Fatal("native case has no Go observation", native.Case)
				}
				if strings.HasPrefix(native.Case, "empty_query_") {
					source = source.Filter(fields.ID.In())
				}
				if native.Case == "pk_alias_assignment" || native.Case == "primary_key_constant" || native.Case == "primary_key_expression" || native.Case == "primary_key_collision" {
					check(t, exec("DELETE FROM gdj_query_update_peer"))
				}
				if native.Case == "pk_alias_assignment" || native.Case == "primary_key_constant" || native.Case == "primary_key_referenced" {
					source = source.Filter(fields.ID.Exact(1))
				}
				if initial := map[string]string{
					"overflow_add": "9223372036854775807", "overflow_subtract": "-9223372036854775808",
					"overflow_multiply": "9223372036854775807", "overflow_negate": "-9223372036854775808",
					"overflow_divide": "-9223372036854775808", "minimum_remainder": "-9223372036854775808",
					"max_exact_add": "9223372036854775806", "min_exact_subtract": "-9223372036854775807",
				}[native.Case]; initial != "" {
					check(t, exec("UPDATE gdj_query_update_item SET amount = "+initial+" WHERE id=1"))
					source = source.Filter(fields.ID.Exact(1))
				}
				read := func() []queryUpdateReferenceObject {
					values, err := owners.QueryUpdateItemObjects.Using(backend).OrderBy(fields.ID.Asc()).All(ctx)
					check(t, err)
					return queryUpdateReferenceObjects(values)
				}
				before := read()
				var count int64
				var callErr error
				var want queryUpdateReferenceResult
				check(t, json.Unmarshal(native.Value, &want))
				wantRows, wantUpdates, wantReads := native.Rows, native.Updates, native.Selects
				wantTransactions, wantSavepoints := 0, 0
				if wantUpdates > 0 {
					wantTransactions = 1
				}
				performed := false
				switch native.Case {
				case "eager_prefetch_read_shape":
					count, callErr = source.SelectRelated(source.Related.Group).PrefetchRelated(source.Prefetch.Peers).Update(ctx, assignments...)
					performed = true
				case "cached_success", "cached_failure", "cached_empty_assignments":
					var cached struct {
						Before, After, Held, Sibling []queryUpdateReferenceObject
						SiblingBefore                []queryUpdateReferenceObject `json:"sibling_before"`
						Updated                      queryUpdateReferenceResult
						CacheCleared                 bool `json:"cache_cleared"`
					}
					check(t, json.Unmarshal(native.Value, &cached))
					cachedSource := owners.QueryUpdateItemObjects.Using(probe).OrderBy(fields.ID.Asc())
					sibling := cachedSource.Filter()
					held, err := cachedSource.All(ctx)
					check(t, err)
					warmSibling, err := sibling.All(ctx)
					check(t, err)
					queryUpdateReferenceEqualRows(t, queryUpdateReferenceObjects(held), cached.Before)
					queryUpdateReferenceEqualRows(t, queryUpdateReferenceObjects(warmSibling), cached.SiblingBefore)
					reads := probe.reads
					count, callErr = cachedSource.Update(ctx, assignments...)
					after, err := cachedSource.All(ctx)
					check(t, err)
					if (probe.reads > reads) != cached.CacheCleared {
						t.Fatal("cache invalidation differs", probe.reads, reads, cached.CacheCleared)
					}
					again, err := sibling.All(ctx)
					check(t, err)
					queryUpdateReferenceEqualRows(t, queryUpdateReferenceObjects(after), cached.After)
					queryUpdateReferenceEqualRows(t, queryUpdateReferenceObjects(held), cached.Held)
					queryUpdateReferenceEqualRows(t, queryUpdateReferenceObjects(again), cached.Sibling)
					want, performed = cached.Updated, true
				case "parent_commit", "parent_rollback":
					rollback := errors.New("authored parent rollback")
					outer := probe.Atomic(ctx, func(session db.Session) error {
						count, callErr = owners.QueryUpdateItemObjects.Using(session).Update(ctx, assignments...)
						if callErr != nil {
							return callErr
						}
						if native.Case == "parent_rollback" {
							return rollback
						}
						return nil
					})
					if native.Case == "parent_rollback" {
						if !errors.Is(outer, rollback) {
							t.Fatal(outer)
						}
					} else {
						check(t, outer)
					}
					wantSavepoints, performed = 1, true
				case "borrowed_failure", "savepoint_failure":
					var parent struct {
						Before, Child, After queryUpdateReferenceResult
						ParentCount          *int64 `json:"parent_count"`
						ParentError          string `json:"parent_error"`
					}
					check(t, json.Unmarshal(native.Value, &parent))
					check(t, probe.Atomic(ctx, func(session db.Session) error {
						q := owners.QueryUpdateItemObjects.Using(session)
						first, err := q.Filter(fields.ID.Exact(1)).Update(ctx, orm.Assign(fields.Amount, int64(99)))
						if err != nil {
							return err
						}
						if parent.Before.Count == nil || first != *parent.Before.Count {
							t.Fatal("parent initial update differs", first)
						}
						count, callErr = q.Update(ctx, orm.Assign(fields.Name, "duplicate"))
						if callErr == nil || count != 0 {
							t.Fatal("child failure accepted", count, callErr)
						}
						total, err := q.Count(ctx)
						if err != nil {
							return err
						}
						if total != 3 {
							t.Fatal("failed child changed parent membership", total)
						}
						last, err := q.Filter(fields.ID.Exact(2)).Update(ctx, orm.Assign(fields.Amount, int64(101)))
						if last != 1 {
							t.Fatal(last)
						}
						return err
					}))
					want, performed = parent.Child, true
					wantUpdates, wantReads, wantSavepoints = 3, 1, 3
					if native.Case == "borrowed_failure" {
						if parent.ParentError != "TransactionManagementError" || parent.ParentCount != nil || parent.After.Count != nil {
							t.Fatal("native borrowed poisoning changed")
						}
						wantRows = slices.Clone(before)
						wantRows[0].Amount, wantRows[1].Amount = "99", "101"
						t.Log("explicit Go deviation: failed borrowed UPDATE owns a savepoint; parent remains usable")
					} else if parent.ParentError != "" || parent.ParentCount == nil || *parent.ParentCount != 3 || parent.After.Count == nil || *parent.After.Count != 1 {
						t.Fatal("native savepoint parent outcome changed")
					}
				}
				if !performed {
					if useDynamic {
						count, callErr = source.UpdateDynamic(ctx, dynamic...)
					} else {
						count, callErr = source.Update(ctx, assignments...)
					}
				}
				wantError, wantCount := want.Error, want.Count
				compareNativeCause := true
				switch native.Case {
				case "many_to_many_field", "joined_reference", "missing_reference":
					if want.Error != "FieldError" || native.Updates != 0 {
						t.Fatal("native field rejection changed")
					}
					wantError = "FieldDoesNotExist"
					if !errors.Is(callErr, &query.Error{Code: query.CodeUnknownField}) {
						t.Fatal("Go did not reject unknown concrete metadata", callErr)
					}
				case "aggregate_reference":
					if want.Error != "FieldError" || native.Updates != 0 {
						t.Fatal("native aggregate rejection changed")
					}
					wantError = "ValueError"
					if !errors.Is(callErr, &query.Error{Code: query.CodeInvalidValue}) {
						t.Fatal("aggregate escaped scalar boundary", callErr)
					}
				case "failure_not_null":
					if want.Error != "IntegrityError" || native.Updates != 1 {
						t.Fatal("native null rejection changed")
					}
					wantError, wantUpdates, wantTransactions, compareNativeCause = "ValueError", 0, 0, false
					if !errors.Is(callErr, &query.Error{Code: query.CodeInvalidPlan}) {
						t.Fatal("literal NULL reached a non-null target", callErr)
					}
					t.Log("explicit Go deviation: NULL literal rejected in preflight")
				case "missing_fk":
					if want.Error != "IntegrityError" || native.Updates != 1 {
						t.Fatal("native deferred FK failure changed")
					}
					wantError = "CommitOutcomeUnknown"
					t.Log("explicit Go deviation: failed literal COMMIT preserves unknown outcome and native cause")
				case "float_to_integer", "integer_mixed_float":
					if want.Count == nil || *want.Count != 3 || native.Updates != 1 {
						t.Fatal("native numeric coercion changed")
					}
					wantError, wantCount, wantRows, wantUpdates, wantTransactions = "ValueError", nil, before, 0, 0
					if !errors.Is(callErr, &query.Error{Code: query.CodeInvalidPlan}) {
						t.Fatal("implicit numeric coercion accepted", callErr)
					}
					t.Log("explicit Go deviation: numeric kinds require explicit conversion")
				case "decimal_add", "decimal_multiply", "decimal_divide_by_zero", "decimal_overflow":
					if native.Updates != 1 {
						t.Fatal("native decimal observation lost")
					}
					wantError, wantCount, wantRows, wantUpdates, wantTransactions, compareNativeCause = "TypeError", nil, before, 0, 0, false
					if !errors.Is(callErr, &query.Error{Code: query.CodeUnsupported}) {
						t.Fatal("Decimal arithmetic escaped its precision boundary", callErr)
					}
					t.Log("explicit unsupported Go surface: Decimal arithmetic awaits its exact precision contract")
				case "overflow_add", "overflow_subtract", "overflow_multiply", "overflow_negate", "overflow_divide":
					if !postgres {
						if want.Count == nil || *want.Count != 1 || native.Updates != 1 {
							t.Fatal("native SQLite promotion changed")
						}
						wantError, wantCount, wantRows = "ValueError", nil, before
						if !errors.Is(callErr, &query.Error{Code: query.CodeInvalidValue}) {
							t.Fatal("SQLite overflow lost exact model domain", callErr)
						}
						t.Log("explicit Go deviation: reject each promoted integer intermediate atomically")
					}
				}
				if got := queryUpdateReferenceError(t, callErr); got != wantError {
					t.Fatalf("result error differs: Go=%s Django=%s expected Go=%s cause=%v", got, want.Error, wantError, callErr)
				}
				if callErr != nil {
					if count != 0 {
						t.Fatal("error exposed partial count", count)
					}
				} else if wantCount == nil || count != *wantCount {
					t.Fatal("matched count differs", count, wantCount)
				}
				if postgres && compareNativeCause && want.SQLState != nil {
					var cause *pgconn.PgError
					if !errors.As(callErr, &cause) || cause.Code != *want.SQLState {
						t.Fatal("native PostgreSQL SQLSTATE lost", callErr)
					}
				}
				queryUpdateReferenceEqualRows(t, read(), wantRows)
				if len(probe.plans) != wantUpdates || probe.reads != wantReads || probe.transactions != wantTransactions || probe.savepoints != wantSavepoints {
					t.Fatalf("query/transaction envelope differs: updates=%d/%d reads=%d/%d transactions=%d/%d savepoints=%d/%d", len(probe.plans), wantUpdates, probe.reads, wantReads, probe.transactions, wantTransactions, probe.savepoints, wantSavepoints)
				}
				if len(native.Callbacks) != 0 {
					t.Fatal("native update unexpectedly ran model hooks", native.Callbacks)
				}
			})
		}
	})
}
