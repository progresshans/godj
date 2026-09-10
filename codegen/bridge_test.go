package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/testschema"
)

func TestGenerateProjectBridgeIsCanonicalAndImportsOnlyAppsAndORM(t *testing.T) {
	t.Parallel()

	input := []codegen.BridgePackage{
		{Alias: "blog", ImportPath: "example.com/relation/blog/models"},
		{Alias: "authors", ImportPath: "example.com/relation/authors/models"},
	}
	first, err := codegen.GenerateProjectBridge("binding", input)
	if err != nil {
		t.Fatalf("GenerateProjectBridge() error = %v", err)
	}
	second, err := codegen.GenerateProjectBridge("binding", []codegen.BridgePackage{input[1], input[0]})
	if err != nil {
		t.Fatalf("GenerateProjectBridge() permuted error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("bridge input order changed bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	for _, fragment := range [][]byte{
		[]byte(`const GoDjProjectBindingGeneratorVersion = "godj-codegen-rel-project-v1"`),
		[]byte("func Bind() (orm.ProjectBinding, error)"),
		[]byte(`"github.com/progresshans/godj/orm"`),
		[]byte(`authors "example.com/relation/authors/models"`),
		[]byte(`blog "example.com/relation/blog/models"`),
		[]byte("authors.GoDjRelationSchema()"),
		[]byte("blog.GoDjRelationSchema()"),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("bridge source does not contain %q:\n%s", fragment, first)
		}
	}
	if bytes.Index(first, []byte("authors.GoDjRelationSchema()")) > bytes.Index(first, []byte("blog.GoDjRelationSchema()")) {
		t.Fatalf("bridge calls are not in canonical alias order:\n%s", first)
	}
	for _, forbidden := range [][]byte{
		[]byte("schema/ir"),
		[]byte("schema.Target"),
		[]byte("ForeignKeyRelation"),
		[]byte("encoding/json"),
	} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("bridge source contains forbidden relation duplication %q:\n%s", forbidden, first)
		}
	}

	input[0].Alias = "mutated"
	if bytes.Contains(first, []byte("mutated")) {
		t.Fatal("post-generation input mutation changed bridge candidate bytes")
	}
}

func TestGenerateProjectBridgeZeroProject(t *testing.T) {
	t.Parallel()

	generated, err := codegen.GenerateProjectBridge("binding", nil)
	if err != nil {
		t.Fatalf("GenerateProjectBridge() error = %v", err)
	}
	if !bytes.Contains(generated, []byte("return orm.BindProject()")) {
		t.Fatalf("zero bridge does not bind an empty project:\n%s", generated)
	}
}

func TestGenerateProjectBridgeRejectsInvalidOrAmbiguousImports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pkg      string
		packages []codegen.BridgePackage
	}{
		{name: "invalid package", pkg: "bad-package"},
		{name: "blank package identifier", pkg: "_"},
		{name: "invalid alias", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "bad-alias", ImportPath: "example.com/app"}}},
		{name: "blank alias", pkg: "binding", packages: []codegen.BridgePackage{{ImportPath: "example.com/app"}}},
		{name: "init alias", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "init", ImportPath: "example.com/app"}}},
		{name: "reserved orm alias", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "orm", ImportPath: "example.com/app"}}},
		{name: "fixed function collision", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "Bind", ImportPath: "example.com/app"}}},
		{name: "duplicate alias", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com/one"}, {Alias: "app", ImportPath: "example.com/two"}}},
		{name: "duplicate path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "one", ImportPath: "example.com/app"}, {Alias: "two", ImportPath: "example.com/app"}}},
		{name: "blank path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app"}}},
		{name: "space in path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com/bad path"}}},
		{name: "compiler punctuation in path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "unsafe!x"}}},
		{name: "replacement rune in path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com/\ufffd"}}},
		{name: "invalid utf8 in path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: string([]byte{0xff})}}},
		{name: "reserved go path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "go"}}},
		{name: "reserved type path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "type"}}},
		{name: "leading dash path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "-example/app"}}},
		{name: "empty path element", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com//app"}}},
		{name: "windows reserved element", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com/con.txt"}}},
		{name: "windows short name element", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com/app~1"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := codegen.GenerateProjectBridge(test.pkg, test.packages); err == nil {
				t.Fatal("GenerateProjectBridge() accepted invalid input")
			}
		})
	}
}

func TestPureByteGeneratorsDoNotWriteCommittedSentinelsOnValidationFailure(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	sentinelPath := filepath.Join(directory, "committed.go")
	sentinel := []byte("package committed\n\nconst LastGood = true\n")
	if err := os.WriteFile(sentinelPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	authors, blog := testschema.Relation()
	blog.Models[0].Fields[1].Relation.Target.AppLabel = "bad-target"
	if _, err := codegen.Generate("models", blog); err == nil {
		t.Fatal("Generate() accepted invalid relation candidate")
	}
	if _, err := codegen.GenerateRelationMetadata("models", blog); err == nil {
		t.Fatal("GenerateRelationMetadata() accepted invalid relation candidate")
	}
	if _, err := codegen.GenerateProjectBridge("binding", []codegen.BridgePackage{
		{Alias: "authors", ImportPath: "example.com/app"},
		{Alias: "authors", ImportPath: "example.com/other"},
	}); err == nil {
		t.Fatal("GenerateProjectBridge() accepted invalid candidate set")
	}
	_ = authors

	got, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatalf("pure-byte validation failure changed sentinel: %q", got)
	}
}
