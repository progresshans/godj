package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestProjectOneThroughFourDecodeTypedRows(t *testing.T) {
	fields := newResultTestFields()

	t.Run("Project1", func(t *testing.T) {
		backend := resultBackendForRows([][]any{{int64(7)}, {int64(9)}})
		projection := Project1(fields.ID, func(id int64) int64 { return id })

		values, err := SelectInto(context.Background(), newResultTestQuerySet(backend), projection)
		if err != nil {
			t.Fatalf("SelectInto() error = %v", err)
		}
		if len(values) != 2 || values[0] != 7 || values[1] != 9 {
			t.Fatalf("SelectInto() = %#v, want [7 9]", values)
		}
		assertResultProjectionPlan(t, backend.plans[0], fields.ID.reference)
	})

	t.Run("Project2", func(t *testing.T) {
		backend := resultBackendForRows([][]any{{int64(3), "three"}})
		projection := Project2(fields.ID, fields.Title, func(id int64, title string) resultProjection2 {
			return resultProjection2{ID: id, Title: title}
		})

		values, err := SelectInto(context.Background(), newResultTestQuerySet(backend), projection)
		if err != nil {
			t.Fatalf("SelectInto() error = %v", err)
		}
		if len(values) != 1 || values[0] != (resultProjection2{ID: 3, Title: "three"}) {
			t.Fatalf("SelectInto() = %#v", values)
		}
		assertResultProjectionPlan(t, backend.plans[0], fields.ID.reference, fields.Title.reference)
	})

	t.Run("Project3 nullable string", func(t *testing.T) {
		backend := resultBackendForRows([][]any{
			{int64(1), "without note", nil},
			{int64(2), "first note", "first"},
			{int64(3), "second note", "second"},
		})
		projection := Project3(fields.ID, fields.Title, fields.Note, func(id int64, title string, note *string) resultProjection3 {
			return resultProjection3{ID: id, Title: title, Note: note}
		})

		values, err := SelectInto(context.Background(), newResultTestQuerySet(backend), projection)
		if err != nil {
			t.Fatalf("SelectInto() error = %v", err)
		}
		if len(values) != 3 || values[0].Note != nil || values[1].Note == nil || *values[1].Note != "first" ||
			values[2].Note == nil || *values[2].Note != "second" {
			t.Fatalf("SelectInto() nullable rows = %#v", values)
		}
		if values[1].Note == values[2].Note {
			t.Fatal("Project3 reused nullable destination storage across rows")
		}
		assertResultProjectionPlan(t, backend.plans[0], fields.ID.reference, fields.Title.reference, fields.Note.reference)
	})

	t.Run("Project4", func(t *testing.T) {
		backend := resultBackendForRows([][]any{{int64(11), "published", "memo", true}})
		projection := Project4(fields.ID, fields.Title, fields.Note, fields.Published,
			func(id int64, title string, note *string, published bool) resultProjection4 {
				return resultProjection4{ID: id, Title: title, Note: note, Published: published}
			})

		values, err := SelectInto(context.Background(), newResultTestQuerySet(backend), projection)
		if err != nil {
			t.Fatalf("SelectInto() error = %v", err)
		}
		if len(values) != 1 || values[0].ID != 11 || values[0].Title != "published" ||
			values[0].Note == nil || *values[0].Note != "memo" || !values[0].Published {
			t.Fatalf("SelectInto() = %#v", values)
		}
		assertResultProjectionPlan(t, backend.plans[0], fields.ID.reference, fields.Title.reference,
			fields.Note.reference, fields.Published.reference)
	})
}

