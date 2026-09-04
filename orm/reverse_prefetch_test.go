package orm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestReversePrefetchLoadBatchesGroupsWarmsClonesAndDerivesCold(t *testing.T) {
	prefetch := bindReversePrefetchTestRelation(t, "posts")
	reviewerID := int64(2)
	canonical := []relationObjectTestPost{
		{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewerID},
		{ID: 11, Title: "Beta", AuthorID: 1},
		{ID: 12, Title: "Gamma", AuthorID: 3},
	}
	backend := &reversePostBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
		conditions := plan.Conditions()
		if len(conditions) != 1 {
			t.Fatalf("conditions = %#v", conditions)
		}
		switch conditions[0].Lookup() {
		case query.LookupIn:
			return &reversePostRows{values: cloneReversePosts(canonical)}, nil
		case query.LookupExact:
			identifier, ok := conditions[0].Value().Integer()
			if !ok {
				t.Fatalf("exact condition value = %#v", conditions[0].Value())
			}
			values := make([]relationObjectTestPost, 0)
			for _, value := range canonical {
				if value.AuthorID == identifier {
					values = append(values, relationObjectTestPostDescriptor{}.CloneModel(value))
				}
			}
			return &reversePostRows{values: values}, nil
		default:
			t.Fatalf("lookup = %q", conditions[0].Lookup())
			return nil, nil
		}
	}}
	owners := []relationObjectTestAuthor{
		{ID: 3, Name: "Cleo"},
		{ID: 1, Name: "Ada"},
		{ID: 2, Name: "Bob"},
		{ID: 1, Name: "Ada duplicate"},
	}

	sets, err := prefetch.Load(context.Background(), backend, owners)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(sets) != len(owners) {
		t.Fatalf("Load() sets = %d, want %d", len(sets), len(owners))
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("Load() backend calls = %d, want 1", got)
	}
	assertReversePrefetchBatchPlan(t, backend.plan(0), []int64{1, 2, 3})
	if sets[1] == sets[3] || sets[1].querySet.evaluation == sets[3].querySet.evaluation {
		t.Fatal("duplicate owner inputs share RelatedSet or evaluation identity")
	}

	wants := [][]int64{{12}, {10, 11}, {}, {10, 11}}
	for index, set := range sets {
		values, err := set.All(context.Background())
		if err != nil {
			t.Fatalf("sets[%d].All() error = %v", index, err)
		}
		assertReversePrefetchPostIDs(t, values, wants[index])
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("warm All() backend calls = %d, want 1", got)
	}

	duplicateValues, err := sets[1].All(context.Background())
	if err != nil || len(duplicateValues) != 2 || duplicateValues[0].ReviewerID == nil {
		t.Fatalf("duplicate warm All() = (%#v, %v)", duplicateValues, err)
	}
	duplicateValues[0].ID = 999
	*duplicateValues[0].ReviewerID = 999
	independentValues, err := sets[3].All(context.Background())
	if err != nil || len(independentValues) != 2 || independentValues[0].ID != 10 ||
		independentValues[0].ReviewerID == nil || *independentValues[0].ReviewerID != 2 ||
		independentValues[0].ReviewerID == duplicateValues[0].ReviewerID {
		t.Fatalf("duplicate warm caches exposed aliases: (%#v, %v)", independentValues, err)
	}

	fresh, err := sets[1].Fresh()
	if err != nil || fresh == sets[1] {
		t.Fatalf("Fresh() = (%p, %v), original=%p", fresh, err, sets[1])
	}
	if _, err := fresh.All(context.Background()); err != nil {
		t.Fatalf("fresh All() error = %v", err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("fresh backend calls = %d, want 2", got)
	}
	assertReverseSetPlan(t, backend.plan(1), "author", 1, "id", query.Ascending)

	ordered, err := sets[1].OrderBy(NewStringField[relationObjectTestPost](relationObjectTestPostField("title")).Desc())
	if err != nil || ordered == sets[1] {
		t.Fatalf("OrderBy() = (%p, %v), original=%p", ordered, err, sets[1])
	}
	if _, err := ordered.All(context.Background()); err != nil {
		t.Fatalf("ordered All() error = %v", err)
	}
	if got := backend.callCount(); got != 3 {
		t.Fatalf("ordered backend calls = %d, want 3", got)
	}
	assertReverseSetPlan(t, backend.plan(2), "author", 1, "title", query.Descending)
	if _, err := sets[1].All(context.Background()); err != nil || backend.callCount() != 3 {
		t.Fatalf("derived queries changed original warm cache: calls=%d err=%v", backend.callCount(), err)
	}
}

