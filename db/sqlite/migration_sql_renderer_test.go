package sqlite

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

func TestSQLiteMigrationSQLRendererMatchesExecutionCompilers(t *testing.T) {
	t.Parallel()

	target, source, sourceField := sqliteRelationTestModels()
	intent := sqliteRelationApplyIntent(target, source, sourceField)
	request := migrationbackend.ForwardMigrationSQLRequest{
		App: "news", Name: "0001_models", Intent: intent,
	}
	statements, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(context.Background(), request)
	if err != nil {
		t.Fatalf("RenderForwardMigrationSQL() error = %v", err)
	}
	targetSQL, err := compileSQLiteRelationCreateModel(target, nil)
	if err != nil {
		t.Fatal(err)
	}
	sourceSQL, err := compileSQLiteRelationCreateModel(source, []migrationbackend.MigrationTarget{intent.Operations[1].Targets[0]})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{targetSQL, sourceSQL}
	if !reflect.DeepEqual(statements, want) {
		t.Fatalf("rendered SQL = %#v, want compiler SQL %#v", statements, want)
	}
	if !reflect.DeepEqual(request.Intent, intent) {
		t.Fatal("renderer mutated caller-owned migration intent")
	}
}

func TestSQLiteMigrationSQLRendererConstructorIsZeroInputNonNilValue(t *testing.T) {
	t.Parallel()

	constructor := reflect.TypeOf(NewMigrationSQLRenderer)
	wantResult := reflect.TypeOf((*migrationbackend.MigrationSQLRenderer)(nil)).Elem()
	if constructor.NumIn() != 0 || constructor.NumOut() != 1 || constructor.Out(0) != wantResult {
		t.Fatalf("NewMigrationSQLRenderer type = %s, want func() %s", constructor, wantResult)
	}
	renderer := NewMigrationSQLRenderer()
	if renderer == nil || reflect.TypeOf(renderer).Kind() == reflect.Pointer {
		t.Fatalf("NewMigrationSQLRenderer() dynamic type = %T, want non-nil immutable value", renderer)
	}
}

func TestSQLiteMigrationSQLRendererScalarAddAndEmptyIntent(t *testing.T) {
	t.Parallel()

	before := ir.Model{
		Name: "article", GoName: "Article", DBTable: "blog_article",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
	after := before.Clone()
	after.Fields = append(after.Fields, ir.Field{
		Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, MaxLength: 120, Nullable: true,
	})
	request := migrationbackend.ForwardMigrationSQLRequest{
		App: "blog", Name: "0002_summary",
		Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationAddField,
			Before:         before,
			After:          after,
		}}},
	}
	statements, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := compileMigrationAddField(before, after.Fields[1])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(statements, []string{want}) {
		t.Fatalf("scalar AddField SQL = %#v, want %q", statements, want)
	}

	empty, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(
		context.Background(),
		migrationbackend.ForwardMigrationSQLRequest{
			App: "blog", Name: "0003_noop",
			Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{}},
		},
	)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty render = %#v, %v; want non-nil empty", empty, err)
	}
}

func TestSQLiteMigrationSQLRendererRejectsDestructiveOperation(t *testing.T) {
	t.Parallel()

	target, _, _ := sqliteRelationTestModels()
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
			statements, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(
				context.Background(),
				migrationbackend.ForwardMigrationSQLRequest{
					App: "news", Name: "0002_unsupported",
					Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
						OperationIndex: 0,
						Kind:           test.kind,
						Before:         target,
					}}},
				},
			)
			if statements != nil || err == nil || !migrationbackend.IsCapabilityError(err) {
				t.Fatalf("kind %d render = %#v, %#v; want nil/CapabilityError", test.kind, statements, err)
			}
		})
	}
}

