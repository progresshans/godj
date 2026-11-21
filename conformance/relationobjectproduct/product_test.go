package relationobjectproduct

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/relationstate"
	"github.com/progresshans/godj/conformance/relationfixture/authors"
	"github.com/progresshans/godj/conformance/relationfixture/blog"
	"github.com/progresshans/godj/conformance/relationfixture/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

func TestObserveExecutesExactREL003AndREL006CasesAndDatabaseState(t *testing.T) {
	t.Parallel()

	got, err := observeCases(context.Background(), defaultFixtureConfig())
	if err != nil {
		t.Fatal(err)
	}
	wantCold := relationstate.QueryMetrics{QueryCount: 1, StatementKinds: []string{"SELECT"}}
	wantWarm := relationstate.QueryMetrics{}
	if got.Forward.Cold != (relationstate.AuthorRow{ID: 1, Name: "Ada"}) || got.Forward.Warm != got.Forward.Cold {
		t.Fatalf("forward result = %#v", got.Forward)
	}
	wantSteps := []AccessStep{
		{Name: "cold_access", Metrics: wantCold},
		{Name: "warm_access", Metrics: wantWarm},
	}
	if !reflect.DeepEqual(got.Forward.Steps, wantSteps) {
		t.Fatalf("forward steps = %#v, want %#v", got.Forward.Steps, wantSteps)
	}
	if got.Nullable.Reviewer != nil || !reflect.DeepEqual(got.Nullable.IsNullPostIDs, []int64{11}) {
		t.Fatalf("nullable result = %#v", got.Nullable)
	}
	if !reflect.DeepEqual(got.Nullable.NullAccess, wantWarm) ||
		!reflect.DeepEqual(got.Nullable.IsNullConstruction, wantWarm) ||
		!reflect.DeepEqual(got.Nullable.IsNullEvaluation, wantCold) {
		t.Fatalf("nullable metrics = %#v", got.Nullable)
	}
	reviewer := int64(2)
	wantState := relationstate.DatabaseState{
		Authors: []relationstate.AuthorRow{{ID: 1, Name: "Ada"}, {ID: 2, Name: "Bob"}, {ID: 3, Name: "Cleo"}},
		Posts: []relationstate.PostRow{
			{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewer},
			{ID: 11, Title: "Beta", AuthorID: 1},
			{ID: 12, Title: "Gamma", AuthorID: 3, ReviewerID: &reviewer},
		},
	}
	if !reflect.DeepEqual(got.DBState, wantState) {
		t.Fatalf("database state = %#v, want %#v", got.DBState, wantState)
	}
}

func TestObjectObserversDoNotLoadSiblingSources(t *testing.T) {
	t.Parallel()
	config := defaultFixtureConfig()
	config.nullablePost = -1
	if _, err := observe(context.Background(), config, observeForward); err != nil {
		t.Fatalf("forward observer loaded the missing nullable source: %v", err)
	}
	config = defaultFixtureConfig()
	config.loadPostID = -1
	if _, err := observe(context.Background(), config, observeNullable); err != nil {
		t.Fatalf("nullable observer loaded the missing forward source: %v", err)
	}
}

