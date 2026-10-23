package codegen

import (
	"bytes"
	"fmt"
)

func renderProjectFacadeStream(output *bytes.Buffer, model projectRelationFacadeModel, queryType, execute, validate string) {
	raw := model.model.app.alias + "." + model.model.model.GoName
	fmt.Fprintf(output, `// Iterate reads explicit batches without consulting or filling the full query cache.
// Use the callback context for all interleaved ORM reads and writes.
func (_query %[1]s) Iterate(_ctx context.Context, _size int, _callback func(context.Context,*%[2]s)(bool,error))error{
 if _err:=relationFacadeContext(_ctx);_err!=nil{return _err}
 if _err:=%[4]s;_err!=nil{return _err}
 if _size<=0||_callback==nil{return &query.Error{Category:query.CategoryArgument,Code:query.CodeInvalidValue,Detail:"streaming requires a positive batch size and a non-nil callback"}}
 _yield:=func(_ctx context.Context,_values []*orm.RelatedSelected[%[3]s])(bool,error){
  _models:=make([]*%[2]s,len(_values))
  for _index,_value:=range _values{
   _wrapped,_err:=_query.state.materialize%[2]s(_ctx,_value);if _err!=nil{return false,_err};_models[_index]=_wrapped
  }
  for _,_model:=range _models{
   if _err:=relationFacadeContext(_ctx);_err!=nil{return false,_err}
   if _err:=_query.state.validate();_err!=nil{return false,_err}
   _more,_err:=_callback(_ctx,_model);if _err!=nil||!_more{return false,_err}
  }
  return true,nil
 }
 return %[5]s
}
`, queryType, model.surface, raw, validate, execute)
}
