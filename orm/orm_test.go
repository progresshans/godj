package orm_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestTypedAndDynamicPredicatesConvergeToSamePlan(t *testing.T) {
	t.Parallel()

	backend := &spyBackend{}
	base := models.ArticleObjects.Using(backend)
	typed := base.Filter(models.ArticleFields.Title.IContains("django"))
	dynamicPredicates, err := orm.ParseDynamic(
		models.ArticleDescriptor{},
		nil,
		[]orm.LookupInput{{Key: "title__icontains", Value: "django"}},
	)
	if err != nil {
		t.Fatalf("ParseDynamic() error = %v", err)
	}
	dynamic := base.Filter(dynamicPredicates...)
	if !typed.Plan().Equal(dynamic.Plan()) {
		t.Fatalf("typed and dynamic plans differ:\ntyped=%#v\ndynamic=%#v", typed.Plan(), dynamic.Plan())
	}
	if backend.calls.Load() != 0 {
		t.Fatalf("query construction performed %d backend calls", backend.calls.Load())
	}
}

func TestQuerySetChainPreservesSource(t *testing.T) {
	t.Parallel()

	base := models.ArticleObjects.Using(&spyBackend{}).Filter(models.ArticleFields.Published.Exact(true))
	derived := base.Filter(models.ArticleFields.Title.Exact("Django Deep Dive"))
	if got := len(base.Plan().Conditions()); got != 1 {
		t.Fatalf("base condition count = %d, want 1", got)
	}
	if got := len(derived.Plan().Conditions()); got != 2 {
		t.Fatalf("derived condition count = %d, want 2", got)
	}
}

func TestParseDynamicReturnsConstructionErrorsWithoutIO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy orm.LookupPolicy
		input  orm.LookupInput
		code   string
	}{
		{name: "unknown field", input: orm.LookupInput{Key: "missing", Value: "value"}, code: query.CodeUnknownField},
		{name: "unsupported lookup", input: orm.LookupInput{Key: "title__starts", Value: "Django"}, code: query.CodeUnsupportedLookup},
		{name: "disallowed lookup", policy: func(ir.Field, query.Lookup) bool { return false }, input: orm.LookupInput{Key: "title", Value: "Django"}, code: query.CodeDisallowedLookup},
		{name: "invalid value", input: orm.LookupInput{Key: "summary__isnull", Value: "maybe"}, code: query.CodeInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := &spyBackend{}
			_ = models.ArticleObjects.Using(backend)
			_, err := orm.ParseDynamic(models.ArticleDescriptor{}, test.policy, []orm.LookupInput{test.input})
			var queryError *query.Error
			if !errors.As(err, &queryError) || queryError.Code != test.code {
				t.Fatalf("error = %v, want code %s", err, test.code)
			}
			if backend.calls.Load() != 0 {
				t.Fatalf("construction error performed %d backend calls", backend.calls.Load())
			}
		})
	}
}

func TestGeneratedDescriptorMetadataIsAnIndependentCopy(t *testing.T) {
	t.Parallel()

	descriptor := models.ArticleDescriptor{}
	first := descriptor.Metadata()
	first.Fields[0].Name = "changed"
	first.Fields[2].Default.Boolean = true
	second := descriptor.Metadata()
	if second.Fields[0].Name != "id" {
		t.Fatalf("descriptor metadata was mutable: %#v", second.Fields[0])
	}
	if second.Fields[2].Default == nil || second.Fields[2].Default.Boolean {
		t.Fatalf("descriptor default metadata was mutable: %#v", second.Fields[2].Default)
	}
}

func TestGeneratedDescriptorConcurrentReads(t *testing.T) {
	t.Parallel()

	descriptor := models.ArticleDescriptor{}
	var group sync.WaitGroup
	for index := 0; index < 32; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for count := 0; count < 100; count++ {
				metadata := descriptor.Metadata()
				if len(metadata.Fields) != 4 {
					t.Errorf("field count = %d, want 4", len(metadata.Fields))
					return
				}
			}
		}()
	}
	group.Wait()
}

func TestTypedNilDescriptorReturnsStructuredError(t *testing.T) {
	t.Parallel()

	var descriptor *nilDescriptor
	querySet := orm.NewManager[testModel](descriptor).Using(&spyBackend{})
	_, err := querySet.All(context.Background())
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("All() error = %v, want invalid_plan", err)
	}
	_, err = orm.ParseDynamic[testModel](descriptor, nil, nil)
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("ParseDynamic() error = %v, want invalid_plan", err)
	}
}

func TestMismatchedTypedFieldMetadataFailsBeforeIO(t *testing.T) {
	t.Parallel()

	backend := &spyBackend{}
	wrong := orm.NewStringField[testModel](ir.Field{
		Name:   "published",
		GoName: "Published",
		Column: "published",
		Kind:   ir.FieldBoolean,
	})
	_, err := orm.NewManager[testModel](testDescriptor{}).
		Using(backend).
		Filter(wrong.Exact("true")).
		All(context.Background())
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("All() error = %v, want invalid_plan", err)
	}
	if backend.calls.Load() != 0 {
		t.Fatalf("invalid field metadata performed %d backend calls", backend.calls.Load())
	}
}