func TestAggregateOneThroughFourDecodeCountAndNullableMax(t *testing.T) {
	fields := newResultTestFields()

	t.Run("Aggregate1 count", func(t *testing.T) {
		backend := resultBackendForRows([][]any{{int64(5)}})
		aggregate := Aggregate1(CountRows[resultTestModel](), func(count int64) int64 { return count })

		value, err := AggregateInto(context.Background(), newResultTestQuerySet(backend), aggregate)
		if err != nil {
			t.Fatalf("AggregateInto() error = %v", err)
		}
		if value != 5 {
			t.Fatalf("AggregateInto() = %d, want 5", value)
		}
		assertResultAggregatePlan(t, backend.plans[0], query.CountAllResult())
	})

	t.Run("Aggregate2 nonempty integer max", func(t *testing.T) {
		backend := resultBackendForRows([][]any{{int64(3), int64(17)}})
		aggregate := Aggregate2(CountRows[resultTestModel](), Max(fields.ID),
			func(count int64, maximum Optional[int64]) resultAggregate2 {
				return resultAggregate2{Count: count, MaxID: maximum}
			})

		value, err := AggregateInto(context.Background(), newResultTestQuerySet(backend), aggregate)
		if err != nil {
			t.Fatalf("AggregateInto() error = %v", err)
		}
		maximum, valid := value.MaxID.Get()
		if value.Count != 3 || !valid || maximum != 17 || !value.MaxID.Valid() {
			t.Fatalf("AggregateInto() = %#v, max = (%d, %v)", value, maximum, valid)
		}
		assertResultAggregatePlan(t, backend.plans[0], query.CountAllResult(), query.MaxResult(fields.ID.reference))
	})

	t.Run("Aggregate3 empty max values", func(t *testing.T) {
		backend := resultBackendForRows([][]any{{int64(0), nil, nil}})
		aggregate := Aggregate3(CountRows[resultTestModel](), Max(fields.ID), Max(fields.Title),
			func(count int64, maximumID Optional[int64], maximumTitle Optional[string]) resultAggregate3 {
				return resultAggregate3{Count: count, MaxID: maximumID, MaxTitle: maximumTitle}
			})

		value, err := AggregateInto(context.Background(), newResultTestQuerySet(backend), aggregate)
		if err != nil {
			t.Fatalf("AggregateInto() error = %v", err)
		}
		_, idValid := value.MaxID.Get()
		_, titleValid := value.MaxTitle.Get()
		if value.Count != 0 || idValid || titleValid || value.MaxID.Valid() || value.MaxTitle.Valid() {
			t.Fatalf("empty AggregateInto() = %#v", value)
		}
		assertResultAggregatePlan(t, backend.plans[0], query.CountAllResult(), query.MaxResult(fields.ID.reference),
			query.MaxResult(fields.Title.reference))
	})

	t.Run("Aggregate4 string and nullable string max", func(t *testing.T) {
		backend := resultBackendForRows([][]any{{int64(4), int64(23), "Zulu", "last memo"}})
		aggregate := Aggregate4(CountRows[resultTestModel](), Max(fields.ID), Max(fields.Title), Max(fields.Note),
			func(count int64, maximumID Optional[int64], maximumTitle, maximumNote Optional[string]) resultAggregate4 {
				return resultAggregate4{Count: count, MaxID: maximumID, MaxTitle: maximumTitle, MaxNote: maximumNote}
			})

		value, err := AggregateInto(context.Background(), newResultTestQuerySet(backend), aggregate)
		if err != nil {
			t.Fatalf("AggregateInto() error = %v", err)
		}
		maximumID, idValid := value.MaxID.Get()
		maximumTitle, titleValid := value.MaxTitle.Get()
		maximumNote, noteValid := value.MaxNote.Get()
		if value.Count != 4 || !idValid || maximumID != 23 || !titleValid || maximumTitle != "Zulu" ||
			!noteValid || maximumNote != "last memo" {
			t.Fatalf("AggregateInto() = %#v", value)
		}
		assertResultAggregatePlan(t, backend.plans[0], query.CountAllResult(), query.MaxResult(fields.ID.reference),
			query.MaxResult(fields.Title.reference), query.MaxResult(fields.Note.reference))
	})
}

func TestResultTerminalsDerivePlanWithoutReadingOrPopulatingSourceCache(t *testing.T) {
	fields := newResultTestFields()
	projectionCalls := 0
	backend := &resultTestBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
		switch plan.ResultShape().Kind() {
		case query.ResultProjection:
			projectionCalls++
			return &resultTestRows{values: [][]any{{fmt.Sprintf("projection-%d", projectionCalls)}}}, nil
		case query.ResultAggregate:
			return &resultTestRows{values: [][]any{{int64(1), int64(41)}}}, nil
		case query.ResultModel:
			return &resultTestRows{values: [][]any{{int64(41), "cached model", nil, true}}}, nil
		default:
			return nil, fmt.Errorf("unexpected result kind %q", plan.ResultShape().Kind())
		}
	}}

	source := newResultTestQuerySet(backend).
		Filter(fields.Published.Exact(true)).
		OrderBy(fields.ID.Asc()).
		Distinct()
	var err error
	source, err = source.Offset(2)
	if err != nil {
		t.Fatalf("Offset() error = %v", err)
	}
	source, err = source.Limit(5)
	if err != nil {
		t.Fatalf("Limit() error = %v", err)
	}
	sourcePlan := source.Plan()
	sourceEvaluation := source.evaluation
	projection := Project1(fields.Title, func(title string) string { return title })

	coldProjection, err := SelectInto(context.Background(), source, projection)
	if err != nil || len(coldProjection) != 1 || coldProjection[0] != "projection-1" {
		t.Fatalf("cold SelectInto() = (%#v, %v)", coldProjection, err)
	}
	models, err := source.All(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != 41 {
		t.Fatalf("All() after projection = (%#v, %v)", models, err)
	}
	warmProjection, err := SelectInto(context.Background(), source, projection)
	if err != nil || len(warmProjection) != 1 || warmProjection[0] != "projection-2" {
		t.Fatalf("warm SelectInto() = (%#v, %v)", warmProjection, err)
	}

	aggregate := Aggregate2(CountRows[resultTestModel](), Max(fields.ID),
		func(count int64, maximum Optional[int64]) resultAggregate2 {
			return resultAggregate2{Count: count, MaxID: maximum}
		})
	aggregated, err := AggregateInto(context.Background(), source, aggregate)
	if err != nil {
		t.Fatalf("AggregateInto() error = %v", err)
	}
	maximum, valid := aggregated.MaxID.Get()
	if aggregated.Count != 1 || !valid || maximum != 41 {
		t.Fatalf("AggregateInto() = %#v", aggregated)
	}
	cached, err := source.All(context.Background())
	if err != nil || len(cached) != 1 || cached[0].ID != 41 {
		t.Fatalf("cached All() = (%#v, %v)", cached, err)
	}

	if len(backend.plans) != 4 {
		t.Fatalf("backend calls = %d, want projection + model + projection + aggregate = 4", len(backend.plans))
	}
	projectionShape, shapeErr := query.NewProjectionResult(fields.Title.reference)
	if shapeErr != nil {
		t.Fatalf("NewProjectionResult() error = %v", shapeErr)
	}
	wantProjectionPlan, shapeErr := sourcePlan.WithResultShape(projectionShape)
	if shapeErr != nil {
		t.Fatalf("WithResultShape(projection) error = %v", shapeErr)
	}
	aggregateShape, shapeErr := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(fields.ID.reference))
	if shapeErr != nil {
		t.Fatalf("NewAggregateResult() error = %v", shapeErr)
	}
	wantAggregatePlan, shapeErr := sourcePlan.WithResultShape(aggregateShape)
	if shapeErr != nil {
		t.Fatalf("WithResultShape(aggregate) error = %v", shapeErr)
	}
	if !backend.plans[0].Equal(wantProjectionPlan) || !backend.plans[1].Equal(sourcePlan) ||
		!backend.plans[2].Equal(wantProjectionPlan) || !backend.plans[3].Equal(wantAggregatePlan) {
		t.Fatalf("terminal plans = %#v, want derived projection/model/projection/aggregate plans", backend.plans)
	}
	if source.evaluation != sourceEvaluation || !source.Plan().Equal(sourcePlan) || source.Plan().ResultShape().Kind() != query.ResultModel {
		t.Fatal("result terminal mutated the source plan or source evaluation ownership")
	}
}

