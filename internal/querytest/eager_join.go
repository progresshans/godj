package querytest

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
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
	projection, err := query.NewForwardRelationProjection(root, "blog_post", author, person, "people_person", id, []query.FieldRef{id, name})
	if err != nil {
		t.Fatal(err)
	}
	base, err := query.NewPlan("blog_post", []query.FieldRef{id, author, reviewer}).WithRelationProjection(projection)
	if err != nil {
		t.Fatal(err)
	}
	forward := func(source ir.ModelIdentity, field query.FieldRef, target ir.ModelIdentity, table, pk string) query.Condition {
		path, err := query.NewForwardRelationPath(source, "blog_post", field.Name(), field.Column(), target, table, pk, field.Nullable(), name)
		if err != nil {
			t.Fatal(err)
		}
		return query.NewRelatedCondition(path, query.LookupExact, query.String("Ada"))
	}
	presence := func(source, target ir.ModelIdentity, table string) query.Condition {
		path, err := query.NewNullableForwardRelationIsNullPath(source, "blog_post", reviewer, target, table, "id")
		if err != nil {
			t.Fatal(err)
		}
		return query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(false))
	}
	reverse, err := query.NewReverseRelationPath(ir.ModelIdentity{AppLabel: "blog", ModelName: "comment"}, "blog_comment", "post", "post_id", otherRoot, "blog_post", "id", "comments", false, name)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]query.Condition{
		"wrong forward root":              {forward(otherRoot, reviewer, person, "people_person", "id")},
		"wrong reverse root":              {query.NewRelatedCondition(reverse, query.LookupExact, query.String("Ada"))},
		"wrong source key root":           {presence(otherRoot, person, "people_person")},
		"selected target identity":        {forward(root, author, otherPerson, "people_person", "id")},
		"selected target table":           {forward(root, author, person, "archived_person", "id")},
		"selected target primary key":     {forward(root, author, person, "people_person", "other_id")},
		"nonselected target identity":     {forward(root, reviewer, person, "people_person", "id"), forward(root, reviewer, otherPerson, "people_person", "id")},
		"nonselected source key identity": {forward(root, reviewer, person, "people_person", "id"), presence(root, otherPerson, "people_person")},
		"two source key identities":       {presence(root, person, "people_person"), presence(root, otherPerson, "people_person")},
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
