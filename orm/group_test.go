package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type groupTestResult struct {
	Note  *string
	Total int64
}

func groupTestQuery(t *testing.T, backend db.Queryer) GroupedQuery[resultTestModel, groupTestResult] {
	t.Helper()
	fields := newResultTestFields()
	group, err := GroupBy(newResultTestQuerySet(backend), Project1(fields.Note, func(note *string) *string { return note }),
		Aggregate1(CountRows[resultTestModel](), func(n int64) int64 { return n }), func(note *string, n int64) groupTestResult { return groupTestResult{note, n} })
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func TestGroupedAllCacheOwnsCellsAndDerivedQueriesAreCold(t *testing.T) {
	backend := &resultTestBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
		if plan.ResultShape().Kind() == query.ResultModel {
			return &resultTestRows{values: [][]any{{int64(1), "title", "model", true}}}, nil
		}
		if plan.ResultShape().GroupMode() == query.GroupPage {
			return &resultTestRows{values: [][]any{{int64(1), int64(1), "page", int64(9)}}}, nil
		}
		return &resultTestRows{values: [][]any{{"canonical", int64(call + 1)}}}, nil
	}}
	group := groupTestQuery(t, backend)
	if _, err := group.source.All(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, err := group.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Total != 2 || first[0].Note == nil || *first[0].Note != "canonical" {
		t.Fatal(first)
	}
	*first[0].Note = "mutated"
	first[0].Total = 99
	copyOfGroup := group
	second, err := copyOfGroup.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if *second[0].Note != "canonical" || second[0].Note == first[0].Note || second[0].Total != 2 {
		t.Fatal("cache returned mutable caller DTO", second)
	}
	if count, err := group.Count(t.Context()); err != nil || count != 1 || len(backend.plans) != 2 {
		t.Fatal("warm group count", count, err, len(backend.plans))
	}
	page, err := group.Page(t.Context(), 1, 0)
	if err != nil || page.Total != 1 || len(page.Rows) != 1 || *page.Rows[0].Note != "page" {
		t.Fatal("fresh page", page, err)
	}
	third, err := group.All(t.Context())
	if err != nil || third[0].Total != 2 {
		t.Fatal("page changed whole group cache", third, err)
	}
	limited, err := group.Limit(1)
	if err != nil {
		t.Fatal(err)
	}
	offset, err := group.Offset(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, derived := range []GroupedQuery[resultTestModel, groupTestResult]{group.Fresh(), group.Having(), group.OrderBy(), limited, offset} {
		before := len(backend.plans)
		rows, err := derived.All(t.Context())
		if err != nil || len(rows) != 1 || len(backend.plans) != before+1 {
			t.Fatal("derived group reused cache", err)
		}
	}
	if group.source.Plan().ResultShape().Kind() != query.ResultModel || group.source.evaluation == nil {
		t.Fatal("grouping changed model source")
	}
}

func TestGroupedAllConcurrentCallersShareReadAndOwnNullableValues(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	backend := &cacheTestBackend{query: func(call int, ctx context.Context, _ query.Plan) (db.Rows, error) {
		if call != 0 {
			return nil, fmt.Errorf("duplicate query %d", call)
		}
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &resultTestRows{values: [][]any{{"canonical", int64(3)}}}, nil
	}}
	group := groupTestQuery(t, backend)
	type result struct {
		note *string
		err  error
	}
	results := make(chan result, 16)
	run := func(ctx context.Context) {
		values, err := group.All(ctx)
		if err != nil {
			results <- result{err: err}
			return
		}
		if len(values) != 1 || values[0].Note == nil || *values[0].Note != "canonical" {
			results <- result{err: fmt.Errorf("invalid rows: %#v", values)}
			return
		}
		*values[0].Note = "caller"
		results <- result{note: values[0].Note}
	}
	go run(t.Context())
	awaitSignal(t, started, "group read")
	for i := 1; i < 16; i++ {
		ctx, entered := newEnteredContext(t.Context())
		go run(ctx)
		awaitEntered(t, entered)
	}
	close(release)
	seen := map[*string]bool{}
	for i := 0; i < 16; i++ {
		value := awaitValue(t, results, "group result")
		if value.err != nil {
			t.Fatal(value.err)
		}
		if seen[value.note] {
			t.Fatal("nullable cache alias")
		}
		seen[value.note] = true
	}
	if backend.callCount() != 1 {
		t.Fatal("read not shared")
	}
}

func TestGroupedFailedReadsNeverPublishPartialCache(t *testing.T) {
	failure := errors.New("group read failed")
	for _, stage := range []string{"query", "nil rows", "scan", "iteration", "close", "cancel on iteration"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rows := &resultTestRows{values: [][]any{{"partial", int64(2)}}}
			switch stage {
			case "scan":
				rows.scanErr = failure
			case "iteration":
				rows.iterationErr = failure
			case "close":
				rows.closeErr = failure
			case "cancel on iteration":
				rows.onNext = func(int) { cancel() }
			}
			backend := &resultTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call > 0 {
					return &resultTestRows{values: [][]any{{"complete", int64(3)}}}, nil
				}
				if stage == "query" {
					return nil, failure
				}
				if stage == "nil rows" {
					return nil, nil
				}
				return rows, nil
			}}
			group := groupTestQuery(t, backend)
			values, err := group.All(ctx)
			if err == nil || values != nil {
				t.Fatal("failed read published", values, err)
			}
			if stage == "cancel on iteration" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if stage != "query" && stage != "nil rows" && rows.closeCalls != 1 {
				t.Fatal("close count", rows.closeCalls)
			}
			values, err = group.All(t.Context())
			if err != nil || len(values) != 1 || *values[0].Note != "complete" || len(backend.plans) != 2 {
				t.Fatal("failure cached", values, err)
			}
		})
	}
}

