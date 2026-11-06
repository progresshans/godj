package testenv_test

import (
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/progresshans/godj/internal/testenv"
)

func TestEnvironmentSnapshotsUseLastValueAndApplyExactRemovals(t *testing.T) {
	input := []string{"TOKEN=first", "KEPT=a=b", "TOKEN=last", "malformed", "=invalid"}
	before := slices.Clone(input)
	overrides := map[string]string{"TOKEN": "override"}
	result := testenv.With(input, overrides)
	overrides["TOKEN"] = "mutated"
	if !slices.Equal(result, []string{"KEPT=a=b", "TOKEN=override"}) || !slices.Equal(input, before) {
		t.Fatalf("detached overlay = %v, input %v", result, input)
	}
	values := testenv.Map(input)
	if values["TOKEN"] != "last" || len(values) != 2 {
		t.Fatalf("canonical values = %v", values)
	}
	if got := testenv.Without(input, "TOKEN"); !slices.Equal(got, []string{"KEPT=a=b"}) {
		t.Fatalf("exact removal = %v", got)
	}
	values["KEPT"] = "changed"
	if result[0] != "KEPT=a=b" {
		t.Fatal("returned map changed an encoded environment")
	}
}

func TestEnvironmentUsesPlatformCaseRulesBeforeSorting(t *testing.T) {
	input := []string{"Mode=first", "MODE=last"}
	values := testenv.Map(input)
	updated := testenv.With(input, map[string]string{"mode": "override"})
	removed := testenv.Without(input, "mode")
	if runtime.GOOS == "windows" {
		if len(values) != 1 || values["MODE"] != "last" || !slices.Equal(updated, []string{"mode=override"}) || len(removed) != 0 {
			t.Fatalf("Windows case-insensitive environment = %v, %v, %v", values, updated, removed)
		}
	} else if len(values) != 2 || len(updated) != 3 || len(removed) != 2 {
		t.Fatalf("case-sensitive environment lost a distinct key: %v, %v, %v", values, updated, removed)
	}
}

func TestOfflineGoRetainsExecutionModeAndClosesHostOverrides(t *testing.T) {
	input := []string{"GOARCH=386", "CGO_ENABLED=0", "GOCACHE=/cache", "GOMODCACHE=/modules", "GOENV=host", "GOCACHEPROG=host", "GOFLAGS=-overlay=host.json", "GOPROXY=direct", "GONOPROXY=*"}
	got := testenv.Map(testenv.OfflineGo(input, "-race"))
	want := map[string]string{
		"GOARCH": "386", "CGO_ENABLED": "0", "GOCACHE": "/cache", "GOMODCACHE": "/modules",
		"GOENV": "off", "GOCACHEPROG": "", "GOFLAGS": "-race", "GOWORK": "off", "GOTOOLCHAIN": "local",
		"GOPROXY": "off", "GOSUMDB": "off", "GONOPROXY": "none",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("offline profile = %v, want %v", got, want)
	}
}
