package query_test

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func calendarFunction(t *testing.T, function query.FunctionKind, argument query.ScalarExpression) query.ScalarExpression {
	t.Helper()
	value, err := query.FunctionExpression(function, argument)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCalendarFunctionSignaturesAndImmutableArguments(t *testing.T) {
	for _, test := range []struct {
		name     string
		function query.FunctionKind
		kinds    []query.FieldKind
		result   query.FieldKind
	}{
		{"date", query.FunctionDate, []query.FieldKind{query.FieldDateTime}, query.FieldDate},
		{"time", query.FunctionTime, []query.FieldKind{query.FieldDateTime}, query.FieldTime},
		{"year", query.FunctionYear, []query.FieldKind{query.FieldDateTime, query.FieldDate}, query.FieldInteger},
		{"iso_year", query.FunctionISOYear, []query.FieldKind{query.FieldDateTime, query.FieldDate}, query.FieldInteger},
		{"quarter", query.FunctionQuarter, []query.FieldKind{query.FieldDateTime, query.FieldDate}, query.FieldInteger},
		{"month", query.FunctionMonth, []query.FieldKind{query.FieldDateTime, query.FieldDate}, query.FieldInteger},
		{"week", query.FunctionWeek, []query.FieldKind{query.FieldDateTime, query.FieldDate}, query.FieldInteger},
		{"week_day", query.FunctionWeekDay, []query.FieldKind{query.FieldDateTime, query.FieldDate}, query.FieldInteger},
		{"iso_week_day", query.FunctionISOWeekDay, []query.FieldKind{query.FieldDateTime, query.FieldDate}, query.FieldInteger},
		{"day", query.FunctionDay, []query.FieldKind{query.FieldDateTime, query.FieldDate}, query.FieldInteger},
		{"hour", query.FunctionHour, []query.FieldKind{query.FieldDateTime, query.FieldTime}, query.FieldInteger},
		{"minute", query.FunctionMinute, []query.FieldKind{query.FieldDateTime, query.FieldTime}, query.FieldInteger},
		{"second", query.FunctionSecond, []query.FieldKind{query.FieldDateTime, query.FieldTime}, query.FieldInteger},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, kind := range []query.FieldKind{query.FieldDateTime, query.FieldDate, query.FieldTime, query.FieldDuration, query.FieldInteger, query.FieldString, query.FieldJSON} {
				allowed := false
				for _, input := range test.kinds {
					allowed = allowed || kind == input
				}
				for _, nullable := range []bool{false, true} {
					field := query.NewFieldRef("original", "original", kind, nullable)
					argument, err := query.FieldExpression(field)
					if err != nil {
						t.Fatal(err)
					}
					arguments := []query.ScalarExpression{argument}
					value, err := query.FunctionExpression(test.function, arguments...)
					if !allowed {
						if err == nil {
							t.Fatal("invalid function input was coerced", kind)
						}
						continue
					}
					if err != nil || value.Kind() != query.ScalarFunction || value.ResultKind() != test.result || value.Nullable() != nullable {
						t.Fatal("function changed result/null domain", kind, err)
					}
					arguments[0] = query.ScalarExpression{}
					function, detached, ok := value.Function()
					if !ok || function != test.function || len(detached) != 1 || !detached[0].Equal(argument) || detached[0].ResultKind() != kind {
						t.Fatal("function lost its operand domain or retained the caller's container")
					}
					detached[0] = query.ScalarExpression{}
					copy := calendarFunction(t, test.function, argument)
					if !value.Equal(copy) || value.Depth() != 2 || value.NodeCount() != 2 || value.RequiresDurationRounding() {
						t.Fatal("function copy changed value or shape")
					}
					if fields := value.Fields(); !reflect.DeepEqual(fields, []query.FieldRef{field}) {
						t.Fatal("result kind replaced source metadata", fields)
					}
				}
				if allowed {
					null := calendarFunction(t, test.function, temporalNull(t, kind))
					if !null.Nullable() || null.ResultKind() != test.result || len(null.Fields()) != 0 {
						t.Fatal("typed NULL acquired a value or source field")
					}
				}
			}
			if _, err := query.FunctionExpression(test.function, scalarLiteral(t, query.Null())); err == nil {
				t.Fatal("untyped NULL inferred a temporal input kind")
			}
		})
	}
	value := temporalNull(t, query.FieldDateTime)
	for _, invalid := range []query.FunctionKind{0, 255} {
		if _, err := query.FunctionExpression(invalid, value); err == nil {
			t.Fatal("unknown function accepted")
		}
	}
	for _, arguments := range [][]query.ScalarExpression{nil, {value, value}, {{}}} {
		if _, err := query.FunctionExpression(query.FunctionYear, arguments...); err == nil {
			t.Fatal("invalid arity or zero operand accepted")
		}
	}
	if calendarFunction(t, query.FunctionYear, value).Equal(calendarFunction(t, query.FunctionISOYear, value)) {
		t.Fatal("function identity collapsed equal result kinds")
	}
}

