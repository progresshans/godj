package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type selectRelatedAuthorProjectionScan struct {
	id   sql.NullInt64
	name sql.NullString
}

func (relationObjectTestAuthorDescriptor) NewProjectionScan() ProjectionScan[relationObjectTestAuthor] {
	return &selectRelatedAuthorProjectionScan{}
}

func (scan *selectRelatedAuthorProjectionScan) Destinations() []any {
	return []any{&scan.id, &scan.name}
}

func (scan *selectRelatedAuthorProjectionScan) Decode() (relationObjectTestAuthor, query.Value, ProjectionPresence) {
	switch {
	case !scan.id.Valid && !scan.name.Valid:
		return relationObjectTestAuthor{}, query.Null(), ProjectionAbsent
	case !scan.id.Valid || !scan.name.Valid:
		return relationObjectTestAuthor{}, query.Value{}, ProjectionInvalid
	default:
		return relationObjectTestAuthor{ID: scan.id.Int64, Name: scan.name.String}, query.Integer(scan.id.Int64), ProjectionPresent
	}
}

type selectRelatedPostProjectionScan struct {
	id         sql.NullInt64
	title      sql.NullString
	authorID   sql.NullInt64
	reviewerID sql.NullInt64
}

func (relationObjectTestPostDescriptor) NewProjectionScan() ProjectionScan[relationObjectTestPost] {
	return &selectRelatedPostProjectionScan{}
}

func (scan *selectRelatedPostProjectionScan) Destinations() []any {
	return []any{&scan.id, &scan.title, &scan.authorID, &scan.reviewerID}
}

func (scan *selectRelatedPostProjectionScan) Decode() (relationObjectTestPost, query.Value, ProjectionPresence) {
	if !scan.id.Valid || !scan.title.Valid || !scan.authorID.Valid {
		return relationObjectTestPost{}, query.Value{}, ProjectionInvalid
	}
	value := relationObjectTestPost{ID: scan.id.Int64, Title: scan.title.String, AuthorID: scan.authorID.Int64}
	if scan.reviewerID.Valid {
		reviewerID := scan.reviewerID.Int64
		value.ReviewerID = &reviewerID
	}
	return value, query.Integer(scan.id.Int64), ProjectionPresent
}

func TestProjectionScansDistinguishAbsentPresentAndInvalidShapes(t *testing.T) {
	t.Parallel()

	author := &selectRelatedAuthorProjectionScan{}
	if value, key, presence := author.Decode(); value != (relationObjectTestAuthor{}) || !key.IsNull() || presence != ProjectionAbsent {
		t.Fatalf("empty author Decode() = (%#v, %#v, %d)", value, key, presence)
	}
	author.id = sql.NullInt64{Int64: 1, Valid: true}
	if _, key, presence := author.Decode(); key.Kind() != "" || presence != ProjectionInvalid {
		t.Fatalf("partial author Decode() = (%#v, %d)", key, presence)
	}
	author.name = sql.NullString{String: "Ada", Valid: true}
	if value, key, presence := author.Decode(); value != (relationObjectTestAuthor{ID: 1, Name: "Ada"}) ||
		presence != ProjectionPresent || !key.Equal(query.Integer(1)) {
		t.Fatalf("present author Decode() = (%#v, %#v, %d)", value, key, presence)
	}

	post := &selectRelatedPostProjectionScan{
		id:       sql.NullInt64{Int64: 10, Valid: true},
		title:    sql.NullString{String: "Alpha", Valid: true},
		authorID: sql.NullInt64{Int64: 1, Valid: true},
	}
	if value, key, presence := post.Decode(); value != (relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1}) ||
		presence != ProjectionPresent || !key.Equal(query.Integer(10)) {
		t.Fatalf("present post Decode() = (%#v, %#v, %d)", value, key, presence)
	}
	post.title.Valid = false
	if _, key, presence := post.Decode(); key.Kind() != "" || presence != ProjectionInvalid {
		t.Fatalf("partial post Decode() = (%#v, %d)", key, presence)
	}
}

