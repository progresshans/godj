// Package emailinput shares the ordered email form diagnostics used by model
// forms and account services. Transport limits and empty-value policies remain
// explicit at each caller; grammar is owned by validation.ValidEmail.
package emailinput

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/validation"
)

// FormErrors checks nonempty, already-normalized text in Django form order.
func FormErrors(name, value string, maximum int) validation.Errors {
	if value == "" {
		return validation.Errors{}
	}
	field := validation.Field(name)
	var failures []validation.Violation
	if !validation.ValidEmail(value) {
		failures = append(failures, validation.New(field, "invalid"))
	}
	if length := utf8.RuneCountInString(value); maximum > 0 && length > maximum {
		failures = append(failures, validation.New(field, "max_length", validation.NewParam("limit_value", strconv.Itoa(maximum)), validation.NewParam("show_value", strconv.Itoa(length))))
	}
	if strings.ContainsRune(value, 0) {
		failures = append(failures, validation.New(field, "null_characters_not_allowed"))
	}
	return validation.NewErrors(failures...)
}
