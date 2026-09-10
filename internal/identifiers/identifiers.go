// Package identifiers owns lexical identifier rules shared across schema,
// generation and publication. Callers retain their own size and scope limits.
package identifiers

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SQL recognizes the canonical database identifiers accepted by Schema IR.
func SQL(value string) bool {
	if value == "" || !(value[0] == '_' || value[0] >= 'a' && value[0] <= 'z') {
		return false
	}
	for index := 1; index < len(value); index++ {
		current := value[index]
		if current != '_' && !(current >= 'a' && current <= 'z') && !(current >= '0' && current <= '9') {
			return false
		}
	}
	return true
}

// ExportedGo recognizes an exported Go identifier. Go keywords cannot start
// with an uppercase letter, so no separate keyword exception is needed.
func ExportedGo(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	first, size := utf8.DecodeRuneInString(value)
	if !unicode.IsUpper(first) {
		return false
	}
	for _, current := range value[size:] {
		if current != '_' && !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			return false
		}
	}
	return true
}

// PortablePathElement excludes ambiguous dot names, Windows device names and
// short-name aliases while allowing the current portable import-path alphabet.
func PortablePathElement(element string) bool {
	if element == "" || strings.Trim(element, ".") == "" || strings.HasSuffix(element, ".") {
		return false
	}
	for _, current := range element {
		if current != '-' && current != '.' && current != '_' && current != '~' && current != '+' &&
			!(current >= '0' && current <= '9') && !(current >= 'A' && current <= 'Z') && !(current >= 'a' && current <= 'z') {
			return false
		}
	}
	short, _, _ := strings.Cut(element, ".")
	upper := strings.ToUpper(short)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" ||
		len(upper) == 4 && upper[3] >= '1' && upper[3] <= '9' && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) {
		return false
	}
	if tilde := strings.LastIndexByte(short, '~'); tilde >= 0 && tilde < len(short)-1 {
		digits := true
		for _, current := range short[tilde+1:] {
			digits = digits && current >= '0' && current <= '9'
		}
		if digits {
			return false
		}
	}
	return true
}

// ImportPath validates syntax independently of a producer's byte/depth budget
// and reserved package aliases.
func ImportPath(value string) bool {
	if value == "" || value == "go" || value == "type" || strings.HasPrefix(value, "-") {
		return false
	}
	for _, element := range strings.Split(value, "/") {
		if !PortablePathElement(element) {
			return false
		}
	}
	return true
}
