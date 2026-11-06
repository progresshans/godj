package parameters

import (
	"net/http"
	"strings"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/validation"
)

// Header owns one scalar field, its presence/byte policy and its OpenAPI
// parameter. It may be shared concurrently. Parse borrows the header map only
// for the call; it never changes it or retains map/slice storage in a result.
type Header[T any] struct {
	parameter openapi.Parameter
	decode    func(string, bool) (T, string)
	maxBytes  int
}

// NewHeader prepares one application-owned field. maximumBytes is a value
// budget of 1..1 MiB, not a limit on unrelated transport/authentication fields.
// Header names are case-insensitive; presence and scalar rules use the same
// closed Input as query parameters. Authentication/representation names and
// Host, Transfer-Encoding and Trailer are reserved: the HTTP server owns
// their request fields. The final operation also rejects its profile's CSRF header.
func NewHeader[T any](name string, input Input[T], maximumBytes int, description string) (Header[T], error) {
	if input.err != nil {
		return Header[T]{}, configError("header input is invalid", input.err)
	}
	if input.decode == nil || input.headerSchema == nil || maximumBytes < 1 || maximumBytes > 1<<20 {
		return Header[T]{}, configError("header input or byte budget is zero or invalid", nil)
	}
	schema, err := input.headerSchema(maximumBytes)
	if err != nil {
		return Header[T]{}, configError("header scalar schema is invalid", err)
	}
	parameter := openapi.Parameter{Name: name, In: "header", Schema: schema, Required: input.required, Description: description}
	if err := openapi.ValidateParameter(parameter); err != nil {
		return Header[T]{}, configError("header parameter is invalid", err)
	}
	return Header[T]{parameter: parameter, decode: input.decode, maxBytes: maximumBytes}, nil
}

func (header Header[T]) MaxBytes() int { return header.maxBytes }

// Parameter returns detached metadata containing an immutable schema.
func (header Header[T]) Parameter() openapi.Parameter { return header.parameter }

// Parse accepts exactly one matching map entry containing exactly one value.
// Case aliases and repeated values are invalid even when their text agrees.
// It never joins/splits comma lists, trims or URL-decodes field text. Unknown
// fields belong to other declarations/transport and are ignored. The HTTP
// server owns normalization before this call; this parser sees its field value.
// Missing required fields return required; all other client errors are invalid.
// A non-nil Go error means an unprepared Header. Failure returns a zero T.
func (header Header[T]) Parse(fields http.Header) (T, validation.Errors, error) {
	var zero T
	if header.decode == nil || header.maxBytes < 1 {
		return zero, validation.Errors{}, configError("header is zero or unprepared", nil)
	}
	invalid := func(code string) (T, validation.Errors, error) {
		return zero, validation.NewErrors(validation.New(validation.Field(header.parameter.Name), validation.Code(code))), nil
	}
	present := false
	raw := ""
	for name, values := range fields {
		// Declared names are ASCII HTTP tokens. Equal byte lengths prevent
		// Unicode case folding from creating aliases for an ASCII field name.
		if len(name) != len(header.parameter.Name) || !strings.EqualFold(name, header.parameter.Name) {
			continue
		}
		if present || len(values) != 1 {
			return invalid("invalid")
		}
		present = true
		raw = values[0]
	}
	if len(raw) > header.maxBytes {
		return invalid("invalid")
	}
	for index := 0; index < len(raw); index++ {
		value := raw[index]
		if value != '\t' && (value < 0x20 || value == 0x7f) {
			return invalid("invalid")
		}
	}
	value, code := header.decode(raw, present)
	if code != "" {
		return invalid(code)
	}
	return value, validation.Errors{}, nil
}
