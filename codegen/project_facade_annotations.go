package codegen

import (
	"bytes"
	"fmt"
)

// Named rows retain the ordinary application model wrapper. Definitions and
// codecs stay in the generic runtime; the facade supplies only model authority.
func renderProjectFacadeAnnotations(output *bytes.Buffer, model projectRelationFacadeModel) {
	raw := model.model.app.alias + "." + model.model.model.GoName
	surface := model.surface
	fmt.Fprintf(output, `
type %[1]sNamedQuery struct {
 state *relationFacadeState
 query orm.NamedQuery[%[2]s, %[2]s]
}
type %[1]sAnnotationSource interface {
 relationFacade%[1]sNames() (%[1]sNamedQuery, error)
}
func (_query %[3]s) relationFacade%[1]sNames() (%[1]sNamedQuery, error) {
 if _err := _query.validate(); _err != nil { return %[1]sNamedQuery{}, _err }
 _named, _err := orm.NamesIn(_query.query, _query.state.models.%[1]s)
 if _err != nil { return %[1]sNamedQuery{}, _err }
 return %[1]sNamedQuery{state:_query.state, query:_named}, nil
}
func (_query %[1]sNamedQuery) relationFacade%[1]sNames() (%[1]sNamedQuery, error) {
 if _err := _query.validate(); _err != nil { return %[1]sNamedQuery{}, _err }
 return _query, nil
}
func (_query %[1]sNamedQuery) validate() error {
 if _err := _query.state.validate(); _err != nil { return _err }
 return _query.query.ConfigurationError()
}
func (_query %[1]sNamedQuery) All(_ctx context.Context) ([]orm.NamedRow[%[2]s, *%[1]s], error) {
 if _err := _query.validate(); _err != nil { return nil, _err }
 _rows, _err := _query.query.All(_ctx)
 if _err != nil { return nil, _err }
 return _query.wrapRows(_rows)
}
func (_query %[1]sNamedQuery) wrapRows(_rows []orm.NamedRow[%[2]s, %[2]s]) ([]orm.NamedRow[%[2]s, *%[1]s], error) {
 _result := make([]orm.NamedRow[%[2]s, *%[1]s], len(_rows))
 for _index, _row := range _rows {
  _wrapped, _err := orm.MapNamedRow(_row, func(_value %[2]s) (*%[1]s, error) { return _query.state.wrap%[1]s(_value, false) })
  if _err != nil { return nil, _err }
  _result[_index] = _wrapped
 }
 return _result, nil
}
func (_query %[1]sNamedQuery) Page(_ctx context.Context, _limit, _offset int) (orm.ResultPage[orm.NamedRow[%[2]s, *%[1]s]], error) {
 var _zero orm.ResultPage[orm.NamedRow[%[2]s, *%[1]s]]
 if _err := _query.validate(); _err != nil { return _zero, _err }
 _page, _err := _query.query.Page(_ctx, _limit, _offset)
 if _err != nil { return _zero, _err }
 _rows, _err := _query.wrapRows(_page.Rows)
 if _err != nil { return _zero, _err }
 return orm.ResultPage[orm.NamedRow[%[2]s, *%[1]s]]{Total:_page.Total, Rows:_rows}, nil
}
func (_query %[1]sNamedQuery) Count(_ctx context.Context) (int64, error) {
 if _err := _query.validate(); _err != nil { return 0, _err }
 return _query.query.Count(_ctx)
}
func (_query %[1]sNamedQuery) Exists(_ctx context.Context) (bool, error) {
 if _err := _query.validate(); _err != nil { return false, _err }
 return _query.query.Exists(_ctx)
}
func (_query %[1]sNamedQuery) Derive() (orm.ResultSource[%[2]s], error) {
 if _err := _query.validate(); _err != nil { return orm.ResultSource[%[2]s]{}, _err }
 return orm.Derive(_query.query)
}
func (_query %[1]sNamedQuery) Column(_name string) (orm.NamedColumn[%[2]s, query.Value], error) {
 if _err := _query.validate(); _err != nil { return orm.NamedColumn[%[2]s, query.Value]{}, _err }
 return _query.query.Column(_name)
}
func (_query %[1]sNamedQuery) Expression(_input orm.DynamicExpression) (orm.DynamicScalar[%[2]s], error) {
 if _err := _query.validate(); _err != nil { return orm.DynamicScalar[%[2]s]{}, _err }
 return _query.query.Expression(_input)
}
func (_query %[1]sNamedQuery) AggregateExpression(_kind query.ResultExpressionKind, _input orm.DynamicExpression) (orm.AggregateExpression[%[2]s, query.Value], error) {
 if _err := _query.validate(); _err != nil { return orm.AggregateExpression[%[2]s, query.Value]{}, _err }
 return _query.query.AggregateExpression(_kind, _input)
}
func (_query %[1]sNamedQuery) ExpressionPredicate(_input orm.DynamicPredicate) (orm.Predicate[%[2]s], error) {
 if _err := _query.validate(); _err != nil { return orm.Predicate[%[2]s]{}, _err }
 return _query.query.Predicate(_input)
}
func (_query %[1]sNamedQuery) ProjectExpressions(_inputs ...orm.DynamicExpression) (orm.Projection[%[2]s, []query.Value], error) {
 if _err := _query.validate(); _err != nil { return orm.Projection[%[2]s, []query.Value]{}, _err }
 return _query.query.ProjectExpressions(_inputs...)
}
func Select%[1]sNamedQuery[R any](_source %[1]sNamedQuery, _projection orm.Projection[%[2]s, R]) (orm.ResultQuery[%[2]s, R], error) {
 if _err := _source.validate(); _err != nil { return orm.ResultQuery[%[2]s, R]{}, _err }
 return orm.SelectNamed(_source.query, _projection)
}
func (_query %[1]sNamedQuery) Filter(_predicates ...orm.Predicate[%[2]s]) %[1]sNamedQuery {
 _query.query = _query.query.Filter(_predicates...)
 return _query
}
func (_query %[1]sNamedQuery) OrderBy(_orderings ...orm.Ordering[%[2]s]) %[1]sNamedQuery {
 _query.query = _query.query.OrderBy(_orderings...)
 return _query
}
`, surface, raw, model.queryType)
	for _, method := range []string{"Distinct", "Fresh"} {
		fmt.Fprintf(output, `func (_query %[1]sNamedQuery) %[2]s() %[1]sNamedQuery { _query.query = _query.query.%[2]s(); return _query }
`, surface, method)
	}
	for _, method := range []string{"Limit", "Offset"} {
		fmt.Fprintf(output, `func (_query %[1]sNamedQuery) %[2]s(_value int) (%[1]sNamedQuery, error) {
 if _err := _query.validate(); _err != nil { return %[1]sNamedQuery{}, _err }
 _rows, _err := _query.query.%[2]s(_value)
 if _err != nil { return %[1]sNamedQuery{}, _err }
 _query.query = _rows
 return _query, nil
}
`, surface, method)
	}
	for _, receiver := range []string{model.queryType, surface + "NamedQuery"} {
		for _, operation := range []string{"Annotate", "Alias"} {
			fmt.Fprintf(output, `func (_query %[1]s) %[2]s(_values ...orm.NamedExpression) (%[3]sNamedQuery, error) {
 _source, _err := _query.relationFacade%[3]sNames()
 if _err != nil { return %[3]sNamedQuery{}, _err }
 _rows, _err := orm.%[2]sDynamic(_source.query, _values...)
 if _err != nil { return %[3]sNamedQuery{}, _err }
 _source.query = _rows
 return _source, nil
}
`, receiver, operation, surface)
		}
	}
	for _, operation := range []string{"Annotate", "Alias"} {
		for _, aggregate := range []bool{false, true} {
			kind, suffix := "ScalarField", ""
			if aggregate {
				kind, suffix = "AggregateExpression", "Aggregate"
			}
			fmt.Fprintf(output, `func %[1]s%[2]s%[3]s[V any](_input %[2]sAnnotationSource, _name string, _value orm.%[4]s[%[5]s, V]) (%[2]sNamedQuery, orm.NamedColumn[%[5]s, V], error) {
 var _column orm.NamedColumn[%[5]s, V]
 if relationFacadeNil(_input) { return %[2]sNamedQuery{}, _column, relationFacadeQueryInvalid("annotation source is nil") }
 _source, _err := _input.relationFacade%[2]sNames()
 if _err != nil { return %[2]sNamedQuery{}, _column, _err }
 _rows, _column, _err := orm.%[1]s%[3]s(_source.query, _name, _value)
 if _err != nil { return %[2]sNamedQuery{}, _column, _err }
 _source.query = _rows
 return _source, _column, nil
}
`, operation, surface, suffix, kind, raw)
		}
	}
}
