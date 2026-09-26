package codegen

import (
	"bytes"
	"fmt"
	"strconv"
)

func renderProjectFacadePrefetchFoundation(output *bytes.Buffer) {
	fmt.Fprint(output, `type relationFacadePrefetchInput[S any] interface {
 relationFacadePrefetchOwner() *relationFacadeState
 relationFacadePrefetchValue() orm.PrefetchSelection[S]
}
type relationFacadeManyPrefetch[S,T,L any] struct {
 state *relationFacadeState
 selection orm.ManyPrefetch[S,T,L]
}
func (_selector relationFacadeManyPrefetch[S,T,L]) relationFacadePrefetchOwner()*relationFacadeState{return _selector.state}
func (_selector relationFacadeManyPrefetch[S,T,L]) relationFacadePrefetchValue()orm.PrefetchSelection[S]{return _selector.selection}
func (_selector relationFacadeManyPrefetch[S,T,L]) Filter(_values ...orm.Predicate[T])relationFacadeManyPrefetch[S,T,L]{_selector.selection=_selector.selection.Filter(_values...);return _selector}
func (_selector relationFacadeManyPrefetch[S,T,L]) OrderBy(_values ...orm.Ordering[T])relationFacadeManyPrefetch[S,T,L]{_selector.selection=_selector.selection.OrderBy(_values...);return _selector}
func (_selector relationFacadeManyPrefetch[S,T,L]) Distinct()relationFacadeManyPrefetch[S,T,L]{_selector.selection=_selector.selection.Distinct();return _selector}
func (_selector relationFacadeManyPrefetch[S,T,L]) WithChildren(_children ...relationFacadePrefetchInput[T])relationFacadeManyPrefetch[S,T,L]{
 if _err:=_selector.state.validate();_err!=nil{_selector.selection=_selector.selection.WithConfigurationError(_err);return _selector}
 _inputs:=make([]orm.PrefetchSelection[T],0,len(_children))
 for _,_child:=range _children{
  if relationFacadeNil(_child)||_child.relationFacadePrefetchOwner()!=_selector.state{_selector.selection=_selector.selection.WithConfigurationError(relationFacadeQueryInvalid("child prefetch belongs to another facade origin"));return _selector}
  _inputs=append(_inputs,_child.relationFacadePrefetchValue())
 }
 _selector.selection=_selector.selection.WithChildren(_inputs...);return _selector
}
type relationFacadePrefetchPath[S any] struct {
 state *relationFacadeState
 selection orm.PrefetchSelection[S]
}
func (_selector relationFacadePrefetchPath[S]) relationFacadePrefetchOwner()*relationFacadeState{return _selector.state}
func (_selector relationFacadePrefetchPath[S]) relationFacadePrefetchValue()orm.PrefetchSelection[S]{return _selector.selection}
`)
}

func renderProjectFacadePrefetch(output *bytes.Buffer, model projectRelationFacadeModel) {
	if len(model.collections) == 0 {
		return
	}
	raw := model.model.app.alias + "." + model.model.model.GoName
	surface := model.surface
	fmt.Fprintf(output, `type %[1]sPrefetchSelector = relationFacadePrefetchInput[%[2]s]
type %[1]sPrefetchSelectors struct {
`, surface, raw)
	for _, relation := range model.collections {
		fmt.Fprintf(output, "%s relationFacadeManyPrefetch[%s,%s.%s,%s.%s]\n", relation.selector, raw, relation.target.app.alias, relation.target.model.GoName, relation.through.app.alias, relation.through.model.GoName)
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
  if relationFacadeNil(_selector)||_selector.relationFacadePrefetchOwner()!=_query.state {_result.prefetch=_result.prefetch.WithConfigurationError(relationFacadeQueryInvalid("prefetch selector belongs to another query origin"));return _result}
  _inputs[_index]=_selector.relationFacadePrefetchValue()
 }
 _result.prefetch=orm.PrefetchRelated(_query.query,_inputs...)
 return _result
}
func (_query %[1]sQuery) PrefetchRelatedPaths(_paths ...string)(%[1]sPrefetchQuery,error){
 if _err:=_query.validate();_err!=nil{return %[1]sPrefetchQuery{},_err}
 if len(_paths)>orm.MaximumRelatedSelectionNodes{return %[1]sPrefetchQuery{},relationFacadeQueryInvalid("prefetch selection budget exceeded")}
 _remaining:=orm.MaximumRelatedSelectionNodes
 _inputs:=make([]orm.PrefetchSelection[%[2]s],len(_paths))
 for _index,_path:=range _paths {
  _selection,_err:=_query.state.prefetch%[1]sPath(_path,1,&_remaining);if _err!=nil{return %[1]sPrefetchQuery{},_err};_inputs[_index]=_selection
 }
 _result:=%[1]sPrefetchQuery{state:_query.state,prefetch:orm.PrefetchRelated(_query.query,_inputs...)}
 if _err:=_result.prefetch.ConfigurationError();_err!=nil{return %[1]sPrefetchQuery{},_err};return _result,nil
}
func (_query %[1]sQuery) PrefetchPath(_path string)(%[1]sPrefetchSelector,error){
 if _err:=_query.validate();_err!=nil{return nil,_err}
 _remaining:=orm.MaximumRelatedSelectionNodes
 _selection,_err:=_query.state.prefetch%[1]sPath(_path,1,&_remaining);if _err!=nil{return nil,_err}
 return relationFacadePrefetchPath[%[2]s]{state:_query.state,selection:_selection},nil
}
func (_query %[1]sPrefetchQuery) validate(_ctx context.Context) error {
 if _err:=relationFacadeContext(_ctx);_err!=nil{return _err}
 if _err:=_query.prefetch.ConfigurationError();_err!=nil{return _err}
 return _query.state.validate()
}
`, surface, raw)

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
 for _index,_value:=range _values{_result[_index],_err=_query.state.materialize%[1]s(_ctx,_value);if _err!=nil{return nil,_err}}
 if _err:=relationFacadeContext(_ctx);_err!=nil{return nil,_err};if _err:=_query.state.validate();_err!=nil{return nil,_err};return _result,nil
}
func (_query %[1]sPrefetchQuery) First(_ctx context.Context)(*%[1]s,bool,error){
 if _err:=_query.validate(_ctx);_err!=nil{return nil,false,_err}
 _value,_present,_err:=_query.prefetch.First(_ctx);if _err!=nil||!_present{return nil,false,_err}
 _result,_err:=_query.state.materialize%[1]s(_ctx,_value);if _err!=nil{return nil,false,_err}
 if _err:=relationFacadeContext(_ctx);_err!=nil{return nil,false,_err};if _err:=_query.state.validate();_err!=nil{return nil,false,_err};return _result,true,nil
}
func (_query %[1]sPrefetchQuery) Count(_ctx context.Context)(int64,error){
 if _err:=_query.validate(_ctx);_err!=nil{return 0,_err};return _query.prefetch.Count(_ctx)
}
`, surface)
}

