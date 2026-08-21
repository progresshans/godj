package protocol

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/progresshans/godj/schema/ir"
)

var errResponseTooLarge = errors.New("project generation protocol: response exceeds maximum size")

type wireSizer struct {
	size int
}

func measureSuccessDocument(spec wireProjectSpec) (int, error) {
	sizer := &wireSizer{}
	if !sizer.literal(`{"protocol_version":1,"status":"ok","project_spec":`) ||
		!measureProjectSpec(sizer, spec) || !sizer.literal(`}`) {
		return 0, errResponseTooLarge
	}
	return sizer.size, nil
}

func measureProjectSpec(sizer *wireSizer, spec wireProjectSpec) bool {
	if !sizer.literal(`{"project":`) || !measurePackage(sizer, spec.Project) || !sizer.literal(`,"apps":[`) {
		return false
	}
	for index := range spec.Apps {
		if index != 0 && !sizer.literal(`,`) {
			return false
		}
		app := spec.Apps[index]
		if !sizer.literal(`{"alias":`) || !sizer.string(app.Alias) || !sizer.literal(`,"package":`) ||
			!measurePackage(sizer, app.Package) || !sizer.literal(`,"schema":`) ||
			!measureSchema(sizer, app.Schema) || !sizer.literal(`}`) {
			return false
		}
	}
	return sizer.literal(`]}`)
}

func measurePackage(sizer *wireSizer, pkg wirePackage) bool {
	return sizer.literal(`{"package_name":`) && sizer.string(pkg.PackageName) &&
		sizer.literal(`,"import_path":`) && sizer.string(pkg.ImportPath) &&
		sizer.literal(`,"directory":`) && sizer.string(pkg.Directory) && sizer.literal(`}`)
}

func measureSchema(sizer *wireSizer, schema ir.Schema) bool {
	if !sizer.literal(`{"format_version":`) || !sizer.integer(int64(schema.FormatVersion)) ||
		!sizer.literal(`,"app_label":`) || !sizer.string(schema.AppLabel) || !sizer.literal(`,"models":[`) {
		return false
	}
	for index := range schema.Models {
		if index != 0 && !sizer.literal(`,`) {
			return false
		}
		if !measureModel(sizer, schema.Models[index]) {
			return false
		}
	}
	return sizer.literal(`]}`)
}

func measureModel(sizer *wireSizer, model ir.Model) bool {
	if !sizer.literal(`{"name":`) || !sizer.string(model.Name) ||
		!sizer.literal(`,"go_name":`) || !sizer.string(model.GoName) ||
		!sizer.literal(`,"db_table":`) || !sizer.string(model.DBTable) || !sizer.literal(`,"fields":[`) {
		return false
	}
	for index := range model.Fields {
		if index != 0 && !sizer.literal(`,`) {
			return false
		}
		if !measureField(sizer, model.Fields[index]) {
			return false
		}
	}
	return sizer.literal(`]}`)
}

func measureField(sizer *wireSizer, field ir.Field) bool {
	if !sizer.literal(`{"name":`) || !sizer.string(field.Name) ||
		!sizer.literal(`,"go_name":`) || !sizer.string(field.GoName) ||
		!sizer.literal(`,"column":`) || !sizer.string(field.Column) ||
		!sizer.literal(`,"kind":`) || !sizer.string(string(field.Kind)) ||
		!sizer.literal(`,"primary_key":`) || !sizer.boolean(field.PrimaryKey) ||
		!sizer.literal(`,"nullable":`) || !sizer.boolean(field.Nullable) {
		return false
	}
	if field.MaxLength != 0 && (!sizer.literal(`,"max_length":`) || !sizer.integer(int64(field.MaxLength))) {
		return false
	}
	if field.Default != nil && (!sizer.literal(`,"default":`) || !measureDefault(sizer, *field.Default)) {
		return false
	}
	if field.Relation != nil && (!sizer.literal(`,"relation":`) || !measureRelation(sizer, *field.Relation)) {
		return false
	}
	return sizer.literal(`}`)
}

func measureDefault(sizer *wireSizer, value ir.ScalarDefault) bool {
	if !sizer.literal(`{"kind":`) || !sizer.string(string(value.Kind)) {
		return false
	}
	if value.String != "" && (!sizer.literal(`,"string":`) || !sizer.string(value.String)) {
		return false
	}
	if value.Boolean && (!sizer.literal(`,"boolean":`) || !sizer.boolean(true)) {
		return false
	}
	if value.Integer != 0 && (!sizer.literal(`,"integer":`) || !sizer.integer(value.Integer)) {
		return false
	}
	return sizer.literal(`}`)
}

func measureRelation(sizer *wireSizer, value ir.ForeignKeyRelation) bool {
	if !sizer.literal(`{"target":{"app_label":`) || !sizer.string(value.Target.AppLabel) ||
		!sizer.literal(`,"model_name":`) || !sizer.string(value.Target.ModelName) ||
		!sizer.literal(`},"cardinality":`) || !sizer.string(string(value.Cardinality)) ||
		!sizer.literal(`,"reverse":{`) {
		return false
	}
	wroteReverse := false
	if value.Reverse.Name != "" {
		if !sizer.literal(`"name":`) || !sizer.string(value.Reverse.Name) {
			return false
		}
		wroteReverse = true
	}
	if value.Reverse.Disabled {
		if wroteReverse && !sizer.literal(`,`) {
			return false
		}
		if !sizer.literal(`"disabled":true`) {
			return false
		}
	}
	return sizer.literal(`},"on_delete":`) && sizer.string(string(value.OnDelete)) && sizer.literal(`}`)
}

func (sizer *wireSizer) literal(value string) bool {
	return sizer.add(len(value))
}

func (sizer *wireSizer) string(value string) bool {
	encoded, err := json.Marshal(value)
	return err == nil && sizer.add(len(encoded))
}

func (sizer *wireSizer) integer(value int64) bool {
	return sizer.add(len(strconv.FormatInt(value, 10)))
}

func (sizer *wireSizer) boolean(value bool) bool {
	if value {
		return sizer.literal("true")
	}
	return sizer.literal("false")
}

func (sizer *wireSizer) add(count int) bool {
	if count < 0 || sizer.size > MaxResponseBytes || count > MaxResponseBytes-sizer.size {
		sizer.size = MaxResponseBytes + 1
		return false
	}
	sizer.size += count
	return true
}
