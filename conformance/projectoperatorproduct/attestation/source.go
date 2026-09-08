package attestation

import (
	"path/filepath"
	"strings"

	"github.com/progresshans/godj/conformance/internal/attestationio"
)

const (
	maxSourceFiles = 8192
	maxSourceBytes = 256 << 20
)

var exactSourcePaths = map[string]struct{}{
	"scripts/conformance.py":                                     {},
	"conformance/suites.json":                                    {},
	"conformance/catalog.py":                                     {},
	"scripts/ci/conformance_tests.py":                            {},
	"scripts/ci/capture_artifact.py":                             {},
	"scripts/ci/go_test_events.py":                               {},
	"scripts/ci/packages.py":                                     {},
	"scripts/ci/scopes.py":                                       {},
	"scripts/ci/python_tests.py":                                 {},
	".github/workflows/ci.yml":                                   {},
	"Makefile":                                                   {},
	"conformance/contracts/system-state-manifest.json":           {},
	"conformance/runners/godj/gdj0045_system_state_scenarios.go": {},
	"conformance/runners/godj/gdj0046_system_state_multi_runtime_scenarios.go": {},
	"conformance/runners/godj/gdj0046_system_state_two_process_execution.go":   {},
	"conformance/runners/godj/inputs.go":                                       {},
	"conformance/runners/godj/runner.go":                                       {},
	"examples/article/godj.toml":                                               {},
	"go.mod":                                                                   {},
	"go.sum":                                                                   {},
}

var productSourcePrefixes = []string{
	"internal/identifiers/",
	"internal/relationpolicy/",
	"internal/irresource/",
	"internal/projectcheck/failurecode/",
	"internal/gobuild/",
	"admin/",
	"api/",
	"apps/",
	"auth/",
	"codegen/",
	"db/",
	"forms/",
	"migrations/",
	"orm/",
	"project/",
	"query/",
	"schema/",
	"serializers/",
	"sessions/",
	"settings/",
	"systemstate/",
	"templates/",
	"validation/",
	"web/",
	"examples/article/",
}

var commandAndInternalSourcePrefixes = []string{
	"cmd/godj/",
	"internal/migrationautodetect/",
	"internal/projectcheck/",
	"internal/projectgenerate/",
	"internal/projectmigration/",
	"internal/projectspec/",
	"internal/projectwire/",
	"internal/wirejson/",
}

var conformanceConsumerSourcePrefixes = []string{
	"conformance/cmd/godjcheck/",
	"conformance/internal/protocol/",
	"conformance/internal/attestationio/",
	"conformance/internal/testprocess/",
}

var embeddedAssetPrefixes = []string{
	"admin/site_templates/",
	"examples/article/webapp/templates/",
}

var migrationDataPrefixes = []string{
	"examples/article/migrations/",
	"examples/article/testdata/postgres/",
	"systemstate/testdata/",
}

const harnessSourcePrefix = "conformance/projectoperatorproduct/"
const captureFixturePrefix = "conformance/projectoperatorproduct/attestation/testdata/"

// ComputeSourceBinding hashes the fixed repository-relative source inventory
// that can affect the SYS-029 external product observation. This includes the
// actual product test harness (including its _test.go files), global godj and
// projectcheck behavior, project/systemstate and Article behavior, their public
// runtime dependencies, the GDJ-0055 consumer/loader/protocol/manifest path,
// dependency locks, Makefile, and hosted workflow. The capture codec fixture
// directory is excluded to avoid a digest self-reference.
func ComputeSourceBinding(repositoryRoot string) (SourceBinding, error) {
	inventory, err := attestationio.BindSource(repositoryRoot, attestationio.SourcePolicy{
		Name:                      "external operator behavioral source",
		MaxFiles:                  maxSourceFiles,
		MaxBytes:                  maxSourceBytes,
		DirectoryExcluded:         sourceDirectoryExcluded,
		PathOwned:                 sourcePathOwned,
		SymlinkMayHideOwnedSource: sourceSymlinkMayHideOwnedSource,
	})
	if err != nil {
		return SourceBinding{}, err
	}
	return newSourceBinding(inventory.FileCount, inventory.PayloadBytes, inventory.SHA256)
}

