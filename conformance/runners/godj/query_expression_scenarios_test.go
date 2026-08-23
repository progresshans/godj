package godj

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestQueryExpressionScenarioRegistryIsExactAndFailClosed(t *testing.T) {
	wanted := []string{
		"django.query.expression.scalar_exact_or",
		"django.query.expression.escaped_ascii_icontains_or",
		"django.query.expression.grouped_or_and_reuse",
		"django.query.expression.nonnull_scalar_not",
		"django.query.expression.nullable_negation_truth_table",
		"django.query.expression.implicit_filter_and",
		"django.query.expression.nested_connector_order_and_source_independence",
		"django.query.expression.composite_distinct_stable_page",
		"django.query.expression.projection_outside_predicate",
		"django.query.expression.composite_count_max",
	}
	if len(queryExpressionScenarioRegistry) != len(wanted) {
		t.Fatalf("query-expression registry size = %d, want %d", len(queryExpressionScenarioRegistry), len(wanted))
	}
	for _, scenario := range wanted {
		handler, ok := queryExpressionScenarioHandler(scenario)
		if !ok || handler == nil {
			t.Fatalf("queryExpressionScenarioHandler(%q) = (%v, %v), want registered handler", scenario, handler, ok)
		}
	}
	for _, scenario := range []string{
		"",
		"django.query.expression",
		"django.query.expression.scalar_exact_or.changed",
		"godj.query.expression.scalar_exact_or",
	} {
		if handler, ok := queryExpressionScenarioHandler(scenario); ok || handler != nil {
			t.Fatalf("queryExpressionScenarioHandler(%q) = (%v, %v), want fail-closed miss", scenario, handler, ok)
		}
	}
}

