package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"example.com/godj-nested-eager/blog"
	"example.com/godj-nested-eager/directory"
	"example.com/godj-nested-eager/people"
	"example.com/godj-nested-eager/project"
	"github.com/progresshans/godj/conformance/nullableforwardproduct"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

//go:embed reference.json
var referenceData []byte

type eagerQuery[O any] interface {
	All(context.Context) ([]*O, error)
	First(context.Context) (*O, bool, error)
	Count(context.Context) (int64, error)
}

func checkQuery[O any](t *testing.T, backend *sqlite.Backend, q eagerQuery[O], observation nullableforwardproduct.NestedEagerObservation) {
	t.Helper()
	ctx := t.Context()
	observe := func(value *O) *nullableforwardproduct.NestedEagerRow {
		if value == nil {
			return nil
		}
		return observeGraph(t, value, observation.Selected)
	}
	observeAll := func(values []*O) []nullableforwardproduct.NestedEagerRow {
		rows := make([]nullableforwardproduct.NestedEagerRow, len(values))
		for i, value := range values {
			rows[i] = *observe(value)
		}
		return rows
	}
	before := backend.QueryCount()
	count, err := q.Count(ctx)
	if err != nil || count != observation.Count || backend.QueryCount()-before != uint64(len(observation.CountSQL)) {
		t.Fatalf("cold Count=%d,%v", count, err)
	}
	before = backend.QueryCount()
	first, found, err := q.First(ctx)
	if err != nil || found != (observation.First != nil) || !reflect.DeepEqual(observe(first), observation.First) || backend.QueryCount()-before != uint64(len(observation.FirstSQL)) {
		t.Fatalf("cold First=%+v,%v,%v", observe(first), found, err)
	}
	before = backend.QueryCount()
	all, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if actual := observeAll(all); !reflect.DeepEqual(actual, observation.Rows) || backend.QueryCount()-before != uint64(len(observation.AllSQL)) {
		wire, _ := json.Marshal(actual)
		t.Fatalf("All graph=%s; reads=%d want=%d", wire, backend.QueryCount()-before, len(observation.AllSQL))
	}
	before = backend.QueryCount()
	if count, err = q.Count(ctx); err != nil || count != observation.WarmCount {
		t.Fatalf("warm Count=%d,%v", count, err)
	}
	first, found, err = q.First(ctx)
	if err != nil || found != (observation.WarmFirst != nil) || !reflect.DeepEqual(observe(first), observation.WarmFirst) {
		t.Fatalf("warm First=%v,%v", found, err)
	}
	if all, err = q.All(ctx); err != nil || !reflect.DeepEqual(observeAll(all), observation.WarmRows) {
		t.Fatalf("warm All=%v", err)
	}
	if backend.QueryCount() != before || observation.WarmQueries != 0 {
		t.Fatal("warm graph performed SQL")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if rows, err := q.All(canceled); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled All=%v", err)
	}
	if row, found, err := q.First(canceled); row != nil || found || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled First=%v", err)
	}
	if count, err := q.Count(canceled); count != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Count=%v", err)
	}
	if backend.QueryCount() != before {
		t.Fatal("canceled graph performed SQL")
	}
}