func TestTypedNilContextIsRejectedByModelAndScalarTerminalsBeforeIO(t *testing.T) {
	var typedNil *resultTestTypedNilContext
	var ctx context.Context = typedNil
	backend := resultBackendForRows([][]any{{int64(1), "unused", nil, true}})
	source := newResultTestQuerySet(backend)
	fields := newResultTestFields()
	projection := Project1(fields.ID, func(id int64) int64 { return id })
	aggregate := Aggregate1(CountRows[resultTestModel](), func(count int64) int64 { return count })

	for _, test := range []struct {
		name string
		run  func() error
	}{
		{name: "model", run: func() error { _, err := source.All(ctx); return err }},
		{name: "projection", run: func() error { _, err := SelectInto(ctx, source, projection); return err }},
		{name: "aggregate", run: func() error { _, err := AggregateInto(ctx, source, aggregate); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertResultQueryError(t, test.run(), query.CategoryQuery, query.CodeInvalidPlan)
			if len(backend.plans) != 0 {
				t.Fatalf("typed-nil context performed %d backend call(s)", len(backend.plans))
			}
		})
	}
}

func TestCountRelationTraversalKeepsColdAndWarmSemantics(t *testing.T) {
	authorModel, postModel := relationObjectTestModels()
	authorField := relationObjectTestPostField("author")
	authorName, ok := findField(authorModel.Fields, "name")
	if !ok {
		t.Fatal("author name field is missing")
	}
	path, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		postModel.DBTable,
		authorField.Name,
		authorField.Column,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		authorModel.DBTable,
		authorModel.Fields[0].Column,
		false,
		fieldReference(authorName),
	)
	if err != nil {
		t.Fatalf("NewForwardRelationPath() error = %v", err)
	}
	backend := &resultTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return &resultTestRows{values: [][]any{
			{int64(10), "first", int64(1), nil},
			{int64(11), "second", int64(1), nil},
		}}, nil
	}}
	source := NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend)
	source.plan = source.plan.WithConditions(
		query.NewRelatedCondition(path, query.LookupExact, query.String("Ada")),
	)
	id := NewIntegerField[relationObjectTestPost](relationObjectTestPostField("id"))
	if _, projectionErr := SelectInto(
		context.Background(),
		source,
		Project1(id, func(value int64) int64 { return value }),
	); projectionErr == nil {
		t.Fatal("relation-backed SelectInto() succeeded")
	} else {
		assertResultQueryError(t, projectionErr, query.CategoryQuery, query.CodeUnsupported)
	}
	if _, aggregateErr := AggregateInto(
		context.Background(),
		source,
		Aggregate1(CountRows[relationObjectTestPost](), func(count int64) int64 { return count }),
	); aggregateErr == nil {
		t.Fatal("relation-backed AggregateInto() succeeded")
	} else {
		assertResultQueryError(t, aggregateErr, query.CategoryQuery, query.CodeUnsupported)
	}
	if len(backend.plans) != 0 {
		t.Fatalf("rejected relation scalar terminals performed %d backend calls", len(backend.plans))
	}

	cold, err := source.Count(context.Background())
	if err != nil || cold != 2 {
		t.Fatalf("cold relation Count() = (%d, %v), want (2, nil)", cold, err)
	}
	if len(backend.plans) != 1 || backend.plans[0].ResultShape().Kind() != query.ResultModel {
		t.Fatalf("cold relation Count() plans = %#v, want one model-row fallback", backend.plans)
	}
	values, err := source.All(context.Background())
	if err != nil || len(values) != 2 {
		t.Fatalf("relation All() = (%#v, %v)", values, err)
	}
	warm, err := source.Count(context.Background())
	if err != nil || warm != cold {
		t.Fatalf("warm relation Count() = (%d, %v), want cold value %d", warm, err, cold)
	}
	if len(backend.plans) != 2 {
		t.Fatalf("warm relation Count() performed I/O: plans = %d, want 2 total", len(backend.plans))
	}
}

