package codegen

import (
	"bytes"
	"fmt"
	"strconv"
)

func renderProjectModelBindings(
	output *bytes.Buffer,
	models []*projectRelationModel,
	resultType string,
	usedModels map[int]struct{},
) {
	fmt.Fprintln(output, "\t_binding, _err := Bind()")
	fmt.Fprintln(output, "\tif _err != nil {")
	fmt.Fprintf(output, "\t\treturn %s{}, _err\n", resultType)
	fmt.Fprintln(output, "\t}")
	for _, model := range models {
		fmt.Fprintf(output, "\t_model%d, _err := orm.BindModel(\n", model.bind)
		fmt.Fprintln(output, "\t\t_binding,")
		fmt.Fprintf(
			output,
			"\t\tir.ModelIdentity{AppLabel: %s, ModelName: %s},\n",
			strconv.Quote(model.identity.AppLabel),
			strconv.Quote(model.identity.ModelName),
		)
		fmt.Fprintf(output, "\t\t%s.%sDescriptor{},\n", model.app.alias, model.model.GoName)
		fmt.Fprintln(output, "\t)")
		fmt.Fprintln(output, "\tif _err != nil {")
		fmt.Fprintf(output, "\t\treturn %s{}, _err\n", resultType)
		fmt.Fprintln(output, "\t}")
		if _, used := usedModels[model.bind]; usedModels != nil && !used {
			fmt.Fprintf(output, "\t_ = _model%d\n", model.bind)
		}
	}
}
