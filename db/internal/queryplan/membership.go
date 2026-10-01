package queryplan

import "github.com/progresshans/godj/query"

// MembershipValues separates SQL parameters from the NULL membership needed
// by nullable negation. Condition.Values supplies an owned copy, so compacting
// the slice cannot change the AST or a caller's list.
func MembershipValues(condition query.Condition) ([]query.Value, bool, error) {
	values, ok := condition.Values()
	if !ok {
		return nil, false, invalidPlan("IN requires a valid scalar list-backed condition")
	}
	filtered := values[:0]
	hasNull := false
	for _, value := range values {
		if value.IsNull() {
			hasNull = true
		} else {
			filtered = append(filtered, value)
		}
	}
	return filtered, hasNull, nil
}