func TestReversePrefetchNullableForeignKeyUsesExactPhysicalMembership(t *testing.T) {
	prefetch := bindReversePrefetchTestRelation(t, "reviewed_posts")
	reviewerID := int64(2)
	backend := &reversePostBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return &reversePostRows{values: []relationObjectTestPost{
			{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewerID},
			{ID: 12, Title: "Gamma", AuthorID: 3, ReviewerID: &reviewerID},
		}}, nil
	}}
	sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}, {ID: 2}})
	if err != nil || len(sets) != 2 {
		t.Fatalf("nullable Load() = (%#v, %v)", sets, err)
	}
	conditions := backend.plan(0).Conditions()
	if len(conditions) != 1 || conditions[0].Field().Name() != "reviewer" ||
		conditions[0].Field().Column() != "reviewer_id" || !conditions[0].Field().Nullable() ||
		conditions[0].Lookup() != query.LookupIn {
		t.Fatalf("nullable batch conditions = %#v", conditions)
	}
	values, ok := conditions[0].Values()
	if !ok || len(values) != 2 {
		t.Fatalf("nullable batch Values() = (%#v, %v)", values, ok)
	}
	first, firstOK := values[0].Integer()
	second, secondOK := values[1].Integer()
	if !firstOK || !secondOK || first != 1 || second != 2 {
		t.Fatalf("nullable batch keys = ((%d,%v),(%d,%v))", first, firstOK, second, secondOK)
	}
	if posts, err := sets[0].All(context.Background()); err != nil || len(posts) != 0 {
		t.Fatalf("owner 1 reviewed posts = (%#v, %v), want empty", posts, err)
	}
	if posts, err := sets[1].All(context.Background()); err != nil || len(posts) != 2 ||
		posts[0].ID != 10 || posts[1].ID != 12 {
		t.Fatalf("owner 2 reviewed posts = (%#v, %v)", posts, err)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("nullable warm calls = %d, want 1", got)
	}
}

func TestReversePrefetchEmptyAndOperandPrecedence(t *testing.T) {
	prefetch := bindReversePrefetchTestRelation(t, "posts")
	backend := &reversePostBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		t.Fatal("empty validation performed backend I/O")
		return nil, nil
	}}
	var typedNilContext *relationObjectNilContext
	var typedNilBackend *reversePostBackend
	var zero ReversePrefetch[relationObjectTestAuthor, relationObjectTestPost]

	if sets, err := zero.Load(nil, nil, nil); sets != nil {
		t.Fatalf("nil context Load() sets = %#v, want nil", sets)
	} else {
		assertReversePrefetchError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
	}
	if sets, err := zero.Load(typedNilContext, backend, nil); sets != nil {
		t.Fatalf("typed-nil context Load() sets = %#v, want nil", sets)
	} else {
		assertReversePrefetchError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if sets, err := zero.Load(canceled, nil, nil); sets != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Load() = (%#v, %v), want nil/context.Canceled", sets, err)
	}
	if sets, err := zero.Load(context.Background(), nil, nil); sets != nil {
		t.Fatalf("nil backend Load() sets = %#v, want nil", sets)
	} else {
		assertReversePrefetchError(t, err, query.CategoryBackend, query.CodeInvalidPlan, "")
	}
	if sets, err := zero.Load(context.Background(), typedNilBackend, nil); sets != nil {
		t.Fatalf("typed-nil backend Load() sets = %#v, want nil", sets)
	} else {
		assertReversePrefetchError(t, err, query.CategoryBackend, query.CodeInvalidPlan, "")
	}
	if sets, err := zero.Load(context.Background(), backend, nil); sets != nil {
		t.Fatalf("zero handle Load() sets = %#v, want nil", sets)
	} else {
		assertReversePrefetchError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
	}
	corrupt := prefetch
	corrupt.state.valid = false
	if sets, err := corrupt.Load(context.Background(), backend, nil); sets != nil {
		t.Fatalf("corrupt handle Load() sets = %#v, want nil", sets)
	} else {
		assertReversePrefetchError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
	}

	sets, err := prefetch.Load(context.Background(), backend, nil)
	if err != nil {
		t.Fatalf("empty Load() error = %v", err)
	}
	if sets == nil || len(sets) != 0 {
		t.Fatalf("empty Load() sets = %#v, want non-nil empty", sets)
	}
	if got := backend.callCount(); got != 0 {
		t.Fatalf("empty Load() backend calls = %d, want 0", got)
	}
}

