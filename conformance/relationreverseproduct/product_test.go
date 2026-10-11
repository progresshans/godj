package relationreverseproduct

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/internal/relationstate"
)

func TestObserveExecutesExactREL005AccessorLookupAndDatabaseState(t *testing.T) {
	t.Parallel()

	got, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantAccessor := relationstate.QueryMetrics{
		QueryCount:     1,
		StatementKinds: []string{"SELECT"},
		JoinKinds:      []string{},
	}
	wantLookup := relationstate.QueryMetrics{
		QueryCount:         1,
		StatementKinds:     []string{"SELECT"},
		JoinKinds:          []string{"INNER"},
		InnerJoinCount:     1,
		LeftOuterJoinCount: 0,
	}
	if !reflect.DeepEqual(got.AccessorPostIDs, []int64{10, 11}) ||
		!reflect.DeepEqual(got.LookupAuthorIDs, []int64{1}) ||
		!reflect.DeepEqual(got.Accessor, wantAccessor) ||
		!reflect.DeepEqual(got.Lookup, wantLookup) {
		t.Fatalf("REL-005 result/metrics = %#v", got)
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

func TestObservationChangesForEveryOwnedREL005ResultStateAndMetricMutation(t *testing.T) {
	t.Parallel()

	base, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*fixtureConfig)
		check  func(*testing.T, Observation)
	}{
		{
			name: "accessor source key",
			mutate: func(config *fixtureConfig) {
				config.posts[1].AuthorID = 3
			},
			check: func(t *testing.T, got Observation) {
				if !reflect.DeepEqual(got.AccessorPostIDs, []int64{10}) {
					t.Fatalf("accessor IDs = %v, want [10]", got.AccessorPostIDs)
				}
			},
		},
		{
			name: "lookup terminal value",
			mutate: func(config *fixtureConfig) {
				config.posts[0].Title = "Mutation Alpha"
			},
			check: func(t *testing.T, got Observation) {
				if len(got.LookupAuthorIDs) != 0 {
					t.Fatalf("lookup IDs = %v, want empty", got.LookupAuthorIDs)
				}
			},
		},
		{
			name: "accessor ordering",
			mutate: func(config *fixtureConfig) {
				config.accessorDescending = true
			},
			check: func(t *testing.T, got Observation) {
				if !reflect.DeepEqual(got.AccessorPostIDs, []int64{11, 10}) {
					t.Fatalf("accessor IDs = %v, want [11 10]", got.AccessorPostIDs)
				}
			},
		},
		{
			name: "database state",
			mutate: func(config *fixtureConfig) {
				config.authors[0].Name = "Mutation Ada"
			},
			check: func(t *testing.T, got Observation) {
				if got.DBState.Authors[0].Name != "Mutation Ada" ||
					!reflect.DeepEqual(got.AccessorPostIDs, base.AccessorPostIDs) ||
					!reflect.DeepEqual(got.LookupAuthorIDs, base.LookupAuthorIDs) {
					t.Fatalf("state-only mutation = %#v", got)
				}
			},
		},
		{
			name: "lookup metrics",
			mutate: func(config *fixtureConfig) {
				config.repeatLookup = true
			},
			check: func(t *testing.T, got Observation) {
				if got.Lookup.QueryCount != 2 || got.Lookup.InnerJoinCount != 2 ||
					!reflect.DeepEqual(got.AccessorPostIDs, base.AccessorPostIDs) ||
					!reflect.DeepEqual(got.LookupAuthorIDs, base.LookupAuthorIDs) ||
					!reflect.DeepEqual(got.DBState, base.DBState) {
					t.Fatalf("metrics-only mutation = %#v", got)
				}
			},
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
			test.check(t, mutated)
			if reflect.DeepEqual(mutated, base) {
				t.Fatal("owned REL-005 mutation produced an unchanged observation")
			}
		})
	}
}
