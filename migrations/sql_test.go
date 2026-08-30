package migrations

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
)

func TestRenderMigrationSQLMaterializesExactlyOneDetachedForwardTarget(t *testing.T) {
	t.Parallel()

	target := lifecycleAlpha2
	loaded := testLoadedDefinitionSet(lifecycleTestDefinitions())
	renderer := &migrationSQLRendererSpy{statements: []string{`ALTER TABLE "alpha_article" ADD COLUMN "published" BOOLEAN NOT NULL`}}

	statements, err := RenderMigrationSQL(context.Background(), loaded, target, renderer)
	if err != nil {
		t.Fatalf("RenderMigrationSQL() error = %v", err)
	}
	if renderer.calls != 1 {
		t.Fatalf("renderer calls = %d, want 1", renderer.calls)
	}
	if renderer.request.App != target.App || renderer.request.Name != target.Name {
		t.Fatalf("renderer identity = %s.%s, want %s.%s", renderer.request.App, renderer.request.Name, target.App, target.Name)
	}
	if len(renderer.request.Intent.Operations) != 1 {
		t.Fatalf("renderer operations = %d, want 1", len(renderer.request.Intent.Operations))
	}
	operation := renderer.request.Intent.Operations[0]
	if operation.OperationIndex != 0 || operation.Kind != backend.MigrationAddField ||
		len(operation.Before.Fields) != 1 || len(operation.After.Fields) != 2 ||
		operation.Before.Fields[0].Name != "id" || operation.After.Fields[1].Name != "published" {
		t.Fatalf("renderer operation = %#v, want target dependency-before AddField", operation)
	}
	want := []string{`ALTER TABLE "alpha_article" ADD COLUMN "published" BOOLEAN NOT NULL`}
	if !reflect.DeepEqual(statements, want) {
		t.Fatalf("statements = %#v, want %#v", statements, want)
	}

	// Both sides of the public renderer port are detached. Mutating the spy's
	// retained request or return slice cannot affect a later materialization or
	// the already-published result.
	renderer.request.Intent.Operations[0].After.Fields[1].Name = "mutated"
	renderer.statements[0] = "mutated"
	if !reflect.DeepEqual(statements, want) {
		t.Fatalf("published statements changed through renderer alias: %#v", statements)
	}
	second := &migrationSQLRendererSpy{statements: append([]string(nil), want...)}
	if _, err := RenderMigrationSQL(context.Background(), loaded, target, second); err != nil {
		t.Fatalf("second RenderMigrationSQL() error = %v", err)
	}
	if got := second.request.Intent.Operations[0].After.Fields[1].Name; got != "published" {
		t.Fatalf("later request retained prior renderer mutation: %q", got)
	}
}

func TestRenderMigrationSQLMaterializesCompoundCrossAppTargetInDefinitionOrder(t *testing.T) {
	t.Parallel()

	target := MigrationKey{App: "blog", Name: "0001_post"}
	renderer := &migrationSQLRendererSpy{statements: []string{
		"CREATE TABLE blog_post",
		"ALTER TABLE blog_post ADD COLUMN published",
		"ALTER TABLE blog_post ADD COLUMN author_id",
	}}
	statements, err := RenderMigrationSQL(
		context.Background(),
		testLoadedDefinitionSet(lifecycleLoadedCompleteIntentDefinitions()),
		target,
		renderer,
	)
	if err != nil {
		t.Fatalf("RenderMigrationSQL() error = %v", err)
	}
	if !reflect.DeepEqual(statements, renderer.statements) || renderer.calls != 1 {
		t.Fatalf("rendered compound SQL = %#v, calls=%d", statements, renderer.calls)
	}
	operations := renderer.request.Intent.Operations
	if len(operations) != 3 || operations[0].OperationIndex != 0 || operations[0].Kind != backend.MigrationCreateModel ||
		operations[1].OperationIndex != 1 || operations[1].Kind != backend.MigrationAddField ||
		operations[2].OperationIndex != 2 || operations[2].Kind != backend.MigrationAddField {
		t.Fatalf("compound operation order = %#v", operations)
	}
	if len(operations[2].Targets) != 1 || operations[2].Targets[0].TargetModel.Name != "author" ||
		operations[2].Targets[0].SourceField.Name != "author" || operations[2].Targets[0].TargetKey.Name != "id" {
		t.Fatalf("cross-app dependency-before target = %#v", operations[2].Targets)
	}
}

