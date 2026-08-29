//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type makemigrationsFingerprintFixture struct {
	root     string
	project  retainedProject
	packages []makemigrationsGoListPackage
	files    map[string][]byte
}

func TestMakemigrationsBuildInputFingerprintCanonicalRoster(t *testing.T) {
	t.Parallel()
	fixture := newMakemigrationsFingerprintFixture(t)

	forward := encodeMakemigrationsGoList(t, fixture.packages)
	reversedPackages := slices.Clone(fixture.packages)
	slices.Reverse(reversedPackages)
	reversedPackages[1].GoFiles = slices.Clone(reversedPackages[1].GoFiles)
	slices.Reverse(reversedPackages[1].GoFiles)
	reversed := encodeMakemigrationsGoList(t, reversedPackages)

	first, err := computeMakemigrationsBuildInputFingerprint(fixture.project, forward)
	if err != nil {
		t.Fatal(err)
	}
	second, err := computeMakemigrationsBuildInputFingerprint(fixture.project, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("go-list ordering changed fingerprint: first=%+v second=%+v", first, second)
	}
	if first.memberCount != 20 {
		t.Fatalf("member count = %d, want 20", first.memberCount)
	}
	var wantBytes uint64
	for _, document := range fixture.files {
		wantBytes += uint64(len(document))
	}
	graphDocument, err := canonicalMakemigrationsDependencyGraph(fixture.packages)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes += uint64(len(graphDocument))
	if first.documentBytes != wantBytes {
		t.Fatalf("document bytes = %d, want %d", first.documentBytes, wantBytes)
	}

	third, err := computeMakemigrationsBuildInputFingerprint(fixture.project, forward)
	if err != nil || third != first {
		t.Fatalf("repeat fingerprint = %+v err=%v, want %+v", third, err, first)
	}

	relocated := slices.Clone(fixture.packages)
	relocatedModule := *relocated[3].Module
	relocated[3].Dir = filepath.Join(filepath.Dir(relocated[3].Dir), "other-cache-location")
	relocatedModule.Dir = relocated[3].Dir
	relocated[3].Module = &relocatedModule
	relocatedFingerprint, err := computeMakemigrationsBuildInputFingerprint(
		fixture.project,
		encodeMakemigrationsGoList(t, relocated),
	)
	if err != nil || relocatedFingerprint != first {
		t.Fatalf("absolute dependency cache path affected canonical graph: got=%+v err=%v want=%+v", relocatedFingerprint, err, first)
	}
}