func TestGroupedCountChecksCardinalityLifecycleAndDoesNotFillAllCache(t *testing.T) {
	failure, cleanup := errors.New("count iteration failed"), errors.New("count close failed")
	for _, test := range []struct {
		name             string
		values           [][]any
		iteration, close error
		want             int64
		success          bool
	}{
		{"valid", [][]any{{int64(4)}}, nil, nil, 4, true}, {"zero", [][]any{{int64(0)}}, nil, nil, 0, true},
		{"missing", nil, nil, nil, 0, false}, {"NULL", [][]any{{nil}}, nil, nil, 0, false}, {"negative", [][]any{{int64(-1)}}, nil, nil, 0, false},
		{"extra", [][]any{{int64(1)}, {int64(2)}}, nil, nil, 0, false}, {"empty iteration failure", nil, failure, cleanup, 0, false},
		{"late failure", [][]any{{int64(4)}}, failure, cleanup, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := &resultTestRows{values: test.values, iterationErr: test.iteration, closeErr: test.close}
			backend := &resultTestBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
				if call == 0 {
					if plan.ResultShape().GroupMode() != query.GroupCount {
						t.Error("not group count")
					}
					return rows, nil
				}
				return &resultTestRows{values: [][]any{{"all", int64(4)}}}, nil
			}}
			group := groupTestQuery(t, backend)
			count, err := group.Count(t.Context())
			if test.success && (err != nil || count != test.want) || !test.success && (err == nil || count != 0) {
				t.Fatal(count, err)
			}
			if test.iteration != nil && (!errors.Is(err, failure) || !errors.Is(err, cleanup)) {
				t.Fatal("lost lifecycle error", err)
			}
			if rows.closeCalls != 1 {
				t.Fatal("close count", rows.closeCalls)
			}
			if _, err := group.All(t.Context()); err != nil || len(backend.plans) != 2 {
				t.Fatal("count filled all cache", err)
			}
		})
	}
}

