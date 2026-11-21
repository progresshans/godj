package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func multipleSelectBindings(t *testing.T) (RelatedSelect[relationObjectTestPost, relationObjectTestAuthor], RelatedSelect[relationObjectTestPost, relationObjectTestAuthor]) {
	t.Helper()
	post, _, required, nullable := bindRelationObjectTestFixture(t)
	a, err := ResolveRelatedSelectPath(post, "author")
	if err != nil {
		t.Fatal(err)
	}
	r, err := ResolveRelatedSelectPath(post, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	author, err := BindRequiredForwardSelect(a, required)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := BindNullableForwardSelect(r, nullable)
	if err != nil {
		t.Fatal(err)
	}
	return author, reviewer
}

// The first six cells use the existing source/author driver fixture. The two
// final cells exercise a separately decoded target within the same Rows.Scan.
type multipleSelectRows struct {
	selectRelatedRows
	reviewer *relationObjectTestAuthor
	partial  bool
	onClose  func()
}

func (r *multipleSelectRows) Scan(destinations ...any) error {
	if len(destinations) != 8 {
		return fmt.Errorf("multiple destinations=%d", len(destinations))
	}
	if err := r.selectRelatedRows.Scan(destinations[:6]...); err != nil {
		return err
	}
	id, ok := destinations[6].(*sql.NullInt64)
	if !ok {
		return fmt.Errorf("reviewer id destination %T", destinations[6])
	}
	name, ok := destinations[7].(*sql.NullString)
	if !ok {
		return fmt.Errorf("reviewer name destination %T", destinations[7])
	}
	if r.reviewer != nil {
		*id = sql.NullInt64{Int64: r.reviewer.ID, Valid: true}
		*name = sql.NullString{String: r.reviewer.Name, Valid: !r.partial}
	} else {
		*id = sql.NullInt64{}
		*name = sql.NullString{}
	}
	return nil
}
func (r *multipleSelectRows) Close() error {
	if r.onClose != nil {
		r.onClose()
	}
	return r.selectRelatedRows.Close()
}
func newMultipleSelectRows() *multipleSelectRows {
	value := selectRelatedRequiredValue()
	id := value.source.AuthorID
	value.source.ReviewerID = &id
	return &multipleSelectRows{selectRelatedRows: selectRelatedRows{values: []selectRelatedJoinedValue{value, value}}, reviewer: &relationObjectTestAuthor{ID: id, Name: "Ada"}}
}

func TestMultipleForwardSelectPublishesOnlyAfterEveryTargetAndRowsClose(t *testing.T) {
	for _, failureKind := range []string{"scan", "rows", "close", "cancel", "target key", "target absent", "partial target"} {
		for _, terminal := range []string{"All", "First"} {
			t.Run(failureKind+"/"+terminal, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				failure := errors.New(failureKind)
				failed := newMultipleSelectRows()
				switch failureKind {
				case "scan":
					failed.scanErr = failure
				case "rows":
					failed.rowsErr = failure
				case "close":
					failed.closeErr = failure
				case "cancel":
					failed.afterScan = cancel
					failure = context.Canceled
				case "target key":
					failed.reviewer.ID = 999
					failure = &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectProjection}
				case "target absent":
					failed.reviewer = nil
					failure = &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectProjection}
				case "partial target":
					failed.partial = true
					failure = &query.Error{Code: query.CodeInvalidPlan}
				}
				backend := &selectRelatedBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
					if len(plan.RelationProjections()) != 2 {
						t.Fatal("missing selection")
					}
					if call == 0 {
						return failed, nil
					}
					return newMultipleSelectRows(), nil
				}}
				author, reviewer := multipleSelectBindings(t)
				source := NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend)
				source = source.OrderBy(NewAutoField[relationObjectTestPost](relationObjectTestPostDescriptor{}.Metadata().Fields[0]).Asc())
				q := SelectRelated(source, reviewer, author)
				failed.onClose = func() {
					if _, ready := q.evaluation.cachedValues(); ready {
						t.Error("published before Close")
					}
				}
				var err error
				if terminal == "All" {
					var rows []*RelatedSelected[relationObjectTestPost]
					rows, err = q.All(ctx)
					if rows != nil {
						t.Fatal("partial results escaped")
					}
				} else {
					var row *RelatedSelected[relationObjectTestPost]
					var found bool
					row, found, err = q.First(ctx)
					if row != nil || found {
						t.Fatal("partial first escaped")
					}
				}
				if !errors.Is(err, failure) || failed.closeCalls.Load() != 1 {
					t.Fatalf("error=%v close=%d", err, failed.closeCalls.Load())
				}
				if _, ready := q.evaluation.cachedValues(); ready {
					t.Fatal("failure warmed cache")
				}
				rows, err := q.All(t.Context())
				if err != nil || len(rows) != 2 {
					t.Fatalf("retry=%d,%v", len(rows), err)
				}
				var previous *RelatedObject[relationObjectTestAuthor]
				for _, row := range rows {
					for _, binding := range []RelatedSelect[relationObjectTestPost, relationObjectTestAuthor]{author, reviewer} {
						related, err := binding.Related(row)
						if err != nil || related == previous {
							t.Fatal("shared target cache", err)
						}
						value, present, err := related.Get(t.Context())
						if err != nil || !present || value.Name != "Ada" {
							t.Fatal("incorrect warmed target", err)
						}
						previous = related
					}
				}
				if backend.callCount() != 2 {
					t.Fatal("target access performed I/O")
				}
			})
		}
	}
}

