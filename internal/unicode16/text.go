// Package unicode16 implements the pinned Unicode text behavior used by the
// Django reference. The host Go version does not select or replace its tables.
// Callers own input limits. Invalid UTF-8 is preserved for their validation;
// no replacement character or stream-safe CGJ is introduced implicitly.
package unicode16

import (
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

type codeRange struct{ low, high rune }

func contains(ranges []codeRange, value rune) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i].high >= value })
	return i < len(ranges) && ranges[i].low <= value
}

func IsAlphanumeric(value rune) bool { return contains(alphanumeric[:], value) }
func IsDigit(value rune) bool        { return contains(digits[:], value) }
func IsSpace(value rune) bool        { return contains(spaces[:], value) }
func TrimSpace(value string) string  { return strings.TrimFunc(value, IsSpace) }

// CaseFold applies the default full case folding (C/F mappings), not the
// locale-specific Turkic mappings or simple one-rune case conversion.
// Callers that need Django's Unicode-insensitive comparison first apply NFKC.
func CaseFold(value string) string {
	if !utf8.ValidString(value) {
		return value
	}
	var output strings.Builder
	output.Grow(len(value))
	for _, char := range value {
		if folded, found := caseFolding[char]; found {
			output.WriteString(folded)
		} else {
			output.WriteRune(char)
		}
	}
	return output.String()
}

// Lower applies Unicode default full lowercase mapping, including contextual
// final sigma. Locale-specific Turkish, Azeri and Lithuanian rules do not apply.
func Lower(value string) string {
	if !utf8.ValidString(value) {
		return value
	}
	runes := []rune(value)
	var output strings.Builder
	output.Grow(len(value))
	for i, r := range runes {
		if r == '\u03a3' && finalSigma(runes, i) {
			output.WriteRune('\u03c2')
		} else if lower, found := lowercase[r]; found {
			output.WriteString(lower)
		} else {
			output.WriteRune(r)
		}
	}
	return output.String()
}

func finalSigma(value []rune, index int) bool {
	preceded := false
	for i := index - 1; i >= 0; i-- {
		if contains(caseIgnorable[:], value[i]) {
			continue
		}
		preceded = contains(cased[:], value[i])
		break
	}
	if !preceded {
		return false
	}
	for i := index + 1; i < len(value); i++ {
		if contains(caseIgnorable[:], value[i]) {
			continue
		}
		return !contains(cased[:], value[i])
	}
	return true
}

type scalar struct {
	value rune
	class uint8
}

// NFKC performs complete compatibility decomposition, canonical ordering and
// canonical composition. It never applies the separate stream-safe transform.
func NFKC(value string) string {
	if !utf8.ValidString(value) {
		return value
	}
	ascii := true
	for i := range len(value) {
		if value[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return value
	}
	values := make([]scalar, 0, utf8.RuneCountInString(value))
	appendRune := func(r rune) { values = append(values, scalar{r, combiningClass[r]}) }
	for _, r := range value {
		if r >= 0xac00 && r <= 0xd7a3 {
			index := r - 0xac00
			appendRune(0x1100 + index/588)
			appendRune(0x1161 + index%588/28)
			if trailing := index % 28; trailing != 0 {
				appendRune(0x11a7 + trailing)
			}
		} else if parts, found := decomposition[r]; found {
			for _, part := range parts {
				appendRune(part)
			}
		} else {
			appendRune(r)
		}
	}
	order := func(start, end int) {
		if end-start > 1 {
			slices.SortStableFunc(values[start:end], func(a, b scalar) int { return int(a.class) - int(b.class) })
		}
	}
	start := 0
	for i, part := range values {
		if part.class == 0 {
			order(start, i)
			start = i + 1
		}
	}
	order(start, len(values))
	result := values[:0]
	starter, previous := -1, uint8(0)
	for _, part := range values {
		if starter >= 0 && (previous == 0 || previous < part.class) {
			if composed, ok := compose(result[starter].value, part.value); ok {
				result[starter].value = composed
				continue
			}
		}
		if part.class == 0 {
			starter = len(result)
		}
		result = append(result, part)
		previous = part.class
	}
	var output strings.Builder
	output.Grow(len(value))
	for _, part := range result {
		output.WriteRune(part.value)
	}
	return output.String()
}

func compose(first, second rune) (rune, bool) {
	if first >= 0x1100 && first < 0x1100+19 && second >= 0x1161 && second < 0x1161+21 {
		return 0xac00 + ((first-0x1100)*21+second-0x1161)*28, true
	}
	if first >= 0xac00 && first <= 0xd7a3 && (first-0xac00)%28 == 0 && second > 0x11a7 && second < 0x11a7+28 {
		return first + second - 0x11a7, true
	}
	value, found := composition[uint64(first)<<21|uint64(second)]
	return value, found
}
