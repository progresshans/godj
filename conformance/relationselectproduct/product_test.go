package relationselectproduct

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/internal/relationstate"
	"github.com/progresshans/godj/conformance/relationfixture/blog"
	"github.com/progresshans/godj/conformance/relationfixture/project"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

func TestObserveExecutesExactREL009REL010AndREL011Cases(t *testing.T) {
	t.Parallel()

	got, err := observeCases(context.Background(), defaultFixtureConfig())
	if err != nil {
		t.Fatal(err)
	}
	wantRequired := []PostRelatedRow{
		{PostID: 10, Name: stringPointer("Ada")},
		{PostID: 11, Name: stringPointer("Ada")},
		{PostID: 12, Name: stringPointer("Cleo")},
	}
	wantNullable := []PostRelatedRow{
		{PostID: 10, Name: stringPointer("Bob")},
		{PostID: 11},
		{PostID: 12, Name: stringPointer("Bob")},
	}
	wantPlainMetrics := QueryMetrics{
		QueryCount: 4, StatementKinds: []string{"SELECT", "SELECT", "SELECT", "SELECT"},
		JoinKinds: []string{}, AccessExtraQueries: 3,
	}
	wantRequiredMetrics := QueryMetrics{
		QueryCount: 1, StatementKinds: []string{"SELECT"}, JoinKinds: []string{"INNER"}, InnerJoinCount: 1,
	}
	wantNullableMetrics := QueryMetrics{
		QueryCount: 1, StatementKinds: []string{"SELECT"}, JoinKinds: []string{"LEFT_OUTER"}, LeftOuterJoinCount: 1,
	}
	wantInvalidMetrics := QueryMetrics{StatementKinds: []string{}, JoinKinds: []string{}}
	if !equalPostRelatedRows(got.Required.Plain, wantRequired) || !equalPostRelatedRows(got.Required.Eager, wantRequired) ||
		!equalPostRelatedRows(got.Nullable.Rows, wantNullable) {
		t.Fatalf("relation select results = required %#v nullable %#v", got.Required, got.Nullable.Rows)
	}
	if !reflect.DeepEqual(got.Required.PlainMetrics, wantPlainMetrics) ||
		!reflect.DeepEqual(got.Required.EagerMetrics, wantRequiredMetrics) ||
		!reflect.DeepEqual(got.Nullable.Metrics, wantNullableMetrics) ||
		!reflect.DeepEqual(got.Invalid.Metrics, wantInvalidMetrics) {
		t.Fatalf("relation select metrics = required %#v nullable %#v invalid %#v", got.Required, got.Nullable.Metrics, got.Invalid.Metrics)
	}
	var queryError *query.Error
	if !errors.As(got.Invalid.Err, &queryError) || queryError.Category != query.CategoryField ||
		queryError.Code != query.CodeInvalidRelatedPath || queryError.Field != "posts" {
		t.Fatalf("REL-011 error = %v, want field_error/invalid_related_path posts", got.Invalid.Err)
	}
	reviewer := int64(2)
	wantState := relationstate.DatabaseState{
		Authors: []relationstate.AuthorRow{{ID: 1, Name: "Ada"}, {ID: 2, Name: "Bob"}, {ID: 3, Name: "Cleo"}},
		Posts: []relationstate.PostRow{
			{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewer},
			{ID: 11, Title: "Beta", AuthorID: 1},
			{ID: 12, Title: "Gamma", AuthorID: 3, ReviewerID: &reviewer},
		},
	}
	if !reflect.DeepEqual(got.DBState, wantState) {
		t.Fatalf("database state = %#v, want %#v", got.DBState, wantState)
	}
}

