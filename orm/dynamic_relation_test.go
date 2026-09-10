package orm_test

import (
	"testing"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestDynamicRelationsShareTypedASTAndOwnInput(t *testing.T) {
	t.Parallel()

	fixture := newRelationQueryFixture(t)
	inputs := []orm.LookupInput{
		{Key: "author__name", Value: "Ada"},
		{Key: "author__id", Value: int64(1)},
	}
	dynamic, err := orm.ParseDynamicRelations(fixture.postModel, nil, inputs)
	if err != nil {
		t.Fatalf("ParseDynamicRelations() error = %v", err)
	}
	typedPlan := orm.NewManager[relationQueryPost](fixture.postDescriptor).
		Using(nil).
		Filter(fixture.authorName.Exact("Ada"), fixture.authorID.Exact(1)).
		Plan()
	dynamicPlan := orm.NewManager[relationQueryPost](fixture.postDescriptor).
		Using(nil).
		Filter(dynamic...).
		Plan()
	if !typedPlan.Equal(dynamicPlan) {
		t.Fatalf("typed and dynamic plans differ:\ntyped=%#v\ndynamic=%#v", typedPlan, dynamicPlan)
	}

	inputs[0] = orm.LookupInput{Key: "missing__id", Value: int64(99)}
	if !typedPlan.Equal(dynamicPlan) {
		t.Fatal("dynamic plan retained caller input slice")
	}

	seenPolicy := false
	_, err = orm.ParseDynamicRelations(fixture.postModel, func(field ir.Field, lookup query.Lookup) bool {
		seenPolicy = field.Name == "name" && lookup == query.LookupExact
		return true
	}, []orm.LookupInput{{Key: "author__name", Value: "Ada"}})
	if err != nil || !seenPolicy {
		t.Fatalf("policy observation = %v, error = %v", seenPolicy, err)
	}
}

func TestDynamicRelationErrorsFollowFrozenPrecedence(t *testing.T) {
	t.Parallel()

	fixture := newRelationQueryFixture(t)
	tests := []struct {
		name     string
		model    any
		input    orm.LookupInput
		policy   orm.LookupPolicy
		category string
		code     string
	}{
		{name: "one segment", input: orm.LookupInput{Key: "author", Value: "Ada"}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "explicit exact suffix", input: orm.LookupInput{Key: "author__name__exact", Value: "Ada"}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "explicit nonexact suffix", input: orm.LookupInput{Key: "author__name__icontains", Value: "Ada"}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "relation exact suffix", input: orm.LookupInput{Key: "author__exact", Value: int64(1)}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "relation icontains suffix", input: orm.LookupInput{Key: "author__icontains", Value: "Ada"}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "relation isnull suffix", input: orm.LookupInput{Key: "author__isnull", Value: true}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "nullable relation isnull suffix", input: orm.LookupInput{Key: "reviewer__isnull", Value: true}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "lookup shape precedes unknown relation", input: orm.LookupInput{Key: "missing__isnull", Value: true}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "leading empty", input: orm.LookupInput{Key: "__name", Value: "Ada"}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "middle empty", input: orm.LookupInput{Key: "author____name", Value: "Ada"}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "trailing empty", input: orm.LookupInput{Key: "author__", Value: "Ada"}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "unknown relation", input: orm.LookupInput{Key: "missing__id", Value: int64(1)}, category: query.CategoryField, code: query.CodeUnknownRelation},
		{name: "nullable relation", input: orm.LookupInput{Key: "reviewer__id", Value: int64(1)}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "unknown related field", input: orm.LookupInput{Key: "author__missing", Value: int64(1)}, category: query.CategoryField, code: query.CodeUnknownRelatedField},
		{name: "invalid string value", input: orm.LookupInput{Key: "author__name", Value: 1}, category: query.CategoryField, code: query.CodeInvalidValue},
		{name: "invalid integer value", input: orm.LookupInput{Key: "author__id", Value: "1"}, category: query.CategoryField, code: query.CodeInvalidValue},
		{name: "policy rejected", input: orm.LookupInput{Key: "author__name", Value: "Ada"}, policy: func(ir.Field, query.Lookup) bool { return false }, category: query.CategoryField, code: query.CodeDisallowedLookup},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			predicates, err := orm.ParseDynamicRelations(fixture.postModel, test.policy, []orm.LookupInput{test.input})
			if predicates != nil {
				t.Fatalf("predicates = %#v, want nil on failure", predicates)
			}
			assertRelationQueryError(t, err, test.category, test.code)
		})
	}

	// Reverse namespaces are recognized as unsupported rather than reported as
	// unknown forward relations.
	reversePredicates, err := orm.ParseDynamicRelations(fixture.authorModel, nil, []orm.LookupInput{{Key: "posts__id", Value: int64(1)}})
	if reversePredicates != nil {
		t.Fatalf("reverse predicates = %#v, want nil", reversePredicates)
	}
	assertRelationQueryError(t, err, query.CategoryField, query.CodeUnsupportedLookup)

	// Bound-model validation precedes even malformed dynamic path validation.
	var zero orm.BoundModel[relationQueryPost]
	zeroPredicates, err := orm.ParseDynamicRelations(zero, nil, []orm.LookupInput{{Key: "bad", Value: nil}})
	if zeroPredicates != nil {
		t.Fatalf("zero-model predicates = %#v, want nil", zeroPredicates)
	}
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	// An error after a valid input never publishes a prefix.
	partialPredicates, err := orm.ParseDynamicRelations(fixture.postModel, nil, []orm.LookupInput{
		{Key: "author__name", Value: "Ada"},
		{Key: "author__missing", Value: "Ada"},
	})
	if partialPredicates != nil {
		t.Fatalf("partial predicates = %#v, want nil", partialPredicates)
	}
	assertRelationQueryError(t, err, query.CategoryField, query.CodeUnknownRelatedField)
}

