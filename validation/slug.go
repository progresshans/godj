package validation

import (
	"unicode/utf8"

	"github.com/progresshans/godj/internal/unicode16"
)

// ValidSlug implements the pinned Django 6.1 slug grammar. ASCII letters,
// digits, underscore and hyphen are accepted; allowUnicode replaces letters
// and digits with Python's Unicode 16 alphanumeric set. It never normalizes,
// trims, lowercases or generates a value, and accepts no empty string.
func ValidSlug(value string, allowUnicode bool) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char == '-' || char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || allowUnicode && unicode16.IsAlphanumeric(char) {
			continue
		}
		return false
	}
	return true
}