// The traversal calls the public generated APIs. It neither reads private cache
// state nor rebuilds models from the oracle, so an eager/lazy boundary leak adds
// observable SQL to the surrounding receipt.
func required[T any](t *testing.T) func(*T, error) any {
	return func(value *T, err error) any {
		t.Helper()
		if err != nil || value == nil {
			t.Fatalf("required target=%v,%v", value, err)
		}
		return value
	}
}
func nullable[T any](t *testing.T) func(*T, bool, error) any {
	return func(value *T, found bool, err error) any {
		t.Helper()
		if err != nil || found != (value != nil) {
			t.Fatalf("nullable target=%v,%v,%v", value, found, err)
		}
		if !found {
			return nil
		}
		return value
	}
}
func raw[T any](t *testing.T) func(T, error) any {
	return func(value T, err error) any {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return &value
	}
}
func child(t *testing.T, parent any, edge string) any {
	t.Helper()
	if parent == nil {
		return nil
	}
	ctx := t.Context()
	switch row := parent.(type) {
	case *project.BlogPost:
		switch edge {
		case "author":
			return required[project.PeoplePerson](t)(row.Author(ctx))
		case "reviewer":
			return nullable[project.PeoplePerson](t)(row.Reviewer(ctx))
		}
	case *project.PeoplePerson:
		switch edge {
		case "team":
			return required[project.DirectoryTeam](t)(row.Team(ctx))
		case "backup":
			return nullable[project.DirectoryTeam](t)(row.Backup(ctx))
		case "manager":
			return nullable[project.PeoplePerson](t)(row.Manager(ctx))
		}
	case *project.DirectoryTeam:
		switch edge {
		case "organization":
			return required[project.DirectoryOrganization](t)(row.Organization(ctx))
		case "parent":
			return nullable[project.DirectoryTeam](t)(row.Parent(ctx))
		}
	case *project.BlogPostObject:
		switch edge {
		case "author":
			return required[project.PeoplePersonObject](t)(row.AuthorObject(ctx))
		case "reviewer":
			return nullable[project.PeoplePersonObject](t)(row.ReviewerObject(ctx))
		}
	case *project.PeoplePersonObject:
		switch edge {
		case "team":
			return required[project.DirectoryTeamObject](t)(row.TeamObject(ctx))
		case "backup":
			return nullable[project.DirectoryTeamObject](t)(row.BackupObject(ctx))
		case "manager":
			return nullable[project.PeoplePersonObject](t)(row.ManagerObject(ctx))
		}
	case *project.DirectoryTeamObject:
		switch edge {
		case "organization":
			return raw[directory.Organization](t)(row.Organization(ctx))
		case "parent":
			return nullable[project.DirectoryTeamObject](t)(row.ParentObject(ctx))
		}
	}
	t.Fatalf("unexpected edge %T.%s", parent, edge)
	return nil
}
func node(t *testing.T, value any) nullableforwardproduct.NestedEagerNode {
	t.Helper()
	if value == nil {
		return nil
	}
	// Model() returns an owned copy for low-level objects; facade models retain
	// their normal public fields. The same observation transport covers both.
	switch row := value.(type) {
	case *project.BlogPost:
		return node(t, &blog.Post{ID: row.ID, Title: row.Title, AuthorID: row.AuthorID, ReviewerID: row.ReviewerID})
	case *project.PeoplePerson:
		return node(t, &people.Person{ID: row.ID, Name: row.Name, Points: row.Points, TeamID: row.TeamID, BackupID: row.BackupID, ManagerID: row.ManagerID})
	case *project.DirectoryTeam:
		return node(t, &directory.Team{ID: row.ID, Label: row.Label, OrganizationID: row.OrganizationID, ParentID: row.ParentID})
	case *project.DirectoryOrganization:
		return node(t, &directory.Organization{ID: row.ID, Name: row.Name, Active: row.Active, Updated: row.Updated})
	case *project.BlogPostObject:
		return node(t, raw[blog.Post](t)(row.Model()))
	case *project.PeoplePersonObject:
		return node(t, raw[people.Person](t)(row.Model()))
	case *project.DirectoryTeamObject:
		return node(t, raw[directory.Team](t)(row.Model()))
	}
	var values map[string]any
	switch row := value.(type) {
	case *blog.Post:
		values = map[string]any{"id": row.ID, "title": row.Title, "author_id": row.AuthorID, "reviewer_id": row.ReviewerID}
	case *people.Person:
		values = map[string]any{"id": row.ID, "name": row.Name, "points": row.Points, "team_id": row.TeamID, "backup_id": row.BackupID, "manager_id": row.ManagerID}
	case *directory.Team:
		values = map[string]any{"id": row.ID, "label": row.Label, "organization_id": row.OrganizationID, "parent_id": row.ParentID}
	case *directory.Organization:
		values = map[string]any{"id": row.ID, "name": row.Name, "active": row.Active, "updated": nullableforwardproduct.EagerInstant(row.Updated)}
	default:
		t.Fatalf("unexpected node %T", value)
	}
	result, err := nullableforwardproduct.EagerNode(values)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func observeGraph(t *testing.T, source any, paths []string) *nullableforwardproduct.NestedEagerRow {
	t.Helper()
	result := &nullableforwardproduct.NestedEagerRow{Source: node(t, source), Targets: map[string]nullableforwardproduct.NestedEagerNode{}}
	for _, path := range paths {
		cursor := source
		parts := strings.Split(path, "__")
		for i, edge := range parts {
			cursor = child(t, cursor, edge)
			result.Targets[strings.Join(parts[:i+1], "__")] = node(t, cursor)
		}
	}
	return result
}

func facadeSelectors(t *testing.T, models project.Models, paths []string) []project.BlogPostRelationSelector {
	t.Helper()
	p := models.PeoplePerson.Related
	team := models.DirectoryTeam.Related
	selections := make([]project.BlogPostRelationSelector, len(paths))
	for i, path := range paths {
		switch path {
		case "author":
			selections[i] = models.BlogPost.Related.Author
		case "author__team":
			selections[i] = models.BlogPost.Related.Author.WithChildren(p.Team)
		case "author__backup":
			selections[i] = models.BlogPost.Related.Author.WithChildren(p.Backup)
		case "author__team__organization":
			selections[i] = models.BlogPost.Related.Author.WithChildren(p.Team.WithChildren(team.Organization))
		case "author__manager__manager":
			selections[i] = models.BlogPost.Related.Author.WithChildren(p.Manager.WithChildren(p.Manager))
		case "author__team__parent__parent__parent":
			selections[i] = models.BlogPost.Related.Author.WithChildren(p.Team.WithChildren(team.Parent.WithChildren(team.Parent.WithChildren(team.Parent))))
		case "reviewer__team__organization":
			selections[i] = models.BlogPost.Related.Reviewer.WithChildren(p.Team.WithChildren(team.Organization))
		case "reviewer__backup__organization":
			selections[i] = models.BlogPost.Related.Reviewer.WithChildren(p.Backup.WithChildren(team.Organization))
		case "reviewer__manager__team__organization":
			selections[i] = models.BlogPost.Related.Reviewer.WithChildren(p.Manager.WithChildren(p.Team.WithChildren(team.Organization)))
		default:
			t.Fatal("unregistered typed selection", path)
		}
	}
	return selections
}
func objectSelectors(t *testing.T, objects project.Objects, paths []string) []orm.RelatedSelection[blog.Post] {
	t.Helper()
	p := objects.PeoplePerson
	team := objects.DirectoryTeam
	selections := make([]orm.RelatedSelection[blog.Post], len(paths))
	for i, path := range paths {
		switch path {
		case "author":
			selections[i] = objects.BlogPost.SelectAuthor()
		case "author__team":
			selections[i] = objects.BlogPost.SelectAuthor(p.SelectTeam())
		case "author__backup":
			selections[i] = objects.BlogPost.SelectAuthor(p.SelectBackup())
		case "author__team__organization":
			selections[i] = objects.BlogPost.SelectAuthor(p.SelectTeam(team.SelectOrganization()))
		case "author__manager__manager":
			selections[i] = objects.BlogPost.SelectAuthor(p.SelectManager(p.SelectManager()))
		case "author__team__parent__parent__parent":
			selections[i] = objects.BlogPost.SelectAuthor(p.SelectTeam(team.SelectParent(team.SelectParent(team.SelectParent()))))
		case "reviewer__team__organization":
			selections[i] = objects.BlogPost.SelectReviewer(p.SelectTeam(team.SelectOrganization()))
		case "reviewer__backup__organization":
			selections[i] = objects.BlogPost.SelectReviewer(p.SelectBackup(team.SelectOrganization()))
		case "reviewer__manager__team__organization":
			selections[i] = objects.BlogPost.SelectReviewer(p.SelectManager(p.SelectTeam(team.SelectOrganization())))
		default:
			t.Fatal("unregistered typed object selection", path)
		}
	}
	return selections
}

func TestGeneratedNestedEagerReference(t *testing.T) {
	backend, facade := fixture(t)
	var reference nullableforwardproduct.NestedEagerReference
	if err := json.Unmarshal(referenceData, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 440 {
		t.Fatal("incomplete reference")
	}
	relations, err := project.BindRelations()
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
	leaves := map[string]orm.Predicate[blog.Post]{}
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
			predicates, err = relations.BlogPost.ParseDynamic(nil, input)
		}
		if err != nil || len(predicates) != 1 {
			t.Fatalf("dynamic input %s: %v", name, err)
		}
		leaves[name] = predicates[0]
	}
	seen := map[string]bool{}
	for _, observation := range reference.Observations {
		if seen[observation.Name] {
			t.Fatal("duplicate reference", observation.Name)
		}
		seen[observation.Name] = true
		t.Run(observation.Name, func(t *testing.T) {
			predicate, ok := fold(t, leaves, observation.Expression)
			if !ok {
				t.Fatal("missing predicate")
			}
			dynamicFacade, err := facade.BlogPost.SelectRelatedPaths(observation.Selected...)
			if err != nil {
				t.Fatal(err)
			}
			queries := []project.BlogPostEagerQuery{
				dynamicFacade,
				facade.BlogPost.SelectRelated(facadeSelectors(t, facade, observation.Selected)...),
			}
			for _, q := range queries {
				q = q.Filter(predicate).OrderBy(blog.PostFields.ID.Asc())
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
				checkQuery(t, backend, q, observation)
			}
			dynamic, err := objects.BlogPost.SelectRelated(blog.PostObjects.Using(backend)).ParseDynamic(observation.Selected...)
			if err != nil {
				t.Fatal(err)
			}
			typed := objects.BlogPost.SelectRelated(blog.PostObjects.Using(backend)).WithSelections(objectSelectors(t, objects, observation.Selected)...)
			for _, q := range []project.BlogPostSelectRelatedQuery{dynamic, typed} {
				q = q.Filter(predicate).OrderBy(blog.PostFields.ID.Asc())
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
				checkQuery(t, backend, q, observation)
			}
		})
	}
}

