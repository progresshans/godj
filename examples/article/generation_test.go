package article_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/examples/article/modeldef"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
)

var articleBundleRoster = []string{
	"models/zz_godj_generated.go",
	"models/zz_godj_relation.go",
	"models/zz_godj_relation_object.go",
	"models/zz_godj_relation_projection.go",
	"project/zz_godj_bindings.go",
	"project/zz_godj_relation_delete.go",
	"project/zz_godj_relation_facade.go",
	"project/zz_godj_relation_object.go",
	"project/zz_godj_relation_prefetch.go",
	"project/zz_godj_relation_query.go",
	"project/zz_godj_relation_reverse.go",
	"project/zz_godj_relation_select_related.go",
}

func TestCheckedInArticleRegeneratesExactProjectBundle(t *testing.T) {
	t.Parallel()
	spec, err := modeldef.ProjectSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	files := bundle.Files()
	if len(files) != 12 {
		t.Fatalf("article bundle file count = %d, want exact 12", len(files))
	}
	gotRoster := make([]string, len(files))
	for index := range files {
		gotRoster[index] = files[index].Path
	}
	if !reflect.DeepEqual(gotRoster, articleBundleRoster) {
		t.Fatalf("article bundle roster = %#v, want %#v", gotRoster, articleBundleRoster)
	}

	root := articleDirectory(t)
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		contents, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(contents, file.Source()) {
			t.Fatalf("checked-in article file %s differs from bundle: %v", file.Path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != file.Mode.Perm() {
			t.Fatalf("checked-in article file %s mode = %v, want regular %v", file.Path, info.Mode(), file.Mode.Perm())
		}
	}
	manifest, err := os.ReadFile(filepath.Join(root, codegen.GeneratedManifestPath))
	if err != nil || !bytes.Equal(manifest, bundle.Manifest()) {
		t.Fatalf("checked-in article manifest differs from bundle: %v", err)
	}

	regenerated, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	assertArticleBundlesEqual(t, bundle, regenerated)
	mutableFiles := bundle.Files()
	originalFirst := files[0].Source()
	mutableFiles[0].Path = "mutated"
	mutableSource := mutableFiles[0].Source()
	mutableSource[0] ^= 0xff
	mutableManifest := bundle.Manifest()
	mutableManifest[0] ^= 0xff
	freshFiles := bundle.Files()
	if freshFiles[0].Path != articleBundleRoster[0] || !bytes.Equal(freshFiles[0].Source(), originalFirst) || !bytes.Equal(bundle.Manifest(), manifest) {
		t.Fatal("article GeneratedBundle retained caller mutation")
	}

	var checkedRoster []string
	for _, directory := range []string{"models", "project"} {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "zz_godj_") && strings.HasSuffix(entry.Name(), ".go") {
				checkedRoster = append(checkedRoster, directory+"/"+entry.Name())
			}
		}
	}
	slices.Sort(checkedRoster)
	if !reflect.DeepEqual(checkedRoster, articleBundleRoster) {
		t.Fatalf("checked-in article inventory = %#v, want exact twelve %#v", checkedRoster, articleBundleRoster)
	}
}

func TestArticleDeclarationRunnerBootstrapsMissingAndBrokenGeneratedOutputs(t *testing.T) {
	repository := repositoryDirectory(t)
	const rootImport = "github.com/progresshans/godj/examples/article/"
	list := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./examples/article/cmd/projectrunner")
	list.Dir = repository
	list.Env = offlineGoEnvironment()
	output, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("list article declaration runner: %v\n%s", err, output)
	}
	dependencies := strings.Fields(string(output))
	for _, forbidden := range []string{rootImport + "models", rootImport + "project"} {
		if slices.Contains(dependencies, forbidden) {
			t.Fatalf("article declaration runner imports generated package %s", forbidden)
		}
	}

	generated := make([]string, 0, len(articleBundleRoster))
	for _, path := range articleBundleRoster {
		generated = append(generated, filepath.Join(repository, "examples", "article", filepath.FromSlash(path)))
	}
	t.Run("missing", func(t *testing.T) {
		replacements := make(map[string]string, len(generated))
		for _, path := range generated {
			replacements[path] = ""
		}
		assertArticleRunnerBuildAndSpec(t, repository, replacements)
	})
	t.Run("broken", func(t *testing.T) {
		broken := filepath.Join(t.TempDir(), "broken.go")
		if err := os.WriteFile(broken, []byte("package models\nfunc broken( {\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertArticleRunnerBuildAndSpec(t, repository, map[string]string{generated[0]: broken})
	})
}

func assertArticleRunnerBuildAndSpec(t *testing.T, repository string, replacements map[string]string) {
	t.Helper()
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	document, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: replacements})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlay, document, 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "projectrunner")
	build := exec.Command("go", "build", "-overlay="+overlay, "-o", binary, "./examples/article/cmd/projectrunner")
	build.Dir = repository
	build.Env = offlineGoEnvironment()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build declaration runner: %v\n%s", err, output)
	}

	command := exec.CommandContext(context.Background(), binary, projectgenerateprotocol.PrivateArgument)
	command.Stdin = bytes.NewReader(projectgenerateprotocol.RequestDocument())
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run declaration runner: %v; stderr=%q", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("declaration runner stderr = %q", stderr.String())
	}
	response, failure, failed := projectgenerateprotocol.ParseResponse(stdout.Bytes(), true)
	if failed || failure != (projectgenerateprotocol.Failure{}) || !response.OK || len(response.ProjectSpec.Apps) != 1 || response.ProjectSpec.Apps[0].Alias != "models" {
		t.Fatalf("declaration response = %+v, failure=%+v, failed=%v", response, failure, failed)
	}
}

func assertArticleBundlesEqual(t *testing.T, left, right codegen.GeneratedBundle) {
	t.Helper()
	if left.SnapshotSHA256() != right.SnapshotSHA256() || !bytes.Equal(left.Manifest(), right.Manifest()) {
		t.Fatal("article bundle snapshot or manifest is nondeterministic")
	}
	leftFiles := left.Files()
	rightFiles := right.Files()
	if len(leftFiles) != len(rightFiles) {
		t.Fatalf("article bundle file counts differ: %d != %d", len(leftFiles), len(rightFiles))
	}
	for index := range leftFiles {
		if leftFiles[index].Path != rightFiles[index].Path ||
			leftFiles[index].Owner != rightFiles[index].Owner ||
			leftFiles[index].SHA256 != rightFiles[index].SHA256 ||
			leftFiles[index].Mode != rightFiles[index].Mode ||
			!bytes.Equal(leftFiles[index].Source(), rightFiles[index].Source()) {
			t.Fatalf("article bundle file %d is nondeterministic", index)
		}
	}
}

func articleDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate article test source")
	}
	return filepath.Dir(source)
}

func repositoryDirectory(t *testing.T) string {
	t.Helper()
	return filepath.Clean(filepath.Join(articleDirectory(t), "..", ".."))
}

func offlineGoEnvironment() []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOWORK=") || strings.HasPrefix(entry, "GOPROXY=") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GOWORK=off", "GOPROXY=off")
}
