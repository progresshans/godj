package orm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// The current owner descriptor implements the presence-aware sealed capability
// that generated scalar descriptors expose.
func (relationObjectTestAuthorDescriptor) PrimaryKey(value relationObjectTestAuthor) (query.Value, bool) {
	return query.Integer(value.ID), value.ID != 0
}

func TestReverseObjectRelatedSetOrdersCachesClonesAndFreshens(t *testing.T) {
	posts := bindReverseObjectTestRelation(t, "posts")
	reviewerID := int64(2)
	canonical := []relationObjectTestPost{
		{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewerID},
		{ID: 11, Title: "Beta", AuthorID: 1},
	}
	backend := &reversePostBackend{query: func(_ int, _ context.Context, _ query.Plan) (db.Rows, error) {
		return &reversePostRows{values: cloneReversePosts(canonical)}, nil
	}}

	set, err := posts.From(backend, relationObjectTestAuthor{ID: 1, Name: "Ada"})
	if err != nil {
		t.Fatalf("ReverseObject.From() error = %v", err)
	}
	if got := backend.callCount(); got != 0 {
		t.Fatalf("From() backend calls = %d, want 0", got)
	}
	first, err := set.All(context.Background())
	if err != nil || len(first) != 2 || first[0].ID != 10 || first[1].ID != 11 {
		t.Fatalf("cold RelatedSet.All() = (%#v, %v)", first, err)
	}
	first[0].ID = 999
	first[0].Title = "caller mutation"
	if first[0].ReviewerID == nil {
		t.Fatal("fixture reviewer pointer is nil")
	}
	*first[0].ReviewerID = 999
	warm, err := set.All(context.Background())
	if err != nil || len(warm) != 2 || warm[0].ID != 10 || warm[0].Title != "Alpha" ||
		warm[0].ReviewerID == nil || *warm[0].ReviewerID != 2 || warm[0].ReviewerID == first[0].ReviewerID {
		t.Fatalf("warm RelatedSet.All() exposed cache aliases: (%#v, %v)", warm, err)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("cold/warm backend calls = %d, want 1", got)
	}
	assertReverseSetPlan(t, backend.plan(0), "author", 1, "id", query.Ascending)

	fresh, err := set.Fresh()
	if err != nil || fresh == set {
		t.Fatalf("Fresh() = (%p, %v), original=%p", fresh, err, set)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("Fresh() performed backend I/O: %d calls", got)
	}
	if _, err := fresh.All(context.Background()); err != nil {
		t.Fatalf("fresh All() error = %v", err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("fresh backend calls = %d, want 2", got)
	}
	if _, err := set.All(context.Background()); err != nil {
		t.Fatalf("original warm All() error = %v", err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("Fresh() changed original cache: %d calls", got)
	}

	ordered, err := set.OrderBy(NewStringField[relationObjectTestPost](relationObjectTestPostField("title")).Desc())
	if err != nil || ordered == set {
		t.Fatalf("OrderBy() = (%p, %v), original=%p", ordered, err, set)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("OrderBy() performed backend I/O: %d calls", got)
	}
	if _, err := ordered.All(context.Background()); err != nil {
		t.Fatalf("ordered All() error = %v", err)
	}
	if got := backend.callCount(); got != 3 {
		t.Fatalf("ordered backend calls = %d, want 3", got)
	}
	assertReverseSetPlan(t, backend.plan(2), "author", 1, "title", query.Descending)
}

func TestReverseObjectNullableDeclarationUsesExactLocalForeignKey(t *testing.T) {
	reviewed := bindReverseObjectTestRelation(t, "reviewed_posts")
	backend := &reversePostBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return &reversePostRows{values: []relationObjectTestPost{{ID: 12, Title: "Gamma", AuthorID: 3}}}, nil
	}}
	set, err := reviewed.From(backend, relationObjectTestAuthor{ID: 2, Name: "Bob"})
	if err != nil {
		t.Fatalf("nullable ReverseObject.From() error = %v", err)
	}
	if _, err := set.All(context.Background()); err != nil {
		t.Fatalf("nullable RelatedSet.All() error = %v", err)
	}
	assertReverseSetPlan(t, backend.plan(0), "reviewer", 2, "id", query.Ascending)
	condition := backend.plan(0).Conditions()[0]
	if !condition.Field().Nullable() {
		t.Fatalf("nullable source predicate field = %#v", condition.Field())
	}
	if _, related := condition.RelationPath(); related {
		t.Fatal("reverse object accessor source-key query unexpectedly used a JOIN relation path")
	}
}

