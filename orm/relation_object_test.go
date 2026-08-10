package orm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestForwardRelationObjectsLoadCacheCloneAndFresh(t *testing.T) {
	postModel, authorModel, required, nullable := bindRelationObjectTestFixture(t)
	backend := &relationObjectAuthorBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
		identifier := relationObjectPlanIdentifier(t, plan)
		return &relationObjectAuthorRows{values: []relationObjectTestAuthor{{
			ID: identifier, Name: map[int64]string{1: "Ada", 2: "Bob"}[identifier],
		}}}, nil
	}}

	related, err := required.From(backend, relationObjectTestPost{ID: 10, AuthorID: 1})
	if err != nil {
		t.Fatalf("required From() error = %v", err)
	}
	if got := backend.callCount(); got != 0 {
		t.Fatalf("From() backend calls = %d, want 0", got)
	}
	first, ok, err := related.Get(context.Background())
	if err != nil || !ok || first != (relationObjectTestAuthor{ID: 1, Name: "Ada"}) {
		t.Fatalf("cold Get() = (%#v, %v, %v)", first, ok, err)
	}
	first.ID = 999
	first.Name = "mutated"
	warm, ok, err := related.Get(context.Background())
	if err != nil || !ok || warm != (relationObjectTestAuthor{ID: 1, Name: "Ada"}) {
		t.Fatalf("warm Get() = (%#v, %v, %v)", warm, ok, err)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("required cold/warm backend calls = %d, want 1", got)
	}
	assertRelationObjectLimit(t, backend.plan(0), 2)

	fresh, err := related.Fresh()
	if err != nil {
		t.Fatalf("Fresh() error = %v", err)
	}
	if _, ok, err := fresh.Get(context.Background()); err != nil || !ok {
		t.Fatalf("fresh Get() = (ok=%v, err=%v)", ok, err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("fresh backend calls = %d, want 2", got)
	}
	if _, ok, err := related.Get(context.Background()); err != nil || !ok {
		t.Fatalf("original warm Get() = (ok=%v, err=%v)", ok, err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("fresh changed original cache: calls=%d", got)
	}

	absent, err := nullable.From(backend, relationObjectTestPost{ID: 11, AuthorID: 1})
	if err != nil {
		t.Fatalf("nullable absent From() error = %v", err)
	}
	if value, ok, err := absent.Get(context.Background()); err != nil || ok || value != (relationObjectTestAuthor{}) {
		t.Fatalf("nullable absent Get() = (%#v, %v, %v)", value, ok, err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("nullable absent performed backend I/O: calls=%d", got)
	}
	absentFresh, err := absent.Fresh()
	if err != nil {
		t.Fatalf("absent Fresh() error = %v", err)
	}
	if _, ok, err := absentFresh.Get(context.Background()); err != nil || ok {
		t.Fatalf("absent fresh Get() = (ok=%v, err=%v)", ok, err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("nullable absent fresh performed backend I/O: calls=%d", got)
	}

	reviewerID := int64(2)
	source := relationObjectTestPost{ID: 10, AuthorID: 1, ReviewerID: &reviewerID}
	reviewer, err := nullable.From(backend, source)
	if err != nil {
		t.Fatalf("nullable positive From() error = %v", err)
	}
	reviewerID = 999
	source.ReviewerID = nil
	value, ok, err := reviewer.Get(context.Background())
	if err != nil || !ok || value != (relationObjectTestAuthor{ID: 2, Name: "Bob"}) {
		t.Fatalf("nullable positive Get() = (%#v, %v, %v)", value, ok, err)
	}
	if got := backend.callCount(); got != 3 {
		t.Fatalf("nullable positive backend calls = %d, want 3", got)
	}

	// Bound descriptors are still usable by ordinary typed QuerySets.
	if err := validateBoundModel(postModel); err != nil {
		t.Fatalf("post BoundModel validation = %v", err)
	}
	if err := validateBoundModel(authorModel); err != nil {
		t.Fatalf("author BoundModel validation = %v", err)
	}
}

func TestRelatedObjectCachesMissingAndCardinalitySnapshots(t *testing.T) {
	_, _, required, _ := bindRelationObjectTestFixture(t)
	tests := []struct {
		name     string
		values   []relationObjectTestAuthor
		category string
		code     string
	}{
		{name: "missing", category: query.CategoryModelState, code: query.CodeRelatedObjectMissing},
		{
			name: "cardinality",
			values: []relationObjectTestAuthor{
				{ID: 1, Name: "Ada"},
				{ID: 1, Name: "Duplicate"},
			},
			category: query.CategoryIntegrity,
			code:     query.CodeRelatedObjectCardinality,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &relationObjectAuthorBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
				return &relationObjectAuthorRows{values: append([]relationObjectTestAuthor(nil), test.values...)}, nil
			}}
			related, err := required.From(backend, relationObjectTestPost{AuthorID: 1})
			if err != nil {
				t.Fatalf("From() error = %v", err)
			}
			for call := 0; call < 2; call++ {
				_, ok, err := related.Get(context.Background())
				if ok {
					t.Fatalf("Get() ok = true, want false")
				}
				assertRelationObjectQueryError(t, err, test.category, test.code)
			}
			if got := backend.callCount(); got != 1 {
				t.Fatalf("successful %s snapshot was not cached: calls=%d", test.name, got)
			}
		})
	}
}

