package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestBackendRecoveryRequiredErrorCodeIsStableAndClassifiable(t *testing.T) {
	t.Parallel()

	if got, want := query.CodeBackendRecoveryRequired, "backend_recovery_required"; got != want {
		t.Fatalf("CodeBackendRecoveryRequired = %q, want %q", got, want)
	}
	err := &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeBackendRecoveryRequired,
		Detail:   "diagnostic detail is not part of the stable contract",
	}
	if !errors.Is(err, &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeBackendRecoveryRequired,
	}) {
		t.Fatalf("error = %v, want backend_error/backend_recovery_required", err)
	}
}

func TestTypedNilQueryErrorDoesNotPanicDuringInspection(t *testing.T) {
	var absent *query.Error
	actual := &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}
	if errors.Is(actual, absent) || errors.Is(absent, actual) {
		t.Fatal("a typed-nil error matched a populated query error")
	}
	if cause := errors.Unwrap(absent); cause != nil {
		t.Fatalf("typed-nil cause = %v, want nil", cause)
	}
	if message := absent.Error(); message == "" {
		t.Fatal("typed-nil error has no diagnostic text")
	}
	if !errors.Is(errors.Join(absent, actual), &query.Error{Code: query.CodeBackendRecoveryRequired}) {
		t.Fatal("typed-nil sibling hid the populated error in a joined chain")
	}
}