func TestSelectIntoRowsAndContextFailuresCloseExactlyOnce(t *testing.T) {
	fields := newResultTestFields()
	scanFailure := errors.New("projection scan failure")
	iterationFailure := errors.New("projection iteration failure")
	closeFailure := errors.New("projection close failure")
	combinedIterationFailure := errors.New("projection combined iteration failure")
	combinedCloseFailure := errors.New("projection combined close failure")

	tests := []struct {
		name      string
		configure func(*resultTestRows, context.CancelFunc)
		want      []error
	}{
		{name: "scan", configure: func(rows *resultTestRows, _ context.CancelFunc) { rows.scanErr = scanFailure }, want: []error{scanFailure}},
		{name: "iteration", configure: func(rows *resultTestRows, _ context.CancelFunc) { rows.iterationErr = iterationFailure }, want: []error{iterationFailure}},
		{name: "close", configure: func(rows *resultTestRows, _ context.CancelFunc) { rows.closeErr = closeFailure }, want: []error{closeFailure}},
		{name: "context", configure: func(rows *resultTestRows, cancel context.CancelFunc) {
			rows.onNext = func(call int) {
				if call == 2 {
					cancel()
				}
			}
		}, want: []error{context.Canceled}},
		{name: "context iteration and close", configure: func(rows *resultTestRows, cancel context.CancelFunc) {
			rows.iterationErr = combinedIterationFailure
			rows.closeErr = combinedCloseFailure
			rows.onNext = func(call int) {
				if call == 2 {
					cancel()
				}
			}
		}, want: []error{context.Canceled, combinedIterationFailure, combinedCloseFailure}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rows := &resultTestRows{values: [][]any{{int64(1)}}}
			test.configure(rows, cancel)
			backend := &resultTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}
			projection := Project1(fields.ID, func(id int64) int64 { return id })

			values, err := SelectInto(ctx, newResultTestQuerySet(backend), projection)
			if values != nil {
				t.Fatalf("SelectInto() values = %#v, want nil", values)
			}
			for _, want := range test.want {
				if !errors.Is(err, want) {
					t.Errorf("SelectInto() error %v does not preserve %v", err, want)
				}
			}
			if rows.closeCalls != 1 || rows.errCalls != 1 || rows.scanCalls != 1 {
				t.Fatalf("rows lifecycle = scan %d err %d close %d, want 1/1/1", rows.scanCalls, rows.errCalls, rows.closeCalls)
			}
		})
	}
}

func TestAggregateIntoRequiresExactlyOneRow(t *testing.T) {
	aggregate := Aggregate1(CountRows[resultTestModel](), func(count int64) int64 { return count })

	tests := []struct {
		name          string
		values        [][]any
		want          int64
		wantError     bool
		wantNextCalls int
		wantScanCalls int
	}{
		{name: "no rows", wantError: true, wantNextCalls: 1},
		{name: "exactly one row", values: [][]any{{int64(6)}}, want: 6, wantNextCalls: 2, wantScanCalls: 1},
		{name: "more than one row", values: [][]any{{int64(6)}, {int64(7)}}, wantError: true, wantNextCalls: 2, wantScanCalls: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows := &resultTestRows{values: test.values}
			backend := &resultTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}
			value, err := AggregateInto(context.Background(), newResultTestQuerySet(backend), aggregate)
			if test.wantError {
				assertResultQueryError(t, err, query.CategoryBackend, query.CodeInvalidPlan)
				if value != 0 {
					t.Fatalf("AggregateInto() value = %d on cardinality error, want zero", value)
				}
			} else if err != nil || value != test.want {
				t.Fatalf("AggregateInto() = (%d, %v), want (%d, nil)", value, err, test.want)
			}
			if rows.nextCalls != test.wantNextCalls || rows.scanCalls != test.wantScanCalls ||
				rows.errCalls != 1 || rows.closeCalls != 1 {
				t.Fatalf("rows lifecycle = next %d scan %d err %d close %d", rows.nextCalls, rows.scanCalls, rows.errCalls, rows.closeCalls)
			}
			if len(backend.plans) != 1 {
				t.Fatalf("backend calls = %d, want 1", len(backend.plans))
			}
			assertResultAggregatePlan(t, backend.plans[0], query.CountAllResult())
		})
	}
}