func TestForwardSelectRequiredPreservesPlanWarmsCacheAndClonesResults(t *testing.T) {
	post, _, required, _ := bindRelationObjectTestFixture(t)
	path, err := ResolveForwardSelectPath(post, "author")
	if err != nil {
		t.Fatalf("ResolveForwardSelectPath(author) error = %v", err)
	}
	selection, err := BindRequiredForwardSelect(path, required)
	if err != nil {
		t.Fatalf("BindRequiredForwardSelect() error = %v", err)
	}

	backend := &selectRelatedBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
		if call == 0 {
			projection, ok := plan.RelationProjection()
			if !ok || projection.Hop().Field() != "author" || projection.Hop().Nullable() {
				t.Fatalf("eager plan projection = (%#v, %v)", projection, ok)
			}
			return &selectRelatedRows{values: []selectRelatedJoinedValue{
				{source: relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1}, target: &relationObjectTestAuthor{ID: 1, Name: "Ada"}},
				{source: relationObjectTestPost{ID: 12, Title: "Gamma", AuthorID: 3}, target: &relationObjectTestAuthor{ID: 3, Name: "Cleo"}},
			}}, nil
		}
		if _, ok := plan.RelationProjection(); ok {
			t.Fatal("fresh related-object query retained eager projection")
		}
		return &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: 1, Name: "Ada"}}}, nil
	}}
	id := NewIntegerField[relationObjectTestPost](relationObjectTestPostField("id"))
	title := NewStringField[relationObjectTestPost](relationObjectTestPostField("title"))
	source := NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).
		Using(backend).
		Filter(title.Exact("Alpha")).
		OrderBy(id.Asc())
	source, err = source.Limit(2)
	if err != nil {
		t.Fatalf("source Limit() error = %v", err)
	}
	eager := selection.Select(source)
	if eager.Backend() != backend {
		t.Fatalf("Backend() = %T %p, want source backend %p", eager.Backend(), eager.Backend(), backend)
	}
	if len(eager.Plan().Conditions()) != 1 || len(eager.Plan().Orderings()) != 1 {
		t.Fatalf("Select() changed source plan: %#v", eager.Plan())
	}
	if limit, ok := eager.Plan().Limit(); !ok || limit != 2 {
		t.Fatalf("eager limit = (%d, %v)", limit, ok)
	}
	if _, ok := source.Plan().RelationProjection(); ok {
		t.Fatal("Select() mutated source QuerySet plan")
	}

	first, err := eager.All(context.Background())
	if err != nil || len(first) != 2 {
		t.Fatalf("first All() = (%#v, %v)", first, err)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("first All backend calls = %d, want 1", got)
	}
	firstSource, err := first[0].Source()
	if err != nil || firstSource != (relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1}) {
		t.Fatalf("Source() = (%#v, %v)", firstSource, err)
	}
	related, err := first[0].Related()
	if err != nil {
		t.Fatalf("Related() error = %v", err)
	}
	author, ok, err := related.Get(context.Background())
	if err != nil || !ok || author != (relationObjectTestAuthor{ID: 1, Name: "Ada"}) {
		t.Fatalf("ready Get() = (%#v, %v, %v)", author, ok, err)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("ready Get backend calls = %d, want 1", got)
	}

	// Mutating caller-owned clones cannot alter the canonical eager cache.
	firstSource.Title = "mutated"
	author.Name = "mutated"
	second, err := eager.All(context.Background())
	if err != nil || len(second) != 2 {
		t.Fatalf("warm All() = (%#v, %v)", second, err)
	}
	if first[0] == second[0] {
		t.Fatal("warm All reused ForwardSelected pointer ownership")
	}
	secondSource, _ := second[0].Source()
	secondRelated, _ := second[0].Related()
	secondAuthor, ok, err := secondRelated.Get(context.Background())
	if err != nil || !ok || secondSource.Title != "Alpha" || secondAuthor.Name != "Ada" {
		t.Fatalf("warm clones = source %#v author %#v ok=%v err=%v", secondSource, secondAuthor, ok, err)
	}
	if related == secondRelated {
		t.Fatal("warm All reused RelatedObject pointer ownership")
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("warm All backend calls = %d, want 1", got)
	}

	copied := *first[0]
	if _, err := copied.Source(); err == nil {
		t.Fatal("copied ForwardSelected.Source() succeeded")
	}
	if _, err := copied.Related(); err == nil {
		t.Fatal("copied ForwardSelected.Related() succeeded")
	}

	fresh, err := related.Fresh()
	if err != nil {
		t.Fatalf("ready RelatedObject.Fresh() error = %v", err)
	}
	if value, ok, err := fresh.Get(context.Background()); err != nil || !ok || value.Name != "Ada" {
		t.Fatalf("fresh related Get() = (%#v, %v, %v)", value, ok, err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("fresh related backend calls = %d, want 2", got)
	}
}

