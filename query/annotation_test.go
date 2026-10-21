package query_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func TestAnnotationDefinitionsRetainRebindingAndBranchOwnership(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, true)
	base := query.NewPlan("measures", []query.FieldRef{field})
	first, original, err := base.WithAnnotation("adjusted", query.FieldResult(field), true)
	if err != nil {
		t.Fatal(err)
	}
	second, added, err := first.WithAnnotation("other", query.CountAllResult(), false)
	if err != nil {
		t.Fatal(err)
	}
	rebound, latest, err := second.WithAnnotation("adjusted", query.CountAllResult(), true)
	if err != nil {
		t.Fatal(err)
	}
	if base.OwnsAnnotation(original) || first.OwnsAnnotation(added) || second.OwnsAnnotation(latest) {
		t.Fatal("an annotation became visible before its definition")
	}
	if !rebound.OwnsAnnotation(original) || !rebound.OwnsAnnotation(added) || !rebound.OwnsAnnotation(latest) || original.Equal(latest) {
		t.Fatal("rebind replaced an earlier reference identity")
	}
	if got, _ := first.Annotation("adjusted"); !got.Equal(original) {
		t.Fatal("rebind changed the source query")
	}
	if got, _ := rebound.Annotation("adjusted"); !got.Equal(latest) {
		t.Fatal("name lookup did not select the new definition")
	}
	if original.ResultKind() != query.FieldInteger || !original.Nullable() || latest.Nullable() || added.Selected() || !latest.Selected() {
		t.Fatal("annotation lost result domain or selection intent")
	}
	definitions := rebound.Annotations()
	if len(definitions) != 2 || !definitions[0].Equal(latest) || !definitions[1].Equal(added) {
		t.Fatal("rebind changed declaration order")
	}
	definitions[0] = query.AnnotationRef{}
	if !rebound.Annotations()[0].Equal(latest) {
		t.Fatal("caller changed annotation storage")
	}
	_, foreign, err := base.WithAnnotation("adjusted", query.FieldResult(field), true)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.OwnsAnnotation(foreign) || foreign.Equal(original) {
		t.Fatal("equal definition shape granted foreign scope authority")
	}
	if rebound.OwnsAnnotation(query.AnnotationRef{}) || len(base.Annotations()) != 0 {
		t.Fatal("empty scope granted annotation authority")
	}
}

func TestAnnotationDefinitionCapturesSourceBeforeLaterFilters(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	base := query.NewPlan("measures", []query.FieldRef{field})
	before := querytest.Conditions(t, base, query.NewCondition(field, query.LookupGreaterThan, query.Integer(2)))
	annotated, ref, err := before.WithAnnotation("total", query.CountAllResult(), true)
	if err != nil {
		t.Fatal(err)
	}
	after := querytest.Conditions(t, annotated, query.NewCondition(field, query.LookupLessThan, query.Integer(8)))
	captured, valid := ref.Source()
	if !valid || !captured.Equal(before) || len(captured.Conditions()) != 1 || len(after.Conditions()) != 2 {
		t.Fatal("a later filter changed definition-time source")
	}
	changed := captured.WithDistinct()
	again, _ := ref.Source()
	if !again.Equal(before) || changed.Equal(before) || !after.OwnsAnnotation(ref) {
		t.Fatal("source accessor exposed mutable source state")
	}
	if annotated.Equal(before) || !annotated.Equal(annotated) {
		t.Fatal("plan equality ignored annotation scope")
	}
}

func TestAnnotationDefinitionsRejectInvalidNamesAndSourceMetadata(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "stored_amount", query.FieldInteger, false)
	base := query.NewPlan("measures", []query.FieldRef{field})
	for _, name := range []string{"", "amount", "1value", "bad name", "quote\"name", "comment--name", "path__value", "한글", strings.Repeat("x", 129)} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := base.WithAnnotation(name, query.FieldResult(field), true); err == nil {
				t.Fatal("invalid annotation name was accepted")
			}
		})
	}
	for _, value := range []query.ResultExpression{{}, query.FieldResult(query.NewFieldRef("missing", "missing", query.FieldInteger, false)), query.FieldResult(query.NewFieldRef("amount", "stored_amount", query.FieldFloat, false))} {
		if _, _, err := base.WithAnnotation("computed", value, true); err == nil {
			t.Fatal("invalid or mismatched source value was accepted")
		}
	}
	if _, _, err := (query.Plan{}).WithAnnotation("count", query.CountAllResult(), true); err == nil {
		t.Fatal("uninitialized source accepted an annotation")
	}
	if _, ok := base.Annotation("unknown"); ok {
		t.Fatal("unknown annotation resolved")
	}
	var zero query.AnnotationRef
	if zero.Valid() || zero.Name() != "" || zero.Selected() || zero.Nullable() || zero.ResultKind() != "" {
		t.Fatal("zero reference has a value")
	}
	if _, ok := zero.Expression(); ok {
		t.Fatal("zero reference exposed an expression")
	}
	if _, ok := zero.Source(); ok {
		t.Fatal("zero reference exposed a source")
	}
}

func TestAnnotationScopeRetainsAggregateResultDomain(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	base := query.NewPlan("measures", []query.FieldRef{field})
	mean, err := query.AvgResult(query.FieldResult(field))
	if err != nil {
		t.Fatal(err)
	}
	_, ref, err := base.WithAnnotation("mean", mean, true)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := ref.Expression()
	if !ok || !value.Equal(mean) || ref.ResultKind() != query.FieldFloat || !ref.Nullable() {
		t.Fatal("aggregate definition inherited its operand domain")
	}
	if len(base.SourceFields()) != 1 || base.SourceFields()[0] != field {
		t.Fatal("annotation changed persisted model metadata")
	}
}