func TestDynamicRelationObjectsAddsOnlyNullableIsNullAndIsAtomic(t *testing.T) {
	t.Parallel()

	fixture := newRelationQueryFixture(t)
	seenPolicy := false
	predicates, err := orm.ParseDynamicRelationObjects(fixture.postModel, func(field ir.Field, lookup query.Lookup) bool {
		seenPolicy = field.Name == "reviewer" && field.Column == "reviewer_id" &&
			field.Kind == ir.FieldForeignKey && field.Nullable && field.Relation != nil &&
			field.Relation.Target == (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) &&
			lookup == query.LookupIsNull
		field.Column = "caller-mutated"
		field.Relation.Target.AppLabel = "caller-mutated"
		return true
	}, []orm.LookupInput{{Key: "reviewer__isnull", Value: true}})
	if err != nil || !seenPolicy || len(predicates) != 1 {
		t.Fatalf("ParseDynamicRelationObjects() = (%#v, %v), policy=%v", predicates, err, seenPolicy)
	}
	plan := orm.NewManager[relationQueryPost](fixture.postDescriptor).Using(nil).Filter(predicates...).Plan()
	conditions := plan.Conditions()
	if len(conditions) != 1 || conditions[0].Lookup() != query.LookupIsNull {
		t.Fatalf("dynamic object conditions = %#v", conditions)
	}
	path, ok := conditions[0].RelationPath()
	if !ok || path.TerminalScope() != query.RelationTerminalSourceKey ||
		path.Terminal().Name() != "reviewer" || path.Terminal().Column() != "reviewer_id" ||
		path.Terminal().Kind() != query.FieldInteger || !path.Terminal().Nullable() {
		t.Fatalf("dynamic nullable path = (%#v, %v)", path, ok)
	}
	if got := path.Hops(); len(got) != 1 || !got[0].Nullable() || got[0].Field() != "reviewer" {
		t.Fatalf("dynamic nullable hops = %#v", got)
	}

	tests := []struct {
		name     string
		inputs   []orm.LookupInput
		policy   orm.LookupPolicy
		category string
		code     string
	}{
		{name: "required isnull", inputs: []orm.LookupInput{{Key: "author__isnull", Value: true}}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "nullable target traversal", inputs: []orm.LookupInput{{Key: "reviewer__name", Value: "Bob"}}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "invalid bool", inputs: []orm.LookupInput{{Key: "reviewer__isnull", Value: "true"}}, category: query.CategoryField, code: query.CodeInvalidValue},
		{name: "policy before value", inputs: []orm.LookupInput{{Key: "reviewer__isnull", Value: "true"}}, policy: func(ir.Field, query.Lookup) bool { return false }, category: query.CategoryField, code: query.CodeDisallowedLookup},
		{name: "mixed no partial", inputs: []orm.LookupInput{{Key: "author__name", Value: "Ada"}, {Key: "reviewer__isnull", Value: "true"}}, category: query.CategoryField, code: query.CodeInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := orm.ParseDynamicRelationObjects(fixture.postModel, test.policy, test.inputs)
			if got != nil {
				t.Fatalf("predicates = %#v, want nil", got)
			}
			assertRelationQueryError(t, err, test.category, test.code)
		})
	}
}

