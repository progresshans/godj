package openapi

import (
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/serializers"
)

// QueryIntegerGrammar describes URL text before integer conversion. It is
// metadata, not a JSON Schema number constraint; the query parser enforces it.
type QueryIntegerGrammar string

const (
	CanonicalDecimal QueryIntegerGrammar = "canonical-decimal"
	UnsignedDigits   QueryIntegerGrammar = "unsigned-digits"
)

// QueryInteger describes a bounded int64 query value and its lexical policy.
// A non-nil default is copied and must be inside the accepted input domain.
func QueryInteger(minimum, maximum int64, grammar QueryIntegerGrammar, fallback *int64) (Schema, error) {
	if grammar != CanonicalDecimal && grammar != UnsignedDigits || grammar == UnsignedDigits && minimum < 0 {
		return Schema{}, schemaConfigError("query.integer", "integer grammar or unsigned range is invalid")
	}
	base, err := IntegerRange(minimum, maximum)
	if err != nil {
		return Schema{}, err
	}
	annotations := []serializers.Member{serializers.MemberOf("x-godj-query-integer", serializers.String(string(grammar)))}
	if fallback != nil {
		if *fallback < minimum || *fallback > maximum {
			return Schema{}, schemaConfigError("query.default", "integer default is outside the input range")
		}
		annotations = append(annotations, serializers.MemberOf("default", serializers.Integer(*fallback)))
	}
	return schemaAnnotate(base, annotations...)
}

// QueryString describes untrimmed UTF-8 query text without NUL. maxLength is a
// necessary code-point bound; x-godj-max-bytes states the stricter byte policy.
// A non-nil default is validated and copied. Empty input and omission differ.
func QueryString(maximumBytes int, allowEmpty bool, fallback *string) (Schema, error) {
	if maximumBytes < 1 {
		return Schema{}, schemaConfigError("query.string", "maximum string bytes must be positive")
	}
	annotations := []serializers.Member{
		serializers.MemberOf("maxLength", serializers.Integer(int64(maximumBytes))),
		serializers.MemberOf("x-godj-max-bytes", serializers.Integer(int64(maximumBytes))),
		serializers.MemberOf("x-godj-no-nul", serializers.Boolean(true)),
	}
	if !allowEmpty {
		annotations = append(annotations, serializers.MemberOf("minLength", serializers.Integer(1)))
	}
	if fallback != nil {
		value := *fallback
		if len(value) > maximumBytes || !allowEmpty && value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return Schema{}, schemaConfigError("query.default", "string default is outside the input domain")
		}
		annotations = append(annotations, serializers.MemberOf("default", serializers.String(value)))
	}
	return schemaAnnotate(String(), annotations...)
}

// ValidateParameter checks a standalone declaration using New's parameter and
// schema rules. Duplicate parameters and authentication header ownership are
// properties of the enclosing operation and are checked by New.
func ValidateParameter(parameter Parameter, components ...NamedSchema) error {
	if err := validateParameterDeclaration(parameter); err != nil {
		return err
	}
	return ValidateSchema(parameter.Schema, components...)
}

func validateParameterDeclaration(parameter Parameter) error {
	if parameter.In != "query" && parameter.In != "header" || !validToken(parameter.Name) || !validText(parameter.Description, 4096, false) {
		return documentError("operation.parameter", "only named query/header parameters with bounded descriptions are supported")
	}
	if parameter.AllowEmptyValue && parameter.In != "query" {
		return documentError("operation.parameter", "empty values may only be enabled for query parameters")
	}
	return nil
}