func TestAnnotationScopeBoundsNamesAndRetainedHistory(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	base := query.NewPlan("measures", []query.FieldRef{field})
	names := base
	var err error
	for index := range query.MaximumAnnotationNames {
		names, _, err = names.WithAnnotation(fmt.Sprintf("value_%d", index), query.CountAllResult(), true)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := names.WithAnnotation("one_more", query.CountAllResult(), true); err == nil {
		t.Fatal("annotation name budget was not enforced")
	}
	if _, _, err := names.WithAnnotation("value_0", query.CountAllResult(), false); err != nil {
		t.Fatal("rebind unnecessarily consumed a new name", err)
	}
	history := base
	for range query.MaximumAnnotationBindings {
		history, _, err = history.WithAnnotation("value", query.CountAllResult(), true)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(history.Annotations()) != 1 {
		t.Fatal("history leaked into visible names")
	}
	if _, _, err := history.WithAnnotation("value", query.CountAllResult(), true); err == nil {
		t.Fatal("rebind bypassed the retained history budget")
	}
}

func TestAnnotationScopeBoundsExpressionAndCapturedPredicateCost(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	base := query.NewPlan("measures", []query.FieldRef{field})
	large, err := query.FieldExpression(field)
	if err != nil {
		t.Fatal(err)
	}
	for range 9 {
		large, err = query.Arithmetic(query.ArithmeticAdd, large, large)
		if err != nil {
			t.Fatal(err)
		}
	}
	value, err := query.ScalarResult(large)
	if err != nil {
		t.Fatal(err)
	}
	definitions := base
	// The total retained-query budget includes one source field and the plan
	// node as well as definitions, even when no result selects those aliases.
	for range (query.MaximumDerivedNodes - 2) / (large.NodeCount() + 1) {
		definitions, _, err = definitions.WithAnnotation("large", value, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := definitions.WithAnnotation("large", value, false); err == nil {
		t.Fatal("unused or rebound values bypassed the expression budget")
	}
	condition, err := query.NewScalarCondition(large, query.LookupGreaterThan, query.Integer(0))
	if err != nil {
		t.Fatal(err)
	}
	filtered := querytest.Conditions(t, base, condition)
	// Keep the live predicate and captured copies in the same total budget;
	// the implicit model group also retains its one source-field key.
	predicateNodes := large.NodeCount() + 1
	for range (query.MaximumDerivedNodes - 2 - predicateNodes - 1) / (predicateNodes + 1) {
		next, _, err := filtered.WithAnnotation("small", query.CountAllResult(), true)
		if err != nil {
			t.Fatal("captured-predicate budget rejected a valid prefix", err)
		}
		filtered = next
	}
	if _, _, err := filtered.WithAnnotation("small", query.CountAllResult(), true); err == nil {
		t.Fatal("retained definition-time predicates bypassed the scope budget")
	}
}

func TestAnnotationScopeDoesNotGrantAuthorityAcrossResultStages(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	base := query.NewPlan("measures", []query.FieldRef{field})
	annotated, original, err := base.WithAnnotation("value", query.FieldResult(field), true)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewProjectionResult(query.FieldResult(field))
	if err != nil {
		t.Fatal(err)
	}
	projected, err := annotated.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	derived, scope, err := query.NewDerivedPlan(projected)
	if err != nil {
		t.Fatal(err)
	}
	if derived.OwnsAnnotation(original) || len(derived.Annotations()) != 0 {
		t.Fatal("row annotation authority crossed a selected result stage")
	}
	column, err := scope.Column(0)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := query.SelectedExpression(column)
	if err != nil {
		t.Fatal(err)
	}
	value, err := query.ScalarResult(selected)
	if err != nil {
		t.Fatal(err)
	}
	later, ref, err := derived.WithAnnotation("value", value, true)
	if err != nil || !later.OwnsAnnotation(ref) || ref.Equal(original) {
		t.Fatal("selected result could not own a separate annotation", err)
	}
	foreign, _, err := query.NewDerivedPlan(projected)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := foreign.WithAnnotation("value", value, true); err == nil {
		t.Fatal("equal result shape admitted a foreign selected column")
	}
	if _, _, err := derived.WithAnnotation("original", query.FieldResult(field), true); err == nil {
		t.Fatal("annotation invented source metadata for an unselected row field")
	}
}

func TestAnnotationScopeSupportsConcurrentIndependentBranches(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	base := query.NewPlan("measures", []query.FieldRef{field})
	source, original, err := base.WithAnnotation("value", query.FieldResult(field), true)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 32 {
				branch, own, err := source.WithAnnotation("value", query.CountAllResult(), false)
				if err != nil || !branch.OwnsAnnotation(original) || !branch.OwnsAnnotation(own) || source.OwnsAnnotation(own) {
					t.Error("concurrent branch changed annotation authority", err)
					return
				}
				values := branch.Annotations()
				values[0] = query.AnnotationRef{}
				if current, _ := source.Annotation("value"); !current.Equal(original) {
					t.Error("concurrent branch changed a shared definition")
					return
				}
			}
		})
	}
	workers.Wait()
}