func TestMakemigrationsBuildInputFingerprintTracksExactBytesPresenceAndMode(t *testing.T) {
	t.Parallel()
	fixture := newMakemigrationsFingerprintFixture(t)
	document := encodeMakemigrationsGoList(t, fixture.packages)
	baseline, err := computeMakemigrationsBuildInputFingerprint(fixture.project, document)
	if err != nil {
		t.Fatal(err)
	}

	mainPath := filepath.Join(fixture.root, "cmd", "site", "main.go")
	before, err := os.Stat(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := slices.Clone(fixture.files["cmd/site/main.go"])
	changed[len(changed)-2] = '2'
	if err := os.WriteFile(mainPath, changed, before.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() {
		t.Fatal("same-inode same-length mutation precondition was not preserved")
	}
	assertMakemigrationsFingerprintChanged(t, fixture.project, document, baseline, "same-inode source bytes")
	writeMakemigrationsFixtureFile(t, fixture.root, "cmd/site/main.go", fixture.files["cmd/site/main.go"], 0o640)

	for _, test := range []struct {
		name string
		path string
		body []byte
	}{
		{name: "embed", path: "cmd/site/assets/page.txt", body: []byte("embed-b")},
		{name: "generated manifest", path: ".godj/generated-manifest.json", body: []byte("{\"version\":2}\n")},
		{name: "module", path: "go.mod", body: []byte("module example.invalid/changed\n")},
		{name: "descriptor", path: descriptorName, body: append(slices.Clone(fixture.files[descriptorName]), []byte("# changed\n")...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := fixture.files[test.path]
			writeMakemigrationsFixtureFile(t, fixture.root, test.path, test.body, 0o640)
			assertMakemigrationsFingerprintChanged(t, fixture.project, document, baseline, test.name)
			writeMakemigrationsFixtureFile(t, fixture.root, test.path, original, 0o640)
		})
	}

	if err := os.Chmod(mainPath, 0o600); err != nil {
		t.Fatal(err)
	}
	assertMakemigrationsFingerprintChanged(t, fixture.project, document, baseline, "permission mode")
	if err := os.Chmod(mainPath, 0o640); err != nil {
		t.Fatal(err)
	}

	goWork := []byte("go 1.26.0\n")
	writeMakemigrationsFixtureFile(t, fixture.root, "go.work", goWork, 0o640)
	withGoWork, err := computeMakemigrationsBuildInputFingerprint(fixture.project, document)
	if err != nil || withGoWork.digest == baseline.digest || withGoWork.memberCount != baseline.memberCount ||
		withGoWork.documentBytes != baseline.documentBytes+uint64(len(goWork)) {
		t.Fatalf("absent-to-present go.work = %+v err=%v, baseline=%+v", withGoWork, err, baseline)
	}
	if err := os.Remove(filepath.Join(fixture.root, "go.work")); err != nil {
		t.Fatal(err)
	}
	restored, err := computeMakemigrationsBuildInputFingerprint(fixture.project, document)
	if err != nil || restored != baseline {
		t.Fatalf("restored absence fingerprint = %+v err=%v, want %+v", restored, err, baseline)
	}

	changedPackages := slices.Clone(fixture.packages)
	changedModule := *changedPackages[3].Module
	changedModule.Version = "v1.2.4"
	changedPackages[3].Module = &changedModule
	assertMakemigrationsFingerprintChanged(
		t,
		fixture.project,
		encodeMakemigrationsGoList(t, changedPackages),
		baseline,
		"normalized dependency graph",
	)
	replacedPackages := slices.Clone(fixture.packages)
	replacedModule := *replacedPackages[5].Module
	replacement := *replacedModule.Replace
	replacement.Path = "../other-godj"
	replacedModule.Replace = &replacement
	replacedPackages[5].Module = &replacedModule
	assertMakemigrationsFingerprintChanged(
		t,
		fixture.project,
		encodeMakemigrationsGoList(t, replacedPackages),
		baseline,
		"normalized replacement graph",
	)
}

func TestMakemigrationsBuildInputFingerprintRejectsUnsafeRoster(t *testing.T) {
	t.Parallel()

	t.Run("duplicate member", func(t *testing.T) {
		fixture := newMakemigrationsFingerprintFixture(t)
		fixture.packages[0].GoFiles = append(fixture.packages[0].GoFiles, fixture.packages[0].GoFiles[0])
		assertMakemigrationsFingerprintRejected(t, fixture.project, encodeMakemigrationsGoList(t, fixture.packages))
	})

	t.Run("duplicate package", func(t *testing.T) {
		fixture := newMakemigrationsFingerprintFixture(t)
		fixture.packages = append(fixture.packages, fixture.packages[0])
		assertMakemigrationsFingerprintRejected(t, fixture.project, encodeMakemigrationsGoList(t, fixture.packages))
	})

	t.Run("outside member", func(t *testing.T) {
		fixture := newMakemigrationsFingerprintFixture(t)
		fixture.packages[0].GoFiles = []string{"../outside.go"}
		assertMakemigrationsFingerprintRejected(t, fixture.project, encodeMakemigrationsGoList(t, fixture.packages))
	})

	t.Run("unbound outside package", func(t *testing.T) {
		fixture := newMakemigrationsFingerprintFixture(t)
		fixture.packages = append(fixture.packages, makemigrationsGoListPackage{
			Dir: filepath.Clean(t.TempDir()), ImportPath: "example.invalid/outside", Name: "outside", GoFiles: []string{"outside.go"},
		})
		assertMakemigrationsFingerprintRejected(t, fixture.project, encodeMakemigrationsGoList(t, fixture.packages))
	})

	t.Run("exact framework main and local replace are excluded", func(t *testing.T) {
		fixture := newMakemigrationsFingerprintFixture(t)
		if _, err := computeMakemigrationsBuildInputFingerprint(
			fixture.project,
			encodeMakemigrationsGoList(t, fixture.packages),
		); err != nil {
			t.Fatalf("exact GoDj framework exclusions were rejected: %v", err)
		}
	})

	t.Run("symlink member", func(t *testing.T) {
		fixture := newMakemigrationsFingerprintFixture(t)
		member := filepath.Join(fixture.root, "cmd", "site", "main.go")
		outside := filepath.Join(filepath.Dir(fixture.root), "outside.go")
		if err := os.WriteFile(outside, fixture.files["cmd/site/main.go"], 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(member); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, member); err != nil {
			t.Fatal(err)
		}
		assertMakemigrationsFingerprintRejected(t, fixture.project, encodeMakemigrationsGoList(t, fixture.packages))
	})

	t.Run("symlink package directory", func(t *testing.T) {
		fixture := newMakemigrationsFingerprintFixture(t)
		packageDirectory := filepath.Join(fixture.root, "internal", "lib")
		realDirectory := packageDirectory + "-real"
		if err := os.Rename(packageDirectory, realDirectory); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realDirectory, packageDirectory); err != nil {
			t.Fatal(err)
		}
		assertMakemigrationsFingerprintRejected(t, fixture.project, encodeMakemigrationsGoList(t, fixture.packages))
	})

	t.Run("nonregular member", func(t *testing.T) {
		fixture := newMakemigrationsFingerprintFixture(t)
		member := filepath.Join(fixture.root, "cmd", "site", "main.go")
		if err := os.Remove(member); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(member, 0o700); err != nil {
			t.Fatal(err)
		}
		assertMakemigrationsFingerprintRejected(t, fixture.project, encodeMakemigrationsGoList(t, fixture.packages))
	})

	t.Run("root rebound", func(t *testing.T) {
		fixture := newMakemigrationsFingerprintFixture(t)
		oldRoot := fixture.root + "-old"
		if err := os.Rename(fixture.root, oldRoot); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(fixture.root, 0o700); err != nil {
			t.Fatal(err)
		}
		assertMakemigrationsFingerprintRejected(t, fixture.project, encodeMakemigrationsGoList(t, fixture.packages))
	})

	t.Run("descriptor semantic rebound", func(t *testing.T) {
		fixture := newMakemigrationsFingerprintFixture(t)
		writeMakemigrationsFixtureFile(
			t,
			fixture.root,
			descriptorName,
			[]byte("format_version = 1\n\n[project]\npackage = \"./cmd/other\"\n"),
			0o640,
		)
		assertMakemigrationsFingerprintRejected(t, fixture.project, encodeMakemigrationsGoList(t, fixture.packages))
	})
}

func TestMakemigrationsBuildInputFingerprintRejectsInvalidGoList(t *testing.T) {
	t.Parallel()
	fixture := newMakemigrationsFingerprintFixture(t)
	valid := encodeMakemigrationsGoList(t, fixture.packages)

	for _, test := range []struct {
		name     string
		document []byte
	}{
		{name: "empty", document: nil},
		{name: "truncated JSON", document: []byte("{")},
		{name: "invalid UTF-8", document: append(slices.Clone(valid), 0xff)},
		{name: "top-level array", document: []byte("[]")},
		{name: "incomplete package", document: encodeMakemigrationsGoList(t, []makemigrationsGoListPackage{{Dir: fixture.root, Incomplete: true}})},
		{name: "missing selected package", document: encodeMakemigrationsGoList(t, fixture.packages[1:])},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertMakemigrationsFingerprintRejected(t, fixture.project, test.document)
		})
	}
}

