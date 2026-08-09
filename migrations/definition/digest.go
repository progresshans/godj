package definition

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

const digestDomain = "godj:migration-definition-set:v1"

// definitionSetDigest hashes only the canonical semantic definition set. Raw
// source bytes, source identifiers, and producer provenance are deliberately
// outside this boundary.
func definitionSetDigest(definitions []migrations.Migration) (string, error) {
	canonical, err := canonicalDefinitionSet(definitions)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalDefinitionSet(definitions []migrations.Migration) ([]byte, error) {
	canonical := cloneMigrations(definitions)
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].App != canonical[right].App {
			return canonical[left].App < canonical[right].App
		}
		return canonical[left].Name < canonical[right].Name
	})
	for index := range canonical {
		sort.Slice(canonical[index].Dependencies, func(left, right int) bool {
			if canonical[index].Dependencies[left].App != canonical[index].Dependencies[right].App {
				return canonical[index].Dependencies[left].App < canonical[index].Dependencies[right].App
			}
			return canonical[index].Dependencies[left].Name < canonical[index].Dependencies[right].Name
		})
	}

	output := []byte(`{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"definitions":[`)
	for definitionIndex, current := range canonical {
		if definitionIndex != 0 {
			output = append(output, ',')
		}
		output = append(output, `{"app":`...)
		var err error
		output, err = appendCanonicalString(output, current.App)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"dependencies":[`...)
		for dependencyIndex, dependency := range current.Dependencies {
			if dependencyIndex != 0 {
				output = append(output, ',')
			}
			output = append(output, `{"app":`...)
			output, err = appendCanonicalString(output, dependency.App)
			if err != nil {
				return nil, err
			}
			output = append(output, `,"name":`...)
			output, err = appendCanonicalString(output, dependency.Name)
			if err != nil {
				return nil, err
			}
			output = append(output, '}')
		}
		output = append(output, `],"name":`...)
		output, err = appendCanonicalString(output, current.Name)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"operations":[`...)
		for operationIndex, operation := range current.Operations {
			if operationIndex != 0 {
				output = append(output, ',')
			}
			output, err = appendCanonicalOperation(output, operation)
			if err != nil {
				return nil, err
			}
		}
		output = append(output, ']', '}')
	}
	output = append(output, `],"domain":`...)
	var err error
	output, err = appendCanonicalString(output, digestDomain)
	if err != nil {
		return nil, err
	}
	return append(output, '}'), nil
}

func appendCanonicalOperation(output []byte, operation migrations.Operation) ([]byte, error) {
	switch value := operation.(type) {
	case migrations.CreateModel:
		return appendCanonicalCreateModel(output, value)
	case *migrations.CreateModel:
		if value == nil {
			return nil, errors.New("canonical operation is a nil *migrations.CreateModel")
		}
		return appendCanonicalCreateModel(output, *value)
	case migrations.AddField:
		return appendCanonicalAddField(output, value)
	case *migrations.AddField:
		if value == nil {
			return nil, errors.New("canonical operation is a nil *migrations.AddField")
		}
		return appendCanonicalAddField(output, *value)
	default:
		return nil, fmt.Errorf("unsupported canonical operation %T", operation)
	}
}

func appendCanonicalCreateModel(output []byte, operation migrations.CreateModel) ([]byte, error) {
	output = append(output, `{"app_label":`...)
	var err error
	output, err = appendCanonicalString(output, operation.AppLabel)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"kind":"create_model","model":`...)
	output, err = appendCanonicalModel(output, operation.Model)
	if err != nil {
		return nil, err
	}
	return append(output, '}'), nil
}

func appendCanonicalAddField(output []byte, operation migrations.AddField) ([]byte, error) {
	output = append(output, `{"app_label":`...)
	var err error
	output, err = appendCanonicalString(output, operation.AppLabel)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"field":`...)
	output, err = appendCanonicalField(output, operation.Field)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"kind":"add_field","model_name":`...)
	output, err = appendCanonicalString(output, operation.ModelName)
	if err != nil {
		return nil, err
	}
	return append(output, '}'), nil
}

func appendCanonicalModel(output []byte, model ir.Model) ([]byte, error) {
	output = append(output, `{"db_table":`...)
	var err error
	output, err = appendCanonicalString(output, model.DBTable)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"fields":[`...)
	for index, field := range model.Fields {
		if index != 0 {
			output = append(output, ',')
		}
		output, err = appendCanonicalField(output, field)
		if err != nil {
			return nil, err
		}
	}
	output = append(output, `],"go_name":`...)
	output, err = appendCanonicalString(output, model.GoName)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"name":`...)
	output, err = appendCanonicalString(output, model.Name)
	if err != nil {
		return nil, err
	}
	return append(output, '}'), nil
}

func appendCanonicalField(output []byte, field ir.Field) ([]byte, error) {
	output = append(output, `{"column":`...)
	var err error
	output, err = appendCanonicalString(output, field.Column)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"default":`...)
	if field.Default == nil {
		output = append(output, "null"...)
	} else {
		switch field.Default.Kind {
		case ir.ScalarString:
			output = append(output, `{"kind":"string","string":`...)
			output, err = appendCanonicalString(output, field.Default.String)
			if err != nil {
				return nil, err
			}
			output = append(output, '}')
		case ir.ScalarBoolean:
			output = append(output, `{"boolean":`...)
			output = strconv.AppendBool(output, field.Default.Boolean)
			output = append(output, `,"kind":"boolean"}`...)
		default:
			return nil, fmt.Errorf("unsupported canonical default %q", field.Default.Kind)
		}
	}
	output = append(output, `,"go_name":`...)
	output, err = appendCanonicalString(output, field.GoName)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"kind":`...)
	output, err = appendCanonicalString(output, string(field.Kind))
	if err != nil {
		return nil, err
	}
	output = append(output, `,"max_length":`...)
	output = strconv.AppendInt(output, int64(field.MaxLength), 10)
	output = append(output, `,"name":`...)
	output, err = appendCanonicalString(output, field.Name)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"nullable":`...)
	output = strconv.AppendBool(output, field.Nullable)
	output = append(output, `,"primary_key":`...)
	output = strconv.AppendBool(output, field.PrimaryKey)
	return append(output, '}'), nil
}

// appendCanonicalString implements the string subset required by the v1
// canonical document. In particular it does not apply HTML or JavaScript
// escaping to <, >, &, U+2028, or U+2029.
func appendCanonicalString(output []byte, value string) ([]byte, error) {
	if !utf8.ValidString(value) {
		return nil, errors.New("canonical string is not valid UTF-8")
	}
	const hexadecimal = "0123456789abcdef"
	output = append(output, '"')
	for len(value) != 0 {
		current, size := utf8.DecodeRuneInString(value)
		switch current {
		case '"':
			output = append(output, '\\', '"')
		case '\\':
			output = append(output, '\\', '\\')
		case '\b':
			output = append(output, '\\', 'b')
		case '\t':
			output = append(output, '\\', 't')
		case '\n':
			output = append(output, '\\', 'n')
		case '\f':
			output = append(output, '\\', 'f')
		case '\r':
			output = append(output, '\\', 'r')
		default:
			if current < 0x20 {
				output = append(output, '\\', 'u', '0', '0', hexadecimal[byte(current)>>4], hexadecimal[byte(current)&0x0f])
			} else {
				output = append(output, value[:size]...)
			}
		}
		value = value[size:]
	}
	return append(output, '"'), nil
}