func TestObservationChangesForEachOwnedREL003AndREL006Mutation(t *testing.T) {
	t.Parallel()

	base, err := observeCases(context.Background(), defaultFixtureConfig())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*fixtureConfig)
		check  func(*testing.T, objectCases)
	}{
		{
			name: "target name",
			mutate: func(config *fixtureConfig) {
				config.authors[0].Name = "Adele"
			},
			check: func(t *testing.T, got objectCases) {
				if got.Forward.Cold.Name != "Adele" || got.Forward.Warm.Name != "Adele" {
					t.Fatalf("mutated author names = %#v", got.Forward)
				}
			},
		},
		{
			name: "required source key",
			mutate: func(config *fixtureConfig) {
				config.posts[0].AuthorID = 3
			},
			check: func(t *testing.T, got objectCases) {
				if got.Forward.Cold.ID != 3 || got.Forward.Warm.ID != 3 {
					t.Fatalf("mutated author rows = %#v", got.Forward)
				}
			},
		},
		{
			name: "nullable source key",
			mutate: func(config *fixtureConfig) {
				reviewer := int64(2)
				config.posts[1].ReviewerID = &reviewer
			},
			check: func(t *testing.T, got objectCases) {
				if got.Nullable.Reviewer == nil || got.Nullable.Reviewer.ID != 2 || len(got.Nullable.IsNullPostIDs) != 0 {
					t.Fatalf("mutated nullable observation = %#v", got.Nullable)
				}
			},
		},
		{
			name: "isnull Boolean",
			mutate: func(config *fixtureConfig) {
				config.isNullValue = false
			},
			check: func(t *testing.T, got objectCases) {
				if !reflect.DeepEqual(got.Nullable.IsNullPostIDs, []int64{10, 12}) {
					t.Fatalf("mutated isnull IDs = %v", got.Nullable.IsNullPostIDs)
				}
			},
		},
		{
			name: "result ordering",
			mutate: func(config *fixtureConfig) {
				config.posts[2].ReviewerID = nil
				config.descending = true
			},
			check: func(t *testing.T, got objectCases) {
				if !reflect.DeepEqual(got.Nullable.IsNullPostIDs, []int64{12, 11}) {
					t.Fatalf("mutated ordered IDs = %v", got.Nullable.IsNullPostIDs)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			got, err := observeCases(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, got)
			if reflect.DeepEqual(got, base) {
				t.Fatal("owned mutation left the complete observation unchanged")
			}
		})
	}
}

func TestGeneratedObjectWrapperFreshAliasCloneAndCopyBoundaries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "godj-rel-object-boundary-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	seedConfig := defaultFixtureConfig()
	if err := relationstate.Provision(ctx, backend, "REL-003/006", seedConfig.authors, seedConfig.posts); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingQueryer{backend: backend}
	post, err := loadPost(ctx, recorder, 10)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	object, err := objects.BlogPost.From(recorder, post)
	if err != nil {
		t.Fatal(err)
	}
	start := recorder.mark()
	alias := object
	if _, err := object.Author(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := alias.Author(ctx); err != nil {
		t.Fatal(err)
	}
	if got := recorder.metricsSince(start).QueryCount; got != 1 {
		t.Fatalf("pointer alias cold/warm query count = %d, want 1", got)
	}
	fresh, err := object.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Author(ctx); err != nil {
		t.Fatal(err)
	}
	separate, err := objects.BlogPost.From(recorder, post)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := separate.Author(ctx); err != nil {
		t.Fatal(err)
	}
	if got := recorder.metricsSince(start).QueryCount; got != 3 {
		t.Fatalf("alias/Fresh/separate query count = %d, want 3", got)
	}

	model, err := object.Model()
	if err != nil {
		t.Fatal(err)
	}
	if model.ReviewerID == nil {
		t.Fatal("model reviewer key unexpectedly nil")
	}
	*model.ReviewerID = 99
	again, err := object.Model()
	if err != nil || again.ReviewerID == nil || *again.ReviewerID != 2 {
		t.Fatalf("second Model() = (%#v, %v), want cloned reviewer 2", again, err)
	}

	copyOfObject := *object
	if _, err := copyOfObject.Author(ctx); !hasQueryCode(err, query.CodeInvalidPlan) {
		t.Fatalf("copied wrapper Author() error = %v, want invalid_plan", err)
	}
	if fresh, err := copyOfObject.Fresh(); fresh != nil || !hasQueryCode(err, query.CodeInvalidPlan) {
		t.Fatalf("copied wrapper Fresh() = (%#v, %v), want nil/invalid_plan", fresh, err)
	}
	var zero project.BlogPostObject
	if _, _, err := zero.Reviewer(ctx); !hasQueryCode(err, query.CodeInvalidPlan) {
		t.Fatalf("zero wrapper Reviewer() error = %v, want invalid_plan", err)
	}
	var nilObject *project.BlogPostObject
	if _, err := nilObject.Model(); !hasQueryCode(err, query.CodeInvalidPlan) {
		t.Fatalf("nil wrapper Model() error = %v, want invalid_plan", err)
	}
}

