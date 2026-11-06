//go:build !race

package compiletest

import (
	"slices"
	"testing"
)

func TestCompileEnvironmentIsOfflineAndRetainsPlatform(t *testing.T) {
	input := []string{"GOFLAGS=-overlay=host.json", "GOWORK=auto", "GOTOOLCHAIN=auto", "GOPROXY=direct", "GOPROXY=https://host", "GOSUMDB=host", "GOPRIVATE=private.invalid", "GONOPROXY=*", "CGO_ENABLED=0", "GOARCH=arm64", "GOMODCACHE=/module-cache", "GOCACHE=/build-cache"}
	before := slices.Clone(input)
	want := []string{"GOPRIVATE=private.invalid", "CGO_ENABLED=0", "GOARCH=arm64", "GOMODCACHE=/module-cache", "GOCACHE=/build-cache", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GONOPROXY=none"}
	want = append(want, "GOENV=off", "GOCACHEPROG=")
	slices.Sort(want)
	got := compileEnvironment(input)
	if !slices.Equal(got, want) {
		t.Fatalf("environment = %q, want %q", got, want)
	}
	got[0] = "changed"
	if !slices.Equal(input, before) {
		t.Fatal("environment construction changed caller settings")
	}
}
