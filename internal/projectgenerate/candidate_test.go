//go:build darwin || linux

package projectgenerate

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/codegen"
)

func TestGoCandidateVerifierCompilesVirtualNewPackagesWithoutExecutingUserCode(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "module root"
		if nested {
			name = "nested project root"
		}
		t.Run(name, func(t *testing.T) {
			moduleRoot := t.TempDir()
			projectRoot := moduleRoot
			spec := projectGenerateTestSpec()
			if nested {
				projectRoot = filepath.Join(moduleRoot, "article")
				if err := os.Mkdir(projectRoot, 0o755); err != nil {
					t.Fatal(err)
				}
				prefix := "example.com/godj-project-bundle/article/"
				spec.Project.ImportPath = prefix + spec.Project.Directory
				for index := range spec.Apps {
					spec.Apps[index].Package.ImportPath = prefix + spec.Apps[index].Package.Directory
				}
			}
			bundle, err := codegen.GenerateProject(spec)
			if err != nil {
				t.Fatalf("GenerateProject() error = %v", err)
			}
			writeProjectGenerateTestFile(t, moduleRoot, "go.mod", projectGenerateModuleFile(t), 0o644)
			consumerImport := spec.Project.ImportPath
			writeProjectGenerateTestFile(t, projectRoot, "consumer/consumer.go", []byte(fmt.Sprintf(`package consumer

import project %q

var _ = project.GoDjProjectRelationFacadeGeneratorVersion
`, consumerImport)), 0o644)

			markerRoot := t.TempDir()
			initMarker := filepath.Join(markerRoot, "init-ran")
			testMainMarker := filepath.Join(markerRoot, "testmain-ran")
			writeProjectGenerateTestFile(t, projectRoot, "hooks/hooks.go", []byte(fmt.Sprintf(`package hooks

import "os"

func init() { _ = os.WriteFile(%q, []byte("ran"), 0600) }
`, initMarker)), 0o644)
			writeProjectGenerateTestFile(t, projectRoot, "hooks/hooks_test.go", []byte(fmt.Sprintf(`package hooks

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	_ = os.WriteFile(%q, []byte("ran"), 0600)
	os.Exit(m.Run())
}
`, testMainMarker)), 0o644)

			if !nested {
				writeRetiredProjectGenerateManifestAndFile(t, projectRoot, bundle)
			}
			stageRoot := t.TempDir()
			writeProjectGenerateTestBundle(t, stageRoot, bundle)
			verifier, err := NewGoCandidateVerifier(projectRoot, bundle)
			if err != nil {
				t.Fatalf("NewGoCandidateVerifier() error = %v", err)
			}
			before := snapshotProjectGenerateTestTree(t, moduleRoot)
			if err := verifier.Verify(context.Background(), stageRoot); err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			after := snapshotProjectGenerateTestTree(t, moduleRoot)
			if strings.Join(before, "\n") != strings.Join(after, "\n") {
				t.Fatalf("Verify mutated project tree\nbefore=%v\nafter=%v", before, after)
			}
			for _, marker := range []string{initMarker, testMainMarker} {
				if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("compile-only verifier executed user code and created %s", marker)
				}
			}
			for _, file := range bundle.Files() {
				if _, err := os.Lstat(filepath.Join(projectRoot, filepath.FromSlash(file.Path))); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("virtual candidate path %q was published during verification", file.Path)
				}
			}
		})
	}
}

