package orm_test

import (
	"testing"

	"github.com/progresshans/godj/internal/testschema"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

func TestProjectSourceDigestCoversHostPoliciesAndOwnedDeclarations(t *testing.T) {
	if digest, err := (orm.ProjectBinding{}).SourceSHA256(); err == nil || digest != "" {
		t.Fatal("unbound source gained an identity")
	}
	authors, blog := testschema.Relation()
	build := func(schemas ...ir.Schema) (orm.ProjectBinding, string) {
		t.Helper()
		bound, err := orm.BindProject(schemas...)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := bound.SourceSHA256()
		if err != nil || len(digest) != 64 {
			t.Fatal("invalid source digest", err)
		}
		return bound, digest
	}
	bound, original := build(authors, blog)
	_, permuted := build(blog, authors)
	if permuted != original {
		t.Fatal("independent canonical binding changed source")
	}
	blog.Models[0].Fields[1].Relation.OnDelete = ir.DeleteCascade
	_, changed := build(authors, blog)
	if changed == original {
		t.Fatal("host incoming delete policy disappeared from source identity")
	}
	if digest, err := bound.SourceSHA256(); err != nil || digest != original {
		t.Fatal("caller-owned schema mutation changed existing source", err)
	}
	_, empty := build()
	if empty == original {
		t.Fatal("empty project inherited another source")
	}
}
