package query_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestExpressionCanonicalizesSameKindConnectorsInCallerOrder(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	a := expressionLeaf(t, query.NewCondition(title, query.LookupExact, query.String("a")))
	b := expressionLeaf(t, query.NewCondition(title, query.LookupExact, query.String("b")))
	c := expressionLeaf(t, query.NewCondition(title, query.LookupExact, query.String("c")))
	d := expressionLeaf(t, query.NewCondition(title, query.LookupExact, query.String("d")))

	ab, err := query.AndExpressions(a, b)
	if err != nil {
		t.Fatalf("AndExpressions(a, b) error = %v", err)
	}
	nestedAnd, err := query.AndExpressions(ab, c, d)
	if err != nil {
		t.Fatalf("AndExpressions(ab, c, d) error = %v", err)
	}
	directAnd, err := query.AndExpressions(a, b, c, d)
	if err != nil {
		t.Fatalf("AndExpressions(a, b, c, d) error = %v", err)
	}
	if nestedAnd.Kind() != query.ExpressionAnd || !nestedAnd.Equal(directAnd) {
		t.Fatalf("flattened AND = kind %v equal %v", nestedAnd.Kind(), nestedAnd.Equal(directAnd))
	}
	assertExpressionLeafStrings(t, nestedAnd.Children(), []string{"a", "b", "c", "d"})

	bc, err := query.OrExpressions(b, c)
	if err != nil {
		t.Fatalf("OrExpressions(b, c) error = %v", err)
	}
	nestedOr, err := query.OrExpressions(a, bc, d)
	if err != nil {
		t.Fatalf("OrExpressions(a, bc, d) error = %v", err)
	}
	directOr, err := query.OrExpressions(a, b, c, d)
	if err != nil {
		t.Fatalf("OrExpressions(a, b, c, d) error = %v", err)
	}
	if nestedOr.Kind() != query.ExpressionOr || !nestedOr.Equal(directOr) {
		t.Fatalf("flattened OR = kind %v equal %v", nestedOr.Kind(), nestedOr.Equal(directOr))
	}
	assertExpressionLeafStrings(t, nestedOr.Children(), []string{"a", "b", "c", "d"})

	grouped, err := query.AndExpressions(a, bc)
	if err != nil {
		t.Fatalf("AndExpressions(a, bc) error = %v", err)
	}
	groupedChildren := grouped.Children()
	if len(groupedChildren) != 2 || groupedChildren[1].Kind() != query.ExpressionOr ||
		len(groupedChildren[1].Children()) != 2 {
		t.Fatalf("opposite connector was flattened: %#v", groupedChildren)
	}
	reordered, err := query.AndExpressions(b, a, c, d)
	if err != nil {
		t.Fatalf("AndExpressions(b, a, c, d) error = %v", err)
	}
	if nestedAnd.Equal(reordered) {
		t.Fatal("ordered AND expressions compare equal after child reordering")
	}
}

func TestExpressionNotIsUnaryAndPreservesSourceStructure(t *testing.T) {
	t.Parallel()

	field := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	leaf := expressionLeaf(t, query.NewCondition(field, query.LookupExact, query.Boolean(true)))
	first, err := query.NotExpression(leaf)
	if err != nil {
		t.Fatalf("NotExpression(leaf) error = %v", err)
	}
	second, err := query.NotExpression(first)
	if err != nil {
		t.Fatalf("NotExpression(first) error = %v", err)
	}
	if first.Kind() != query.ExpressionNot || second.Kind() != query.ExpressionNot {
		t.Fatalf("NOT kinds = %v, %v", first.Kind(), second.Kind())
	}
	firstChildren := first.Children()
	secondChildren := second.Children()
	if len(firstChildren) != 1 || !firstChildren[0].Equal(leaf) ||
		len(secondChildren) != 1 || !secondChildren[0].Equal(first) {
		t.Fatalf("NOT structure = first %#v second %#v", firstChildren, secondChildren)
	}
	if second.Equal(leaf) {
		t.Fatal("double NOT was simplified away")
	}
}

func TestExpressionAccessorsDoNotExposeMutableStorage(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	input := []query.Value{query.Integer(1), query.Integer(2), query.Integer(3)}
	inCondition, err := query.NewInCondition(id, input)
	if err != nil {
		t.Fatalf("NewInCondition() error = %v", err)
	}
	leaf := expressionLeaf(t, inCondition)
	input[0] = query.Integer(999)

	returnedCondition, ok := leaf.Condition()
	if !ok {
		t.Fatal("leaf Condition() reports false")
	}
	returnedValues, ok := returnedCondition.Values()
	if !ok || len(returnedValues) != 3 {
		t.Fatalf("leaf Values() = (%#v, %v)", returnedValues, ok)
	}
	returnedValues[0] = query.Integer(888)
	again, ok := leaf.Condition()
	if !ok {
		t.Fatal("second leaf Condition() reports false")
	}
	againValues, ok := again.Values()
	if !ok {
		t.Fatal("second leaf Values() reports false")
	}
	first, firstOK := againValues[0].Integer()
	if !firstOK || first != 1 {
		t.Fatalf("Condition() exposed leaf storage: (%d, %v)", first, firstOK)
	}

	other := expressionLeaf(t, query.NewCondition(id, query.LookupExact, query.Integer(4)))
	combined, err := query.AndExpressions(leaf, other)
	if err != nil {
		t.Fatalf("AndExpressions() error = %v", err)
	}
	children := combined.Children()
	children[0] = other
	canonical := combined.Children()
	if len(canonical) != 2 || !canonical[0].Equal(leaf) || !canonical[1].Equal(other) {
		t.Fatalf("Children() exposed connector storage: %#v", canonical)
	}
	if condition, present := combined.Condition(); present || condition != (query.Condition{}) {
		t.Fatalf("connector Condition() = (%#v, %v), want zero/false", condition, present)
	}
	if children := leaf.Children(); children != nil {
		t.Fatalf("leaf Children() = %#v, want nil", children)
	}
}

