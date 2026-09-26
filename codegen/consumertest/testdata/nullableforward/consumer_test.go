package consumer_test

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"example.com/godj-nullable-forward/models"
	"example.com/godj-nullable-forward/project"
	"github.com/progresshans/godj/conformance/nullableforwardproduct"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed reference.json
var referenceData []byte

func TestGeneratedNullableForwardReference(t *testing.T) {
	ctx := t.Context()
	backend, err := sqlite.OpenMemory(ctx, "nullable-forward-consumer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	wire, err := definition.Encode(definition.Producer{Name: "nullable-forward", Version: "1"}, migrations.Migration{App: "nullable_reference", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "nullable_reference", Model: models.AuthorDescriptor{}.Metadata()}, migrations.CreateModel{AppLabel: "nullable_reference", Model: models.PostDescriptor{}.Metadata()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "nullable-forward", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 9, 19, 0, 0, 0, 123456000, time.UTC)
	second := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, input := range []models.AuthorCreate{models.NewAuthorCreate("Ada", 0, " space\nline ", first), models.NewAuthorCreate("Bob", -1, "plain", second), models.NewAuthorCreate("Cleo", 9223372036854775807, "", first)} {
		if _, err := models.AuthorObjects.Create(ctx, backend, input); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		title            string
		author, reviewer int64
	}{{"keep", 1, 0}, {"drop", 2, 0}, {"keep", 1, 1}, {"drop", 2, 1}, {"keep", 1, 2}, {"drop", 2, 2}, {"keep", 3, 3}} {
		input := models.NewPostCreate(row.title, row.author)
		if row.reviewer != 0 {
			input = input.WithReviewerID(row.reviewer)
		}
		if _, err := models.PostObjects.Create(ctx, backend, input); err != nil {
			t.Fatal(err)
		}
	}
	var reference nullableforwardproduct.Reference
	if err := json.Unmarshal(referenceData, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 73 {
		t.Fatal("incomplete reference")
	}
	bound, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	post, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "nullable_reference", ModelName: "post"}, models.PostDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	author, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "nullable_reference", ModelName: "author"}, models.AuthorDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	reviewerObject, err := orm.BindNullableForwardObject(post, "reviewer", author)
	if err != nil {
		t.Fatal(err)
	}
	related, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	typed := make(map[string]orm.Predicate[models.Post])
	dynamic := make(map[string]orm.Predicate[models.Post])
	for name, leaf := range reference.Leaves {
		var value any
		switch leaf.Path {
		case "title", "reviewer__name", "author__name", "reviewer__bio":
			var v string
			if err := json.Unmarshal(leaf.Value, &v); err != nil {
				t.Fatal(err)
			}
			value = v
			switch leaf.Path {
			case "title":
				typed[name] = models.PostFields.Title.Exact(v)
			case "reviewer__name":
				typed[name] = related.ModelsPost.Reviewer.Name.Exact(v)
			case "author__name":
				typed[name] = related.ModelsPost.Author.Name.Exact(v)
			case "reviewer__bio":
				typed[name] = related.ModelsPost.Reviewer.Bio.Exact(v)
			}
		case "reviewer__rank", "reviewer__id":
			var v int64
			if err := json.Unmarshal(leaf.Value, &v); err != nil {
				t.Fatal(err)
			}
			value = v
			if leaf.Path == "reviewer__rank" {
				typed[name] = related.ModelsPost.Reviewer.Rank.Exact(v)
			} else {
				typed[name] = related.ModelsPost.Reviewer.ID.Exact(v)
			}
		case "reviewer__seen_at":
			var text string
			if err := json.Unmarshal(leaf.Value, &text); err != nil {
				t.Fatal(err)
			}
			v, err := time.Parse(time.RFC3339Nano, text)
			if err != nil {
				t.Fatal(err)
			}
			value = v
			typed[name] = related.ModelsPost.Reviewer.SeenAt.Exact(v)
		case "reviewer__isnull":
			var v bool
			if err := json.Unmarshal(leaf.Value, &v); err != nil {
				t.Fatal(err)
			}
			value = v
			typed[name] = reviewerObject.IsNull(v)
		default:
			t.Fatal("unknown input path")
		}
		var predicates []orm.Predicate[models.Post]
		input := []orm.LookupInput{{Key: leaf.Path, Value: value}}
		if leaf.Path == "title" {
			predicates, err = orm.ParseDynamic[models.Post](models.PostDescriptor{}, nil, input)
		} else {
			predicates, err = orm.ParseDynamicRelations(post, nil, input)
		}
		if err != nil || len(predicates) != 1 {
			t.Fatalf("dynamic %s = %v", name, err)
		}
		dynamic[name] = predicates[0]
	}
	for _, observation := range reference.Observations {
		t.Run(observation.Name, func(t *testing.T) {
			left := fold(t, typed, observation.Expression)
			right := fold(t, dynamic, observation.Expression)
			q := models.PostObjects.Using(backend).Filter(left).OrderBy(models.PostFields.ID.Asc())
			other := models.PostObjects.Using(backend).Filter(right).OrderBy(models.PostFields.ID.Asc())
			if !q.Plan().Equal(other.Plan()) {
				t.Fatal("typed and dynamic AST differ")
			}
			before := backend.QueryCount()
			if count, err := q.Count(ctx); err != nil || count != observation.Count || backend.QueryCount() != before+1 {
				t.Fatalf("cold Count=%d,%v", count, err)
			}
			rows, err := q.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, len(rows))
			for i, row := range rows {
				ids[i] = row.ID
			}
			if !reflect.DeepEqual(ids, observation.IDs) {
				t.Fatalf("ids=%v want %v", ids, observation.IDs)
			}
			before = backend.QueryCount()
			if count, err := q.Count(ctx); err != nil || count != observation.Count || backend.QueryCount() != before {
				t.Fatalf("warm Count=%d,%v", count, err)
			}
			if count, err := other.Count(ctx); err != nil || count != observation.Count {
				t.Fatalf("dynamic Count=%d,%v", count, err)
			}
			eager := facade.ModelsPost.Filter(left).OrderBy(models.PostFields.ID.Asc()).SelectRelated(facade.ModelsPost.Related.Reviewer)
			if count, err := eager.Count(ctx); err != nil || count != observation.Count {
				t.Fatalf("eager Count=%d,%v", count, err)
			}
			before = backend.QueryCount()
			selected, err := eager.All(ctx)
			if err != nil || len(selected) != len(ids) {
				t.Fatalf("eager All=%d,%v", len(selected), err)
			}
			before = backend.QueryCount()
			for i, row := range selected {
				if row.ID != ids[i] {
					t.Fatal("eager row identity changed")
				}
				value, present, err := row.Reviewer(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if present != (row.ReviewerID != nil) || present && (value == nil || value.ID != *row.ReviewerID) {
					t.Fatal("nullable eager cache corrupted")
				}
			}
			if count, err := eager.Count(ctx); err != nil || count != observation.Count || backend.QueryCount() != before {
				t.Fatalf("warm eager Count=%d,%v", count, err)
			}
		})
	}
}

func fold(t *testing.T, leaves map[string]orm.Predicate[models.Post], node nullableforwardproduct.Node) orm.Predicate[models.Post] {
	t.Helper()
	if node.Name != "" {
		value, ok := leaves[node.Name]
		if !ok {
			t.Fatal("missing input")
		}
		return value
	}
	children := make([]orm.Predicate[models.Post], len(node.Children))
	for i, child := range node.Children {
		children[i] = fold(t, leaves, child)
	}
	if node.Kind == "not" && len(children) == 1 {
		return orm.Not(children[0])
	}
	if len(children) >= 2 {
		switch node.Kind {
		case "and":
			return orm.And(children[0], children[1], children[2:]...)
		case "or":
			return orm.Or(children[0], children[1], children[2:]...)
		}
	}
	t.Fatal("invalid reference tree")
	return orm.Predicate[models.Post]{}
}