func TestAllDefendsBackendRowsResultContract(t *testing.T) {
	t.Parallel()

	querySet := orm.NewManager[testModel](testDescriptor{})
	_, err := querySet.Using(rawBackend{}).All(context.Background())
	if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("nil rows error = %v, want invalid_plan", err)
	}

	queryFailure := errors.New("query failed")
	rows := &fakeRows{}
	_, err = querySet.Using(rawBackend{rows: rows, err: queryFailure}).All(context.Background())
	if !errors.Is(err, queryFailure) {
		t.Fatalf("rows plus error = %v, want query failure", err)
	}
	if rows.closeCalls.Load() != 1 {
		t.Fatalf("rows returned with error Close() calls = %d, want 1", rows.closeCalls.Load())
	}
}

func TestAllClosesRowsAcrossSuccessAndFailure(t *testing.T) {
	t.Parallel()

	scanFailure := errors.New("scan failed")
	iterationFailure := errors.New("iteration failed")
	closeFailure := errors.New("close failed")
	tests := []struct {
		name     string
		rows     *fakeRows
		wantRows []testModel
		wantErr  error
	}{
		{name: "success", rows: &fakeRows{values: []int64{7}}, wantRows: []testModel{{ID: 7}}},
		{name: "scan error", rows: &fakeRows{values: []int64{7}, scanErr: scanFailure}, wantErr: scanFailure},
		{name: "iteration error", rows: &fakeRows{iterationErr: iterationFailure}, wantErr: iterationFailure},
		{name: "close error", rows: &fakeRows{closeErr: closeFailure}, wantErr: closeFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := &spyBackend{rows: test.rows}
			values, err := orm.NewManager[testModel](testDescriptor{}).Using(backend).All(context.Background())
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("All() error = %v, want %v", err, test.wantErr)
				}
			} else if err != nil {
				t.Fatalf("All() error = %v", err)
			}
			if fmt.Sprint(values) != fmt.Sprint(test.wantRows) {
				t.Fatalf("All() values = %#v, want %#v", values, test.wantRows)
			}
			if test.rows.closeCalls.Load() != 1 {
				t.Fatalf("Close() calls = %d, want 1", test.rows.closeCalls.Load())
			}
		})
	}
}

type spyBackend struct {
	calls atomic.Uint64
	rows  db.Rows
}

type rawBackend struct {
	rows db.Rows
	err  error
}

func (backend rawBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	return backend.rows, backend.err
}

func (backend *spyBackend) Query(ctx context.Context, _ query.Plan) (db.Rows, error) {
	backend.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if backend.rows == nil {
		return &fakeRows{}, nil
	}
	return backend.rows, nil
}

type testModel struct{ ID int64 }

type testDescriptor struct{}

type nilDescriptor struct{}

func (*nilDescriptor) Metadata() ir.Model { panic("typed nil descriptor must not be called") }
func (*nilDescriptor) Scan(db.Row) (testModel, error) {
	panic("typed nil descriptor must not be called")
}
func (*nilDescriptor) CloneModel(testModel) testModel {
	panic("typed nil descriptor must not be called")
}

func (testDescriptor) Metadata() ir.Model {
	return ir.Model{
		Name:    "test_model",
		GoName:  "TestModel",
		DBTable: "test_model",
		Fields: []ir.Field{{
			Name:       "id",
			GoName:     "ID",
			Column:     "id",
			Kind:       ir.FieldAuto,
			PrimaryKey: true,
		}},
	}
}

func (testDescriptor) Scan(row db.Row) (testModel, error) {
	var result testModel
	if err := row.Scan(&result.ID); err != nil {
		return testModel{}, err
	}
	return result, nil
}

func (testDescriptor) CloneModel(value testModel) testModel { return value }

type fakeRows struct {
	values       []int64
	position     int
	scanErr      error
	iterationErr error
	closeErr     error
	closeCalls   atomic.Uint64
}

func (rows *fakeRows) Next() bool {
	if rows.position >= len(rows.values) {
		return false
	}
	rows.position++
	return true
}

func (rows *fakeRows) Scan(destinations ...any) error {
	if rows.scanErr != nil {
		return rows.scanErr
	}
	if len(destinations) != 1 {
		return fmt.Errorf("destinations = %d, want 1", len(destinations))
	}
	pointer, ok := destinations[0].(*int64)
	if !ok {
		return fmt.Errorf("destination type = %T, want *int64", destinations[0])
	}
	*pointer = rows.values[rows.position-1]
	return nil
}

func (rows *fakeRows) Err() error { return rows.iterationErr }

func (rows *fakeRows) Close() error {
	rows.closeCalls.Add(1)
	return rows.closeErr
}