func TestMakemigrationsBuildInputFingerprintBoundsDeepReads(t *testing.T) {
	t.Parallel()
	fixture := newMakemigrationsFingerprintFixture(t)
	deepRelative := "cmd/site/assets/one/two/three/payload.txt"
	writeMakemigrationsFixtureFile(t, fixture.root, deepRelative, bytes.Repeat([]byte("x"), 65), 0o640)
	fixture.packages[0].EmbedFiles = append(fixture.packages[0].EmbedFiles, "assets/one/two/three/payload.txt")
	document := encodeMakemigrationsGoList(t, fixture.packages)

	limits := defaultMakemigrationsBuildInputLimits()
	limits.fileBytes = 64
	if _, err := computeMakemigrationsBuildInputFingerprintWithLimits(fixture.project, document, limits); err == nil {
		t.Fatal("oversized deep member was accepted")
	}

	limits = defaultMakemigrationsBuildInputLimits()
	limits.pathDepth = 7
	if _, err := computeMakemigrationsBuildInputFingerprintWithLimits(fixture.project, document, limits); err != nil {
		t.Fatalf("deep member at the path bound was rejected: %v", err)
	}
	limits.pathDepth = 6
	if _, err := computeMakemigrationsBuildInputFingerprintWithLimits(fixture.project, document, limits); err == nil {
		t.Fatal("deep member beyond the path bound was accepted")
	}

	limits = defaultMakemigrationsBuildInputLimits()
	limits.goListBytes = uint64(len(document) - 1)
	if _, err := computeMakemigrationsBuildInputFingerprintWithLimits(fixture.project, document, limits); err == nil {
		t.Fatal("oversized go-list document was accepted")
	}

	baseline, err := computeMakemigrationsBuildInputFingerprint(fixture.project, document)
	if err != nil {
		t.Fatal(err)
	}
	limits = defaultMakemigrationsBuildInputLimits()
	limits.documentBytes = baseline.documentBytes - 1
	if _, err := computeMakemigrationsBuildInputFingerprintWithLimits(fixture.project, document, limits); err == nil {
		t.Fatal("oversized aggregate documents were accepted")
	}

	limits = defaultMakemigrationsBuildInputLimits()
	limits.memberCount = int(baseline.memberCount) - 1
	if _, err := computeMakemigrationsBuildInputFingerprintWithLimits(fixture.project, document, limits); err == nil {
		t.Fatal("oversized member roster was accepted")
	}
}

