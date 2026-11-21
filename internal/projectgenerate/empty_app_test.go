//go:build darwin || linux

package projectgenerate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestEmptyAppPublicationKeepsSourceGuardsAndFirstModelOwnership(t *testing.T) {
	spec := projectGenerateTestSpec()
	for index := range spec.Apps {
		spec.Apps[index].Schema.Models = nil
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// Real compilation for this transition also runs through the global
	// startproject product. This test isolates filesystem/namespace ownership.
	if err := Publish(t.Context(), root, bundle, publicationTestVerifier(t, bundle, new(atomic.Int32))); err != nil {
		t.Fatal("empty apps could not publish", err)
	}
	if checked, err := Check(t.Context(), root, bundle); err != nil || !checked.Clean() {
		t.Fatal("empty app check differs", checked, err)
	}
	out := t.TempDir()
	writeProjectGenerateTestFile(t, out, "private.go", []byte("package private\n"), 0o644)
	link := filepath.Join(root, spec.Apps[0].Package.Directory, "unexpected.go")
	if err := os.Symlink(filepath.Join(out, "private.go"), link); err != nil {
		t.Fatal(err)
	}
	before := snapshotProjectGenerateTestTree(t, root)
	if _, err := Check(t.Context(), root, bundle); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatal("empty app bypassed source symlink guard", err)
	}
	if !reflect.DeepEqual(before, snapshotProjectGenerateTestTree(t, root)) {
		t.Fatal("read-only empty-app rejection changed files")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	spec.Apps[0].Schema.Models = []ir.Model{{Name: "note", GoName: "Note"}}
	next, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := Publish(t.Context(), root, next, publicationTestVerifier(t, next, new(atomic.Int32))); err != nil {
		t.Fatal("first model could not publish", err)
	}
	if checked, err := Check(t.Context(), root, next); err != nil || !checked.Clean() {
		t.Fatal("first model bundle differs", checked, err)
	}
	app := spec.Apps[0]
	writeProjectGenerateTestFile(t, root, filepath.Join(app.Package.Directory, "methods.go"), []byte("package "+app.Package.PackageName+"\nfunc (Note) Save() {}\n"), 0o644)
	before = snapshotProjectGenerateTestTree(t, root)
	if _, err := Check(t.Context(), root, next); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatal("first model lost its reserved method guard", err)
	}
	if !reflect.DeepEqual(before, snapshotProjectGenerateTestTree(t, root)) {
		t.Fatal("reserved method rejection changed the last-good bundle")
	}
}

func TestAbsentFacadeRequiresTheCanonicalEmptySchemaHash(t *testing.T) {
	spec := projectGenerateTestSpec()
	nonemptyHash, err := ir.Hash(spec.Apps[0].Schema)
	if err != nil {
		t.Fatal(err)
	}
	for index := range spec.Apps {
		spec.Apps[index].Schema.Models = nil
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := validateGeneratedBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Apps[0].SchemaSHA256 = nonemptyHash
	if _, err := sourceNamespacePlanFromBundle(bundle, manifest); !errors.Is(err, ErrInvalidGeneratedBundle) {
		t.Fatal("a missing model surface was treated as an empty app", err)
	}
}