func TestForwardSelectNullablePublishesAbsentAndPresentReadyObjects(t *testing.T) {
	post, _, _, nullable := bindRelationObjectTestFixture(t)
	path, err := ResolveForwardSelectPath(post, "reviewer")
	if err != nil {
		t.Fatalf("ResolveForwardSelectPath(reviewer) error = %v", err)
	}
	selection, err := BindNullableForwardSelect(path, nullable)
	if err != nil {
		t.Fatalf("BindNullableForwardSelect() error = %v", err)
	}
	reviewerID := int64(2)
	rows := &selectRelatedRows{values: []selectRelatedJoinedValue{
		{source: relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewerID}, target: &relationObjectTestAuthor{ID: 2, Name: "Bob"}},
		{source: relationObjectTestPost{ID: 11, Title: "Beta", AuthorID: 1}, target: nil},
	}}
	backend := &selectRelatedBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
		projection, ok := plan.RelationProjection()
		if !ok || !projection.Hop().Nullable() || projection.Hop().Field() != "reviewer" {
			t.Fatalf("nullable projection = (%#v, %v)", projection, ok)
		}
		return rows, nil
	}}
	result, err := selection.Select(NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend)).
		All(context.Background())
	if err != nil || len(result) != 2 {
		t.Fatalf("nullable All() = (%#v, %v)", result, err)
	}
	first, _ := result[0].Related()
	if value, ok, err := first.Get(context.Background()); err != nil || !ok || value.Name != "Bob" {
		t.Fatalf("present reviewer Get() = (%#v, %v, %v)", value, ok, err)
	}
	absent, _ := result[1].Related()
	if value, ok, err := absent.Get(context.Background()); err != nil || ok || value != (relationObjectTestAuthor{}) {
		t.Fatalf("absent reviewer Get() = (%#v, %v, %v)", value, ok, err)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("ready nullable access backend calls = %d, want 1", got)
	}
	if got := rows.scanCalls.Load(); got != 2 {
		t.Fatalf("joined Row.Scan calls = %d, want one per row", got)
	}
	if got := rows.closeCalls.Load(); got != 1 {
		t.Fatalf("Rows.Close calls = %d, want 1", got)
	}
}

func TestResolveForwardSelectPathTaxonomyAndBindersRejectMismatches(t *testing.T) {
	post, author, required, nullable := bindRelationObjectTestFixture(t)
	for _, path := range []string{"", " ", "missing", "posts", "Author", "author__name", "author.name"} {
		resolved, err := ResolveForwardSelectPath(post, path)
		if err == nil || resolved.state.valid {
			t.Fatalf("ResolveForwardSelectPath(%q) = (%#v, %v), want zero/error", path, resolved, err)
		}
		assertSelectRelatedError(t, err, query.CategoryField, query.CodeInvalidRelatedPath, path)
	}
	if _, err := ResolveForwardSelectPath(author, "posts"); err == nil {
		t.Fatal("reverse path resolved as forward eager path")
	} else {
		assertSelectRelatedError(t, err, query.CategoryField, query.CodeInvalidRelatedPath, "posts")
	}

	authorPath, err := ResolveForwardSelectPath(post, "author")
	if err != nil {
		t.Fatalf("author path error = %v", err)
	}
	reviewerPath, err := ResolveForwardSelectPath(post, "reviewer")
	if err != nil {
		t.Fatalf("reviewer path error = %v", err)
	}
	if _, err := BindRequiredForwardSelect(reviewerPath, required); err == nil {
		t.Fatal("required binder accepted nullable path")
	}
	if _, err := BindNullableForwardSelect(authorPath, nullable); err == nil {
		t.Fatal("nullable binder accepted required path")
	}
	if _, err := BindRequiredForwardSelect(ForwardSelectPath[relationObjectTestPost]{}, required); err == nil {
		t.Fatal("required binder accepted zero path")
	}
	if _, err := BindNullableForwardSelect(reviewerPath, NullableForwardObject[relationObjectTestPost, relationObjectTestAuthor]{}); err == nil {
		t.Fatal("nullable binder accepted zero object handle")
	}

	otherBinding := relationObjectTestBinding(t)
	otherAuthor, err := BindModel(otherBinding, ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, relationObjectTestAuthorDescriptor{})
	if err != nil {
		t.Fatalf("other BindModel(author) error = %v", err)
	}
	otherRequired, err := BindRequiredForwardObject(post, "author", otherAuthor)
	if err == nil || otherRequired.state.valid {
		t.Fatalf("cross-snapshot forward object = (%#v, %v), want zero/error", otherRequired, err)
	}
}

