package projectwire

import (
	"encoding/json"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func parseManyToMany(decoder *json.Decoder, budget *specBudget) error {
	stringValue := func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err }
	identity := func() error {
		return wirejson.Object(decoder, []string{"app_label", "model_name"}, map[string]func() error{"app_label": stringValue, "model_name": stringValue})
	}
	return wirejson.Object(decoder, []string{"name", "go_name", "target", "reverse", "symmetry"}, map[string]func() error{
		"name": stringValue, "go_name": stringValue, "symmetry": stringValue, "target": identity,
		"reverse": func() error {
			return wirejson.Object(decoder, nil, map[string]func() error{"name": stringValue, "disabled": func() error { return wirejson.Bool(decoder) }})
		},
		"through": func() error {
			if err := budget.consumeNodes(2); err != nil {
				return err
			}
			return wirejson.Object(decoder, []string{"model", "source_field", "target_field"}, map[string]func() error{"model": identity, "source_field": stringValue, "target_field": stringValue})
		},
	})
}

func measureManyToMany(sizer *wirejson.Sizer, field ir.ManyToManyField) bool {
	identity := func(value ir.ModelIdentity) bool {
		return sizer.Literal(`{"app_label":`) && sizer.String(value.AppLabel) && sizer.Literal(`,"model_name":`) && sizer.String(value.ModelName) && sizer.Literal(`}`)
	}
	if !sizer.Literal(`{"name":`) || !sizer.String(field.Name) || !sizer.Literal(`,"go_name":`) || !sizer.String(field.GoName) ||
		!sizer.Literal(`,"target":`) || !identity(field.Target) || !sizer.Literal(`,"reverse":{`) {
		return false
	}
	if field.Reverse.Name != "" && (!sizer.Literal(`"name":`) || !sizer.String(field.Reverse.Name)) {
		return false
	}
	if field.Reverse.Disabled {
		if field.Reverse.Name != "" && !sizer.Literal(`,`) {
			return false
		}
		if !sizer.Literal(`"disabled":true`) {
			return false
		}
	}
	if !sizer.Literal(`},"symmetry":`) || !sizer.String(string(field.Symmetry)) {
		return false
	}
	if through := field.Through; through != nil {
		if !sizer.Literal(`,"through":{"model":`) || !identity(through.Model) || !sizer.Literal(`,"source_field":`) || !sizer.String(through.SourceField) ||
			!sizer.Literal(`,"target_field":`) || !sizer.String(through.TargetField) || !sizer.Literal(`}`) {
			return false
		}
	}
	return sizer.Literal(`}`)
}
