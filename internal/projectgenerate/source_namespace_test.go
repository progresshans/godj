//go:build darwin || linux

package projectgenerate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
)

func TestCheckRejectsRawModelReservedMethodCollisionsWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		source string
		method string
	}{
		{
			name:   "value receiver",
			source: "package blog\n\nfunc (BlogPost) Save() {}\n",
			method: "Save",
		},
		{
			name:   "pointer receiver in inactive build variant",
			source: "//go:build godj_never\n\npackage blog\n\nfunc (*BlogPost) WithAuthorID(int64) {}\n",
			method: "WithAuthorID",
		},
		{
			name:   "JSON receiver",
			source: "package blog\n\nfunc (*BlogPost) UnmarshalJSON([]byte) error { return nil }\n",
			method: "UnmarshalJSON",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := projectGenerateTestBundle(t)
			root := t.TempDir()
			writeProjectGenerateTestBundle(t, root, bundle)
			writeProjectGenerateTestFile(t, root, "blog/methods.go", []byte(test.source), 0o644)
			before := snapshotProjectGenerateTestTree(t, root)

			report, err := Check(context.Background(), root, bundle)
			if !errors.Is(err, ErrGeneratedConflict) || errors.Is(err, ErrGeneratedDrift) {
				t.Fatalf("Check(collision) error = %v, want conflict without generated drift", err)
			}
			if !report.Clean() {
				t.Fatalf("namespace-only conflict reported generated drift: %#v", report)
			}
			if !strings.Contains(err.Error(), "example.com/godj-project-bundle/blog.BlogPost."+test.method) {
				t.Fatalf("Check(collision) error = %v, want exact raw receiver method", err)
			}
			after := snapshotProjectGenerateTestTree(t, root)
			if strings.Join(before, "\n") != strings.Join(after, "\n") {
				t.Fatal("read-only namespace rejection mutated project")
			}
		})
	}
}

func TestSourceNamespacePlanAllowsProjectOnlyBundle(t *testing.T) {
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{
		PackageName: "project",
		ImportPath:  "example.com/project-only/project",
		Directory:   "project",
	}})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := decodeCommittedManifest(bundle.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sourceNamespacePlanFromBundle(bundle, manifest)
	if err != nil {
		t.Fatalf("sourceNamespacePlanFromBundle(project-only) error = %v", err)
	}
	if len(plan.apps) != 0 {
		t.Fatalf("sourceNamespacePlanFromBundle(project-only) apps = %d, want 0", len(plan.apps))
	}
}

func TestSourceNamespaceAuditIgnoresTestsAndAllowsUnreservedMethods(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	writeProjectGenerateTestBundle(t, root, bundle)
	writeProjectGenerateTestFile(t, root, "blog/methods.go", []byte("package blog\n\nfunc (BlogPost) NormalizeTitle() {}\n"), 0o644)
	writeProjectGenerateTestFile(t, root, "blog/methods_test.go", []byte("package blog\n\nfunc (BlogPost) Save() {}\n"), 0o644)
	writeProjectGenerateTestFile(t, root, "blog/.sidecar.go", []byte("not valid Go source\n"), 0o644)
	writeProjectGenerateTestFile(t, root, "blog/_ignored.go", []byte("also not valid Go source\n"), 0o644)

	report, err := Check(context.Background(), root, bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("Check(allowed source) report=%#v error=%v", report, err)
	}
}

func TestSourceNamespaceBudgetUsesExactGlobalBoundaries(t *testing.T) {
	var entries sourceNamespaceBudget
	for index := 0; index < maxProjectTreeEntries; index++ {
		if err := entries.consumeEntry("x"); err != nil {
			t.Fatalf("consumeEntry(exact boundary %d) error = %v", index, err)
		}
	}
	if err := entries.consumeEntry("x"); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("consumeEntry(over entry boundary) error = %v, want conflict", err)
	}

	var paths sourceNamespaceBudget
	if err := paths.consumeEntry(strings.Repeat("x", maxProjectTreePathBytes)); err != nil {
		t.Fatalf("consumeEntry(exact path-byte boundary) error = %v", err)
	}
	if err := paths.consumeEntry("x"); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("consumeEntry(over path-byte boundary) error = %v, want conflict", err)
	}

	var files sourceNamespaceBudget
	for index := 0; index < maxSourceNamespaceFiles; index++ {
		if err := files.consumeSource(0); err != nil {
			t.Fatalf("consumeSource(exact file boundary %d) error = %v", index, err)
		}
	}
	if err := files.consumeSource(0); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("consumeSource(over file boundary) error = %v, want conflict", err)
	}

	var bytes sourceNamespaceBudget
	if err := bytes.consumeSource(maxSourceNamespaceAggregateBytes); err != nil {
		t.Fatalf("consumeSource(exact aggregate boundary) error = %v", err)
	}
	if err := bytes.consumeSource(1); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("consumeSource(over aggregate boundary) error = %v, want conflict", err)
	}
}

func TestSourceNamespaceAuditRejectsUnsafeAndOversizedProductionSource(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		writeProjectGenerateTestBundle(t, root, bundle)
		writeProjectGenerateTestFile(t, outside, "secret.go", []byte("package outside\n"), 0o644)
		if err := os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(root, "blog", "methods.go")); err != nil {
			t.Fatal(err)
		}
		before := snapshotProjectGenerateTestTree(t, root)
		_, err := Check(context.Background(), root, bundle)
		if !errors.Is(err, ErrGeneratedConflict) {
			t.Fatalf("Check(source symlink) error = %v, want conflict", err)
		}
		if after := snapshotProjectGenerateTestTree(t, root); strings.Join(before, "\n") != strings.Join(after, "\n") {
			t.Fatal("source symlink rejection mutated project")
		}
	})

	t.Run("per-file cap", func(t *testing.T) {
		root := t.TempDir()
		writeProjectGenerateTestBundle(t, root, bundle)
		source := "package blog\n/*" + strings.Repeat("x", maxSourceNamespaceFileBytes) + "*/\n"
		writeProjectGenerateTestFile(t, root, "blog/methods.go", []byte(source), 0o644)
		_, err := Check(context.Background(), root, bundle)
		if !errors.Is(err, ErrGeneratedConflict) || !strings.Contains(err.Error(), "resource limit") {
			t.Fatalf("Check(oversized source) error = %v, want bounded conflict", err)
		}
	})
}