func TestReversePrefetchOwnerKeysCloneFirstValidateAndEnforceCap(t *testing.T) {
	reversePrefetchOwnerCloneCalls.Store(0)
	cloneCounting := bindReversePrefetchWithDescriptors(
		t,
		reversePrefetchCloneCountingAuthorDescriptor{},
		relationObjectTestPostDescriptor{},
	)
	backend := &reversePostBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return &reversePostRows{}, nil
	}}
	owners := []relationObjectTestAuthor{{ID: 0}, {ID: 1}, {ID: 2}}
	if sets, err := cloneCounting.Load(context.Background(), backend, owners); sets != nil {
		t.Fatalf("missing-key Load() sets = %#v, want nil", sets)
	} else {
		assertReversePrefetchError(t, err, query.CategoryQuery, query.CodeMissingPrimaryKey, "id")
	}
	if got := reversePrefetchOwnerCloneCalls.Load(); got != int64(len(owners)) {
		t.Fatalf("owner clone calls = %d, want %d before key inspection", got, len(owners))
	}
	if got := backend.callCount(); got != 0 {
		t.Fatalf("missing-key backend calls = %d, want 0", got)
	}

	for name, descriptor := range map[string]ModelDescriptor[relationObjectTestAuthor]{
		"NULL":    reversePrefetchNullPrimaryKeyAuthorDescriptor{},
		"boolean": reverseBooleanPrimaryKeyAuthorDescriptor{},
	} {
		t.Run(name, func(t *testing.T) {
			prefetch := bindReversePrefetchWithDescriptors(t, descriptor, relationObjectTestPostDescriptor{})
			if sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}}); sets != nil {
				t.Fatalf("Load() sets = %#v, want nil", sets)
			} else {
				assertReversePrefetchError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
			}
		})
	}
	if got := backend.callCount(); got != 0 {
		t.Fatalf("invalid-key backend calls = %d, want 0", got)
	}

	zeroPresent := bindReversePrefetchWithDescriptors(
		t,
		reverseZeroPresentAuthorDescriptor{},
		relationObjectTestPostDescriptor{},
	)
	sets, err := zeroPresent.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 0}})
	if err != nil || len(sets) != 1 {
		t.Fatalf("zero-present Load() = (%#v, %v)", sets, err)
	}
	assertReversePrefetchBatchPlan(t, backend.plan(0), []int64{0})

	prefetch := bindReversePrefetchTestRelation(t, "posts")
	owners999 := make([]relationObjectTestAuthor, 999)
	for index := range owners999 {
		owners999[index].ID = int64(999 - index)
	}
	sets, err = prefetch.Load(context.Background(), backend, owners999)
	if err != nil || len(sets) != len(owners999) {
		t.Fatalf("999-key Load() = (sets=%d, err=%v)", len(sets), err)
	}
	assertReversePrefetchBatchPlan(t, backend.plan(1), integerRange(1, 999))
	if got := backend.callCount(); got != 2 {
		t.Fatalf("999-key backend calls = %d, want 2 total", got)
	}

	owners1000 := make([]relationObjectTestAuthor, 1000)
	for index := range owners1000 {
		owners1000[index].ID = int64(index + 1)
	}
	if sets, err := prefetch.Load(context.Background(), backend, owners1000); sets != nil {
		t.Fatalf("1000-key Load() sets = %d, want nil", len(sets))
	} else {
		assertReversePrefetchError(t, err, query.CategoryArgument, query.CodeInvalidValue, "")
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("1000-key Load() performed backend I/O: %d total calls", got)
	}
}

