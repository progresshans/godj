package codegen

import (
	"bytes"
	"fmt"
)

func renderProjectFacadeReverseCollections(output *bytes.Buffer, model projectRelationFacadeModel) {
	for index, relation := range model.reverseCollections {
		view := model.surface + relation.selector + "Collection"
		raw := relation.source.app.alias + "." + relation.source.model.GoName
		target := relation.source.app.prefix + relation.source.model.GoName
		fmt.Fprintf(output, `type %[1]s struct {
 owner *%[2]s
 relation *orm.RelatedSet[%[3]s]
 _self *%[1]s
}
func new%[1]s(_owner *%[2]s,_relation *orm.RelatedSet[%[3]s])*%[1]s{
 _view:=&%[1]s{owner:_owner,relation:_relation};_view._self=_view;return _view
}
func(_view *%[1]s)validate()error{
 if _view==nil||_view._self!=_view||_view.relation==nil{return relationFacadeQueryInvalid("reverse collection view is nil, zero, or copied")}
 _,_,_err:=_view.owner.relationFacadePrimaryKey();return _err
}
func(%[1]s)MarshalJSON()([]byte,error){return nil,relationFacadeQueryInvalid("direct collection view JSON is unsupported")}
func(*%[1]s)UnmarshalJSON([]byte)error{return relationFacadeQueryInvalid("direct collection view JSON is unsupported")}
func(_model *%[2]s)%[4]s()(*%[1]s,error){
 _,_present,_err:=_model.relationFacadePrimaryKey();if _err!=nil{return nil,_err}
 if !_present{return nil,&query.Error{Category:query.CategoryQuery,Code:query.CodeMissingPrimaryKey,Detail:"reverse collection owner has no saved primary key"}}
 _relation,_err:=_model._reverseCollection%[5]d.Get(func()(*orm.RelatedSet[%[3]s],error){return _model.state.reverseCollections.%[2]s.%[6]s.From(_model.state.backend,_model.%[7]s)})
 if _err!=nil{return nil,_err};return new%[1]s(_model,_relation),nil
}
func(_view *%[1]s)Query()(%[8]sQuery,error){
 if _err:=_view.validate();_err!=nil{return %[8]sQuery{},_err}
 _query,_err:=_view.relation.Query();if _err!=nil{return %[8]sQuery{},_err};return new%[8]sQuery(_view.owner.state,_query),nil
}
func(_view *%[1]s)All(_ctx context.Context)([]*%[8]s,error){_query,_err:=_view.Query();if _err!=nil{return nil,_err};return _query.All(_ctx)}
func(_view *%[1]s)Fresh()(*%[1]s,error){
 if _err:=_view.validate();_err!=nil{return nil,_err};_related,_err:=_view.relation.Fresh();if _err!=nil{return nil,_err};return new%[1]s(_view.owner,_related),nil
}
func(_view *%[1]s)Invalidate()error{if _err:=_view.validate();_err!=nil{return _err};return _view.relation.Invalidate()}
`, view, model.surface, raw, relation.selector, index, lowerFirst(relation.selector), model.rawAlias, target)
	}
}