func TestForwardSelectTerminalPrecedenceAndConfigurationValidation(t *testing.T) {
	post, _, required, _ := bindRelationObjectTestFixture(t)
	path, err := ResolveForwardSelectPath(post, "author")
	if err != nil {
		t.Fatalf("ResolveForwardSelectPath() error = %v", err)
	}
	selection, err := BindRequiredForwardSelect(path, required)
	if err != nil {
		t.Fatalf("BindRequiredForwardSelect() error = %v", err)
	}
	backend := &selectRelatedBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return &selectRelatedRows{}, nil
	}}
	valid := selection.Select(NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend))
	if values, err := valid.All(nil); values != nil || err == nil {
		t.Fatalf("All(nil) = (%#v, %v), want nil/error", values, err)
	} else {
		assertSelectRelatedError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
	}
	var typedNil *relationObjectNilContext
	if values, err := valid.All(typedNil); values != nil || err == nil {
		t.Fatalf("All(typed nil context) = (%#v, %v), want nil/error", values, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := valid.All(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("All(canceled) error = %v", err)
	}
	if got := backend.callCount(); got != 0 {
		t.Fatalf("invalid contexts performed backend I/O: %d", got)
	}

	configurationFailure := errors.New("source configuration")
	configured := NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).
		Using(nil).
		OrderBy(Ordering[relationObjectTestPost]{err: configurationFailure})
	configuredEager := selection.Select(configured)
	if _, err := configuredEager.All(context.Background()); !errors.Is(err, configurationFailure) {
		t.Fatalf("configuration precedence error = %v, want %v", err, configurationFailure)
	}
	if _, err := selection.Select(NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(nil)).
		All(context.Background()); err == nil {
		t.Fatal("nil backend eager All succeeded")
	} else {
		assertSelectRelatedError(t, err, query.CategoryBackend, query.CodeInvalidPlan, "")
	}
	var typedNilBackend *selectRelatedBackend
	if _, err := selection.Select(NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(typedNilBackend)).
		All(context.Background()); err == nil {
		t.Fatal("typed nil backend eager All succeeded")
	} else {
		assertSelectRelatedError(t, err, query.CategoryBackend, query.CodeInvalidPlan, "")
	}
	zero := ForwardSelect[relationObjectTestPost, relationObjectTestAuthor]{}.
		Select(NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend))
	if _, err := zero.All(context.Background()); err == nil {
		t.Fatal("zero ForwardSelect eager All succeeded")
	} else {
		assertSelectRelatedError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
	}
	if _, err := (ForwardSelectQuery[relationObjectTestPost, relationObjectTestAuthor]{}).All(context.Background()); err == nil {
		t.Fatal("zero ForwardSelectQuery.All succeeded")
	} else {
		assertSelectRelatedError(t, err, query.CategoryBackend, query.CodeInvalidPlan, "")
	}

	wrongDescriptor := selectRelatedAlternatePostDescriptor{}
	mismatch := selection.Select(NewManager[relationObjectTestPost](wrongDescriptor).Using(backend))
	if _, err := mismatch.All(context.Background()); err == nil {
		t.Fatal("Select accepted a different descriptor dynamic type")
	} else {
		assertSelectRelatedError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
	}
}

