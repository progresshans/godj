package ir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"unicode"
	"unicode/utf8"
)

var databaseIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

type ValidationError struct {
	Path string
	Code string
	Info string
}

func (e *ValidationError) Error() string {
	if e.Info == "" {
		return fmt.Sprintf("schema %s: %s", e.Path, e.Code)
	}
	return fmt.Sprintf("schema %s: %s: %s", e.Path, e.Code, e.Info)
}

func Normalize(input Schema) (Schema, error) {
	schema := input.Clone()
	if schema.FormatVersion == 0 {
		schema.FormatVersion = FormatVersion
	}
	if schema.FormatVersion != FormatVersion {
		return Schema{}, validation("format_version", "unsupported_version", fmt.Sprintf("got %d, want %d", schema.FormatVersion, FormatVersion))
	}
	if !databaseIdentifier.MatchString(schema.AppLabel) {
		return Schema{}, validation("app_label", "invalid_identifier", schema.AppLabel)
	}
	if len(schema.Models) == 0 {
		return Schema{}, validation("models", "empty", "at least one model is required")
	}

	modelNames := make(map[string]struct{}, len(schema.Models))
	goNames := make(map[string]struct{}, len(schema.Models))
	tables := make(map[string]struct{}, len(schema.Models))
	for index := range schema.Models {
		modelPath := fmt.Sprintf("models[%d]", index)
		model := &schema.Models[index]
		if !databaseIdentifier.MatchString(model.Name) {
			return Schema{}, validation(modelPath+".name", "invalid_identifier", model.Name)
		}
		if !exportedIdentifier(model.GoName) {
			return Schema{}, validation(modelPath+".go_name", "invalid_go_identifier", model.GoName)
		}
		if model.DBTable == "" {
			model.DBTable = schema.AppLabel + "_" + model.Name
		}
		if !databaseIdentifier.MatchString(model.DBTable) {
			return Schema{}, validation(modelPath+".db_table", "invalid_identifier", model.DBTable)
		}
		if duplicate(modelNames, model.Name) {
			return Schema{}, validation(modelPath+".name", "duplicate", model.Name)
		}
		if duplicate(goNames, model.GoName) {
			return Schema{}, validation(modelPath+".go_name", "duplicate", model.GoName)
		}
		if duplicate(tables, model.DBTable) {
			return Schema{}, validation(modelPath+".db_table", "duplicate", model.DBTable)
		}
		if err := normalizeModel(model, modelPath); err != nil {
			return Schema{}, err
		}
	}
	return schema, nil
}

func normalizeModel(model *Model, path string) error {
	hasAuto := false
	for _, field := range model.Fields {
		if field.Kind == FieldAuto {
			hasAuto = true
			break
		}
	}
	if !hasAuto {
		model.Fields = append([]Field{{
			Name:       "id",
			GoName:     "ID",
			Column:     "id",
			Kind:       FieldAuto,
			PrimaryKey: true,
		}}, model.Fields...)
	}
	if len(model.Fields) == 0 {
		return validation(path+".fields", "empty", "at least the implicit primary key is required")
	}

	names := make(map[string]struct{}, len(model.Fields))
	goNames := make(map[string]struct{}, len(model.Fields))
	columns := make(map[string]struct{}, len(model.Fields))
	primaryKeys := 0
	for index := range model.Fields {
		fieldPath := fmt.Sprintf("%s.fields[%d]", path, index)
		field := &model.Fields[index]
		if field.Column == "" {
			field.Column = field.Name
		}
		if !databaseIdentifier.MatchString(field.Name) {
			return validation(fieldPath+".name", "invalid_identifier", field.Name)
		}
		if !exportedIdentifier(field.GoName) {
			return validation(fieldPath+".go_name", "invalid_go_identifier", field.GoName)
		}
		if !databaseIdentifier.MatchString(field.Column) {
			return validation(fieldPath+".column", "invalid_identifier", field.Column)
		}
		if duplicate(names, field.Name) {
			return validation(fieldPath+".name", "duplicate", field.Name)
		}
		if duplicate(goNames, field.GoName) {
			return validation(fieldPath+".go_name", "duplicate", field.GoName)
		}
		if duplicate(columns, field.Column) {
			return validation(fieldPath+".column", "duplicate", field.Column)
		}
		if field.PrimaryKey {
			primaryKeys++
		}
		if err := validateField(*field, fieldPath); err != nil {
			return err
		}
	}
	if primaryKeys != 1 {
		return validation(path+".fields", "primary_key_count", fmt.Sprintf("got %d, want 1", primaryKeys))
	}
	return nil
}