func TestREL009REL010REL011FalseGreenMutations(t *testing.T) {
	t.Parallel()

	base, err := observeCases(context.Background(), defaultFixtureConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*fixtureConfig)
		check  func(*testing.T, selectCases)
	}{
		{
			name: "required target membership",
			mutate: func(config *fixtureConfig) {
				config.posts[0].AuthorID = 3
			},
			check: func(t *testing.T, got selectCases) {
				if got.Required.Eager[0].Name == nil || *got.Required.Eager[0].Name != "Cleo" {
					t.Fatalf("required membership mutation = %#v", got.Required.Eager)
				}
			},
		},
		{
			name: "nullable absence",
			mutate: func(config *fixtureConfig) {
				reviewer := int64(2)
				config.posts[1].ReviewerID = &reviewer
			},
			check: func(t *testing.T, got selectCases) {
				if got.Nullable.Rows[1].Name == nil || *got.Nullable.Rows[1].Name != "Bob" {
					t.Fatalf("nullable mutation = %#v", got.Nullable.Rows)
				}
			},
		},
		{
			name: "root ordering",
			mutate: func(config *fixtureConfig) {
				config.postsDescending = true
			},
			check: func(t *testing.T, got selectCases) {
				if got.Required.Eager[0].PostID != 12 || got.Nullable.Rows[2].PostID != 10 {
					t.Fatalf("descending mutation = required %#v nullable %#v", got.Required.Eager, got.Nullable.Rows)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			got, err := observeCases(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, got)
			if reflect.DeepEqual(got, base) {
				t.Fatal("owned mutation left the complete observation unchanged")
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*fixtureConfig)
	}{
		{name: "second eager query", mutate: func(config *fixtureConfig) { config.repeatRequiredEager = true }},
		{name: "required cold access", mutate: func(config *fixtureConfig) { config.forceRequiredCold = true }},
		{name: "nullable cold access", mutate: func(config *fixtureConfig) { config.forceNullableCold = true }},
		{name: "invalid path query", mutate: func(config *fixtureConfig) { config.allowInvalidPathQuery = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			if _, err := observeCases(context.Background(), config); err == nil {
				t.Fatal("trace mutation published a successful select-related observation")
			}
		})
	}
}

func TestSelectObserversDoNotExecuteSiblingCases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	config := defaultFixtureConfig()
	config.forceNullableCold = true
	config.allowInvalidPathQuery = true
	if _, err := observe(ctx, config, observeRequired); err != nil {
		t.Fatalf("required observer executed a poisoned sibling: %v", err)
	}
	config = defaultFixtureConfig()
	config.repeatRequiredEager = true
	config.forceRequiredCold = true
	config.allowInvalidPathQuery = true
	if _, err := observe(ctx, config, observeNullable); err != nil {
		t.Fatalf("nullable observer executed a poisoned sibling: %v", err)
	}
	config.forceNullableCold = true
	config.allowInvalidPathQuery = false
	if got, err := observe(ctx, config, observeInvalid); err != nil || got.Result.Metrics.QueryCount != 0 {
		t.Fatalf("invalid observer executed a sibling query: metrics=%+v error=%v", got.Result.Metrics, err)
	}
}

func TestTypedAndDynamicSelectRelatedConvergeOnTheSamePlan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "godj-select-related-convergence-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	config := defaultFixtureConfig()
	if err := relationstate.Provision(ctx, backend, "select-related", config.authors, config.posts); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingQueryer{backend: backend}
	source := blog.PostObjects.Using(recorder).OrderBy(blog.PostFields.ID.Asc())
	selected := objects.BlogPost.SelectRelated(source)
	typed := selected.Author()
	dynamic, err := selected.ParseDynamic("author")
	if err != nil {
		t.Fatal(err)
	}
	if recorder.mark() != 0 {
		t.Fatalf("typed/dynamic construction issued %d queries", recorder.mark())
	}
	if _, err := typed.All(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := dynamic.All(ctx); err != nil {
		t.Fatal(err)
	}
	records := recorder.recordsSince(0)
	if len(records) != 2 || !records[0].plan.Equal(records[1].plan) {
		t.Fatalf("typed/dynamic execution plans = %#v, want two equal plans", records)
	}
	beforeInvalid := recorder.mark()
	if _, err := selected.ParseDynamic("posts"); !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidRelatedPath}) {
		t.Fatalf("reverse ParseDynamic error = %v", err)
	}
	if recorder.mark() != beforeInvalid {
		t.Fatalf("reverse ParseDynamic issued %d queries", recorder.mark()-beforeInvalid)
	}
}

func stringPointer(value string) *string {
	return &value
}

// observeCases is the explicit unit-test suite. Each case calls its own
// production operation with a fresh database and a separately read state.
type selectCases struct {
	Required RequiredObservation
	Nullable NullableObservation
	Invalid  InvalidObservation
	DBState  relationstate.DatabaseState
}

func observeCases(ctx context.Context, config fixtureConfig) (selectCases, error) {
	required, err := observe(ctx, config, observeRequired)
	if err != nil {
		return selectCases{}, err
	}
	nullable, err := observe(ctx, config, observeNullable)
	if err != nil {
		return selectCases{}, err
	}
	invalid, err := observe(ctx, config, observeInvalid)
	if err != nil {
		return selectCases{}, err
	}
	if !reflect.DeepEqual(required.DBState, nullable.DBState) {
		return selectCases{}, errors.New("independent observer fixtures have different actual final states")
	}
	if !reflect.DeepEqual(required.DBState, invalid.DBState) {
		return selectCases{}, errors.New("independent observer fixtures have different actual final states")
	}
	return selectCases{Required: required.Result, Nullable: nullable.Result, Invalid: invalid.Result, DBState: required.DBState}, nil
}