func TestRelatedObjectFailuresRetryAndConcurrentColdAccessSingleflights(t *testing.T) {
	_, _, required, _ := bindRelationObjectTestFixture(t)
	failure := errors.New("backend failure")
	retryingBackend := &relationObjectAuthorBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
		if call == 0 {
			return nil, failure
		}
		return &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: 1, Name: "Ada"}}}, nil
	}}
	retrying, err := required.From(retryingBackend, relationObjectTestPost{AuthorID: 1})
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}
	if _, _, err := retrying.Get(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("first Get() error = %v, want %v", err, failure)
	}
	if _, ok, err := retrying.Get(context.Background()); err != nil || !ok {
		t.Fatalf("retry Get() = (ok=%v, err=%v)", ok, err)
	}
	if _, ok, err := retrying.Get(context.Background()); err != nil || !ok {
		t.Fatalf("warm retry Get() = (ok=%v, err=%v)", ok, err)
	}
	if got := retryingBackend.callCount(); got != 2 {
		t.Fatalf("failure/retry backend calls = %d, want 2", got)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	concurrentBackend := &relationObjectAuthorBackend{query: func(_ int, ctx context.Context, _ query.Plan) (db.Rows, error) {
		close(started)
		select {
		case <-release:
			return &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: 1, Name: "Ada"}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	concurrent, err := required.From(concurrentBackend, relationObjectTestPost{AuthorID: 1})
	if err != nil {
		t.Fatalf("concurrent From() error = %v", err)
	}
	const callers = 24
	results := make(chan error, callers)
	go func() {
		_, _, err := concurrent.Get(context.Background())
		results <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for owner query")
	}
	for index := 1; index < callers; index++ {
		go func() {
			_, _, err := concurrent.Get(context.Background())
			results <- err
		}()
	}
	close(release)
	for index := 0; index < callers; index++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent Get() error = %v", err)
		}
	}
	if got := concurrentBackend.callCount(); got != 1 {
		t.Fatalf("concurrent backend calls = %d, want 1", got)
	}
}

func TestRelatedObjectWaiterCancellationDoesNotCancelOwner(t *testing.T) {
	_, _, required, _ := bindRelationObjectTestFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	backend := &relationObjectAuthorBackend{query: func(_ int, ctx context.Context, _ query.Plan) (db.Rows, error) {
		close(started)
		select {
		case <-release:
			return &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: 1, Name: "Ada"}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	related, err := required.From(backend, relationObjectTestPost{AuthorID: 1})
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}

	type getResult struct {
		value relationObjectTestAuthor
		ok    bool
		err   error
	}
	ownerResult := make(chan getResult, 1)
	go func() {
		value, ok, err := related.Get(context.Background())
		ownerResult <- getResult{value: value, ok: ok, err: err}
	}()
	awaitSignal(t, started, "related-object owner backend start")

	waiterBase, cancelWaiter := context.WithCancel(context.Background())
	waiterCtx, waiterEntered := newEnteredContext(waiterBase)
	waiterResult := make(chan error, 1)
	go func() {
		_, _, err := related.Get(waiterCtx)
		waiterResult <- err
	}()
	awaitEntered(t, waiterEntered)
	cancelWaiter()
	if err := awaitValue(t, waiterResult, "related-object canceled waiter"); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v, want context.Canceled", err)
	}
	select {
	case result := <-ownerResult:
		t.Fatalf("waiter cancellation completed owner early: %#v", result)
	default:
	}
	close(release)
	owner := awaitValue(t, ownerResult, "related-object owner result")
	if owner.err != nil || !owner.ok || owner.value != (relationObjectTestAuthor{ID: 1, Name: "Ada"}) {
		t.Fatalf("owner result = %#v", owner)
	}
	if _, ok, err := related.Get(context.Background()); err != nil || !ok {
		t.Fatalf("warm Get() = (ok=%v, err=%v)", ok, err)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("waiter cancellation/backend calls = %d, want 1", got)
	}
}

