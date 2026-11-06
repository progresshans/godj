package model

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// DatabaseCheck reads selected scalar candidates and reports input violations
// separately from execution errors. It must not mutate storage or retain the
// borrowed reader, context or candidate map after returning.
type DatabaseCheck func(context.Context, map[string]query.Value) (validation.Errors, error)

// DatabaseChecks belongs to one caller-authorized read snapshot. Both checks
// are required, even when the model has no matching constraints. A callback
// may return empty diagnostics without I/O. ORM Manager.ValidateUniqueFields
// and ValidateUniqueConstraints can be bound to the same reader/current row.
type DatabaseChecks struct {
	UniqueFields DatabaseCheck
	Constraints  DatabaseCheck
}

// CheckDatabase checks unique fields, applies their errors to a private form
// copy, then recomputes exclusions before checking model constraints. It also
// runs on invalid forms: unrelated valid values still need their DB checks.
// Only newly discovered diagnostics are returned, so callers can apply them
// once with WithErrors. A failed/canceled check publishes no partial result.
// The caller must await successful read-snapshot cleanup before publishing
// these diagnostics. No successful check grants authority to persist a row.
func (bound BoundForm) CheckDatabase(ctx context.Context, checks DatabaseChecks) (validation.Errors, error) {
	if ctx == nil {
		return validation.Errors{}, &Error{Path: "context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return validation.Errors{}, &databaseValidationError{cause: err}
	}
	if checks.UniqueFields == nil || checks.Constraints == nil {
		return validation.Errors{}, &Error{Path: "database_checks", Code: "incomplete"}
	}
	var results []validation.Errors
	for _, check := range []DatabaseCheck{checks.UniqueFields, checks.Constraints} {
		values, err := bound.ValidationValues()
		if err != nil {
			return validation.Errors{}, err
		}
		failures, err := check(ctx, values)
		if err = errors.Join(err, ctx.Err()); err != nil {
			// An error is an execution failure, even if a callback accidentally
			// returns a renderable rejection instead of separate diagnostics.
			return validation.Errors{}, &databaseValidationError{cause: err}
		}
		bound, err = bound.WithErrors(failures)
		if err != nil {
			return validation.Errors{}, err
		}
		results = append(results, failures)
	}
	return validation.Join(results...), nil
}

type databaseValidationError struct{ cause error }

func (*databaseValidationError) Error() string         { return "forms/model: database validation failed" }
func (failure *databaseValidationError) Unwrap() error { return failure.cause }
func (failure *databaseValidationError) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, failure.Error())
}