func TestMultipleForwardSelectValidationAndCacheOwnership(t *testing.T) {
	backend := &selectRelatedBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
		if plan.ResultShape().IsCountAll() {
			if len(plan.RelationProjections()) != 0 {
				t.Fatal("Count retained projections")
			}
			return &resultTestRows{values: [][]any{{int64(2)}}}, nil
		}
		return newMultipleSelectRows(), nil
	}}
	author, reviewer := multipleSelectBindings(t)
	source := NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend)
	q := author.Select(source, reviewer, author)
	if !q.Plan().Equal(SelectRelated(source, reviewer, author).Plan()) || len(q.Plan().RelationProjections()) != 2 || len(source.Plan().RelationProjections()) != 0 {
		t.Fatal("canonical immutable selection lost")
	}
	if count, err := q.Count(t.Context()); count != 2 || err != nil {
		t.Fatal("cold Count", err)
	}
	rows, err := q.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if count, err := q.Count(t.Context()); count != 2 || err != nil || backend.callCount() != 2 {
		t.Fatal("warm Count", err)
	}
	foreignAuthor, foreignReviewer := multipleSelectBindings(t)
	var nilSelection *RelatedSelect[relationObjectTestPost, relationObjectTestAuthor]
	for _, selections := range [][]RelatedSelection[relationObjectTestPost]{nil, {author, nil}, {author, nilSelection}, {author, foreignReviewer}, {author, foreignAuthor}} {
		invalid := SelectRelated(source, selections...)
		if values, err := invalid.All(t.Context()); values != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatalf("invalid All=%v", err)
		}
		if count, err := invalid.Count(t.Context()); count != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatalf("invalid Count=%v", err)
		}
	}
	if _, err := foreignAuthor.Related(rows[0]); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("foreign binding accepted", err)
	}
	copy := *rows[0]
	if _, err := author.Related(&copy); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("copied selected object accepted", err)
	}
	if backend.callCount() != 2 {
		t.Fatal("invalid selection performed I/O")
	}
}

func TestMultipleForwardSelectConcurrentCallersOwnEveryTarget(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	backend := &selectRelatedBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		close(started)
		<-release
		return newMultipleSelectRows(), nil
	}}
	author, reviewer := multipleSelectBindings(t)
	q := SelectRelated(NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend), author, reviewer)
	type outcome struct {
		rows []*RelatedSelected[relationObjectTestPost]
		err  error
	}
	const callers = 12
	outcomes := make(chan outcome, callers)
	for i := 0; i < callers; i++ {
		go func() { rows, err := q.All(t.Context()); outcomes <- outcome{rows, err} }()
	}
	awaitSignal(t, started, "multiple eager owner")
	close(release)
	caches := map[*RelatedObject[relationObjectTestAuthor]]bool{}
	for i := 0; i < callers; i++ {
		result := <-outcomes
		if result.err != nil || len(result.rows) != 2 {
			t.Fatalf("caller=%v", result.err)
		}
		for _, row := range result.rows {
			for _, selection := range []RelatedSelect[relationObjectTestPost, relationObjectTestAuthor]{author, reviewer} {
				cache, err := selection.Related(row)
				if err != nil || caches[cache] {
					t.Fatal("callers share target cache", err)
				}
				caches[cache] = true
			}
		}
	}
	if backend.callCount() != 1 {
		t.Fatalf("concurrent queries=%d", backend.callCount())
	}
}

