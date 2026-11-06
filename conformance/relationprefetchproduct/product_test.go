package relationprefetchproduct

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/internal/relationstate"
)

func TestObserveExecutesExactREL012PrefetchAndDatabaseState(t *testing.T) {
	t.Parallel()

	got, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantAuthors := []AuthorPosts{
		{AuthorID: 1, PostIDs: []int64{10, 11}},
		{AuthorID: 2, PostIDs: []int64{}},
		{AuthorID: 3, PostIDs: []int64{12}},
	}
	wantMetrics := QueryMetrics{
		QueryCount:                2,
		StatementKinds:            []string{"SELECT", "SELECT"},
		JoinKinds:                 []string{},
		PrimaryQueryCount:         1,
		BatchQueryCount:           1,
		BatchPredicateColumn:      "author_id",
		BatchKeyCount:             3,
		RelatedAccessExtraQueries: 0,
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
	if !reflect.DeepEqual(got.Authors, wantAuthors) ||
		!reflect.DeepEqual(got.Metrics, wantMetrics) ||
		!reflect.DeepEqual(got.DBState, wantState) {
		t.Fatalf("REL-012 observation = %#v, want authors=%#v metrics=%#v state=%#v", got, wantAuthors, wantMetrics, wantState)
	}
}

func TestREL012ObservationAndInternalGatesRejectFalseGreens(t *testing.T) {
	t.Parallel()

	base, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Run("membership and state mutation changes payload", func(t *testing.T) {
		config := defaultFixtureConfig()
		config.posts[1].AuthorID = 3
		mutated, err := observe(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(mutated, base) || !reflect.DeepEqual(mutated.Authors[0].PostIDs, []int64{10}) ||
			!reflect.DeepEqual(mutated.Authors[2].PostIDs, []int64{11, 12}) {
			t.Fatalf("membership mutation did not change REL-012 payload: %#v", mutated)
		}
	})
	t.Run("owner ordering mutation changes payload", func(t *testing.T) {
		config := defaultFixtureConfig()
		config.ownersDescending = true
		mutated, err := observe(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(mutated, base) || mutated.Authors[0].AuthorID != 3 || mutated.Authors[2].AuthorID != 1 {
			t.Fatalf("owner-order mutation did not change REL-012 payload: %#v", mutated.Authors)
		}
	})
	t.Run("database state mutation changes payload", func(t *testing.T) {
		config := defaultFixtureConfig()
		config.authors[0].Name = "Mutation Ada"
		mutated, err := observe(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(mutated, base) || mutated.DBState.Authors[0].Name != "Mutation Ada" ||
			!reflect.DeepEqual(mutated.Authors, base.Authors) || !reflect.DeepEqual(mutated.Metrics, base.Metrics) {
			t.Fatalf("state-only mutation = %#v", mutated)
		}
	})
	for _, test := range []struct {
		name   string
		mutate func(*fixtureConfig)
	}{
		{name: "extra primary query", mutate: func(config *fixtureConfig) { config.repeatPrimary = true }},
		{name: "cold related access", mutate: func(config *fixtureConfig) { config.forceColdAccess = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			if _, err := observe(context.Background(), config); err == nil {
				t.Fatal("trace mutation published a successful REL-012 observation")
			}
		})
	}
}
