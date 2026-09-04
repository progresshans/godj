package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestForwardRelationProjectionIsSingularImmutableAndPlanPreserving(t *testing.T) {
	t.Parallel()

	projection := newTestRelationProjection(t, false)
	hop := projection.Hop()
	if hop.Source() != (ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}) ||
		hop.SourceTable() != "blog_post" || hop.Field() != "author" || hop.SourceColumn() != "author_id" ||
		hop.Target() != (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) ||
		hop.TargetTable() != "authors_author" || hop.TargetPrimaryKeyColumn() != "id" ||
		hop.Direction() != query.RelationForward || hop.Cardinality() != ir.RelationManyToOne || hop.Nullable() {
		t.Fatalf("projection hop = %#v", hop)
	}

	targetColumns := projection.TargetColumns()
	if got, want := len(targetColumns), 2; got != want {
		t.Fatalf("len(TargetColumns()) = %d, want %d", got, want)
	}
	targetColumns[0] = query.NewFieldRef("mutated", "mutated", query.FieldString, false)
	if got := projection.TargetColumns()[0].Name(); got != "id" {
		t.Fatalf("TargetColumns() exposed storage: first name = %q", got)
	}
	if !projection.Equal(newTestRelationProjection(t, false)) || projection.Equal(newTestRelationProjection(t, true)) {
		t.Fatal("RelationProjection.Equal omitted projection state")
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	base := query.NewPlan("blog_post", []query.FieldRef{id, title}).
		WithConditions(query.NewCondition(title, query.LookupExact, query.String("Alpha"))).
		WithOrderings(query.NewOrdering(id, query.Ascending))
	limited, err := base.WithLimit(2)
	if err != nil {
		t.Fatalf("WithLimit() error = %v", err)
	}
	selected, err := limited.WithRelationProjection(projection)
	if err != nil {
		t.Fatalf("WithRelationProjection() error = %v", err)
	}
	if _, ok := limited.RelationProjection(); ok {
		t.Fatal("WithRelationProjection mutated its source plan")
	}
	returned, ok := selected.RelationProjection()
	if !ok || !returned.Equal(projection) {
		t.Fatalf("RelationProjection() = (%#v, %v)", returned, ok)
	}
	if selected.Table() != limited.Table() || len(selected.Conditions()) != 1 || len(selected.Orderings()) != 1 {
		t.Fatalf("projection derivation changed root plan: %#v", selected)
	}
	limit, ok := selected.Limit()
	if !ok || limit != 2 {
		t.Fatalf("selected limit = (%d, %v)", limit, ok)
	}
	identical, err := limited.WithRelationProjection(newTestRelationProjection(t, false))
	if err != nil || !selected.Equal(identical) {
		t.Fatalf("equal selected plans differ: err=%v", err)
	}
	different, err := limited.WithRelationProjection(newTestRelationProjection(t, true))
	if err != nil || selected.Equal(different) {
		t.Fatalf("different selected plans compare equal: err=%v", err)
	}
	if overwritten, err := selected.WithRelationProjection(projection); err == nil || !overwritten.Equal(query.Plan{}) {
		t.Fatalf("second WithRelationProjection() = (%#v, %v), want zero/error", overwritten, err)
	} else {
		assertInvalidProjectionPlanError(t, err)
	}
	if selected.WithConditions(query.NewCondition(id, query.LookupExact, query.Integer(10))).Equal(selected) {
		t.Fatal("derived projected plan omitted new condition from equality")
	}
	if got, ok := selected.WithOrderings().RelationProjection(); !ok || !got.Equal(projection) {
		t.Fatal("plan derivation dropped relation projection")
	}
}

