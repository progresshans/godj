package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"example.com/godj-nested-forward/directory"
	"example.com/godj-nested-forward/people"
	"fmt"
	"github.com/progresshans/godj/query"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/godj-nested-forward/blog"
	"example.com/godj-nested-forward/project"
	"github.com/progresshans/godj/conformance/nullableforwardproduct"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed reference.json
var referenceData []byte

func TestGeneratedNestedForwardInvalidRoutes(t *testing.T) {
	backend, facade := fixture(t)
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	before := backend.QueryCount()
	for _, input := range []orm.LookupInput{
		{Key: "author__team__missing", Value: "x"},
		{Key: "author__team__label__name", Value: "x"},
		{Key: "author__team__organization__updated", Value: "2000-01-01T00:00:00Z"},
		{Key: "reviewer__backup__organization__isnull", Value: "true"},
		{Key: "author__team__members__name", Value: int64(1)},
		{Key: "author__manager__manager__points__in", Value: []any{int64(1), "2"}},
		{Key: "author__" + strings.Repeat("manager__", 64) + "name", Value: "Ada"},
	} {
		predicates, err := relations.BlogPost.ParseDynamic(nil, []orm.LookupInput{{Key: "author__team__label", Value: "Red"}, input})
		var structured *query.Error
		if predicates != nil || !errors.As(err, &structured) {
			t.Fatalf("invalid route %s: %v", input.Key, err)
		}
	}
	var zero project.PeoplePersonRelatedFields[blog.Post]
	tooDeep := relations.BlogPost.Author
	for i := 0; i < 64; i++ {
		tooDeep = tooDeep.Manager()
	}
	for name, predicate := range map[string]orm.Predicate[blog.Post]{
		"zero scalar": zero.Name.Exact("Ada"), "zero traversal": zero.Team().Organization().Name.In(), "zero datetime": zero.Team().Organization().Updated.GreaterThan(time.Time{}), "over bound": tooDeep.Name.Exact("Ada"), "zero isnull": zero.IsNull(true),
	} {
		rows, err := facade.BlogPost.Filter(predicate).All(t.Context())
		var structured *query.Error
		if rows != nil || !errors.As(err, &structured) {
			t.Fatalf("invalid typed %s: %v", name, err)
		}
	}
	// Both Go types and sealed project identities matter. Identically shaped
	// projects are not interchangeable, and a prior structured cause survives.
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	post, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, blog.PostDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	person, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "people", ModelName: "person"}, people.PersonDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	other, err := orm.BindModel(foreign, ir.ModelIdentity{AppLabel: "people", ModelName: "person"}, people.PersonDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	author, err := orm.BindForward(post, "author", person)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := orm.BindForward(other, "manager", other)
	if err != nil {
		t.Fatal(err)
	}
	_, err = orm.ChainRelations(author, manager).String(people.PersonFields.Name)
	if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatalf("foreign composition: %v", err)
	}
	cause := &query.Error{Category: query.CategoryField, Code: query.CodeDisallowedLookup, Field: "manager"}
	failed := orm.ChainRelations(author.WithConfigurationError(fmt.Errorf("binding: %w", cause)), manager)
	_, err = failed.String(people.PersonFields.Name)
	if !errors.Is(err, cause) {
		t.Fatalf("binding lost cause: %v", err)
	}
	for _, predicate := range []orm.Predicate[blog.Post]{failed.IsNull(true), orm.RelatedStringField[blog.Post]{}.WithConfigurationError(cause).In(), orm.RelatedIntegerField[blog.Post]{}.WithConfigurationError(cause).GreaterThan(1), orm.RelatedBooleanField[blog.Post]{}.WithConfigurationError(cause).Exact(true), orm.RelatedDateTimeField[blog.Post]{}.WithConfigurationError(cause).IsNull(false)} {
		_, err := facade.BlogPost.Filter(predicate).All(t.Context())
		if !errors.Is(err, cause) {
			t.Fatalf("query lost binding cause: %v", err)
		}
	}
	policyCalls := 0
	failedPredicates, err := relations.BlogPost.ParseDynamic(func(field ir.Field, lookup query.Lookup) bool {
		policyCalls++
		if field.Name != "organization" || field.Nullable || lookup != query.LookupIsNull {
			t.Fatal("source-key policy metadata")
		}
		field.Relation.Target.ModelName = "corrupt"
		return false
	}, []orm.LookupInput{{Key: "reviewer__backup__organization__isnull", Value: "bad"}})
	if failedPredicates != nil || !errors.Is(err, &query.Error{Code: query.CodeDisallowedLookup}) || policyCalls != 1 {
		t.Fatalf("policy precedence: %v", err)
	}
	if predicates, err := relations.BlogPost.ParseDynamic(nil, []orm.LookupInput{{Key: "reviewer__backup__organization__isnull", Value: true}}); err != nil || len(predicates) != 1 {
		t.Fatalf("policy corrupted binding: %v", err)
	}
	if backend.QueryCount() != before {
		t.Fatal("invalid routes performed I/O")
	}
	original := people.PersonFields.Name
	t.Cleanup(func() { people.PersonFields.Name = original })
	people.PersonFields.Name = orm.NewStringField[people.Person](ir.Field{Name: "missing", Column: "missing", Kind: ir.FieldChar, MaxLength: 40})
	broken, err := project.BindRelations()
	if err == nil || !reflect.DeepEqual(broken, project.Relations{}) {
		t.Fatalf("failed terminal binding published partial groups: %v", err)
	}
	_, err = facade.BlogPost.Filter(relations.BlogPost.Author.Manager().Name.Exact("Ada")).All(t.Context())
	if !errors.Is(err, &query.Error{Code: query.CodeUnknownRelatedField}) {
		t.Fatalf("lazy group dropped terminal binding error: %v", err)
	}
	if backend.QueryCount() != before {
		t.Fatal("failed group binding performed I/O")
	}
}

