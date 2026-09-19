package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/godj-forward-lookups/models"
	"example.com/godj-forward-lookups/project"
	"github.com/progresshans/godj/conformance/nullableforwardproduct"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

//go:embed reference.json
var referenceData []byte

type relatedFields struct {
	name, nickname, bio orm.RelatedStringField[models.Post]
	score               orm.RelatedIntegerField[models.Post]
	seenAt              orm.RelatedDateTimeField[models.Post]
	active              orm.RelatedBooleanField[models.Post]
}

func TestGeneratedForwardLookupReference(t *testing.T) {
	ctx := t.Context()
	backend, err := sqlite.OpenMemory(ctx, "forward-lookup-consumer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	wire, err := definition.Encode(definition.Producer{Name: "forward-lookup", Version: "1"}, migrations.Migration{App: "forward_lookup", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "forward_lookup", Model: models.PersonDescriptor{}.Metadata()}, migrations.CreateModel{AppLabel: "forward_lookup", Model: models.PostDescriptor{}.Metadata()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "forward-lookup", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 9, 19, 0, 0, 0, 123456000, time.UTC)
	second := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, input := range []models.PersonCreate{
		models.NewPersonCreate("Ada", true),
		models.NewPersonCreate("Bob", false).WithNickname("").WithScore(0).WithBio("rate 50%_ done").WithSeenAt(first),
		models.NewPersonCreate("Cleo", true).WithNickname("ADA").WithScore(-1).WithBio("plain").WithSeenAt(second),
	} {
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
	var reference nullableforwardproduct.Reference
	if err := json.Unmarshal(referenceData, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 748 {
		t.Fatal("reference roster incomplete")
	}
	related, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]relatedFields{
		"author":   {related.ModelsPost.Author.Name, related.ModelsPost.Author.Nickname, related.ModelsPost.Author.Bio, related.ModelsPost.Author.Score, related.ModelsPost.Author.SeenAt, related.ModelsPost.Author.Active},
		"reviewer": {related.ModelsPost.Reviewer.Name, related.ModelsPost.Reviewer.Nickname, related.ModelsPost.Reviewer.Bio, related.ModelsPost.Reviewer.Score, related.ModelsPost.Reviewer.SeenAt, related.ModelsPost.Reviewer.Active},
	}
	typed := make(map[string]orm.Predicate[models.Post])
	dynamic := make(map[string]orm.Predicate[models.Post])
	for name, leaf := range reference.Leaves {
		field, lookup, raw, err := nullableforwardproduct.LookupInput(leaf)
		if err != nil {
			t.Fatal(err)
		}
		input := []orm.LookupInput{{Key: leaf.Path, Value: raw}}
		var values []orm.Predicate[models.Post]
		if leaf.Path == "title" {
			values, err = orm.ParseDynamic[models.Post](models.PostDescriptor{}, nil, input)
		} else {
			values, err = related.ModelsPost.ParseDynamic(nil, input)
		}
		if err != nil || len(values) != 1 {
			t.Fatalf("dynamic %s: %v", name, err)
		}
		dynamic[name] = values[0]
		// Typed In accepts concrete values, as on root fields. Explicit NULL list
		// members are a dynamic input capability and are not claimed as typed parity.
		if items, ok := raw.([]any); ok {
			nullMember := false
			for _, item := range items {
				nullMember = nullMember || item == nil
			}
			if nullMember {
				continue
			}
		}
		if leaf.Path == "title" {
			typed[name] = models.PostFields.Title.Exact(raw.(string))
			continue
		}
		edge, _, _ := strings.Cut(leaf.Path, "__")
		target := fields[edge]
		switch field.Name() {
		case "name":
			typed[name] = stringPredicate(t, target.name, lookup, raw)
		case "nickname":
			typed[name] = stringPredicate(t, target.nickname, lookup, raw)
		case "bio":
			typed[name] = stringPredicate(t, target.bio, lookup, raw)
		case "score":
			typed[name] = orderedPredicate(t, target.score, lookup, raw)
		case "seen_at":
			typed[name] = orderedPredicate(t, target.seenAt, lookup, raw)
		case "active":
			typed[name] = booleanPredicate(t, target.active, lookup, raw)
		default:
			t.Fatal("unknown input field")
		}
	}
	typedCount := 0
	for _, observation := range reference.Observations {
		t.Run(observation.Name, func(t *testing.T) {
			predicate, ok := fold(t, dynamic, observation.Expression)
			if !ok {
				t.Fatal("missing dynamic input")
			}
			q := models.PostObjects.Using(backend).Filter(predicate).OrderBy(models.PostFields.ID.Asc())
			if typedPredicate, ok := fold(t, typed, observation.Expression); ok {
				typedCount++
				typedQuery := models.PostObjects.Using(backend).Filter(typedPredicate).OrderBy(models.PostFields.ID.Asc())
				if !typedQuery.Plan().Equal(q.Plan()) {
					t.Fatal("typed/dynamic AST differ")
				}
				if count, err := typedQuery.Count(ctx); err != nil || count != observation.Count {
					t.Fatalf("typed Count=%d,%v", count, err)
				}
			}
			before := backend.QueryCount()
			if count, err := q.Count(ctx); err != nil || count != observation.Count || backend.QueryCount() != before+uint64(len(observation.CountSQL)) {
				t.Fatalf("cold Count=%d,%v", count, err)
			}
			before = backend.QueryCount()
			rows, err := q.All(ctx)
			if err != nil || backend.QueryCount() != before+uint64(len(observation.SQL)) {
				t.Fatalf("All=%v", err)
			}
			ids := make([]int64, len(rows))
			for i, row := range rows {
				ids[i] = row.ID
			}
			if !reflect.DeepEqual(ids, observation.IDs) {
				t.Fatalf("IDs=%v want %v", ids, observation.IDs)
			}
			before = backend.QueryCount()
			if count, err := q.Count(ctx); err != nil || count != observation.Count || backend.QueryCount() != before {
				t.Fatalf("warm Count=%d,%v", count, err)
			}
			eager := facade.ModelsPost.Filter(predicate).OrderBy(models.PostFields.ID.Asc()).SelectRelated(facade.ModelsPost.Related.Reviewer)
			if count, err := eager.Count(ctx); err != nil || count != observation.Count {
				t.Fatalf("eager Count=%d,%v", count, err)
			}
			before = backend.QueryCount()
			selected, err := eager.All(ctx)
			if usesAuthor(reference.Leaves, observation.Expression) {
				if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || backend.QueryCount() != before {
					t.Fatalf("unrelated projection=%v", err)
				}
				return
			}
			if err != nil || len(selected) != len(ids) || backend.QueryCount() != before+uint64(len(observation.SQL)) {
				t.Fatalf("eager All=%d,%v", len(selected), err)
			}
			before = backend.QueryCount()
			for i, row := range selected {
				if row.ID != ids[i] {
					t.Fatal("eager identity changed")
				}
				person, present, err := row.Reviewer(ctx)
				if err != nil || present != (row.ReviewerID != nil) || present && (person == nil || person.ID != *row.ReviewerID) {
					t.Fatalf("eager presence=%v,%v", present, err)
				}
				if present && person.ID == 1 && (person.Nickname != nil || person.Score != nil || person.Bio != nil || person.SeenAt != nil || !person.Active) {
					t.Fatal("nullable target scan lost NULL/Boolean values")
				}
			}
			if count, err := eager.Count(ctx); err != nil || count != observation.Count || backend.QueryCount() != before {
				t.Fatalf("eager cache=%d,%v", count, err)
			}
		})
	}
	if typedCount != 628 {
		t.Fatalf("typed roster=%d", typedCount)
	}
	before := backend.QueryCount()
	for _, input := range []orm.LookupInput{
		{Key: "reviewer__score__in", Value: []string{}}, {Key: "reviewer__active__gt", Value: true},
		{Key: "reviewer__nickname__in", Value: nil}, {Key: "reviewer__seen_at__in", Value: []any{"2026-09-19"}},
		{Key: "reviewer__name__icontains", Value: 123},
	} {
		values, err := related.ModelsPost.ParseDynamic(nil, []orm.LookupInput{{Key: "reviewer__active__exact", Value: false}, input})
		if err == nil || values != nil {
			t.Fatalf("invalid partial batch: %v", err)
		}
	}
	empty := related.ModelsPost.Reviewer.Score.In()
	broken := orm.RelatedIntegerField[models.Post]{}.In()
	if _, err := models.PostObjects.Using(backend).Filter(empty, broken).All(ctx); err == nil {
		t.Fatal("empty list hid unbound relation")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := models.PostObjects.Using(backend).Filter(empty).All(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("empty context=%v", err)
	}
	if backend.QueryCount() != before {
		t.Fatal("invalid or canceled query performed I/O")
	}
}

type orderedField[V any] interface {
	Exact(V) orm.Predicate[models.Post]
	GreaterThan(V) orm.Predicate[models.Post]
	GreaterThanOrEqual(V) orm.Predicate[models.Post]
	LessThan(V) orm.Predicate[models.Post]
	LessThanOrEqual(V) orm.Predicate[models.Post]
	IsNull(bool) orm.Predicate[models.Post]
	In(...V) orm.Predicate[models.Post]
}

func orderedPredicate[V any](t *testing.T, field orderedField[V], lookup query.Lookup, raw any) orm.Predicate[models.Post] {
	t.Helper()
	if lookup == query.LookupIsNull {
		return field.IsNull(raw.(bool))
	}
	if lookup == query.LookupIn {
		items := raw.([]any)
		values := make([]V, len(items))
		for i, v := range items {
			values[i] = v.(V)
		}
		return field.In(values...)
	}
	value := raw.(V)
	switch lookup {
	case query.LookupExact:
		return field.Exact(value)
	case query.LookupGreaterThan:
		return field.GreaterThan(value)
	case query.LookupGreaterThanOrEqual:
		return field.GreaterThanOrEqual(value)
	case query.LookupLessThan:
		return field.LessThan(value)
	case query.LookupLessThanOrEqual:
		return field.LessThanOrEqual(value)
	}
	t.Fatal("unknown ordered input")
	return orm.Predicate[models.Post]{}
}
func stringPredicate(t *testing.T, field orm.RelatedStringField[models.Post], lookup query.Lookup, raw any) orm.Predicate[models.Post] {
	if lookup == query.LookupIContains {
		return field.IContains(raw.(string))
	}
	return orderedPredicate(t, field, lookup, raw)
}
func booleanPredicate(t *testing.T, field orm.RelatedBooleanField[models.Post], lookup query.Lookup, raw any) orm.Predicate[models.Post] {
	switch lookup {
	case query.LookupExact:
		return field.Exact(raw.(bool))
	case query.LookupIsNull:
		return field.IsNull(raw.(bool))
	case query.LookupIn:
		items := raw.([]any)
		values := make([]bool, len(items))
		for i, v := range items {
			values[i] = v.(bool)
		}
		return field.In(values...)
	}
	t.Fatal("unknown Boolean input")
	return orm.Predicate[models.Post]{}
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
func usesAuthor(leaves map[string]nullableforwardproduct.Leaf, node nullableforwardproduct.Node) bool {
	if node.Name != "" {
		return strings.HasPrefix(leaves[node.Name].Path, "author__")
	}
	for _, child := range node.Children {
		if usesAuthor(leaves, child) {
			return true
		}
	}
	return false
}
