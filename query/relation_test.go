package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestForwardRelationPathAccessorsAndPlanCopies(t *testing.T) {
	t.Parallel()

	source := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	target := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	path, err := query.NewForwardRelationPath(
		source,
		"blog_post",
		"author",
		"author_id",
		target,
		"authors_author",
		"id",
		false,
		name,
	)
	if err != nil {
		t.Fatalf("NewForwardRelationPath() error = %v", err)
	}

	hops := path.Hops()
	if got, want := len(hops), 1; got != want {
		t.Fatalf("len(Hops()) = %d, want %d", got, want)
	}
	hop := hops[0]
	if hop.Source() != source || hop.SourceTable() != "blog_post" || hop.Field() != "author" ||
		hop.SourceColumn() != "author_id" || hop.Target() != target || hop.TargetTable() != "authors_author" ||
		hop.TargetPrimaryKeyColumn() != "id" || hop.Direction() != query.RelationForward ||
		hop.Cardinality() != ir.RelationManyToOne || hop.Nullable() {
		t.Fatalf("RelationHop accessors returned unexpected metadata: %#v", hop)
	}
	if !hop.Equal(path.Hops()[0]) || !path.Terminal().Equal(name) || !path.Equal(path) {
		t.Fatal("relation path value equality failed")
	}

	// Accessors return copies rather than the path's private slice.
	hops[0] = query.RelationHop{}
	if path.Hops()[0].Source() != source {
		t.Fatal("Hops() exposed mutable relation path storage")
	}

	related := query.NewRelatedCondition(path, query.LookupExact, query.String("Ada"))
	if !related.Field().Equal(name) {
		t.Fatalf("related condition field = %#v, want terminal %#v", related.Field(), name)
	}
	returned, ok := related.RelationPath()
	if !ok || !returned.Equal(path) {
		t.Fatalf("RelationPath() = (%#v, %v), want original path", returned, ok)
	}
	returnedHops := returned.Hops()
	returnedHops[0] = query.RelationHop{}
	again, ok := related.RelationPath()
	if !ok || again.Hops()[0].Source() != source {
		t.Fatal("Condition.RelationPath() exposed mutable path storage")
	}

	plan := query.NewPlan("blog_post", []query.FieldRef{
		query.NewFieldRef("id", "id", query.FieldInteger, false),
	}).WithConditions(related)
	conditions := plan.Conditions()
	conditions[0] = query.NewCondition(name, query.LookupExact, query.String("mutated"))
	gotPath, ok := plan.Conditions()[0].RelationPath()
	if !ok || !gotPath.Equal(path) {
		t.Fatal("Plan.Conditions() exposed mutable related condition storage")
	}
	if !plan.Equal(query.NewPlan("blog_post", plan.Columns()).WithConditions(related)) {
		t.Fatal("equal relation plans differ")
	}
	if plan.Equal(query.NewPlan("blog_post", plan.Columns()).WithConditions(
		query.NewCondition(name, query.LookupExact, query.String("Ada")),
	)) {
		t.Fatal("scalar and related conditions compared equal")
	}
	ordered := plan.WithOrderings(query.NewOrdering(
		query.NewFieldRef("id", "id", query.FieldInteger, false),
		query.Ascending,
	))
	limited, err := ordered.WithLimit(1)
	if err != nil {
		t.Fatalf("WithLimit() error = %v", err)
	}
	clonedPath, ok := limited.Conditions()[0].RelationPath()
	if !ok || !clonedPath.Equal(path) {
		t.Fatal("plan derivation dropped or changed relation path")
	}
}

func TestForwardRelationPathValidationIsStructured(t *testing.T) {
	t.Parallel()

	source := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	target := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	terminal := query.NewFieldRef("name", "name", query.FieldString, false)

	tests := []struct {
		name        string
		source      ir.ModelIdentity
		table       string
		field       string
		column      string
		target      ir.ModelIdentity
		targetTable string
		targetPK    string
		nullable    bool
		terminal    query.FieldRef
		code        string
	}{
		{name: "blank source", source: ir.ModelIdentity{}, table: "blog_post", field: "author", column: "author_id", target: target, targetTable: "authors_author", targetPK: "id", terminal: terminal, code: query.CodeInvalidPlan},
		{name: "blank source table", source: source, field: "author", column: "author_id", target: target, targetTable: "authors_author", targetPK: "id", terminal: terminal, code: query.CodeInvalidPlan},
		{name: "blank field", source: source, table: "blog_post", column: "author_id", target: target, targetTable: "authors_author", targetPK: "id", terminal: terminal, code: query.CodeInvalidPlan},
		{name: "blank source column", source: source, table: "blog_post", field: "author", target: target, targetTable: "authors_author", targetPK: "id", terminal: terminal, code: query.CodeInvalidPlan},
		{name: "blank target", source: source, table: "blog_post", field: "author", column: "author_id", targetTable: "authors_author", targetPK: "id", terminal: terminal, code: query.CodeInvalidPlan},
		{name: "blank target table", source: source, table: "blog_post", field: "author", column: "author_id", target: target, targetPK: "id", terminal: terminal, code: query.CodeInvalidPlan},
		{name: "blank target key", source: source, table: "blog_post", field: "author", column: "author_id", target: target, targetTable: "authors_author", terminal: terminal, code: query.CodeInvalidPlan},
		{name: "invalid terminal", source: source, table: "blog_post", field: "author", column: "author_id", target: target, targetTable: "authors_author", targetPK: "id", code: query.CodeInvalidPlan},
		{name: "nullable path", source: source, table: "blog_post", field: "author", column: "author_id", target: target, targetTable: "authors_author", targetPK: "id", nullable: true, terminal: terminal, code: query.CodeUnsupportedLookup},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := query.NewForwardRelationPath(
				test.source,
				test.table,
				test.field,
				test.column,
				test.target,
				test.targetTable,
				test.targetPK,
				test.nullable,
				test.terminal,
			)
			var queryError *query.Error
			if !errors.As(err, &queryError) || queryError.Code != test.code {
				t.Fatalf("error = %T %v, want *query.Error code %q", err, err, test.code)
			}
		})
	}
}
