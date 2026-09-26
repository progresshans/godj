package codegen

import (
	"bytes"
	"fmt"

	"github.com/progresshans/godj/schema/ir"
)

func renderManyToManyInput(output *bytes.Buffer, model ir.Model, fields []ir.Field) {
	fmt.Fprintf(output, "func (%sDescriptor) ManyToManyCreateInput() orm.ManyToManyInput[%s] { return %sCreate{} }\n\n", model.GoName, model.GoName, model.GoName)
	fmt.Fprintf(output, "var _ orm.ManyToManyDescriptor[%s] = %sDescriptor{}\n\n", model.GoName, model.GoName)
	fmt.Fprintf(output, "func (input %sCreate) BuildManyToManyCreate(source, target ir.Field, sourceKey, targetKey int64) orm.Mutation[%s] {\n", model.GoName, model.GoName)
	fmt.Fprintf(output, "\tinvalid := func() orm.Mutation[%s] { return orm.InvalidMutation[%s](&query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: \"collection endpoints must be distinct canonical ForeignKeys omitted from through defaults\"}) }\n", model.GoName, model.GoName)
	fmt.Fprintln(output, "\tif source.Name == target.Name { return invalid() }")
	fmt.Fprintln(output, "\tfor index, field := range []ir.Field{source, target} {")
	fmt.Fprintln(output, "\t\tkey := sourceKey; if index == 1 { key = targetKey }")
	fmt.Fprintln(output, "\t\tswitch {")
	for _, field := range fields {
		fmt.Fprintf(output, "\t\tcase reflect.DeepEqual(field, (%s{}).Field()):\n", relationObjectStorageName(model, field))
		if field.Nullable {
			fmt.Fprintf(output, "\t\t\tif _, state := input.%s.Get(); state != orm.NullableChangeUnset { return invalid() }\n", privateName(field.GoName))
		} else {
			fmt.Fprintf(output, "\t\t\tif _, set := input.%s.Get(); set { return invalid() }\n", privateName(field.GoName))
		}
		fmt.Fprintf(output, "\t\t\tinput = input.With%s(key)\n", field.GoName)
	}
	fmt.Fprintln(output, "\t\tdefault: return invalid()")
	fmt.Fprintln(output, "\t\t}")
	fmt.Fprintln(output, "\t}")
	fmt.Fprintln(output, "\treturn input.BuildCreate()")
	fmt.Fprintln(output, "}")
	fmt.Fprintln(output)
}
