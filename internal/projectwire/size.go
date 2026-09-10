package projectwire

import (
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

// Measure follows the canonical Schema IR JSON shape, including omitempty and
// escaped strings, before allocating the complete enclosing wire document.
func Measure(sizer *wirejson.Sizer, spec Spec) bool {
	if !sizer.Literal(`{"project":`) || !measurePackage(sizer, spec.Project) || !sizer.Literal(`,"apps":[`) {
		return false
	}
	for index := range spec.Apps {
		if index != 0 && !sizer.Literal(`,`) {
			return false
		}
		app := spec.Apps[index]
		if !sizer.Literal(`{"alias":`) || !sizer.String(app.Alias) || !sizer.Literal(`,"package":`) ||
			!measurePackage(sizer, app.Package) || !sizer.Literal(`,"schema":`) ||
			!measureSchema(sizer, app.Schema) || !sizer.Literal(`}`) {
			return false
		}
	}
	return sizer.Literal(`]}`)
}

func measurePackage(sizer *wirejson.Sizer, pkg Package) bool {
	return sizer.Literal(`{"package_name":`) && sizer.String(pkg.PackageName) &&
		sizer.Literal(`,"import_path":`) && sizer.String(pkg.ImportPath) &&
		sizer.Literal(`,"directory":`) && sizer.String(pkg.Directory) && sizer.Literal(`}`)
}

func measureSchema(sizer *wirejson.Sizer, schema ir.Schema) bool {
	if !sizer.Literal(`{"format_version":`) || !sizer.Integer(int64(schema.FormatVersion)) ||
		!sizer.Literal(`,"app_label":`) || !sizer.String(schema.AppLabel) || !sizer.Literal(`,"models":[`) {
		return false
	}
	for index := range schema.Models {
		if index != 0 && !sizer.Literal(`,`) {
			return false
		}
		if !measureModel(sizer, schema.Models[index]) {
			return false
		}
	}
	return sizer.Literal(`]}`)
}

func measureModel(sizer *wirejson.Sizer, model ir.Model) bool {
	if !sizer.Literal(`{"name":`) || !sizer.String(model.Name) ||
		!sizer.Literal(`,"go_name":`) || !sizer.String(model.GoName) ||
		!sizer.Literal(`,"db_table":`) || !sizer.String(model.DBTable) || !sizer.Literal(`,"fields":[`) {
		return false
	}
	for index := range model.Fields {
		if index != 0 && !sizer.Literal(`,`) {
			return false
		}
		if !measureField(sizer, model.Fields[index]) {
			return false
		}
	}
	return sizer.Literal(`]}`)
}

func measureField(sizer *wirejson.Sizer, field ir.Field) bool {
	if !sizer.Literal(`{"name":`) || !sizer.String(field.Name) ||
		!sizer.Literal(`,"go_name":`) || !sizer.String(field.GoName) ||
		!sizer.Literal(`,"column":`) || !sizer.String(field.Column) ||
		!sizer.Literal(`,"kind":`) || !sizer.String(string(field.Kind)) ||
		!sizer.Literal(`,"primary_key":`) || !sizer.Boolean(field.PrimaryKey) ||
		!sizer.Literal(`,"nullable":`) || !sizer.Boolean(field.Nullable) {
		return false
	}
	if field.MaxLength != 0 && (!sizer.Literal(`,"max_length":`) || !sizer.Integer(int64(field.MaxLength))) {
		return false
	}
	if field.Default != nil && (!sizer.Literal(`,"default":`) || !measureDefault(sizer, *field.Default)) {
		return false
	}
	if field.Relation != nil && (!sizer.Literal(`,"relation":`) || !measureRelation(sizer, *field.Relation)) {
		return false
	}
	return sizer.Literal(`}`)
}

func measureDefault(sizer *wirejson.Sizer, value ir.ScalarDefault) bool {
	if !sizer.Literal(`{"kind":`) || !sizer.String(string(value.Kind)) {
		return false
	}
	if value.String != "" && (!sizer.Literal(`,"string":`) || !sizer.String(value.String)) {
		return false
	}
	if value.Boolean && (!sizer.Literal(`,"boolean":`) || !sizer.Boolean(true)) {
		return false
	}
	if value.Integer != 0 && (!sizer.Literal(`,"integer":`) || !sizer.Integer(value.Integer)) {
		return false
	}
	return sizer.Literal(`}`)
}

func measureRelation(sizer *wirejson.Sizer, value ir.ForeignKeyRelation) bool {
	if !sizer.Literal(`{"target":{"app_label":`) || !sizer.String(value.Target.AppLabel) ||
		!sizer.Literal(`,"model_name":`) || !sizer.String(value.Target.ModelName) ||
		!sizer.Literal(`},"cardinality":`) || !sizer.String(string(value.Cardinality)) ||
		!sizer.Literal(`,"reverse":{`) {
		return false
	}
	wroteReverse := false
	if value.Reverse.Name != "" {
		if !sizer.Literal(`"name":`) || !sizer.String(value.Reverse.Name) {
			return false
		}
		wroteReverse = true
	}
	if value.Reverse.Disabled {
		if wroteReverse && !sizer.Literal(`,`) {
			return false
		}
		if !sizer.Literal(`"disabled":true`) {
			return false
		}
	}
	return sizer.Literal(`},"on_delete":`) && sizer.String(string(value.OnDelete)) && sizer.Literal(`}`)
}
