//go:build darwin || linux

package projectgenerate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestDecodeCommittedManifestStrictCanonicalCurrent(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	manifest, err := decodeCommittedManifest(bundle.Manifest())
	if err != nil {
		t.Fatalf("decodeCommittedManifest(current) error = %v", err)
	}
	if manifest.SnapshotSHA256 != bundle.SnapshotSHA256() {
		t.Fatalf("snapshot = %q, want %q", manifest.SnapshotSHA256, bundle.SnapshotSHA256())
	}
	if _, err := validateGeneratedBundle(bundle); err != nil {
		t.Fatalf("validateGeneratedBundle(current) error = %v", err)
	}
}

func TestDecodeCommittedManifestAcceptsSafePriorRosterButCurrentValidatorRejectsIt(t *testing.T) {
	manifest := decodeProjectGenerateTestManifest(t)
	manifest.GeneratorABI[1].Version = "godj-codegen-retired-v0"
	manifest.GeneratorABI = append(manifest.GeneratorABI, manifestABI{
		Role: "project.retired", Filename: "zz_godj_retired.go", Version: "godj-codegen-retired-v0",
	})
	manifest.Files = append(manifest.Files, manifestFile{
		Path: "project/zz_godj_retired.go", Owner: "project", Mode: "0644", SHA256: strings.Repeat("1", 64),
	})
	sort.Slice(manifest.Files, func(left, right int) bool { return manifest.Files[left].Path < manifest.Files[right].Path })
	document := encodeProjectGenerateTestManifest(t, manifest)
	decoded, err := decodeCommittedManifest(document)
	if err != nil {
		t.Fatalf("decode safe prior manifest error = %v", err)
	}
	if err := validateCurrentManifest(decoded); err == nil {
		t.Fatal("validateCurrentManifest accepted prior ABI/roster")
	}
}

func TestDecodeCommittedManifestNeverOwnsArbitraryUserOrControlFiles(t *testing.T) {
	for _, candidate := range []string{"project/main.go", ".godj/zz_godj_claimed.go", "project/sub/zz_godj_claimed.go"} {
		t.Run(strings.ReplaceAll(candidate, "/", "_"), func(t *testing.T) {
			manifest := decodeProjectGenerateTestManifest(t)
			manifest.Files = append(manifest.Files, manifestFile{
				Path: candidate, Owner: "project", Mode: "0644", SHA256: strings.Repeat("2", 64),
			})
			sort.Slice(manifest.Files, func(left, right int) bool { return manifest.Files[left].Path < manifest.Files[right].Path })
			if _, err := decodeCommittedManifest(encodeProjectGenerateTestManifest(t, manifest)); err == nil {
				t.Fatalf("decodeCommittedManifest accepted arbitrary prior-owned path %q", candidate)
			}
		})
	}
}

func TestDecodeCommittedManifestRejectsUnicodeAndCaseFoldOwnershipAliases(t *testing.T) {
	for _, candidate := range []string{"prøject/zz_godj_claimed.go", "project/zz_godj_생성.go"} {
		manifest := decodeProjectGenerateTestManifest(t)
		manifest.Files = append(manifest.Files, manifestFile{
			Path: candidate, Owner: "project", Mode: "0644", SHA256: strings.Repeat("4", 64),
		})
		sort.Slice(manifest.Files, func(left, right int) bool { return manifest.Files[left].Path < manifest.Files[right].Path })
		if _, err := decodeCommittedManifest(encodeProjectGenerateTestManifest(t, manifest)); err == nil {
			t.Fatalf("decodeCommittedManifest accepted Unicode ownership path %q", candidate)
		}
	}

	manifest := decodeProjectGenerateTestManifest(t)
	duplicate := manifest.Files[0]
	duplicate.Path = strings.TrimSuffix(duplicate.Path, "zz_godj_generated.go") + "zz_godj_Generated.go"
	manifest.Files = append(manifest.Files, duplicate)
	sort.Slice(manifest.Files, func(left, right int) bool { return manifest.Files[left].Path < manifest.Files[right].Path })
	if _, err := decodeCommittedManifest(encodeProjectGenerateTestManifest(t, manifest)); err == nil {
		t.Fatal("decodeCommittedManifest accepted case-folded file ownership collision")
	}
}

