package orm_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/conformance/relationfixture/project"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestDynamicIExactValidatesTypesAndHonorsRootAndRelationPolicy(t *testing.T) {
	for _, input := range []orm.LookupInput{
		{Key: "title__iexact", Value: 1}, {Key: "summary__iexact", Value: nil},
		{Key: "title__iexact", Value: []string{"alpha"}}, {Key: "id__iexact", Value: "1"},
		{Key: "published__iexact", Value: "true"}, {Key: "title__iexact", Value: "a", JSONPath: []query.JSONPathSegment{query.JSONKey("a")}},
	} {
		if predicates, err := orm.ParseDynamic(models.ArticleDescriptor{}, nil, []orm.LookupInput{input}); err == nil || predicates != nil {
			t.Fatal("invalid dynamic iexact accepted", input.Key)
		}
	}
	seen := false
	policy := func(field ir.Field, lookup query.Lookup) bool {
		seen = field.Name == "title" && lookup == query.LookupIExact
		return false
	}
	if predicates, err := orm.ParseDynamic(models.ArticleDescriptor{}, policy, []orm.LookupInput{{Key: "title__iexact", Value: "alpha"}}); !errors.Is(err, &query.Error{Code: query.CodeDisallowedLookup}) || !seen || predicates != nil {
		t.Fatal("root policy bypassed", err)
	}
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []orm.LookupInput{
		{Key: "author__iexact", Value: "alpha"}, {Key: "reviewer__name__iexact", Value: nil},
		{Key: "author__id__iexact", Value: "1"}, {Key: "reviewer__name__iexact", Value: 1},
	} {
		if predicates, err := relations.BlogPost.ParseDynamic(nil, []orm.LookupInput{input}); err == nil || predicates != nil {
			t.Fatal("invalid relation iexact accepted", input.Key)
		}
	}
	seen = false
	policy = func(field ir.Field, lookup query.Lookup) bool {
		seen = field.Name == "name" && lookup == query.LookupIExact
		return false
	}
	if predicates, err := relations.BlogPost.ParseDynamic(policy, []orm.LookupInput{{Key: "reviewer__name__iexact", Value: "alpha"}}); !errors.Is(err, &query.Error{Code: query.CodeDisallowedLookup}) || !seen || predicates != nil {
		t.Fatal("relation policy bypassed", err)
	}
}
