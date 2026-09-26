//go:build darwin || linux

package projectgenerate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
)

func externalAppFixture(t *testing.T) (host, library string, spec codegen.ProjectSpec, bundle codegen.GeneratedBundle) {
	t.Helper()
	library = t.TempDir()
	librarySpec := projectGenerateTestSpec()
	libraryBundle, err := codegen.GenerateProject(librarySpec)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectGenerateTestFile(t, library, "go.mod", projectGenerateModuleFile(t), 0o644)
	if err := Publish(t.Context(), library, libraryBundle, publicationTestVerifier(t, libraryBundle, nil)); err != nil {
		t.Fatal(err)
	}
	host = t.TempDir()
	spec = projectGenerateTestSpec()
	spec.Project.ImportPath = "example.com/import-host/project"
	for index := range spec.Apps {
		spec.Apps[index].External = true
		spec.Apps[index].Package.Directory = ""
	}
	bundle, err = codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	module := strings.Replace(string(projectGenerateModuleFile(t)), "module example.com/godj-project-bundle", "module example.com/import-host", 1)
	module += fmt.Sprintf("\nrequire example.com/godj-project-bundle v0.0.0\nreplace example.com/godj-project-bundle => %s\n", filepath.ToSlash(library))
	writeProjectGenerateTestFile(t, host, "go.mod", []byte(module), 0o644)
	return host, library, spec, bundle
}

func TestExternalAppsPublishAndCheckWithoutWritingDependency(t *testing.T) {
	host, library, _, bundle := externalAppFixture(t)
	canary := filepath.Join(t.TempDir(), "dependency-init-ran")
	writeProjectGenerateTestFile(t, library, "authors/hooks.go", []byte(fmt.Sprintf("package authors\nimport \"os\"\nfunc init() { _ = os.WriteFile(%q, []byte(\"executed\"), 0600) }\n", canary)), 0o644)
	before := snapshotProjectGenerateTestTree(t, library)
	verifier, err := NewGoCandidateVerifier(host, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := Publish(t.Context(), host, bundle, verifier); err != nil {
		t.Fatal(err)
	}
	report, err := Check(t.Context(), host, bundle)
	if err != nil || !report.Clean() {
		t.Fatal("external app check", report, err)
	}
	if after := snapshotProjectGenerateTestTree(t, library); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatal("consumer publication changed dependency")
	}
	for _, directory := range []string{"authors", "blog"} {
		if _, err := os.Stat(filepath.Join(host, directory)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("host copied dependency package")
		}
	}
	assertPublishedBundle(t, host, bundle)
	if _, err := os.Stat(canary); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("external app inspection or verification executed dependency code", err)
	}
}

func TestExternalizingOwnedFilesCannotDeleteItsOwnDependency(t *testing.T) {
	_, library, _, _ := externalAppFixture(t)
	before := snapshotProjectGenerateTestTree(t, library)
	spec := projectGenerateTestSpec()
	for index := range spec.Apps {
		spec.Apps[index].External = true
		spec.Apps[index].Package.Directory = ""
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	verifier := CandidateVerifyFunc(func(context.Context, string) error { called = true; return nil })
	if err := Publish(t.Context(), library, bundle, verifier); called || !errors.Is(err, ErrGeneratedConflict) {
		t.Fatal("overlapping ownership reached candidate verification", err)
	}
	if after := snapshotProjectGenerateTestTree(t, library); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatal("failed ownership transition changed original library")
	}
}