func TestDecodeCommittedManifestRejectsNoncanonicalOrUnsafeDocuments(t *testing.T) {
	baseline := projectGenerateTestBundle(t).Manifest()
	manifest := decodeProjectGenerateTestManifest(t)
	tests := []struct {
		name     string
		document func() []byte
	}{
		{name: "empty", document: func() []byte { return nil }},
		{name: "no final LF", document: func() []byte { return bytes.TrimSuffix(baseline, []byte("\n")) }},
		{name: "trailing value", document: func() []byte { return append(append([]byte(nil), baseline...), []byte("{}\n")...) }},
		{name: "duplicate member", document: func() []byte {
			return bytes.Replace(baseline, []byte(`{"format_version":1`), []byte(`{"format_version":1,"format_version":1`), 1)
		}},
		{name: "unknown member", document: func() []byte {
			return bytes.Replace(baseline, []byte(`{"format_version":1`), []byte(`{"unknown":0,"format_version":1`), 1)
		}},
		{name: "alternate whitespace", document: func() []byte { return append([]byte(" "), baseline...) }},
		{name: "unsupported version", document: func() []byte {
			changed := manifest
			changed.FormatVersion++
			return encodeProjectGenerateTestManifest(t, changed)
		}},
		{name: "uppercase hash", document: func() []byte {
			changed := manifest
			changed.SnapshotSHA256 = strings.ToUpper(changed.SnapshotSHA256)
			return encodeProjectGenerateTestManifest(t, changed)
		}},
		{name: "bad mode", document: func() []byte {
			changed := manifest
			changed.Files = append([]manifestFile(nil), manifest.Files...)
			changed.Files[0].Mode = "0600"
			return encodeProjectGenerateTestManifest(t, changed)
		}},
		{name: "unknown owner", document: func() []byte {
			changed := manifest
			changed.Files = append([]manifestFile(nil), manifest.Files...)
			changed.Files[0].Owner = "unknown"
			return encodeProjectGenerateTestManifest(t, changed)
		}},
		{name: "file order", document: func() []byte {
			changed := manifest
			changed.Files = append([]manifestFile(nil), manifest.Files...)
			changed.Files[0], changed.Files[1] = changed.Files[1], changed.Files[0]
			return encodeProjectGenerateTestManifest(t, changed)
		}},
		{name: "app order", document: func() []byte {
			changed := manifest
			changed.Apps = append([]manifestApp(nil), manifest.Apps...)
			changed.Apps[0], changed.Apps[1] = changed.Apps[1], changed.Apps[0]
			return encodeProjectGenerateTestManifest(t, changed)
		}},
		{name: "reserved directory casefold", document: func() []byte {
			changed := manifest
			changed.Project.Directory = ".GoDj/project"
			return encodeProjectGenerateTestManifest(t, changed)
		}},
		{name: "ABI user filename", document: func() []byte {
			changed := manifest
			changed.GeneratorABI = append([]manifestABI(nil), manifest.GeneratorABI...)
			changed.GeneratorABI[1].Filename = "main.go"
			return encodeProjectGenerateTestManifest(t, changed)
		}},
		{name: "ABI duplicate role", document: func() []byte {
			changed := manifest
			changed.GeneratorABI = append(changed.GeneratorABI, changed.GeneratorABI[1])
			return encodeProjectGenerateTestManifest(t, changed)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeCommittedManifest(test.document()); err == nil {
				t.Fatal("decodeCommittedManifest accepted invalid document")
			}
		})
	}
}