func TestAggregateIntoFirstNextFailureDoesNotSynthesizeCardinalityError(t *testing.T) {
	iterationFailure := errors.New("aggregate first-next iteration failure")
	closeFailure := errors.New("aggregate first-next close failure")
	aggregate := Aggregate1(CountRows[resultTestModel](), func(count int64) int64 { return count })

	tests := []struct {
		name      string
		configure func(*resultTestRows, context.CancelFunc)
		want      []error
	}{
		{
			name: "iteration",
			configure: func(rows *resultTestRows, _ context.CancelFunc) {
				rows.iterationErr = iterationFailure
			},
			want: []error{iterationFailure},
		},
		{
			name: "context and close",
			configure: func(rows *resultTestRows, cancel context.CancelFunc) {
				rows.closeErr = closeFailure
				rows.onNext = func(call int) {
					if call == 1 {
						cancel()
					}
				}
			},
			want: []error{closeFailure, context.Canceled},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rows := &resultTestRows{}
			test.configure(rows, cancel)
			backend := &resultTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}

			value, err := AggregateInto(ctx, newResultTestQuerySet(backend), aggregate)
			if value != 0 {
				t.Fatalf("AggregateInto() value = %d, want zero", value)
			}
			for _, want := range test.want {
				if !errors.Is(err, want) {
					t.Errorf("AggregateInto() error %v does not preserve %v", err, want)
				}
			}
			var queryErr *query.Error
			if errors.As(err, &queryErr) && queryErr.Category == query.CategoryBackend && queryErr.Code == query.CodeInvalidPlan {
				t.Fatalf("AggregateInto() synthesized cardinality error over lifecycle failure: %v", err)
			}
			if rows.nextCalls != 1 || rows.scanCalls != 0 || rows.errCalls != 1 || rows.closeCalls != 1 {
				t.Fatalf("rows lifecycle = next %d scan %d err %d close %d, want 1/0/1/1", rows.nextCalls, rows.scanCalls, rows.errCalls, rows.closeCalls)
			}
		})
	}
}

func TestAggregateIntoRowsAndContextFailuresCloseExactlyOnce(t *testing.T) {
	scanFailure := errors.New("aggregate scan failure")
	iterationFailure := errors.New("aggregate iteration failure")
	closeFailure := errors.New("aggregate close failure")
	combinedIterationFailure := errors.New("aggregate combined iteration failure")
	combinedCloseFailure := errors.New("aggregate combined close failure")
	aggregate := Aggregate1(CountRows[resultTestModel](), func(count int64) int64 { return count })

	tests := []struct {
		name      string
		configure func(*resultTestRows, context.CancelFunc)
		want      []error
	}{
		{name: "scan", configure: func(rows *resultTestRows, _ context.CancelFunc) { rows.scanErr = scanFailure }, want: []error{scanFailure}},
		{name: "iteration", configure: func(rows *resultTestRows, _ context.CancelFunc) { rows.iterationErr = iterationFailure }, want: []error{iterationFailure}},
		{name: "close", configure: func(rows *resultTestRows, _ context.CancelFunc) { rows.closeErr = closeFailure }, want: []error{closeFailure}},
		{name: "context", configure: func(rows *resultTestRows, cancel context.CancelFunc) {
			rows.onNext = func(call int) {
				if call == 2 {
					cancel()
				}
			}
		}, want: []error{context.Canceled}},
		{name: "context iteration and close", configure: func(rows *resultTestRows, cancel context.CancelFunc) {
			rows.iterationErr = combinedIterationFailure
			rows.closeErr = combinedCloseFailure
			rows.onNext = func(call int) {
				if call == 2 {
					cancel()
				}
			}
		}, want: []error{context.Canceled, combinedIterationFailure, combinedCloseFailure}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rows := &resultTestRows{values: [][]any{{int64(8)}}}
			test.configure(rows, cancel)
			backend := &resultTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}

			value, err := AggregateInto(ctx, newResultTestQuerySet(backend), aggregate)
			if value != 0 {
				t.Fatalf("AggregateInto() value = %d, want zero", value)
			}
			for _, want := range test.want {
				if !errors.Is(err, want) {
					t.Errorf("AggregateInto() error %v does not preserve %v", err, want)
				}
			}
			if rows.closeCalls != 1 || rows.errCalls != 1 || rows.scanCalls != 1 {
				t.Fatalf("rows lifecycle = scan %d err %d close %d, want 1/1/1", rows.scanCalls, rows.errCalls, rows.closeCalls)
			}
		})
	}
}

