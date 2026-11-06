package projectgenerate

import (
	"context"
	"errors"
	"testing"
)

func TestCandidateVerifyFuncRejectsNilFunction(t *testing.T) {
	var verify CandidateVerifyFunc
	if err := verify.Verify(context.Background(), t.TempDir()); !errors.Is(err, ErrCandidateVerification) {
		t.Fatalf("nil verifier error = %v, want %v", err, ErrCandidateVerification)
	}
}

func TestCheckReportClean(t *testing.T) {
	if !(CheckReport{}).Clean() {
		t.Fatal("zero report must be clean")
	}
	if (CheckReport{Drifts: []Drift{{Path: "generated.go", Kind: DriftModified}}}).Clean() {
		t.Fatal("drift report must not be clean")
	}
	if (CheckReport{Interrupted: true}).Clean() {
		t.Fatal("interrupted report must not be clean")
	}
}
