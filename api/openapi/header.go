package openapi

import (
	"strconv"

	"github.com/progresshans/godj/serializers"
)

const maximumHeaderValueBytes = 1 << 20

// HeaderInteger describes one bounded, untrimmed header value. The integer
// grammar and byte budget apply before conversion; they do not authorize a
// client to combine repeated fields or comma-separated values.
func HeaderInteger(minimum, maximum int64, grammar IntegerTextGrammar, maximumBytes int, fallback *int64) (Schema, error) {
	if maximumBytes < 1 || maximumBytes > maximumHeaderValueBytes {
		return Schema{}, schemaConfigError("header.bytes", "header value budget must be between 1 and 1 MiB")
	}
	if fallback != nil && len(strconv.FormatInt(*fallback, 10)) > maximumBytes {
		return Schema{}, schemaConfigError("header.default", "integer default exceeds the header value budget")
	}
	schema, err := integerTextSchema(minimum, maximum, grammar, fallback, "header")
	if err != nil {
		return Schema{}, err
	}
	return schemaAnnotate(schema, serializers.MemberOf("x-godj-max-bytes", serializers.Integer(int64(maximumBytes))))
}

// HeaderString describes untrimmed UTF-8 field text. HTAB and ordinary spaces
// remain values; all other ASCII controls and DEL are rejected. No URL or
// comma-list decoding is implied. Defaults obey the same text and byte policy.
func HeaderString(maximumBytes int, allowEmpty bool, fallback *string) (Schema, error) {
	if maximumBytes < 1 || maximumBytes > maximumHeaderValueBytes {
		return Schema{}, schemaConfigError("header.bytes", "header value budget must be between 1 and 1 MiB")
	}
	if fallback != nil {
		for index := 0; index < len(*fallback); index++ {
			value := (*fallback)[index]
			if value != '\t' && (value < 0x20 || value == 0x7f) {
				return Schema{}, schemaConfigError("header.default", "string default contains forbidden header controls")
			}
		}
	}
	schema, err := stringTextSchema(maximumBytes, allowEmpty, fallback, "header")
	if err != nil {
		return Schema{}, err
	}
	// The final assertion excludes ECMAScript end-before-newline matching.
	return schemaAnnotate(schema, serializers.MemberOf("pattern", serializers.String("^[^\\x00-\\x08\\x0a-\\x1f\\x7f]*$(?![\\s\\S])")))
}