func TestResultBuildersRejectNilAndInvalidInputsBeforeIO(t *testing.T) {
	fields := newResultTestFields()
	invalidTitle := NewStringField[resultTestModel](ir.Field{
		Name: "title", Column: "title", Kind: ir.FieldBoolean,
	})
	var nilID *IntegerField[resultTestModel]
	var nilTitle *StringField[resultTestModel]

	tests := []struct {
		name string
		run  func(QuerySet[resultTestModel]) error
	}{
		{name: "nil projection builder", run: func(source QuerySet[resultTestModel]) error {
			_, err := SelectInto(context.Background(), source, Project1(fields.ID, (func(int64) int64)(nil)))
			return err
		}},
		{name: "nil projection field", run: func(source QuerySet[resultTestModel]) error {
			projection := Project1[resultTestModel, int64, int64](nilID, func(id int64) int64 { return id })
			_, err := SelectInto(context.Background(), source, projection)
			return err
		}},
		{name: "zero projection field", run: func(source QuerySet[resultTestModel]) error {
			projection := Project1(IntegerField[resultTestModel]{}, func(id int64) int64 { return id })
			_, err := SelectInto(context.Background(), source, projection)
			return err
		}},
		{name: "invalid projection field metadata", run: func(source QuerySet[resultTestModel]) error {
			_, err := SelectInto(context.Background(), source, Project1(invalidTitle, func(title string) string { return title }))
			return err
		}},
		{name: "zero projection", run: func(source QuerySet[resultTestModel]) error {
			_, err := SelectInto(context.Background(), source, Projection[resultTestModel, int64]{})
			return err
		}},
		{name: "nil aggregate builder", run: func(source QuerySet[resultTestModel]) error {
			aggregate := Aggregate1(CountRows[resultTestModel](), (func(int64) int64)(nil))
			_, err := AggregateInto(context.Background(), source, aggregate)
			return err
		}},
		{name: "nil max field", run: func(source QuerySet[resultTestModel]) error {
			maximum := Max[resultTestModel, string](nilTitle)
			aggregate := Aggregate1(maximum, func(value Optional[string]) Optional[string] { return value })
			_, err := AggregateInto(context.Background(), source, aggregate)
			return err
		}},
		{name: "zero max field", run: func(source QuerySet[resultTestModel]) error {
			maximum := Max(IntegerField[resultTestModel]{})
			aggregate := Aggregate1(maximum, func(value Optional[int64]) Optional[int64] { return value })
			_, err := AggregateInto(context.Background(), source, aggregate)
			return err
		}},
		{name: "zero aggregate", run: func(source QuerySet[resultTestModel]) error {
			_, err := AggregateInto(context.Background(), source, Aggregate[resultTestModel, int64]{})
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &resultTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
				return &resultTestRows{}, nil
			}}
			err := test.run(newResultTestQuerySet(backend))
			assertResultQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
			if len(backend.plans) != 0 {
				t.Fatalf("invalid result builder performed %d backend calls", len(backend.plans))
			}
		})
	}

	t.Run("canceled context", func(t *testing.T) {
		backend := resultBackendForRows([][]any{{int64(1)}})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := SelectInto(ctx, newResultTestQuerySet(backend), Project1(fields.ID, func(id int64) int64 { return id }))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SelectInto(canceled context) error = %v", err)
		}
		if len(backend.plans) != 0 {
			t.Fatalf("canceled terminal performed %d backend calls", len(backend.plans))
		}
	})
}

func TestDistinctOffsetAndFreshOwnIndependentEvaluationState(t *testing.T) {
	backend := &resultTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
		id := int64(call + 1)
		return &resultTestRows{values: [][]any{{id, fmt.Sprintf("call-%d", id), nil, true}}}, nil
	}}
	base := newResultTestQuerySet(backend)
	distinct := base.Distinct()
	offset, err := distinct.Offset(3)
	if err != nil {
		t.Fatalf("Offset() error = %v", err)
	}
	fresh := offset.Fresh()
	directCopy := offset

	if len(backend.plans) != 0 {
		t.Fatalf("query transformations performed %d backend calls", len(backend.plans))
	}
	if base.Plan().Distinct() || !distinct.Plan().Distinct() || !offset.Plan().Distinct() {
		t.Fatalf("distinct flags = base %v distinct %v offset %v", base.Plan().Distinct(), distinct.Plan().Distinct(), offset.Plan().Distinct())
	}
	if _, ok := base.Plan().Offset(); ok {
		t.Fatal("Offset() mutated base plan")
	}
	if got, ok := offset.Plan().Offset(); !ok || got != 3 {
		t.Fatalf("offset plan = (%d, %v), want (3, true)", got, ok)
	}
	if !fresh.Plan().Equal(offset.Plan()) {
		t.Fatal("Fresh() changed the immutable query plan")
	}
	if base.evaluation == distinct.evaluation || distinct.evaluation == offset.evaluation ||
		offset.evaluation == fresh.evaluation || directCopy.evaluation != offset.evaluation {
		t.Fatal("Distinct/Offset/Fresh/direct-copy evaluation ownership is incorrect")
	}

	assertResultModelID(t, base, 1)
	assertResultModelID(t, distinct, 2)
	assertResultModelID(t, offset, 3)
	assertResultModelID(t, directCopy, 3)
	assertResultModelID(t, fresh, 4)
	if len(backend.plans) != 4 {
		t.Fatalf("backend calls = %d, want one per independent evaluation state = 4", len(backend.plans))
	}
	if backend.plans[0].Distinct() || !backend.plans[1].Distinct() || !backend.plans[2].Equal(offset.Plan()) ||
		!backend.plans[3].Equal(fresh.Plan()) {
		t.Fatalf("executed plans do not preserve distinct/offset/fresh semantics: %#v", backend.plans)
	}

	beforeInvalid := len(backend.plans)
	if _, invalidErr := base.Offset(-1); invalidErr == nil {
		t.Fatal("Offset(-1) succeeded")
	} else {
		assertResultQueryError(t, invalidErr, query.CategoryQuery, query.CodeInvalidOffset)
	}
	maximum, maximumErr := base.Offset(math.MaxInt32)
	if maximumErr != nil {
		t.Fatalf("Offset(MaxInt32) error = %v", maximumErr)
	}
	if got, ok := maximum.Plan().Offset(); !ok || got != math.MaxInt32 {
		t.Fatalf("maximum offset = (%d, %v)", got, ok)
	}
	if strconv.IntSize > 32 {
		tooLarge64 := int64(math.MaxInt32)
		tooLarge64++
		tooLarge := int(tooLarge64)
		if _, invalidErr := base.Offset(tooLarge); invalidErr == nil {
			t.Fatalf("Offset(%d) succeeded", tooLarge)
		} else {
			assertResultQueryError(t, invalidErr, query.CategoryQuery, query.CodeInvalidOffset)
		}
	}
	if len(backend.plans) != beforeInvalid {
		t.Fatalf("offset validation performed backend I/O: calls %d -> %d", beforeInvalid, len(backend.plans))
	}
}

