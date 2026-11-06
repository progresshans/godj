package relationqueryproduct

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/internal/relationstate"
	"github.com/progresshans/godj/conformance/relationfixture/blog"
	"github.com/progresshans/godj/conformance/relationfixture/project"
	"github.com/progresshans/godj/orm"
)

func TestObserveExecutesExactREL004CasesAndDatabaseState(t *testing.T) {
	t.Parallel()

	got, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantMetric := relationstate.QueryMetrics{
		QueryCount:         1,
		StatementKinds:     []string{"SELECT"},
		JoinKinds:          []string{"INNER"},
		InnerJoinCount:     1,
		LeftOuterJoinCount: 0,
	}
	wantConstruction := relationstate.QueryMetrics{StatementKinds: []string{}, JoinKinds: []string{}}
	wantCases := []CaseObservation{
		{Name: "one_predicate", PostIDs: []int64{10, 11}, Construction: wantConstruction, Evaluation: wantMetric},
		{Name: "two_predicates", PostIDs: []int64{10, 11}, Construction: wantConstruction, Evaluation: wantMetric},
	}
	if !reflect.DeepEqual(got.Cases, wantCases) {
		t.Fatalf("cases = %#v, want %#v", got.Cases, wantCases)
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

func TestObservationChangesForEachOwnedREL004Mutation(t *testing.T) {
	t.Parallel()

	base, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		mutate      func(*fixtureConfig)
		wantPostIDs [][]int64
	}{
		{
			name: "author name",
			mutate: func(config *fixtureConfig) {
				config.authors[0].Name = "Adele"
			},
			wantPostIDs: [][]int64{{}, {}},
		},
		{
			name: "foreign key identity",
			mutate: func(config *fixtureConfig) {
				config.posts[2].AuthorID = 1
			},
			wantPostIDs: [][]int64{{10, 11, 12}, {10, 11, 12}},
		},
		{
			name: "terminal target field",
			mutate: func(config *fixtureConfig) {
				config.firstPredicateTerminalID = true
			},
			wantPostIDs: [][]int64{{}, {}},
		},
		{
			name: "row ordering",
			mutate: func(config *fixtureConfig) {
				config.descending = true
			},
			wantPostIDs: [][]int64{{11, 10}, {11, 10}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			mutated, err := observe(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			if len(mutated.Cases) != len(test.wantPostIDs) {
				t.Fatalf("mutated case count = %d, want %d", len(mutated.Cases), len(test.wantPostIDs))
			}
			for index, want := range test.wantPostIDs {
				if !reflect.DeepEqual(mutated.Cases[index].PostIDs, want) {
					t.Fatalf("mutated case %d IDs = %v, want %v", index, mutated.Cases[index].PostIDs, want)
				}
				if reflect.DeepEqual(mutated.Cases[index].PostIDs, base.Cases[index].PostIDs) {
					t.Fatalf("mutated case %d IDs remained %v; DBState differences cannot satisfy the gate", index, mutated.Cases[index].PostIDs)
				}
			}
		})
	}
}

func TestGeneratedTypedAndDynamicRelationSelectorsBuildEqualPlans(t *testing.T) {
	t.Parallel()

	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	typed := blog.PostObjects.Using(nil).
		Filter(relations.BlogPost.Author.Name.Exact("Ada"), relations.BlogPost.Author.ID.Exact(1)).
		OrderBy(blog.PostFields.ID.Asc()).
		Plan()
	dynamicPredicates, err := relations.BlogPost.ParseDynamic(nil, []orm.LookupInput{
		{Key: "author__name", Value: "Ada"},
		{Key: "author__id", Value: int64(1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	dynamic := blog.PostObjects.Using(nil).
		Filter(dynamicPredicates...).
		OrderBy(blog.PostFields.ID.Asc()).
		Plan()
	if !typed.Equal(dynamic) {
		t.Fatalf("typed plan %#v differs from dynamic plan %#v", typed, dynamic)
	}
}