func TestRelatedObjectOwnerCancellationLetsLiveWaiterRetry(t *testing.T) {
	_, _, required, _ := bindRelationObjectTestFixture(t)
	started := make(chan struct{})
	backend := &relationObjectAuthorBackend{query: func(call int, ctx context.Context, _ query.Plan) (db.Rows, error) {
		if call == 0 {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: 1, Name: "Ada"}}}, nil
	}}
	related, err := required.From(backend, relationObjectTestPost{AuthorID: 1})
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}

	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	ownerResult := make(chan error, 1)
	go func() {
		_, _, err := related.Get(ownerCtx)
		ownerResult <- err
	}()
	awaitSignal(t, started, "related-object canceled owner backend start")

	waiterCtx, waiterEntered := newEnteredContext(context.Background())
	type getResult struct {
		value relationObjectTestAuthor
		ok    bool
		err   error
	}
	waiterResult := make(chan getResult, 1)
	go func() {
		value, ok, err := related.Get(waiterCtx)
		waiterResult <- getResult{value: value, ok: ok, err: err}
	}()
	awaitEntered(t, waiterEntered)
	cancelOwner()
	if err := awaitValue(t, ownerResult, "related-object canceled owner result"); !errors.Is(err, context.Canceled) {
		t.Fatalf("owner error = %v, want context.Canceled", err)
	}
	waiter := awaitValue(t, waiterResult, "related-object retrying waiter result")
	if waiter.err != nil || !waiter.ok || waiter.value != (relationObjectTestAuthor{ID: 1, Name: "Ada"}) {
		t.Fatalf("live waiter result = %#v", waiter)
	}
	if _, ok, err := related.Get(context.Background()); err != nil || !ok {
		t.Fatalf("warm Get() = (ok=%v, err=%v)", ok, err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("owner cancellation/backend calls = %d, want 2", got)
	}
}

func TestRelatedObjectScanRowsAndCloseFailuresRemainRetryable(t *testing.T) {
	_, _, required, _ := bindRelationObjectTestFixture(t)
	tests := []struct {
		name      string
		configure func(*relationObjectAuthorRows, error)
	}{
		{name: "scan", configure: func(rows *relationObjectAuthorRows, failure error) { rows.scanErr = failure }},
		{name: "rows", configure: func(rows *relationObjectAuthorRows, failure error) { rows.rowsErr = failure }},
		{name: "close", configure: func(rows *relationObjectAuthorRows, failure error) { rows.closeErr = failure }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New(test.name + " failure")
			failedRows := &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: 1, Name: "failed"}}}
			test.configure(failedRows, failure)
			successRows := &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: 1, Name: "Ada"}}}
			backend := &relationObjectAuthorBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call == 0 {
					return failedRows, nil
				}
				return successRows, nil
			}}
			related, err := required.From(backend, relationObjectTestPost{AuthorID: 1})
			if err != nil {
				t.Fatalf("From() error = %v", err)
			}
			if _, _, err := related.Get(context.Background()); !errors.Is(err, failure) {
				t.Fatalf("first Get() error = %v, want preserved %v", err, failure)
			}
			value, ok, err := related.Get(context.Background())
			if err != nil || !ok || value != (relationObjectTestAuthor{ID: 1, Name: "Ada"}) {
				t.Fatalf("retry Get() = (%#v, %v, %v)", value, ok, err)
			}
			if _, ok, err := related.Get(context.Background()); err != nil || !ok {
				t.Fatalf("warm Get() = (ok=%v, err=%v)", ok, err)
			}
			if got := backend.callCount(); got != 2 {
				t.Fatalf("%s failure/backend calls = %d, want 2", test.name, got)
			}
			if got := failedRows.closeCalls.Load() + successRows.closeCalls.Load(); got != 2 {
				t.Fatalf("%s failure/Close calls = %d, want 2", test.name, got)
			}
		})
	}
}