func sourcePathOwned(path string) bool {
	if _, exact := exactSourcePaths[path]; exact {
		return true
	}
	if strings.HasPrefix(path, captureFixturePrefix) {
		return false
	}
	for _, prefix := range embeddedAssetPrefixes {
		if strings.HasPrefix(path, prefix) && filepath.Ext(path) == ".html" {
			return true
		}
	}
	for _, prefix := range migrationDataPrefixes {
		if strings.HasPrefix(path, prefix) && strings.HasSuffix(path, ".godj.json") {
			return true
		}
	}
	if filepath.Ext(path) != ".go" {
		return false
	}
	if strings.HasPrefix(path, harnessSourcePrefix) {
		// Unlike ordinary product tests, this package is the executable
		// product sentinel itself. Its root tests and production attestation
		// codec are behavioral source. Codec unit tests and synthetic input
		// fixtures are not the live observation that this binding attests.
		if strings.HasPrefix(path, harnessSourcePrefix+"attestation/") && strings.HasSuffix(path, "_test.go") {
			return false
		}
		return !pathComponentExcluded(path)
	}
	for _, prefix := range conformanceConsumerSourcePrefixes {
		if strings.HasPrefix(path, prefix) {
			return !strings.HasSuffix(path, "_test.go") && !pathComponentExcluded(path)
		}
	}
	if strings.HasPrefix(path, "conformance/runners/godj/") {
		return strings.HasPrefix(filepath.Base(path), "gdj0055_") && !pathComponentExcluded(path)
	}
	for _, prefix := range productSourcePrefixes {
		if strings.HasPrefix(path, prefix) {
			return !strings.HasSuffix(path, "_test.go") && !pathComponentExcluded(path)
		}
	}
	for _, prefix := range commandAndInternalSourcePrefixes {
		if strings.HasPrefix(path, prefix) {
			return !strings.HasSuffix(path, "_test.go") && !pathComponentExcluded(path)
		}
	}
	return false
}

func sourceSymlinkMayHideOwnedSource(path string) bool {
	if sourceDirectoryExcluded(path) {
		return false
	}
	directory := strings.TrimSuffix(path, "/") + "/"
	for exact := range exactSourcePaths {
		if strings.HasPrefix(exact, directory) {
			return true
		}
	}
	for _, prefix := range embeddedAssetPrefixes {
		if sourceDirectoryRelated(directory, prefix) {
			return true
		}
	}
	for _, prefix := range migrationDataPrefixes {
		if sourceDirectoryRelated(directory, prefix) {
			return true
		}
	}
	for _, prefix := range productSourcePrefixes {
		if sourceDirectoryRelated(directory, prefix) && !pathComponentExcluded(directory+"hidden.go") {
			return true
		}
	}
	for _, prefix := range commandAndInternalSourcePrefixes {
		if sourceDirectoryRelated(directory, prefix) && !pathComponentExcluded(directory+"hidden.go") {
			return true
		}
	}
	for _, prefix := range conformanceConsumerSourcePrefixes {
		if sourceDirectoryRelated(directory, prefix) && !pathComponentExcluded(directory+"hidden.go") {
			return true
		}
	}
	if sourceDirectoryRelated(directory, "conformance/runners/godj/") && !pathComponentExcluded(directory+"gdj0055_hidden.go") {
		return true
	}
	return sourceDirectoryRelated(directory, harnessSourcePrefix) && !pathComponentExcluded(directory+"hidden.go")
}

func sourceDirectoryRelated(directory, prefix string) bool {
	return strings.HasPrefix(directory, prefix) || strings.HasPrefix(prefix, directory)
}

func sourceDirectoryExcluded(path string) bool {
	if path == ".git" || path == "docs" || path == "work" || path == "vendor" || strings.HasSuffix(path, "/__pycache__") {
		return true
	}
	return path == "conformance/oracles" ||
		path == "conformance/fixtures" ||
		path == strings.TrimSuffix(captureFixturePrefix, "/")
}

func pathComponentExcluded(path string) bool {
	return strings.Contains(path, "/testdata/") ||
		strings.HasPrefix(path, "conformance/oracles/") ||
		strings.HasPrefix(path, "conformance/fixtures/") ||
		strings.HasPrefix(path, captureFixturePrefix)
}
