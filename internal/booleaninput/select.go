// Package booleaninput owns the three-state HTML Boolean submission grammar.
package booleaninput

// NullableSelect follows the pinned Django NullBooleanSelect widget. The
// second result distinguishes a Boolean from the unknown/null selection.
// Direct field cleaning has a different grammar; whitespace is not trimmed.
func NullableSelect(raw string) (bool, bool) {
	switch raw {
	case "true", "True", "2":
		return true, true
	case "false", "False", "3":
		return false, true
	default:
		return false, false
	}
}