func TestBindReversePrefetchValidatesSourceStorage(t *testing.T) {
	validReverse := bindReverseObjectWithDescriptors(
		t,
		relationObjectTestAuthorDescriptor{},
		relationObjectTestPostDescriptor{},
	)
	prefetch, err := BindReversePrefetch(validReverse)
	if err != nil || !prefetch.state.valid {
		t.Fatalf("BindReversePrefetch(valid) = (%#v, %v)", prefetch, err)
	}
	var zero ReverseObject[relationObjectTestAuthor, relationObjectTestPost]
	if result, err := BindReversePrefetch(zero); err == nil || result.state.valid {
		t.Fatalf("BindReversePrefetch(zero) = (%#v, %v), want zero/error", result, err)
	}

	for name, descriptor := range map[string]ModelDescriptor[relationObjectTestPost]{
		"unavailable": reversePrefetchNoStoragePostDescriptor{},
		"typed nil":   reversePrefetchTypedNilStoragePostDescriptor{},
		"pointer":     relationObjectPointerStoragePostDescriptor{},
		"wrong field": relationObjectWrongFieldPostDescriptor{},
	} {
		t.Run(name, func(t *testing.T) {
			reverse := bindReverseObjectWithDescriptors(t, relationObjectTestAuthorDescriptor{}, descriptor)
			result, err := BindReversePrefetch(reverse)
			if err == nil || result.state.valid {
				t.Fatalf("BindReversePrefetch() = (%#v, %v), want zero/error", result, err)
			}
			assertReversePrefetchError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
		})
	}
}

func TestReversePrefetchResourceFailuresReturnNilCloseOnceAndRetry(t *testing.T) {
	prefetch := bindReversePrefetchTestRelation(t, "posts")
	tests := []struct {
		name      string
		configure func(*reversePostRows, error)
		queryErr  bool
	}{
		{name: "backend", queryErr: true},
		{name: "scan", configure: func(rows *reversePostRows, failure error) { rows.scanErr = failure }},
		{name: "rows", configure: func(rows *reversePostRows, failure error) { rows.rowsErr = failure }},
		{name: "close", configure: func(rows *reversePostRows, failure error) { rows.closeErr = failure }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New(test.name + " failure")
			failedRows := &reversePostRows{values: []relationObjectTestPost{{ID: 10, AuthorID: 1}}}
			if test.configure != nil {
				test.configure(failedRows, failure)
			}
			successRows := &reversePostRows{values: []relationObjectTestPost{{ID: 10, AuthorID: 1}}}
			backend := &reversePostBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call == 0 {
					if test.queryErr {
						return failedRows, failure
					}
					return failedRows, nil
				}
				return successRows, nil
			}}

			if sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}}); sets != nil || !errors.Is(err, failure) {
				t.Fatalf("failed Load() = (%#v, %v), want nil/%v", sets, err, failure)
			}
			if got := failedRows.closeCalls.Load(); got != 1 {
				t.Fatalf("failed Rows.Close calls = %d, want 1", got)
			}
			sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}})
			if err != nil || len(sets) != 1 {
				t.Fatalf("retry Load() = (%#v, %v)", sets, err)
			}
			if values, err := sets[0].All(context.Background()); err != nil || len(values) != 1 || values[0].ID != 10 {
				t.Fatalf("retry warm All() = (%#v, %v)", values, err)
			}
			if got := successRows.closeCalls.Load(); got != 1 {
				t.Fatalf("success Rows.Close calls = %d, want 1", got)
			}
			if got := backend.callCount(); got != 2 {
				t.Fatalf("backend calls = %d, want 2", got)
			}
		})
	}

	t.Run("nil rows", func(t *testing.T) {
		backend := &reversePostBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
			if call == 0 {
				return nil, nil
			}
			return &reversePostRows{}, nil
		}}
		if sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}}); sets != nil {
			t.Fatalf("nil-rows Load() sets = %#v, want nil", sets)
		} else {
			assertReversePrefetchError(t, err, query.CategoryBackend, query.CodeInvalidPlan, "")
		}
		if sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}}); err != nil || len(sets) != 1 {
			t.Fatalf("nil-rows retry Load() = (%#v, %v)", sets, err)
		}
	})
}