func TestExpressionTracksRelationProvenanceThroughConnectors(t *testing.T) {
	t.Parallel()

	rootID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	authorName := query.NewFieldRef("name", "name", query.FieldString, false)
	path, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post",
		"author",
		"author_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author",
		"id",
		false,
		authorName,
	)
	if err != nil {
		t.Fatalf("NewForwardRelationPath() error = %v", err)
	}
	related := expressionLeaf(t, query.NewRelatedCondition(path, query.LookupExact, query.String("Ada")))
	scalar := expressionLeaf(t, query.NewCondition(rootID, query.LookupExact, query.Integer(7)))
	if !related.HasRelations() || scalar.HasRelations() {
		t.Fatalf("leaf relation flags = related %v scalar %v", related.HasRelations(), scalar.HasRelations())
	}
	combined, err := query.OrExpressions(scalar, related)
	if err != nil {
		t.Fatalf("OrExpressions() error = %v", err)
	}
	negated, err := query.NotExpression(combined)
	if err != nil {
		t.Fatalf("NotExpression() error = %v", err)
	}
	if !combined.HasRelations() || !negated.HasRelations() {
		t.Fatalf("connector relation flags = OR %v NOT %v", combined.HasRelations(), negated.HasRelations())
	}
	condition, ok := related.Condition()
	if !ok {
		t.Fatal("related leaf Condition() reports false")
	}
	returnedPath, ok := condition.RelationPath()
	if !ok || !returnedPath.Equal(path) {
		t.Fatalf("related Condition() path = (%#v, %v)", returnedPath, ok)
	}
}

func TestExpressionEnforcesDepthAndNodeLimits(t *testing.T) {
	t.Parallel()

	field := query.NewFieldRef("title", "title", query.FieldString, false)
	leaf := expressionLeaf(t, query.NewCondition(field, query.LookupExact, query.String("bounded")))

	maximumDepth := leaf
	for depth := 2; depth <= 64; depth++ {
		var err error
		maximumDepth, err = query.NotExpression(maximumDepth)
		if err != nil {
			t.Fatalf("NotExpression() at depth %d error = %v", depth, err)
		}
	}
	if _, err := query.NotExpression(maximumDepth); !isInvalidPlan(err) {
		t.Fatalf("NotExpression() beyond depth 64 error = %v, want invalid_plan", err)
	}

	rest := make([]query.Expression, 1021)
	for index := range rest {
		rest[index] = leaf
	}
	maximumNodes, err := query.AndExpressions(leaf, leaf, rest...)
	if err != nil {
		t.Fatalf("AndExpressions() at 1024 nodes error = %v", err)
	}
	if got := len(maximumNodes.Children()); got != 1023 {
		t.Fatalf("maximum-node child count = %d, want 1023", got)
	}
	if _, err := query.AndExpressions(maximumNodes, leaf); !isInvalidPlan(err) {
		t.Fatalf("AndExpressions() beyond 1024 nodes error = %v, want invalid_plan", err)
	}
	if _, err := query.NotExpression(maximumNodes); !isInvalidPlan(err) {
		t.Fatalf("NotExpression() beyond 1024 nodes error = %v, want invalid_plan", err)
	}

	tooManyOperands := make([]query.Expression, 1022)
	for index := range tooManyOperands {
		tooManyOperands[index] = leaf
	}
	if _, err := query.AndExpressions(leaf, leaf, tooManyOperands...); !isInvalidPlan(err) {
		t.Fatalf("AndExpressions() with 1024 operands error = %v, want invalid_plan", err)
	}
}

