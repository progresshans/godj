package testfixture

import (
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func QueryPackages(authors, blog ir.Schema) []codegen.RelationQueryPackage {
	return []codegen.RelationQueryPackage{
		{Alias: "authors", ImportPath: "example.com/godj-relation-query-project/authors", Schema: authors},
		{Alias: "blog", ImportPath: "example.com/godj-relation-query-project/blog", Schema: blog},
	}
}

func FacadePackages(modulePath string, authors, blog ir.Schema) []codegen.RelationObjectPackage {
	return []codegen.RelationObjectPackage{
		{Alias: "authors", ImportPath: modulePath + "/authors", Schema: authors},
		{Alias: "blog", ImportPath: modulePath + "/blog", Schema: blog},
	}
}

func TargetSourcePackages(
	modulePath, targetAlias, sourceAlias string,
	authors, blog ir.Schema,
) []codegen.RelationObjectPackage {
	return []codegen.RelationObjectPackage{
		{Alias: targetAlias, ImportPath: modulePath + "/target", Schema: authors},
		{Alias: sourceAlias, ImportPath: modulePath + "/source", Schema: blog},
	}
}

func Generate(t *testing.T, label string, generate func() ([]byte, error)) []byte {
	t.Helper()
	result, err := generate()
	if err != nil {
		t.Fatalf("generate %s: %v", label, err)
	}
	return result
}
