package attestation

import (
	"path/filepath"
	"strings"

	"github.com/progresshans/godj/conformance/internal/attestationio"
)

const (
	maxSourceFiles = 4096
	maxSourceBytes = 128 << 20
)

var exactSourcePaths = map[string]struct{}{
	"scripts/conformance.py":                                    {},
	"conformance/suites.json":                                   {},
	"conformance/catalog.py":                                    {},
	"scripts/ci/conformance_tests.py":                           {},
	"scripts/ci/capture_artifact.py":                            {},
	"scripts/ci/go_test_events.py":                              {},
	"scripts/ci/packages.py":                                    {},
	"scripts/ci/scopes.py":                                      {},
	"scripts/ci/python_tests.py":                                {},
	"scripts/ci/relation-required.txt":                          {},
	"scripts/ci/compile-required.txt":                           {},
	".github/workflows/ci.yml":                                  {},
	"Makefile":                                                  {},
	"admin/site_templates/delete.html":                          {},
	"admin/site_templates/form.html":                            {},
	"admin/site_templates/history.html":                         {},
	"admin/site_templates/index.html":                           {},
	"admin/site_templates/list.html":                            {},
	"admin/site_templates/login.html":                           {},
	"admin/site_templates/logout.html":                          {},
	"conformance/contracts/system-state-manifest.json":          {},
	"examples/article/testdata/postgres/0001_initial.godj.json": {},
	"examples/article/webapp/templates/article_list.html":       {},
	"go.mod": {},
	"go.sum": {},
	"systemstate/testdata/0001_initial.godj.json": {},
}

var productSourcePrefixes = []string{
	"internal/identifiers/",
	"internal/relationpolicy/",
	"internal/irresource/",
	"internal/projectcheck/failurecode/",
	"internal/gobuild/",
	"internal/projectwire/",
	"internal/wirejson/",
	"admin/",
	"api/",
	"apps/",
	"auth/",
	"db/",
	"examples/article/",
	"forms/",
	"migrations/",
	"orm/",
	"query/",
	"schema/",
	"serializers/",
	"sessions/",
	"settings/",
	"systemstate/",
	"templates/",
	"validation/",
	"web/",
}

var embeddedAssetPrefixes = []string{
	"admin/site_templates/",
	"examples/article/webapp/templates/",
}

var conformanceSourcePrefixes = []string{
	"internal/testenv/",
	"conformance/cmd/godjcheck/",
	"conformance/internal/protocol/",
	"conformance/internal/attestationio/",
	"conformance/internal/testprocess/",
	"conformance/internal/testfixture/",
	"conformance/internal/relationstate/",
	"conformance/runners/godj/",
	"conformance/systemstate/",
}

// ComputeSourceBinding hashes a fixed repository-relative behavioral source
// inventory. The inventory excludes documentation, reference oracles, fixtures,
// and capture codec fixtures, so those files cannot create a self-reference.
func ComputeSourceBinding(repositoryRoot string) (SourceBinding, error) {
	inventory, err := attestationio.BindSource(repositoryRoot, attestationio.SourcePolicy{
		Name:                      "behavioral source",
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
	for _, prefix := range embeddedAssetPrefixes {
		if strings.HasPrefix(path, prefix) && filepath.Ext(path) == ".html" {
			return true
		}
	}
	if filepath.Ext(path) != ".go" {
		return false
	}
	for _, prefix := range productSourcePrefixes {
		if strings.HasPrefix(path, prefix) {
			return !strings.HasSuffix(path, "_test.go") && !pathComponentExcluded(path)
		}
	}
	for _, prefix := range conformanceSourcePrefixes {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		if pathComponentExcluded(path) {
			return false
		}
		if !strings.HasSuffix(path, "_test.go") {
			return true
		}
		// The distinct-process restart sentinel is test-owned product code. Its
		// test files therefore belong to the live-attestation source binding.
		return strings.HasPrefix(path, "conformance/systemstate/restart/")
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
	for _, prefix := range productSourcePrefixes {
		if strings.HasPrefix(prefix, directory) {
			return true
		}
		if strings.HasPrefix(directory, prefix) && !pathComponentExcluded(directory+"hidden.go") {
			return true
		}
	}
	for _, prefix := range conformanceSourcePrefixes {
		if strings.HasPrefix(prefix, directory) {
			return true
		}
		if strings.HasPrefix(directory, prefix) && !pathComponentExcluded(directory+"hidden.go") {
			return true
		}
	}
	return false
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
		path == "conformance/systemstate/attestation/testdata"
}

func pathComponentExcluded(path string) bool {
	return strings.Contains(path, "/testdata/") ||
		strings.HasPrefix(path, "conformance/oracles/") ||
		strings.HasPrefix(path, "conformance/fixtures/") ||
		strings.HasPrefix(path, "conformance/systemstate/attestation/testdata/")
}
