package orm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestRowLockQueryDerivationDoesNotUseAnOrdinaryCache(t *testing.T) {
	backend := &cacheTestBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
		lock, present := plan.RowLock()
		if present != (call != 0) {
			return nil, fmt.Errorf("call %d lock=%v", call, present)
		}
		if present && (lock.Strength() != query.LockForNoKeyUpdate || lock.WaitPolicy() != query.LockNoWait || len(lock.Targets()) != 1 || !lock.Targets()[0].Self()) {
			return nil, errors.New("lock options or target were lost")
		}
		return rowsForIDs(int64(call + 1)), nil
	}}
	base := newCacheTestManager().Using(backend)
	assertCacheTestID(t, base, 1)
	locked := base.SelectForUpdate(RowLockOptions{NoKey: true, NoWait: true}, base.LockTarget())
	assertCacheTestID(t, locked, 2)
	assertCacheTestID(t, locked, 2)
	assertCacheTestID(t, base, 1)
	assertCacheTestID(t, locked.Fresh(), 3)
	if backend.callCount() != 3 {
		t.Fatal("lock query reused an ordinary cache or changed it")
	}
	bad := base.SelectForUpdate(RowLockOptions{NoWait: true, SkipLocked: true})
	if _, err := bad.All(t.Context()); !errors.Is(err, &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidValue}) {
		t.Fatalf("conflicting wait = %v", err)
	}
	if _, err := base.SelectForUpdate(RowLockOptions{}, RowLockTarget[cacheTestModel]{}).All(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatalf("zero target = %v", err)
	}
	first := errors.New("earlier filter error")
	bad = base.Filter(Predicate[cacheTestModel]{err: first}).SelectForUpdate(RowLockOptions{NoWait: true, SkipLocked: true})
	if !errors.Is(bad.ConfigurationError(), first) {
		t.Fatal("lock configuration replaced the earlier error")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := bad.All(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation precedence = %v", err)
	}
	if backend.callCount() != 3 {
		t.Fatal("invalid lock executed I/O")
	}
}

func TestRowLockTypedAndDynamicTargetsShareTheSamePlan(t *testing.T) {
	post, author, _, _ := bindRelationObjectTestFixture(t)
	relation, err := BindQueryRelation(post, "author", author)
	if err != nil {
		t.Fatal(err)
	}
	base := NewManager(post.objectDescriptor).Using(nil)
	typed := base.SelectForUpdate(RowLockOptions{SkipLocked: true}, base.LockTarget(), relation.LockTarget())
	if err := typed.ConfigurationError(); err != nil {
		t.Fatal(err)
	}
	targets, err := ParseRowLockTargets(post, "author", "self")
	if err != nil {
		t.Fatal(err)
	}
	dynamic := base.SelectForUpdate(RowLockOptions{SkipLocked: true}, targets...)
	if err := dynamic.ConfigurationError(); err != nil || !typed.Plan().Equal(dynamic.Plan()) {
		t.Fatalf("typed/dynamic lock plans differ: %v", err)
	}
	targets[0] = RowLockTarget[relationObjectTestPost]{}
	if !typed.Plan().Equal(dynamic.Plan()) {
		t.Fatal("dynamic targets alias the plan")
	}
	for _, path := range []string{"", "__", "Author", "author__", "author__missing", "author__posts"} {
		if _, err := ParseRowLockTargets(post, path); err == nil {
			t.Fatalf("invalid path accepted: %q", path)
		}
	}
	if _, err := ParseRowLockTargets(BoundModel[relationObjectTestPost]{}, "self"); err == nil {
		t.Fatal("zero binding accepted")
	}
	if err := base.SelectForUpdate(RowLockOptions{}, (QueryRelation[relationObjectTestPost, relationObjectTestAuthor]{}).LockTarget()).ConfigurationError(); err == nil {
		t.Fatal("zero relation accepted")
	}
}

func TestSinglePrefetchExplicitLockReevaluatesAnEagerTarget(t *testing.T) {
	post, author, required, nullable := bindRelationObjectTestFixture(t)
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			selection := SelectRequiredForward(required)
			cache := typedCachedRelatedTarget[relationObjectTestAuthor]{selected: selection.state.path.projection, descriptor: selection.state.targetDescriptor, binding: author, present: present, value: relationObjectTestAuthor{ID: 7, Name: "cached"}}
			values := []relatedSelectedValue[relationObjectTestPost]{{source: relationObjectTestPost{ID: 1, AuthorID: 7}, targets: []cachedRelatedTarget{cache}}}
			backend := &relationObjectAuthorBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
				if _, locked := plan.RowLock(); !locked || call != 0 {
					return nil, fmt.Errorf("custom target lock missing, call=%d", call)
				}
				return &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: 7, Name: "locked current"}}}, nil
			}}
			remaining := MaximumRelatedSelectionNodes
			prepared, err := PrefetchRequiredForward(required).SelectForUpdate(RowLockOptions{}).preparePrefetch(1, &remaining)
			if err != nil {
				t.Fatal(err)
			}
			if err := prepared.apply(t.Context(), backend, values); err != nil {
				t.Fatal(err)
			}
			if backend.callCount() != 1 || len(values[0].targets) != 1 {
				t.Fatal("lock was served from cache or appended a duplicate target")
			}
			graph, err := cloneRelatedSelection(backend, post, post.objectDescriptor, values[0])
			if err != nil {
				t.Fatal(err)
			}
			handle, err := selection.Related(graph)
			if err != nil {
				t.Fatal(err)
			}
			value, found, err := handle.Get(t.Context())
			if err != nil || !found || value.Name != "locked current" || cache.value.Name != "cached" || backend.callCount() != 1 {
				t.Fatalf("locked graph = %#v/%v/%v", value, found, err)
			}
		})
	}
	t.Run("null_fk_checks_explicit_capability", func(t *testing.T) {
		selection := SelectNullableForward(nullable)
		cache := typedCachedRelatedTarget[relationObjectTestAuthor]{selected: selection.state.path.projection, descriptor: selection.state.targetDescriptor, binding: author}
		values := []relatedSelectedValue[relationObjectTestPost]{{source: relationObjectTestPost{ID: 1, AuthorID: 7}, targets: []cachedRelatedTarget{cache}}}
		unsupported := &query.Error{Code: query.CodeUnsupported}
		backend := &relationObjectAuthorBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
			if _, locked := plan.RowLock(); !locked || !plan.EmptyResult() {
				t.Fatal("null FK validation lost empty lock plan")
			}
			return nil, unsupported
		}}
		remaining := MaximumRelatedSelectionNodes
		prepared, err := PrefetchNullableForward(nullable).SelectForUpdate(RowLockOptions{}).preparePrefetch(1, &remaining)
		if err != nil {
			t.Fatal(err)
		}
		if err := prepared.apply(t.Context(), backend, values); !errors.Is(err, unsupported) || backend.callCount() != 1 {
			t.Fatalf("null FK lock validation = %v/%d", err, backend.callCount())
		}
	})
}
