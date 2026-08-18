package definition

import (
	"fmt"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/internal/definitionhandoff"
	"github.com/progresshans/godj/schema/ir"
)

func newDefinitionHandoff(decoded []decodedDocument) (definitionhandoff.Handoff, error) {
	records := make([]definitionhandoff.Record, len(decoded))
	for index := range decoded {
		definition, err := handoffDefinition(decoded[index].migration)
		if err != nil {
			return definitionhandoff.Handoff{}, err
		}
		records[index] = definitionhandoff.Record{
			SourceID: decoded[index].source.sourceID,
			Producer: definitionhandoff.Producer{
				Name:    decoded[index].producer.Name,
				Version: decoded[index].producer.Version,
			},
			Profile:    handoffCompatibility(decoded[index].compatibility),
			Definition: definition,
		}
	}
	return definitionhandoff.New(records)
}

func handoffDefinition(value migrations.Migration) (definitionhandoff.Definition, error) {
	definition := definitionhandoff.Definition{
		App:          value.App,
		Name:         value.Name,
		Dependencies: make([]definitionhandoff.Identity, len(value.Dependencies)),
		Operations:   make([]definitionhandoff.Operation, len(value.Operations)),
	}
	for index := range value.Dependencies {
		definition.Dependencies[index] = definitionhandoff.Identity{
			App:  value.Dependencies[index].App,
			Name: value.Dependencies[index].Name,
		}
	}
	for index, operation := range value.Operations {
		converted, err := handoffOperation(operation)
		if err != nil {
			return definitionhandoff.Definition{}, fmt.Errorf("operation %d: %w", index, err)
		}
		definition.Operations[index] = converted
	}
	return definition, nil
}

func handoffOperation(value migrations.Operation) (definitionhandoff.Operation, error) {
	switch operation := value.(type) {
	case migrations.CreateModel:
		return definitionhandoff.Operation{
			Kind: "create_model", AppLabel: operation.AppLabel,
			HasModel: true, Model: handoffModel(operation.Model),
		}, nil
	case *migrations.CreateModel:
		if operation == nil {
			return definitionhandoff.Operation{}, fmt.Errorf("nil *migrations.CreateModel")
		}
		return definitionhandoff.Operation{
			Kind: "create_model", AppLabel: operation.AppLabel,
			HasModel: true, Model: handoffModel(operation.Model),
		}, nil
	case migrations.AddField:
		return definitionhandoff.Operation{
			Kind: "add_field", AppLabel: operation.AppLabel, ModelName: operation.ModelName,
			HasField: true, Field: handoffField(operation.Field),
		}, nil
	case *migrations.AddField:
		if operation == nil {
			return definitionhandoff.Operation{}, fmt.Errorf("nil *migrations.AddField")
		}
		return definitionhandoff.Operation{
			Kind: "add_field", AppLabel: operation.AppLabel, ModelName: operation.ModelName,
			HasField: true, Field: handoffField(operation.Field),
		}, nil
	default:
		return definitionhandoff.Operation{}, fmt.Errorf("unsupported operation %T", value)
	}
}

func handoffModel(value ir.Model) definitionhandoff.Model {
	model := definitionhandoff.Model{
		Name: value.Name, GoName: value.GoName, DBTable: value.DBTable,
		Fields: make([]definitionhandoff.Field, len(value.Fields)),
	}
	for index := range value.Fields {
		model.Fields[index] = handoffField(value.Fields[index])
	}
	return model
}

func handoffField(value ir.Field) definitionhandoff.Field {
	field := definitionhandoff.Field{
		Name: value.Name, GoName: value.GoName, Column: value.Column, Kind: string(value.Kind),
		PrimaryKey: value.PrimaryKey, Nullable: value.Nullable, MaxLength: int64(value.MaxLength),
	}
	if value.Default != nil {
		field.Default = definitionhandoff.Default{
			Present: true, Kind: string(value.Default.Kind), String: value.Default.String,
			Boolean: value.Default.Boolean, Integer: value.Default.Integer,
		}
	}
	if value.Relation != nil {
		field.Relation = definitionhandoff.Relation{
			Present: true, TargetApp: value.Relation.Target.AppLabel, TargetModel: value.Relation.Target.ModelName,
			Cardinality: string(value.Relation.Cardinality), ReverseName: value.Relation.Reverse.Name,
			ReverseDisabled: value.Relation.Reverse.Disabled, OnDelete: string(value.Relation.OnDelete),
		}
	}
	return field
}