func TestExpressionRejectsZeroAndMalformedInputs(t *testing.T) {
	t.Parallel()

	stringField := query.NewFieldRef("title", "title", query.FieldString, false)
	integerField := query.NewFieldRef("id", "id", query.FieldInteger, false)
	malformed := []struct {
		name      string
		condition query.Condition
	}{
		{name: "zero"},
		{name: "empty name", condition: query.NewCondition(query.NewFieldRef("", "title", query.FieldString, false), query.LookupExact, query.String("x"))},
		{name: "empty column", condition: query.NewCondition(query.NewFieldRef("title", "", query.FieldString, false), query.LookupExact, query.String("x"))},
		{name: "NUL name", condition: query.NewCondition(query.NewFieldRef("title\x00", "title", query.FieldString, false), query.LookupExact, query.String("x"))},
		{name: "unknown field kind", condition: query.NewCondition(query.NewFieldRef("score", "score", query.FieldKind("float"), false), query.LookupExact, query.Integer(1))},
		{name: "exact kind mismatch", condition: query.NewCondition(stringField, query.LookupExact, query.Integer(1))},
		{name: "icontains non-string field", condition: query.NewCondition(integerField, query.LookupIContains, query.String("1"))},
		{name: "isnull non-Boolean value", condition: query.NewCondition(stringField, query.LookupIsNull, query.String("true"))},
		{name: "scalar IN without list storage", condition: query.NewCondition(integerField, query.LookupIn, query.Integer(1))},
		{name: "unknown lookup", condition: query.NewCondition(stringField, query.Lookup("starts"), query.String("x"))},
		{name: "zero relation path", condition: query.NewRelatedCondition(query.RelationPath{}, query.LookupExact, query.Value{})},
	}
	for _, test := range malformed {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			expression, err := query.NewExpression(test.condition)
			if !isInvalidPlan(err) {
				t.Fatalf("NewExpression() error = %v, want query_error/invalid_plan", err)
			}
			if expression != (query.Expression{}) {
				t.Fatalf("failed NewExpression() = %#v, want zero", expression)
			}
		})
	}

	valid := expressionLeaf(t, query.NewCondition(stringField, query.LookupExact, query.String("valid")))
	zero := query.Expression{}
	for name, run := range map[string]func() error{
		"AND left": func() error {
			_, err := query.AndExpressions(zero, valid)
			return err
		},
		"AND right": func() error {
			_, err := query.AndExpressions(valid, zero)
			return err
		},
		"OR left": func() error {
			_, err := query.OrExpressions(zero, valid)
			return err
		},
		"OR right": func() error {
			_, err := query.OrExpressions(valid, zero)
			return err
		},
		"NOT": func() error {
			_, err := query.NotExpression(zero)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := run(); !isInvalidPlan(err) {
				t.Fatalf("constructor error = %v, want query_error/invalid_plan", err)
			}
		})
	}
	if zero.Kind() != 0 || zero.HasRelations() {
		t.Fatalf("zero accessors = kind %v relations %v", zero.Kind(), zero.HasRelations())
	}
	if condition, ok := zero.Condition(); ok || condition != (query.Condition{}) {
		t.Fatalf("zero Condition() = (%#v, %v)", condition, ok)
	}
	if children := zero.Children(); children != nil {
		t.Fatalf("zero Children() = %#v, want nil", children)
	}
	if !zero.Equal(query.Expression{}) || zero.Equal(valid) {
		t.Fatalf("zero equality = zero %v valid %v", zero.Equal(query.Expression{}), zero.Equal(valid))
	}
}

func TestExpressionConcurrentAccessRetainsImmutableValues(t *testing.T) {
	t.Parallel()

	field := query.NewFieldRef("title", "title", query.FieldString, false)
	a := expressionLeaf(t, query.NewCondition(field, query.LookupExact, query.String("a")))
	b := expressionLeaf(t, query.NewCondition(field, query.LookupExact, query.String("b")))
	expression, err := query.OrExpressions(a, b)
	if err != nil {
		t.Fatalf("OrExpressions() error = %v", err)
	}

	const goroutines = 32
	const iterations = 100
	failures := make(chan error, goroutines)
	var wait sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				children := expression.Children()
				if expression.Kind() != query.ExpressionOr || expression.HasRelations() || len(children) != 2 ||
					!children[0].Equal(a) || !children[1].Equal(b) {
					failures <- fmt.Errorf("expression changed: kind=%v relations=%v children=%#v", expression.Kind(), expression.HasRelations(), children)
					return
				}
				children[0] = b
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}

func expressionLeaf(t *testing.T, condition query.Condition) query.Expression {
	t.Helper()
	expression, err := query.NewExpression(condition)
	if err != nil {
		t.Fatalf("NewExpression() error = %v", err)
	}
	if expression.Kind() != query.ExpressionLeaf {
		t.Fatalf("NewExpression() kind = %v, want leaf", expression.Kind())
	}
	return expression
}

func assertExpressionLeafStrings(t *testing.T, expressions []query.Expression, want []string) {
	t.Helper()
	if len(expressions) != len(want) {
		t.Fatalf("expression count = %d, want %d", len(expressions), len(want))
	}
	for index, expression := range expressions {
		condition, ok := expression.Condition()
		if !ok {
			t.Fatalf("expression %d is not a leaf", index)
		}
		value, ok := condition.Value().String()
		if !ok || value != want[index] {
			t.Fatalf("expression %d value = (%q, %v), want %q", index, value, ok, want[index])
		}
	}
}

func isInvalidPlan(err error) bool {
	return errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan})
}
