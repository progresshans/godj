package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"example.com/godj-multiple-eager/models"
	"example.com/godj-multiple-eager/project"
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
	wire, err := definition.Encode(definition.Producer{Name: "multiple-eager", Version: "1"}, migrations.Migration{App: "join_reference", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "join_reference", Model: models.PersonDescriptor{}.Metadata()},
		migrations.CreateModel{AppLabel: "join_reference", Model: models.TeamDescriptor{}.Metadata()},
		migrations.CreateModel{AppLabel: "join_reference", Model: models.PostDescriptor{}.Metadata()},
		migrations.CreateModel{AppLabel: "join_reference", Model: models.CommentDescriptor{}.Metadata()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "multiple-eager", Document: wire})
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
	for _, label := range []string{"Red", "Blue"} {
		if _, err := models.TeamObjects.Create(ctx, backend, models.NewTeamCreate(label)); err != nil {
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
		if row.reviewer == 0 {
			input = input.WithTeamID(1)
		} else if row.reviewer == 2 {
			input = input.WithTeamID(2)
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

func checkQuery[O any](t *testing.T, backend *sqlite.Backend, q eagerQuery[O], observation nullableforwardproduct.MultipleEagerObservation, observe func(*O) *nullableforwardproduct.MultipleEagerRow) {
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
	rows := make([]nullableforwardproduct.MultipleEagerRow, len(all))
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

func TestGeneratedMultipleEagerReference(t *testing.T) {
	backend, facade := fixture(t)
	var reference nullableforwardproduct.MultipleEagerReference
	if err := json.Unmarshal(referenceData, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 220 {
		t.Fatal("incomplete reference")
	}
	related, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := project.BindRelations()
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
				t.Fatal("missing input")
			}
			typedPredicate, typedOK := fold(t, typed, observation.Expression)
			raw := models.PostObjects.Using(backend)
			// Apply all modifiers after selecting, exercising selection-preserving derivation.
			dynamicQuery, err := objects.ModelsPost.SelectRelated(raw).ParseDynamic(observation.Selected...)
			if err != nil {
				t.Fatal(err)
			}
			dynamicQuery = dynamicQuery.Filter(predicate).OrderBy(models.PostFields.ID.Asc())
			if observation.Distinct {
				dynamicQuery = dynamicQuery.Distinct()
			}
			dynamicQuery, err = dynamicQuery.Offset(observation.Offset)
			if err != nil {
				t.Fatal(err)
			}
			if observation.Limit != nil {
				dynamicQuery, err = dynamicQuery.Limit(*observation.Limit)
				if err != nil {
					t.Fatal(err)
				}
			}
			observe := func(row *project.ModelsPostObject) *nullableforwardproduct.MultipleEagerRow {
				return observeObject(t, row, observation.Selected)
			}
			checkQuery(t, backend, dynamicQuery, observation, observe)
			if typedOK {
				typedCases++
				typedQuery := objects.ModelsPost.SelectRelated(raw)
				for _, name := range observation.Selected {
					switch name {
					case "author":
						typedQuery = typedQuery.WithAuthor()
					case "reviewer":
						typedQuery = typedQuery.WithReviewer()
					case "team":
						typedQuery = typedQuery.WithTeam()
					default:
						t.Fatal(name)
					}
				}
				typedQuery = typedQuery.Filter(typedPredicate).OrderBy(models.PostFields.ID.Asc())
				if observation.Distinct {
					typedQuery = typedQuery.Distinct()
				}
				typedQuery, err = typedQuery.Offset(observation.Offset)
				if err != nil {
					t.Fatal(err)
				}
				if observation.Limit != nil {
					typedQuery, err = typedQuery.Limit(*observation.Limit)
					if err != nil {
						t.Fatal(err)
					}
				}
				if !raw.Filter(typedPredicate).Plan().Equal(raw.Filter(predicate).Plan()) {
					t.Fatal("typed/dynamic filter AST differ")
				}
				checkQuery(t, backend, typedQuery, observation, observe)
			}
			selectors := make([]project.ModelsPostRelationSelector, len(observation.Selected))
			for i, name := range observation.Selected {
				switch name {
				case "author":
					selectors[i] = facade.ModelsPost.Related.Author
				case "reviewer":
					selectors[i] = facade.ModelsPost.Related.Reviewer
				case "team":
					selectors[i] = facade.ModelsPost.Related.Team
				}
			}
			q := facade.ModelsPost.SelectRelated(selectors...).Filter(predicate).OrderBy(models.PostFields.ID.Asc())
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
			checkQuery(t, backend, q, observation, func(row *project.ModelsPost) *nullableforwardproduct.MultipleEagerRow {
				return observeFacade(t, row, observation.Selected)
			})
		})
	}
	if typedCases != 215 {
		t.Fatalf("typed reference roster=%d", typedCases)
	}
}
func observedPerson(person models.Person, present bool) *nullableforwardproduct.MultipleEagerTarget {
	if !present {
		return nil
	}
	return &nullableforwardproduct.MultipleEagerTarget{ID: person.ID, Name: person.Name, Nickname: person.Nickname, Active: person.Active}
}
func observeObject(t *testing.T, row *project.ModelsPostObject, selected []string) *nullableforwardproduct.MultipleEagerRow {
	t.Helper()
	if row == nil {
		return nil
	}
	source, err := row.Model()
	if err != nil {
		t.Fatal(err)
	}
	result := &nullableforwardproduct.MultipleEagerRow{ID: source.ID, Targets: map[string]*nullableforwardproduct.MultipleEagerTarget{}}
	for _, name := range selected {
		switch name {
		case "author":
			person, err := row.Author(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			result.Targets[name] = observedPerson(person, true)
		case "reviewer":
			person, present, err := row.Reviewer(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			result.Targets[name] = observedPerson(person, present)
		case "team":
			team, present, err := row.Team(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			result.Targets[name] = nil
			if present {
				result.Targets[name] = &nullableforwardproduct.MultipleEagerTarget{ID: team.ID, Label: team.Label}
			}
		default:
			t.Fatal(name)
		}
	}
	return result
}
func observeFacade(t *testing.T, row *project.ModelsPost, selected []string) *nullableforwardproduct.MultipleEagerRow {
	t.Helper()
	if row == nil {
		return nil
	}
	result := &nullableforwardproduct.MultipleEagerRow{ID: row.ID, Targets: map[string]*nullableforwardproduct.MultipleEagerTarget{}}
	for _, name := range selected {
		var person *project.ModelsPerson
		var err error
		switch name {
		case "author":
			person, err = row.Author(t.Context())
		case "reviewer":
			var present bool
			person, present, err = row.Reviewer(t.Context())
			if present != (person != nil) {
				t.Fatal("nullable person shape")
			}
		case "team":
			team, present, err := row.Team(t.Context())
			if err != nil || present != (team != nil) {
				t.Fatalf("team=%v,%v", present, err)
			}
			result.Targets[name] = nil
			if present {
				result.Targets[name] = &nullableforwardproduct.MultipleEagerTarget{ID: team.ID, Label: team.Label}
			}
			continue
		default:
			t.Fatal(name)
		}
		if err != nil {
			t.Fatal(err)
		}
		result.Targets[name] = nil
		if person != nil {
			result.Targets[name] = &nullableforwardproduct.MultipleEagerTarget{ID: person.ID, Name: person.Name, Nickname: person.Nickname, Active: person.Active}
		}
	}
	return result
}
func TestGeneratedMultipleEagerOwnershipAndDerivation(t *testing.T) {
	backend, facade := fixture(t)
	ctx := t.Context()
	reverse, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	selectors := []project.ModelsPostRelationSelector{facade.ModelsPost.Related.Team, facade.ModelsPost.Related.Author, facade.ModelsPost.Related.Reviewer}
	q := facade.ModelsPost.SelectRelated(selectors...).Filter(reverse.ModelsPost.Comments.Body.Exact("match")).OrderBy(models.PostFields.ID.Asc())
	selectors[0] = nil // caller-owned selector slice must not remain live
	rows, err := q.All(ctx)
	if err != nil || len(rows) != 7 {
		t.Fatalf("rows=%d,%v", len(rows), err)
	}
	before := backend.QueryCount()
	names := []string{"author", "reviewer", "team"}
	for _, pair := range [][2]int{{0, 1}, {3, 4}} {
		a, b := rows[pair[0]], rows[pair[1]]
		expected := observeFacade(t, b, names)
		if a == b || a.ID != b.ID {
			t.Fatal("duplicate sources collapsed")
		}
		a.Title = "caller source edit"
		for _, name := range names {
			switch name {
			case "author":
				one, e := a.Author(ctx)
				if e != nil {
					t.Fatal(e)
				}
				two, e := b.Author(ctx)
				if e != nil || one == two {
					t.Fatal("author storage aliases")
				}
				one.Name = "changed"
			case "reviewer":
				one, p, e := a.Reviewer(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if p {
					two, _, e := b.Reviewer(ctx)
					if e != nil || one == two {
						t.Fatal("reviewer storage aliases")
					}
					one.Name = "changed"
				}
			case "team":
				one, p, e := a.Team(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if p {
					two, _, e := b.Team(ctx)
					if e != nil || one == two {
						t.Fatal("team storage aliases")
					}
					one.Label = "changed"
				}
			}
		}
		if !reflect.DeepEqual(observeFacade(t, b, names), expected) {
			t.Fatal("sibling target cache changed")
		}
	}
	again, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range again {
		got := observeFacade(t, row, names)
		for _, target := range got.Targets {
			if target != nil && (target.Name == "changed" || target.Label == "changed") {
				t.Fatal("query cache mutated")
			}
		}
	}
	if backend.QueryCount() != before {
		t.Fatal("warm multiple selection performed I/O")
	}
	// Equal Person PK through different FK accessors still owns independent objects.
	same := facade.ModelsPost.Filter(models.PostFields.ID.Exact(3)).OrderBy(models.PostFields.ID.Asc()).SelectRelated(facade.ModelsPost.Related.Author, facade.ModelsPost.Related.Reviewer)
	row, found, err := same.First(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	before = backend.QueryCount()
	author, err := row.Author(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, present, err := row.Reviewer(ctx)
	if err != nil || !present || author == reviewer {
		t.Fatal("different FK cache aliases")
	}
	author.Name = "edit"
	if reviewer.Name != "Ada" || backend.QueryCount() != before {
		t.Fatal("same PK relation cache contaminated")
	}
	fresh := q.Fresh()
	freshRows, err := fresh.All(ctx)
	if err != nil || len(freshRows) != 7 || backend.QueryCount() != before+1 {
		t.Fatal("Fresh lost selections or cache isolation")
	}
	_ = observeFacade(t, freshRows[0], names)
	if backend.QueryCount() != before+1 {
		t.Fatal("Fresh did not warm all relations")
	}
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	raw := models.PostObjects.Using(backend).OrderBy(models.PostFields.ID.Asc())
	typed := objects.ModelsPost.SelectRelated(raw).WithAuthor()
	combined := typed.WithReviewer().WithTeam()
	singleRows, err := typed.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before = backend.QueryCount()
	if _, _, err := singleRows[0].Team(ctx); err != nil || backend.QueryCount() != before+1 {
		t.Fatal("derived builder mutated its source", err)
	}
	result, err := combined.Fresh().All(ctx)
	if err != nil || len(result) != 7 {
		t.Fatal(err)
	}
	before = backend.QueryCount()
	_ = observeObject(t, result[0], names)
	if backend.QueryCount() != before {
		t.Fatal("typed Fresh dropped target")
	}
}
func TestGeneratedMultipleEagerInvalidSelectors(t *testing.T) {
	backend, facade := fixture(t)
	other, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	raw := models.PostObjects.Using(backend).OrderBy(models.PostFields.ID.Asc())
	before := backend.QueryCount()
	for _, names := range [][]string{nil, {"author", "missing"}, {"reviewer", "team__label"}, {"author", "comments"}, {"author", "title"}} {
		q, err := objects.ModelsPost.SelectRelated(raw).ParseDynamic(names...)
		if err == nil {
			t.Fatalf("accepted %v", names)
		}
		if rows, err := q.All(t.Context()); rows != nil || err == nil {
			t.Fatal("partial dynamic query escaped")
		}
	}
	for _, q := range []project.ModelsPostEagerQuery{facade.ModelsPost.SelectRelated(), facade.ModelsPost.SelectRelated(facade.ModelsPost.Related.Author, nil), facade.ModelsPost.SelectRelated(facade.ModelsPost.Related.Author, other.ModelsPost.Related.Reviewer)} {
		if rows, err := q.All(t.Context()); rows != nil || err == nil {
			t.Fatal("invalid All accepted")
		}
		if row, found, err := q.First(t.Context()); row != nil || found || err == nil {
			t.Fatal("invalid First accepted")
		}
		// GoDj deliberately validates selectors even for Count, unlike Django.
		if count, err := q.Count(t.Context()); count != 0 || err == nil {
			t.Fatal("invalid Count accepted")
		}
	}
	if backend.QueryCount() != before {
		t.Fatal("invalid selection reached I/O")
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
