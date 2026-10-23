package orm

import (
	"context"
	"errors"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"sync"
	"testing"
)

func (relationObjectTestPostDescriptor) PrimaryKey(value relationObjectTestPost) (query.Value, bool) {
	return query.Integer(value.ID), value.ID != 0
}

func TestReverseCollectionPrefetchChunksAndPublishesAfterAllBatches(t *testing.T) {
	relation := bindReverseObjectTestRelation(t, "posts")
	for _, mode := range []string{"batch", "foreign", "second_failure"} {
		t.Run(mode, func(t *testing.T) {
			sentinel := errors.New("second batch failed")
			backend := &reversePostBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
				if mode == "second_failure" && call == 1 {
					return nil, sentinel
				}
				keys, ok := plan.Conditions()[0].Values()
				if !ok || len(keys) > 999 {
					t.Fatal("unbounded reverse batch")
				}
				rows := make([]relationObjectTestPost, len(keys))
				for i, key := range keys {
					id, _ := key.Integer()
					rows[i] = relationObjectTestPost{ID: id * 10, Title: "child", AuthorID: id}
				}
				if mode == "foreign" {
					rows[0].AuthorID = -1
				}
				return &reversePostRows{values: rows}, nil
			}}
			remaining := MaximumRelatedSelectionNodes
			p, err := relation.WithChildren().preparePrefetch(1, &remaining)
			if err != nil {
				t.Fatal(err)
			}
			owners := make([]relatedSelectedValue[relationObjectTestAuthor], 1002)
			for i := range owners {
				owners[i].source = relationObjectTestAuthor{ID: int64(i%1000 + 1), Name: "owner"}
			}
			err = p.apply(t.Context(), backend, owners)
			if mode != "batch" {
				if err == nil {
					t.Fatal("failed batch accepted")
				}
				if mode == "second_failure" && !errors.Is(err, sentinel) {
					t.Fatal(err)
				}
				for _, owner := range owners {
					if len(owner.collections) != 0 {
						t.Fatal("partial reverse caches published")
					}
				}
				if mode == "foreign" && backend.callCount() != 1 {
					t.Fatal("foreign row not rejected in its batch")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if backend.callCount() != 2 {
				t.Fatal("reverse keys not grouped into two batches")
			}
			for _, owner := range owners {
				graph, err := cloneRelatedSelection(backend, relation.state.owner, relation.state.ownerDescriptor, owner)
				if err != nil {
					t.Fatal(err)
				}
				set, present, err := relation.FromPrefetched(graph)
				if err != nil || !present {
					t.Fatal(err)
				}
				rows, err := set.All(t.Context())
				if err != nil || len(rows) != 1 || rows[0].AuthorID != owner.source.ID {
					t.Fatal("wrong owner grouping", err)
				}
			}
			if backend.callCount() != 2 {
				t.Fatal("warm reverse grouping queried backend")
			}
		})
	}
}

func TestRelatedSetSnapshotSurvivesConcurrentInvalidation(t *testing.T) {
	relation := bindReverseObjectTestRelation(t, "posts")
	backend := &reversePostBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		t.Error("binding did I/O")
		return &reversePostRows{}, nil
	}}
	set, err := relation.From(backend, relationObjectTestAuthor{ID: 1, Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	held, err := set.Query()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if err := set.Invalidate(); err != nil {
					t.Error(err)
				}
				q, err := set.Query()
				if err != nil {
					t.Error(err)
					return
				}
				if !q.Plan().Equal(held.Plan()) {
					t.Error("default owner scope changed")
				}
			}
		}()
	}
	wg.Wait()
	if backend.callCount() != 0 {
		t.Fatal("cache binding did I/O")
	}
}