func TestReverseObjectPrimaryKeyCapabilityPresenceAndKindsFailBeforeIO(t *testing.T) {
	binding := relationObjectTestBinding(t)
	post, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		relationObjectTestPostDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(post) error = %v", err)
	}
	backend := &reversePostBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return &reversePostRows{}, nil
	}}

	owner, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		relationObjectTestAuthorDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(author) error = %v", err)
	}
	reverse, err := BindReverseObject(owner, "posts", post)
	if err != nil {
		t.Fatalf("BindReverseObject() error = %v", err)
	}
	if set, err := reverse.From(backend, relationObjectTestAuthor{}); set != nil {
		t.Fatalf("missing-PK From() set = %#v, want nil", set)
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeMissingPrimaryKey)
	}
	if got := backend.callCount(); got != 0 {
		t.Fatalf("missing PK performed backend I/O: %d calls", got)
	}

	zeroOwner, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		reverseZeroPresentAuthorDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(zero-present author) error = %v", err)
	}
	zeroReverse, err := BindReverseObject(zeroOwner, "posts", post)
	if err != nil {
		t.Fatalf("BindReverseObject(zero-present) error = %v", err)
	}
	zeroSet, err := zeroReverse.From(backend, relationObjectTestAuthor{ID: 0})
	if err != nil {
		t.Fatalf("present numeric-zero From() error = %v", err)
	}
	if _, err := zeroSet.All(context.Background()); err != nil {
		t.Fatalf("present numeric-zero All() error = %v", err)
	}
	assertReverseSetPlan(t, backend.plan(0), "author", 0, "id", query.Ascending)

	wrongOwner, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		reverseBooleanPrimaryKeyAuthorDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(boolean-PK author) error = %v", err)
	}
	wrongReverse, err := BindReverseObject(wrongOwner, "posts", post)
	if err != nil {
		t.Fatalf("BindReverseObject(boolean-PK) error = %v", err)
	}
	if set, err := wrongReverse.From(backend, relationObjectTestAuthor{ID: 1}); set != nil {
		t.Fatalf("wrong-kind From() set = %#v, want nil", set)
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("wrong PK kind performed backend I/O: %d total calls", got)
	}
}

func TestReverseObjectBindingSeparatesQueryAndObjectCapabilities(t *testing.T) {
	binding := relationObjectTestBinding(t)
	owner, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		reverseNoPrimaryKeyAuthorDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(no-PK owner) error = %v", err)
	}
	post, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		relationObjectTestPostDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(post) error = %v", err)
	}
	if _, err := BindReverse(owner, "posts", post); err != nil {
		t.Fatalf("query-only BindReverse() rejected no-PK owner: %v", err)
	}
	if object, err := BindReverseObject(owner, "posts", post); err == nil || object.state.valid {
		t.Fatalf("BindReverseObject(no-PK) = (%#v, %v), want zero/error", object, err)
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}

	plainPost, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		relationObjectPlainPostDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(plain post) error = %v", err)
	}
	capableOwner, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		relationObjectTestAuthorDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(capable owner) error = %v", err)
	}
	if _, err := BindReverse(capableOwner, "posts", plainPost); err != nil {
		t.Fatalf("query-only BindReverse() rejected plain source: %v", err)
	}
	_, err = BindReverseObject(capableOwner, "posts", plainPost)
	assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	otherBinding := relationObjectTestBinding(t)
	otherPost, err := BindModel(
		otherBinding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		relationObjectTestPostDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(other post) error = %v", err)
	}
	_, err = BindReverseObject(capableOwner, "posts", otherPost)
	assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
}

