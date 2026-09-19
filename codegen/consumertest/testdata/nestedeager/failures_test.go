package consumer_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"example.com/godj-nested-eager/blog"
	"example.com/godj-nested-eager/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

// Wrap the real database rowset so failure tests keep generated descriptors,
// driver destinations, SQL, complete public wrappers and retry behavior in play.
type graphFaultBackend struct {
	*sqlite.Backend
	queryCalls int
	fail       func(db.Rows, query.Plan) db.Rows
}

func (backend *graphFaultBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := backend.Backend.Query(ctx, plan)
	backend.queryCalls++
	if err == nil && backend.queryCalls == 1 && backend.fail != nil {
		rows = backend.fail(rows, plan)
	}
	return rows, err
}

type graphFaultRows struct {
	db.Rows
	plan          query.Plan
	scans, closes int
	failAt        int
	failure       error
	kind          string
	cancel        context.CancelFunc
}

func (rows *graphFaultRows) Scan(destinations ...any) error {
	rows.scans++
	if err := rows.Rows.Scan(destinations...); err != nil {
		return err
	}
	if rows.scans != rows.failAt {
		return nil
	}
	switch rows.kind {
	case "scan":
		return rows.failure
	case "cancel scan":
		rows.cancel()
	case "child key", "child absent", "partial child", "absent ancestor", "partial below absent":
		offset := len(rows.plan.SourceFields())
		for _, projection := range rows.plan.RelationProjections() {
			fields := projection.TargetColumns()
			path := make([]string, 0, len(projection.Path().Hops()))
			for _, hop := range projection.Path().Hops() {
				path = append(path, hop.Field())
			}
			name := strings.Join(path, "__")
			for i, field := range fields {
				change := false
				var value any
				switch rows.kind {
				case "child key":
					change = name == "reviewer__team" && field.Name() == "id"
					value = int64(999)
				case "child absent":
					change = name == "reviewer__team"
				case "partial child":
					change = name == "reviewer__team" && field.Name() == "label"
				case "absent ancestor", "partial below absent":
					change = name == "reviewer"
					if rows.kind == "partial below absent" && name == "reviewer__team" {
						change = field.Name() != "label"
					}
				}
				if change {
					if err := destinations[offset+i].(sql.Scanner).Scan(value); err != nil {
						return err
					}
				}
			}
			offset += len(fields)
		}
		if rows.kind == "absent ancestor" || rows.kind == "partial below absent" {
			for i, field := range rows.plan.SourceFields() {
				if field.Name() == "reviewer" {
					if err := destinations[i].(sql.Scanner).Scan(nil); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
func (rows *graphFaultRows) Err() error {
	if rows.kind == "rows" {
		return errors.Join(rows.Rows.Err(), rows.failure)
	}
	return rows.Rows.Err()
}
func (rows *graphFaultRows) Close() error {
	rows.closes++
	err := rows.Rows.Close()
	if rows.kind == "cancel close" {
		rows.cancel()
	}
	if rows.kind == "close" {
		return errors.Join(err, rows.failure)
	}
	return err
}
func TestGeneratedNestedEagerFailures(t *testing.T) {
	for _, kind := range []string{"scan", "rows", "close", "cancel scan", "cancel close", "child key", "child absent", "partial child", "absent ancestor", "partial below absent"} {
		for _, terminal := range []string{"All", "First"} {
			t.Run(kind+"/"+terminal, func(t *testing.T) {
				backend, _ := fixture(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				failure := errors.New("injected " + kind)
				expected := failure
				switch kind {
				case "cancel scan", "cancel close":
					expected = context.Canceled
				case "child key", "child absent", "absent ancestor", "partial below absent":
					expected = &query.Error{Code: query.CodeRelatedObjectProjection}
				case "partial child":
					expected = &query.Error{Code: query.CodeInvalidPlan}
				}
				var failed *graphFaultRows
				faults := &graphFaultBackend{Backend: backend, fail: func(rows db.Rows, plan query.Plan) db.Rows {
					failAt := 2
					if terminal == "First" {
						failAt = 1
					}
					failed = &graphFaultRows{Rows: rows, plan: plan, failAt: failAt, failure: failure, kind: kind, cancel: cancel}
					return failed
				}}
				facade, err := project.Using(faults)
				if err != nil {
					t.Fatal(err)
				}
				q, err := facade.BlogPost.Filter(blog.PostFields.ID.GreaterThanOrEqual(3)).OrderBy(blog.PostFields.ID.Asc()).SelectRelatedPaths("reviewer__team__organization")
				if err != nil {
					t.Fatal(err)
				}
				if terminal == "All" {
					rows, err := q.All(ctx)
					if rows != nil || !errors.Is(err, expected) {
						t.Fatalf("partial All=%v,%v", rows, err)
					}
				} else {
					row, found, err := q.First(ctx)
					if row != nil || found || !errors.Is(err, expected) {
						t.Fatalf("partial First=%v,%v,%v", row, found, err)
					}
				}
				if failed == nil || failed.closes != 1 {
					t.Fatalf("failed rowset closed %v", failed)
				}
				// An invalid descendant cannot publish any row, including a valid earlier
				// row, and must not poison a retry on the same query value.
				rows, err := q.All(t.Context())
				if err != nil || len(rows) != 6 || faults.queryCalls != 2 {
					t.Fatalf("retry=%d,%v calls=%d", len(rows), err, faults.queryCalls)
				}
				for _, row := range rows {
					observeGraph(t, row, []string{"reviewer__team__organization"})
				}
				if _, err = q.All(t.Context()); err != nil || faults.queryCalls != 2 {
					t.Fatalf("warm retry=%v calls=%d", err, faults.queryCalls)
				}
				if failed.closes != 1 {
					t.Fatal("retry closed failed rows twice")
				}
			})
		}
	}
}

func TestGeneratedNestedEagerSelectedGraph(t *testing.T) {
	backend, _ := fixture(t)
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	selection := objects.BlogPost.SelectAuthor(objects.PeoplePerson.SelectTeam(objects.DirectoryTeam.SelectOrganization()))
	q := selection.Select(blog.PostObjects.Using(backend).OrderBy(blog.PostFields.ID.Asc()))
	selected, found, err := q.First(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	before := backend.QueryCount()
	related, err := selection.Related(selected)
	if err != nil {
		t.Fatal(err)
	}
	graph, present, err := related.SelectedGraph(ctx)
	if err != nil || !present {
		t.Fatalf("selected graph=%v,%v", present, err)
	}
	person, err := objects.PeoplePerson.FromSelected(graph)
	if err != nil {
		t.Fatal(err)
	}
	team := child(t, person, "team")
	org := child(t, team, "organization")
	if node(t, org)["name"] == nil || backend.QueryCount() != before {
		t.Fatal("graph transfer lost cache")
	}
	copy := *graph
	if _, err := objects.PeoplePerson.FromSelected(&copy); err == nil {
		t.Fatal("copied graph accepted")
	}
	foreign, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.PeoplePerson.FromSelected(graph); err == nil {
		t.Fatal("foreign graph accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if graph, present, err := related.SelectedGraph(canceled); graph != nil || present || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled graph=%v", err)
	}
	if _, _, err := related.SelectedGraph(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	fresh, err := related.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	if graph, present, err := fresh.SelectedGraph(ctx); graph != nil || present || err != nil {
		t.Fatalf("Fresh retained graph: %v", err)
	}
	if backend.QueryCount() != before {
		t.Fatal("graph inspection performed SQL")
	}
	if _, found, err := fresh.Get(ctx); err != nil || !found || backend.QueryCount() != before+1 {
		t.Fatalf("Fresh relation read=%v,%v", found, err)
	}
	if graph, present, err := fresh.SelectedGraph(ctx); graph != nil || present || err != nil {
		t.Fatal("lazy row fabricated a selected graph")
	}
	direct := objects.BlogPost.SelectAuthor()
	only, found, err := direct.Select(blog.PostObjects.Using(backend).OrderBy(blog.PostFields.ID.Asc())).First(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	plain, err := direct.Related(only)
	if err != nil {
		t.Fatal(err)
	}
	if graph, present, err := plain.SelectedGraph(ctx); graph != nil || present || err != nil {
		t.Fatal("unselected descendants appeared")
	}
	reviewer := objects.BlogPost.SelectReviewer(objects.PeoplePerson.SelectTeam())
	absent, found, err := reviewer.Select(blog.PostObjects.Using(backend).Filter(blog.PostFields.ID.Exact(1)).OrderBy(blog.PostFields.ID.Asc())).First(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	empty, err := reviewer.Related(absent)
	if err != nil {
		t.Fatal(err)
	}
	before = backend.QueryCount()
	if graph, present, err := empty.SelectedGraph(ctx); graph != nil || present || err != nil {
		t.Fatalf("absent graph=%v", err)
	}
	if value, present, err := empty.Get(ctx); present || err != nil || value.ID != 0 || backend.QueryCount() != before {
		t.Fatal("absent graph performed I/O")
	}
	// Descriptor preparation preserves the earliest error through depth, and
	// copies input slices before caller mutation.
	children := []orm.ForwardSelection[blog.Post]{selection}
	stable := orm.SelectForward(blog.PostObjects.Using(backend), children...)
	children[0] = nil
	if _, err := stable.All(ctx); err != nil {
		t.Fatalf("caller selection mutation: %v", err)
	}
	marker := fmt.Errorf("preserve: %w", errors.New("original cause"))
	invalid := selection.WithConfigurationError(marker).Select(blog.PostObjects.Using(backend))
	if _, err := invalid.Count(ctx); !errors.Is(err, marker) {
		t.Fatal("lost original error")
	}
}