func TestRelatedObjectCapturedBackendAndSessionLifetime(t *testing.T) {
	_, _, required, _ := bindRelationObjectTestFixture(t)
	closedError := errors.New("captured session is closed")
	newBackend := func() (*relationObjectAuthorBackend, *atomic.Bool) {
		closed := &atomic.Bool{}
		backend := &relationObjectAuthorBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
			if closed.Load() {
				return nil, closedError
			}
			return &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: 1, Name: "Ada"}}}, nil
		}}
		return backend, closed
	}

	coldBackend, coldClosed := newBackend()
	cold, err := required.From(coldBackend, relationObjectTestPost{AuthorID: 1})
	if err != nil {
		t.Fatalf("cold From() error = %v", err)
	}
	coldClosed.Store(true)
	if _, _, err := cold.Get(context.Background()); !errors.Is(err, closedError) {
		t.Fatalf("cold-after-close Get() error = %v, want %v", err, closedError)
	}
	if got := coldBackend.callCount(); got != 1 {
		t.Fatalf("cold-after-close backend calls = %d, want 1", got)
	}

	warmBackend, warmClosed := newBackend()
	warm, err := required.From(warmBackend, relationObjectTestPost{AuthorID: 1})
	if err != nil {
		t.Fatalf("warm From() error = %v", err)
	}
	if _, ok, err := warm.Get(context.Background()); err != nil || !ok {
		t.Fatalf("warm populate Get() = (ok=%v, err=%v)", ok, err)
	}
	warmClosed.Store(true)
	if _, ok, err := warm.Get(context.Background()); err != nil || !ok {
		t.Fatalf("cached warm-after-close Get() = (ok=%v, err=%v)", ok, err)
	}
	if got := warmBackend.callCount(); got != 1 {
		t.Fatalf("cached warm-after-close backend calls = %d, want 1", got)
	}
	fresh, err := warm.Fresh()
	if err != nil {
		t.Fatalf("Fresh() error = %v", err)
	}
	if _, _, err := fresh.Get(context.Background()); !errors.Is(err, closedError) {
		t.Fatalf("Fresh() closed-backend Get() error = %v, want %v", err, closedError)
	}
	if got := warmBackend.callCount(); got != 2 {
		t.Fatalf("Fresh() closed-backend calls = %d, want 2", got)
	}

	liveBackend, _ := newBackend()
	live, err := required.From(liveBackend, relationObjectTestPost{AuthorID: 1})
	if err != nil {
		t.Fatalf("live From() error = %v", err)
	}
	if _, ok, err := live.Get(context.Background()); err != nil || !ok {
		t.Fatalf("new live-backend Get() = (ok=%v, err=%v)", ok, err)
	}
	if got := liveBackend.callCount(); got != 1 {
		t.Fatalf("new live-backend calls = %d, want 1", got)
	}
}