func TestGeneratedObjectWrapperCapturesBackendLifetime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}

	coldBackend, err := sqlite.OpenMemory(ctx, "godj-rel-object-cold-lifetime-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	coldSeedConfig := defaultFixtureConfig()
	if err := relationstate.Provision(ctx, coldBackend, "REL-003/006", coldSeedConfig.authors, coldSeedConfig.posts); err != nil {
		t.Fatal(err)
	}
	coldPost, err := loadPost(ctx, coldBackend, 10)
	if err != nil {
		t.Fatal(err)
	}
	coldObject, err := objects.BlogPost.From(coldBackend, coldPost)
	if err != nil {
		t.Fatal(err)
	}
	if err := coldBackend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := coldObject.Author(ctx); err == nil {
		t.Fatal("cold object access unexpectedly survived its captured backend close")
	}

	warmBackend, err := sqlite.OpenMemory(ctx, "godj-rel-object-warm-lifetime-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	warmSeedConfig := defaultFixtureConfig()
	if err := relationstate.Provision(ctx, warmBackend, "REL-003/006", warmSeedConfig.authors, warmSeedConfig.posts); err != nil {
		t.Fatal(err)
	}
	warmPost, err := loadPost(ctx, warmBackend, 10)
	if err != nil {
		t.Fatal(err)
	}
	warmObject, err := objects.BlogPost.From(warmBackend, warmPost)
	if err != nil {
		t.Fatal(err)
	}
	if author, err := warmObject.Author(ctx); err != nil || author.ID != 1 {
		t.Fatalf("populate warm object = (%#v, %v)", author, err)
	}
	if err := warmBackend.Close(); err != nil {
		t.Fatal(err)
	}
	if author, err := warmObject.Author(ctx); err != nil || author.ID != 1 {
		t.Fatalf("warm object after backend close = (%#v, %v)", author, err)
	}
	fresh, err := warmObject.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Author(ctx); err == nil {
		t.Fatal("Fresh unexpectedly revived a closed captured backend")
	}

	otherBackend, err := sqlite.OpenMemory(ctx, "godj-rel-object-other-lifetime-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := otherBackend.Close(); err != nil {
			t.Errorf("Close(other backend) error = %v", err)
		}
	})
	otherSeedConfig := defaultFixtureConfig()
	if err := relationstate.Provision(ctx, otherBackend, "REL-003/006", otherSeedConfig.authors, otherSeedConfig.posts); err != nil {
		t.Fatal(err)
	}
	otherObject, err := objects.BlogPost.From(otherBackend, warmPost)
	if err != nil {
		t.Fatal(err)
	}
	if author, err := otherObject.Author(ctx); err != nil || author.ID != 1 {
		t.Fatalf("new From on other backend = (%#v, %v)", author, err)
	}
}

