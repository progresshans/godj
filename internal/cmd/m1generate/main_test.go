package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/examples/article/modeldef"
	"github.com/progresshans/godj/schema"
)

func TestProductionVerifierCompilesOnlyAndPreservesLastGood(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	project := t.TempDir()
	modelsDirectory := filepath.Join(project, "models")
	if err := os.Mkdir(modelsDirectory, 0o755); err != nil {
		t.Fatalf("create models directory: %v", err)
	}
	writeFixture(t, filepath.Join(project, "go.mod"), fmt.Sprintf(`module example.com/m1generate-fixture

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, filepath.ToSlash(root)))

	validSchema, err := modeldef.Schema()
	if err != nil {
		t.Fatalf("modeldef.Schema() error = %v", err)
	}
	validSource, err := codegen.Generate("models", validSchema)
	if err != nil {
		t.Fatalf("Generate(valid) error = %v", err)
	}
	target := filepath.Join(modelsDirectory, "zz_godj_generated.go")
	writeFixture(t, target, string(validSource))

	initMarker := filepath.Join(project, "init-ran")
	testMainMarker := filepath.Join(project, "testmain-ran")
	writeFixture(t, filepath.Join(modelsDirectory, "article.go"), fmt.Sprintf(`package models

import "os"

func init() {
	_ = os.WriteFile(%s, []byte("ran"), 0o644)
}

func (article Article) Heading() string { return article.Title }
`, strconv.Quote(initMarker)))
	writeFixture(t, filepath.Join(modelsDirectory, "article_test.go"), fmt.Sprintf(`package models

import (
	"os"
	"testing"
)

func TestMain(main *testing.M) {
	_ = os.WriteFile(%s, []byte("ran"), 0o644)
	os.Exit(main.Run())
}
`, strconv.Quote(testMainMarker)))

	verify := func(ctx context.Context, candidate string) error {
		return verifyTargetPackage(ctx, project, "./models", target, candidate)
	}
	if err := verify(context.Background(), target); err != nil {
		t.Fatalf("verifyTargetPackage(valid) error = %v", err)
	}
	for _, marker := range []string{initMarker, testMainMarker} {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("compile-only verification executed target code; marker %s stat error = %v", marker, err)
		}
	}

	staleSchema, err := schema.Build(schema.Definition{
		AppLabel: "godj_conformance",
		Models: []schema.Model{{
			Name:   "article",
			GoName: "Article",
			Fields: []schema.Field{
				schema.BooleanField("published", "Published"),
				schema.CharField("summary", "Summary", 200, schema.Nullable()),
			},
		}},
	})
	if err != nil {
		t.Fatalf("Build(stale) error = %v", err)
	}
	staleSource, err := codegen.Generate("models", staleSchema)
	if err != nil {
		t.Fatalf("Generate(stale) error = %v", err)
	}
	err = codegen.WriteFile(context.Background(), target, staleSource, codegen.WriteOptions{Verify: verify})
	if err == nil {
		t.Fatal("WriteFile() replaced output that breaks user model source")
	}
	lastGood, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read last-good output: %v", readErr)
	}
	if !bytes.Equal(lastGood, validSource) {
		t.Fatal("failed target compile changed last-good generated output")
	}

	if err := os.Remove(target); err != nil {
		t.Fatalf("remove generated target: %v", err)
	}
	if err := codegen.WriteFile(context.Background(), target, validSource, codegen.WriteOptions{Verify: verify}); err != nil {
		t.Fatalf("WriteFile() could not recover a missing generated target: %v", err)
	}
	recovered, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read recovered generated output: %v", err)
	}
	if !bytes.Equal(recovered, validSource) {
		t.Fatal("recovered generated output differs from candidate")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
}

func writeFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
