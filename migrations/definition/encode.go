package definition

import (
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

// Encode snapshots and validates one current migration, then returns its
// deterministic current-format definition document. The returned document is
// compact JSON with exactly one trailing newline.
func Encode(producer Producer, migration migrations.Migration) ([]byte, error) {
	if err := preflightEncodingResources(producer, migration); err != nil {
		return nil, err
	}
	snapshot, err := snapshotMigrationForEncoding(migration)
	if err != nil {
		return nil, err
	}
	if err := validateEncodingInput(producer, snapshot); err != nil {
		return nil, err
	}

	document := definitionDocument{
		FormatVersion: DefinitionFormatVersion,
		Producer: producerDocument{
			Name:    producer.Name,
			Version: producer.Version,
		},
		Migration: migrationDocument{
			App:          snapshot.App,
			Name:         snapshot.Name,
			Dependencies: make([]dependencyDocument, len(snapshot.Dependencies)),
			Operations:   make([]any, len(snapshot.Operations)),
		},
	}
	for index, dependency := range snapshot.Dependencies {
		document.Migration.Dependencies[index] = dependencyDocument{
			App:  dependency.App,
			Name: dependency.Name,
		}
	}
	for index, operation := range snapshot.Operations {
		switch value := operation.(type) {
		case migrations.CreateModel:
			document.Migration.Operations[index] = createModelDocument{
				Kind:     "create_model",
				AppLabel: value.AppLabel,
				Model:    encodeModel(value.Model),
			}
		case migrations.AddField:
			document.Migration.Operations[index] = addFieldDocument{
				Kind:      "add_field",
				AppLabel:  value.AppLabel,
				ModelName: value.ModelName,
				Field:     encodeField(value.Field),
			}
		default:
			return nil, encodeFailure(
				fmt.Sprintf("migration.operations[%d]", index),
				"unsupported operation type %T",
				operation,
			)
		}
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode migration definition: marshal canonical document: %w", err)
	}
	actualDocumentBytes := uint64(len(encoded)) + 1
	if actualDocumentBytes > MaxDocumentBytes {
		return nil, encodeFailure(
			"document",
			"document_bytes resource limit exceeded: got %d, maximum %d",
			actualDocumentBytes,
			MaxDocumentBytes,
		)
	}
	return append(encoded, '\n'), nil
}

func snapshotMigrationForEncoding(migration migrations.Migration) (migrations.Migration, error) {
	if len(migration.Dependencies) > MaxDependenciesPerMigration {
		return migrations.Migration{}, encodeFailure(
			"migration.dependencies",
			"dependencies_per_migration resource limit exceeded: got %d, maximum %d",
			len(migration.Dependencies),
			MaxDependenciesPerMigration,
		)
	}
	if len(migration.Operations) > MaxOperationsPerMigration {
		return migrations.Migration{}, encodeFailure(
			"migration.operations",
			"operations_per_migration resource limit exceeded: got %d, maximum %d",
			len(migration.Operations),
			MaxOperationsPerMigration,
		)
	}

	snapshot := migrations.Migration{
		App:          migration.App,
		Name:         migration.Name,
		Dependencies: append([]migrations.MigrationKey(nil), migration.Dependencies...),
		Operations:   make([]migrations.Operation, len(migration.Operations)),
	}
	for index, operation := range migration.Operations {
		switch value := operation.(type) {
		case migrations.CreateModel:
			if len(value.Model.Fields) > MaxFieldsPerCreateModel {
				return migrations.Migration{}, createModelFieldsLimitFailure(index, len(value.Model.Fields))
			}
			snapshot.Operations[index] = migrations.CreateModel{
				AppLabel: value.AppLabel,
				Model:    value.Model.Clone(),
			}
		case *migrations.CreateModel:
			if value == nil {
				return migrations.Migration{}, encodeFailure(
					fmt.Sprintf("migration.operations[%d]", index),
					"nil *migrations.CreateModel",
				)
			}
			if len(value.Model.Fields) > MaxFieldsPerCreateModel {
				return migrations.Migration{}, createModelFieldsLimitFailure(index, len(value.Model.Fields))
			}
			snapshot.Operations[index] = migrations.CreateModel{
				AppLabel: value.AppLabel,
				Model:    value.Model.Clone(),
			}
		case migrations.AddField:
			snapshot.Operations[index] = migrations.AddField{
				AppLabel:  value.AppLabel,
				ModelName: value.ModelName,
				Field:     value.Field.Clone(),
			}
		case *migrations.AddField:
			if value == nil {
				return migrations.Migration{}, encodeFailure(
					fmt.Sprintf("migration.operations[%d]", index),
					"nil *migrations.AddField",
				)
			}
			snapshot.Operations[index] = migrations.AddField{
				AppLabel:  value.AppLabel,
				ModelName: value.ModelName,
				Field:     value.Field.Clone(),
			}
		case nil:
			return migrations.Migration{}, encodeFailure(
				fmt.Sprintf("migration.operations[%d]", index),
				"nil operation",
			)
		default:
			return migrations.Migration{}, encodeFailure(
				fmt.Sprintf("migration.operations[%d]", index),
				"unsupported operation type %T",
				operation,
			)
		}
	}

	sort.Slice(snapshot.Dependencies, func(left, right int) bool {
		if snapshot.Dependencies[left].App != snapshot.Dependencies[right].App {
			return snapshot.Dependencies[left].App < snapshot.Dependencies[right].App
		}
		return snapshot.Dependencies[left].Name < snapshot.Dependencies[right].Name
	})
	return snapshot, nil
}

func createModelFieldsLimitFailure(operationIndex, actual int) error {
	return encodeFailure(
		fmt.Sprintf("migration.operations[%d].model.fields", operationIndex),
		"fields_per_create_model resource limit exceeded: got %d, maximum %d",
		actual,
		MaxFieldsPerCreateModel,
	)
}

func validateEncodingInput(producer Producer, migration migrations.Migration) error {
	if producer.Name == "" {
		return encodeFailure("producer.name", "empty producer name")
	}
	if !utf8.ValidString(producer.Name) {
		return encodeFailure("producer.name", "producer name is not valid UTF-8")
	}
	if producer.Version == "" {
		return encodeFailure("producer.version", "empty producer version")
	}
	if !utf8.ValidString(producer.Version) {
		return encodeFailure("producer.version", "producer version is not valid UTF-8")
	}
	if !validAppLabel(migration.App) {
		return encodeFailure("migration.app", "invalid current app identity %q", migration.App)
	}
	if migration.Name == "" {
		return encodeFailure("migration.name", "empty migration name")
	}
	if !utf8.ValidString(migration.Name) {
		return encodeFailure("migration.name", "migration name is not valid UTF-8")
	}

	for index, dependency := range migration.Dependencies {
		path := fmt.Sprintf("migration.dependencies[%d]", index)
		if !validAppLabel(dependency.App) {
			return encodeFailure(path+".app", "invalid current app identity %q", dependency.App)
		}
		if dependency.Name == "" {
			return encodeFailure(path+".name", "empty migration name")
		}
		if !utf8.ValidString(dependency.Name) {
			return encodeFailure(path+".name", "migration name is not valid UTF-8")
		}
		if dependency == migration.Key() {
			return encodeFailure(path, "self dependency %s.%s", dependency.App, dependency.Name)
		}
		if index != 0 && dependency == migration.Dependencies[index-1] {
			return encodeFailure(path, "duplicate dependency %s.%s", dependency.App, dependency.Name)
		}
	}

	for index, operation := range migration.Operations {
		path := fmt.Sprintf("migration.operations[%d]", index)
		switch value := operation.(type) {
		case migrations.CreateModel:
			if value.AppLabel != migration.App {
				return encodeFailure(path+".app_label", "operation app %q does not match migration app %q", value.AppLabel, migration.App)
			}
			if !fullyNormalizedCreateModel(value.AppLabel, value.Model) {
				return encodeFailure(path+".model", "model is not exact current normalized IR")
			}
			for fieldIndex, field := range value.Model.Fields {
				if err := validateFieldWireRange(field, fmt.Sprintf("%s.model.fields[%d]", path, fieldIndex)); err != nil {
					return err
				}
			}
		case migrations.AddField:
			if value.AppLabel != migration.App {
				return encodeFailure(path+".app_label", "operation app %q does not match migration app %q", value.AppLabel, migration.App)
			}
			if !validAddFieldModelName(value.ModelName) {
				return encodeFailure(path+".model_name", "invalid current model identity %q", value.ModelName)
			}
			if !fullyNormalizedAddField(value.AppLabel, value.Field) {
				return encodeFailure(path+".field", "field is not exact current normalized IR")
			}
			if err := validateFieldWireRange(value.Field, path+".field"); err != nil {
				return err
			}
		default:
			return encodeFailure(path, "unsupported operation type %T", operation)
		}
	}
	return nil
}

func validateFieldWireRange(field ir.Field, path string) error {
	if field.MaxLength < 0 || int64(field.MaxLength) > maximumWireLength {
		return encodeFailure(path+".max_length", "max_length is outside current wire range: %d", field.MaxLength)
	}
	return nil
}

func encodeFailure(path, format string, arguments ...any) error {
	return fmt.Errorf("encode migration definition %s: %s", path, fmt.Sprintf(format, arguments...))
}

type definitionDocument struct {
	FormatVersion int64             `json:"format_version"`
	Producer      producerDocument  `json:"producer"`
	Migration     migrationDocument `json:"migration"`
}

type producerDocument struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type migrationDocument struct {
	App          string               `json:"app"`
	Name         string               `json:"name"`
	Dependencies []dependencyDocument `json:"dependencies"`
	Operations   []any                `json:"operations"`
}

type dependencyDocument struct {
	App  string `json:"app"`
	Name string `json:"name"`
}

type createModelDocument struct {
	Kind     string        `json:"kind"`
	AppLabel string        `json:"app_label"`
	Model    modelDocument `json:"model"`
}

type addFieldDocument struct {
	Kind      string        `json:"kind"`
	AppLabel  string        `json:"app_label"`
	ModelName string        `json:"model_name"`
	Field     fieldDocument `json:"field"`
}

type modelDocument struct {
	Name    string          `json:"name"`
	GoName  string          `json:"go_name"`
	DBTable string          `json:"db_table"`
	Fields  []fieldDocument `json:"fields"`
}

type fieldDocument struct {
	Name       string            `json:"name"`
	GoName     string            `json:"go_name"`
	Column     string            `json:"column"`
	Kind       ir.FieldKind      `json:"kind"`
	PrimaryKey bool              `json:"primary_key"`
	Nullable   bool              `json:"nullable"`
	MaxLength  int               `json:"max_length"`
	Default    any               `json:"default"`
	Relation   *relationDocument `json:"relation,omitempty"`
}

type stringDefaultDocument struct {
	Kind   ir.ScalarKind `json:"kind"`
	String string        `json:"string"`
}

type booleanDefaultDocument struct {
	Kind    ir.ScalarKind `json:"kind"`
	Boolean bool          `json:"boolean"`
}

type integerDefaultDocument struct {
	Kind    ir.ScalarKind `json:"kind"`
	Integer int64         `json:"integer"`
}

type relationDocument struct {
	Target      relationTargetDocument `json:"target"`
	Cardinality ir.RelationCardinality `json:"cardinality"`
	Reverse     reverseDocument        `json:"reverse"`
	OnDelete    ir.DeletePolicy        `json:"on_delete"`
}

type relationTargetDocument struct {
	AppLabel  string `json:"app_label"`
	ModelName string `json:"model_name"`
}

type reverseDocument struct {
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
}

func encodeModel(model ir.Model) modelDocument {
	encoded := modelDocument{
		Name:    model.Name,
		GoName:  model.GoName,
		DBTable: model.DBTable,
		Fields:  make([]fieldDocument, len(model.Fields)),
	}
	for index, field := range model.Fields {
		encoded.Fields[index] = encodeField(field)
	}
	return encoded
}

func encodeField(field ir.Field) fieldDocument {
	encoded := fieldDocument{
		Name:       field.Name,
		GoName:     field.GoName,
		Column:     field.Column,
		Kind:       field.Kind,
		PrimaryKey: field.PrimaryKey,
		Nullable:   field.Nullable,
		MaxLength:  field.MaxLength,
		Default:    encodeDefault(field.Default),
	}
	if field.Relation != nil {
		encoded.Relation = &relationDocument{
			Target: relationTargetDocument{
				AppLabel:  field.Relation.Target.AppLabel,
				ModelName: field.Relation.Target.ModelName,
			},
			Cardinality: field.Relation.Cardinality,
			Reverse: reverseDocument{
				Name:     field.Relation.Reverse.Name,
				Disabled: field.Relation.Reverse.Disabled,
			},
			OnDelete: field.Relation.OnDelete,
		}
	}
	return encoded
}

func encodeDefault(value *ir.ScalarDefault) any {
	if value == nil {
		return nil
	}
	switch value.Kind {
	case ir.ScalarString:
		return stringDefaultDocument{Kind: value.Kind, String: value.String}
	case ir.ScalarBoolean:
		return booleanDefaultDocument{Kind: value.Kind, Boolean: value.Boolean}
	case ir.ScalarInteger:
		return integerDefaultDocument{Kind: value.Kind, Integer: value.Integer}
	default:
		// The exact normalized IR check rejects this before materialization.
		return nil
	}
}
