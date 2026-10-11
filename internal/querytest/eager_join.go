package querytest

import (
	"database/sql"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"reflect"
	"testing"
)

// ConflictingEagerJoinPlans names distinct provenance attacks against a
// selected root. Each is also returned with LIMIT 0 to check validation before
// empty-result I/O elision. Tables intentionally need not exist.
func ConflictingEagerJoinPlans(t testing.TB) map[string]query.Plan {
	t.Helper()
	root := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	otherRoot := ir.ModelIdentity{AppLabel: "archive", ModelName: "post"}
	person := ir.ModelIdentity{AppLabel: "people", ModelName: "person"}
	otherPerson := ir.ModelIdentity{AppLabel: "people", ModelName: "other"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	author := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	reviewer := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	projection, err := query.NewForwardRelationProjection(root, "blog_post", author, person, "people_person", id, []query.FieldRef{id, name}, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	base, err := query.NewPlan("blog_post", []query.FieldRef{id, author, reviewer}).WithRelationProjections(projection)
	if err != nil {
		t.Fatal(err)
	}
	forward := func(source ir.ModelIdentity, field query.FieldRef, target ir.ModelIdentity, table, pk string) query.Condition {
		path, err := query.NewForwardRelationPath(source, "blog_post", field.Name(), field.Column(), target, table, pk, field.Nullable(), name, ir.RelationManyToOne)
		if err != nil {
			t.Fatal(err)
		}
		return query.NewRelatedCondition(path, query.LookupExact, query.String("Ada"))
	}
	presence := func(source, target ir.ModelIdentity, table string) query.Condition {
		path, err := query.NewForwardRelationIsNullPath(source, "blog_post", reviewer, target, table, "id", ir.RelationManyToOne)
		if err != nil {
			t.Fatal(err)
		}
		return query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(false))
	}
	reverse, err := query.NewReverseRelationPath(ir.ModelIdentity{AppLabel: "blog", ModelName: "comment"}, "blog_comment", "post", "post_id", otherRoot, "blog_post", "id", "comments", false, name, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	selfReverse := func(field query.FieldRef, table string) query.Condition {
		path, err := query.NewReverseRelationPath(root, table, field.Name(), field.Column(), root, "blog_post", "id", "children", field.Nullable(), id, ir.RelationOneToMany)
		if err != nil {
			t.Fatal(err)
		}
		return query.NewRelatedCondition(path, query.LookupExact, query.Integer(1))
	}
	cases := map[string][]query.Condition{
		"wrong forward root":                         {forward(otherRoot, reviewer, person, "people_person", "id")},
		"wrong reverse root":                         {query.NewRelatedCondition(reverse, query.LookupExact, query.String("Ada"))},
		"wrong source key root":                      {presence(otherRoot, person, "people_person")},
		"selected target identity":                   {forward(root, author, otherPerson, "people_person", "id")},
		"selected target table":                      {forward(root, author, person, "archived_person", "id")},
		"selected target primary key":                {forward(root, author, person, "people_person", "other_id")},
		"nonselected target identity":                {forward(root, reviewer, person, "people_person", "id"), forward(root, reviewer, otherPerson, "people_person", "id")},
		"nonselected source key identity":            {forward(root, reviewer, person, "people_person", "id"), presence(root, otherPerson, "people_person")},
		"two source key identities":                  {presence(root, person, "people_person"), presence(root, otherPerson, "people_person")},
		"self reverse conflicts with selected FK":    {selfReverse(author, "blog_post")},
		"self reverse conflicts with nonselected FK": {forward(root, reviewer, person, "people_person", "id"), selfReverse(reviewer, "blog_post")},
		"self reverse wrong source table":            {selfReverse(author, "archived_post")},
		"self reverse unknown source field":          {selfReverse(query.NewFieldRef("missing", "missing_id", query.FieldInteger, false), "blog_post")},
		"self reverse wrong source nullability":      {selfReverse(query.NewFieldRef("author", "author_id", query.FieldInteger, true), "blog_post")},
	}
	result := make(map[string]query.Plan, len(cases)*2)
	for name, conditions := range cases {
		plan := Conditions(t, base, conditions...)
		result[name] = plan
		empty, err := plan.WithLimit(0)
		if err != nil {
			t.Fatal(err)
		}
		result[name+" empty"] = empty
	}
	return result
}

// CheckSelfReferenceEagerRows verifies that opposite views of the same valid FK
// still compose. The caller supplies a real tree_node table with one root and
// matching child/grandchild rows; this is an invariant fixture, not an oracle.
func CheckSelfReferenceEagerRows(t testing.TB, backend db.Queryer) {
	t.Helper()
	root := ir.ModelIdentity{AppLabel: "tree", ModelName: "node"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	parent := query.NewFieldRef("parent", "parent_id", query.FieldInteger, true)
	fields := []query.FieldRef{id, name, parent}
	projection, err := query.NewForwardRelationProjection(root, "tree_node", parent, root, "tree_node", id, fields, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := query.NewReverseRelationPath(root, "tree_node", "parent", "parent_id", root, "tree_node", "id", "children", true, name, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("tree_node", fields).WithOrderings(query.NewOrdering(id, query.Ascending)).WithRelationProjections(projection)
	if err != nil {
		t.Fatal(err)
	}
	plan = Conditions(t, plan, query.NewRelatedCondition(reverse, query.LookupExact, query.String("child")))
	rows, err := backend.Query(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	ids := []int64{}
	for rows.Next() {
		var sourceID int64
		var sourceName string
		var sourceParent, targetID, targetParent sql.NullInt64
		var targetName sql.NullString
		if err := rows.Scan(&sourceID, &sourceName, &sourceParent, &targetID, &targetName, &targetParent); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sourceID)
		if sourceID == 1 {
			if sourceName != "root" || sourceParent.Valid || targetID.Valid || targetName.Valid || targetParent.Valid {
				t.Fatal("NULL self projection corrupted")
			}
		} else if sourceID != 2 || sourceName != "child" || !sourceParent.Valid || sourceParent.Int64 != 1 || !targetID.Valid || targetID.Int64 != 1 || !targetName.Valid || targetName.String != "root" || targetParent.Valid {
			t.Fatal("present self projection corrupted")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []int64{1, 2, 2}) {
		t.Fatalf("self reverse multiplicity=%v", ids)
	}
}

// ConflictingMultipleEagerPlans also selects the second edge, so validation
// cannot treat a conflicting selected target as an unrelated filter-only edge.
func ConflictingMultipleEagerPlans(t testing.TB) map[string]query.Plan {
	t.Helper()
	root := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	person := ir.ModelIdentity{AppLabel: "people", ModelName: "person"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	key := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	columns := []query.FieldRef{id, query.NewFieldRef("name", "name", query.FieldString, false)}
	projection, err := query.NewForwardRelationProjection(root, "blog_post", key, person, "people_person", id, columns, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	result := ConflictingEagerJoinPlans(t)
	for name, plan := range result {
		result[name], err = plan.WithRelationProjections(projection)
		if err != nil {
			t.Fatal(err)
		}
	}
	missing, err := query.NewForwardRelationProjection(root, "blog_post", query.NewFieldRef("missing", "missing_id", query.FieldInteger, true), person, "people_person", id, columns, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("blog_post", []query.FieldRef{id, key}).WithRelationProjections(projection, missing)
	if err != nil {
		t.Fatal(err)
	}
	result["selected unknown source key"] = plan
	empty, err := plan.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	result["selected unknown source key empty"] = empty
	return result
}