func TestGeneratedNestedEagerInvalidSelections(t *testing.T) {
	backend, facade := fixture(t)
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	before := backend.QueryCount()
	for _, path := range []string{"", "author__", "__author", "author__team__label", "comments", "author__team__members", "author__missing", "author__" + strings.Repeat("manager__", 63) + "manager"} {
		q, err := facade.BlogPost.SelectRelatedPaths("author__team", path)
		var structured *query.Error
		if !errors.As(err, &structured) {
			t.Fatalf("invalid path %q: %v", path, err)
		}
		if _, err := q.Count(t.Context()); err == nil {
			t.Fatal("failed parse exposed a usable query")
		}
		if _, err = objects.BlogPost.SelectRelated(blog.PostObjects.Using(backend)).ParseDynamic("author__team", path); err == nil {
			t.Fatalf("object accepted %q", path)
		}
	}
	foreign, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []project.BlogPostEagerQuery{
		facade.BlogPost.SelectRelated(foreign.BlogPost.Related.Author.WithChildren(foreign.PeoplePerson.Related.Team)),
		facade.BlogPost.SelectRelated(facade.BlogPost.Related.Author.WithChildren(foreign.PeoplePerson.Related.Team)),
		facade.BlogPost.SelectRelated(facade.BlogPost.Related.Author.WithChildren(facade.PeoplePerson.Related.Team.WithChildren(foreign.DirectoryTeam.Related.Organization))),
		facade.BlogPost.SelectRelated(nil),
	} {
		if _, err := q.All(t.Context()); err == nil {
			t.Fatal("foreign or nil facade selection accepted")
		}
	}
	foreignObjects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, selection := range []orm.RelatedSelection[blog.Post]{foreignObjects.BlogPost.SelectAuthor(), objects.BlogPost.SelectAuthor(foreignObjects.PeoplePerson.SelectTeam()), objects.BlogPost.SelectAuthor(nil)} {
		if _, err := objects.BlogPost.SelectRelated(blog.PostObjects.Using(backend)).WithSelections(selection).Count(t.Context()); err == nil {
			t.Fatal("foreign or nil object selection accepted")
		}
	}
	cause := errors.New("nested selection preparation")
	failed := objects.PeoplePerson.SelectTeam().WithConfigurationError(cause).WithConfigurationError(errors.New("later"))
	if _, err := objects.BlogPost.SelectRelated(blog.PostObjects.Using(backend)).WithAuthor(failed).All(t.Context()); !errors.Is(err, cause) {
		t.Fatalf("lost nested cause: %v", err)
	}
	if backend.QueryCount() != before {
		t.Fatal("invalid selection performed SQL")
	}
	// Maximum depth is a graph preparation limit; empty Count keeps the SQLite
	// physical JOIN limit separate. The 65th selected node must fail before SQL.
	selection := objects.PeoplePerson.SelectManager()
	for i := 1; i < 63; i++ {
		selection = objects.PeoplePerson.SelectManager(selection)
	}
	empty, err := blog.PostObjects.Using(backend).Limit(0)
	if err != nil {
		t.Fatal(err)
	}
	accepted := objects.BlogPost.SelectRelated(empty).WithAuthor(selection)
	if count, err := accepted.Count(t.Context()); err != nil || count != 0 {
		t.Fatalf("64-node route: %d,%v", count, err)
	}
	tooDeep := objects.BlogPost.SelectRelated(empty).WithAuthor(objects.PeoplePerson.SelectManager(selection))
	if _, err := tooDeep.Count(t.Context()); err == nil {
		t.Fatal("65-node route accepted")
	}
	repeated := make([]orm.RelatedSelection[blog.Post], orm.MaximumRelatedSelectionNodes)
	for i := range repeated {
		repeated[i] = objects.BlogPost.SelectAuthor()
	}
	if count, err := objects.BlogPost.SelectRelated(empty).WithSelections(repeated...).Count(t.Context()); err != nil || count != 0 {
		t.Fatalf("node bound: %d,%v", count, err)
	}
	repeated = append(repeated, objects.BlogPost.SelectAuthor())
	if _, err := objects.BlogPost.SelectRelated(empty).WithSelections(repeated...).Count(t.Context()); err == nil {
		t.Fatal("node bound exceeded")
	}
	if backend.QueryCount() != before {
		t.Fatal("empty/invalid bounded graph performed SQL")
	}
}

