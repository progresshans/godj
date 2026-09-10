package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestPostgresMigrationSQLRendererMatchesExecutionCompilers(t *testing.T) {
	t.Parallel()

	before := postgresMigrationTestPostModel(false)
	after := before.Clone()
	after.Fields = append(after.Fields, ir.Field{
		Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, MaxLength: 120, Nullable: true,
	})
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{OperationIndex: 0, Kind: migrationbackend.MigrationCreateModel, After: before},
		{OperationIndex: 1, Kind: migrationbackend.MigrationAddField, Before: before, After: after},
	}}
	request := migrationbackend.ForwardMigrationSQLRequest{
		App: "blog", Name: "0001_post", Intent: intent,
	}
	renderer := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"})
	statements, err := renderer.RenderForwardMigrationSQL(context.Background(), request)
	if err != nil {
		t.Fatalf("RenderForwardMigrationSQL() error = %v", err)
	}
	create, err := compilePostgresMigrationCreateModel("product_schema", before, nil)
	if err != nil {
		t.Fatal(err)
	}
	add, err := compilePostgresMigrationAddField("product_schema", before, after.Fields[len(after.Fields)-1], nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{create, add}
	if !reflect.DeepEqual(statements, want) {
		t.Fatalf("rendered SQL = %#v, want compiler SQL %#v", statements, want)
	}
	if !reflect.DeepEqual(request.Intent, intent) {
		t.Fatal("renderer mutated caller-owned migration intent")
	}
	for _, statement := range statements {
		if !strings.Contains(statement, `"product_schema".`) {
			t.Fatalf("statement is not explicitly schema-qualified: %q", statement)
		}
	}
}

func TestPostgresMigrationSQLRendererPublicConfigurationIsSchemaOnly(t *testing.T) {
	t.Parallel()

	config := reflect.TypeOf(MigrationSQLConfig{})
	if config.NumField() != 1 || config.Field(0).Name != "Schema" || config.Field(0).Type.Kind() != reflect.String {
		t.Fatalf("MigrationSQLConfig shape = %v, want exactly Schema string", config)
	}
	constructor := reflect.TypeOf(NewMigrationSQLRenderer)
	wantResult := reflect.TypeOf((*migrationbackend.MigrationSQLRenderer)(nil)).Elem()
	if constructor.NumIn() != 1 || constructor.In(0) != config || constructor.NumOut() != 1 || constructor.Out(0) != wantResult {
		t.Fatalf("NewMigrationSQLRenderer type = %s, want func(MigrationSQLConfig) %s", constructor, wantResult)
	}
	renderer := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"})
	if renderer == nil || reflect.TypeOf(renderer).Kind() == reflect.Pointer {
		t.Fatalf("NewMigrationSQLRenderer() dynamic type = %T, want non-nil immutable value", renderer)
	}
}

func TestPostgresMigrationSQLRendererProjectsLogicalDefaultWithoutDDLDefault(t *testing.T) {
	t.Parallel()

	before := postgresMigrationTestPostModel(false)
	logicalDefault := &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false}
	after := before.Clone()
	after.Fields = append(after.Fields, ir.Field{
		Name: "featured", GoName: "Featured", Column: "featured", Kind: ir.FieldBoolean, Default: logicalDefault,
	})
	request := migrationbackend.ForwardMigrationSQLRequest{
		App: "blog", Name: "0002_featured",
		Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationAddField,
			Before:         before,
			After:          after,
		}}},
	}
	statements, err := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"}).RenderForwardMigrationSQL(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := compilePostgresMigrationAddField("product_schema", before, after.Fields[len(after.Fields)-1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(statements, []string{want}) || strings.Contains(strings.ToUpper(statements[0]), " DEFAULT ") {
		t.Fatalf("logical-default AddField SQL = %#v, want compiler projection without DDL DEFAULT", statements)
	}
}

func TestPostgresMigrationSQLRendererPreservesLoaderIdentityAuthority(t *testing.T) {
	t.Parallel()

	model := postgresMigrationTestAuthorModel()
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationCreateModel,
		After:          model,
	}}}
	requests := []migrationbackend.ForwardMigrationSQLRequest{
		{App: strings.Repeat("a", postgresMigrationRecorderMaxChars+1), Name: "0001_author", Intent: intent},
		{App: "authors", Name: strings.Repeat("n", postgresMigrationRecorderMaxChars+1), Intent: intent},
		{App: "authors", Name: strings.Repeat("😀", postgresMigrationRecorderMaxChars+1), Intent: intent},
		{App: "authors", Name: "0001\x00still-loader-valid", Intent: intent},
	}
	renderer := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"})
	for index, request := range requests {
		statements, err := renderer.RenderForwardMigrationSQL(context.Background(), request)
		if err != nil || len(statements) != 1 {
			t.Fatalf("case %d loader-valid identity render = %#v, %v; want one statement", index, statements, err)
		}
		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: request.App, Name: request.Name},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		if _, err := newPostgresMigrationSchema(transition, request.Intent); err == nil {
			t.Fatalf("case %d execution schema unexpectedly accepted recorder-invalid identity", index)
		}
	}
}

func TestPostgresMigrationSQLRendererInvalidConfigIsClosedAndRedacted(t *testing.T) {
	t.Parallel()

	raw := "Bad Schema secret-value"
	renderer := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: raw})
	_, err := renderer.RenderForwardMigrationSQL(
		context.Background(),
		migrationbackend.ForwardMigrationSQLRequest{
			App: "blog", Name: "0001_noop",
			Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}},
		},
	)
	if err == nil || strings.Contains(err.Error(), raw) || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("invalid config error = %q, want generic redacted failure", err)
	}
}