func newMakemigrationsFingerprintFixture(t *testing.T) makemigrationsFingerprintFixture {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	if err := os.MkdirAll(filepath.Join(root, "cmd", "site", "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "lib"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".godj"), 0o700); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		descriptorName:                  []byte("format_version = 1\n\n[project]\npackage = \"./cmd/site\"\n"),
		"go.mod":                        []byte("module example.invalid/project\n"),
		"go.sum":                        []byte("example.invalid/dependency v1.2.3 h1:fixture\n"),
		".godj/generated-manifest.json": []byte("{\"version\":1}\n"),
		"cmd/site/main.go":              []byte("package main\nvar value=1\n"),
		"cmd/site/cgo.go":               []byte("package main\n"),
		"cmd/site/native.c":             []byte("c"),
		"cmd/site/native.cc":            []byte("x"),
		"cmd/site/native.m":             []byte("m"),
		"cmd/site/native.h":             []byte("h"),
		"cmd/site/native.f":             []byte("f"),
		"cmd/site/native.s":             []byte("s"),
		"cmd/site/interface.swig":       []byte("w"),
		"cmd/site/interface.swigcxx":    []byte("W"),
		"cmd/site/object.syso":          []byte{0x00, 0xff, 0x01},
		"cmd/site/assets/page.txt":      []byte("embed-a"),
		"internal/lib/lib.go":           []byte("package lib\n"),
	}
	for relative, document := range files {
		writeMakemigrationsFixtureFile(t, root, relative, document, 0o640)
	}

	report := Report{}
	project, primary := selectProject(root, commandArguments{}, &report)
	if primary != nil {
		t.Fatalf("select project: %+v", primary)
	}
	t.Cleanup(func() { _ = project.close() })
	root = project.rootPath
	externalStandard := filepath.Clean(filepath.Join(parent, "goroot", "src", "fmt"))
	externalModule := filepath.Clean(filepath.Join(parent, "gomodcache", "dependency"))
	externalFramework := filepath.Clean(filepath.Join(parent, "godj-framework"))
	externalFrameworkReplace := filepath.Clean(filepath.Join(parent, "godj-framework-replace"))
	return makemigrationsFingerprintFixture{
		root:    root,
		project: project,
		files:   files,
		packages: []makemigrationsGoListPackage{
			{
				Dir:          filepath.Join(root, "cmd", "site"),
				ImportPath:   "example.invalid/project/cmd/site",
				Name:         "main",
				GoFiles:      []string{"main.go"},
				CgoFiles:     []string{"cgo.go"},
				CFiles:       []string{"native.c"},
				CXXFiles:     []string{"native.cc"},
				MFiles:       []string{"native.m"},
				HFiles:       []string{"native.h"},
				FFiles:       []string{"native.f"},
				SFiles:       []string{"native.s"},
				SwigFiles:    []string{"interface.swig"},
				SwigCXXFiles: []string{"interface.swigcxx"},
				SysoFiles:    []string{"object.syso"},
				EmbedFiles:   []string{"assets/page.txt"},
			},
			{
				Dir:        filepath.Join(root, "internal", "lib"),
				ImportPath: "example.invalid/project/internal/lib",
				Name:       "lib",
				GoFiles:    []string{"lib.go"},
			},
			{
				Dir: externalStandard, ImportPath: "fmt", Name: "fmt", Standard: true,
			},
			{
				Dir: externalModule, ImportPath: "example.invalid/dependency", Name: "dependency",
				Module: &makemigrationsGoListModule{Path: "example.invalid/dependency", Version: "v1.2.3", Dir: externalModule},
			},
			{
				Dir: externalFramework, ImportPath: "github.com/progresshans/godj/schema", Name: "schema",
				Module: &makemigrationsGoListModule{Path: makemigrationsFrameworkModulePath, Main: true, Dir: externalFramework},
			},
			{
				Dir: externalFrameworkReplace, ImportPath: "github.com/progresshans/godj/project", Name: "project",
				Module: &makemigrationsGoListModule{
					Path: makemigrationsFrameworkModulePath,
					Replace: &makemigrationsGoListModule{
						Path: "../godj", Dir: externalFrameworkReplace,
					},
				},
			},
		},
	}
}