func TestGeneratedObjectWrapperSingleflightsAndSeparatesCancellation(t *testing.T) {
	t.Run("concurrent cold access", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		var startedOnce sync.Once
		backend := &productAuthorBackend{run: func(_ int, ctx context.Context, _ query.Plan) (db.Rows, error) {
			startedOnce.Do(func() { close(started) })
			select {
			case <-release:
				return productAuthorRows(authors.Author{ID: 1, Name: "Ada"}), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}
		object := newProductBlogPostObject(t, backend)

		const callers = 16
		results := make(chan productAuthorResult, callers)
		go func() {
			value, err := object.Author(context.Background())
			results <- productAuthorResult{value: value, err: err}
		}()
		awaitProductSignal(t, started, "generated owner query")
		for index := 1; index < callers; index++ {
			waiterCtx, entered := newProductEnteredContext(context.Background())
			go func() {
				value, err := object.Author(waiterCtx)
				results <- productAuthorResult{value: value, err: err}
			}()
			awaitProductSignal(t, entered, fmt.Sprintf("generated waiter %d", index))
		}
		close(release)
		for index := 0; index < callers; index++ {
			result := awaitProductValue(t, results, "generated concurrent result")
			assertProductAuthor(t, result.value, result.err, 1, "Ada")
		}
		assertProductAuthorCallCount(t, backend, 1)
		assertProductAuthorPlan(t, backend.plan(0))
		value, err := object.Author(context.Background())
		assertProductAuthor(t, value, err, 1, "Ada")
		assertProductAuthorCallCount(t, backend, 1)
	})

	t.Run("waiter cancellation leaves owner live", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		var startedOnce sync.Once
		backend := &productAuthorBackend{run: func(_ int, ctx context.Context, _ query.Plan) (db.Rows, error) {
			startedOnce.Do(func() { close(started) })
			select {
			case <-release:
				return productAuthorRows(authors.Author{ID: 1, Name: "Ada"}), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}
		object := newProductBlogPostObject(t, backend)
		ownerResult := make(chan productAuthorResult, 1)
		go func() {
			value, err := object.Author(context.Background())
			ownerResult <- productAuthorResult{value: value, err: err}
		}()
		awaitProductSignal(t, started, "generated owner query")

		waiterBase, cancelWaiter := context.WithCancel(context.Background())
		waiterCtx, entered := newProductEnteredContext(waiterBase)
		waiterResult := make(chan error, 1)
		go func() {
			_, err := object.Author(waiterCtx)
			waiterResult <- err
		}()
		awaitProductSignal(t, entered, "generated canceled waiter entry")
		cancelWaiter()
		if err := awaitProductValue(t, waiterResult, "generated canceled waiter"); !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter error = %v, want context.Canceled", err)
		}
		select {
		case result := <-ownerResult:
			t.Fatalf("waiter cancellation completed generated owner early: %#v", result)
		default:
		}
		close(release)
		owner := awaitProductValue(t, ownerResult, "generated owner completion")
		assertProductAuthor(t, owner.value, owner.err, 1, "Ada")
		assertProductAuthorCallCount(t, backend, 1)
		value, err := object.Author(context.Background())
		assertProductAuthor(t, value, err, 1, "Ada")
		assertProductAuthorCallCount(t, backend, 1)
	})

	t.Run("owner cancellation lets waiter retry", func(t *testing.T) {
		started := make(chan struct{})
		backend := &productAuthorBackend{run: func(call int, ctx context.Context, _ query.Plan) (db.Rows, error) {
			if call == 0 {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return productAuthorRows(authors.Author{ID: 1, Name: "Ada"}), nil
		}}
		object := newProductBlogPostObject(t, backend)
		ownerCtx, cancelOwner := context.WithCancel(context.Background())
		ownerResult := make(chan error, 1)
		go func() {
			_, err := object.Author(ownerCtx)
			ownerResult <- err
		}()
		awaitProductSignal(t, started, "generated canceled owner query")

		waiterCtx, entered := newProductEnteredContext(context.Background())
		waiterResult := make(chan productAuthorResult, 1)
		go func() {
			value, err := object.Author(waiterCtx)
			waiterResult <- productAuthorResult{value: value, err: err}
		}()
		awaitProductSignal(t, entered, "generated retrying waiter entry")
		cancelOwner()
		if err := awaitProductValue(t, ownerResult, "generated canceled owner"); !errors.Is(err, context.Canceled) {
			t.Fatalf("owner error = %v, want context.Canceled", err)
		}
		waiter := awaitProductValue(t, waiterResult, "generated retrying waiter")
		assertProductAuthor(t, waiter.value, waiter.err, 1, "Ada")
		assertProductAuthorCallCount(t, backend, 2)
		for index := 0; index < 2; index++ {
			assertProductAuthorPlan(t, backend.plan(index))
		}
		value, err := object.Author(context.Background())
		assertProductAuthor(t, value, err, 1, "Ada")
		assertProductAuthorCallCount(t, backend, 2)
	})
}

func TestGeneratedObjectWrapperRetriesBackendScanRowsAndCloseFailures(t *testing.T) {
	t.Run("backend", func(t *testing.T) {
		failure := errors.New("backend failure")
		backend := &productAuthorBackend{run: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
			if call == 0 {
				return nil, failure
			}
			return productAuthorRows(authors.Author{ID: 1, Name: "Ada"}), nil
		}}
		object := newProductBlogPostObject(t, backend)
		if _, err := object.Author(context.Background()); !errors.Is(err, failure) {
			t.Fatalf("first generated Author() error = %v, want %v", err, failure)
		}
		value, err := object.Author(context.Background())
		assertProductAuthor(t, value, err, 1, "Ada")
		value, err = object.Author(context.Background())
		assertProductAuthor(t, value, err, 1, "Ada")
		assertProductAuthorCallCount(t, backend, 2)
		for index := 0; index < 2; index++ {
			assertProductAuthorPlan(t, backend.plan(index))
		}
	})

	for _, test := range []struct {
		name      string
		configure func(*productAuthorRowSet, error)
	}{
		{name: "scan", configure: func(rows *productAuthorRowSet, failure error) { rows.scanErr = failure }},
		{name: "rows", configure: func(rows *productAuthorRowSet, failure error) { rows.rowsErr = failure }},
		{name: "close", configure: func(rows *productAuthorRowSet, failure error) { rows.closeErr = failure }},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New(test.name + " failure")
			failedRows := productAuthorRows(authors.Author{ID: 1, Name: "failed"})
			test.configure(failedRows, failure)
			successRows := productAuthorRows(authors.Author{ID: 1, Name: "Ada"})
			backend := &productAuthorBackend{run: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call == 0 {
					return failedRows, nil
				}
				return successRows, nil
			}}
			object := newProductBlogPostObject(t, backend)
			if _, err := object.Author(context.Background()); !errors.Is(err, failure) {
				t.Fatalf("first generated Author() error = %v, want %v", err, failure)
			}
			value, err := object.Author(context.Background())
			assertProductAuthor(t, value, err, 1, "Ada")
			value, err = object.Author(context.Background())
			assertProductAuthor(t, value, err, 1, "Ada")
			assertProductAuthorCallCount(t, backend, 2)
			if got := failedRows.closeCalls.Load() + successRows.closeCalls.Load(); got != 2 {
				t.Fatalf("%s generated rows Close calls = %d, want 2", test.name, got)
			}
			for index := 0; index < 2; index++ {
				assertProductAuthorPlan(t, backend.plan(index))
			}
		})
	}
}

