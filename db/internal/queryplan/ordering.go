package queryplan

import (
	"github.com/progresshans/godj/query"
	"strconv"
	"strings"
)

func HasOrderingRelations(plan query.Plan) bool {
	for _, ordering := range plan.Orderings() {
		if _, related := ordering.Expression().RelationPath(); related {
			return true
		}
	}
	return false
}

// SelectedRows describes the physical model/projection cells in scan order.
// COUNT's derived row source has the same root cells as a model result.
func SelectedRows(plan query.Plan) ([]query.ResultExpression, error) {
	if plan.ResultShape().Kind() == query.ResultProjection {
		return ProjectionExpressions(plan.ResultShape(), plan.SourceFields())
	}
	selected := FieldExpressions(plan.SourceFields())
	for _, projection := range plan.RelationProjections() {
		expressions, err := projection.TargetExpressions()
		if err != nil {
			return nil, err
		}
		selected = append(selected, expressions...)
	}
	if plan.ResultShape().Kind() == query.ResultPrefetch {
		selected = append(selected, plan.ResultShape().Expressions()...)
	}
	return selected, nil
}

// HiddenOrderings keeps full-model DISTINCT cardinality without adding cells to
// its public row shape. Every supported ordering is determined by root fields:
// a root value/path or a finite forward route rooted in a selected FK. DTO
// DISTINCT remains strict: each ordered value must be explicitly selected.
func HiddenOrderings(plan query.Plan, selected []query.ResultExpression) ([]query.ResultExpression, error) {
	if !plan.Distinct() {
		return nil, nil
	}
	var hidden []query.ResultExpression
	for _, ordering := range plan.Orderings() {
		expression := ordering.Expression()
		if ExpressionIndex(selected, expression) >= 0 {
			continue
		}
		if plan.ResultShape().Kind() == query.ResultProjection {
			return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported,
				Field: ordering.Field().Name(), Detail: "DISTINCT projection requires every ordering value in its result shape"}
		}
		if ExpressionIndex(hidden, expression) < 0 {
			hidden = append(hidden, expression)
		}
	}
	return hidden, nil
}

func ExpressionIndex(expressions []query.ResultExpression, target query.ResultExpression) int {
	for index, expression := range expressions {
		if expression.Equal(target) {
			return index
		}
	}
	return -1
}

// WrapHiddenOrderings exposes exactly the original cells and their column names.
// Internal aliases are positional, so eager targets may repeat root names.
func WrapHiddenOrderings(inner string, selected []query.ResultExpression, quote func(string) (string, error)) (string, error) {
	var sql strings.Builder
	sql.WriteString("SELECT ")
	for i, expression := range selected {
		if i > 0 {
			sql.WriteString(", ")
		}
		field, _ := expression.Field()
		column, err := quote(field.Column())
		if err != nil {
			return "", err
		}
		sql.WriteString(`"godj_order_source"."c` + strconv.Itoa(i) + `" AS ` + column)
	}
	sql.WriteString(" FROM (" + inner + `) AS "godj_order_source"`)
	return sql.String(), nil
}

func HiddenOrderingColumn(selected, hidden []query.ResultExpression, expression query.ResultExpression) string {
	if index := ExpressionIndex(selected, expression); index >= 0 {
		return `"godj_order_source"."c` + strconv.Itoa(index) + `"`
	}
	return `"godj_order_source"."o` + strconv.Itoa(ExpressionIndex(hidden, expression)) + `"`
}
