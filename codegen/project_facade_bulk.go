package codegen

import (
	"bytes"
	"fmt"
)

func renderProjectFacadeBulk(output *bytes.Buffer, model projectRelationFacadeModel) {
	raw := model.model.app.alias + "." + model.model.model.GoName
	fmt.Fprintf(output, `// Update applies one assignment set to matching rows and invalidates this query's cache.
func (_query %[1]s) Update(_ctx context.Context,_assignments ...orm.UpdateAssignment[%[2]s])(int64,error){
 if _err:=_query.validate();_err!=nil{return 0,_err}
 return _query.query.Update(_ctx,_assignments...)
}
func (_query %[1]s) UpdateDynamic(_ctx context.Context,_inputs ...orm.DynamicUpdateInput)(int64,error){
 if _err:=_query.validate();_err!=nil{return 0,_err}
 return _query.query.UpdateDynamic(_ctx,_inputs...)
}
`, model.queryType, raw)
	fmt.Fprintf(output, `// BulkUpdate writes selected fields while retaining the query predicate.
// It returns the matched count without changing caller objects or read caches.
func (_query %[1]s) BulkUpdate(_ctx context.Context,_inputs []%[2]s,_options ...orm.BulkUpdateOption[%[2]s])(int64,error){
 if _err:=_query.validate();_err!=nil{return 0,_err}
 return _query.query.BulkUpdate(_ctx,_inputs,_options...)
}
`, model.queryType, raw)
	for _, method := range []string{"BulkCreate", "BulkCreateInputs"} {
		input, prepare := raw, "_inputs"
		if method == "BulkCreateInputs" {
			input += "Create"
			prepare = "orm.CreateInputs[" + raw + "](_inputs)"
		}
		fmt.Fprintf(output, `// %[4]s writes explicit inputs and returns owned objects in input order.
// Read filters and eager-loading requests do not populate or constrain them.
func (_query %[1]s) %[4]s(_ctx context.Context,_inputs []%[5]s,_options ...orm.BulkCreateOption[%[2]s])(orm.BulkCreateResult[*%[3]s],error){
 var _zero orm.BulkCreateResult[*%[3]s]
 if _err:=_query.validate();_err!=nil{return _zero,_err}
 _result,_err:=orm.Materialize%[4]s(_ctx,_query.query,_query.state.models.%[3]s,%[6]s,_options...);if _err!=nil{return _zero,_err}
 _wrapped:=orm.BulkCreateResult[*%[3]s]{Objects:make([]*%[3]s,len(_result.Objects)),RowsAffected:_result.RowsAffected,ReturnedKeys:_result.ReturnedKeys}
 for _index,_value:=range _result.Objects{
  _wrapped.Objects[_index],_err=_query.state.materialize%[3]s(context.WithoutCancel(_ctx),_value);if _err!=nil{return _zero,_err}
 }
 return _wrapped,nil
}
`, model.queryType, raw, model.surface, method, input, prepare)
	}
}