func TestRelationObjectNilContextAndCopyFailuresAreStructured(t *testing.T) {
	_, _, required, nullable := bindRelationObjectTestFixture(t)
	backend := &relationObjectAuthorBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: 1, Name: "Ada"}}}, nil
	}}

	if object, err := required.From(nil, relationObjectTestPost{AuthorID: 1}); object != nil || err == nil {
		t.Fatalf("From(nil) = (%#v, %v), want nil/error", object, err)
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryBackend, query.CodeInvalidPlan)
	}
	var typedNilBackend *relationObjectAuthorBackend
	if object, err := required.From(typedNilBackend, relationObjectTestPost{AuthorID: 1}); object != nil || err == nil {
		t.Fatalf("From(typed nil) = (%#v, %v), want nil/error", object, err)
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryBackend, query.CodeInvalidPlan)
	}
	var zeroRequired RequiredForwardObject[relationObjectTestPost, relationObjectTestAuthor]
	if object, err := zeroRequired.From(backend, relationObjectTestPost{AuthorID: 1}); object != nil || err == nil {
		t.Fatalf("zero From() = (%#v, %v), want nil/error", object, err)
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}

	valid, err := required.From(backend, relationObjectTestPost{AuthorID: 1})
	if err != nil {
		t.Fatalf("valid From() error = %v", err)
	}
	var nilObject *RelatedObject[relationObjectTestAuthor]
	if _, _, err := nilObject.Get(context.Background()); err == nil {
		t.Fatal("nil RelatedObject.Get() succeeded")
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}
	var zero RelatedObject[relationObjectTestAuthor]
	if _, _, err := zero.Get(context.Background()); err == nil {
		t.Fatal("zero RelatedObject.Get() succeeded")
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}
	copyOfValid := *valid
	if _, _, err := copyOfValid.Get(context.Background()); err == nil {
		t.Fatal("copied RelatedObject.Get() succeeded")
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}
	if fresh, err := copyOfValid.Fresh(); fresh != nil || err == nil {
		t.Fatalf("copied Fresh() = (%#v, %v), want nil/error", fresh, err)
	}

	var typedNilContext *relationObjectNilContext
	if _, _, err := valid.Get(typedNilContext); err == nil {
		t.Fatal("typed-nil context Get() succeeded")
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}
	absent, err := nullable.From(backend, relationObjectTestPost{AuthorID: 1})
	if err != nil {
		t.Fatalf("absent From() error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := absent.Get(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("absent canceled Get() error = %v, want context.Canceled", err)
	}
	if got := backend.callCount(); got != 0 {
		t.Fatalf("invalid/canceled paths performed backend I/O: calls=%d", got)
	}
	if _, ok, err := valid.Get(context.Background()); err != nil || !ok {
		t.Fatalf("valid cold Get() = (ok=%v, err=%v)", ok, err)
	}
	warmCanceled, cancelWarm := context.WithCancel(context.Background())
	cancelWarm()
	if _, _, err := valid.Get(warmCanceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("warm canceled Get() error = %v, want context.Canceled", err)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("warm canceled Get() performed backend I/O: calls=%d", got)
	}
}

func TestRelationObjectBindingRejectsMissingSealStorageAndWrongShape(t *testing.T) {
	binding := relationObjectTestBinding(t)
	postIdentity := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	authorIdentity := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	author, err := BindModel(binding, authorIdentity, relationObjectTestAuthorDescriptor{})
	if err != nil {
		t.Fatalf("BindModel(author) error = %v", err)
	}
	plainPost, err := BindModel(binding, postIdentity, relationObjectPlainPostDescriptor{})
	if err != nil {
		t.Fatalf("BindModel(plain post) error = %v", err)
	}
	if _, err := BindRequiredForwardObject(plainPost, "author", author); err == nil {
		t.Fatal("BindRequiredForwardObject() accepted missing source seal")
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}

	post, err := BindModel(binding, postIdentity, relationObjectTestPostDescriptor{})
	if err != nil {
		t.Fatalf("BindModel(post) error = %v", err)
	}
	if _, err := BindRequiredForwardObject(post, "reviewer", author); err == nil {
		t.Fatal("required binder accepted nullable relation")
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryField, query.CodeUnsupportedLookup)
	}
	if _, err := BindNullableForwardObject(post, "author", author); err == nil {
		t.Fatal("nullable binder accepted required relation")
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryField, query.CodeUnsupportedLookup)
	}

	otherBinding := relationObjectTestBinding(t)
	otherAuthor, err := BindModel(otherBinding, authorIdentity, relationObjectTestAuthorDescriptor{})
	if err != nil {
		t.Fatalf("BindModel(other author) error = %v", err)
	}
	if _, err := BindRequiredForwardObject(post, "author", otherAuthor); err == nil {
		t.Fatal("binder accepted different project snapshots")
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}

	badStoragePost, err := BindModel(binding, postIdentity, relationObjectPointerStoragePostDescriptor{})
	if err != nil {
		t.Fatalf("BindModel(pointer storage post) error = %v", err)
	}
	if _, err := BindRequiredForwardObject(badStoragePost, "author", author); err == nil {
		t.Fatal("binder accepted pointer relation storage")
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}

	wrongFieldPost, err := BindModel(binding, postIdentity, relationObjectWrongFieldPostDescriptor{})
	if err != nil {
		t.Fatalf("BindModel(wrong-field storage post) error = %v", err)
	}
	if _, err := BindRequiredForwardObject(wrongFieldPost, "author", author); err == nil {
		t.Fatal("binder accepted mismatched relation storage field")
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}

	wrongValuePost, err := BindModel(binding, postIdentity, relationObjectWrongValuePostDescriptor{})
	if err != nil {
		t.Fatalf("BindModel(wrong-value storage post) error = %v", err)
	}
	wrongValue, err := BindRequiredForwardObject(wrongValuePost, "author", author)
	if err != nil {
		t.Fatalf("BindRequiredForwardObject(wrong value) bind error = %v", err)
	}
	noQueryBackend := &relationObjectAuthorBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return nil, errors.New("must not query")
	}}
	if object, err := wrongValue.From(noQueryBackend, relationObjectTestPost{AuthorID: 1}); object != nil || err == nil {
		t.Fatalf("wrong-value From() = (%#v, %v), want nil/error", object, err)
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}
	if got := noQueryBackend.callCount(); got != 0 {
		t.Fatalf("wrong storage value performed backend I/O: calls=%d", got)
	}
}