type nilTargetProjectionDescriptor struct {
	relationObjectTestAuthorDescriptor
}

func (nilTargetProjectionDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestAuthor] {
	return nilTargetProjectionDescriptor{}
}
func (nilTargetProjectionDescriptor) NewProjectionScan() ProjectionScan[relationObjectTestAuthor] {
	return (*selectRelatedAuthorProjectionScan)(nil)
}

type shortTargetProjectionDescriptor struct {
	relationObjectTestAuthorDescriptor
}

func (shortTargetProjectionDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestAuthor] {
	return shortTargetProjectionDescriptor{}
}
func (shortTargetProjectionDescriptor) NewProjectionScan() ProjectionScan[relationObjectTestAuthor] {
	return &shortTargetProjectionScan{}
}

type shortTargetProjectionScan struct {
	selectRelatedAuthorProjectionScan
}

func (s *shortTargetProjectionScan) Destinations() []any { return []any{&s.id} }

type nilDestinationProjectionDescriptor struct {
	relationObjectTestAuthorDescriptor
}

func (nilDestinationProjectionDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestAuthor] {
	return nilDestinationProjectionDescriptor{}
}
func (nilDestinationProjectionDescriptor) NewProjectionScan() ProjectionScan[relationObjectTestAuthor] {
	return &nilDestinationProjectionScan{}
}

type nilDestinationProjectionScan struct {
	selectRelatedAuthorProjectionScan
}

func (s *nilDestinationProjectionScan) Destinations() []any {
	return []any{&s.id, (*sql.NullString)(nil)}
}

func TestMultipleForwardSelectInvalidSecondScannerNeverScansOrPublishes(t *testing.T) {
	for _, descriptor := range []RelationObjectDescriptor[relationObjectTestAuthor]{nilTargetProjectionDescriptor{}, shortTargetProjectionDescriptor{}, nilDestinationProjectionDescriptor{}} {
		t.Run(fmt.Sprintf("%T", descriptor), func(t *testing.T) {
			author, reviewer := multipleSelectBindings(t)
			source := author.state.path.source
			target, err := BindModel(ProjectBinding{snapshot: source.snapshot}, author.state.path.targetIdentity, descriptor)
			if err != nil {
				t.Fatal(err)
			}
			relation, err := BindNullableForwardObject(source, "reviewer", target)
			if err != nil {
				t.Fatal(err)
			}
			path, err := ResolveRelatedSelectPath(source, "reviewer")
			if err != nil {
				t.Fatal(err)
			}
			invalid, err := BindNullableForwardSelect(path, relation)
			if err != nil {
				t.Fatal(err)
			}
			rows := newMultipleSelectRows()
			backend := &selectRelatedBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}
			raw := NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend)
			q := SelectRelated(raw, author, invalid)
			if result, err := q.All(t.Context()); result != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatalf("invalid scanner=%v", err)
			}
			if rows.scanCalls.Load() != 0 || rows.closeCalls.Load() != 1 {
				t.Fatal("invalid destination reached Scan or leaked rows")
			}
			if _, ready := q.evaluation.cachedValues(); ready {
				t.Fatal("invalid scanner warmed cache")
			}
			// Metadata-equal duplicate selectors with distinct scanner bindings must be
			// rejected before I/O, rather than one silently replacing the other.
			duplicate := SelectRelated(raw, author, reviewer, invalid)
			if _, err := duplicate.All(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || backend.callCount() != 1 {
				t.Fatal("conflicting duplicate reached backend", err)
			}
		})
	}
}