func TestRenderMigrationSQLFailurePrecedenceAndRendererValidation(t *testing.T) {
	t.Parallel()

	validTarget := lifecycleAlpha2
	invalidUnrelated := lifecycleTestDefinitions()
	invalidUnrelated = append(invalidUnrelated, Migration{
		App:          "broken",
		Name:         "0001_invalid",
		Dependencies: []MigrationKey{{App: "missing", Name: "0001_absent"}},
	})
	spy := &migrationSQLRendererSpy{statements: []string{"SHOULD NOT RUN"}}
	if _, err := RenderMigrationSQL(context.Background(), testLoadedDefinitionSet(invalidUnrelated), validTarget, spy); err == nil {
		t.Fatal("invalid unrelated definition unexpectedly rendered")
	}
	if spy.calls != 0 {
		t.Fatalf("invalid complete catalog called renderer %d time(s)", spy.calls)
	}

	spy = &migrationSQLRendererSpy{statements: []string{"SHOULD NOT RUN"}}
	missing := MigrationKey{App: validTarget.App, Name: "0002"}
	_, err := RenderMigrationSQL(context.Background(), testLoadedDefinitionSet(lifecycleTestDefinitions()), missing, spy)
	var planning *PlanningError
	if !errors.As(err, &planning) || planning.Category != CategoryPlan || planning.Code != CodeTargetNotFound {
		t.Fatalf("exact miss error = %#v, want plan/target_not_found", err)
	}
	if spy.calls != 0 {
		t.Fatalf("exact miss called renderer %d time(s)", spy.calls)
	}

	loaded := testLoadedDefinitionSet(lifecycleTestDefinitions())
	_, err = RenderMigrationSQL(context.Background(), loaded, validTarget, nil)
	assertMigrationSQLError(t, err, CategorySQLRender, CodeRendererUnavailable, validTarget)
	var typedNil *migrationSQLRendererSpy
	_, err = RenderMigrationSQL(context.Background(), loaded, validTarget, typedNil)
	assertMigrationSQLError(t, err, CategorySQLRender, CodeRendererUnavailable, validTarget)
}

func TestRenderMigrationSQLRejectsContextAndZeroPublicationBeforeRenderer(t *testing.T) {
	t.Parallel()

	target := lifecycleAlpha2
	spy := &migrationSQLRendererSpy{statements: []string{"SHOULD NOT RUN"}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	statements, err := RenderMigrationSQL(canceled, LoadedDefinitionSet{}, target, spy)
	if statements != nil || !errors.Is(err, context.Canceled) || spy.calls != 0 {
		t.Fatalf("canceled entry = %#v, %v, calls=%d; want nil/context.Canceled/0", statements, err, spy.calls)
	}

	statements, err = RenderMigrationSQL(nil, LoadedDefinitionSet{}, target, spy)
	var migrationError *Error
	if statements != nil || !errors.As(err, &migrationError) || migrationError.Category != CategoryExecution ||
		migrationError.Code != CodeOperationFailed || !strings.Contains(err.Error(), "context is nil") || spy.calls != 0 {
		t.Fatalf("nil-context entry = %#v, %#v, calls=%d; want nil execution error/0", statements, err, spy.calls)
	}

	statements, err = RenderMigrationSQL(context.Background(), LoadedDefinitionSet{}, target, spy)
	if statements != nil || !errors.As(err, &migrationError) || migrationError.Category != CategoryState ||
		migrationError.Code != CodeInvalidState || spy.calls != 0 {
		t.Fatalf("zero publication = %#v, %#v, calls=%d; want nil state error/0", statements, err, spy.calls)
	}
}

func TestRenderMigrationSQLRedactsRendererFailuresAndPartialSQL(t *testing.T) {
	t.Parallel()

	target := lifecycleAlpha2
	secret := "postgres://operator:top-secret@example.invalid/database"
	renderer := &migrationSQLRendererSpy{
		statements: []string{"SELECT 'partial-secret'"},
		err:        errors.New(secret),
	}
	statements, err := RenderMigrationSQL(
		context.Background(),
		testLoadedDefinitionSet(lifecycleTestDefinitions()),
		target,
		renderer,
	)
	if statements != nil {
		t.Fatalf("failed render statements = %#v, want nil", statements)
	}
	assertMigrationSQLError(t, err, CategorySQLRender, CodeRenderFailed, target)
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "partial-secret") {
		t.Fatalf("public error leaked renderer data: %q", err)
	}
	if errors.Unwrap(err) != nil || errors.Is(err, renderer.err) {
		t.Fatalf("public error retained raw renderer cause: %#v", err)
	}

	capability := &migrationSQLRendererSpy{
		err: backend.NewCapabilityError("custom_data_operation", secret, errors.New(secret)),
	}
	_, err = RenderMigrationSQL(
		context.Background(),
		testLoadedDefinitionSet(lifecycleTestDefinitions()),
		target,
		capability,
	)
	assertMigrationSQLError(t, err, CategoryCapability, CodeUnsupported, target)
	if strings.Contains(err.Error(), secret) || errors.Unwrap(err) != nil {
		t.Fatalf("capability mapping leaked renderer data: %#v", err)
	}
}

