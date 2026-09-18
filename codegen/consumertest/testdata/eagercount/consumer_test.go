package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"example.com/godj-eager-count/models"
	"example.com/godj-eager-count/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

//go:embed eager-count-django61.json
var reference []byte

type recorder struct {
	*sqlite.Backend
	last query.Plan
	sql  string
}

func (r *recorder) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	r.last = plan
	statement, _, err := sqlite.Compile(plan)
	if err != nil {
		return nil, err
	}
	r.sql = statement
	return r.Backend.Query(ctx, plan)
}

func fixture(t *testing.T) (*recorder, project.Models) {
	t.Helper()
	ctx := t.Context()
	b, err := sqlite.OpenMemory(ctx, "eager-count")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	wire, err := definition.Encode(definition.Producer{Name: "eager-count", Version: "1"}, migrations.Migration{App: "count_reference", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "count_reference", Model: models.AuthorDescriptor{}.Metadata()},
		migrations.CreateModel{AppLabel: "count_reference", Model: models.PostDescriptor{}.Metadata()},
		migrations.CreateModel{AppLabel: "count_reference", Model: models.CommentDescriptor{}.Metadata()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "eager-count", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (migrations.Executor{Backend: b}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	a, err := models.AuthorObjects.Create(ctx, b, models.NewAuthorCreate("Ada"))
	if err != nil {
		t.Fatal(err)
	}
	bb, err := models.AuthorObjects.Create(ctx, b, models.NewAuthorCreate("Bob"))
	if err != nil {
		t.Fatal(err)
	}
	posts := make([]models.Post, 4)
	for i := 1; i <= 4; i++ {
		author := a.ID
		if i >= 3 {
			author = bb.ID
		}
		var reviewer *int64
		if i%2 == 1 {
			v := bb.ID
			reviewer = &v
		}
		input := models.NewPostCreate(fmt.Sprint(i), author)
		if reviewer != nil {
			input = input.WithReviewerID(*reviewer)
		}
		posts[i-1], err = models.PostObjects.Create(ctx, b, input)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		index int
		body  string
	}{{0, "match"}, {0, "match"}, {1, "match"}, {2, "other"}} {
		if _, err := models.CommentObjects.Create(ctx, b, models.NewCommentCreate(posts[row.index].ID, row.body)); err != nil {
			t.Fatal(err)
		}
	}
	r := &recorder{Backend: b}
	facade, err := project.Using(r)
	if err != nil {
		t.Fatal(err)
	}
	return r, facade
}

func TestGeneratedEagerCountReference(t *testing.T) {
	var expected struct {
		Observations []struct {
			Name    string   `json:"name"`
			Count   int64    `json:"count"`
			ColdSQL []string `json:"cold_sql"`
			AllIDs  []int64  `json:"all_ids"`
		}
	}
	if err := json.Unmarshal(reference, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected.Observations) != 22 {
		t.Fatal("incomplete reference")
	}
	r, facade := fixture(t)
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range expected.Observations {
		t.Run(observation.Name, func(t *testing.T) {
			relation, variant, _ := strings.Cut(observation.Name, "_")
			selector := facade.ModelsPost.Related.Author
			if relation == "reviewer" {
				selector = facade.ModelsPost.Related.Reviewer
			}
			q := facade.ModelsPost.OrderBy(models.PostFields.ID.Asc()).SelectRelated(selector)
			switch variant {
			case "all":
			case "filtered":
				q = q.Filter(models.PostFields.Title.In("1", "3"))
			case "limit":
				q, err = q.Limit(2)
			case "offset_limit":
				q, err = q.Offset(1)
				if err == nil {
					q, err = q.Limit(2)
				}
			case "past_end":
				q, err = q.Offset(20)
			case "empty_in", "none":
				q = q.Filter(models.PostFields.ID.In())
			case "related_filter":
				if relation == "author" {
					q = q.Filter(relations.ModelsPost.Author.Name.Exact("Bob"))
				} else {
					before := r.QueryCount()
					predicates, err := relations.ModelsPost.ParseDynamic(nil, []orm.LookupInput{{Key: "reviewer__name", Value: "Bob"}})
					var typed *query.Error
					if predicates != nil || !errors.As(err, &typed) || typed.Code != query.CodeUnsupportedLookup || r.QueryCount() != before {
						t.Fatalf("unsupported nullable target filter = %v", err)
					}
					return // The reference result is observed, not claimed as GoDj parity.
				}
			case "reverse_filter", "reverse_distinct", "reverse_slice":
				q = q.Filter(reverse.ModelsPost.Comments.Body.Exact("match"))
				if variant == "reverse_distinct" {
					q = q.Distinct()
				}
				if variant == "reverse_slice" {
					q, err = q.Offset(1)
					if err == nil {
						q, err = q.Limit(2)
					}
				}
			default:
				t.Fatal("unhandled reference case")
			}
			if err != nil {
				t.Fatal(err)
			}
			for repeat := 0; repeat < 2; repeat++ {
				before := r.QueryCount()
				got, err := q.Count(t.Context())
				if err != nil || got != observation.Count || r.QueryCount()-before != uint64(len(observation.ColdSQL)) {
					t.Fatalf("cold Count=%d,%v queries=%d", got, err, r.QueryCount()-before)
				}
				if _, selected := r.last.RelationProjection(); selected || !r.last.ResultShape().IsCountAll() {
					t.Fatal("Count materializes models")
				}
				if len(observation.ColdSQL) > 0 && strings.Count(r.sql, " JOIN ") != strings.Count(observation.ColdSQL[0], " JOIN ") {
					t.Fatalf("Count JOIN shape=%s", r.sql)
				}
			}
			before := r.QueryCount()
			all, err := q.All(t.Context())
			if strings.HasPrefix(variant, "reverse_") {
				// Count needs only the filter JOIN. Materializing a different
				// eager edge remains an explicit, unimplemented combination.
				var typed *query.Error
				if !errors.As(err, &typed) || typed.Code != query.CodeInvalidPlan || r.QueryCount() != before {
					t.Fatalf("unrelated eager/filter joins=%v", err)
				}
				if got, err := q.Count(t.Context()); err != nil || got != observation.Count || r.QueryCount() != before+1 {
					t.Fatalf("failed All affected Count=%d,%v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, len(all))
			for i, row := range all {
				ids[i] = row.ID
			}
			if !reflect.DeepEqual(ids, observation.AllIDs) {
				t.Fatalf("rows=%v want %v", ids, observation.AllIDs)
			}
			before = r.QueryCount()
			if got, err := q.Count(t.Context()); err != nil || got != observation.Count || r.QueryCount() != before {
				t.Fatalf("warm Count=%d,%v", got, err)
			}
			for _, row := range all {
				if relation == "author" {
					if _, err := row.Author(t.Context()); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, _, err := row.Reviewer(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
			}
			if r.QueryCount() != before {
				t.Fatal("warm relation access performed I/O")
			}
		})
	}
}

type nilContext struct{ context.Context }

func TestGeneratedEagerCountPublicSurfaces(t *testing.T) {
	r, facade := fixture(t)
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	raw := models.PostObjects.Using(r).Filter(models.PostFields.ID.GreaterThan(1))
	typed := objects.ModelsPost.SelectRelated(raw).Author()
	dynamic, err := objects.ModelsPost.SelectRelated(raw).ParseDynamic("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []func(context.Context) (int64, error){typed.Count, dynamic.Count} {
		if got, err := count(t.Context()); err != nil || got != 3 {
			t.Fatalf("public Count=%d,%v", got, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var zeroDynamic project.ModelsPostDynamicSelectRelatedQuery
	var zeroTyped project.ModelsPostAuthorSelectRelatedQuery
	var zeroFacade project.ModelsPostEagerQuery
	valid := facade.ModelsPost.SelectRelated(facade.ModelsPost.Related.Author)
	before := r.QueryCount()
	for _, count := range []func(context.Context) (int64, error){typed.Count, dynamic.Count, valid.Count, zeroTyped.Count, zeroDynamic.Count, zeroFacade.Count} {
		if got, err := count(ctx); got != 0 || !errors.Is(err, context.Canceled) {
			t.Fatalf("context precedence=%d,%v", got, err)
		}
		for _, nilCtx := range []context.Context{nil, (*nilContext)(nil)} {
			if got, err := count(nilCtx); got != 0 || err == nil {
				t.Fatalf("nil context=%d,%v", got, err)
			}
		}
	}
	for _, count := range []func(context.Context) (int64, error){zeroTyped.Count, zeroDynamic.Count, zeroFacade.Count} {
		if got, err := count(t.Context()); got != 0 || err == nil {
			t.Fatalf("zero query=%d,%v", got, err)
		}
	}
	if r.QueryCount() != before {
		t.Fatal("invalid Count performed I/O")
	}
}