func TestReversePrefetchMembershipErrorsAreAtomicAndClassified(t *testing.T) {
	tests := []struct {
		name       string
		descriptor ModelDescriptor[relationObjectTestPost]
		row        relationObjectTestPost
		category   string
		code       string
		field      string
	}{
		{
			name:       "storage false",
			descriptor: reversePrefetchFalseStoragePostDescriptor{},
			row:        relationObjectTestPost{ID: 10, AuthorID: 1},
			category:   query.CategoryQuery,
			code:       query.CodeInvalidPlan,
		},
		{
			name:       "NULL",
			descriptor: reversePrefetchNullStoragePostDescriptor{},
			row:        relationObjectTestPost{ID: 10, AuthorID: 1},
			category:   query.CategoryIntegrity,
			code:       query.CodeRelatedSetMembership,
			field:      "author",
		},
		{
			name:       "non-integer",
			descriptor: relationObjectWrongValuePostDescriptor{},
			row:        relationObjectTestPost{ID: 10, AuthorID: 1},
			category:   query.CategoryIntegrity,
			code:       query.CodeRelatedSetMembership,
			field:      "author",
		},
		{
			name:       "outside requested",
			descriptor: relationObjectTestPostDescriptor{},
			row:        relationObjectTestPost{ID: 10, AuthorID: 999},
			category:   query.CategoryIntegrity,
			code:       query.CodeRelatedSetMembership,
			field:      "author",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prefetch := bindReversePrefetchWithDescriptors(t, relationObjectTestAuthorDescriptor{}, test.descriptor)
			rows := &reversePostRows{values: []relationObjectTestPost{test.row}}
			backend := &reversePostBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
				return rows, nil
			}}
			sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}})
			if sets != nil {
				t.Fatalf("Load() sets = %#v, want nil", sets)
			}
			assertReversePrefetchError(t, err, test.category, test.code, test.field)
			if got := rows.closeCalls.Load(); got != 1 {
				t.Fatalf("Rows.Close calls = %d, want 1", got)
			}
		})
	}

	prefetch := bindReversePrefetchTestRelation(t, "posts")
	backend := &reversePostBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
		if call == 0 {
			return &reversePostRows{values: []relationObjectTestPost{{ID: 10, AuthorID: 999}}}, nil
		}
		return &reversePostRows{values: []relationObjectTestPost{{ID: 10, AuthorID: 1}}}, nil
	}}
	if sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}}); sets != nil {
		t.Fatalf("membership-failed Load() sets = %#v, want nil", sets)
	} else {
		assertReversePrefetchError(t, err, query.CategoryIntegrity, query.CodeRelatedSetMembership, "author")
	}
	sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}})
	if err != nil || len(sets) != 1 {
		t.Fatalf("membership retry Load() = (%#v, %v)", sets, err)
	}
	if values, err := sets[0].All(context.Background()); err != nil || len(values) != 1 || values[0].AuthorID != 1 {
		t.Fatalf("membership retry warm All() = (%#v, %v)", values, err)
	}
}

