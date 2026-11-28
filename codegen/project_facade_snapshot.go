package codegen

import (
	"bytes"
	"fmt"
)

func renderProjectFacadeSnapshotFoundation(output *bytes.Buffer) {
	fmt.Fprint(output, `type relationFacadeSnapshotOwner[S any] interface {
 relationFacadeSnapshotSource()(*relationFacadeState,*orm.RelatedSelected[S],error)
}
func relationFacadeReadSnapshot[S,T,W any](_ctx context.Context,_state *relationFacadeState,_owner relationFacadeSnapshotOwner[S],_validate func()error,_read func(context.Context,*orm.RelatedSelected[S])([]*orm.RelatedSelected[T],bool,error),_materialize func(context.Context,*orm.RelatedSelected[T])(*W,error))([]*W,bool,error){
 if _ctx==nil{return nil,false,relationFacadeQueryInvalid("snapshot read requires a context")}
 if _err:=_ctx.Err();_err!=nil{return nil,false,_err}
 if _err:=_state.validate();_err!=nil{return nil,false,_err}
 if _err:=_validate();_err!=nil{return nil,false,_err}
 if relationFacadeNil(_owner){return nil,false,relationFacadeQueryInvalid("snapshot owner is nil")}
 _origin,_graph,_err:=_owner.relationFacadeSnapshotSource();if _err!=nil{return nil,false,_err}
 if _origin!=_state{return nil,false,relationFacadeQueryInvalid("snapshot owner belongs to another facade origin")}
 if _graph==nil{return nil,false,nil}
 _values,_present,_err:=_read(_ctx,_graph);if _err!=nil||!_present{return nil,false,_err}
 _result:=make([]*W,len(_values))
 for _index,_value:=range _values{_result[_index],_err=_materialize(_ctx,_value);if _err!=nil{return nil,false,_err}}
 if _err:=_ctx.Err();_err!=nil{return nil,false,_err}
 if _err:=_state.validate();_err!=nil{return nil,false,_err}
 return _result,true,nil
}
`)
	for _, selector := range []string{"relationFacadeManyPrefetch[S,T,L,W]", "relationFacadeReversePrefetch[S,T,W]"} {
		fmt.Fprintf(output, `func (_selector %[1]s) Snapshot(_name string)%[1]s{_selector.selection=_selector.selection.Snapshot(_name);return _selector}
func (_selector %[1]s) Read(_ctx context.Context,_owner relationFacadeSnapshotOwner[S])([]*W,bool,error){return relationFacadeReadSnapshot(_ctx,_selector.state,_owner,_selector.selection.ValidateSnapshot,_selector.selection.Read,_selector.materialize)}
`, selector)
		for _, method := range []string{"Limit", "Offset"} {
			fmt.Fprintf(output, `func (_selector %[1]s) %[2]s(_value int)(%[1]s,error){_next,_err:=_selector.selection.%[2]s(_value);if _err!=nil{return %[1]s{},_err};_selector.selection=_next;return _selector,nil}
`, selector, method)
		}
	}
}
