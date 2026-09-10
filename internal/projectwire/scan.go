package projectwire

import (
	"encoding/json"
	"fmt"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

type specBudget struct{ fields, nodes uint64 }

// Scan consumes one closed ProjectSpec after the enclosing wirejson.Scan. It
// applies field/model/aggregate schema limits before any typed decode or clone.
func Scan(decoder *json.Decoder) error {
	budget := &specBudget{}
	return wirejson.Object(decoder, []string{"project", "apps"}, map[string]func() error{
		"project": func() error { return parsePackage(decoder) },
		"apps": func() error {
			return wirejson.Array(decoder, MaxApps, func(index int) error {
				if err := budget.consumeNodes(1); err != nil {
					return err
				}
				return parseApp(decoder, budget)
			})
		},
	})
}

func parsePackage(decoder *json.Decoder) error {
	return wirejson.Object(decoder, []string{"package_name", "import_path", "directory"}, map[string]func() error{
		"package_name": func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"import_path":  func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"directory":    func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
	})
}

func parseApp(decoder *json.Decoder, budget *specBudget) error {
	return wirejson.Object(decoder, []string{"alias", "package", "schema"}, map[string]func() error{
		"alias":   func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"package": func() error { return parsePackage(decoder) },
		"schema":  func() error { return parseSchema(decoder, budget) },
	})
}

func parseSchema(decoder *json.Decoder, budget *specBudget) error {
	return wirejson.Object(decoder, []string{"format_version", "app_label", "models"}, map[string]func() error{
		"format_version": func() error {
			version, err := wirejson.UintToken(decoder)
			if err == nil && version != uint64(ir.CurrentFormatVersion) {
				return fmt.Errorf("schema format version %d is incompatible", version)
			}
			return err
		},
		"app_label": func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"models": func() error {
			return wirejson.Array(decoder, projectspec.MaxModelsPerApp, func(int) error {
				if err := budget.consumeNodes(1); err != nil {
					return err
				}
				return parseModel(decoder, budget)
			})
		},
	})
}

func parseModel(decoder *json.Decoder, budget *specBudget) error {
	return wirejson.Object(decoder, []string{"name", "go_name", "db_table", "fields"}, map[string]func() error{
		"name":     func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"go_name":  func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"db_table": func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"fields": func() error {
			return wirejson.Array(decoder, projectspec.MaxFieldsPerModel, func(int) error {
				if err := budget.consumeFields(1); err != nil {
					return err
				}
				if err := budget.consumeNodes(1); err != nil {
					return err
				}
				return parseField(decoder, budget)
			})
		},
	})
}

func parseField(decoder *json.Decoder, budget *specBudget) error {
	required := []string{"name", "go_name", "column", "kind", "primary_key", "nullable"}
	return wirejson.Object(decoder, required, map[string]func() error{
		"name":        func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"go_name":     func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"column":      func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"kind":        func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"primary_key": func() error { return wirejson.Bool(decoder) },
		"nullable":    func() error { return wirejson.Bool(decoder) },
		"max_length":  func() error { _, err := wirejson.IntToken(decoder); return err },
		"default": func() error {
			if err := budget.consumeNodes(1); err != nil {
				return err
			}
			return parseDefault(decoder)
		},
		"relation": func() error {
			if err := budget.consumeNodes(3); err != nil {
				return err
			}
			return parseRelation(decoder)
		},
	})
}

func parseDefault(decoder *json.Decoder) error {
	return wirejson.Object(decoder, []string{"kind"}, map[string]func() error{
		"kind":    func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"string":  func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"boolean": func() error { return wirejson.Bool(decoder) },
		"integer": func() error { _, err := wirejson.IntToken(decoder); return err },
	})
}

func parseRelation(decoder *json.Decoder) error {
	return wirejson.Object(decoder, []string{"target", "cardinality", "reverse", "on_delete"}, map[string]func() error{
		"target": func() error {
			return wirejson.Object(decoder, []string{"app_label", "model_name"}, map[string]func() error{
				"app_label":  func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
				"model_name": func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
			})
		},
		"cardinality": func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
		"reverse": func() error {
			return wirejson.Object(decoder, nil, map[string]func() error{
				"name":     func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
				"disabled": func() error { return wirejson.Bool(decoder) },
			})
		},
		"on_delete": func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err },
	})
}

func (budget *specBudget) consumeFields(count uint64) error {
	if budget.fields > projectspec.MaxAggregateFields || count > projectspec.MaxAggregateFields-budget.fields {
		return fmt.Errorf("aggregate schema fields exceed %d", projectspec.MaxAggregateFields)
	}
	budget.fields += count
	return nil
}

func (budget *specBudget) consumeNodes(count uint64) error {
	if budget.nodes > projectspec.MaxAggregateNodes || count > projectspec.MaxAggregateNodes-budget.nodes {
		return fmt.Errorf("aggregate schema nodes exceed %d", projectspec.MaxAggregateNodes)
	}
	budget.nodes += count
	return nil
}
