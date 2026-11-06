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

func TestRejectionOriginPreservesCauseFreeDirectDiagnostics(t *testing.T) {
	origin, other := new(RejectionOrigin), new(RejectionOrigin)
	diagnostics := NewErrors(New("password", "policy"))
	marked := origin.Reject(diagnostics)
	if errors.Unwrap(marked) != nil {
		t.Fatal("diagnostic provenance is an execution cause")
	}
	if got, ok := Rejected(marked); !ok || got.Len() != 1 {
		t.Fatal("ordinary input handling lost marked diagnostics")
	}
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"same", marked, true},
		{"other", other.Reject(diagnostics), false},
		{"unmarked", Reject(diagnostics, nil), false},
		{"private_cause", Reject(diagnostics, errors.New("private")), false},
		{"wrapped", fmt.Errorf("owner: %w", marked), false},
		{"joined", errors.Join(marked), false},
		{"cleanup", errors.Join(marked, errors.New("cleanup")), false},
		{"nil", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := origin.Diagnostics(test.err)
			if ok != test.want || got.Empty() == test.want {
				t.Fatal("origin crossed a producer or execution boundary")
			}
		})
	}
	var zero *RejectionOrigin
	if _, ok := zero.Diagnostics(marked); ok || zero.Reject(diagnostics) == nil || origin.Reject(Errors{}) != nil {
		t.Fatal("nil origin or empty diagnostics changed input semantics")
	}
	got, _ := origin.Diagnostics(marked)
	copy := got.All()
	copy[0] = New("other", "other")
	if first, _ := diagnostics.At(0); first.Field() != "password" {
		t.Fatal("origin returned mutable diagnostics")
	}
}