func TestReversePrefetchGroupingCancellationPublishesNothingAndRetries(t *testing.T) {
	prefetch := bindReversePrefetchWithDescriptors(
		t,
		relationObjectTestAuthorDescriptor{},
		reversePrefetchCancelStoragePostDescriptor{},
	)
	backend := &reversePostBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return &reversePostRows{values: []relationObjectTestPost{{ID: 10, AuthorID: 1}}}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	reversePrefetchCancelStorageHook = cancel
	t.Cleanup(func() { reversePrefetchCancelStorageHook = nil })
	if sets, err := prefetch.Load(ctx, backend, []relationObjectTestAuthor{{ID: 1}}); sets != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled grouping Load() = (%#v, %v), want nil/context.Canceled", sets, err)
	}
	reversePrefetchCancelStorageHook = nil
	sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}})
	if err != nil || len(sets) != 1 {
		t.Fatalf("grouping cancellation retry Load() = (%#v, %v)", sets, err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("grouping cancellation backend calls = %d, want 2", got)
	}
}

func TestReversePrefetchWarmAllIsConcurrentAndRetainsClosedBackend(t *testing.T) {
	prefetch := bindReversePrefetchTestRelation(t, "posts")
	closed := atomic.Bool{}
	closedFailure := errors.New("backend closed")
	backend := &reversePostBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		if closed.Load() {
			return nil, closedFailure
		}
		return &reversePostRows{values: []relationObjectTestPost{{ID: 10, AuthorID: 1}}}, nil
	}}
	sets, err := prefetch.Load(context.Background(), backend, []relationObjectTestAuthor{{ID: 1}})
	if err != nil || len(sets) != 1 {
		t.Fatalf("Load() = (%#v, %v)", sets, err)
	}
	closed.Store(true)

	const callers = 16
	var wait sync.WaitGroup
	failures := make(chan error, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			values, err := sets[0].All(context.Background())
			if err != nil {
				failures <- err
				return
			}
			if len(values) != 1 || values[0].ID != 10 {
				failures <- errors.New("unexpected warm values")
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Fatalf("concurrent warm All() error = %v", failure)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("concurrent warm All() backend calls = %d, want 1", got)
	}

	var typedNilContext *relationObjectNilContext
	if values, err := sets[0].All(typedNilContext); values != nil {
		t.Fatalf("typed-nil warm All() values = %#v, want nil", values)
	} else {
		assertReversePrefetchError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if values, err := sets[0].All(canceled); values != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled warm All() = (%#v, %v)", values, err)
	}

	fresh, err := sets[0].Fresh()
	if err != nil {
		t.Fatalf("Fresh() error = %v", err)
	}
	if values, err := fresh.All(context.Background()); values != nil || !errors.Is(err, closedFailure) {
		t.Fatalf("closed-backend fresh All() = (%#v, %v)", values, err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("closed-backend fresh calls = %d, want 2", got)
	}
}

func bindReversePrefetchTestRelation(
	t *testing.T,
	reverseName string,
) ReversePrefetch[relationObjectTestAuthor, relationObjectTestPost] {
	t.Helper()
	reverse := bindReverseObjectTestRelation(t, reverseName)
	prefetch, err := BindReversePrefetch(reverse)
	if err != nil {
		t.Fatalf("BindReversePrefetch(%s) error = %v", reverseName, err)
	}
	return prefetch
}

func bindReversePrefetchWithDescriptors(
	t *testing.T,
	ownerDescriptor ModelDescriptor[relationObjectTestAuthor],
	sourceDescriptor ModelDescriptor[relationObjectTestPost],
) ReversePrefetch[relationObjectTestAuthor, relationObjectTestPost] {
	t.Helper()
	reverse := bindReverseObjectWithDescriptors(t, ownerDescriptor, sourceDescriptor)
	prefetch, err := BindReversePrefetch(reverse)
	if err != nil {
		t.Fatalf("BindReversePrefetch() error = %v", err)
	}
	return prefetch
}

func bindReverseObjectWithDescriptors(
	t *testing.T,
	ownerDescriptor ModelDescriptor[relationObjectTestAuthor],
	sourceDescriptor ModelDescriptor[relationObjectTestPost],
) ReverseObject[relationObjectTestAuthor, relationObjectTestPost] {
	t.Helper()
	binding := relationObjectTestBinding(t)
	owner, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		ownerDescriptor,
	)
	if err != nil {
		t.Fatalf("BindModel(owner) error = %v", err)
	}
	source, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		sourceDescriptor,
	)
	if err != nil {
		t.Fatalf("BindModel(source) error = %v", err)
	}
	reverse, err := BindReverseObject(owner, "posts", source)
	if err != nil {
		t.Fatalf("BindReverseObject() error = %v", err)
	}
	return reverse
}

