package orm

import (
	"github.com/progresshans/godj/query"
	"testing"
)

var benchmarkRelatedCache *RelatedObject[relationObjectTestAuthor]

func BenchmarkForwardSelectedReadyCache(b *testing.B) {
	eager := requiredSelectQuery(b, &selectRelatedBackend{})
	value := forwardSelectedValue[relationObjectTestPost, relationObjectTestAuthor]{
		target:    relationObjectTestAuthor{ID: 1, Name: "Ada"},
		targetKey: query.Integer(1), targetPresent: true,
	}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkRelatedCache = eager.readyRelated(value)
	}
}
