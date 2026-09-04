package orm_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestPublicReversePrefetchSurfaceCompilesAndEvaluates(t *testing.T) {
	authors, blog := relationSchemas()
	binding, err := orm.BindProject(authors, blog)
	if err != nil {
		t.Fatalf("BindProject() error = %v", err)
	}
	owner, err := orm.BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		publicAuthorObjectDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(owner) error = %v", err)
	}
	source, err := orm.BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		publicPostObjectDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(source) error = %v", err)
	}
	reverse, err := orm.BindReverseObject(owner, "posts", source)
	if err != nil {
		t.Fatalf("BindReverseObject() error = %v", err)
	}
	prefetch, err := orm.BindReversePrefetch(reverse)
	if err != nil {
		t.Fatalf("BindReversePrefetch() error = %v", err)
	}
	var _ orm.ReversePrefetch[relationQueryAuthor, relationQueryPost] = prefetch
	_ = query.CodeRelatedSetMembership

	backend := &publicReversePrefetchBackend{}
	sets, err := prefetch.Load(context.Background(), backend, []relationQueryAuthor{
		{ID: 3, Name: "Cleo"},
		{ID: 1, Name: "Ada"},
		{ID: 2, Name: "Bob"},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(sets) != 3 || backend.callCount() != 1 {
		t.Fatalf("Load() = (sets=%d, calls=%d), want (3, 1)", len(sets), backend.callCount())
	}
	conditions := backend.plans[0].Conditions()
	if len(conditions) != 1 || conditions[0].Lookup() != query.LookupIn ||
		conditions[0].Field().Name() != "author" || conditions[0].Field().Column() != "author_id" {
		t.Fatalf("batch conditions = %#v", conditions)
	}
	values, ok := conditions[0].Values()
	if !ok || len(values) != 3 {
		t.Fatalf("batch Values() = (%#v, %v)", values, ok)
	}
	for index, want := range []int64{1, 2, 3} {
		got, integer := values[index].Integer()
		if !integer || got != want {
			t.Fatalf("batch value[%d] = (%d, %v), want %d", index, got, integer, want)
		}
	}

	wants := [][]int64{{12}, {10, 11}, {}}
	for index, set := range sets {
		posts, err := set.All(context.Background())
		if err != nil || len(posts) != len(wants[index]) {
			t.Fatalf("sets[%d].All() = (%#v, %v)", index, posts, err)
		}
		for postIndex := range posts {
			if posts[postIndex].ID != wants[index][postIndex] {
				t.Fatalf("sets[%d] post[%d] = %d, want %d", index, postIndex, posts[postIndex].ID, wants[index][postIndex])
			}
		}
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("warm All() calls = %d, want 1", got)
	}
	fresh, err := sets[1].Fresh()
	if err != nil {
		t.Fatalf("Fresh() error = %v", err)
	}
	if _, err := fresh.All(context.Background()); err != nil || backend.callCount() != 2 {
		t.Fatalf("fresh All() = (calls=%d, err=%v)", backend.callCount(), err)
	}
}

type publicReversePrefetchBackend struct {
	plans []query.Plan
}

func (backend *publicReversePrefetchBackend) Query(_ context.Context, plan query.Plan) (db.Rows, error) {
	backend.plans = append(backend.plans, plan)
	conditions := plan.Conditions()
	if len(conditions) != 1 {
		return nil, fmt.Errorf("conditions = %#v", conditions)
	}
	posts := []relationQueryPost{
		{ID: 10, AuthorID: 1},
		{ID: 11, AuthorID: 1},
		{ID: 12, AuthorID: 3},
	}
	switch conditions[0].Lookup() {
	case query.LookupIn:
		return &publicReversePostRows{values: posts}, nil
	case query.LookupExact:
		identifier, ok := conditions[0].Value().Integer()
		if !ok {
			return nil, fmt.Errorf("exact value = %#v", conditions[0].Value())
		}
		filtered := make([]relationQueryPost, 0)
		for _, post := range posts {
			if post.AuthorID == identifier {
				filtered = append(filtered, post)
			}
		}
		return &publicReversePostRows{values: filtered}, nil
	default:
		return nil, fmt.Errorf("lookup = %q", conditions[0].Lookup())
	}
}

func (backend *publicReversePrefetchBackend) callCount() int {
	return len(backend.plans)
}