func TestQueryExpressionProductMatchesLockedOracleAndIsDeterministic(t *testing.T) {
	profile, manifest, oracle := loadQueryExpressionProductInputs(t)
	first := generateQueryExpressionProductSuite(t, profile, manifest)
	second := generateQueryExpressionProductSuite(t, profile, manifest)

	for name, actual := range map[string]protocol.ObservationSuite{"first": first, "second": second} {
		differences, err := protocol.Compare(profile, manifest, oracle, actual)
		if err != nil {
			t.Fatalf("Compare(%s) error = %v", name, err)
		}
		if len(differences) != 0 {
			t.Fatalf("Compare(%s) differences = %#v", name, differences)
		}
		if len(actual.Contracts) != 10 {
			t.Fatalf("%s contract count = %d, want 10", name, len(actual.Contracts))
		}
		for _, observation := range actual.Contracts {
			if observation.Status != protocol.StatusObserved {
				t.Fatalf("%s contract %s status = %q, want observed", name, observation.ID, observation.Status)
			}
		}
	}
	firstBytes, err := protocol.MarshalCanonical(first)
	if err != nil {
		t.Fatalf("MarshalCanonical(first) error = %v", err)
	}
	secondBytes, err := protocol.MarshalCanonical(second)
	if err != nil {
		t.Fatalf("MarshalCanonical(second) error = %v", err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("independent query-expression product runs changed canonical observations")
	}
}

func TestQueryExpressionSQLRecorderUsesCompiledShapeAndNullableNot(t *testing.T) {
	delegate := &queryExpressionTestQueryer{}
	recorder := &queryExpressionSQLRecorder{}
	observed := &queryExpressionObservedQueryer{delegate: delegate, recorder: recorder}

	orSource := models.ArticleObjects.Using(observed).
		Filter(orm.Or(
			models.ArticleFields.Title.Exact("Alpine Guide"),
			models.ArticleFields.Summary.IsNull(true),
		)).
		OrderBy(models.ArticleFields.ID.Asc()).
		Distinct()
	orSource, err := queryBreadthPaginate(orSource, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := observed.Query(context.Background(), orSource.Plan())
	if err != nil {
		t.Fatalf("record paginated OR: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	notSource := models.ArticleObjects.Using(observed).
		Filter(orm.Not(models.ArticleFields.Summary.IContains("orm")))
	rows, err = observed.Query(context.Background(), notSource.Plan())
	if err != nil {
		t.Fatalf("record nullable NOT: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	metric, err := recorder.metricSince(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	statements := queryBreadthTestObjectField(t, metric, "statements")
	if len(statements.Items) != 2 {
		t.Fatalf("recorded statement count = %d, want 2", len(statements.Items))
	}
	assertQueryExpressionStatementShape(t, statements.Items[0], queryExpressionShapeExpectation{
		distinct:  true,
		limit:     true,
		offset:    true,
		andCount:  "0",
		notCount:  "0",
		orCount:   "1",
		isNotNull: "0",
		isNull:    "1",
		parameters: []protocol.Value{
			protocol.String("Alpine Guide"),
		},
	})
	assertQueryExpressionStatementShape(t, statements.Items[1], queryExpressionShapeExpectation{
		andCount:  "1",
		notCount:  "1",
		orCount:   "0",
		isNotNull: "1",
		isNull:    "0",
		parameters: []protocol.Value{
			protocol.String("%orm%"),
		},
	})
	if delegate.calls != 2 {
		t.Fatalf("delegate calls = %d, want 2", delegate.calls)
	}
}

func TestQueryExpressionInvalidPlanAndCanceledContextFailBeforeBackendIO(t *testing.T) {
	t.Run("invalid plan", func(t *testing.T) {
		delegate := &queryExpressionTestQueryer{}
		recorder := &queryExpressionSQLRecorder{}
		observed := &queryExpressionObservedQueryer{delegate: delegate, recorder: recorder}
		if rows, err := observed.Query(context.Background(), query.NewPlan("", nil)); err == nil || rows != nil {
			t.Fatalf("invalid compile = (%v, %v), want nil rows and error", rows, err)
		}
		if delegate.calls != 0 || recorder.checkpoint() != 0 {
			t.Fatalf("invalid compile reached delegate/recorder: calls=%d statements=%d", delegate.calls, recorder.checkpoint())
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		delegate := &queryExpressionTestQueryer{}
		recorder := &queryExpressionSQLRecorder{}
		source := models.ArticleObjects.Using(queryExpressionObserved(delegate, recorder)).
			Filter(orm.Or(
				models.ArticleFields.Title.Exact("Alpine Guide"),
				models.ArticleFields.Title.Exact("Other"),
			))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := queryExpressionPrimaryKeyRows(ctx, source)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SelectInto(canceled context) error = %v, want context.Canceled", err)
		}
		if delegate.calls != 0 || recorder.checkpoint() != 0 {
			t.Fatalf("canceled terminal reached delegate/recorder: calls=%d statements=%d", delegate.calls, recorder.checkpoint())
		}
	})
}

func TestQueryExpressionScenarioSourceDoesNotReadExpectedArtifacts(t *testing.T) {
	contents, err := os.ReadFile("query_expression_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"LoadObservationSuite",
		"query-expression-oracle.json",
		"godj-query-expression-not-implemented.json",
		"conformance/oracles/",
	} {
		if strings.Contains(string(contents), forbidden) {
			t.Fatalf("query-expression scenario source contains forbidden expected-artifact dependency %q", forbidden)
		}
	}
}

func loadQueryExpressionProductInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "query-expression-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-expression-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle
}

func generateQueryExpressionProductSuite(t *testing.T, profile protocol.Profile, manifest protocol.Manifest) protocol.ObservationSuite {
	t.Helper()
	suite, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(query-expression) error = %v", err)
	}
	return suite
}

type queryExpressionTestQueryer struct {
	calls int
}

func (queryer *queryExpressionTestQueryer) Query(context.Context, query.Plan) (db.Rows, error) {
	queryer.calls++
	return &queryExpressionTestRows{}, nil
}

type queryExpressionTestRows struct{}

func (*queryExpressionTestRows) Next() bool { return false }
func (*queryExpressionTestRows) Scan(...any) error {
	return errors.New("query-expression test rows have no current row")
}
func (*queryExpressionTestRows) Err() error   { return nil }
func (*queryExpressionTestRows) Close() error { return nil }

type queryExpressionShapeExpectation struct {
	distinct   bool
	limit      bool
	offset     bool
	andCount   string
	notCount   string
	orCount    string
	isNotNull  string
	isNull     string
	parameters []protocol.Value
}

func assertQueryExpressionStatementShape(t *testing.T, shape protocol.Value, want queryExpressionShapeExpectation) {
	t.Helper()
	for name, expected := range map[string]bool{
		"derived_table": false,
		"distinct":      want.distinct,
		"has_limit":     want.limit,
		"has_offset":    want.offset,
	} {
		field := queryBreadthTestObjectField(t, shape, name)
		if field.Bool == nil || *field.Bool != expected {
			t.Fatalf("statement shape %s = %#v, want %v", name, field, expected)
		}
	}
	logical := queryBreadthTestObjectField(t, shape, "logical_operators")
	for name, expected := range map[string]string{"and": want.andCount, "not": want.notCount, "or": want.orCount} {
		if got := queryBreadthTestInteger(t, queryBreadthTestObjectField(t, logical, name)); got != expected {
			t.Fatalf("logical operator %s count = %q, want %q", name, got, expected)
		}
	}
	nulls := queryBreadthTestObjectField(t, shape, "null_predicates")
	for name, expected := range map[string]string{"is_not_null": want.isNotNull, "is_null": want.isNull} {
		if got := queryBreadthTestInteger(t, queryBreadthTestObjectField(t, nulls, name)); got != expected {
			t.Fatalf("null predicate %s count = %q, want %q", name, got, expected)
		}
	}
	parameters := queryBreadthTestObjectField(t, shape, "parameters")
	if !reflect.DeepEqual(parameters.Items, want.parameters) {
		t.Fatalf("statement parameters = %#v, want %#v", parameters.Items, want.parameters)
	}
	if got := queryBreadthTestString(t, queryBreadthTestObjectField(t, shape, "statement_kind")); got != "SELECT" {
		t.Fatalf("statement kind = %q, want SELECT", got)
	}
}
