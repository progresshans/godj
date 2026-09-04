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
