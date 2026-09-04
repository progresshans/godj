package godj

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestQueryBreadthScenarioRegistryIsExactAndFailClosed(t *testing.T) {
	wanted := []string{
		"django.query.breadth.ordered_projection",
		"django.query.breadth.source_fields_outside_projection",
		"django.query.breadth.projection_cache_independence",
		"django.query.breadth.distinct_projection",
		"django.query.breadth.stable_offset_limit",
		"django.query.breadth.invalid_offset_pre_io",
		"django.query.breadth.cold_count_and_warm_cache",
		"django.query.breadth.sliced_distinct_count",
		"django.query.breadth.empty_count_and_nullable_max",
		"django.query.breadth.filtered_count_and_max",
		"django.query.breadth.terminal_failure_ownership",
		"django.query.breadth.backend_parity_reference",
	}
	if len(queryBreadthScenarioRegistry) != len(wanted) {
		t.Fatalf("query-breadth registry size = %d, want %d", len(queryBreadthScenarioRegistry), len(wanted))
	}
	for _, scenario := range wanted {
		handler, ok := queryBreadthScenarioHandler(scenario)
		if !ok || handler == nil {
			t.Fatalf("queryBreadthScenarioHandler(%q) = (%v, %v), want registered handler", scenario, handler, ok)
		}
	}
	for _, scenario := range []string{
		"",
		"django.query.breadth",
		"django.query.breadth.ordered_projection.changed",
		"godj.query.breadth.ordered_projection",
	} {
		if handler, ok := queryBreadthScenarioHandler(scenario); ok || handler != nil {
			t.Fatalf("queryBreadthScenarioHandler(%q) = (%v, %v), want fail-closed miss", scenario, handler, ok)
		}
	}
}

func TestQueryBreadthProductMatchesLockedOracleAndIsDeterministic(t *testing.T) {
	profile, manifest, oracle := loadQueryBreadthProductInputs(t)
	first := generateQueryBreadthProductSuite(t, profile, manifest)
	second := generateQueryBreadthProductSuite(t, profile, manifest)

	for name, actual := range map[string]protocol.ObservationSuite{"first": first, "second": second} {
		differences, err := protocol.Compare(profile, manifest, oracle, actual)
		if err != nil {
			t.Fatalf("Compare(%s) error = %v", name, err)
		}
		if len(differences) != 0 {
			t.Fatalf("Compare(%s) differences = %#v", name, differences)
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
		t.Fatal("independent query-breadth product runs changed canonical observations")
	}
}

func TestQueryBreadthSQLRecorderUsesCompiledStatementShapeAndFailsClosed(t *testing.T) {
	delegate := &queryBreadthTestQueryer{}
	recorder := &queryBreadthSQLRecorder{}
	observed := &queryBreadthObservedQueryer{delegate: delegate, recorder: recorder}

	base := models.ArticleObjects.Using(delegate).Plan()
	aggregate, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
	}
	direct, err := base.WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := observed.Query(context.Background(), direct)
	if err != nil {
		t.Fatalf("record direct aggregate: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	derived := base.WithDistinct()
	derived, err = derived.WithOffset(1)
	if err != nil {
		t.Fatal(err)
	}
	derived, err = derived.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}
	derived, err = derived.WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = observed.Query(context.Background(), derived)
	if err != nil {
		t.Fatalf("record derived aggregate: %v", err)
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
	assertQueryBreadthStatementShape(t, statements.Items[0], false, false, false, false, []string{"COUNT"})
	assertQueryBreadthStatementShape(t, statements.Items[1], true, true, true, true, []string{"COUNT"})
	if delegate.calls != 2 {
		t.Fatalf("delegate calls = %d, want 2", delegate.calls)
	}

	invalidRecorder := &queryBreadthSQLRecorder{}
	invalidDelegate := &queryBreadthTestQueryer{}
	invalidObserved := &queryBreadthObservedQueryer{delegate: invalidDelegate, recorder: invalidRecorder}
	if rows, err := invalidObserved.Query(context.Background(), query.NewPlan("", nil)); err == nil || rows != nil {
		t.Fatalf("invalid compile = (%v, %v), want nil rows and error", rows, err)
	}
	if invalidDelegate.calls != 0 || invalidRecorder.checkpoint() != 0 {
		t.Fatalf("invalid compile reached delegate/recorder: calls=%d statements=%d", invalidDelegate.calls, invalidRecorder.checkpoint())
	}
}

func TestQueryBreadthTerminalFaultsCloseExactlyOnceWithoutDjangoFetchMetrics(t *testing.T) {
	observation, err := queryBreadthTerminalFailureOwnership(context.Background(), "QRY-032")
	if err != nil {
		t.Fatal(err)
	}
	metrics := queryBreadthTestObjectField(t, *observation.Metrics, "steps")
	wantedNames := []string{"consumer_stop", "decode_failure", "iteration_failure", "close_failure"}
	if len(metrics.Items) != len(wantedNames) {
		t.Fatalf("terminal metric steps = %d, want %d", len(metrics.Items), len(wantedNames))
	}
	for index, metric := range metrics.Items {
		if got := queryBreadthTestString(t, queryBreadthTestObjectField(t, metric, "name")); got != wantedNames[index] {
			t.Fatalf("terminal metric %d name = %q, want %q", index, got, wantedNames[index])
		}
		if got := queryBreadthTestInteger(t, queryBreadthTestObjectField(t, metric, "close_attempts")); got != "1" {
			t.Fatalf("terminal metric %s close_attempts = %q, want 1", wantedNames[index], got)
		}
		if _, present := queryBreadthTestMaybeObjectField(metric, "fetch_calls"); present {
			t.Fatalf("terminal metric %s exposed Django-specific fetch_calls", wantedNames[index])
		}
		if got := queryBreadthTestInteger(t, queryBreadthTestObjectField(t, metric, "query_count")); got != "1" {
			t.Fatalf("terminal metric %s query_count = %q, want 1", wantedNames[index], got)
		}
	}
}

func TestQueryBreadthCanceledContextFailsBeforeBackendIO(t *testing.T) {
	delegate := &queryBreadthTestQueryer{}
	source := models.ArticleObjects.Using(delegate).OrderBy(models.ArticleFields.ID.Asc())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := orm.SelectInto(ctx, source, orm.Project1(models.ArticleFields.ID, func(id int64) int64 { return id }))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SelectInto(canceled context) error = %v, want context.Canceled", err)
	}
	if delegate.calls != 0 {
		t.Fatalf("SelectInto(canceled context) backend calls = %d, want 0", delegate.calls)
	}
}

func loadQueryBreadthProductInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "query-breadth-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-breadth-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle
}

