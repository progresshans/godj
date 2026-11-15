package model

import (
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// Text representation and storage length come from IR. Reading stored values
// must not normalize them or run input-only required, choice or host policies.
func checkModelText(field ir.Field, value query.Value) error {
	text, textual := value.String()
	if !textual {
		return nil
	}
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return profileInputError(field.Name, "invalid")
	}
	if field.MaxLength > 0 && utf8.RuneCountInString(text) > field.MaxLength {
		return profileInputError(field.Name, "max_length")
	}
	return nil
}