func TestExternalAppInsideProjectRootRemainsReadOnly(t *testing.T) {
	host := t.TempDir()
	spec := projectGenerateTestSpec()
	library, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectGenerateTestFile(t, host, "go.mod", projectGenerateModuleFile(t), 0o644)
	for _, file := range library.Files() {
		if strings.HasPrefix(file.Owner, "app:") {
			writeProjectGenerateTestFile(t, host, file.Path, file.Source(), 0o644)
		}
	}
	for index := range spec.Apps {
		spec.Apps[index].External = true
		spec.Apps[index].Package.Directory = ""
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewGoCandidateVerifier(host, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := Publish(t.Context(), host, bundle, verifier); err != nil {
		t.Fatal(err)
	}
	if report, err := Check(t.Context(), host, bundle); err != nil || !report.Clean() {
		t.Fatal("co-located dependency became unowned generated drift", report, err)
	}
	for _, file := range library.Files() {
		if !strings.HasPrefix(file.Owner, "app:") {
			continue
		}
		actual, err := os.ReadFile(filepath.Join(host, file.Path))
		if err != nil || !bytes.Equal(actual, file.Source()) {
			t.Fatal("host changed read-only dependency", file.Path, err)
		}
	}
}

func TestExternalAppStaleCompanionAndRawMethodsFailBeforePublication(t *testing.T) {
	for _, mode := range []string{"stale_companion", "missing_companion", "reserved_method", "unowned_generated"} {
		t.Run(mode, func(t *testing.T) {
			host, library, _, bundle := externalAppFixture(t)
			if err := Publish(t.Context(), host, bundle, publicationTestVerifier(t, bundle, nil)); err != nil {
				t.Fatal(err)
			}
			before := snapshotProjectGenerateTestTree(t, host)
			switch mode {
			case "stale_companion":
				path := filepath.Join(library, "authors", "zz_godj_relation_object.go")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data = bytes.Replace(data, []byte("type GoDjAppPart2_"), []byte("type RemovedGoDjAppPart2_"), 1)
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			case "missing_companion":
				if err := os.Remove(filepath.Join(library, "authors", "zz_godj_relation_projection.go")); err != nil {
					t.Fatal(err)
				}
			case "reserved_method":
				writeProjectGenerateTestFile(t, library, "authors/custom.go", []byte("package authors\nfunc (Author) Unwrap() {}\n"), 0o644)
			case "unowned_generated":
				writeProjectGenerateTestFile(t, library, "authors/zz_godj_extra.go", []byte("package authors\n"), 0o644)
			}
			if _, err := Check(t.Context(), host, bundle); !errors.Is(err, ErrGeneratedConflict) {
				t.Fatal("dependency drift was accepted by check", err)
			}
			called := false
			err := Publish(t.Context(), host, bundle, CandidateVerifyFunc(func(context.Context, string) error { called = true; return nil }))
			if called || !errors.Is(err, ErrGeneratedConflict) {
				t.Fatal("dependency conflict reached publication", err)
			}
			if after := snapshotProjectGenerateTestTree(t, host); strings.Join(before, "\n") != strings.Join(after, "\n") {
				t.Fatal("dependency rejection changed last-good host")
			}
		})
	}
}

func TestExternalAppChangeDuringVerificationPreservesLastGoodHost(t *testing.T) {
	host, library, spec, prior := externalAppFixture(t)
	if err := Publish(t.Context(), host, prior, publicationTestVerifier(t, prior, nil)); err != nil {
		t.Fatal(err)
	}
	spec.Project.PackageName = "hostproject"
	next, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotProjectGenerateTestTree(t, host)
	changed := false
	err = Publish(t.Context(), host, next, CandidateVerifyFunc(func(context.Context, string) error {
		changed = true
		return os.WriteFile(filepath.Join(library, "authors", "custom.go"), []byte("package authors\nfunc (Author) AddedDuringValidation() {}\n"), 0o644)
	}))
	if !changed || !errors.Is(err, ErrGeneratedConflict) {
		t.Fatal("concurrent dependency edit was not fenced", err)
	}
	if after := snapshotProjectGenerateTestTree(t, host); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatal("dependency change replaced last-good host")
	}
	assertPublishedBundle(t, host, prior)
}

func TestManifestCannotClaimFilesFromExternalApp(t *testing.T) {
	_, _, _, bundle := externalAppFixture(t)
	manifest, err := decodeCommittedManifest(bundle.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	manifest.Files[0].Owner = "app:" + manifest.Apps[0].AppLabel
	if err := validateCommittedManifestStructure(manifest); err == nil {
		t.Fatal("manifest gained write ownership over dependency")
	}
}

func TestExternalAppsRejectCaseAliasedControlDirectory(t *testing.T) {
	host, library, _, bundle := externalAppFixture(t)
	destination := filepath.Join(host, ".GODJ", "dependency")
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(library, destination); err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(host, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	mod = bytes.ReplaceAll(mod, []byte(filepath.ToSlash(library)), []byte(filepath.ToSlash(destination)))
	if err := os.WriteFile(filepath.Join(host, "go.mod"), mod, 0644); err != nil {
		t.Fatal(err)
	}
	manifest, err := validateGeneratedBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sourceNamespacePlanFromBundle(bundle, manifest)
	if err != nil {
		t.Fatal(err)
	}
	host, err = canonicalProjectRoot(host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureSourceNamespaceSnapshot(t.Context(), host, manifest, plan); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatal("control case alias was accepted", err)
	}
}

func TestExternalAppsRejectCaseAliasedPriorOwnership(t *testing.T) {
	_, library, _, _ := externalAppFixture(t)
	prior, exists, err := readPriorManifest(library)
	if err != nil || !exists {
		t.Fatal(err)
	}
	// File ownership is case-folded on every platform. A manifest written on
	// another filesystem must not retire an imported companion through an alias.
	for index := range prior.Apps {
		prior.Apps[index].Package.Directory = strings.ToUpper(prior.Apps[index].Package.Directory)
	}
	for index := range prior.Files {
		if strings.HasPrefix(prior.Files[index].Owner, "app:") {
			parts := strings.Split(prior.Files[index].Path, "/")
			parts[0] = strings.ToUpper(parts[0])
			prior.Files[index].Path = strings.Join(parts, "/")
		}
	}
	document, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, codegen.GeneratedManifestPath), append(document, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	spec := projectGenerateTestSpec()
	for index := range spec.Apps {
		spec.Apps[index].External = true
		spec.Apps[index].Package.Directory = ""
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := validateGeneratedBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sourceNamespacePlanFromBundle(bundle, manifest)
	if err != nil {
		t.Fatal(err)
	}
	library, err = canonicalProjectRoot(library)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureSourceNamespaceSnapshot(t.Context(), library, manifest, plan); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatal("case-folded publication files escaped ownership guard", err)
	}
}