func validateField(field Field, path string) error {
	if field.Default != nil {
		if err := validateScalarDefault(*field.Default, path+".default"); err != nil {
			return err
		}
	}
	switch field.Kind {
	case FieldAuto:
		if !field.PrimaryKey {
			return validation(path+".primary_key", "required", "AutoField must be the primary key")
		}
		if field.Nullable {
			return validation(path+".nullable", "unsupported", "AutoField cannot be nullable")
		}
		if field.MaxLength != 0 {
			return validation(path+".max_length", "unsupported", "AutoField has no max length")
		}
		if field.Default != nil {
			return validation(path+".default", "unsupported", "AutoField default is database generated")
		}
	case FieldChar:
		if field.PrimaryKey {
			return validation(path+".primary_key", "unsupported", "M1 supports only AutoField primary keys")
		}
		if field.MaxLength <= 0 {
			return validation(path+".max_length", "invalid", "CharField max length must be positive")
		}
		if field.Default != nil {
			if field.Default.Kind != ScalarString {
				return validation(path+".default", "type_mismatch", "CharField default must be a string")
			}
			if utf8.RuneCountInString(field.Default.String) > field.MaxLength {
				return validation(path+".default", "max_length", "CharField default exceeds max length")
			}
		}
	case FieldBoolean:
		if field.PrimaryKey {
			return validation(path+".primary_key", "unsupported", "M1 supports only AutoField primary keys")
		}
		if field.Nullable {
			return validation(path+".nullable", "unsupported", "nullable BooleanField is outside M1")
		}
		if field.MaxLength != 0 {
			return validation(path+".max_length", "unsupported", "BooleanField has no max length")
		}
		if field.Default != nil && field.Default.Kind != ScalarBoolean {
			return validation(path+".default", "type_mismatch", "BooleanField default must be a boolean")
		}
	default:
		return validation(path+".kind", "unsupported_field_kind", string(field.Kind))
	}
	return nil
}

func validateScalarDefault(value ScalarDefault, path string) error {
	switch value.Kind {
	case ScalarString:
		if !utf8.ValidString(value.String) {
			return validation(path, "invalid_utf8", "string default must contain valid UTF-8")
		}
		if value.Boolean || value.Integer != 0 {
			return validation(path, "invalid_scalar", "string default carries another scalar payload")
		}
	case ScalarBoolean:
		if value.String != "" || value.Integer != 0 {
			return validation(path, "invalid_scalar", "boolean default carries another scalar payload")
		}
	case ScalarInteger:
		if value.String != "" || value.Boolean {
			return validation(path, "invalid_scalar", "integer default carries another scalar payload")
		}
	default:
		return validation(path+".kind", "unsupported_scalar_kind", string(value.Kind))
	}
	return nil
}

func CanonicalJSON(input Schema) ([]byte, error) {
	normalized, err := Normalize(input)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized schema: %w", err)
	}
	return append(data, '\n'), nil
}

func Hash(input Schema) (string, error) {
	canonical, err := CanonicalJSON(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func validation(path, code, info string) error {
	return &ValidationError{Path: path, Code: code, Info: info}
}

func duplicate(seen map[string]struct{}, value string) bool {
	if _, exists := seen[value]; exists {
		return true
	}
	seen[value] = struct{}{}
	return false
}

func exportedIdentifier(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	first, size := utf8.DecodeRuneInString(value)
	if first == utf8.RuneError || !unicode.IsUpper(first) {
		return false
	}
	for _, current := range value[size:] {
		if current != '_' && !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			return false
		}
	}
	return true
}
