// Package querytest constructs valid query fixtures. Tests of construction
// failures call the public error-returning constructors directly.
package querytest

import (
	"testing"

	"github.com/progresshans/godj/query"
)

func Conditions(t testing.TB, plan query.Plan, conditions ...query.Condition) query.Plan {
	t.Helper()
	result, err := plan.WithConditions(conditions...)
	if err != nil {
		t.Fatalf("construct query fixture: %v", err)
	}
	return result
}