func generateQueryBreadthProductSuite(t *testing.T, profile protocol.Profile, manifest protocol.Manifest) protocol.ObservationSuite {
	t.Helper()
	suite, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(query-breadth) error = %v", err)
	}
	return suite
}

type queryBreadthTestQueryer struct {
	calls int
}

func (queryer *queryBreadthTestQueryer) Query(context.Context, query.Plan) (db.Rows, error) {
	queryer.calls++
	return &queryBreadthTestRows{}, nil
}

type queryBreadthTestRows struct {
	closed bool
}

func (*queryBreadthTestRows) Next() bool { return false }
func (*queryBreadthTestRows) Scan(...any) error {
	return errors.New("query-breadth test rows have no current row")
}
func (*queryBreadthTestRows) Err() error        { return nil }
func (rows *queryBreadthTestRows) Close() error { rows.closed = true; return nil }

func assertQueryBreadthStatementShape(
	t *testing.T,
	shape protocol.Value,
	derived, distinct, limit, offset bool,
	aggregates []string,
) {
	t.Helper()
	for name, want := range map[string]bool{
		"derived_table": derived,
		"distinct":      distinct,
		"has_limit":     limit,
		"has_offset":    offset,
	} {
		field := queryBreadthTestObjectField(t, shape, name)
		if field.Bool == nil || *field.Bool != want {
			t.Fatalf("statement shape %s = %#v, want %v", name, field, want)
		}
	}
	functions := queryBreadthTestObjectField(t, shape, "aggregate_functions")
	gotAggregates := make([]string, len(functions.Items))
	for index, value := range functions.Items {
		gotAggregates[index] = queryBreadthTestString(t, value)
	}
	if !reflect.DeepEqual(gotAggregates, aggregates) {
		t.Fatalf("statement aggregate functions = %#v, want %#v", gotAggregates, aggregates)
	}
	if kind := queryBreadthTestString(t, queryBreadthTestObjectField(t, shape, "statement_kind")); kind != "SELECT" {
		t.Fatalf("statement kind = %q, want SELECT", kind)
	}
}

func queryBreadthTestObjectField(t *testing.T, value protocol.Value, name string) protocol.Value {
	t.Helper()
	field, ok := queryBreadthTestMaybeObjectField(value, name)
	if !ok {
		t.Fatalf("object field %q absent from %#v", name, value)
	}
	return field
}

func queryBreadthTestMaybeObjectField(value protocol.Value, name string) (protocol.Value, bool) {
	for _, field := range value.Fields {
		if field.Name == name {
			return field.Value, true
		}
	}
	return protocol.Value{}, false
}

func queryBreadthTestString(t *testing.T, value protocol.Value) string {
	t.Helper()
	if value.Type != protocol.ValueString || value.Text == nil {
		t.Fatalf("value = %#v, want string", value)
	}
	return *value.Text
}

func queryBreadthTestInteger(t *testing.T, value protocol.Value) string {
	t.Helper()
	if value.Type != protocol.ValueInt || value.Text == nil {
		t.Fatalf("value = %#v, want integer", value)
	}
	return *value.Text
}
