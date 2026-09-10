package orm

import (
	"context"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

var benchmarkPreparedQuery QuerySet[relationObjectTestPost]
var benchmarkPreparedManager Manager[relationObjectTestPost]
var benchmarkPrefetchedSets []*RelatedSet[relationObjectTestPost]

func BenchmarkManagerPreparation(b *testing.B) {
	b.Run("Using", func(b *testing.B) {
		manager := NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{})
		b.ReportAllocs()
		for b.Loop() {
			benchmarkPreparedQuery = manager.Using(nil)
		}
	})
	b.Run("NewManager", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			benchmarkPreparedManager = NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{})
		}
	})
}

func BenchmarkReversePrefetch(b *testing.B) {
	post, author, _, _ := bindRelationObjectTestFixture(b)
	reverse, err := BindReverseObject(author, "posts", post)
	if err != nil {
		b.Fatal(err)
	}
	prefetch, err := BindReversePrefetch(reverse)
	if err != nil {
		b.Fatal(err)
	}
	owners := make([]relationObjectTestAuthor, 20)
	values := make([]relationObjectTestPost, 1000)
	for index := range owners {
		owners[index].ID = int64(index + 1)
	}
	for index := range values {
		reviewer := int64(1)
		values[index] = relationObjectTestPost{ID: int64(index + 1), Title: "Post", AuthorID: int64(index%20 + 1), ReviewerID: &reviewer}
	}
	backend := prefetchBenchmarkBackend{values: values}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkPrefetchedSets, err = prefetch.Load(context.Background(), backend, owners)
		if err != nil {
			b.Fatal(err)
		}
	}
}

type prefetchBenchmarkBackend struct{ values []relationObjectTestPost }

func (backend prefetchBenchmarkBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	return &reversePostRows{values: backend.values}, nil
}