func TestForwardSelectFailuresCloseOnceDoNotPublishAndRetry(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*selectRelatedRows, error)
	}{
		{name: "scan", configure: func(rows *selectRelatedRows, failure error) { rows.scanErr = failure }},
		{name: "rows", configure: func(rows *selectRelatedRows, failure error) { rows.rowsErr = failure }},
		{name: "close", configure: func(rows *selectRelatedRows, failure error) { rows.closeErr = failure }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New(test.name + " failure")
			failed := &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue()}}
			test.configure(failed, failure)
			success := &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue()}}
			backend := &selectRelatedBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call == 0 {
					return failed, nil
				}
				return success, nil
			}}
			eager := requiredSelectQuery(t, backend)
			if values, err := eager.All(context.Background()); values != nil || !errors.Is(err, failure) {
				t.Fatalf("failed All() = (%#v, %v), want nil/%v", values, err, failure)
			}
			if values, err := eager.All(context.Background()); err != nil || len(values) != 1 {
				t.Fatalf("retry All() = (%#v, %v)", values, err)
			}
			if values, err := eager.All(context.Background()); err != nil || len(values) != 1 {
				t.Fatalf("warm retry All() = (%#v, %v)", values, err)
			}
			if got := backend.callCount(); got != 2 {
				t.Fatalf("failure/retry backend calls = %d, want 2", got)
			}
			if got := failed.closeCalls.Load(); got != 1 {
				t.Fatalf("failed rows Close calls = %d, want 1", got)
			}
			if got := success.closeCalls.Load(); got != 1 {
				t.Fatalf("success rows Close calls = %d, want 1", got)
			}
		})
	}

	backendFailure := errors.New("backend failure")
	closeFailure := errors.New("returned rows close failure")
	returned := &selectRelatedRows{closeErr: closeFailure}
	backend := &selectRelatedBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return returned, backendFailure
	}}
	if _, err := requiredSelectQuery(t, backend).All(context.Background()); !errors.Is(err, backendFailure) || !errors.Is(err, closeFailure) {
		t.Fatalf("backend+close error = %v", err)
	}
	if got := returned.closeCalls.Load(); got != 1 {
		t.Fatalf("backend-error rows Close calls = %d, want 1", got)
	}

	nilRowsBackend := &selectRelatedBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		return nil, nil
	}}
	if _, err := requiredSelectQuery(t, nilRowsBackend).All(context.Background()); err == nil {
		t.Fatal("backend nil rows without error succeeded")
	} else {
		assertSelectRelatedError(t, err, query.CategoryBackend, query.CodeInvalidPlan, "")
	}
}