func TestGoCandidateVerifierRejectsChangedConsumerAndStageWithoutMutation(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	writeProjectGenerateTestFile(t, root, "go.mod", projectGenerateModuleFile(t), 0o644)
	writeProjectGenerateTestFile(t, root, "consumer/consumer.go", []byte(`package consumer

import project "example.com/godj-project-bundle/project"

var _ = project.SymbolThatDoesNotExist
`), 0o644)
	stage := t.TempDir()
	writeProjectGenerateTestBundle(t, stage, bundle)
	verifier, err := NewGoCandidateVerifier(root, bundle)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotProjectGenerateTestTree(t, root)
	if err := verifier.Verify(context.Background(), stage); !errors.Is(err, ErrCandidateVerification) {
		t.Fatalf("Verify(broken consumer) error = %v", err)
	}
	if after := snapshotProjectGenerateTestTree(t, root); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatal("failed consumer verification mutated project")
	}

	file := bundle.Files()[0]
	writeProjectGenerateTestFile(t, stage, file.Path, []byte("package tampered\n"), 0o644)
	if err := verifier.Verify(context.Background(), stage); !errors.Is(err, ErrCandidateVerification) ||
		!strings.Contains(err.Error(), "staged file") {
		t.Fatalf("Verify(tampered stage) error = %v", err)
	}
}

func TestGoCandidateVerifierRejectsFIFOsWithoutBlocking(t *testing.T) {
	bundle := projectGenerateTestBundle(t)
	tests := []struct {
		name  string
		setup func(*testing.T, string, string)
	}{
		{name: "stage manifest", setup: func(t *testing.T, _, stage string) {
			replaceProjectGenerateTestFileWithFIFO(t, stage, generatedManifestRelativePath)
		}},
		{name: "stage generated file", setup: func(t *testing.T, _, stage string) {
			replaceProjectGenerateTestFileWithFIFO(t, stage, bundle.Files()[0].Path)
		}},
		{name: "prior manifest", setup: func(t *testing.T, root, _ string) {
			replaceProjectGenerateTestFileWithFIFO(t, root, generatedManifestRelativePath)
		}},
		{name: "target generated file", setup: func(t *testing.T, root, _ string) {
			replaceProjectGenerateTestFileWithFIFO(t, root, bundle.Files()[0].Path)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			stage := t.TempDir()
			writeProjectGenerateTestFile(t, root, "go.mod", projectGenerateModuleFile(t), 0o644)
			writeProjectGenerateTestBundle(t, stage, bundle)
			test.setup(t, root, stage)
			verifier, err := NewGoCandidateVerifier(root, bundle)
			if err != nil {
				t.Fatal(err)
			}
			beforeRoot := snapshotProjectGenerateTestTree(t, root)
			beforeStage := snapshotProjectGenerateTestTree(t, stage)
			err = verifyProjectGenerateWithin(t, verifier, stage, 2*time.Second)
			requireErrorIs(t, err, ErrCandidateVerification)
			if after := snapshotProjectGenerateTestTree(t, root); strings.Join(beforeRoot, "\n") != strings.Join(after, "\n") {
				t.Fatalf("Verify(FIFO) mutated project\nbefore=%v\nafter=%v", beforeRoot, after)
			}
			if after := snapshotProjectGenerateTestTree(t, stage); strings.Join(beforeStage, "\n") != strings.Join(after, "\n") {
				t.Fatalf("Verify(FIFO) mutated stage\nbefore=%v\nafter=%v", beforeStage, after)
			}
		})
	}
}

func TestCandidateCommandEnvironmentSanitizesGoControlsAndUsesExternalPaths(t *testing.T) {
	projectRoot := filepath.Join(string(filepath.Separator), "project")
	workspace := filepath.Join(string(filepath.Separator), "private", "candidate")
	environment := candidateCommandEnvironment(projectRoot, workspace, []string{
		"PATH=/bin", "GOFLAGS=-run=evil", "GOWORK=/project/go.work", "GOTOOLCHAIN=evil", "GOENV=/project/go.env",
		"GOCACHE=/project/cache", "GOTMPDIR=/project/tmp", "GOMODCACHE=/project/mod", "GOPATH=/project/gopath",
	})
	values := make(map[string]string)
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	for key, want := range map[string]string{"GOFLAGS": "", "GOWORK": "off", "GOTOOLCHAIN": "local", "GOENV": "off"} {
		if values[key] != want {
			t.Fatalf("%s = %q, want %q", key, values[key], want)
		}
	}
	for _, key := range []string{"GOCACHE", "GOTMPDIR", "TMPDIR", "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "GOPATH", "GOMODCACHE"} {
		if sameOrDescendantPath(values[key], projectRoot) {
			t.Fatalf("%s remains inside project: %q", key, values[key])
		}
	}
	if want := filepath.Join(workspace, "gomodcache"); values["GOMODCACHE"] != want {
		t.Fatalf("GOMODCACHE = %q, want private %q", values["GOMODCACHE"], want)
	}
}