func renderProjectFacadePrefetchPath(output *bytes.Buffer, model projectRelationFacadeModel) {
	raw := model.model.app.alias + "." + model.model.model.GoName
	fmt.Fprintf(output, `func (_state *relationFacadeState) prefetch%[1]sPath(_path string,_depth int,_remaining *int)(orm.PrefetchSelection[%[2]s],error){
 if _depth>query.MaximumRelationHops||*_remaining<=0{return nil,relationFacadeQueryInvalid("prefetch path exceeds its depth or node bound")}
 *_remaining--
`, model.surface, raw)
	if len(model.collections) > 0 {
		fmt.Fprint(output, "_head,_tail,_nested:=strings.Cut(_path,\"__\")\nswitch _head {\n")
		for _, relation := range model.collections {
			fmt.Fprintf(output, `case %[1]s:
 _selection:=_state.collections.%[2]s.WithChildren()
 if _nested{_child,_err:=_state.prefetch%[3]sPath(_tail,_depth+1,_remaining);if _err!=nil{return nil,_err};_selection=_selection.WithChildren(_child)}
 return _selection,nil
`, strconv.Quote(relation.name), relation.surface, relation.target.app.prefix+relation.target.model.GoName)
		}
		fmt.Fprintln(output, "}")
	}
	fmt.Fprintln(output, "return nil,&query.Error{Category:query.CategoryField,Code:query.CodeUnknownRelation,Field:_path,Detail:\"prefetch path is not a declared collection\"}\n}")
}

func renderProjectFacadeMaterialize(output *bytes.Buffer, model projectRelationFacadeModel) {
	raw := model.model.app.alias + "." + model.model.model.GoName
	fmt.Fprintf(output, "func (_state *relationFacadeState) materialize%s(_ctx context.Context,_value *orm.RelatedSelected[%s])(*%s,error){\n", model.surface, raw, model.surface)
	if model.source != nil {
		fmt.Fprintf(output, "_object,_err:=_state.objects.%s.FromSelected(_value);if _err!=nil{return nil,_err}\nreturn _state.wrapSelected%sObject(_ctx,_object)\n}\n", model.surface, model.surface)
		return
	}
	fmt.Fprintf(output, `if _err:=_value.ValidateSourceBinding(_state.models.%[1]s);_err!=nil{return nil,_err}
 _raw,_err:=_value.Source();if _err!=nil{return nil,_err}
 _wrapped,_err:=_state.wrap%[1]s(_raw,false);if _err!=nil{return nil,_err}
`, model.surface)
	renderProjectFacadePrefetchedCollections(output, model, "_value")
	fmt.Fprintln(output, "if _err:=_ctx.Err();_err!=nil{return nil,_err};return _wrapped,nil\n}")
}

func renderProjectFacadePrefetchedCollections(output *bytes.Buffer, model projectRelationFacadeModel, graph string) {
	for index, relation := range model.collections {
		target := relation.target.app.alias + "." + relation.target.model.GoName
		through := relation.through.app.alias + "." + relation.through.model.GoName
		fmt.Fprintf(output, `if _collection,_present,_err:=_state.collections.%[1]s.FromPrefetched(%[5]s);_err!=nil{return nil,_err}else if _present{
 _,_err=_wrapped._manyCollection%[2]d.Get(func()(*orm.ManyCollection[%[3]s,%[4]s],error){return _collection,nil});if _err!=nil{return nil,_err}
}
`, relation.surface, index, target, through, graph)
	}
}