func TestForwardRelationProjectionValidationRejectsEveryUnsupportedShape(t *testing.T) {
	t.Parallel()

	validSource := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	validTarget := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	validSourceKey := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	validTargetKey := query.NewFieldRef("id", "id", query.FieldInteger, false)
	validColumns := []query.FieldRef{
		validTargetKey,
		query.NewFieldRef("name", "name", query.FieldString, false),
	}

	tests := []struct {
		name         string
		source       ir.ModelIdentity
		sourceTable  string
		sourceKey    query.FieldRef
		target       ir.ModelIdentity
		targetTable  string
		targetKey    query.FieldRef
		targetFields []query.FieldRef
	}{
		{name: "zero source", sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: validColumns},
		{name: "noncanonical source", source: ir.ModelIdentity{AppLabel: "Blog", ModelName: "post"}, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: validColumns},
		{name: "invalid source table", source: validSource, sourceTable: "blog-post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: validColumns},
		{name: "invalid source field", source: validSource, sourceTable: "blog_post", sourceKey: query.NewFieldRef("Author", "author_id", query.FieldInteger, false), target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: validColumns},
		{name: "noninteger source key", source: validSource, sourceTable: "blog_post", sourceKey: query.NewFieldRef("author", "author_id", query.FieldString, false), target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: validColumns},
		{name: "zero target", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, targetTable: "authors_author", targetKey: validTargetKey, targetFields: validColumns},
		{name: "invalid target table", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors-author", targetKey: validTargetKey, targetFields: validColumns},
		{name: "nullable target key", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: query.NewFieldRef("id", "id", query.FieldInteger, true), targetFields: validColumns},
		{name: "noninteger target key", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: query.NewFieldRef("id", "id", query.FieldString, false), targetFields: validColumns},
		{name: "empty target fields", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: validTargetKey},
		{name: "target key absent", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: validColumns[1:]},
		{name: "target key shape mismatch", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: []query.FieldRef{query.NewFieldRef("id", "id", query.FieldInteger, true)}},
		{name: "duplicate target name", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: []query.FieldRef{validTargetKey, query.NewFieldRef("id", "other", query.FieldInteger, false)}},
		{name: "duplicate target column", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: []query.FieldRef{validTargetKey, query.NewFieldRef("other", "id", query.FieldInteger, false)}},
		{name: "invalid target field", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: []query.FieldRef{validTargetKey, query.NewFieldRef("DisplayName", "name", query.FieldString, false)}},
		{name: "unsupported target kind", source: validSource, sourceTable: "blog_post", sourceKey: validSourceKey, target: validTarget, targetTable: "authors_author", targetKey: validTargetKey, targetFields: []query.FieldRef{validTargetKey, query.NewFieldRef("score", "score", query.FieldKind("float"), false)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection, err := query.NewForwardRelationProjection(
				test.source,
				test.sourceTable,
				test.sourceKey,
				test.target,
				test.targetTable,
				test.targetKey,
				test.targetFields,
			)
			if err == nil || !projection.Equal(query.RelationProjection{}) {
				t.Fatalf("NewForwardRelationProjection() = (%#v, %v), want zero/error", projection, err)
			}
			assertInvalidProjectionPlanError(t, err)
		})
	}

	if plan, err := query.NewPlan("blog_post", nil).WithRelationProjection(query.RelationProjection{}); err == nil || !plan.Equal(query.Plan{}) {
		t.Fatalf("WithRelationProjection(zero) = (%#v, %v), want zero/error", plan, err)
	} else {
		assertInvalidProjectionPlanError(t, err)
	}
}

func newTestRelationProjection(t *testing.T, nullable bool) query.RelationProjection {
	t.Helper()
	field := "author"
	column := "author_id"
	if nullable {
		field = "reviewer"
		column = "reviewer_id"
	}
	projection, err := query.NewForwardRelationProjection(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post",
		query.NewFieldRef(field, column, query.FieldInteger, nullable),
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author",
		query.NewFieldRef("id", "id", query.FieldInteger, false),
		[]query.FieldRef{
			query.NewFieldRef("id", "id", query.FieldInteger, false),
			query.NewFieldRef("name", "name", query.FieldString, false),
		},
	)
	if err != nil {
		t.Fatalf("NewForwardRelationProjection() error = %v", err)
	}
	return projection
}

func assertInvalidProjectionPlanError(t *testing.T, err error) {
	t.Helper()
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Category != query.CategoryQuery || queryError.Code != query.CodeInvalidPlan {
		t.Fatalf("error = %T %v, want query_error/invalid_plan", err, err)
	}
}