func TestRelatedSetSelfSentinelNilInputsAndRetryAreFailClosed(t *testing.T) {
	reverse := bindReverseObjectTestRelation(t, "posts")
	backendFailure := errors.New("backend failure")
	backend := &reversePostBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
		if call == 0 {
			return nil, backendFailure
		}
		return &reversePostRows{values: []relationObjectTestPost{{ID: 10, Title: "Alpha", AuthorID: 1}}}, nil
	}}
	set, err := reverse.From(backend, relationObjectTestAuthor{ID: 1})
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}
	if _, err := set.All(context.Background()); !errors.Is(err, backendFailure) {
		t.Fatalf("first All() error = %v, want %v", err, backendFailure)
	}
	if values, err := set.All(context.Background()); err != nil || len(values) != 1 || values[0].ID != 10 {
		t.Fatalf("retry All() = (%#v, %v)", values, err)
	}
	if _, err := set.All(context.Background()); err != nil {
		t.Fatalf("warm retry All() error = %v", err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("failure/retry/cache backend calls = %d, want 2", got)
	}

	var nilSet *RelatedSet[relationObjectTestPost]
	var zeroSet RelatedSet[relationObjectTestPost]
	copySet := *set
	for name, candidate := range map[string]*RelatedSet[relationObjectTestPost]{
		"nil":    nilSet,
		"zero":   &zeroSet,
		"copied": &copySet,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := candidate.All(context.Background())
			assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
			_, err = candidate.Fresh()
			assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
			_, err = candidate.OrderBy()
			assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
		})
	}
	var typedNilContext *relationObjectNilContext
	_, err = set.All(typedNilContext)
	assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	var typedNilBackend *reversePostBackend
	if value, err := reverse.From(typedNilBackend, relationObjectTestAuthor{ID: 1}); value != nil {
		t.Fatalf("typed-nil backend set = %#v, want nil", value)
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryBackend, query.CodeInvalidPlan)
	}
	var zeroReverse ReverseObject[relationObjectTestAuthor, relationObjectTestPost]
	if value, err := zeroReverse.From(backend, relationObjectTestAuthor{ID: 1}); value != nil {
		t.Fatalf("zero reverse set = %#v, want nil", value)
	} else {
		assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	}
}

func TestRelatedSetConcurrentColdAllSingleflights(t *testing.T) {
	reverse := bindReverseObjectTestRelation(t, "posts")
	started := make(chan struct{})
	release := make(chan struct{})
	backend := &reversePostBackend{query: func(call int, ctx context.Context, _ query.Plan) (db.Rows, error) {
		if call != 0 {
			return nil, fmt.Errorf("duplicate backend call %d", call)
		}
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &reversePostRows{values: []relationObjectTestPost{{ID: 10, Title: "Alpha", AuthorID: 1}}}, nil
	}}
	set, err := reverse.From(backend, relationObjectTestAuthor{ID: 1})
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}
	const callers = 16
	results := make(chan error, callers)
	go func() {
		values, err := set.All(context.Background())
		if err == nil && (len(values) != 1 || values[0].ID != 10) {
			err = fmt.Errorf("values = %#v", values)
		}
		results <- err
	}()
	<-started
	for index := 1; index < callers; index++ {
		go func() {
			values, err := set.All(context.Background())
			if err == nil && (len(values) != 1 || values[0].ID != 10) {
				err = fmt.Errorf("values = %#v", values)
			}
			results <- err
		}()
	}
	close(release)
	for index := 0; index < callers; index++ {
		if err := <-results; err != nil {
			t.Errorf("All() error = %v", err)
		}
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("concurrent backend calls = %d, want 1", got)
	}
}