func TestDecodeCommittedManifestBoundsBeforeTypedAllocation(t *testing.T) {
	oversize := append(bytes.Repeat([]byte(" "), maxCommittedManifestBytes), '\n')
	if _, err := decodeCommittedManifest(oversize); err == nil || !strings.Contains(err.Error(), "resource limit") {
		t.Fatalf("oversize error = %v", err)
	}

	tooManyApps := []byte(`{"format_version":1,"snapshot_sha256":"` + strings.Repeat("0", 64) +
		`","generator_abi":[],"project":{},"apps":[` + strings.Repeat(`{},`, maxManifestApps) +
		`{}],"files":[]}` + "\n")
	if _, err := decodeCommittedManifest(tooManyApps); err == nil || !strings.Contains(err.Error(), "resource limit") {
		t.Fatalf("too-many-apps error = %v", err)
	}

	tooManyABI := []byte(`{"format_version":1,"snapshot_sha256":"` + strings.Repeat("0", 64) +
		`","generator_abi":[` + strings.Repeat(`{},`, 64) + `{}],"project":{},"apps":[],"files":[]}` + "\n")
	if _, err := decodeCommittedManifest(tooManyABI); err == nil || !strings.Contains(err.Error(), "resource limit") {
		t.Fatalf("too-many-ABI error = %v", err)
	}
}

func projectGenerateTestBundle(t *testing.T) codegen.GeneratedBundle {
	t.Helper()
	bundle, err := codegen.GenerateProject(projectGenerateTestSpec())
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	return bundle
}

func projectGenerateTestSpec() codegen.ProjectSpec {
	authors := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "authors",
		Models: []ir.Model{{Name: "author", GoName: "Author", Fields: []ir.Field{
			{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 100},
		}}},
	}
	blog := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{Name: "blog_post", GoName: "BlogPost", Fields: []ir.Field{
			{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
			{Name: "author", GoName: "AuthorID", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Cardinality: ir.RelationManyToOne,
				Reverse: ir.ReverseRelation{Name: "blog_posts"}, OnDelete: ir.DeleteProtect,
			}},
		}}},
	}
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"},
		Apps: []codegen.AppSpec{
			{Alias: "blog", Package: codegen.PackageSpec{PackageName: "blog", ImportPath: "example.com/godj-project-bundle/blog", Directory: "blog"}, Schema: blog},
			{Alias: "authors", Package: codegen.PackageSpec{PackageName: "authors", ImportPath: "example.com/godj-project-bundle/authors", Directory: "authors"}, Schema: authors},
		},
	}
}

func decodeProjectGenerateTestManifest(t *testing.T) committedManifest {
	t.Helper()
	manifest, err := decodeCommittedManifest(projectGenerateTestBundle(t).Manifest())
	if err != nil {
		t.Fatalf("decode test manifest: %v", err)
	}
	return manifest
}

func encodeProjectGenerateTestManifest(t *testing.T, manifest committedManifest) []byte {
	t.Helper()
	document, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal test manifest: %v", err)
	}
	return append(document, '\n')
}

func writeProjectGenerateTestFile(t *testing.T, root, relative string, contents []byte, mode os.FileMode) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", relative, err)
	}
	if err := os.WriteFile(filename, contents, mode); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func writeProjectGenerateTestBundle(t *testing.T, root string, bundle codegen.GeneratedBundle) {
	t.Helper()
	for _, file := range bundle.Files() {
		writeProjectGenerateTestFile(t, root, file.Path, file.Source(), file.Mode)
	}
	writeProjectGenerateTestFile(t, root, generatedManifestRelativePath, bundle.Manifest(), 0o644)
}

func projectGenerateRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func projectGenerateModuleFile(t *testing.T) []byte {
	t.Helper()
	return []byte(fmt.Sprintf(`module example.com/godj-project-bundle

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, filepath.ToSlash(projectGenerateRepositoryRoot(t))))
}

func requireErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(..., %v)", err, target)
	}
}