func TestGeneratedNestedForwardDerivation(t *testing.T) {
	backend, facade := fixture(t)
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	base := facade.BlogPost.SelectRelated(facade.BlogPost.Related.Author, facade.BlogPost.Related.Reviewer).Filter(relations.BlogPost.Reviewer.Team().Organization().Name.Exact("North")).OrderBy(blog.PostFields.ID.Asc())
	rows, err := base.All(t.Context())
	if err != nil || len(rows) != 3 {
		t.Fatalf("initial rows=%d,%v", len(rows), err)
	}
	before := backend.QueryCount()
	author, err := rows[0].Author(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	*author.Points = 99
	again, err := base.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	untouched, err := again[0].Author(t.Context())
	if err != nil || *untouched.Points != 0 {
		t.Fatal("selected cache leaked caller mutation", err)
	}
	if backend.QueryCount() != before {
		t.Fatal("warm access performed I/O")
	}
	organization, found, err := directory.OrganizationObjects.Using(backend).Filter(directory.OrganizationFields.ID.Exact(1)).OrderBy(directory.OrganizationFields.ID.Asc()).First(t.Context())
	if err != nil || !found {
		t.Fatal(err)
	}
	changed := "Renamed"
	organization.Name = &changed
	if err := directory.OrganizationObjects.Save(t.Context(), backend, &organization, directory.OrganizationUpdateFields(directory.OrganizationFields.Name)); err != nil {
		t.Fatal(err)
	}
	before = backend.QueryCount()
	if count, err := base.Count(t.Context()); err != nil || count != 3 || backend.QueryCount() != before {
		t.Fatalf("warm snapshot=%d,%v", count, err)
	}
	if fresh, err := base.Fresh().All(t.Context()); err != nil || len(fresh) != 0 || backend.QueryCount() != before+1 {
		t.Fatalf("fresh rows=%d,%v", len(fresh), err)
	}
	derived := base.Filter(blog.PostFields.Title.Exact("keep"))
	if got, err := derived.All(t.Context()); err != nil || len(got) != 0 {
		t.Fatalf("derived query reused cache=%d,%v", len(got), err)
	}
}

type eagerQuery[O any] interface {
	All(context.Context) ([]*O, error)
	First(context.Context) (*O, bool, error)
	Count(context.Context) (int64, error)
}

func checkQuery[O any](t *testing.T, backend *sqlite.Backend, q eagerQuery[O], observation nullableforwardproduct.NestedObservation, observe func(*O) *nullableforwardproduct.NestedRow) {
	t.Helper()
	ctx := t.Context()
	expectedRows := make([]nullableforwardproduct.NestedRow, len(observation.IDs))
	for i, id := range observation.IDs {
		expectedRows[i] = nullableforwardproduct.NestedRow{ID: id, Targets: observation.Targets[i]}
	}
	var expectedFirst *nullableforwardproduct.NestedRow
	if len(expectedRows) > 0 {
		expectedFirst = &expectedRows[0]
	}
	before := backend.QueryCount()
	if count, err := q.Count(ctx); err != nil || count != observation.Count || backend.QueryCount() != before+uint64(len(observation.CountSQL)) {
		t.Fatalf("cold Count=%d,%v", count, err)
	}
	before = backend.QueryCount()
	first, found, err := q.First(ctx)
	if err != nil || found != (observation.First != nil) || !reflect.DeepEqual(observe(first), expectedFirst) || backend.QueryCount() != before+uint64(len(observation.FirstSQL)) {
		t.Fatalf("cold First=%v,%v,%v", first, found, err)
	}
	before = backend.QueryCount()
	all, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]nullableforwardproduct.NestedRow, len(all))
	for i, row := range all {
		rows[i] = *observe(row)
	}
	if !reflect.DeepEqual(rows, expectedRows) || backend.QueryCount() != before+uint64(len(observation.AllSQL)) {
		t.Fatalf("All=%+v want %+v; reads=%d", rows, expectedRows, backend.QueryCount()-before)
	}
	before = backend.QueryCount()
	if count, err := q.Count(ctx); err != nil || count != observation.WarmCount {
		t.Fatalf("warm Count=%d,%v", count, err)
	}
	first, found, err = q.First(ctx)
	if err != nil || found != (observation.WarmFirst != nil) || !reflect.DeepEqual(observe(first), expectedFirst) {
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

func TestGeneratedNestedForwardReference(t *testing.T) {
	backend, facade := fixture(t)
	var reference nullableforwardproduct.NestedReference
	if err := json.Unmarshal(referenceData, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 146 {
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
	typed := map[string]orm.Predicate[blog.Post]{
		"author_north":                related.BlogPost.Author.Team().Organization().Name.IContains("nor"),
		"reviewer_north":              related.BlogPost.Reviewer.Team().Organization().Name.Exact("North"),
		"reviewer_inactive":           related.BlogPost.Reviewer.Team().Organization().Active.Exact(false),
		"reviewer_name_null":          related.BlogPost.Reviewer.Team().Organization().Name.IsNull(true),
		"reviewer_name_present":       related.BlogPost.Reviewer.Team().Organization().Name.IsNull(false),
		"reviewer_backup_red":         related.BlogPost.Reviewer.Backup().Label.Exact("Red"),
		"reviewer_backup_north":       related.BlogPost.Reviewer.Backup().Organization().Name.Exact("North"),
		"reviewer_backup_null":        related.BlogPost.Reviewer.Backup().IsNull(true),
		"reviewer_backup_present":     related.BlogPost.Reviewer.Backup().IsNull(false),
		"reviewer_required_tail_null": related.BlogPost.Reviewer.Backup().Organization().IsNull(true),
		"reviewer_empty":              related.BlogPost.Reviewer.Team().Organization().Name.In(),
		"parent_north":                related.BlogPost.Author.Team().Parent().Organization().Name.Exact("North"),
		"parent_cycle":                related.BlogPost.Author.Team().Parent().Parent().Parent().Label.Exact("Red"),
		"manager_cycle":               related.BlogPost.Author.Manager().Manager().Name.Exact("Ada"),
		"reviewer_manager":            related.BlogPost.Reviewer.Manager().Team().Organization().Active.Exact(true),
		"manager_points":              related.BlogPost.Author.Manager().Points.GreaterThanOrEqual(1),
		"reviewer_team_id":            related.BlogPost.Reviewer.Team().ID.GreaterThanOrEqual(2),
		"reviewer_ids":                related.BlogPost.Reviewer.Team().Organization().ID.In(1, 3),
		"reviewer_updated_after":      related.BlogPost.Reviewer.Team().Organization().Updated.GreaterThan(time.Date(2000, 6, 1, 0, 0, 0, 0, time.UTC)),
		"reviewer_updated_null":       related.BlogPost.Reviewer.Team().Organization().Updated.IsNull(true),
		"author_required_present":     related.BlogPost.Author.IsNull(false),
		"author_required_absent":      related.BlogPost.Author.IsNull(true),
		"reviewer_team_missing":       related.BlogPost.Reviewer.Team().IsNull(true),
		"comments_match":              reverse.BlogPost.Comments.Body.Exact("match"),
		"title_keep":                  blog.PostFields.Title.Exact("keep"),
	}

	dynamic := make(map[string]orm.Predicate[blog.Post])
	for name, leaf := range reference.Leaves {
		value, err := nullableforwardproduct.NestedValue(leaf)
		if err != nil {
			t.Fatal(err)
		}
		input := []orm.LookupInput{{Key: leaf.Path, Value: value}}
		var predicates []orm.Predicate[blog.Post]
		switch {
		case leaf.Path == "title":
			predicates, err = orm.ParseDynamic[blog.Post](blog.PostDescriptor{}, nil, input)
		case strings.HasPrefix(leaf.Path, "comments__"):
			predicates, err = reverse.BlogPost.ParseDynamic(nil, input)
		default:
			predicates, err = related.BlogPost.ParseDynamic(nil, input)
		}
		if err != nil || len(predicates) != 1 {
			t.Fatalf("dynamic input %s=%v", name, err)
		}
		dynamic[name] = predicates[0]
		if leaf.Path != "title" && !strings.HasPrefix(leaf.Path, "comments__") {
			objectPredicates, err := objects.BlogPost.ParseDynamic(nil, input)
			if err != nil || len(objectPredicates) != 1 {
				t.Fatalf("object parser %s: %v", name, err)
			}
			if !blog.PostObjects.Using(nil).Filter(predicates...).Plan().Equal(blog.PostObjects.Using(nil).Filter(objectPredicates...).Plan()) {
				t.Fatal("object/query dynamic AST differ", name)
			}
		}
	}

	typedCases := 0
	for _, observation := range reference.Observations {
		t.Run(observation.Name, func(t *testing.T) {
			predicate, ok := fold(t, dynamic, observation.Expression)
			if !ok {
				t.Fatal("missing input")
			}
			typedPredicate, typedOK := fold(t, typed, observation.Expression)
			if len(observation.Selected) == 0 {
				predicates := []orm.Predicate[blog.Post]{predicate}
				if typedOK {
					typedCases++
					predicates = append(predicates, typedPredicate)
					if !blog.PostObjects.Using(nil).Filter(predicate).Plan().Equal(blog.PostObjects.Using(nil).Filter(typedPredicate).Plan()) {
						t.Fatal("plain typed/dynamic AST differ")
					}
				}
				for _, input := range predicates {
					q := facade.BlogPost.Filter(input).OrderBy(blog.PostFields.ID.Asc())
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
					checkQuery(t, backend, q, observation, func(row *project.BlogPost) *nullableforwardproduct.NestedRow {
						return observeFacade(t, row, observation.Selected)
					})
					checkRootProjection(t, backend, input, observation)
				}
				return
			}
			raw := blog.PostObjects.Using(backend)
			// Apply all modifiers after selecting, exercising selection-preserving derivation.
			dynamicQuery, err := objects.BlogPost.SelectRelated(raw).ParseDynamic(observation.Selected...)
			if err != nil {
				t.Fatal(err)
			}
			dynamicQuery = dynamicQuery.Filter(predicate).OrderBy(blog.PostFields.ID.Asc())
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
			observe := func(row *project.BlogPostObject) *nullableforwardproduct.NestedRow {
				return observeObject(t, row, observation.Selected)
			}
			checkQuery(t, backend, dynamicQuery, observation, observe)
			if typedOK {
				typedCases++
				typedQuery := objects.BlogPost.SelectRelated(raw)
				for _, name := range observation.Selected {
					switch name {
					case "author":
						typedQuery = typedQuery.WithAuthor()
					case "reviewer":
						typedQuery = typedQuery.WithReviewer()
					default:
						t.Fatal(name)
					}
				}
				typedQuery = typedQuery.Filter(typedPredicate).OrderBy(blog.PostFields.ID.Asc())
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
			selectors := make([]project.BlogPostRelationSelector, len(observation.Selected))
			for i, name := range observation.Selected {
				switch name {
				case "author":
					selectors[i] = facade.BlogPost.Related.Author
				case "reviewer":
					selectors[i] = facade.BlogPost.Related.Reviewer
				}
			}
			q := facade.BlogPost.SelectRelated(selectors...).Filter(predicate).OrderBy(blog.PostFields.ID.Asc())
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
			checkQuery(t, backend, q, observation, func(row *project.BlogPost) *nullableforwardproduct.NestedRow {
				return observeFacade(t, row, observation.Selected)
			})
		})
	}
	if typedCases != 138 {
		t.Fatalf("typed reference roster=%d", typedCases)
	}
}

// Scalar results retain the same bounded nested JOIN predicate, multiplicity,
// and slice as the independently observed model source. Selecting the root PK
// keeps DISTINCT's row identity while the JSON fixture covers value-only DISTINCT.
func checkRootProjection(t *testing.T, backend *sqlite.Backend, input orm.Predicate[blog.Post], observation nullableforwardproduct.NestedObservation) {
	t.Helper()
	source := blog.PostObjects.Using(backend).Filter(input).OrderBy(blog.PostFields.ID.Asc())
	if observation.Distinct {
		source = source.Distinct()
	}
	var err error
	source, err = source.Offset(observation.Offset)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Limit != nil {
		source, err = source.Limit(*observation.Limit)
		if err != nil {
			t.Fatal(err)
		}
	}
	type selectedPost struct {
		ID    int64
		Title string
	}
	projection := orm.Project2(blog.PostFields.ID, blog.PostFields.Title, func(id int64, title string) selectedPost { return selectedPost{id, title} })
	rows, err := orm.SelectInto(t.Context(), source, projection)
	if err != nil || len(rows) != len(observation.IDs) {
		t.Fatalf("nested root projection: %v, rows %v want IDs %v", err, rows, observation.IDs)
	}
	for i, row := range rows {
		title := "drop"
		if observation.IDs[i]%2 == 1 {
			title = "keep"
		}
		if row.ID != observation.IDs[i] || row.Title != title {
			t.Fatalf("nested projection row %d: %+v want %d/%s", i, row, observation.IDs[i], title)
		}
	}
}
func observedPerson(person people.Person, present bool) *nullableforwardproduct.NestedTarget {
	if !present {
		return nil
	}
	return &nullableforwardproduct.NestedTarget{ID: person.ID, Name: person.Name, Points: person.Points, Team: person.TeamID, Backup: person.BackupID, Manager: person.ManagerID}
}
func observeObject(t *testing.T, row *project.BlogPostObject, selected []string) *nullableforwardproduct.NestedRow {
	t.Helper()
	if row == nil {
		return nil
	}
	source, err := row.Model()
	if err != nil {
		t.Fatal(err)
	}
	result := &nullableforwardproduct.NestedRow{ID: source.ID, Targets: map[string]*nullableforwardproduct.NestedTarget{}}
	for _, name := range selected {
		switch name {
		case "author":
			person, err := row.Author(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			result.Targets[name] = observedPerson(person, true)
		case "reviewer":
			person, found, err := row.Reviewer(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			result.Targets[name] = observedPerson(person, found)
		default:
			t.Fatal(name)
		}
	}
	return result
}
func observeFacade(t *testing.T, row *project.BlogPost, selected []string) *nullableforwardproduct.NestedRow {
	t.Helper()
	if row == nil {
		return nil
	}
	result := &nullableforwardproduct.NestedRow{ID: row.ID, Targets: map[string]*nullableforwardproduct.NestedTarget{}}
	for _, name := range selected {
		var person *project.PeoplePerson
		var err error
		switch name {
		case "author":
			person, err = row.Author(t.Context())
		case "reviewer":
			var found bool
			person, found, err = row.Reviewer(t.Context())
			if found != (person != nil) {
				t.Fatal("nullable shape")
			}
		default:
			t.Fatal(name)
		}
		if err != nil {
			t.Fatal(err)
		}
		result.Targets[name] = nil
		if person != nil {
			result.Targets[name] = &nullableforwardproduct.NestedTarget{ID: person.ID, Name: person.Name, Points: person.Points, Team: person.TeamID, Backup: person.BackupID, Manager: person.ManagerID}
		}
	}
	return result
}

func TestGeneratedMixedCollectionRoutes(t *testing.T) {
	backend, facade := fixture(t)
	r, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := people.PersonObjects.Create(t.Context(), backend, people.NewPersonCreate("Another", 1)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		typed  orm.Predicate[blog.Post]
		input  orm.LookupInput
		negate bool
		want   []int64
	}{
		{"author", r.BlogPost.Author.Team().Members().Name.Exact("Ada"), orm.LookupInput{Key: "author__team__members__name", Value: "Ada"}, false, []int64{1, 3, 5}},
		{"nullable", r.BlogPost.Reviewer.Team().Members().Name.Exact("Ada"), orm.LookupInput{Key: "reviewer__team__members__name", Value: "Ada"}, false, []int64{3, 4}},
		{"nullable_not", r.BlogPost.Reviewer.Team().Members().Name.Exact("Ada"), orm.LookupInput{Key: "reviewer__team__members__name", Value: "Ada"}, true, []int64{1, 2, 5, 6, 7, 8}},
		{"multiplicity", r.BlogPost.Author.Team().Members().Name.In("Ada", "Another"), orm.LookupInput{Key: "author__team__members__name__in", Value: []string{"Ada", "Another"}}, false, []int64{1, 1, 3, 3, 5, 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := r.BlogPost.ParseDynamic(nil, []orm.LookupInput{tc.input})
			if err != nil || len(parsed) != 1 {
				t.Fatal(err)
			}
			if !blog.PostObjects.Using(backend).Filter(tc.typed).Plan().Equal(blog.PostObjects.Using(backend).Filter(parsed[0]).Plan()) {
				t.Fatal("mixed route AST differs")
			}
			for _, predicate := range []orm.Predicate[blog.Post]{tc.typed, parsed[0]} {
				if tc.negate {
					predicate = orm.Not(predicate)
				}
				qs := facade.BlogPost.Filter(predicate).OrderBy(blog.PostFields.ID.Asc())
				if count, err := qs.Count(t.Context()); err != nil || count != int64(len(tc.want)) {
					t.Fatal("mixed route cold count", count, err)
				}
				rows, err := qs.All(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				got := make([]int64, len(rows))
				for i, row := range rows {
					got[i] = row.ID
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatal("mixed route rows", got, tc.want)
				}
			}
		})
	}
}