func TestRelatedSetWaiterCancellationDoesNotCancelOwner(t *testing.T) {
	reverse := bindReverseObjectTestRelation(t, "posts")
	started := make(chan struct{})
	release := make(chan struct{})
	backend := &reversePostBackend{query: func(_ int, ctx context.Context, _ query.Plan) (db.Rows, error) {
		close(started)
		select {
		case <-release:
			return &reversePostRows{values: []relationObjectTestPost{{ID: 10, Title: "Alpha", AuthorID: 1}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	set, err := reverse.From(backend, relationObjectTestAuthor{ID: 1})
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}

	ownerResult := make(chan error, 1)
	go func() {
		_, err := set.All(context.Background())
		ownerResult <- err
	}()
	awaitSignal(t, started, "related-set owner backend start")

	waiterBase, cancelWaiter := context.WithCancel(context.Background())
	waiterCtx, waiterEntered := newEnteredContext(waiterBase)
	waiterResult := make(chan error, 1)
	go func() {
		_, err := set.All(waiterCtx)
		waiterResult <- err
	}()
	awaitEntered(t, waiterEntered)
	cancelWaiter()
	if err := awaitValue(t, waiterResult, "related-set canceled waiter"); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v, want context.Canceled", err)
	}
	select {
	case err := <-ownerResult:
		t.Fatalf("waiter cancellation completed owner early: %v", err)
	default:
	}
	close(release)
	if err := awaitValue(t, ownerResult, "related-set owner result"); err != nil {
		t.Fatalf("owner error = %v", err)
	}
	if values, err := set.All(context.Background()); err != nil || len(values) != 1 || values[0].ID != 10 {
		t.Fatalf("warm All() = (%#v, %v)", values, err)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("waiter cancellation/backend calls = %d, want 1", got)
	}
}

func TestRelatedSetOwnerCancellationLetsLiveWaiterRetry(t *testing.T) {
	reverse := bindReverseObjectTestRelation(t, "posts")
	started := make(chan struct{})
	backend := &reversePostBackend{query: func(call int, ctx context.Context, _ query.Plan) (db.Rows, error) {
		if call == 0 {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &reversePostRows{values: []relationObjectTestPost{{ID: 10, Title: "Alpha", AuthorID: 1}}}, nil
	}}
	set, err := reverse.From(backend, relationObjectTestAuthor{ID: 1})
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}

	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	ownerResult := make(chan error, 1)
	go func() {
		_, err := set.All(ownerCtx)
		ownerResult <- err
	}()
	awaitSignal(t, started, "related-set canceled owner backend start")

	type allResult struct {
		values []relationObjectTestPost
		err    error
	}
	waiterCtx, waiterEntered := newEnteredContext(context.Background())
	waiterResult := make(chan allResult, 1)
	go func() {
		values, err := set.All(waiterCtx)
		waiterResult <- allResult{values: values, err: err}
	}()
	awaitEntered(t, waiterEntered)
	cancelOwner()
	if err := awaitValue(t, ownerResult, "related-set canceled owner result"); !errors.Is(err, context.Canceled) {
		t.Fatalf("owner error = %v, want context.Canceled", err)
	}
	waiter := awaitValue(t, waiterResult, "related-set retrying waiter result")
	if waiter.err != nil || len(waiter.values) != 1 || waiter.values[0].ID != 10 {
		t.Fatalf("live waiter result = %#v", waiter)
	}
	if values, err := set.All(context.Background()); err != nil || len(values) != 1 || values[0].ID != 10 {
		t.Fatalf("warm All() = (%#v, %v)", values, err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("owner cancellation/backend calls = %d, want 2", got)
	}
}

func TestRelatedSetScanRowsAndCloseFailuresRemainRetryable(t *testing.T) {
	reverse := bindReverseObjectTestRelation(t, "posts")
	tests := []struct {
		name      string
		configure func(*reversePostRows, error)
	}{
		{name: "scan", configure: func(rows *reversePostRows, failure error) { rows.scanErr = failure }},
		{name: "rows", configure: func(rows *reversePostRows, failure error) { rows.rowsErr = failure }},
		{name: "close", configure: func(rows *reversePostRows, failure error) { rows.closeErr = failure }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New(test.name + " failure")
			failedRows := &reversePostRows{values: []relationObjectTestPost{{ID: 10, Title: "failed", AuthorID: 1}}}
			test.configure(failedRows, failure)
			successRows := &reversePostRows{values: []relationObjectTestPost{{ID: 10, Title: "Alpha", AuthorID: 1}}}
			backend := &reversePostBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call == 0 {
					return failedRows, nil
				}
				return successRows, nil
			}}
			set, err := reverse.From(backend, relationObjectTestAuthor{ID: 1})
			if err != nil {
				t.Fatalf("From() error = %v", err)
			}
			if _, err := set.All(context.Background()); !errors.Is(err, failure) {
				t.Fatalf("first All() error = %v, want preserved %v", err, failure)
			}
			values, err := set.All(context.Background())
			if err != nil || len(values) != 1 || values[0] != (relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1}) {
				t.Fatalf("retry All() = (%#v, %v)", values, err)
			}
			if _, err := set.All(context.Background()); err != nil {
				t.Fatalf("warm All() error = %v", err)
			}
			if got := backend.callCount(); got != 2 {
				t.Fatalf("%s failure/backend calls = %d, want 2", test.name, got)
			}
			if got := failedRows.closeCalls.Load(); got != 1 {
				t.Fatalf("%s failure/failed Rows.Close calls = %d, want 1", test.name, got)
			}
			if got := successRows.closeCalls.Load(); got != 1 {
				t.Fatalf("%s failure/success Rows.Close calls = %d, want 1", test.name, got)
			}
		})
	}
}

func bindReverseObjectTestRelation(
	t *testing.T,
	reverseName string,
) ReverseObject[relationObjectTestAuthor, relationObjectTestPost] {
	t.Helper()
	post, author, _, _ := bindRelationObjectTestFixture(t)
	relation, err := BindReverseObject(author, reverseName, post)
	if err != nil {
		t.Fatalf("BindReverseObject(%s) error = %v", reverseName, err)
	}
	return relation
}