func writeMakemigrationsFixtureFile(t *testing.T, root, relative string, document []byte, mode os.FileMode) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, document, mode); err != nil {
		t.Fatal(err)
	}
}

func encodeMakemigrationsGoList(t *testing.T, packages []makemigrationsGoListPackage) []byte {
	t.Helper()
	var document bytes.Buffer
	encoder := json.NewEncoder(&document)
	for _, listed := range packages {
		if err := encoder.Encode(listed); err != nil {
			t.Fatal(err)
		}
	}
	return document.Bytes()
}

func assertMakemigrationsFingerprintChanged(
	t *testing.T,
	project retainedProject,
	goListDocument []byte,
	baseline makemigrationsBuildInputFingerprint,
	member string,
) {
	t.Helper()
	changed, err := computeMakemigrationsBuildInputFingerprint(project, goListDocument)
	if err != nil {
		t.Fatalf("fingerprint after %s change: %v", member, err)
	}
	if changed.digest == baseline.digest {
		t.Fatalf("%s change preserved digest %x", member, changed.digest)
	}
}

func assertMakemigrationsFingerprintRejected(t *testing.T, project retainedProject, goListDocument []byte) {
	t.Helper()
	if got, err := computeMakemigrationsBuildInputFingerprint(project, goListDocument); err == nil {
		t.Fatalf("unsafe build input was accepted: %+v", got)
	}
}