func TestCandidateCommandEnvironmentRejectsAmbientCacheSymlinkIntoProject(t *testing.T) {
	projectRoot := t.TempDir()
	workspace := t.TempDir()
	projectCache := filepath.Join(projectRoot, "module-cache")
	if err := os.MkdirAll(filepath.Join(projectCache, "cache", "download"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkRoot := t.TempDir()
	ambientLink := filepath.Join(linkRoot, "ambient-cache")
	if err := os.Symlink(projectCache, ambientLink); err != nil {
		t.Fatal(err)
	}
	environment := candidateCommandEnvironment(projectRoot, workspace, []string{
		"PATH=/bin", "GOMODCACHE=" + ambientLink, "GOPROXY=off",
	})
	values := make(map[string]string)
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	wantCache := filepath.Join(workspace, "gomodcache")
	if values["GOMODCACHE"] != wantCache {
		t.Fatalf("GOMODCACHE = %q, want private %q", values["GOMODCACHE"], wantCache)
	}
	for _, forbidden := range []string{filepath.ToSlash(ambientLink), filepath.ToSlash(projectCache)} {
		if strings.Contains(values["GOPROXY"], forbidden) {
			t.Fatalf("GOPROXY exposes cache inside project: %q", values["GOPROXY"])
		}
	}
	if err := os.MkdirAll(values["GOMODCACHE"], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(values["GOMODCACHE"], "probe"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(projectCache, "probe")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private cache write reached project: %v", err)
	}
}

func TestGoCandidateVerifierUsesAmbientCacheOnlyAsOfflineFileProxy(t *testing.T) {
	const (
		modulePath = "example.com/godj-offline"
		version    = "v1.0.0"
	)
	sourceProxy := t.TempDir()
	writeProjectGenerateTestModuleProxy(t, sourceProxy, modulePath, version)
	ambientCache := t.TempDir()
	t.Cleanup(func() { makeProjectGenerateTestTreeWritable(t, ambientCache) })
	seedRoot := t.TempDir()
	writeProjectGenerateTestFile(t, seedRoot, "go.mod", []byte("module example.com/seed\n\ngo 1.26.0\n"), 0o644)
	seedWorkspace := t.TempDir()
	for _, name := range []string{"home", "gocache", "gotmp", "tmp", "gopath", "xdg-config", "xdg-cache"} {
		if err := os.Mkdir(filepath.Join(seedWorkspace, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("go", "mod", "download", "-json", modulePath+"@"+version)
	command.Dir = seedRoot
	command.Env = projectGenerateTestCommandEnvironment(map[string]string{
		"GOENV":           "off",
		"GOWORK":          "off",
		"GOTOOLCHAIN":     "local",
		"GOSUMDB":         "off",
		"GOMODCACHE":      ambientCache,
		"GOPATH":          filepath.Join(seedWorkspace, "gopath"),
		"GOPROXY":         projectGenerateTestFileProxyURL(sourceProxy),
		"HOME":            filepath.Join(seedWorkspace, "home"),
		"GOCACHE":         filepath.Join(seedWorkspace, "gocache"),
		"GOTMPDIR":        filepath.Join(seedWorkspace, "gotmp"),
		"TMPDIR":          filepath.Join(seedWorkspace, "tmp"),
		"XDG_CONFIG_HOME": filepath.Join(seedWorkspace, "xdg-config"),
		"XDG_CACHE_HOME":  filepath.Join(seedWorkspace, "xdg-cache"),
	})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("seed offline module cache: %v\n%s", err, output)
	}
	var downloaded struct {
		Sum      string
		GoModSum string
		Error    string
	}
	if err := json.Unmarshal(output, &downloaded); err != nil || downloaded.Error != "" || downloaded.Sum == "" || downloaded.GoModSum == "" {
		t.Fatalf("decode seeded module: value=%#v error=%v output=%s", downloaded, err, output)
	}

	root := t.TempDir()
	stage := t.TempDir()
	bundle := projectGenerateTestBundle(t)
	goMod := fmt.Sprintf(`module example.com/godj-project-bundle

go 1.26.0

require (
	github.com/progresshans/godj v0.0.0
	%s %s
)

replace github.com/progresshans/godj => %s
`, modulePath, version, filepath.ToSlash(projectGenerateRepositoryRoot(t)))
	writeProjectGenerateTestFile(t, root, "go.mod", []byte(goMod), 0o644)
	goSum := fmt.Sprintf("%s %s %s\n%s %s/go.mod %s\n", modulePath, version, downloaded.Sum, modulePath, version, downloaded.GoModSum)
	writeProjectGenerateTestFile(t, root, "go.sum", []byte(goSum), 0o644)
	writeProjectGenerateTestFile(t, root, "consumer/consumer.go", []byte(fmt.Sprintf(`package consumer

import (
	offline %q
	project "example.com/godj-project-bundle/project"
)

var _ = offline.Value
var _ = project.GoDjProjectRelationFacadeGeneratorVersion
`, modulePath)), 0o644)
	writeProjectGenerateTestBundle(t, stage, bundle)
	t.Setenv("GOMODCACHE", ambientCache)
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	verifier, err := NewGoCandidateVerifier(root, bundle)
	if err != nil {
		t.Fatal(err)
	}
	beforeRoot := snapshotProjectGenerateTestTree(t, root)
	beforeAmbient := snapshotProjectGenerateTestTree(t, ambientCache)
	if err := verifier.Verify(context.Background(), stage); err != nil {
		t.Fatalf("Verify(offline ambient file proxy) error = %v", err)
	}
	if after := snapshotProjectGenerateTestTree(t, root); strings.Join(beforeRoot, "\n") != strings.Join(after, "\n") {
		t.Fatalf("offline candidate mutated project\nbefore=%v\nafter=%v", beforeRoot, after)
	}
	if after := snapshotProjectGenerateTestTree(t, ambientCache); strings.Join(beforeAmbient, "\n") != strings.Join(after, "\n") {
		t.Fatalf("offline candidate wrote ambient cache\nbefore=%v\nafter=%v", beforeAmbient, after)
	}
}

func TestGoCandidateVerifierDisablesVCSExecutionAndPreservesGitTree(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	bundle := projectGenerateTestBundle(t)
	root := t.TempDir()
	stage := t.TempDir()
	writeProjectGenerateTestFile(t, root, "go.mod", projectGenerateModuleFile(t), 0o644)
	writeProjectGenerateTestFile(t, root, "consumer/consumer.go", []byte(`package consumer

import project "example.com/godj-project-bundle/project"

var _ = project.GoDjProjectRelationFacadeGeneratorVersion
`), 0o644)
	writeProjectGenerateTestFile(t, root, "cmd/server/main.go", []byte("package main\n\nfunc main() {}\n"), 0o644)
	writeProjectGenerateTestBundle(t, stage, bundle)
	initCommand := exec.Command(realGit, "init", "--quiet")
	initCommand.Dir = root
	if output, err := initCommand.CombinedOutput(); err != nil {
		t.Fatalf("initialize Git-backed candidate: %v\n%s", err, output)
	}

	fakeBin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "git-invoked")
	fakeGit := filepath.Join(fakeBin, "git")
	if err := os.WriteFile(fakeGit, []byte("#!/bin/sh\n: > \"$GODJ_GIT_MARKER\"\nexit 97\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GODJ_GIT_MARKER", marker)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	verifier, err := NewGoCandidateVerifier(root, bundle)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotProjectGenerateTestTree(t, root)
	if err := verifier.Verify(context.Background(), stage); err != nil {
		t.Fatalf("Verify(Git-backed project) error = %v", err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate invoked Git despite -buildvcs=false: %v", err)
	}
	if after := snapshotProjectGenerateTestTree(t, root); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("Git-backed candidate mutated project\nbefore=%v\nafter=%v", before, after)
	}
}

func writeRetiredProjectGenerateManifestAndFile(t *testing.T, root string, bundle codegen.GeneratedBundle) {
	t.Helper()
	manifest, err := decodeCommittedManifest(bundle.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	retiredPath := "retired/zz_godj_retired.go"
	retiredSource := []byte("this is deliberately invalid Go source\n")
	manifest.Apps = append(manifest.Apps, manifestApp{
		Alias: "retired", AppLabel: "retired",
		Package:      manifestPackage{PackageName: "retired", ImportPath: "example.com/godj-project-bundle/retired", Directory: "retired"},
		SchemaSHA256: strings.Repeat("5", 64),
	})
	sort.Slice(manifest.Apps, func(left, right int) bool { return manifestAppLess(manifest.Apps[left], manifest.Apps[right]) })
	manifest.Files = append(manifest.Files, manifestFile{
		Path: retiredPath, Owner: "app:retired", Mode: "0644", SHA256: sha256Hex(retiredSource),
	})
	sort.Slice(manifest.Files, func(left, right int) bool { return manifest.Files[left].Path < manifest.Files[right].Path })
	writeProjectGenerateTestFile(t, root, generatedManifestRelativePath, encodeProjectGenerateTestManifest(t, manifest), 0o644)
	writeProjectGenerateTestFile(t, root, retiredPath, retiredSource, 0o644)
}

func verifyProjectGenerateWithin(
	t *testing.T,
	verifier CandidateVerifier,
	stage string,
	maximum time.Duration,
) error {
	t.Helper()
	completed := make(chan error, 1)
	go func() { completed <- verifier.Verify(context.Background(), stage) }()
	select {
	case err := <-completed:
		return err
	case <-time.After(maximum):
		t.Fatalf("Verify did not reject a non-regular path within %s", maximum)
		return nil
	}
}

func writeProjectGenerateTestModuleProxy(t *testing.T, root, modulePath, version string) {
	t.Helper()
	versionRoot := filepath.Join(root, filepath.FromSlash(modulePath), "@v")
	if err := os.MkdirAll(versionRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectGenerateTestFile(t, root, filepath.ToSlash(filepath.Join(modulePath, "@v", "list")), []byte(version+"\n"), 0o644)
	writeProjectGenerateTestFile(t, root, filepath.ToSlash(filepath.Join(modulePath, "@v", version+".info")),
		[]byte(fmt.Sprintf("{\"Version\":%q,\"Time\":\"2026-01-01T00:00:00Z\"}\n", version)), 0o644)
	writeProjectGenerateTestFile(t, root, filepath.ToSlash(filepath.Join(modulePath, "@v", version+".mod")),
		[]byte("module "+modulePath+"\n\ngo 1.26.0\n"), 0o644)
	archivePath := filepath.Join(versionRoot, version+".zip")
	archive, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	entry, err := writer.Create(modulePath + "@" + version + "/offline.go")
	if err == nil {
		_, err = entry.Write([]byte("package offline\n\nconst Value = true\n"))
	}
	closeZipErr := writer.Close()
	closeFileErr := archive.Close()
	if err != nil || closeZipErr != nil || closeFileErr != nil {
		t.Fatalf("write module proxy zip: write=%v zip-close=%v file-close=%v", err, closeZipErr, closeFileErr)
	}
}

func projectGenerateTestFileProxyURL(root string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(root)}).String()
}

func projectGenerateTestCommandEnvironment(overrides map[string]string) []string {
	values := make(map[string]string, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, len(keys))
	for index, key := range keys {
		result[index] = key + "=" + values[key]
	}
	return result
}

func makeProjectGenerateTestTreeWritable(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		mode := os.FileMode(0o600)
		if entry.IsDir() {
			mode = 0o700
		}
		return os.Chmod(filename, mode)
	}); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Errorf("make temporary module cache writable: %v", err)
	}
}
