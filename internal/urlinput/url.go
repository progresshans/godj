// Package urlinput owns URL form normalization and ordered diagnostics.
// Model and serializer storage remain the original explicitly chosen string.
package urlinput

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/validation"
)

// Normalize fills in an assumed scheme after the caller's whitespace policy.
// Like pinned Django 6.1, an explicit colon with an ASCII-letter prefix is
// treated as an existing scheme; a bare host:port is not silently repaired.
func Normalize(value, scheme string) string {
	if value == "" {
		return value
	}
	prefix, _, found := strings.Cut(value, ":")
	if !found || prefix == "" || !(prefix[0] >= 'a' && prefix[0] <= 'z' || prefix[0] >= 'A' && prefix[0] <= 'Z') || strings.ContainsRune(prefix, '/') {
		if strings.HasPrefix(value, "//") {
			return scheme + ":" + value
		}
		return scheme + "://" + value
	}
	return value
}

func FormErrors(name, value string, maximum int) validation.Errors {
	if value == "" {
		return validation.Errors{}
	}
	field := validation.Field(name)
	var failures []validation.Violation
	if !validation.ValidURL(value) {
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