func TestTypedAndDynamicNullableRelationObjectsSharePlan(t *testing.T) {
	post, author, _, nullable := bindRelationObjectTestFixture(t)
	forward, err := BindForward(post, "author", author)
	if err != nil {
		t.Fatalf("BindForward(author) error = %v", err)
	}
	authorMetadata, _ := relationObjectTestModels()
	authorName, err := forward.String(NewStringField[relationObjectTestAuthor](authorMetadata.Fields[1]))
	if err != nil {
		t.Fatalf("ForwardRelation.String(name) error = %v", err)
	}

	seenPolicy := false
	dynamic, err := ParseDynamicRelationObjects(post, func(field ir.Field, lookup query.Lookup) bool {
		if field.Name == "reviewer" {
			seenPolicy = field.Kind == ir.FieldForeignKey && field.Nullable && field.Column == "reviewer_id" &&
				field.Relation != nil && field.Relation.Target == (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) &&
				lookup == query.LookupIsNull
			field.Column = "mutated"
		}
		return true
	}, []LookupInput{
		{Key: "author__name", Value: "Ada"},
		{Key: "reviewer__isnull", Value: true},
	})
	if err != nil {
		t.Fatalf("ParseDynamicRelationObjects() error = %v", err)
	}
	if !seenPolicy {
		t.Fatal("policy did not receive canonical nullable source field")
	}
	typedPlan := NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).
		Using(nil).
		Filter(authorName.Exact("Ada"), nullable.IsNull(true)).
		Plan()
	dynamicPlan := NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).
		Using(nil).
		Filter(dynamic...).
		Plan()
	if !typedPlan.Equal(dynamicPlan) {
		t.Fatalf("typed/dynamic plans differ:\ntyped=%#v\ndynamic=%#v", typedPlan, dynamicPlan)
	}
	conditions := dynamicPlan.Conditions()
	path, ok := conditions[1].RelationPath()
	if !ok || path.TerminalScope() != query.RelationTerminalSourceKey ||
		!conditions[1].Field().Equal(query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)) {
		t.Fatalf("dynamic reviewer path = (%#v, %v)", path, ok)
	}
}

func bindRelationObjectTestFixture(t *testing.T) (
	BoundModel[relationObjectTestPost],
	BoundModel[relationObjectTestAuthor],
	RequiredForwardObject[relationObjectTestPost, relationObjectTestAuthor],
	NullableForwardObject[relationObjectTestPost, relationObjectTestAuthor],
) {
	t.Helper()
	binding := relationObjectTestBinding(t)
	post, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		relationObjectTestPostDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(post) error = %v", err)
	}
	author, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		relationObjectTestAuthorDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(author) error = %v", err)
	}
	required, err := BindRequiredForwardObject(post, "author", author)
	if err != nil {
		t.Fatalf("BindRequiredForwardObject(author) error = %v", err)
	}
	nullable, err := BindNullableForwardObject(post, "reviewer", author)
	if err != nil {
		t.Fatalf("BindNullableForwardObject(reviewer) error = %v", err)
	}
	return post, author, required, nullable
}