func TestGeneratedObjectWrapperCachesMissingAndCardinalitySnapshots(t *testing.T) {
	for _, test := range []struct {
		name     string
		values   []authors.Author
		category string
		code     string
	}{
		{name: "missing", category: query.CategoryModelState, code: query.CodeRelatedObjectMissing},
		{
			name: "cardinality",
			values: []authors.Author{
				{ID: 1, Name: "Ada"},
				{ID: 1, Name: "Duplicate"},
			},
			category: query.CategoryIntegrity,
			code:     query.CodeRelatedObjectCardinality,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &productAuthorBackend{run: func(_ int, _ context.Context, _ query.Plan) (db.Rows, error) {
				return productAuthorRows(test.values...), nil
			}}
			object := newProductBlogPostObject(t, backend)
			for call := 0; call < 2; call++ {
				_, err := object.Author(context.Background())
				assertProductQueryError(t, err, test.category, test.code)
			}
			assertProductAuthorCallCount(t, backend, 1)
			assertProductAuthorPlan(t, backend.plan(0))
		})
	}
}

type productAuthorResult struct {
	value authors.Author
	err   error
}

type productAuthorBackend struct {
	mu    sync.Mutex
	calls []query.Plan
	run   func(int, context.Context, query.Plan) (db.Rows, error)
}

func (backend *productAuthorBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.mu.Lock()
	call := len(backend.calls)
	backend.calls = append(backend.calls, plan)
	run := backend.run
	backend.mu.Unlock()
	return run(call, ctx, plan)
}

func (backend *productAuthorBackend) callCount() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.calls)
}

func (backend *productAuthorBackend) plan(index int) query.Plan {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.calls[index]
}

type productAuthorRowSet struct {
	values     []authors.Author
	position   int
	scanErr    error
	rowsErr    error
	closeErr   error
	closeCalls atomic.Uint64
}