func TestRenderMigrationSQLObservesCancellationAfterRendererReturn(t *testing.T) {
	t.Parallel()

	target := lifecycleAlpha2
	ctx, cancel := context.WithCancel(context.Background())
	renderer := &migrationSQLCancelingRenderer{cancel: cancel}
	statements, err := RenderMigrationSQL(
		ctx,
		testLoadedDefinitionSet(lifecycleTestDefinitions()),
		target,
		renderer,
	)
	if statements != nil || !errors.Is(err, context.Canceled) || renderer.calls != 1 {
		t.Fatalf("cancel-after-render = %#v, %v, calls=%d; want nil/context.Canceled/1", statements, err, renderer.calls)
	}
}

func TestLoadedStateReconstructorChecksCancellationBetweenOperations(t *testing.T) {
	t.Parallel()

	definition := Migration{
		App: "blog", Name: "0001_compound",
		Operations: []Operation{
			CreateModel{AppLabel: "blog", Model: stateModel("base_model", "blog_base")},
			AddField{AppLabel: "blog", ModelName: "base_model", Field: lifecyclePublishedField()},
		},
	}
	reconstructor, err := newLoadedStateReconstructor([]Migration{definition})
	if err != nil {
		t.Fatal(err)
	}
	ctx := newMigrationSQLCheckpointContext(3)
	err = reconstructor.applyLoadedMigrationContext(ctx, newLoadedStateBuilder(), definition, DirectionForward)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("between-operation cancellation error = %v, want context.Canceled", err)
	}
}

func TestValidateRenderedMigrationSQLChecksCancellationDuringScan(t *testing.T) {
	t.Parallel()

	step := PlanStep{Key: MigrationKey{App: "blog", Name: "0002_render_sql"}, Direction: DirectionForward}
	ctx := newMigrationSQLCheckpointContext(2)
	statements, err := validateRenderedMigrationSQL(ctx, step, []string{"SELECT 1", "SELECT 2"}, 2)
	if statements != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("scan cancellation = %#v, %v; want nil/context.Canceled", statements, err)
	}
}

func TestValidateRenderedMigrationSQLResourceBeforeSemanticAndExactLimits(t *testing.T) {
	t.Parallel()

	step := PlanStep{Key: MigrationKey{App: "blog", Name: "0002_render_sql"}, Direction: DirectionForward}
	exactCount := make([]string, migrationSQLMaxStatements)
	for index := range exactCount {
		exactCount[index] = "X"
	}
	if got, err := validateRenderedMigrationSQL(context.Background(), step, exactCount, len(exactCount)); err != nil || len(got) != len(exactCount) {
		t.Fatalf("exact statement limit = (%d, %v), want success", len(got), err)
	}
	oneOverCount := append(exactCount, "") // also semantically invalid; resource must win.
	_, err := validateRenderedMigrationSQL(context.Background(), step, oneOverCount, len(oneOverCount))
	assertMigrationSQLError(t, err, CategorySQLResource, CodeRenderedSQLResourceLimit, step.Key)

	exactBytes := []string{strings.Repeat("X", migrationSQLMaxAggregateBodyBytes)}
	if got, err := validateRenderedMigrationSQL(context.Background(), step, exactBytes, 1); err != nil || len(got) != 1 {
		t.Fatalf("exact aggregate byte limit = (%d, %v), want success", len(got), err)
	}
	oneOverBytes := []string{strings.Repeat("X", migrationSQLMaxAggregateBodyBytes) + ";"}
	_, err = validateRenderedMigrationSQL(context.Background(), step, oneOverBytes, 1)
	assertMigrationSQLError(t, err, CategorySQLResource, CodeRenderedSQLResourceLimit, step.Key)
}

