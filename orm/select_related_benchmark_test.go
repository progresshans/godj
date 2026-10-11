package orm

import (
	"github.com/progresshans/godj/query"
	"testing"
)

var benchmarkRelatedCache *RelatedObject[relationObjectTestAuthor]

func BenchmarkForwardSelectedReadyCache(b *testing.B) {
	eager := requiredSelectQuery(b, &selectRelatedBackend{})
	prepared := eager.targets[0].(preparedRelatedTarget[relationObjectTestPost, relationObjectTestAuthor])
	target, err := (typedProjectedRelatedTarget[relationObjectTestPost, relationObjectTestAuthor]{target: prepared, value: relationObjectTestAuthor{ID: 1, Name: "Ada"}, key: query.Integer(1), presence: ProjectionPresent}).validate(relationObjectTestPost{ID: 10, AuthorID: 1}, selectedCardinality{}, "")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkRelatedCache = target.relatedObject(eager.Backend()).(*RelatedObject[relationObjectTestAuthor])
	}
}
