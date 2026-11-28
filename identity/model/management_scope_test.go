package model

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func TestManagementProtectionRequiresConfirmedRollback(t *testing.T) {
	protected, err := query.NewProtectedForeignKeyError(2)
	if err != nil {
		t.Fatal(err)
	}
	other, err := query.NewProtectedForeignKeyError(2)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := fmt.Errorf("collector: %w", protected)
	joined := errors.Join(protected)
	invalid := &query.ProtectedForeignKeyError{}
	var nilProtected *query.ProtectedForeignKeyError
	classification := protected.Unwrap()
	for _, test := range []struct {
		name              string
		completion, cause error
		want              ErrorCode
	}{
		{"confirmed", protected, protected, ""},
		{"backend_only", protected, nil, CodePersistence},
		{"different_refusal", other, protected, CodePersistence},
		{"wrapped_collector", wrapped, wrapped, CodePersistence},
		{"joined_collector", joined, joined, CodePersistence},
		{"rollback_failure", errors.Join(protected, errors.New("rollback")), protected, CodePersistence},
		{"canceled", errors.Join(protected, context.Canceled), protected, CodePersistence},
		{"invalid_refusal", invalid, invalid, CodePersistence},
		{"typed_nil", nilProtected, nilProtected, CodePersistence},
		{"query_classification_only", classification, classification, CodePersistence},
		{"commit_unknown", errors.Join(protected, &query.Error{Code: query.CodeCommitOutcomeUnknown}), protected, CodeOutcomeUnknown},
		{"transaction_unknown", errors.Join(protected, &query.Error{Code: query.CodeTransactionOutcomeUnknown}), protected, CodeOutcomeUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, failure := managementCompletion(42, test.completion, test.cause)
			if value != 0 {
				t.Fatal("failed mutation published a value")
			}
			diagnostics, rejected := validation.Rejected(failure)
			if test.want != "" {
				if rejected || !errors.Is(failure, &Error{Code: test.want}) || !errors.Is(failure, test.completion) {
					t.Fatal("execution failure lost its classification or cause", failure)
				}
				return
			}
			items := diagnostics.All()
			if !rejected || len(items) != 1 || items[0].Field() != validation.NonField || items[0].Code() != "protected" || len(items[0].Params()) != 0 {
				t.Fatal("confirmed protection lost its value-free input diagnostic", failure)
			}
			var cause *query.ProtectedForeignKeyError
			if !errors.As(failure, &cause) || cause != protected || cause.ProtectedSourceRows() != 2 {
				t.Fatal("confirmed protection lost its structured cause", failure)
			}
		})
	}
}