func TestValidateRenderedMigrationSQLRejectsMalformedBodiesAndCardinality(t *testing.T) {
	t.Parallel()

	step := PlanStep{Key: MigrationKey{App: "blog", Name: "0002_render_sql"}, Direction: DirectionForward}
	tests := []struct {
		name       string
		statements []string
		want       int
	}{
		{name: "empty", statements: []string{""}, want: 1},
		{name: "invalid UTF-8", statements: []string{string([]byte{0xff})}, want: 1},
		{name: "leading ASCII whitespace", statements: []string{" SELECT 1"}, want: 1},
		{name: "trailing ASCII whitespace", statements: []string{"SELECT 1\n"}, want: 1},
		{name: "semicolon", statements: []string{"SELECT 1;"}, want: 1},
		{name: "control rune", statements: []string{"SELECT\t1"}, want: 1},
		{name: "cardinality", statements: []string{"SELECT 1"}, want: 2},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := validateRenderedMigrationSQL(context.Background(), step, test.statements, test.want)
			assertMigrationSQLError(t, err, CategorySQLRender, CodeInvalidRenderedSQL, step.Key)
		})
	}
	for _, whitespace := range []byte{' ', '\t', '\n', '\v', '\f', '\r'} {
		for _, body := range []string{string(whitespace) + "SELECT 1", "SELECT 1" + string(whitespace)} {
			_, err := validateRenderedMigrationSQL(context.Background(), step, []string{body}, 1)
			assertMigrationSQLError(t, err, CategorySQLRender, CodeInvalidRenderedSQL, step.Key)
		}
	}
	if got, err := validateRenderedMigrationSQL(context.Background(), step, []string{"SELECT\n1"}, 1); err != nil ||
		!reflect.DeepEqual(got, []string{"SELECT\n1"}) {
		t.Fatalf("internal LF = %#v, %v; want accepted", got, err)
	}
}

func TestRenderMigrationSQLEmptyIntentReturnsNonNilEmptyResult(t *testing.T) {
	t.Parallel()

	target := MigrationKey{App: "empty", Name: "0001_noop"}
	renderer := &migrationSQLRendererSpy{statements: []string{}}
	statements, err := RenderMigrationSQL(
		context.Background(),
		testLoadedDefinitionSet([]Migration{{App: target.App, Name: target.Name}}),
		target,
		renderer,
	)
	if err != nil {
		t.Fatalf("RenderMigrationSQL(empty) error = %v", err)
	}
	if statements == nil || len(statements) != 0 {
		t.Fatalf("RenderMigrationSQL(empty) = %#v, want non-nil empty", statements)
	}
}

type migrationSQLRendererSpy struct {
	calls      int
	request    backend.ForwardMigrationSQLRequest
	statements []string
	err        error
}

type migrationSQLCancelingRenderer struct {
	calls  int
	cancel context.CancelFunc
}

func (renderer *migrationSQLCancelingRenderer) RenderForwardMigrationSQL(
	_ context.Context,
	_ backend.ForwardMigrationSQLRequest,
) ([]string, error) {
	renderer.calls++
	renderer.cancel()
	return []string{"SELECT 1"}, nil
}

type migrationSQLCheckpointContext struct {
	context.Context
	cancel   context.CancelFunc
	cancelAt int
	calls    int
}

func newMigrationSQLCheckpointContext(cancelAt int) *migrationSQLCheckpointContext {
	ctx, cancel := context.WithCancel(context.Background())
	return &migrationSQLCheckpointContext{Context: ctx, cancel: cancel, cancelAt: cancelAt}
}

func (ctx *migrationSQLCheckpointContext) Err() error {
	ctx.calls++
	if ctx.calls == ctx.cancelAt {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func (renderer *migrationSQLRendererSpy) RenderForwardMigrationSQL(
	_ context.Context,
	request backend.ForwardMigrationSQLRequest,
) ([]string, error) {
	renderer.calls++
	renderer.request = request
	return renderer.statements, renderer.err
}

func assertMigrationSQLError(
	t *testing.T,
	err error,
	category ErrorCategory,
	code ErrorCode,
	target MigrationKey,
) *MigrationSQLError {
	t.Helper()
	var sqlError *MigrationSQLError
	if !errors.As(err, &sqlError) {
		t.Fatalf("error = %T %v, want *MigrationSQLError", err, err)
	}
	if sqlError.Category != category || sqlError.Code != code {
		t.Fatalf("SQL error = %#v, want %s/%s for %s.%s", sqlError, category, code, target.App, target.Name)
	}
	if want := string(category) + "/" + string(code); sqlError.Error() != want {
		t.Fatalf("SQL error text = %q, want %q", sqlError.Error(), want)
	}
	errorType := reflect.TypeOf(*sqlError)
	if errorType.NumField() != 2 || errorType.Field(0).Name != "Category" || errorType.Field(1).Name != "Code" {
		t.Fatalf("MigrationSQLError fields = %v, want only Category/Code", errorType)
	}
	if errors.Unwrap(sqlError) != nil {
		t.Fatalf("MigrationSQLError unwrap = %v, want nil", errors.Unwrap(sqlError))
	}
	return sqlError
}