func TestGroupedPageValidatesWholeStatementAndNeverPublishesPartialRows(t *testing.T) {
	for _, test := range []struct {
		name          string
		values        [][]any
		limit, offset int
		total         int64
		count         int
		valid         bool
	}{
		{"page", [][]any{{int64(3), int64(1), nil, int64(2)}, {int64(3), int64(1), "x", int64(1)}}, 2, 0, 3, 2, true},
		{"last page", [][]any{{int64(3), int64(1), "last", int64(2)}}, 2, 2, 3, 1, true},
		{"empty source", [][]any{{int64(0), nil, nil, nil}}, 2, 0, 0, 0, true},
		{"past end", [][]any{{int64(3), nil, nil, nil}}, 2, 5, 3, 0, true},
		{"zero limit keeps total", [][]any{{int64(3), nil, nil, nil}}, 0, 0, 3, 0, true},
		{"missing count", nil, 2, 0, 0, 0, false}, {"NULL count", [][]any{{nil, nil, nil, nil}}, 2, 0, 0, 0, false},
		{"negative count", [][]any{{int64(-1), nil, nil, nil}}, 2, 0, 0, 0, false},
		{"invalid presence", [][]any{{int64(1), int64(2), "x", int64(1)}}, 2, 0, 0, 0, false},
		{"inconsistent total", [][]any{{int64(2), int64(1), "x", int64(1)}, {int64(3), int64(1), "y", int64(1)}}, 2, 0, 0, 0, false},
		{"sentinel contains values", [][]any{{int64(0), nil, "x", nil}}, 2, 0, 0, 0, false},
		{"missing rows", [][]any{{int64(2), int64(1), "x", int64(1)}}, 2, 0, 0, 0, false},
		{"extra row", [][]any{{int64(1), int64(1), "x", int64(1)}, {int64(1), int64(1), "y", int64(1)}}, 1, 0, 0, 0, false},
		{"NULL required aggregate", [][]any{{int64(1), int64(1), "x", nil}}, 2, 0, 0, 0, false},
		{"mixed sentinel", [][]any{{int64(1), nil, nil, nil}, {int64(1), int64(1), "x", int64(1)}}, 2, 0, 0, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := &resultTestRows{values: test.values}
			backend := &resultTestBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
				if call > 0 {
					return &resultTestRows{values: [][]any{{"all", int64(3)}}}, nil
				}
				if plan.ResultShape().GroupMode() != query.GroupPage || len(plan.ResultShape().GroupOrderings()) != 1 || plan.ResultShape().GroupOrderings()[0].Nulls() != query.NullsLast {
					t.Error("page lost deterministic ordering")
				}
				return rows, nil
			}}
			group := groupTestQuery(t, backend)
			page, err := group.Page(t.Context(), test.limit, test.offset)
			if test.valid {
				if err != nil || page.Total != test.total || len(page.Rows) != test.count || page.Rows == nil {
					t.Fatal(page, err)
				}
			} else if err == nil || page.Total != 0 || page.Rows != nil {
				t.Fatal("invalid page published", page, err)
			}
			if rows.closeCalls != 1 {
				t.Fatal("close count", rows.closeCalls)
			}
			if _, err := group.All(t.Context()); err != nil || len(backend.plans) != 2 {
				t.Fatal("page filled all cache", err)
			}
		})
	}
}

type groupClosingRows struct {
	*resultTestRows
	close func()
}

func (rows *groupClosingRows) Close() error { rows.close(); return rows.resultTestRows.Close() }

func TestGroupedTerminalsRespectContextAndSessionOnEveryPublication(t *testing.T) {
	for _, terminal := range []string{"all", "count", "page"} {
		t.Run(terminal, func(t *testing.T) {
			run := func(group GroupedQuery[resultTestModel, groupTestResult], ctx context.Context) error {
				switch terminal {
				case "count":
					_, err := group.Count(ctx)
					return err
				case "page":
					_, err := group.Page(ctx, 1, 0)
					return err
				default:
					_, err := group.All(ctx)
					return err
				}
			}
			for _, stage := range []string{"before", "close", "builder", "warm"} {
				t.Run(stage, func(t *testing.T) {
					expired := errors.New("session expired")
					backend := &getSessionBackend{expired: expired}
					backend.active.Store(true)
					values := [][]any{{"ok", int64(1)}}
					if terminal == "count" {
						values = [][]any{{int64(1)}}
					}
					if terminal == "page" {
						values = [][]any{{int64(1), int64(1), "ok", int64(1)}}
					}
					rows := &resultTestRows{values: values}
					backend.rows = rows
					group := groupTestQuery(t, backend)
					switch stage {
					case "before":
						backend.active.Store(false)
					case "close":
						backend.rows = &groupClosingRows{rows, func() { backend.active.Store(false) }}
					case "builder":
						if terminal == "count" {
							return
						}
						decoder := group.newDecoder
						group.newDecoder = func() resultDecoder[groupTestResult] {
							value := decoder()
							build := value.decode
							value.decode = func() groupTestResult { backend.active.Store(false); return build() }
							return value
						}
					case "warm":
						if err := run(group, t.Context()); err != nil {
							t.Fatal(err)
						}
						backend.active.Store(false)
					}
					if err := run(group, t.Context()); !errors.Is(err, expired) {
						t.Fatal("escaped session", err)
					}
				})
			}
			backend := resultBackendForRows(nil)
			group := groupTestQuery(t, backend)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := run(group, ctx); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			var typedNil *resultTestTypedNilContext
			if run(group, nil) == nil || run(group, typedNil) == nil || len(backend.plans) != 0 {
				t.Fatal("invalid context performed I/O")
			}
		})
	}
}

