package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"example.com/godj-eager-joins/models"
	"example.com/godj-eager-joins/project"
	"github.com/progresshans/godj/conformance/nullableforwardproduct"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
)

//go:embed reference.json
var referenceData []byte

func fixture(t *testing.T) (*sqlite.Backend, project.Models) {
	t.Helper()
	ctx := t.Context()
	backend, err := sqlite.OpenMemory(ctx, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	wire, err := definition.Encode(definition.Producer{Name: "eager-joins", Version: "1"}, migrations.Migration{App: "join_reference", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "join_reference", Model: models.PersonDescriptor{}.Metadata()},
		migrations.CreateModel{AppLabel: "join_reference", Model: models.PostDescriptor{}.Metadata()},
		migrations.CreateModel{AppLabel: "join_reference", Model: models.CommentDescriptor{}.Metadata()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "eager-joins", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	for _, input := range []models.PersonCreate{models.NewPersonCreate("Ada", true), models.NewPersonCreate("Bob", false).WithNickname("B"), models.NewPersonCreate("Cleo", true).WithNickname("")} {
		if _, err := models.PersonObjects.Create(ctx, backend, input); err != nil {
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
	for _, row := range []struct {
		post int64
		body string
	}{{1, "match"}, {1, "match"}, {2, "match"}, {3, "other"}, {4, "match"}, {4, "match"}, {5, "match"}, {7, "match"}} {
		if _, err := models.CommentObjects.Create(ctx, backend, models.NewCommentCreate(row.post, row.body)); err != nil {
			t.Fatal(err)
		}
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	return backend, facade
}

type eagerQuery[O any] interface {
	All(context.Context) ([]*O, error)
	First(context.Context) (*O, bool, error)
	Count(context.Context) (int64, error)
}

func checkQuery[O any](t *testing.T, backend *sqlite.Backend, q eagerQuery[O], observation nullableforwardproduct.EagerJoinObservation, observe func(*O) *nullableforwardproduct.EagerJoinRow) {
	t.Helper()
	ctx := t.Context()
	before := backend.QueryCount()
	if count, err := q.Count(ctx); err != nil || count != observation.Count || backend.QueryCount() != before+uint64(len(observation.CountSQL)) {
		t.Fatalf("cold Count=%d,%v", count, err)
	}
	before = backend.QueryCount()
	first, found, err := q.First(ctx)
	if err != nil || found != (observation.First != nil) || !reflect.DeepEqual(observe(first), observation.First) || backend.QueryCount() != before+uint64(len(observation.FirstSQL)) {
		t.Fatalf("cold First=%v,%v,%v", first, found, err)
	}
	before = backend.QueryCount()
	all, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]nullableforwardproduct.EagerJoinRow, len(all))
	for i, row := range all {
		rows[i] = *observe(row)
	}
	if !reflect.DeepEqual(rows, observation.Rows) || backend.QueryCount() != before+uint64(len(observation.AllSQL)) {
		t.Fatalf("All=%+v want %+v; reads=%d", rows, observation.Rows, backend.QueryCount()-before)
	}
	before = backend.QueryCount()
	if count, err := q.Count(ctx); err != nil || count != observation.WarmCount {
		t.Fatalf("warm Count=%d,%v", count, err)
	}
	first, found, err = q.First(ctx)
	if err != nil || found != (observation.WarmFirst != nil) || !reflect.DeepEqual(observe(first), observation.WarmFirst) {
		t.Fatalf("warm First=%v,%v,%v", first, found, err)
	}
	if again, err := q.All(ctx); err != nil || len(again) != len(all) {
		t.Fatalf("warm All=%d,%v", len(again), err)
	}
	if backend.QueryCount() != before {
		t.Fatal("warm query/relation access performed I/O")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if rows, err := q.All(canceled); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("warm canceled All=%v", err)
	}
	if row, found, err := q.First(canceled); row != nil || found || !errors.Is(err, context.Canceled) {
		t.Fatalf("warm canceled First=%v", err)
	}
	if count, err := q.Count(canceled); count != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("warm canceled Count=%v", err)
	}
	if backend.QueryCount() != before {
		t.Fatal("canceled query performed I/O")
	}
}

func TestGeneratedEagerJoinReference(t *testing.T) {
	backend, facade := fixture(t)
	var reference nullableforwardproduct.EagerJoinReference
	if err := json.Unmarshal(referenceData, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 88 {
		t.Fatal("incomplete reference")
	}
	related, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	typed := map[string]orm.Predicate[models.Post]{
		"author_ada":            related.ModelsPost.Author.Name.IContains("ad"),
		"author_active":         related.ModelsPost.Author.Active.Exact(true),
		"reviewer_bob":          related.ModelsPost.Reviewer.Name.Exact("Bob"),
		"reviewer_inactive":     related.ModelsPost.Reviewer.Active.Exact(false),
		"reviewer_name_present": related.ModelsPost.Reviewer.Nickname.IsNull(false),
		"reviewer_name_null":    related.ModelsPost.Reviewer.Nickname.IsNull(true),
		"reviewer_empty":        related.ModelsPost.Reviewer.Name.In(),
		"reviewer_null":         objects.ModelsPost.Reviewer.IsNull(true),
		"comments_match":        reverse.ModelsPost.Comments.Body.Exact("match"),
		"title_keep":            models.PostFields.Title.Exact("keep"),
	}
	dynamic := make(map[string]orm.Predicate[models.Post])
	for name, leaf := range reference.Leaves {
		value, err := nullableforwardproduct.EagerJoinValue(leaf)
		if err != nil {
			t.Fatal(err)
		}
		input := []orm.LookupInput{{Key: leaf.Path, Value: value}}
		var predicates []orm.Predicate[models.Post]
		switch {
		case leaf.Path == "title":
			predicates, err = orm.ParseDynamic[models.Post](models.PostDescriptor{}, nil, input)
		case leaf.Path == "reviewer__isnull":
			predicates, err = objects.ModelsPost.ParseDynamic(nil, input)
		case strings.HasPrefix(leaf.Path, "comments__"):
			predicates, err = reverse.ModelsPost.ParseDynamic(nil, input)
		default:
			predicates, err = related.ModelsPost.ParseDynamic(nil, input)
		}
		if err != nil || len(predicates) != 1 {
			t.Fatalf("dynamic input %s=%v", name, err)
		}
		dynamic[name] = predicates[0]
	}
	typedCases := 0
	for _, observation := range reference.Observations {
		t.Run(observation.Name, func(t *testing.T) {
			predicate, ok := fold(t, dynamic, observation.Expression)
			if !ok {
				t.Fatal("missing dynamic input")
			}
			raw := models.PostObjects.Using(backend).Filter(predicate).OrderBy(models.PostFields.ID.Asc())
			typedPredicate, typedOK := fold(t, typed, observation.Expression)
			if typedOK {
				if !raw.Plan().Equal(models.PostObjects.Using(backend).Filter(typedPredicate).OrderBy(models.PostFields.ID.Asc()).Plan()) {
					t.Fatal("typed/dynamic filter AST differ")
				}
				typedCases++
			}
			if observation.Distinct {
				raw = raw.Distinct()
			}
			raw, err = raw.Offset(observation.Offset)
			if err != nil {
				t.Fatal(err)
			}
			if observation.Limit != nil {
				raw, err = raw.Limit(*observation.Limit)
				if err != nil {
					t.Fatal(err)
				}
			}
			dynamicQuery, err := objects.ModelsPost.SelectRelated(raw).ParseDynamic(observation.Selected)
			if err != nil {
				t.Fatal(err)
			}
			objectObserver := func(row *project.ModelsPostObject) *nullableforwardproduct.EagerJoinRow {
				if row == nil {
					return nil
				}
				source, err := row.Model()
				if err != nil {
					t.Fatal(err)
				}
				var person models.Person
				present := true
				if observation.Selected == "author" {
					person, err = row.Author(t.Context())
				} else {
					person, present, err = row.Reviewer(t.Context())
				}
				if err != nil {
					t.Fatal(err)
				}
				observed := &nullableforwardproduct.EagerJoinRow{ID: source.ID}
				if present {
					observed.Target = &nullableforwardproduct.EagerJoinTarget{ID: person.ID, Name: person.Name, Nickname: person.Nickname, Active: person.Active}
				}
				return observed
			}
			checkQuery(t, backend, dynamicQuery, observation, objectObserver)
			if typedOK {
				// A separate query owns a separate evaluation cache for the typed selector.
				typedRaw := models.PostObjects.Using(backend).Filter(typedPredicate).OrderBy(models.PostFields.ID.Asc())
				if observation.Distinct {
					typedRaw = typedRaw.Distinct()
				}
				typedRaw, err = typedRaw.Offset(observation.Offset)
				if err != nil {
					t.Fatal(err)
				}
				if observation.Limit != nil {
					typedRaw, err = typedRaw.Limit(*observation.Limit)
					if err != nil {
						t.Fatal(err)
					}
				}
				var typedQuery eagerQuery[project.ModelsPostObject]
				if observation.Selected == "author" {
					typedQuery = objects.ModelsPost.SelectRelated(typedRaw).Author()
				} else {
					typedQuery = objects.ModelsPost.SelectRelated(typedRaw).Reviewer()
				}
				checkQuery(t, backend, typedQuery, observation, objectObserver)
			}
			selector := facade.ModelsPost.Related.Author
			if observation.Selected == "reviewer" {
				selector = facade.ModelsPost.Related.Reviewer
			}
			q := facade.ModelsPost.Filter(predicate).OrderBy(models.PostFields.ID.Asc()).SelectRelated(selector)
			if observation.Distinct {
				q = q.Distinct()
			}
			q, err = q.Offset(observation.Offset)
			if err != nil {
				t.Fatal(err)
			}
			if observation.Limit != nil {
				q, err = q.Limit(*observation.Limit)
				if err != nil {
					t.Fatal(err)
				}
			}
			checkQuery(t, backend, q, observation, func(row *project.ModelsPost) *nullableforwardproduct.EagerJoinRow {
				return observeFacade(t, row, observation.Selected)
			})
		})
	}
	if typedCases != 86 {
		t.Fatalf("typed observation roster=%d", typedCases)
	}
}

func observeFacade(t *testing.T, row *project.ModelsPost, selected string) *nullableforwardproduct.EagerJoinRow {
	t.Helper()
	if row == nil {
		return nil
	}
	var person *project.ModelsPerson
	var err error
	present := true
	if selected == "author" {
		person, err = row.Author(t.Context())
	} else {
		person, present, err = row.Reviewer(t.Context())
	}
	if err != nil || present != (person != nil) {
		t.Fatalf("selected relation=%v,%v", present, err)
	}
	observed := &nullableforwardproduct.EagerJoinRow{ID: row.ID}
	if present {
		observed.Target = &nullableforwardproduct.EagerJoinTarget{ID: person.ID, Name: person.Name, Nickname: person.Nickname, Active: person.Active}
	}
	return observed
}

func TestGeneratedEagerJoinOwnsDuplicateRows(t *testing.T) {
	backend, facade := fixture(t)
	reverse, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{"author", "reviewer"} {
		t.Run(selected, func(t *testing.T) {
			selector := facade.ModelsPost.Related.Author
			if selected == "reviewer" {
				selector = facade.ModelsPost.Related.Reviewer
			}
			q := facade.ModelsPost.Filter(reverse.ModelsPost.Comments.Body.Exact("match")).OrderBy(models.PostFields.ID.Asc()).SelectRelated(selector)
			rows, err := q.All(t.Context())
			if err != nil || len(rows) != 7 {
				t.Fatalf("rows=%d,%v", len(rows), err)
			}
			before := backend.QueryCount()
			for _, pair := range [][2]int{{0, 1}, {3, 4}} {
				a, b := rows[pair[0]], rows[pair[1]]
				expected := observeFacade(t, b, selected)
				if a == b || a.ID != b.ID {
					t.Fatal("duplicate row identity collapsed")
				}
				a.Title = "caller source edit"
				if b.Title == a.Title {
					t.Fatal("duplicate source storage aliases")
				}
				if a.ReviewerID != nil {
					if a.ReviewerID == b.ReviewerID {
						t.Fatal("duplicate FK pointer aliases")
					}
					*a.ReviewerID = 999
					if *b.ReviewerID == 999 {
						t.Fatal("duplicate FK mutation escaped")
					}
					*a.ReviewerID = *b.ReviewerID
				}
				var person *project.ModelsPerson
				if selected == "author" {
					person, err = a.Author(t.Context())
				} else {
					person, _, err = a.Reviewer(t.Context())
				}
				if err != nil {
					t.Fatal(err)
				}
				if person != nil {
					person.Name = "caller target edit"
					if person.Nickname != nil {
						*person.Nickname = "caller nullable edit"
					}
					if got := observeFacade(t, b, selected); !reflect.DeepEqual(got, expected) {
						t.Fatal("duplicate target cache aliases")
					}
				}
			}
			again, err := q.All(t.Context())
			if err != nil || len(again) != 7 {
				t.Fatal(err)
			}
			if again[0].Title != "keep" || again[3].Title != "drop" {
				t.Fatal("source mutation reached query cache")
			}
			for _, row := range again {
				got := observeFacade(t, row, selected)
				if got.Target != nil && got.Target.Name == "caller target edit" {
					t.Fatal("target mutation reached query cache")
				}
			}
			first, found, err := q.First(t.Context())
			if err != nil || !found || first.Title != "keep" {
				t.Fatal("warm First storage corrupted")
			}
			if count, err := q.Count(t.Context()); err != nil || count != 7 {
				t.Fatalf("duplicate Count=%d,%v", count, err)
			}
			if backend.QueryCount() != before {
				t.Fatal("duplicate relation/cache reads performed I/O")
			}
		})
	}
}

func fold(t *testing.T, leaves map[string]orm.Predicate[models.Post], node nullableforwardproduct.Node) (orm.Predicate[models.Post], bool) {
	t.Helper()
	if node.Name != "" {
		value, ok := leaves[node.Name]
		return value, ok
	}
	children := make([]orm.Predicate[models.Post], len(node.Children))
	for i, child := range node.Children {
		value, ok := fold(t, leaves, child)
		if !ok {
			return orm.Predicate[models.Post]{}, false
		}
		children[i] = value
	}
	if node.Kind == "not" && len(children) == 1 {
		return orm.Not(children[0]), true
	}
	if len(children) >= 2 {
		switch node.Kind {
		case "and":
			return orm.And(children[0], children[1], children[2:]...), true
		case "or":
			return orm.Or(children[0], children[1], children[2:]...), true
		}
	}
	t.Fatal("invalid input tree")
	return orm.Predicate[models.Post]{}, false
}