func TestGoCandidateVerifierRejectsRawMethodShadowingBeforeCompileSuccess(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	stage := t.TempDir()
	writeProjectGenerateTestFile(t, root, "go.mod", projectGenerateModuleFile(t), 0o644)
	writeProjectGenerateTestFile(t, root, "blog/methods.go", []byte("package blog\n\nfunc (*BlogPost) Save() {}\n"), 0o644)
	writeProjectGenerateTestBundle(t, stage, bundle)
	verifier, err := NewGoCandidateVerifier(root, bundle)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotProjectGenerateTestTree(t, root)
	err = verifier.Verify(context.Background(), stage)
	if !errors.Is(err, ErrCandidateVerification) || !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("Verify(raw shadow) error = %v, want candidate conflict", err)
	}
	if after := snapshotProjectGenerateTestTree(t, root); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatal("candidate namespace rejection mutated project")
	}
}

func TestPublishRejectsRawMethodCollisionWithExactPriorTargets(t *testing.T) {
	root := t.TempDir()
	prior := projectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
		t.Fatalf("Publish(prior) error = %v", err)
	}
	writeProjectGenerateTestFile(t, root, "blog/methods.go", []byte("package blog\n\nfunc (BlogPost) Unwrap() {}\n"), 0o644)
	before := snapshotProjectGenerateTestTree(t, root)
	next := changedProjectGenerateTestBundle(t)

	err := Publish(context.Background(), root, next, publicationTestVerifier(t, next, nil))
	if !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("Publish(raw collision) error = %v, want conflict", err)
	}
	assertPublishedBundle(t, root, prior)
	if after := snapshotProjectGenerateTestTree(t, root); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatal("raw-method publication rejection changed project")
	}
}

func TestPublishRevalidatesAppSourceFingerprintAfterCandidateCompile(t *testing.T) {
	root := t.TempDir()
	prior := projectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
		t.Fatalf("Publish(prior) error = %v", err)
	}
	initial := []byte("package blog\n\nfunc (BlogPost) NormalizeTitle() {}\n")
	changed := []byte("package blog\n\nfunc (BlogPost) NormalizeTitle() {}\nfunc (*BlogPost) ValidateTitle() {}\n")
	writeProjectGenerateTestFile(t, root, "blog/methods.go", initial, 0o644)
	next := changedProjectGenerateTestBundle(t)
	fired := false
	err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
		after: func(step publicationStep, _ string, _ int) error {
			if step != publicationStepPriorCASValid {
				return nil
			}
			fired = true
			return os.WriteFile(filepath.Join(root, "blog", "methods.go"), changed, 0o644)
		},
	})
	if !fired || !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("Publish(source change) fired=%t error=%v, want conflict", fired, err)
	}
	assertPublishedBundle(t, root, prior)
	if got, readErr := os.ReadFile(filepath.Join(root, "blog", "methods.go")); readErr != nil || !bytes.Equal(got, changed) {
		t.Fatalf("concurrent app source was not preserved: bytes=%q err=%v", got, readErr)
	}
}

func TestPublishCompletesMandatoryRecoveryBeforeNamespaceRejection(t *testing.T) {
	root := t.TempDir()
	prior := projectGenerateTestBundle(t)
	next := changedProjectGenerateTestBundle(t)
	if err := Publish(context.Background(), root, prior, publicationTestVerifier(t, prior, nil)); err != nil {
		t.Fatalf("Publish(prior) error = %v", err)
	}
	cleanupFault := errors.New("injected committed cleanup interruption")
	err := publishWithHooks(context.Background(), root, next, publicationTestVerifier(t, next, nil), publicationHooks{
		after: func(step publicationStep, _ string, _ int) error {
			if step == publicationStepTransactionClean {
				return cleanupFault
			}
			return nil
		},
	})
	if !errors.Is(err, ErrPublicationRecoveryRequired) {
		t.Fatalf("Publish(cleanup interruption) error = %v, want recovery required", err)
	}
	assertPublishedBundle(t, root, next)
	writeProjectGenerateTestFile(t, root, "blog/methods.go", []byte("package blog\n\nfunc (BlogPost) Save() {}\n"), 0o644)
	verifierCalls := 0
	err = Publish(context.Background(), root, next, CandidateVerifyFunc(func(context.Context, string) error {
		verifierCalls++
		return errors.New("namespace-rejected retry must not verify")
	}))
	if !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("Publish(recovery then namespace rejection) error = %v, want conflict", err)
	}
	if verifierCalls != 0 {
		t.Fatalf("Publish(recovery then namespace rejection) verifier calls = %d, want 0", verifierCalls)
	}
	assertPublishedBundle(t, root, next)
	assertPublicationControlClean(t, root)
	contents, readErr := os.ReadFile(filepath.Join(root, "blog", "methods.go"))
	if readErr != nil || !bytes.Equal(contents, []byte("package blog\n\nfunc (BlogPost) Save() {}\n")) {
		t.Fatalf("namespace conflict source after mandatory recovery = %q, %v", contents, readErr)
	}
}