func TestSQLiteMigrationSQLRendererExpandsChangedOnlyForeignKeyTarget(t *testing.T) {
	t.Parallel()

	target, before, existing := sqliteRelationTestModels()
	added := existing.Clone()
	added.Name = "editor"
	added.GoName = "Editor"
	added.Column = "editor_id"
	added.Nullable = true
	added.Relation.Reverse.Name = "edited_articles"
	after := before.Clone()
	after.Fields = append(after.Fields, added)
	changedTarget := migrationbackend.MigrationTarget{
		SourceField: added,
		TargetModel: target,
		TargetKey:   target.Fields[0],
	}
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationAddField,
		Before:         before,
		After:          after,
		Targets:        []migrationbackend.MigrationTarget{changedTarget},
	}}}
	statements, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(
		context.Background(),
		migrationbackend.ForwardMigrationSQLRequest{App: "news", Name: "0002_editor", Intent: intent},
	)
	if err != nil {
		t.Fatal(err)
	}
	want, err := compileSQLiteRelationAddField(before, added, []migrationbackend.MigrationTarget{
		{SourceField: existing, TargetModel: target, TargetKey: target.Fields[0]},
		changedTarget,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(statements, []string{want}) {
		t.Fatalf("ForeignKey Add SQL = %#v, want %q", statements, want)
	}
	if len(intent.Operations[0].Targets) != 1 || !reflect.DeepEqual(intent.Operations[0].Targets[0], changedTarget) {
		t.Fatal("renderer leaked sealed target expansion into caller input")
	}
}

func TestSQLiteMigrationSQLRendererProjectsLivePreflightFieldsDeterministically(t *testing.T) {
	t.Parallel()

	base := ir.Model{
		Name: "article", GoName: "Article", DBTable: "blog_article",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
	required := ir.Field{Name: "slug", GoName: "Slug", Column: "slug", Kind: ir.FieldChar, MaxLength: 64}
	withRequired := base.Clone()
	withRequired.Fields = append(withRequired.Fields, required)
	logicalDefault := &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false}
	featured := ir.Field{Name: "featured", GoName: "Featured", Column: "featured", Kind: ir.FieldBoolean, Default: logicalDefault}
	withDefault := withRequired.Clone()
	withDefault.Fields = append(withDefault.Fields, featured)
	request := migrationbackend.ForwardMigrationSQLRequest{
		App: "blog", Name: "0002_live_preflight",
		Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
			{OperationIndex: 0, Kind: migrationbackend.MigrationAddField, Before: base, After: withRequired},
			{OperationIndex: 1, Kind: migrationbackend.MigrationAddField, Before: withRequired, After: withDefault},
		}},
	}
	renderer := NewMigrationSQLRenderer()
	want, err := renderer.RenderForwardMigrationSQL(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	requiredSQL, err := compileMigrationAddField(base, required)
	if err != nil {
		t.Fatal(err)
	}
	defaultSQL, err := compileMigrationAddField(withRequired, featured)
	if err != nil {
		t.Fatal(err)
	}
	compilerSQL := []string{requiredSQL, defaultSQL}
	if !reflect.DeepEqual(want, compilerSQL) || !strings.Contains(want[0], "NOT NULL") ||
		strings.Contains(strings.Join(want, "\n"), "DEFAULT") {
		t.Fatalf("live-preflight projection SQL = %#v, want compiler SQL %#v without DDL DEFAULT", want, compilerSQL)
	}

	// One immutable renderer value is repeatable and safe for parallel use.
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

func TestSQLiteMigrationSQLRendererPreservesLoaderIdentityAuthority(t *testing.T) {
	t.Parallel()

	model := ir.Model{
		Name: "article", GoName: "Article", DBTable: "blog_article",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationCreateModel,
		After:          model,
	}}}
	renderer := NewMigrationSQLRenderer()
	valid := []migrationbackend.ForwardMigrationSQLRequest{
		{App: strings.Repeat("a", 256), Name: "0001_article", Intent: intent},
		{App: "blog", Name: strings.Repeat("n", 256), Intent: intent},
		{App: "blog", Name: strings.Repeat("😀", 256), Intent: intent},
		{App: "blog", Name: "0001\x00still-loader-valid", Intent: intent},
	}
	for index, request := range valid {
		statements, err := renderer.RenderForwardMigrationSQL(context.Background(), request)
		if err != nil || len(statements) != 1 {
			t.Fatalf("case %d loader-valid identity render = %#v, %v; want one statement", index, statements, err)
		}
	}

	invalid := []migrationbackend.ForwardMigrationSQLRequest{
		{App: "", Name: "0001", Intent: intent},
		{App: "Blog", Name: "0001", Intent: intent},
		{App: "blog", Name: "", Intent: intent},
		{App: "blog", Name: string([]byte{0xff}), Intent: intent},
	}
	for index, request := range invalid {
		if statements, err := renderer.RenderForwardMigrationSQL(context.Background(), request); err == nil || statements != nil {
			t.Fatalf("invalid identity case %d = %#v, %v; want nil/error", index, statements, err)
		}
	}
}

func TestSQLiteMigrationSQLRendererChecksCancellationAtBoundedCheckpoints(t *testing.T) {
	t.Parallel()

	base := ir.Model{
		Name: "article", GoName: "Article", DBTable: "blog_article",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
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
		{name: "after seal", cancelAt: 2},
		{name: "between operations", cancelAt: 4},
		{name: "after final compilation", cancelAt: 5},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := newSQLiteMigrationSQLCheckpointContext(test.cancelAt)
			statements, err := NewMigrationSQLRenderer().RenderForwardMigrationSQL(ctx, request)
			if statements != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("render = %#v, %v; want nil/context.Canceled", statements, err)
			}
		})
	}
}

type sqliteMigrationSQLCheckpointContext struct {
	context.Context
	cancel   context.CancelFunc
	cancelAt int
	calls    int
}

func newSQLiteMigrationSQLCheckpointContext(cancelAt int) *sqliteMigrationSQLCheckpointContext {
	ctx, cancel := context.WithCancel(context.Background())
	return &sqliteMigrationSQLCheckpointContext{Context: ctx, cancel: cancel, cancelAt: cancelAt}
}

func (ctx *sqliteMigrationSQLCheckpointContext) Err() error {
	ctx.calls++
	if ctx.calls == ctx.cancelAt {
		ctx.cancel()
	}
	return ctx.Context.Err()
}