func TestGroupedTypedAndDynamicInputsResolveToSameOwnedPlan(t *testing.T) {
	backend := resultBackendForRows([][]any{{"key", int64(3), int64(2)}})
	fields := newResultTestFields()
	source := newResultTestQuerySet(backend)
	open := Count(fields.ID).Where(fields.Published.Exact(true))
	typed, err := GroupBy(source, Project1(fields.Note, func(v *string) *string { return v }), Aggregate2(CountRows[resultTestModel](), open, func(a, b int64) [2]int64 { return [2]int64{a, b} }), func(k *string, a [2]int64) any { return a })
	if err != nil {
		t.Fatal(err)
	}
	typed = typed.Having(open.GreaterThanOrEqual(1)).OrderBy(open.Desc(), fields.Note.Asc().NullsLast())
	keys := []string{"note"}
	filters := []LookupInput{{Key: "published", Value: true}}
	aggregates := []DynamicAggregateInput{{Kind: query.ResultCountAll}, {Kind: query.ResultCount, Field: "id", Filter: filters}}
	dynamic, err := GroupValues(source, keys, aggregates)
	if err != nil {
		t.Fatal(err)
	}
	dynamic = dynamic.HavingDynamic(GroupLookupInput{Column: 2, Lookup: query.LookupGreaterThanOrEqual, Value: int64(1)}).OrderByDynamic(GroupOrderInput{Column: 2, Direction: query.Descending}, GroupOrderInput{Column: 0, Direction: query.Ascending, Nulls: query.NullsLast})
	keys[0] = "id"
	filters[0].Value = false
	aggregates[1].Field = "title"
	if !typed.Plan().Equal(dynamic.Plan()) {
		t.Fatal("typed and dynamic plans differ")
	}
	rows, err := dynamic.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !reflect.DeepEqual(rows[0].Keys, []query.Value{query.String("key")}) || !reflect.DeepEqual(rows[0].Aggregates, []query.Value{query.Integer(3), query.Integer(2)}) {
		t.Fatal(rows)
	}
	rows[0].Keys[0] = query.String("mutated")
	rows[0].Aggregates[0] = query.Integer(0)
	rows, err = dynamic.All(t.Context())
	if err != nil || rows[0].Keys[0] != query.String("key") || rows[0].Aggregates[0] != query.Integer(3) {
		t.Fatal("dynamic output aliases", rows, err)
	}
	before := len(backend.plans)
	for _, bad := range []GroupedQuery[resultTestModel, GroupRow]{dynamic.HavingDynamic(GroupLookupInput{Column: 3, Lookup: query.LookupExact, Value: 1}), dynamic.HavingDynamic(GroupLookupInput{Column: 2, Lookup: query.LookupExact, Value: "1"}), dynamic.OrderByDynamic(GroupOrderInput{Column: -1, Direction: query.Ascending})} {
		if _, err := bad.All(t.Context()); err == nil {
			t.Fatal("invalid dynamic reference")
		}
	}
	if len(backend.plans) != before {
		t.Fatal("invalid group performed I/O")
	}
}

func TestGroupedCardinalityCannotMaskSessionExpiryOrLateContext(t *testing.T) {
	expired := errors.New("read session expired while closing")
	for _, terminal := range []string{"count", "page"} {
		t.Run(terminal, func(t *testing.T) {
			backend := &getSessionBackend{expired: expired}
			backend.active.Store(true)
			backend.rows = &groupClosingRows{&resultTestRows{}, func() { backend.active.Store(false) }}
			group := groupTestQuery(t, backend)
			var err error
			if terminal == "count" {
				_, err = group.Count(t.Context())
			} else {
				_, err = group.Page(t.Context(), 1, 0)
			}
			if !errors.Is(err, expired) {
				t.Fatal("cardinality hid expired read scope", err)
			}
		})
	}
	for _, terminal := range []string{"all", "page"} {
		t.Run("cancel during builder "+terminal, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			values := [][]any{{"present", int64(1)}}
			if terminal == "page" {
				values = [][]any{{int64(1), int64(1), "present", int64(1)}}
			}
			group := groupTestQuery(t, resultBackendForRows(values))
			decoder := group.newDecoder
			group.newDecoder = func() resultDecoder[groupTestResult] {
				value := decoder()
				build := value.decode
				value.decode = func() groupTestResult { cancel(); return build() }
				return value
			}
			if terminal == "all" {
				rows, err := group.All(ctx)
				if !errors.Is(err, context.Canceled) || rows != nil {
					t.Fatal("canceled builder published", rows, err)
				}
			} else {
				page, err := group.Page(ctx, 1, 0)
				if !errors.Is(err, context.Canceled) || page.Total != 0 || page.Rows != nil {
					t.Fatal("canceled page builder published", page, err)
				}
			}
		})
	}
}
