package consumer_test

import (
	"example.com/godj-nested-forward/blog"
	"example.com/godj-nested-forward/directory"
	"example.com/godj-nested-forward/people"
	"example.com/godj-nested-forward/project"
	"github.com/progresshans/godj/conformance/nullableforwardproduct"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/orm"
	"testing"
	"time"
)

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
	// Query traversal supports finite self paths. The migration lifecycle's
	// general cyclic schema support remains separate, so this query consumer
	// provisions an explicit FK-enforced physical fixture, then uses generated
	// create/save APIs for all data. It is not migration conformance evidence.
	for _, statement := range []string{
		`CREATE TABLE nested_reference_organization(id INTEGER PRIMARY KEY,name TEXT NULL,active BOOLEAN NOT NULL,updated DATETIME NULL)`,
		`CREATE TABLE nested_reference_team(id INTEGER PRIMARY KEY,label TEXT NOT NULL,organization_id INTEGER NOT NULL REFERENCES nested_reference_organization(id),parent_id INTEGER NULL REFERENCES nested_reference_team(id))`,
		`CREATE TABLE nested_reference_person(id INTEGER PRIMARY KEY,name TEXT NOT NULL,points BIGINT NULL,team_id INTEGER NOT NULL REFERENCES nested_reference_team(id),backup_id INTEGER NULL REFERENCES nested_reference_team(id),manager_id INTEGER NULL REFERENCES nested_reference_person(id))`,
		`CREATE TABLE nested_reference_post(id INTEGER PRIMARY KEY,title TEXT NOT NULL,author_id INTEGER NOT NULL REFERENCES nested_reference_person(id),reviewer_id INTEGER NULL REFERENCES nested_reference_person(id))`,
		`CREATE TABLE nested_reference_comment(id INTEGER PRIMARY KEY,post_id INTEGER NOT NULL REFERENCES nested_reference_post(id),body TEXT NOT NULL)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	for _, input := range []directory.OrganizationCreate{directory.NewOrganizationCreate(true).WithName("North").WithUpdated(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)), directory.NewOrganizationCreate(false).WithName("South").WithUpdated(time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)), directory.NewOrganizationCreate(true)} {
		if _, err := directory.OrganizationObjects.Create(ctx, backend, input); err != nil {
			t.Fatal(err)
		}
	}
	teams := make([]directory.Team, 4)
	for i, row := range []struct {
		label        string
		organization int64
	}{{"Red", 1}, {"Blue", 2}, {"Green", 3}, {"Quiet", 1}} {
		value, err := directory.TeamObjects.Create(ctx, backend, directory.NewTeamCreate(row.label, row.organization))
		if err != nil {
			t.Fatal(err)
		}
		teams[i] = value
	}
	for i, parent := range []int64{3, 1, 2} {
		teams[i].ParentID = &parent
		if err := directory.TeamObjects.Save(ctx, backend, &teams[i], directory.TeamUpdateFieldNames("parent")); err != nil {
			t.Fatal(err)
		}
	}
	persons := make([]people.Person, 4)
	for i, input := range []people.PersonCreate{people.NewPersonCreate("Ada", 1).WithPoints(0), people.NewPersonCreate("Bob", 2).WithPoints(2).WithBackupID(1), people.NewPersonCreate("Cleo", 3).WithBackupID(2), people.NewPersonCreate("Dana", 4).WithPoints(-1).WithBackupID(3)} {
		person, err := people.PersonObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		persons[i] = person
	}
	for i, manager := range []int64{2, 1, 2} {
		persons[i].ManagerID = &manager
		if err := people.PersonObjects.Save(ctx, backend, &persons[i], people.PersonUpdateFieldNames("manager")); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		title            string
		author, reviewer int64
	}{{"keep", 1, 0}, {"drop", 2, 0}, {"keep", 1, 1}, {"drop", 2, 1}, {"keep", 1, 2}, {"drop", 2, 2}, {"keep", 3, 3}, {"drop", 4, 4}} {
		input := blog.NewPostCreate(row.title, row.author)
		if row.reviewer != 0 {
			input = input.WithReviewerID(row.reviewer)
		}
		if _, err := blog.PostObjects.Create(ctx, backend, input); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		post int64
		body string
	}{{1, "match"}, {1, "match"}, {2, "match"}, {3, "other"}, {4, "match"}, {4, "match"}, {5, "match"}, {7, "match"}, {8, "match"}} {
		if _, err := blog.CommentObjects.Create(ctx, backend, blog.NewCommentCreate(row.post, row.body)); err != nil {
			t.Fatal(err)
		}
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	return backend, facade
}

func fold(t *testing.T, leaves map[string]orm.Predicate[blog.Post], node nullableforwardproduct.Node) (orm.Predicate[blog.Post], bool) {
	t.Helper()
	if node.Name != "" {
		value, ok := leaves[node.Name]
		return value, ok
	}
	children := make([]orm.Predicate[blog.Post], len(node.Children))
	for i, child := range node.Children {
		value, ok := fold(t, leaves, child)
		if !ok {
			return orm.Predicate[blog.Post]{}, false
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
	return orm.Predicate[blog.Post]{}, false
}
