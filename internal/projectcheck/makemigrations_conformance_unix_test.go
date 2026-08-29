//go:build darwin || linux

package projectcheck

import (
	"reflect"
	"testing"
)

func TestRunMakemigrationsConformanceFaultRejectsUnknownSelectorBeforeIO(t *testing.T) {
	report, err := RunMakemigrationsConformanceFault(MakemigrationsInvocation{}, MakemigrationsConformanceFault(255))
	if err == nil {
		t.Fatal("unknown makemigrations conformance fault was accepted")
	}
	if !reflect.DeepEqual(report, MakemigrationsReport{}) {
		t.Fatalf("invalid selector report = %+v, want zero", report)
	}
}