func TestForwardSelectProjectionIntegrityAndShapeFailuresArePrePublication(t *testing.T) {
	tests := []struct {
		name     string
		value    selectRelatedJoinedValue
		category string
		code     string
	}{
		{name: "required absent", value: selectRelatedJoinedValue{source: relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1}}, category: query.CategoryIntegrity, code: query.CodeRelatedObjectProjection},
		{name: "target key mismatch", value: selectRelatedJoinedValue{source: relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1}, target: &relationObjectTestAuthor{ID: 2, Name: "Bob"}}, category: query.CategoryIntegrity, code: query.CodeRelatedObjectProjection},
		{name: "partial target", value: selectRelatedJoinedValue{source: relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1}, target: &relationObjectTestAuthor{ID: 1, Name: "Ada"}, shape: selectRelatedTargetPartial}, category: query.CategoryQuery, code: query.CodeInvalidPlan},
		{name: "partial source", value: selectRelatedJoinedValue{source: relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1}, target: &relationObjectTestAuthor{ID: 1, Name: "Ada"}, shape: selectRelatedSourcePartial}, category: query.CategoryQuery, code: query.CodeInvalidPlan},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := &selectRelatedRows{values: []selectRelatedJoinedValue{test.value}}
			second := &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue()}}
			backend := &selectRelatedBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call == 0 {
					return first, nil
				}
				return second, nil
			}}
			eager := requiredSelectQuery(t, backend)
			if values, err := eager.All(context.Background()); values != nil || err == nil {
				t.Fatalf("corrupt All() = (%#v, %v), want nil/error", values, err)
			} else {
				assertSelectRelatedError(t, err, test.category, test.code, map[bool]string{true: "author"}[test.category == query.CategoryIntegrity])
			}
			if values, err := eager.All(context.Background()); err != nil || len(values) != 1 {
				t.Fatalf("retry after corrupt row = (%#v, %v)", values, err)
			}
			if got := backend.callCount(); got != 2 {
				t.Fatalf("corrupt/retry backend calls = %d, want 2", got)
			}
		})
	}

	// A NULL nullable source key paired with a present target is an integrity
	// failure; a non-NULL key paired with an absent target is the inverse.
	post, _, _, nullable := bindRelationObjectTestFixture(t)
	path, _ := ResolveForwardSelectPath(post, "reviewer")
	selection, _ := BindNullableForwardSelect(path, nullable)
	for _, value := range []selectRelatedJoinedValue{
		{source: relationObjectTestPost{ID: 11, Title: "Beta", AuthorID: 1}, target: &relationObjectTestAuthor{ID: 2, Name: "Bob"}},
		func() selectRelatedJoinedValue {
			reviewerID := int64(2)
			return selectRelatedJoinedValue{source: relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewerID}}
		}(),
	} {
		backend := &selectRelatedBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
			return &selectRelatedRows{values: []selectRelatedJoinedValue{value}}, nil
		}}
		_, err := selection.Select(NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend)).All(context.Background())
		assertSelectRelatedError(t, err, query.CategoryIntegrity, query.CodeRelatedObjectProjection, "reviewer")
	}
}

func TestForwardSelectCancellationAndConcurrentCachePublication(t *testing.T) {
	// Cancellation after Scan but before publication closes rows and leaves the
	// query retryable under a new live context.
	ctx, cancel := context.WithCancel(context.Background())
	canceledRows := &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue()}, afterScan: cancel}
	successRows := &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue()}}
	backend := &selectRelatedBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
		if call == 0 {
			return canceledRows, nil
		}
		return successRows, nil
	}}
	eager := requiredSelectQuery(t, backend)
	if values, err := eager.All(ctx); values != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled All() = (%#v, %v)", values, err)
	}
	if values, err := eager.All(context.Background()); err != nil || len(values) != 1 {
		t.Fatalf("retry after cancellation = (%#v, %v)", values, err)
	}
	if got := backend.callCount(); got != 2 {
		t.Fatalf("cancellation/retry backend calls = %d, want 2", got)
	}
	if got := canceledRows.closeCalls.Load(); got != 1 {
		t.Fatalf("canceled rows Close calls = %d, want 1", got)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	concurrentBackend := &selectRelatedBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
		close(started)
		<-release
		return &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue()}}, nil
	}}
	concurrent := requiredSelectQuery(t, concurrentBackend)
	const callers = 24
	results := make(chan error, callers)
	go func() {
		values, err := concurrent.All(context.Background())
		if err == nil && len(values) != 1 {
			err = fmt.Errorf("len(values) = %d", len(values))
		}
		results <- err
	}()
	awaitSignal(t, started, "forward select owner backend start")
	for index := 1; index < callers; index++ {
		go func() {
			values, err := concurrent.All(context.Background())
			if err == nil && len(values) != 1 {
				err = fmt.Errorf("len(values) = %d", len(values))
			}
			results <- err
		}()
	}
	close(release)
	for index := 0; index < callers; index++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent All() error = %v", err)
		}
	}
	if got := concurrentBackend.callCount(); got != 1 {
		t.Fatalf("concurrent backend calls = %d, want 1", got)
	}
}

func TestForwardSelectEmptyResultIsNonNilAndCached(t *testing.T) {
	rows := &selectRelatedRows{}
	backend := &selectRelatedBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}
	eager := requiredSelectQuery(t, backend)
	for call := 0; call < 2; call++ {
		values, err := eager.All(context.Background())
		if err != nil || values == nil || len(values) != 0 {
			t.Fatalf("empty All call %d = (%#v, %v)", call, values, err)
		}
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("empty cache backend calls = %d, want 1", got)
	}
}