type resultProjection2 struct {
	ID    int64
	Title string
}

type resultProjection3 struct {
	ID    int64
	Title string
	Note  *string
}

type resultProjection4 struct {
	ID        int64
	Title     string
	Note      *string
	Published bool
}

type resultAggregate2 struct {
	Count int64
	MaxID Optional[int64]
}

type resultAggregate3 struct {
	Count    int64
	MaxID    Optional[int64]
	MaxTitle Optional[string]
}

type resultAggregate4 struct {
	Count    int64
	MaxID    Optional[int64]
	MaxTitle Optional[string]
	MaxNote  Optional[string]
}

type resultTestModel struct {
	ID        int64
	Title     string
	Note      *string
	Published bool
}

type resultTestDescriptor struct{}

func (resultTestDescriptor) Metadata() ir.Model { return resultTestMetadata() }

func (resultTestDescriptor) Scan(row db.Row) (resultTestModel, error) {
	var value resultTestModel
	if err := row.Scan(&value.ID, &value.Title, &value.Note, &value.Published); err != nil {
		return resultTestModel{}, err
	}
	return value, nil
}

func (resultTestDescriptor) CloneModel(value resultTestModel) resultTestModel {
	clone := value
	if value.Note != nil {
		note := *value.Note
		clone.Note = &note
	}
	return clone
}

func resultTestMetadata() ir.Model {
	return ir.Model{
		Name:    "result_test_model",
		GoName:  "ResultTestModel",
		DBTable: "result_test_model",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar},
			{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldChar, Nullable: true},
			{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean},
		},
	}
}

type resultTestFields struct {
	ID        IntegerField[resultTestModel]
	Title     StringField[resultTestModel]
	Note      NullableStringField[resultTestModel]
	Published BooleanField[resultTestModel]
}

func newResultTestFields() resultTestFields {
	metadata := resultTestMetadata()
	return resultTestFields{
		ID:        NewIntegerField[resultTestModel](metadata.Fields[0]),
		Title:     NewStringField[resultTestModel](metadata.Fields[1]),
		Note:      NewNullableStringField[resultTestModel](metadata.Fields[2]),
		Published: NewBooleanField[resultTestModel](metadata.Fields[3]),
	}
}

func newResultTestQuerySet(backend db.Queryer) QuerySet[resultTestModel] {
	return NewManager[resultTestModel](resultTestDescriptor{}).Using(backend)
}

type resultTestBackend struct {
	plans []query.Plan
	query func(int, context.Context, query.Plan) (db.Rows, error)
}

type resultTestTypedNilContext struct {
	context.Context
}

func (backend *resultTestBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	call := len(backend.plans)
	backend.plans = append(backend.plans, plan)
	return backend.query(call, ctx, plan)
}

func resultBackendForRows(values [][]any) *resultTestBackend {
	return &resultTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return &resultTestRows{values: values}, nil
	}}
}

type resultTestRows struct {
	values       [][]any
	position     int
	scanErr      error
	iterationErr error
	closeErr     error
	onNext       func(int)
	nextCalls    int
	scanCalls    int
	errCalls     int
	closeCalls   int
}

func (rows *resultTestRows) Next() bool {
	rows.nextCalls++
	if rows.onNext != nil {
		rows.onNext(rows.nextCalls)
	}
	if rows.position >= len(rows.values) {
		return false
	}
	rows.position++
	return true
}

