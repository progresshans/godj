package orm

import "github.com/progresshans/godj/query"

// NamedColumn keeps the producer's Go decoder, SQL domain and immutable
// definition. Reading a row checks the exact definition, not its current name.
type NamedColumn[M, V any] struct {
	reference query.AnnotationRef
	column    ResultColumn[M, V]
}

func (column NamedColumn[M, V]) Name() string { return column.reference.Name() }
func (column NamedColumn[M, V]) Get(row NamedValues[M]) (V, error) {
	var zero V
	if interfaceIsNil(row) || !column.reference.Valid() || column.column.newCell == nil {
		return zero, invalidResultBuilder("named column or row is uninitialized")
	}
	raw, selected := row.namedValue(*new(M), column.reference)
	if !selected {
		return zero, invalidResultBuilder("named definition was not selected in this row")
	}
	cell := column.column.newCell()
	if cell.value == nil || interfaceIsNil(cell.destination) {
		return zero, invalidResultBuilder("named column returned an invalid decoder")
	}
	if err := scanGroupValue(cell.destination, raw); err != nil {
		return zero, err
	}
	return cell.value(), nil
}

func (column NamedColumn[M, V]) scalarOperand(model M, value V) (query.ScalarExpression, error) {
	if !column.reference.Valid() {
		return query.ScalarExpression{}, invalidResultBuilder("named column is uninitialized")
	}
	return column.column.scalarOperand(model, value)
}
func (column NamedColumn[M, V]) scalarResultField(model M, value V) (query.ResultExpression, func() scalarCell[V], error) {
	if !column.reference.Valid() {
		return query.ResultExpression{}, nil, invalidResultBuilder("named column is uninitialized")
	}
	return column.column.scalarResultField(model, value)
}
func (column NamedColumn[M, V]) Exact(value V) Predicate[M] {
	return column.column.Exact(value)
}
func (column NamedColumn[M, V]) GreaterThan(value V) Predicate[M] {
	return column.column.GreaterThan(value)
}
func (column NamedColumn[M, V]) GreaterThanOrEqual(value V) Predicate[M] {
	return column.column.GreaterThanOrEqual(value)
}
func (column NamedColumn[M, V]) LessThan(value V) Predicate[M] {
	return column.column.LessThan(value)
}
func (column NamedColumn[M, V]) LessThanOrEqual(value V) Predicate[M] {
	return column.column.LessThanOrEqual(value)
}
func (column NamedColumn[M, V]) IsNull(value bool) Predicate[M] {
	return column.column.IsNull(value)
}
func (column NamedColumn[M, V]) Asc() Ordering[M]  { return column.column.Asc() }
func (column NamedColumn[M, V]) Desc() Ordering[M] { return column.column.Desc() }

func (q NamedQuery[M, R]) Column(name string) (NamedColumn[M, query.Value], error) {
	var zero NamedColumn[M, query.Value]
	if err := q.ConfigurationError(); err != nil {
		return zero, err
	}
	ref, found := q.input.plan.Annotation(name)
	if !found {
		return zero, &query.Error{Category: query.CategoryField, Code: query.CodeUnknownField, Field: name, Detail: "name is not an annotation of this query"}
	}
	scalar, err := query.AnnotationExpression(ref)
	if err != nil {
		return zero, err
	}
	expression, err := query.ScalarResult(scalar)
	if err != nil {
		return zero, err
	}
	factory, err := dynamicComputedCell(expression)
	if err != nil {
		return zero, err
	}
	return NamedColumn[M, query.Value]{reference: ref, column: ResultColumn[M, query.Value]{value: ScalarExpression[M, query.Value]{expression: scalar}, newCell: factory}}, nil
}