func TestPostgresMigrationSQLRendererRejectsDestructiveOperation(t *testing.T) {
	t.Parallel()

	model := postgresMigrationTestAuthorModel()
	for _, test := range []struct {
		name string
		kind migrationbackend.MigrationOperationKind
	}{
		{name: "delete model", kind: migrationbackend.MigrationDeleteModel},
		{name: "remove field", kind: migrationbackend.MigrationRemoveField},
		{name: "unknown", kind: migrationbackend.MigrationOperationKind(255)},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			statements, err := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"}).RenderForwardMigrationSQL(
				context.Background(),
				migrationbackend.ForwardMigrationSQLRequest{
					App: "authors", Name: "0002_unsupported",
					Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
						OperationIndex: 0,
						Kind:           test.kind,
						Before:         model,
					}}},
				},
			)
			if statements != nil || err == nil || !migrationbackend.IsCapabilityError(err) {
				t.Fatalf("kind %d render = %#v, %#v; want nil/CapabilityError", test.kind, statements, err)
			}
		})
	}
}

func TestPostgresMigrationSQLRendererForeignKeyAddIsOneDeterministicStatement(t *testing.T) {
	t.Parallel()

	author := postgresMigrationTestAuthorModel()
	before := postgresMigrationTestPostModel(false)
	after := postgresMigrationTestPostModel(true)
	field := after.Fields[len(after.Fields)-1]
	target := postgresMigrationTestTarget(field, author)
	request := migrationbackend.ForwardMigrationSQLRequest{
		App: "blog", Name: "0002_author",
		Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationAddField,
			Before:         before,
			After:          after,
			Targets:        []migrationbackend.MigrationTarget{target},
		}}},
	}
	renderer := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"})
	want, err := renderer.RenderForwardMigrationSQL(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	compilerSQL, err := compilePostgresMigrationAddField("product_schema", before, field, &target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, []string{compilerSQL}) ||
		!strings.Contains(want[0], "ADD COLUMN") || !strings.Contains(want[0], ", ADD CONSTRAINT") {
		t.Fatalf("ForeignKey Add SQL = %#v, want one compiler statement %q", want, compilerSQL)
	}

	const parallel = 16
	results := make(chan []string, parallel)
	errors := make(chan error, parallel)
	var group sync.WaitGroup
	for index := 0; index < parallel; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			got, renderErr := renderer.RenderForwardMigrationSQL(context.Background(), request)
			results <- got
			errors <- renderErr
		}()
	}
	group.Wait()
	close(results)
	close(errors)
	for renderErr := range errors {
		if renderErr != nil {
			t.Fatalf("parallel render error = %v", renderErr)
		}
	}
	for got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("parallel render = %#v, want %#v", got, want)
		}
	}
	want[0] = "mutated"
	again, err := renderer.RenderForwardMigrationSQL(context.Background(), request)
	if err != nil || again[0] == "mutated" {
		t.Fatalf("repeat render retained returned-slice mutation: %#v, %v", again, err)
	}
}

func TestPostgresMigrationSQLRendererRejectsOnlyLoaderInvalidIdentity(t *testing.T) {
	t.Parallel()

	renderer := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"})
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}}
	tests := []migrationbackend.ForwardMigrationSQLRequest{
		{App: "", Name: "0001", Intent: intent},
		{App: "Blog", Name: "0001", Intent: intent},
		{App: "blog", Name: "", Intent: intent},
		{App: "blog", Name: string([]byte{0xff}), Intent: intent},
	}
	for index, request := range tests {
		if statements, err := renderer.RenderForwardMigrationSQL(context.Background(), request); err == nil || statements != nil {
			t.Fatalf("invalid identity case %d = %#v, %v; want nil/error", index, statements, err)
		}
	}
}

func TestPostgresMigrationSQLRendererChecksCancellationAtBoundedCheckpoints(t *testing.T) {
	t.Parallel()

	base := postgresMigrationTestPostModel(false)
	withSummary := base.Clone()
	withSummary.Fields = append(withSummary.Fields, ir.Field{
		Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, MaxLength: 120, Nullable: true,
	})
	request := migrationbackend.ForwardMigrationSQLRequest{
		App: "blog", Name: "0002_summary",
		Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
			{OperationIndex: 0, Kind: migrationbackend.MigrationCreateModel, After: base},
			{OperationIndex: 1, Kind: migrationbackend.MigrationAddField, Before: base, After: withSummary},
		}},
	}
	tests := []struct {
		name     string
		cancelAt int
	}{
		{name: "entry", cancelAt: 1},
		{name: "after preparation", cancelAt: 2},
		{name: "between operations", cancelAt: 4},
		{name: "after final compilation", cancelAt: 5},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := newPostgresMigrationSQLCheckpointContext(test.cancelAt)
			statements, err := NewMigrationSQLRenderer(MigrationSQLConfig{Schema: "product_schema"}).RenderForwardMigrationSQL(ctx, request)
			if statements != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("render = %#v, %v; want nil/context.Canceled", statements, err)
			}
		})
	}
}

type postgresMigrationSQLCheckpointContext struct {
	context.Context
	cancel   context.CancelFunc
	cancelAt int
	calls    int
}

func newPostgresMigrationSQLCheckpointContext(cancelAt int) *postgresMigrationSQLCheckpointContext {
	ctx, cancel := context.WithCancel(context.Background())
	return &postgresMigrationSQLCheckpointContext{Context: ctx, cancel: cancel, cancelAt: cancelAt}
}

func (ctx *postgresMigrationSQLCheckpointContext) Err() error {
	ctx.calls++
	if ctx.calls == ctx.cancelAt {
		ctx.cancel()
	}
	return ctx.Context.Err()
}