func requiredSelectQuery(t *testing.T, backend db.Queryer) ForwardSelectQuery[relationObjectTestPost, relationObjectTestAuthor] {
	t.Helper()
	post, _, required, _ := bindRelationObjectTestFixture(t)
	path, err := ResolveForwardSelectPath(post, "author")
	if err != nil {
		t.Fatalf("ResolveForwardSelectPath(author) error = %v", err)
	}
	selection, err := BindRequiredForwardSelect(path, required)
	if err != nil {
		t.Fatalf("BindRequiredForwardSelect() error = %v", err)
	}
	return selection.Select(NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend))
}

func selectRelatedRequiredValue() selectRelatedJoinedValue {
	return selectRelatedJoinedValue{
		source: relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1},
		target: &relationObjectTestAuthor{ID: 1, Name: "Ada"},
	}
}

type selectRelatedAlternatePostDescriptor struct{}

func (selectRelatedAlternatePostDescriptor) Metadata() ir.Model {
	return relationObjectTestPostDescriptor{}.Metadata()
}
func (selectRelatedAlternatePostDescriptor) Scan(row db.Row) (relationObjectTestPost, error) {
	return relationObjectTestPostDescriptor{}.Scan(row)
}
func (selectRelatedAlternatePostDescriptor) CloneModel(value relationObjectTestPost) relationObjectTestPost {
	return relationObjectTestPostDescriptor{}.CloneModel(value)
}

type selectRelatedShape uint8

const (
	selectRelatedShapeValid selectRelatedShape = iota
	selectRelatedSourcePartial
	selectRelatedTargetPartial
)

type selectRelatedJoinedValue struct {
	source relationObjectTestPost
	target *relationObjectTestAuthor
	shape  selectRelatedShape
}

type selectRelatedRows struct {
	values     []selectRelatedJoinedValue
	position   int
	scanErr    error
	rowsErr    error
	closeErr   error
	afterScan  func()
	scanCalls  atomic.Int64
	closeCalls atomic.Int64
}

func (rows *selectRelatedRows) Next() bool {
	return rows.position < len(rows.values)
}

func (rows *selectRelatedRows) Scan(destinations ...any) error {
	rows.scanCalls.Add(1)
	if rows.scanErr != nil {
		return rows.scanErr
	}
	if len(destinations) != 6 {
		return fmt.Errorf("destination count = %d, want 6", len(destinations))
	}
	holders := []any{
		destinations[0], destinations[1], destinations[2], destinations[3], destinations[4], destinations[5],
	}
	postID, ok0 := holders[0].(*sql.NullInt64)
	title, ok1 := holders[1].(*sql.NullString)
	authorID, ok2 := holders[2].(*sql.NullInt64)
	reviewerID, ok3 := holders[3].(*sql.NullInt64)
	targetID, ok4 := holders[4].(*sql.NullInt64)
	targetName, ok5 := holders[5].(*sql.NullString)
	if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return fmt.Errorf("destination types = %T %T %T %T %T %T", holders[0], holders[1], holders[2], holders[3], holders[4], holders[5])
	}
	value := rows.values[rows.position]
	rows.position++
	*postID = sql.NullInt64{Int64: value.source.ID, Valid: true}
	*title = sql.NullString{String: value.source.Title, Valid: value.shape != selectRelatedSourcePartial}
	*authorID = sql.NullInt64{Int64: value.source.AuthorID, Valid: true}
	if value.source.ReviewerID != nil {
		*reviewerID = sql.NullInt64{Int64: *value.source.ReviewerID, Valid: true}
	} else {
		*reviewerID = sql.NullInt64{}
	}
	if value.target != nil {
		*targetID = sql.NullInt64{Int64: value.target.ID, Valid: true}
		*targetName = sql.NullString{String: value.target.Name, Valid: value.shape != selectRelatedTargetPartial}
	} else {
		*targetID = sql.NullInt64{}
		*targetName = sql.NullString{}
	}
	if rows.afterScan != nil {
		rows.afterScan()
	}
	return nil
}

func (rows *selectRelatedRows) Err() error { return rows.rowsErr }
func (rows *selectRelatedRows) Close() error {
	rows.closeCalls.Add(1)
	return rows.closeErr
}