func (rows *resultTestRows) Scan(destinations ...any) error {
	rows.scanCalls++
	if rows.scanErr != nil {
		return rows.scanErr
	}
	if rows.position == 0 || rows.position > len(rows.values) {
		return errors.New("Scan called without a current row")
	}
	values := rows.values[rows.position-1]
	if len(destinations) != len(values) {
		return fmt.Errorf("destinations = %d, row values = %d", len(destinations), len(values))
	}
	for index := range destinations {
		if err := assignResultTestDestination(destinations[index], values[index]); err != nil {
			return fmt.Errorf("destination %d: %w", index, err)
		}
	}
	return nil
}

func (rows *resultTestRows) Err() error {
	rows.errCalls++
	return rows.iterationErr
}

func (rows *resultTestRows) Close() error {
	rows.closeCalls++
	return rows.closeErr
}

func assignResultTestDestination(destination, source any) error {
	switch target := destination.(type) {
	case *int64:
		value, ok := source.(int64)
		if !ok {
			return fmt.Errorf("source type = %T, want int64", source)
		}
		*target = value
	case *string:
		value, ok := source.(string)
		if !ok {
			return fmt.Errorf("source type = %T, want string", source)
		}
		*target = value
	case **string:
		if source == nil {
			*target = nil
			return nil
		}
		value, ok := source.(string)
		if !ok {
			return fmt.Errorf("source type = %T, want string or nil", source)
		}
		copy := value
		*target = &copy
	case **int64:
		if source == nil {
			*target = nil
			return nil
		}
		value, ok := source.(int64)
		if !ok {
			return fmt.Errorf("source type = %T, want int64 or nil", source)
		}
		copy := value
		*target = &copy
	case *bool:
		value, ok := source.(bool)
		if !ok {
			return fmt.Errorf("source type = %T, want bool", source)
		}
		*target = value
	case *sql.NullInt64:
		if source == nil {
			*target = sql.NullInt64{}
			return nil
		}
		value, ok := source.(int64)
		if !ok {
			return fmt.Errorf("source type = %T, want int64 or nil", source)
		}
		*target = sql.NullInt64{Int64: value, Valid: true}
	case *sql.NullString:
		if source == nil {
			*target = sql.NullString{}
			return nil
		}
		value, ok := source.(string)
		if !ok {
			return fmt.Errorf("source type = %T, want string or nil", source)
		}
		*target = sql.NullString{String: value, Valid: true}
	default:
		return fmt.Errorf("unsupported destination type %T", destination)
	}
	return nil
}

func assertResultProjectionPlan(t *testing.T, plan query.Plan, fields ...query.FieldRef) {
	t.Helper()
	shape := plan.ResultShape()
	if shape.Kind() != query.ResultProjection {
		t.Fatalf("result kind = %q, want projection", shape.Kind())
	}
	expressions := shape.Expressions()
	if len(expressions) != len(fields) {
		t.Fatalf("projection expressions = %d, want %d", len(expressions), len(fields))
	}
	for index := range fields {
		field, ok := expressions[index].Field()
		if expressions[index].Kind() != query.ResultField || !ok || !field.Equal(fields[index]) {
			t.Fatalf("projection expression %d = %#v, want field %#v", index, expressions[index], fields[index])
		}
	}
	assertResultSourceFields(t, plan)
}

func assertResultAggregatePlan(t *testing.T, plan query.Plan, expressions ...query.ResultExpression) {
	t.Helper()
	shape := plan.ResultShape()
	if shape.Kind() != query.ResultAggregate {
		t.Fatalf("result kind = %q, want aggregate", shape.Kind())
	}
	got := shape.Expressions()
	if len(got) != len(expressions) {
		t.Fatalf("aggregate expressions = %d, want %d", len(got), len(expressions))
	}
	for index := range expressions {
		if !got[index].Equal(expressions[index]) {
			t.Fatalf("aggregate expression %d = %#v, want %#v", index, got[index], expressions[index])
		}
	}
	assertResultSourceFields(t, plan)
}

func assertResultSourceFields(t *testing.T, plan query.Plan) {
	t.Helper()
	metadata := resultTestMetadata()
	source := plan.SourceFields()
	if len(source) != len(metadata.Fields) {
		t.Fatalf("source fields = %d, want %d", len(source), len(metadata.Fields))
	}
	for index, field := range metadata.Fields {
		if !source[index].Equal(fieldReference(field)) {
			t.Fatalf("source field %d = %#v, want %#v", index, source[index], fieldReference(field))
		}
	}
}

func assertResultQueryError(t *testing.T, err error, category, code string) {
	t.Helper()
	var result *query.Error
	if !errors.As(err, &result) || result.Category != category || result.Code != code {
		t.Fatalf("error = %v, want %s/%s", err, category, code)
	}
}

func assertResultModelID(t *testing.T, source QuerySet[resultTestModel], want int64) {
	t.Helper()
	values, err := source.All(context.Background())
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if len(values) != 1 || values[0].ID != want {
		t.Fatalf("All() = %#v, want ID %d", values, want)
	}
}