func TestCalendarFunctionsKeepSourceCardinalityAndResultScope(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	at := query.NewFieldRef("at", "at", query.FieldDateTime, true)
	value, _ := query.FieldExpression(at)
	day := calendarFunction(t, query.FunctionDate, value)
	plan := query.NewPlan("events", []query.FieldRef{id, at})
	if err := plan.ValidateScalar(day); err != nil {
		t.Fatal(err)
	}
	if err := day.ValidateSource([]query.FieldRef{id, query.NewFieldRef("at", "at", query.FieldDate, true)}); err == nil {
		t.Fatal("function result kind replaced source authority")
	}
	shape, err := query.NewProjectionResult(query.FieldResult(at))
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := query.NewResultScope(shape)
	other, _ := query.NewResultScope(shape)
	selected := calendarFunction(t, query.FunctionDate, selectedScalar(t, scope, 0))
	if err := selected.ValidateResultSource(scope); err != nil {
		t.Fatal(err)
	}
	if err := selected.ValidateResultSource(other); err == nil {
		t.Fatal("function accepted an equal-shaped foreign result scope")
	}
	if err := day.ValidateResultSource(scope); err == nil {
		t.Fatal("original field crossed a result stage inside a function")
	}
	if err := plan.ValidateScalar(selected); err == nil {
		t.Fatal("selected function crossed back into model rows")
	}
	if _, err := query.NewScalarAssignment(query.NewFieldRef("day", "day", query.FieldDate, true), selected); err == nil {
		t.Fatal("function erased selected-result write restrictions")
	}
	selectedResult, _ := query.ScalarResult(selected)
	selectedShape, _ := query.NewProjectionResult(selectedResult)
	next, err := query.NewResultScope(selectedShape)
	if err != nil || next.Depth() != scope.Depth()+1 {
		t.Fatal("function erased result-stage depth", err)
	}
	root := ir.ModelIdentity{AppLabel: "app", ModelName: "schedule"}
	target := ir.ModelIdentity{AppLabel: "app", ModelName: "event"}
	path, err := query.NewReverseRelationPath(target, "events", "schedule", "schedule_id", root, "schedules", "id", "events", false, at, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	related, err := query.RelatedExpression(path)
	if err != nil {
		t.Fatal(err)
	}
	collection := calendarFunction(t, query.FunctionYear, related)
	rootPlan := query.NewPlan("schedules", []query.FieldRef{id})
	if err := rootPlan.ValidateAggregateScalar(collection); err != nil {
		t.Fatal(err)
	}
	if !collection.HasRelations() || len(collection.RelationPaths()) != 1 || len(collection.Fields()) != 0 {
		t.Fatal("function lost relation provenance or invented a root field")
	}
	if _, err := query.ScalarAggregateResult(query.ResultSum, collection); err != nil {
		t.Fatal("completed collection function aggregate was rejected", err)
	}
	if err := rootPlan.ValidateScalar(collection); err == nil {
		t.Fatal("collection function became a row scalar")
	}
	if _, err := query.NewScalarAssignment(id, collection); err == nil {
		t.Fatal("collection function became a same-row assignment")
	}
	if err := query.NewPlan("foreign", []query.FieldRef{id}).ValidateAggregateScalar(collection); err == nil {
		t.Fatal("function accepted foreign relation authority")
	}
	paths := collection.RelationPaths()
	paths[0] = query.RelationPath{}
	if !collection.RelationPaths()[0].Equal(path) {
		t.Fatal("function traversal exposed relation storage")
	}
}

func TestCalendarFunctionResourceBudgets(t *testing.T) {
	at := query.NewFieldRef("at", "at", query.FieldDateTime, false)
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	base, _ := query.FieldExpression(at)
	predicate, err := query.NewExpression(query.NewCondition(id, query.LookupExact, query.Integer(1)))
	if err != nil {
		t.Fatal(err)
	}
	branch, _ := query.WhenScalar(predicate, base)
	value := base
	for value.Depth() < query.MaximumScalarDepth-1 {
		value, err = query.CaseScalar(value, branch)
		if err != nil {
			t.Fatal(err)
		}
	}
	if function := calendarFunction(t, query.FunctionYear, value); function.Depth() != query.MaximumScalarDepth {
		t.Fatal("last valid function depth was lost")
	}
	value, err = query.CaseScalar(value, branch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.FunctionExpression(query.FunctionYear, value); err == nil {
		t.Fatal("function bypassed scalar depth budget")
	}
	branches := make([]query.ScalarWhen, (query.MaximumScalarNodes-2)/2)
	for index := range branches {
		branches[index] = branch
	}
	value, err = query.CaseScalar(base, branches...)
	if err != nil || value.NodeCount() != query.MaximumScalarNodes {
		t.Fatal("test failed to reach its exact node boundary", value.NodeCount(), err)
	}
	if _, err := query.FunctionExpression(query.FunctionYear, value); err == nil {
		t.Fatal("function bypassed scalar node budget")
	}
}