type selectRelatedBackend struct {
	mu    sync.Mutex
	plans []query.Plan
	query func(int, context.Context, query.Plan) (db.Rows, error)
}

func (backend *selectRelatedBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.mu.Lock()
	call := len(backend.plans)
	backend.plans = append(backend.plans, plan)
	queryFn := backend.query
	backend.mu.Unlock()
	return queryFn(call, ctx, plan)
}

func (backend *selectRelatedBackend) callCount() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.plans)
}

func assertSelectRelatedError(t *testing.T, err error, category, code, field string) {
	t.Helper()
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Category != category || queryError.Code != code ||
		(field != "" && queryError.Field != field) {
		t.Fatalf("error = %T %v, want %s/%s field=%q", err, err, category, code, field)
	}
}

func TestForwardSelectWaiterCancellationDoesNotCancelOwner(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	backend := &selectRelatedBackend{query: func(_ int, ctx context.Context, _ query.Plan) (db.Rows, error) {
		close(started)
		select {
		case <-release:
			return &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue()}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	eager := requiredSelectQuery(t, backend)
	ownerResult := make(chan error, 1)
	go func() {
		_, err := eager.All(context.Background())
		ownerResult <- err
	}()
	awaitSignal(t, started, "forward select owner start")
	waiterCtx, cancel := context.WithCancel(context.Background())
	waiterResult := make(chan error, 1)
	go func() {
		_, err := eager.All(waiterCtx)
		waiterResult <- err
	}()
	time.Sleep(time.Millisecond)
	cancel()
	if err := awaitValue(t, waiterResult, "forward select canceled waiter"); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v", err)
	}
	close(release)
	if err := awaitValue(t, ownerResult, "forward select owner"); err != nil {
		t.Fatalf("owner error = %v", err)
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("waiter cancellation backend calls = %d, want 1", got)
	}
}

func TestForwardSelectProjectionDescriptorCapabilityIsRequired(t *testing.T) {
	binding := relationObjectTestBinding(t)
	post, err := BindModel(binding, ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, selectRelatedNoProjectionPostDescriptor{})
	if err != nil {
		t.Fatalf("BindModel(no projection post) error = %v", err)
	}
	if _, err := ResolveForwardSelectPath(post, "author"); err == nil {
		t.Fatal("ResolveForwardSelectPath accepted descriptor without projection capability")
	} else {
		assertSelectRelatedError(t, err, query.CategoryQuery, query.CodeInvalidPlan, "")
	}
}

type selectRelatedNoProjectionPostDescriptor struct{}

func (selectRelatedNoProjectionPostDescriptor) Metadata() ir.Model {
	return relationObjectTestPostDescriptor{}.Metadata()
}
func (selectRelatedNoProjectionPostDescriptor) Scan(row db.Row) (relationObjectTestPost, error) {
	return relationObjectTestPostDescriptor{}.Scan(row)
}
func (selectRelatedNoProjectionPostDescriptor) CloneModel(value relationObjectTestPost) relationObjectTestPost {
	return relationObjectTestPostDescriptor{}.CloneModel(value)
}
func (selectRelatedNoProjectionPostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return selectRelatedNoProjectionPostDescriptor{}
}
func (selectRelatedNoProjectionPostDescriptor) BindRelationStorage(field ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	return relationObjectTestPostDescriptor{}.BindRelationStorage(field)
}

func TestForwardSelectProductionTypesRemainNonAliasing(t *testing.T) {
	t.Parallel()

	// Keep reflection limited to this cold ABI test: none of the public value
	// types exposes mutable slices, maps, or exported state.
	for _, value := range []any{
		ForwardSelectPath[relationObjectTestPost]{},
		ForwardSelect[relationObjectTestPost, relationObjectTestAuthor]{},
		ForwardSelectQuery[relationObjectTestPost, relationObjectTestAuthor]{},
	} {
		typeOf := reflect.TypeOf(value)
		for index := 0; index < typeOf.NumField(); index++ {
			if typeOf.Field(index).PkgPath == "" {
				t.Fatalf("%s exposes field %s", typeOf, typeOf.Field(index).Name)
			}
		}
	}
}