func TestDynamicReverseRelationsShareTypedASTAndPreserveNullableDeclaration(t *testing.T) {
	t.Parallel()

	fixture := newRelationQueryFixture(t)
	postMetadata, ok := fixture.binding.Model(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"})
	if !ok {
		t.Fatal("blog.post missing from binding")
	}
	posts, err := orm.BindReverse(fixture.authorModel, "posts", fixture.postModel)
	if err != nil {
		t.Fatalf("BindReverse(posts) error = %v", err)
	}
	postID, err := posts.Integer(orm.NewIntegerField[relationQueryPost](postMetadata.Fields[0]))
	if err != nil {
		t.Fatalf("ReverseRelation.Integer(id) error = %v", err)
	}

	dynamic, err := orm.ParseDynamicReverseRelations(
		fixture.authorModel,
		nil,
		[]orm.LookupInput{{Key: "posts__id", Value: int64(10)}},
	)
	if err != nil {
		t.Fatalf("ParseDynamicReverseRelations() error = %v", err)
	}
	typedPlan := orm.NewManager[relationQueryAuthor](fixture.authorDescriptor).
		Using(nil).
		Filter(postID.Exact(10)).
		Plan()
	dynamicPlan := orm.NewManager[relationQueryAuthor](fixture.authorDescriptor).
		Using(nil).
		Filter(dynamic...).
		Plan()
	if !typedPlan.Equal(dynamicPlan) {
		t.Fatalf("typed and dynamic reverse plans differ:\ntyped=%#v\ndynamic=%#v", typedPlan, dynamicPlan)
	}
	path, ok := dynamicPlan.Conditions()[0].RelationPath()
	if !ok || path.TerminalScope() != query.RelationTerminalRelatedField {
		t.Fatalf("dynamic reverse path = (%#v, %v)", path, ok)
	}
	hops := path.Hops()
	if len(hops) != 1 || hops[0].Direction() != query.RelationReverse ||
		hops[0].Cardinality() != ir.RelationOneToMany || hops[0].ReverseName() != "posts" ||
		hops[0].Nullable() {
		t.Fatalf("posts reverse hop = %#v", hops)
	}

	reviewed, err := orm.ParseDynamicReverseRelations(
		fixture.authorModel,
		nil,
		[]orm.LookupInput{{Key: "reviewed_posts__id", Value: 10}},
	)
	if err != nil {
		t.Fatalf("nullable ParseDynamicReverseRelations() error = %v", err)
	}
	reviewedPlan := orm.NewManager[relationQueryAuthor](fixture.authorDescriptor).
		Using(nil).
		Filter(reviewed...).
		Plan()
	reviewedPath, ok := reviewedPlan.Conditions()[0].RelationPath()
	if !ok || len(reviewedPath.Hops()) != 1 || !reviewedPath.Hops()[0].Nullable() ||
		reviewedPath.Hops()[0].ReverseName() != "reviewed_posts" {
		t.Fatalf("nullable reverse path = (%#v, %v)", reviewedPath, ok)
	}

	seenPolicy := false
	_, err = orm.ParseDynamicReverseRelations(
		fixture.authorModel,
		func(field ir.Field, lookup query.Lookup) bool {
			seenPolicy = field.Name == "id" && field.Kind == ir.FieldAuto && lookup == query.LookupExact
			field.Name = "caller_mutated"
			return true
		},
		[]orm.LookupInput{{Key: "posts__id", Value: int64(10)}},
	)
	if err != nil || !seenPolicy {
		t.Fatalf("reverse policy observation = %v, error = %v", seenPolicy, err)
	}
}

func TestDynamicReverseRelationErrorsFollowFrozenPrecedenceAndAreAtomic(t *testing.T) {
	t.Parallel()

	fixture := newRelationQueryFixture(t)
	tests := []struct {
		name     string
		input    orm.LookupInput
		policy   orm.LookupPolicy
		category string
		code     string
	}{
		{name: "one segment", input: orm.LookupInput{Key: "posts", Value: int64(10)}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "explicit suffix", input: orm.LookupInput{Key: "posts__id__exact", Value: int64(10)}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "relation suffix", input: orm.LookupInput{Key: "posts__exact", Value: int64(10)}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "unknown suffix precedence", input: orm.LookupInput{Key: "missing__isnull", Value: true}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "leading empty", input: orm.LookupInput{Key: "__id", Value: int64(10)}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "middle empty", input: orm.LookupInput{Key: "posts____id", Value: int64(10)}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "trailing empty", input: orm.LookupInput{Key: "posts__", Value: int64(10)}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "unknown namespace", input: orm.LookupInput{Key: "missing__id", Value: int64(10)}, category: query.CategoryField, code: query.CodeUnknownRelation},
		{name: "unknown terminal", input: orm.LookupInput{Key: "posts__missing", Value: int64(10)}, category: query.CategoryField, code: query.CodeUnknownRelatedField},
		{name: "foreign key terminal", input: orm.LookupInput{Key: "posts__author", Value: int64(1)}, category: query.CategoryField, code: query.CodeUnsupportedLookup},
		{name: "policy before value", input: orm.LookupInput{Key: "posts__id", Value: "10"}, policy: func(ir.Field, query.Lookup) bool { return false }, category: query.CategoryField, code: query.CodeDisallowedLookup},
		{name: "invalid value", input: orm.LookupInput{Key: "posts__id", Value: "10"}, category: query.CategoryField, code: query.CodeInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			predicates, err := orm.ParseDynamicReverseRelations(
				fixture.authorModel,
				test.policy,
				[]orm.LookupInput{test.input},
			)
			if predicates != nil {
				t.Fatalf("predicates = %#v, want nil", predicates)
			}
			assertRelationQueryError(t, err, test.category, test.code)
		})
	}

	var zero orm.BoundModel[relationQueryAuthor]
	got, err := orm.ParseDynamicReverseRelations(zero, nil, []orm.LookupInput{{Key: "bad", Value: nil}})
	if got != nil {
		t.Fatalf("zero predicates = %#v, want nil", got)
	}
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	got, err = orm.ParseDynamicReverseRelations(fixture.authorModel, nil, []orm.LookupInput{
		{Key: "posts__id", Value: int64(10)},
		{Key: "posts__missing", Value: int64(10)},
	})
	if got != nil {
		t.Fatalf("partial predicates = %#v, want nil", got)
	}
	assertRelationQueryError(t, err, query.CategoryField, query.CodeUnknownRelatedField)
}