func productAuthorRows(values ...authors.Author) *productAuthorRowSet {
	return &productAuthorRowSet{values: append([]authors.Author(nil), values...)}
}

func (rows *productAuthorRowSet) Next() bool {
	if rows.position >= len(rows.values) {
		return false
	}
	rows.position++
	return true
}

func (rows *productAuthorRowSet) Scan(destinations ...any) error {
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

func (rows *productAuthorRowSet) Err() error { return rows.rowsErr }

func (rows *productAuthorRowSet) Close() error {
	rows.closeCalls.Add(1)
	return rows.closeErr
}

type productEnteredContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func newProductEnteredContext(parent context.Context) (*productEnteredContext, <-chan struct{}) {
	ctx := &productEnteredContext{Context: parent, entered: make(chan struct{})}
	return ctx, ctx.entered
}

func (ctx *productEnteredContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.entered) })
	return ctx.Context.Done()
}

func newProductBlogPostObject(t *testing.T, backend db.Queryer) *project.BlogPostObject {
	t.Helper()
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	object, err := objects.BlogPost.From(backend, blog.Post{
		ID:       10,
		Title:    "Alpha",
		AuthorID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return object
}

func assertProductAuthor(t *testing.T, value authors.Author, err error, wantID int64, wantName string) {
	t.Helper()
	if err != nil || value.ID != wantID || value.Name != wantName {
		t.Fatalf("generated Author() = (%#v, %v), want (%d, %q, nil)", value, err, wantID, wantName)
	}
}

func assertProductAuthorCallCount(t *testing.T, backend *productAuthorBackend, want int) {
	t.Helper()
	if got := backend.callCount(); got != want {
		t.Fatalf("generated author backend calls = %d, want %d", got, want)
	}
}

func assertProductAuthorPlan(t *testing.T, plan query.Plan) {
	t.Helper()
	if plan.Table() != "authors_author" {
		t.Fatalf("generated author plan table = %q, want authors_author", plan.Table())
	}
	columns := plan.SourceFields()
	if len(columns) != 2 || columns[0].Name() != "id" || columns[1].Name() != "name" {
		t.Fatalf("generated author plan columns = %#v", columns)
	}
	conditions := plan.Conditions()
	if len(conditions) != 1 || conditions[0].Lookup() != query.LookupExact || conditions[0].Field().Name() != "id" {
		t.Fatalf("generated author plan conditions = %#v", conditions)
	}
	identifier, ok := conditions[0].Value().Integer()
	if !ok || identifier != 1 {
		t.Fatalf("generated author plan identifier = (%d, %v), want (1, true)", identifier, ok)
	}
	limit, ok := plan.Limit()
	if !ok || limit != 2 {
		t.Fatalf("generated author plan limit = (%d, %v), want (2, true)", limit, ok)
	}
}

func assertProductQueryError(t *testing.T, err error, category, code string) {
	t.Helper()
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Category != category || queryError.Code != code {
		t.Fatalf("generated relation error = %v, want %s/%s", err, category, code)
	}
}

func awaitProductSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func awaitProductValue[T any](t *testing.T, values <-chan T, label string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		var zero T
		return zero
	}
}

func hasQueryCode(err error, code string) bool {
	var queryError *query.Error
	return errors.As(err, &queryError) && queryError.Code == code
}

// observeCases is the explicit unit-test suite. Each case calls its own
// production operation with a fresh database and a separately read state.
type objectCases struct {
	Forward  ForwardCacheObservation
	Nullable NullableObservation
	DBState  relationstate.DatabaseState
}

func observeCases(ctx context.Context, config fixtureConfig) (objectCases, error) {
	forward, err := observe(ctx, config, observeForward)
	if err != nil {
		return objectCases{}, err
	}
	nullable, err := observe(ctx, config, observeNullable)
	if err != nil {
		return objectCases{}, err
	}
	if !reflect.DeepEqual(forward.DBState, nullable.DBState) {
		return objectCases{}, errors.New("independent observer fixtures have different actual final states")
	}
	return objectCases{Forward: forward.Result, Nullable: nullable.Result, DBState: forward.DBState}, nil
}
