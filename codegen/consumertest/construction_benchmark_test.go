package codegen_test

import (
	"testing"

	"github.com/progresshans/godj/conformance/relationfixture/blog"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

var benchmarkGeneratedMutation orm.Mutation[models.Article]
var benchmarkGeneratedRelation ir.Field

func BenchmarkGeneratedConstruction(b *testing.B) {
	b.Run("Create", func(b *testing.B) {
		input := models.NewArticleCreate("value")
		b.ReportAllocs()
		for b.Loop() {
			benchmarkGeneratedMutation = input.BuildCreate()
		}
	})
	b.Run("Patch", func(b *testing.B) {
		input := models.ArticlePatch{}.WithTitle("value")
		b.ReportAllocs()
		for b.Loop() {
			benchmarkGeneratedMutation = input.BuildPatch(models.Article{})
		}
	})
	b.Run("RelationStorage", func(b *testing.B) {
		descriptor := blog.PostDescriptor{}
		var field ir.Field
		for _, candidate := range descriptor.Metadata().Fields {
			if candidate.Relation != nil {
				field = candidate
				break
			}
		}
		b.ReportAllocs()
		for b.Loop() {
			storage, found := descriptor.BindRelationStorage(field)
			if !found {
				b.Fatal("relation storage missing")
			}
			benchmarkGeneratedRelation = storage.Field()
		}
	})
}