func assertReverseSetPlan(
	t *testing.T,
	plan query.Plan,
	wantField string,
	wantIdentifier int64,
	wantOrdering string,
	wantDirection query.Direction,
) {
	t.Helper()
	if plan.Table() != "blog_post" {
		t.Fatalf("plan table = %q, want blog_post", plan.Table())
	}
	conditions := plan.Conditions()
	if len(conditions) != 1 || conditions[0].Field().Name() != wantField ||
		conditions[0].Lookup() != query.LookupExact {
		t.Fatalf("plan conditions = %#v", conditions)
	}
	identifier, ok := conditions[0].Value().Integer()
	if !ok || identifier != wantIdentifier {
		t.Fatalf("condition identifier = (%d, %v), want %d", identifier, ok, wantIdentifier)
	}
	orderings := plan.Orderings()
	if len(orderings) != 1 || orderings[0].Field().Name() != wantOrdering ||
		orderings[0].Direction() != wantDirection {
		t.Fatalf("plan orderings = %#v", orderings)
	}
}

type reversePostBackend struct {
	mu    sync.Mutex
	calls []query.Plan
	query func(int, context.Context, query.Plan) (db.Rows, error)
}

func (backend *reversePostBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.mu.Lock()
	call := len(backend.calls)
	backend.calls = append(backend.calls, plan)
	queryFn := backend.query
	backend.mu.Unlock()
	return queryFn(call, ctx, plan)
}

func (backend *reversePostBackend) callCount() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.calls)
}

func (backend *reversePostBackend) plan(index int) query.Plan {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.calls[index]
}

type reversePostRows struct {
	values     []relationObjectTestPost
	position   int
	scanErr    error
	rowsErr    error
	closeErr   error
	closeCalls atomic.Uint64
}

func (rows *reversePostRows) Next() bool {
	if rows.position >= len(rows.values) {
		return false
	}
	rows.position++
	return true
}

func (rows *reversePostRows) Scan(destinations ...any) error {
	if rows.scanErr != nil {
		return rows.scanErr
	}
	if len(destinations) != 4 {
		return fmt.Errorf("destination count = %d, want 4", len(destinations))
	}
	id, idOK := destinations[0].(*int64)
	title, titleOK := destinations[1].(*string)
	authorID, authorOK := destinations[2].(*int64)
	reviewerID, reviewerOK := destinations[3].(**int64)
	if !idOK || !titleOK || !authorOK || !reviewerOK {
		return fmt.Errorf("destination types = (%T, %T, %T, %T)", destinations[0], destinations[1], destinations[2], destinations[3])
	}
	value := rows.values[rows.position-1]
	*id = value.ID
	*title = value.Title
	*authorID = value.AuthorID
	if value.ReviewerID == nil {
		*reviewerID = nil
	} else {
		cloned := *value.ReviewerID
		*reviewerID = &cloned
	}
	return nil
}

func (rows *reversePostRows) Err() error { return rows.rowsErr }
func (rows *reversePostRows) Close() error {
	rows.closeCalls.Add(1)
	return rows.closeErr
}

func cloneReversePosts(values []relationObjectTestPost) []relationObjectTestPost {
	clones := make([]relationObjectTestPost, len(values))
	for index := range values {
		clones[index] = relationObjectTestPostDescriptor{}.CloneModel(values[index])
	}
	return clones
}

type reverseNoPrimaryKeyAuthorDescriptor struct{}

func (reverseNoPrimaryKeyAuthorDescriptor) Metadata() ir.Model {
	return relationObjectTestAuthorDescriptor{}.Metadata()
}
func (reverseNoPrimaryKeyAuthorDescriptor) Scan(row db.Row) (relationObjectTestAuthor, error) {
	return relationObjectTestAuthorDescriptor{}.Scan(row)
}
func (reverseNoPrimaryKeyAuthorDescriptor) CloneModel(value relationObjectTestAuthor) relationObjectTestAuthor {
	return value
}
func (reverseNoPrimaryKeyAuthorDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestAuthor] {
	return reverseNoPrimaryKeyAuthorDescriptor{}
}
func (reverseNoPrimaryKeyAuthorDescriptor) BindRelationStorage(ir.Field) (RelationStorage[relationObjectTestAuthor], bool) {
	return nil, false
}

type reverseZeroPresentAuthorDescriptor struct {
	relationObjectTestAuthorDescriptor
}

func (reverseZeroPresentAuthorDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestAuthor] {
	return reverseZeroPresentAuthorDescriptor{}
}
func (reverseZeroPresentAuthorDescriptor) PrimaryKey(value relationObjectTestAuthor) (query.Value, bool) {
	return query.Integer(value.ID), true
}

type reverseBooleanPrimaryKeyAuthorDescriptor struct {
	relationObjectTestAuthorDescriptor
}

func (reverseBooleanPrimaryKeyAuthorDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestAuthor] {
	return reverseBooleanPrimaryKeyAuthorDescriptor{}
}
func (reverseBooleanPrimaryKeyAuthorDescriptor) PrimaryKey(relationObjectTestAuthor) (query.Value, bool) {
	return query.Boolean(true), true
}
