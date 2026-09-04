package definition

import (
	"reflect"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func cloneField(field ir.Field) ir.Field {
	return field.Clone()
}

func cloneOperation(operation migrations.Operation) migrations.Operation {
	switch value := operation.(type) {
	case migrations.CreateModel:
		return migrations.CreateModel{AppLabel: value.AppLabel, Model: value.Model.Clone()}
	case *migrations.CreateModel:
		if value == nil {
			return (*migrations.CreateModel)(nil)
		}
		clone := migrations.CreateModel{AppLabel: value.AppLabel, Model: value.Model.Clone()}
		return &clone
	case migrations.AddField:
		return migrations.AddField{
			AppLabel:  value.AppLabel,
			ModelName: value.ModelName,
			Field:     cloneField(value.Field),
		}
	case *migrations.AddField:
		if value == nil {
			return (*migrations.AddField)(nil)
		}
		clone := migrations.AddField{
			AppLabel:  value.AppLabel,
			ModelName: value.ModelName,
			Field:     cloneField(value.Field),
		}
		return &clone
	default:
		return operation
	}
}

func cloneMigration(migration migrations.Migration) migrations.Migration {
	clone := migrations.Migration{
		App:          migration.App,
		Name:         migration.Name,
		Dependencies: make([]migrations.MigrationKey, len(migration.Dependencies)),
		Operations:   make([]migrations.Operation, len(migration.Operations)),
	}
	copy(clone.Dependencies, migration.Dependencies)
	for index, operation := range migration.Operations {
		clone.Operations[index] = cloneOperation(operation)
	}
	return clone
}

func cloneMigrations(definitions []migrations.Migration) []migrations.Migration {
	clones := make([]migrations.Migration, len(definitions))
	for index, definition := range definitions {
		clones[index] = cloneMigration(definition)
	}
	return clones
}

func fixedAutoField() ir.Field {
	return ir.Field{
		Name:       "_godj_loader_pk",
		GoName:     "GodjLoaderPK",
		Column:     "_godj_loader_pk",
		Kind:       ir.FieldAuto,
		PrimaryKey: true,
	}
}

func fixedValidationModel() ir.Model {
	return ir.Model{
		Name:    "_godj_loader_validation",
		GoName:  "GodjLoaderValidation",
		DBTable: "_godj_loader_validation",
		Fields:  []ir.Field{fixedAutoField()},
	}
}

func exactNormalized(schema ir.Schema) bool {
	normalized, err := ir.Normalize(schema)
	return err == nil && reflect.DeepEqual(normalized, schema)
}

func validAppLabel(value string) bool {
	return exactNormalized(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      value,
		Models:        []ir.Model{fixedValidationModel()},
	})
}

func validModelName(value string) bool {
	model := fixedValidationModel()
	model.Name = value
	return exactNormalized(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "_godj_loader_validation",
		Models:        []ir.Model{model},
	})
}

func validModelGoName(value string) bool {
	model := fixedValidationModel()
	model.GoName = value
	return exactNormalized(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "_godj_loader_validation",
		Models:        []ir.Model{model},
	})
}

func validModelTable(value string) bool {
	model := fixedValidationModel()
	model.DBTable = value
	return exactNormalized(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "_godj_loader_validation",
		Models:        []ir.Model{model},
	})
}

func validFieldName(value string) bool {
	field := fixedAutoField()
	field.Name = value
	model := fixedValidationModel()
	model.Fields = []ir.Field{field}
	return exactNormalized(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "_godj_loader_validation",
		Models:        []ir.Model{model},
	})
}

func validFieldGoName(value string) bool {
	field := fixedAutoField()
	field.GoName = value
	model := fixedValidationModel()
	model.Fields = []ir.Field{field}
	return exactNormalized(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "_godj_loader_validation",
		Models:        []ir.Model{model},
	})
}

func validFieldColumn(value string) bool {
	field := fixedAutoField()
	field.Column = value
	model := fixedValidationModel()
	model.Fields = []ir.Field{field}
	return exactNormalized(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "_godj_loader_validation",
		Models:        []ir.Model{model},
	})
}

func fullyNormalizedCreateModel(appLabel string, model ir.Model) bool {
	wrapper := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      appLabel,
		Models:        []ir.Model{model.Clone()},
	}
	return exactNormalized(wrapper)
}

// validAddFieldModelName intentionally delegates the identifier decision to
// schema/ir.Normalize. A product-local regex would duplicate and eventually
// drift from the canonical IR rule.
func validAddFieldModelName(modelName string) bool {
	model := fixedValidationModel()
	model.Name = modelName
	wrapper := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "_godj_loader_validation",
		Models:        []ir.Model{model},
	}
	return exactNormalized(wrapper)
}

func fullyNormalizedAddField(appLabel string, field ir.Field) bool {
	if field.PrimaryKey || (field.Kind != ir.FieldChar && field.Kind != ir.FieldBoolean && field.Kind != ir.FieldForeignKey) {
		return false
	}

	syntheticName := "_godj_loader_pk"
	syntheticGoName := "GodjLoaderPK"
	syntheticColumn := "_godj_loader_pk"
	for field.Name == syntheticName || field.GoName == syntheticGoName || field.Column == syntheticColumn {
		syntheticName += "_"
		syntheticGoName += "X"
		syntheticColumn += "_"
	}
	synthetic := ir.Field{
		Name:       syntheticName,
		GoName:     syntheticGoName,
		Column:     syntheticColumn,
		Kind:       ir.FieldAuto,
		PrimaryKey: true,
	}
	model := ir.Model{
		Name:    "_godj_loader_validation",
		GoName:  "GodjLoaderValidation",
		DBTable: "_godj_loader_validation",
		Fields:  []ir.Field{synthetic, cloneField(field)},
	}
	wrapper := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      appLabel,
		Models:        []ir.Model{model},
	}
	normalized, err := ir.Normalize(wrapper)
	return err == nil &&
		reflect.DeepEqual(normalized, wrapper) &&
		len(normalized.Models) == 1 &&
		len(normalized.Models[0].Fields) == 2 &&
		reflect.DeepEqual(normalized.Models[0].Fields[1], field)
}