func assertReversePrefetchBatchPlan(t *testing.T, plan query.Plan, wantKeys []int64) {
	t.Helper()
	if plan.Table() != "blog_post" {
		t.Fatalf("batch table = %q, want blog_post", plan.Table())
	}
	conditions := plan.Conditions()
	if len(conditions) != 1 || conditions[0].Field().Name() != "author" ||
		conditions[0].Field().Column() != "author_id" || conditions[0].Lookup() != query.LookupIn {
		t.Fatalf("batch conditions = %#v", conditions)
	}
	if _, related := conditions[0].RelationPath(); related {
		t.Fatal("batch condition unexpectedly has a relation path")
	}
	if conditions[0].Value().Kind() != "" {
		t.Fatalf("batch scalar Value() = %#v, want zero", conditions[0].Value())
	}
	values, ok := conditions[0].Values()
	if !ok || len(values) != len(wantKeys) {
		t.Fatalf("batch Values() = (%#v, %v), want %d keys", values, ok, len(wantKeys))
	}
	for index, value := range values {
		identifier, integer := value.Integer()
		if !integer || identifier != wantKeys[index] {
			t.Fatalf("batch key[%d] = (%d, %v), want %d", index, identifier, integer, wantKeys[index])
		}
	}
	orderings := plan.Orderings()
	if len(orderings) != 1 || orderings[0].Field().Name() != "id" ||
		orderings[0].Direction() != query.Ascending {
		t.Fatalf("batch orderings = %#v", orderings)
	}
}

func assertReversePrefetchPostIDs(t *testing.T, values []relationObjectTestPost, want []int64) {
	t.Helper()
	if len(values) != len(want) {
		t.Fatalf("post count = %d, want %d (%#v)", len(values), len(want), values)
	}
	for index := range want {
		if values[index].ID != want[index] {
			t.Fatalf("post ID[%d] = %d, want %d", index, values[index].ID, want[index])
		}
	}
}

func assertReversePrefetchError(t *testing.T, err error, category, code, field string) *query.Error {
	t.Helper()
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Category != category || queryError.Code != code ||
		queryError.Field != field {
		t.Fatalf("error = %T %v, want %s/%s field=%q", err, err, category, code, field)
	}
	return queryError
}

func integerRange(first, last int64) []int64 {
	values := make([]int64, last-first+1)
	for index := range values {
		values[index] = first + int64(index)
	}
	return values
}

var reversePrefetchOwnerCloneCalls atomic.Int64

type reversePrefetchCloneCountingAuthorDescriptor struct {
	relationObjectTestAuthorDescriptor
}

func (reversePrefetchCloneCountingAuthorDescriptor) CloneModel(value relationObjectTestAuthor) relationObjectTestAuthor {
	reversePrefetchOwnerCloneCalls.Add(1)
	return value
}

func (reversePrefetchCloneCountingAuthorDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestAuthor] {
	return reversePrefetchCloneCountingAuthorDescriptor{}
}

type reversePrefetchNullPrimaryKeyAuthorDescriptor struct {
	relationObjectTestAuthorDescriptor
}

func (reversePrefetchNullPrimaryKeyAuthorDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestAuthor] {
	return reversePrefetchNullPrimaryKeyAuthorDescriptor{}
}

func (reversePrefetchNullPrimaryKeyAuthorDescriptor) PrimaryKey(relationObjectTestAuthor) (query.Value, bool) {
	return query.Null(), true
}

type reversePrefetchNoStoragePostDescriptor struct {
	relationObjectTestPostDescriptor
}

func (reversePrefetchNoStoragePostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return reversePrefetchNoStoragePostDescriptor{}
}

func (reversePrefetchNoStoragePostDescriptor) BindRelationStorage(ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	return nil, false
}

type reversePrefetchTypedNilStoragePostDescriptor struct {
	relationObjectTestPostDescriptor
}

func (reversePrefetchTypedNilStoragePostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return reversePrefetchTypedNilStoragePostDescriptor{}
}

func (reversePrefetchTypedNilStoragePostDescriptor) BindRelationStorage(ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	var storage *reversePrefetchTypedNilStorage
	return storage, true
}

type reversePrefetchTypedNilStorage struct{}

func (*reversePrefetchTypedNilStorage) Field() ir.Field {
	panic("typed nil storage must not be called")
}

func (*reversePrefetchTypedNilStorage) Value(relationObjectTestPost) (query.Value, bool) {
	panic("typed nil storage must not be called")
}

type reversePrefetchFalseStoragePostDescriptor struct {
	relationObjectTestPostDescriptor
}

func (reversePrefetchFalseStoragePostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return reversePrefetchFalseStoragePostDescriptor{}
}

func (reversePrefetchFalseStoragePostDescriptor) BindRelationStorage(field ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	if field.Name != "author" {
		return nil, false
	}
	return reversePrefetchFalseStorage{}, true
}

type reversePrefetchFalseStorage struct{}

func (reversePrefetchFalseStorage) Field() ir.Field {
	return relationObjectTestPostField("author")
}

func (reversePrefetchFalseStorage) Value(relationObjectTestPost) (query.Value, bool) {
	return query.Value{}, false
}

type reversePrefetchNullStoragePostDescriptor struct {
	relationObjectTestPostDescriptor
}

func (reversePrefetchNullStoragePostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return reversePrefetchNullStoragePostDescriptor{}
}

func (reversePrefetchNullStoragePostDescriptor) BindRelationStorage(field ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	if field.Name != "author" {
		return nil, false
	}
	return reversePrefetchNullStorage{}, true
}

type reversePrefetchNullStorage struct{}

func (reversePrefetchNullStorage) Field() ir.Field {
	return relationObjectTestPostField("author")
}

func (reversePrefetchNullStorage) Value(relationObjectTestPost) (query.Value, bool) {
	return query.Null(), true
}

var reversePrefetchCancelStorageHook func()

type reversePrefetchCancelStoragePostDescriptor struct {
	relationObjectTestPostDescriptor
}

func (reversePrefetchCancelStoragePostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return reversePrefetchCancelStoragePostDescriptor{}
}

func (reversePrefetchCancelStoragePostDescriptor) BindRelationStorage(field ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	if field.Name != "author" {
		return nil, false
	}
	return reversePrefetchCancelStorage{}, true
}

type reversePrefetchCancelStorage struct{}

func (reversePrefetchCancelStorage) Field() ir.Field {
	return relationObjectTestPostField("author")
}

func (reversePrefetchCancelStorage) Value(value relationObjectTestPost) (query.Value, bool) {
	if reversePrefetchCancelStorageHook != nil {
		reversePrefetchCancelStorageHook()
	}
	return query.Integer(value.AuthorID), true
}
