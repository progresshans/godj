package validation

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRejectionPreservesDiagnosticsAndCauseWithoutDisplayingIt(t *testing.T) {
	cause := errors.New("private database value")
	diagnostics := NewErrors(New("reference", CodeUnique))
	err := Reject(diagnostics, cause)
	got, rejected := Rejected(err)
	if !rejected || got.Len() != 1 || got.All()[0].Field() != "reference" || got.All()[0].Code() != CodeUnique || !errors.Is(err, cause) || strings.Contains(err.Error(), cause.Error()) {
		t.Fatal("invalid input rejection", got.All(), err)
	}
	copy := got.All()
	copy[0] = New("other", "other")
	if diagnostics.All()[0].Field() != "reference" || got.All()[0].Field() != "reference" {
		t.Fatal("diagnostics share caller-owned mutation")
	}
	if Reject(Errors{}, nil) != nil || Reject(Errors{}, cause) != cause {
		t.Fatal("empty rejection hid an execution error")
	}
}

func TestRejectedDoesNotHideAdditionalErrorOwnersOrRollbackFailures(t *testing.T) {
	rejected := Reject(NewErrors(New(NonField, CodeUnique)), nil)
	rollback := errors.New("rollback failed")
	for _, err := range []error{nil, rollback, fmt.Errorf("reconciliation required: %w", rejected), errors.Join(rejected, rollback), errors.Join(rollback, rejected), errors.Join(rejected)} {
		if diagnostics, ok := Rejected(err); ok || !diagnostics.Empty() {
			t.Fatal("execution error was reduced to input diagnostics", err)
		}
	}
}
