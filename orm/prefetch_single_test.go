package orm

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestSinglePrefetchBatchesUniqueKeysAndRejectsForeignRows(t *testing.T) {
	post, _, required, _ := bindRelationObjectTestFixture(t)
	for _, mode := range []string{"batch", "foreign", "duplicate", "missing"} {
		t.Run(mode, func(t *testing.T) {
			backend := &relationObjectAuthorBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
				keys, ok := plan.Conditions()[0].Values()
				if !ok || len(keys) > manyPrefetchBatchSize {
					t.Fatal("unbounded target batch")
				}
				rows := make([]relationObjectTestAuthor, len(keys))
				for i, key := range keys {
					id, _ := key.Integer()
					rows[i] = relationObjectTestAuthor{ID: id, Name: "parent"}
				}
				switch mode {
				case "foreign":
					rows[0].ID = -1
				case "duplicate":
					rows = append(rows, rows[0])
				case "missing":
					rows = nil
				}
				return &relationObjectAuthorRows{values: rows}, nil
			}}
			values := make([]relatedSelectedValue[relationObjectTestPost], 1002)
			for i := range values {
				values[i].source = relationObjectTestPost{ID: int64(i + 1), AuthorID: int64(i%1000 + 1)}
			}
			remaining := MaximumRelatedSelectionNodes
			p, err := PrefetchRequiredForward(required).preparePrefetch(1, &remaining)
			if err != nil {
				t.Fatal(err)
			}
			err = p.apply(t.Context(), backend, values)
			if mode == "foreign" || mode == "duplicate" {
				code := query.CodeRelatedObjectProjection
				if mode == "duplicate" {
					code = query.CodeRelatedObjectCardinality
				}
				if !errors.Is(err, &query.Error{Code: code}) || backend.callCount() != 1 {
					t.Fatal("corrupt first batch was not rejected by its own invariant", err, backend.callCount())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if backend.callCount() != 2 {
				t.Fatalf("batch calls=%d", backend.callCount())
			}
			for _, value := range values {
				graph, err := cloneRelatedSelection(backend, post, post.objectDescriptor, value)
				if err != nil {
					t.Fatal(err)
				}
				relation, err := SelectRequiredForward(required).Related(graph)
				if err != nil {
					t.Fatal(err)
				}
				target, present, err := relation.Get(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if mode == "missing" {
					if present {
						t.Fatal("missing target fabricated")
					}
				} else if !present || target.ID != value.source.AuthorID {
					t.Fatal("target assigned to wrong owner")
				}
			}
			if backend.callCount() != 2 {
				t.Fatal("warm single targets performed I/O")
			}
		})
	}
}

func TestAbsentRelatedObjectPreservesBorrowedSessionLifetime(t *testing.T) {
	_, _, _, nullable := bindRelationObjectTestFixture(t)
	closed := errors.New("borrowed session expired")
	for _, origin := range []string{"lazy", "prefetch", "eager"} {
		t.Run(origin, func(t *testing.T) {
			backend := &sessionCacheBackend{allowed: -1, closed: closed}
			var related *RelatedObject[relationObjectTestAuthor]
			if origin == "lazy" {
				var err error
				related, err = nullable.From(backend, relationObjectTestPost{ID: 1, AuthorID: 1})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				// Both eager and prefetch materializations use the sealed cache bridge.
				cache := typedCachedRelatedTarget[relationObjectTestAuthor]{}
				if origin == "prefetch" {
					remaining := MaximumRelatedSelectionNodes
					p, err := PrefetchNullableForward(nullable).preparePrefetch(1, &remaining)
					if err != nil {
						t.Fatal(err)
					}
					values := []relatedSelectedValue[relationObjectTestPost]{{source: relationObjectTestPost{ID: 1, AuthorID: 1}}}
					if err := p.apply(t.Context(), backend, values); err != nil {
						t.Fatal(err)
					}
					cache = values[0].targets[0].(typedCachedRelatedTarget[relationObjectTestAuthor])
				}
				related = cache.relatedObject(backend).(*RelatedObject[relationObjectTestAuthor])
			}
			if _, present, err := related.Get(t.Context()); err != nil || present {
				t.Fatal("live NULL relation", err)
			}
			backend.allowed = 0
			if _, _, err := related.Get(t.Context()); !errors.Is(err, closed) {
				t.Fatal("expired absent Get", err)
			}
			if _, err := related.Fresh(); !errors.Is(err, closed) {
				t.Fatal("expired absent Fresh", err)
			}
			if _, _, err := related.SelectedGraph(t.Context()); !errors.Is(err, closed) {
				t.Fatal("expired absent graph", err)
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, _, err := related.Get(canceled); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation precedence", err)
			}
			if backend.queries != 0 {
				t.Fatal("NULL relation performed I/O")
			}
		})
	}
}
