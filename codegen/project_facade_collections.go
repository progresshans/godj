package codegen

import (
	"bytes"
	"fmt"
)

func renderProjectFacadeCollections(output *bytes.Buffer, model projectRelationFacadeModel) {
	for index, relation := range model.collections {
		view := relation.surface + "Collection"
		target := relation.target.app.prefix + relation.target.model.GoName
		raw := relation.target.app.alias + "." + relation.target.model.GoName
		link := relation.through.app.alias + "." + relation.through.model.GoName
		fmt.Fprintf(output, `
type %[1]s struct {
 owner *%[2]s
 relation *orm.ManyCollection[%[3]s,%[4]s]
 _self *%[1]s
}
func new%[1]s(_owner *%[2]s, _relation *orm.ManyCollection[%[3]s,%[4]s]) *%[1]s {
 _view := &%[1]s{owner:_owner,relation:_relation}; _view._self = _view; return _view
}
func (_view *%[1]s) validate() error {
 if _view == nil || _view._self != _view || _view.relation == nil { return relationFacadeQueryInvalid("collection view is nil, zero, or copied") }
 _, _, _err := _view.owner.relationFacadePrimaryKey(); return _err
}
func (%[1]s) MarshalJSON() ([]byte,error) { return nil, relationFacadeQueryInvalid("direct collection view JSON is unsupported") }
func (*%[1]s) UnmarshalJSON([]byte) error { return relationFacadeQueryInvalid("direct collection view JSON is unsupported") }
func (_model *%[2]s) %[5]s() (*%[1]s,error) {
 _,_present,_err := _model.relationFacadePrimaryKey(); if _err != nil { return nil,_err }
 if !_present { return nil,&query.Error{Category:query.CategoryQuery,Code:query.CodeMissingPrimaryKey,Detail:"collection owner has no saved primary key"} }
 _relation,_err := _model._manyCollection%[6]d.Get(func() (*orm.ManyCollection[%[3]s,%[4]s],error) {
  if _model.state.sessionScope != nil {
   _session,_ok := _model.state.backend.(db.RelationSession)
   if !_ok { return nil,relationFacadeBackendInvalid("collection mutation requires a relation-capable session") }
   return _model.state.collections.%[7]s.InSession(_session,_model.%[8]s)
  }
  return _model.state.collections.%[7]s.From(_model.state.backend,_model.%[8]s)
 })
 if _err != nil { return nil,_err }; return new%[1]s(_model,_relation),nil
}
func (_view *%[1]s) Query() (%[9]sQuery,error) {
 if _err := _view.validate(); _err != nil { return %[9]sQuery{},_err }
 _query,_err := _view.relation.Query(); if _err != nil { return %[9]sQuery{},_err }
 return new%[9]sQuery(_view.owner.state,_query),nil
}
func (_view *%[1]s) All(_ctx context.Context) ([]*%[9]s,error) {
 _query,_err := _view.Query(); if _err != nil { return nil,_err }; return _query.All(_ctx)
}
func (_view *%[1]s) Fresh() (*%[1]s,error) {
 if _err := _view.validate(); _err != nil { return nil,_err }
 _fresh,_err := _view.relation.Fresh(); if _err != nil { return nil,_err }; return new%[1]s(_view.owner,_fresh),nil
}
func (_view *%[1]s) Invalidate() error {
 if _err := _view.validate(); _err != nil { return _err }; return _view.relation.Invalidate()
}
func (_view *%[1]s) values(_targets []*%[9]s) ([]%[3]s,error) {
 if _err := _view.validate(); _err != nil { return nil,_err }
 if _err := _view.relation.Invalidate(); _err != nil { return nil,_err }
 defer _view.relation.Invalidate()
 _values := make([]%[3]s,len(_targets))
 for _index,_target := range _targets {
  if _target == nil || _target.state != _view.owner.state { return nil,relationFacadeQueryInvalid("collection target is nil or belongs to another facade origin") }
  if _,_,_err := _target.relationFacadePrimaryKey(); _err != nil { return nil,_err }
  _values[_index] = _target.%[10]s
 }
 return _values,nil
}
func (_view *%[1]s) Add(_ctx context.Context, _targets []*%[9]s, _defaults ...orm.ManyToManyInput[%[4]s]) error {
 _values,_err := _view.values(_targets); if _err != nil { return _err }; return _view.relation.Add(_ctx,_values,_defaults...)
}
func (_view *%[1]s) Remove(_ctx context.Context, _targets ...*%[9]s) error {
 _values,_err := _view.values(_targets); if _err != nil { return _err }; return _view.relation.Remove(_ctx,_values...)
}
func (_view *%[1]s) Set(_ctx context.Context, _targets []*%[9]s, _options ...orm.ManyToManySetOptions[%[4]s]) error {
 _values,_err := _view.values(_targets); if _err != nil { return _err }; return _view.relation.Set(_ctx,_values,_options...)
}
func (_view *%[1]s) AddKeys(_ctx context.Context, _keys []int64, _defaults ...orm.ManyToManyInput[%[4]s]) error {
 if _err := _view.validate(); _err != nil { return _err }; return _view.relation.AddKeys(_ctx,_keys,_defaults...)
}
func (_view *%[1]s) RemoveKeys(_ctx context.Context, _keys ...int64) error {
 if _err := _view.validate(); _err != nil { return _err }; return _view.relation.RemoveKeys(_ctx,_keys...)
}
func (_view *%[1]s) SetKeys(_ctx context.Context, _keys []int64, _options ...orm.ManyToManySetOptions[%[4]s]) error {
 if _err := _view.validate(); _err != nil { return _err }; return _view.relation.SetKeys(_ctx,_keys,_options...)
}
func (_view *%[1]s) Clear(_ctx context.Context) error {
 if _err := _view.validate(); _err != nil { return _err }; return _view.relation.Clear(_ctx)
}
`, view, model.surface, raw, link, relation.selector, index, relation.surface, model.rawAlias, target, lowerFirst(target)+"Model")
	}
}