func relationObjectPlanIdentifier(t *testing.T, plan query.Plan) int64 {
	t.Helper()
	conditions := plan.Conditions()
	if len(conditions) != 1 || conditions[0].Lookup() != query.LookupExact || conditions[0].Field().Name() != "id" {
		t.Fatalf("target plan conditions = %#v", conditions)
	}
	identifier, ok := conditions[0].Value().Integer()
	if !ok {
		t.Fatalf("target identifier = %#v", conditions[0].Value())
	}
	return identifier
}

func assertRelationObjectLimit(t *testing.T, plan query.Plan, want int) {
	t.Helper()
	limit, ok := plan.Limit()
	if !ok || limit != want {
		t.Fatalf("plan limit = (%d, %v), want (%d, true)", limit, ok, want)
	}
}

type relationObjectAuthorBackend struct {
	mu    sync.Mutex
	calls []query.Plan
	query func(int, context.Context, query.Plan) (db.Rows, error)
}

func (backend *relationObjectAuthorBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.mu.Lock()
	call := len(backend.calls)
	backend.calls = append(backend.calls, plan)
	queryFn := backend.query
	backend.mu.Unlock()
	return queryFn(call, ctx, plan)
}

func (backend *relationObjectAuthorBackend) callCount() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.calls)
}

func (backend *relationObjectAuthorBackend) plan(index int) query.Plan {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.calls[index]
}

type relationObjectAuthorRows struct {
	values     []relationObjectTestAuthor
	position   int
	scanErr    error
	rowsErr    error
	closeErr   error
	closeCalls atomic.Uint64
}

func (rows *relationObjectAuthorRows) Next() bool {
	if rows.position >= len(rows.values) {
		return false
	}
	rows.position++
	return true
}

func (rows *relationObjectAuthorRows) Scan(destinations ...any) error {
	if rows.scanErr != nil {
		return rows.scanErr
	}
	if len(destinations) != 2 {
		return fmt.Errorf("destination count = %d, want 2", len(destinations))
	}
	id, idOK := destinations[0].(*int64)
	name, nameOK := destinations[1].(*string)
	if !idOK || !nameOK {
		return fmt.Errorf("destination types = (%T, %T)", destinations[0], destinations[1])
	}
	value := rows.values[rows.position-1]
	*id = value.ID
	*name = value.Name
	return nil
}

func (rows *relationObjectAuthorRows) Err() error { return rows.rowsErr }
func (rows *relationObjectAuthorRows) Close() error {
	rows.closeCalls.Add(1)
	return rows.closeErr
}

type relationObjectNilContext struct{}

func (*relationObjectNilContext) Deadline() (time.Time, bool) {
	panic("typed nil context must not be called")
}
func (*relationObjectNilContext) Done() <-chan struct{} {
	panic("typed nil context must not be called")
}
func (*relationObjectNilContext) Err() error    { panic("typed nil context must not be called") }
func (*relationObjectNilContext) Value(any) any { panic("typed nil context must not be called") }

type relationObjectPointerStoragePostDescriptor struct {
	relationObjectTestPostDescriptor
}

func (relationObjectPointerStoragePostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return relationObjectPointerStoragePostDescriptor{}
}

func (relationObjectPointerStoragePostDescriptor) BindRelationStorage(field ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	if field.Name != "author" {
		return nil, false
	}
	return &relationObjectTestAuthorStorage{}, true
}

type relationObjectWrongFieldPostDescriptor struct {
	relationObjectTestPostDescriptor
}

func (relationObjectWrongFieldPostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return relationObjectWrongFieldPostDescriptor{}
}

func (relationObjectWrongFieldPostDescriptor) BindRelationStorage(field ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	if field.Name != "author" {
		return nil, false
	}
	return relationObjectTestReviewerStorage{}, true
}

type relationObjectWrongValuePostDescriptor struct {
	relationObjectTestPostDescriptor
}

func (relationObjectWrongValuePostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return relationObjectWrongValuePostDescriptor{}
}

func (relationObjectWrongValuePostDescriptor) BindRelationStorage(field ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	if field.Name != "author" {
		return nil, false
	}
	return relationObjectWrongValueStorage{}, true
}

type relationObjectWrongValueStorage struct{}

func (relationObjectWrongValueStorage) Field() ir.Field {
	return relationObjectTestPostField("author")
}

func (relationObjectWrongValueStorage) Value(relationObjectTestPost) (query.Value, bool) {
	return query.Boolean(true), true
}
