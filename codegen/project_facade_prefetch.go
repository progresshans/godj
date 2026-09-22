package codegen

import (
	"bytes"
	"fmt"
	"strconv"
)

func renderProjectFacadePrefetch(output *bytes.Buffer, model projectRelationFacadeModel) {
	if len(model.collections) == 0 {
		return
	}
	raw := model.model.app.alias + "." + model.model.model.GoName
	surface := model.surface
	fmt.Fprintf(output, `type %[1]sPrefetchSelector struct {
 state *relationFacadeState
 selection orm.PrefetchSelection[%[2]s]
}
type %[1]sPrefetchSelectors struct {
`, surface, raw)
	for _, relation := range model.collections {
		fmt.Fprintf(output, "%s %sPrefetchSelector\n", relation.selector, surface)
	}
	fmt.Fprintln(output, "}")
	fmt.Fprintf(output, `type %[1]sPrefetchQuery struct {
 state *relationFacadeState
 prefetch orm.PrefetchQuery[%[2]s]
}
func (_query %[1]sQuery) PrefetchRelated(_selectors ...%[1]sPrefetchSelector) %[1]sPrefetchQuery {
 _result:=%[1]sPrefetchQuery{state:_query.state}
 if _err:=_query.validate();_err!=nil{_result.prefetch=_result.prefetch.WithConfigurationError(_err);return _result}
 _inputs:=make([]orm.PrefetchSelection[%[2]s],len(_selectors))
 for _index,_selector:=range _selectors {
  if _selector.state!=_query.state || _selector.selection==nil {_result.prefetch=_result.prefetch.WithConfigurationError(relationFacadeQueryInvalid("prefetch selector belongs to another query origin"));return _result}
  _inputs[_index]=_selector.selection
 }
 _result.prefetch=orm.PrefetchRelated(_query.query,_inputs...)
 return _result
}
func (_query %[1]sQuery) PrefetchRelatedPaths(_paths ...string)(%[1]sPrefetchQuery,error){
 if _err:=_query.validate();_err!=nil{return %[1]sPrefetchQuery{},_err}
 if len(_paths)>orm.MaximumRelatedSelectionNodes{return %[1]sPrefetchQuery{},relationFacadeQueryInvalid("prefetch selection budget exceeded")}
 _selectors:=make([]%[1]sPrefetchSelector,len(_paths))
 for _index,_path:=range _paths {switch _path {
`, surface, raw)
	for _, relation := range model.collections {
		fmt.Fprintf(output, "case %s:_selectors[_index]=_query.Prefetch.%s\n", strconv.Quote(relation.name), relation.selector)
	}
	fmt.Fprintf(output, `default:return %[1]sPrefetchQuery{},&query.Error{Category:query.CategoryField,Code:query.CodeUnknownRelation,Field:_path,Detail:"prefetch path is not a declared collection"}
 }}
 _result:=_query.PrefetchRelated(_selectors...);if _err:=_result.prefetch.ConfigurationError();_err!=nil{return %[1]sPrefetchQuery{},_err};return _result,nil
}
func (_query %[1]sPrefetchQuery) validate(_ctx context.Context) error {
 if _err:=relationFacadeContext(_ctx);_err!=nil{return _err}
 if _err:=_query.prefetch.ConfigurationError();_err!=nil{return _err}
 return _query.state.validate()
}
func (_state *relationFacadeState) wrap%[1]sPrefetched(_value *orm.Prefetched[%[2]s])(*%[1]s,error){
 _raw,_err:=_value.Source();if _err!=nil{return nil,_err}
 _wrapped,_err:=_state.wrap%[1]s(_raw,false);if _err!=nil{return nil,_err}
`, surface, raw)
	for index, relation := range model.collections {
		target := relation.target.app.alias + "." + relation.target.model.GoName
		through := relation.through.app.alias + "." + relation.through.model.GoName
		fmt.Fprintf(output, `if _collection,_present,_err:=_state.collections.%[1]s.FromPrefetched(_value);_err!=nil{return nil,_err}else if _present{
 _,_err=_wrapped._manyCollection%[2]d.Get(func()(*orm.ManyCollection[%[3]s,%[4]s],error){return _collection,nil});if _err!=nil{return nil,_err}
}
`, relation.surface, index, target, through)
	}
	fmt.Fprintln(output, "return _wrapped,nil\n}")
	for _, method := range []struct{ name, argument string }{{"Filter", "orm.Predicate[" + raw + "]"}, {"OrderBy", "orm.Ordering[" + raw + "]"}} {
		fmt.Fprintf(output, `func (_query %[1]sPrefetchQuery) %[2]s(_values ...%[3]s)%[1]sPrefetchQuery{_query.prefetch=_query.prefetch.%[2]s(_values...);return _query}
`, surface, method.name, method.argument)
	}
	for _, method := range []string{"Distinct", "Fresh"} {
		fmt.Fprintf(output, `func (_query %[1]sPrefetchQuery) %[2]s()%[1]sPrefetchQuery{_query.prefetch=_query.prefetch.%[2]s();return _query}
`, surface, method)
	}
	for _, method := range []string{"Limit", "Offset"} {
		fmt.Fprintf(output, `func (_query %[1]sPrefetchQuery) %[2]s(_value int)(%[1]sPrefetchQuery,error){_next,_err:=_query.prefetch.%[2]s(_value);if _err!=nil{return %[1]sPrefetchQuery{},_err};_query.prefetch=_next;return _query,nil}
`, surface, method)
	}
	fmt.Fprintf(output, `func (_query %[1]sPrefetchQuery) All(_ctx context.Context)([]*%[1]s,error){
 if _err:=_query.validate(_ctx);_err!=nil{return nil,_err}
 _values,_err:=_query.prefetch.All(_ctx);if _err!=nil{return nil,_err}
 _result:=make([]*%[1]s,len(_values))
 for _index,_value:=range _values{_result[_index],_err=_query.state.wrap%[1]sPrefetched(_value);if _err!=nil{return nil,_err}}
 if _err:=relationFacadeContext(_ctx);_err!=nil{return nil,_err};if _err:=_query.state.validate();_err!=nil{return nil,_err};return _result,nil
}
func (_query %[1]sPrefetchQuery) First(_ctx context.Context)(*%[1]s,bool,error){
 if _err:=_query.validate(_ctx);_err!=nil{return nil,false,_err}
 _value,_present,_err:=_query.prefetch.First(_ctx);if _err!=nil||!_present{return nil,false,_err}
 _result,_err:=_query.state.wrap%[1]sPrefetched(_value);if _err!=nil{return nil,false,_err}
 if _err:=relationFacadeContext(_ctx);_err!=nil{return nil,false,_err};if _err:=_query.state.validate();_err!=nil{return nil,false,_err};return _result,true,nil
}
func (_query %[1]sPrefetchQuery) Count(_ctx context.Context)(int64,error){
 if _err:=_query.validate(_ctx);_err!=nil{return 0,_err};return _query.prefetch.Count(_ctx)
}
`, surface)
}
