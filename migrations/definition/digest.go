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

	output := []byte(`{"definitions":[`)
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
	return append(output, `,"format_version":1}`...), nil
}

func appendCanonicalOperation(output []byte, operation migrations.Operation) ([]byte, error) {
	switch value := operation.(type) {
	case migrations.CreateModel:
		return appendCanonicalCreateModel(output, value)
	case migrations.AddField:
		return appendCanonicalAddField(output, value)
	case migrations.AlterField:
		return appendCanonicalAlterField(output, value)
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
	if operation.BeforeField != "" {
		output = append(output, `,"before_field":`...)
		output, err = appendCanonicalString(output, operation.BeforeField)
		if err != nil {
			return nil, err
		}
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
	if len(model.UniqueConstraints) != 0 {
		output = append(output, `,"unique_constraints":[`...)
		for index, constraint := range model.UniqueConstraints {
			if index != 0 {
				output = append(output, ',')
			}
			output, err = appendCanonicalUniqueConstraint(output, constraint)
			if err != nil {
				return nil, err
			}
		}
		output = append(output, ']')
	}
	return append(output, '}'), nil
}

func appendCanonicalScalar(output []byte, value ir.Scalar) ([]byte, error) {
	var err error
	switch value.Kind {
	case ir.ScalarJSON:
		output = append(output, `{"json":`...)
		output, err = appendCanonicalString(output, value.JSON)
		output = append(output, `,"kind":"json"}`...)
	case ir.ScalarUUID:
		output = append(output, `{"kind":"uuid","uuid":`...)
		output, err = appendCanonicalString(output, value.UUID)
		output = append(output, '}')
	case ir.ScalarDecimal:
		output = append(output, `{"decimal":`...)
		output, err = appendCanonicalString(output, value.Decimal)
		output = append(output, `,"kind":"decimal"}`...)
	case ir.ScalarFloat:
		output = append(output, `{"kind":"float","float_bits":`...)
		output, err = appendCanonicalString(output, value.FloatBits)
		output = append(output, '}')
	case ir.ScalarDuration:
		output = append(output, `{"kind":"duration","duration":`...)
		output, err = appendCanonicalString(output, value.Duration)
		output = append(output, '}')
	case ir.ScalarTime:
		output = append(output, `{"kind":"time","time":`...)
		output, err = appendCanonicalString(output, value.Time)
		output = append(output, '}')
	case ir.ScalarDate:
		output = append(output, `{"date":`...)
		output, err = appendCanonicalString(output, value.Date)
		output = append(output, `,"kind":"date"}`...)
	case ir.ScalarDateTime:
		output = append(output, `{"datetime":`...)
		output, err = appendCanonicalString(output, value.DateTime)
		output = append(output, `,"kind":"datetime"}`...)
	case ir.ScalarString:
		output = append(output, `{"kind":"string","string":`...)
		output, err = appendCanonicalString(output, value.String)
		output = append(output, '}')
	case ir.ScalarBoolean:
		output = append(output, `{"boolean":`...)
		output = strconv.AppendBool(output, value.Boolean)
		output = append(output, `,"kind":"boolean"}`...)
	case ir.ScalarInteger:
		output = append(output, `{"integer":`...)
		output = strconv.AppendInt(output, value.Integer, 10)
		output = append(output, `,"kind":"integer"}`...)
	default:
		return nil, fmt.Errorf("unsupported canonical scalar %q", value.Kind)
	}
	if err != nil {
		return nil, err
	}
	return output, nil
}

func appendCanonicalField(output []byte, field ir.Field) ([]byte, error) {
	var err error
	output = append(output, '{')
	if len(field.Choices) > 0 {
		output = append(output, `"choices":[`...)
		for index, choice := range field.Choices {
			if index > 0 {
				output = append(output, ',')
			}
			output = append(output, `{"label":`...)
			output, err = appendCanonicalString(output, choice.Label)
			if err != nil {
				return nil, err
			}
			output = append(output, `,"value":`...)
			output, err = appendCanonicalScalar(output, choice.Value)
			if err != nil {
				return nil, err
			}
			output = append(output, '}')
		}
		output = append(output, `],`...)
	}
	output = append(output, `"column":`...)
	output, err = appendCanonicalString(output, field.Column)
	if err != nil {
		return nil, err
	}
	if field.Decimal != nil {
		output = append(output, `,"decimal":{"decimal_places":`...)
		output = strconv.AppendInt(output, int64(field.Decimal.DecimalPlaces), 10)
		output = append(output, `,"max_digits":`...)
		output = strconv.AppendInt(output, int64(field.Decimal.MaxDigits), 10)
		output = append(output, '}')
	}
	output = append(output, `,"default":`...)
	if field.Default == nil {
		output = append(output, "null"...)
	} else {
		output, err = appendCanonicalScalar(output, *field.Default)
		if err != nil {
			return nil, err
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
	if field.Relation != nil {
		output = append(output, `,"relation":{"cardinality":`...)
		output, err = appendCanonicalString(output, string(field.Relation.Cardinality))
		if err != nil {
			return nil, err
		}
		output = append(output, `,"on_delete":`...)
		output, err = appendCanonicalString(output, string(field.Relation.OnDelete))
		if err != nil {
			return nil, err
		}
		output = append(output, `,"reverse":{"disabled":`...)
		output = strconv.AppendBool(output, field.Relation.Reverse.Disabled)
		output = append(output, `,"name":`...)
		output, err = appendCanonicalString(output, field.Relation.Reverse.Name)
		if err != nil {
			return nil, err
		}
		output = append(output, `},"target":{"app_label":`...)
		output, err = appendCanonicalString(output, field.Relation.Target.AppLabel)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"model_name":`...)
		output, err = appendCanonicalString(output, field.Relation.Target.ModelName)
		if err != nil {
			return nil, err
		}
		output = append(output, `}}`...)
	}
	if field.Unique {
		output = append(output, `,"unique":true`...)
	}

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