func TestGeneratedNestedEagerOwnership(t *testing.T) {
	backend, facade := fixture(t)
	ctx := t.Context()
	paths := []string{"author__team__organization", "reviewer__backup__organization"}
	base, err := facade.BlogPost.SelectRelatedPaths(paths...)
	if err != nil {
		t.Fatal(err)
	}
	base = base.OrderBy(blog.PostFields.ID.Asc())
	// The query owns its path/selector inputs, and each returned tree owns all
	// nullable fields down to the leaf, even when SQL rows share a target PK.
	paths[0] = "missing"
	rows, err := base.All(ctx)
	if err != nil || len(rows) != 8 {
		t.Fatalf("rows=%d,%v", len(rows), err)
	}
	before := backend.QueryCount()
	org := child(t, child(t, child(t, rows[0], "author"), "team"), "organization").(*project.DirectoryOrganization)
	*org.Name = "caller change"
	if got := child(t, child(t, child(t, rows[0], "author"), "team"), "organization").(*project.DirectoryOrganization); got != org || *got.Name != "caller change" {
		t.Fatal("one wrapper lost its normal relation identity")
	}
	for _, root := range []*project.BlogPost{rows[2]} {
		copy := child(t, child(t, child(t, root, "author"), "team"), "organization").(*project.DirectoryOrganization)
		if *copy.Name != "North" {
			t.Fatal("shared returned node leaked mutation")
		}
	}
	again, err := base.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	copy := child(t, child(t, child(t, again[0], "author"), "team"), "organization").(*project.DirectoryOrganization)
	if *copy.Name != "North" || backend.QueryCount() != before {
		t.Fatal("warm graph changed or performed SQL")
	}
	// Runtime coalesces concurrent evaluation, and wrappers return independent
	// graphs to every caller. Inspect traversal after joining workers so failures
	// and receipts remain deterministic.
	concurrent := base.Fresh()
	var wg sync.WaitGroup
	values := make([][]*project.BlogPost, 12)
	errs := make([]error, 12)
	for i := range values {
		wg.Go(func() { values[i], errs[i] = concurrent.All(ctx) })
	}
	wg.Wait()
	if backend.QueryCount() != before+1 {
		t.Fatal("concurrent eager evaluation repeated SQL")
	}
	for i := range values {
		if errs[i] != nil || len(values[i]) != 8 {
			t.Fatalf("concurrent result %d: %v", i, errs[i])
		}
		observeGraph(t, values[i][0], []string{"author__team__organization"})
	}
	if backend.QueryCount() != before+1 {
		t.Fatal("concurrent descendant access performed SQL")
	}
	changed := "Renamed"
	rawOrg, err := org.Unwrap()
	if err != nil {
		t.Fatal(err)
	}
	rawOrg.Name = &changed
	if err := directory.OrganizationObjects.Save(ctx, backend, &rawOrg, directory.OrganizationUpdateFields(directory.OrganizationFields.Name)); err != nil {
		t.Fatal(err)
	}
	before = backend.QueryCount()
	warm, found, err := base.First(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	if got := child(t, child(t, child(t, warm, "author"), "team"), "organization").(*project.DirectoryOrganization); *got.Name != "North" {
		t.Fatal("warm graph was not a snapshot")
	}
	if backend.QueryCount() != before {
		t.Fatal("warm snapshot performed SQL")
	}
	fresh, found, err := base.Fresh().First(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	if got := child(t, child(t, child(t, fresh, "author"), "team"), "organization").(*project.DirectoryOrganization); *got.Name != "Renamed" {
		t.Fatal("Fresh retained descendant cache")
	}
	if backend.QueryCount() != before+1 {
		t.Fatal("Fresh failed one-query graph receipt")
	}
	derived, err := base.Filter(blog.PostFields.ID.Exact(1)).All(ctx)
	if err != nil || len(derived) != 1 {
		t.Fatal(err)
	}
	if got := child(t, child(t, child(t, derived[0], "author"), "team"), "organization").(*project.DirectoryOrganization); *got.Name != "Renamed" {
		t.Fatal("derived query retained stale graph")
	}
	// An unselected edge keeps the original backend and normal lazy cache.
	before = backend.QueryCount()
	person := child(t, derived[0], "author").(*project.PeoplePerson)
	if _, found, err := person.Manager(ctx); err != nil || !found {
		t.Fatal(err)
	}
	if _, found, err := person.Manager(ctx); err != nil || !found {
		t.Fatal(err)
	}
	if backend.QueryCount() != before+1 {
		t.Fatal("unselected child lost lazy cache/backend affinity")
	}
}

func TestGeneratedNestedEagerAssignmentInvalidatesOnlyChangedEdge(t *testing.T) {
	backend, facade := fixture(t)
	q, err := facade.BlogPost.Filter(blog.PostFields.ID.Exact(3)).OrderBy(blog.PostFields.ID.Asc()).SelectRelatedPaths("author__team__organization", "reviewer__team__organization")
	if err != nil {
		t.Fatal(err)
	}
	post, found, err := q.First(t.Context())
	if err != nil || !found {
		t.Fatal(err)
	}
	originalAuthor := child(t, post, "author").(*project.PeoplePerson)
	reviewer := child(t, post, "reviewer").(*project.PeoplePerson)
	before := backend.QueryCount()
	post.AuthorID = 2
	changed := child(t, post, "author").(*project.PeoplePerson)
	organization := child(t, child(t, changed, "team"), "organization").(*project.DirectoryOrganization)
	if changed == originalAuthor || changed.ID != 2 || *organization.Name != "South" || backend.QueryCount() != before+3 {
		t.Fatal("FK assignment kept the previous descendant graph")
	}
	before = backend.QueryCount()
	if same := child(t, post, "reviewer").(*project.PeoplePerson); same != reviewer {
		t.Fatal("unrelated edge lost identity")
	}
	if got := child(t, child(t, reviewer, "team"), "organization").(*project.DirectoryOrganization); *got.Name != "North" || backend.QueryCount() != before {
		t.Fatal("unrelated selected subtree lost cache")
	}
	post.ReviewerID = nil
	if target, found, err := post.Reviewer(t.Context()); target != nil || found || err != nil || backend.QueryCount() != before {
		t.Fatal("NULL assignment retained a descendant graph or performed SQL")
	}
	fresh, found, err := q.First(t.Context())
	if err != nil || !found {
		t.Fatal(err)
	}
	if fresh.AuthorID != 1 || fresh.ReviewerID == nil || *fresh.ReviewerID != 1 {
		t.Fatal("assignment changed query state")
	}
	// Mutating the exported aggregate cannot replace a previously bound factory's
	// private project dispatch table.
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	objects.PeoplePerson = foreign.PeoplePerson
	graph, err := objects.BlogPost.SelectRelated(blog.PostObjects.Using(backend)).ParseDynamic("author__team__organization")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.All(t.Context()); err != nil {
		t.Fatalf("aggregate mutation changed sealed graph dispatch: %v", err)
	}
}

func TestGeneratedNestedEagerChildAssignmentPreservesUnvisitedSibling(t *testing.T) {
	for _, mode := range []string{"raw FK", "assignment"} {
		t.Run(mode, func(t *testing.T) {
			backend, facade := fixture(t)
			ctx := t.Context()
			q, err := facade.BlogPost.Filter(blog.PostFields.ID.Exact(5)).OrderBy(blog.PostFields.ID.Asc()).SelectRelatedPaths("reviewer__team__organization", "reviewer__backup__organization")
			if err != nil {
				t.Fatal(err)
			}
			post, found, err := q.First(ctx)
			if err != nil || !found {
				t.Fatal(err)
			}
			reviewer := child(t, post, "reviewer").(*project.PeoplePerson)
			before := backend.QueryCount()
			// Neither sibling has been accessed by application code. Changing TeamID
			// must preserve the complete selected Backup graph already returned by SQL.
			if mode == "assignment" {
				reviewer, err = reviewer.WithTeamID(4)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				reviewer.TeamID = 4
			}
			backup := child(t, reviewer, "backup").(*project.DirectoryTeam)
			org := child(t, backup, "organization").(*project.DirectoryOrganization)
			if backup.ID != 1 || *org.Name != "North" || backend.QueryCount() != before {
				t.Fatal("changing a child FK dropped an unvisited sibling graph")
			}
			team := child(t, reviewer, "team").(*project.DirectoryTeam)
			if team.ID != 4 || backend.QueryCount() != before+1 {
				t.Fatal("changed child FK kept its old selected value")
			}
			fresh, found, err := q.First(ctx)
			if err != nil || !found {
				t.Fatal(err)
			}
			if original := child(t, fresh, "reviewer").(*project.PeoplePerson); original.TeamID != 2 {
				t.Fatal("child assignment changed query state")
			}
		})
	}
}
