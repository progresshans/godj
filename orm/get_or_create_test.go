package orm_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestGetOrCreateFreshExistingDoesNotBuildOrOpenTransaction(t *testing.T) {
	summary := "owned"
	backend := &creationBackend{}
	backend.read = func(call int, _ query.Plan) (db.Rows, error) {
		return articleCreationRows(models.Article{ID: int64(call + 1), Title: "existing", Summary: &summary}), nil
	}
	source := models.ArticleObjects.Using(backend)
	warm, err := source.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	input := articleCreationInput(func() orm.Mutation[models.Article] {
		t.Fatal("existing object evaluated input")
		return orm.Mutation[models.Article]{}
	})
	for index, candidate := range []orm.CreateInput[models.Article]{input, nil, (*nilArticleCreate)(nil)} {
		value, created, err := source.GetOrCreate(t.Context(), candidate)
		if err != nil || created || value.ID != int64(index+2) || value.Summary == warm[0].Summary {
			t.Fatalf("existing result = %#v, %v, %v", value, created, err)
		}
		*value.Summary = "caller mutation"
	}
	cached, err := source.All(t.Context())
	if err != nil || cached[0].ID != 1 || *cached[0].Summary != "owned" || backend.atomicCalls.Load() != 0 || backend.insertCalls.Load() != 0 || backend.readCalls.Load() != 4 {
		t.Fatalf("existing lookup altered cache or wrote: %#v, %v; reads=%d atomic=%d inserts=%d", cached, err, backend.readCalls.Load(), backend.atomicCalls.Load(), backend.insertCalls.Load())
	}
}

func TestGetOrCreateBuildsOnceInsideScopeAndUsesOriginalManagerSnapshot(t *testing.T) {
	descriptor := &mutableManagerDescriptor{metadata: (models.ArticleDescriptor{}).Metadata()}
	manager := orm.NewManager[models.Article](descriptor)
	backend := &creationBackend{}
	source := manager.Using(backend).Filter(models.ArticleFields.Title.Exact("lookup value"))
	before := source.Plan()
	descriptor.metadata.DBTable = "changed"
	descriptor.metadata.Fields[1].Column = "changed_title"
	builds := 0
	summary := "original input"
	input := articleCreationInput(func() orm.Mutation[models.Article] {
		builds++
		if !backend.scopeActive.Load() {
			t.Fatal("input was evaluated outside the owned transaction")
		}
		value := models.Article{Title: "explicit input", Summary: &summary}
		generated := models.NewArticleCreate(value.Title).WithSummary(summary).BuildCreate()
		return orm.NewCreateMutation(value, generated.Table(), generated.Assignments())
	})
	value, created, err := source.GetOrCreate(t.Context(), input)
	if err != nil || !created || value.ID != 71 || value.Title != "explicit input" || value.Summary == &summary || builds != 1 || backend.insertCalls.Load() != 1 || backend.atomicCalls.Load() != 1 {
		t.Fatalf("created result = %#v, %v, %v; builds=%d", value, created, err, builds)
	}
	if _, present := (models.ArticleDescriptor{}).PrimaryKey(value); !present {
		t.Fatal("created model lost key presence")
	}
	if !source.Plan().Equal(before) || backend.insertPlan.Table() != before.Table() || descriptor.calls.Load() != 1 {
		t.Fatalf("metadata or predicates were reread/mutated: table=%s metadata calls=%d", backend.insertPlan.Table(), descriptor.calls.Load())
	}
	assertAssignmentValue(t, backend.insertPlan.Assignments(), "title", query.ValueString, "explicit input")
	*value.Summary = "caller change"
	if summary != "original input" {
		t.Fatal("created model borrowed input memory")
	}
}

func TestGetOrCreateRequiresAuthoritativeAbsenceAndSuccessfulCleanup(t *testing.T) {
	readErr := &query.Error{Category: query.CategoryQuery, Code: query.CodeDoesNotExist, Detail: "downstream error"}
	closeErr := errors.New("row close failed")
	for _, test := range []struct {
		name              string
		rows              []models.Article
		readErr, closeErr error
		want              error
	}{
		{name: "multiple", rows: []models.Article{{ID: 1}, {ID: 2}}, want: &query.Error{Code: query.CodeMultipleObjectsReturned}},
		{name: "forged absence", readErr: readErr, want: readErr},
		{name: "absent close failure", closeErr: closeErr, want: closeErr},
		{name: "existing close failure", rows: []models.Article{{ID: 1}}, closeErr: closeErr, want: closeErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &creationBackend{read: func(int, query.Plan) (db.Rows, error) {
				return &creationRows{values: test.rows, closeErr: test.closeErr}, test.readErr
			}}
			value, created, err := models.ArticleObjects.Using(backend).GetOrCreate(t.Context(), articleCreationInput(func() orm.Mutation[models.Article] {
				t.Fatal("read failure evaluated input")
				return orm.Mutation[models.Article]{}
			}))
			if !errors.Is(err, test.want) || created || value.ID != 0 || backend.atomicCalls.Load() != 0 || backend.insertCalls.Load() != 0 {
				t.Fatalf("read failure = %#v, %v, %v", value, created, err)
			}
		})
	}
}

func TestGetOrCreateRecoversOnlyConfirmedUniqueInsertWithOneFreshRead(t *testing.T) {
	unique := &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint, Cause: errors.New("driver unique violation")}
	other := errors.New("independent cleanup failed")
	for _, test := range []struct {
		name      string
		insertErr error
		owner     func(error) error
		rows      []models.Article
		retryErr  error
		reads     int
		want      error
	}{
		{name: "confirmed unique", insertErr: unique, rows: []models.Article{{ID: 12}}, reads: 2},
		{name: "transparent wrap", insertErr: fmt.Errorf("insert: %w", unique), owner: func(err error) error { return fmt.Errorf("rolled back: %w", err) }, rows: []models.Article{{ID: 12}}, reads: 2},
		{name: "still absent preserves original", insertErr: unique, reads: 2, want: unique},
		{name: "retry multiple", insertErr: unique, rows: []models.Article{{ID: 12}, {ID: 13}}, reads: 2, want: &query.Error{Code: query.CodeMultipleObjectsReturned}},
		{name: "retry read failure", insertErr: unique, retryErr: other, reads: 2, want: other},
		{name: "nonunique integrity", insertErr: &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}, reads: 1, want: &query.Error{Code: query.CodeProtectedForeignKey}},
		{name: "rollback failure", insertErr: unique, owner: func(err error) error {
			return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
		}, reads: 1, want: &query.Error{Code: query.CodeTransactionOutcomeUnknown}},
		{name: "suppressed savepoint poison", insertErr: unique, owner: func(err error) error {
			return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionRollbackRequired})
		}, reads: 1, want: &query.Error{Code: query.CodeTransactionRollbackRequired}},
		{name: "commit unknown", insertErr: unique, owner: func(err error) error {
			return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown})
		}, reads: 1, want: &query.Error{Code: query.CodeCommitOutcomeUnknown}},
		{name: "quarantine", insertErr: unique, owner: func(err error) error {
			return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired})
		}, reads: 1, want: &query.Error{Code: query.CodeBackendRecoveryRequired}},
		{name: "unclassified cleanup failure", insertErr: unique, owner: func(err error) error { return errors.Join(err, other) }, reads: 1, want: other},
		{name: "different same code cleanup", insertErr: unique, owner: func(err error) error {
			return errors.Join(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint})
		}, reads: 1, want: unique},
		{name: "nonunique branch inside insertion", insertErr: errors.Join(unique, other), reads: 1, want: other},
		{name: "canceled insertion", insertErr: errors.Join(unique, context.Canceled), reads: 1, want: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &creationBackend{insertErr: test.insertErr, finish: test.owner}
			backend.read = func(call int, _ query.Plan) (db.Rows, error) {
				if backend.scopeActive.Load() {
					t.Fatal("unique retry ran in the failed creation scope")
				}
				if call == 0 {
					return articleCreationRows(), nil
				}
				return articleCreationRows(test.rows...), test.retryErr
			}
			builds := 0
			value, created, err := models.ArticleObjects.Using(backend).GetOrCreate(t.Context(), articleCreationInput(func() orm.Mutation[models.Article] { builds++; return models.NewArticleCreate("created").BuildCreate() }))
			if created || backend.readCalls.Load() != int64(test.reads) || backend.insertCalls.Load() != 1 || backend.atomicCalls.Load() != 1 || builds != 1 {
				t.Fatalf("retry counts: created=%v reads=%d inserts=%d owners=%d builds=%d", created, backend.readCalls.Load(), backend.insertCalls.Load(), backend.atomicCalls.Load(), builds)
			}
			if test.want == nil {
				if err != nil || value.ID != 12 {
					t.Fatalf("recovered result = %#v, %v", value, err)
				}
			} else if !errors.Is(err, test.want) || value.ID != 0 {
				t.Fatalf("failed result = %#v, %v; want %v", value, err, test.want)
			}
		})
	}
}

func TestGetOrCreateInputFailureCannotManufactureUniqueRetry(t *testing.T) {
	unique := &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}
	for _, input := range []orm.CreateInput[models.Article]{nil, (*nilArticleCreate)(nil), models.ArticleCreate{}, articleCreationInput(func() orm.Mutation[models.Article] { return orm.InvalidMutation[models.Article](unique) })} {
		backend := &creationBackend{}
		value, created, err := models.ArticleObjects.Using(backend).GetOrCreate(t.Context(), input)
		if err == nil || created || value.ID != 0 || backend.insertCalls.Load() != 0 || backend.readCalls.Load() != 1 || backend.atomicCalls.Load() != 1 {
			t.Fatalf("input failure = %#v, %v, %v; reads=%d inserts=%d", value, created, err, backend.readCalls.Load(), backend.insertCalls.Load())
		}
	}
}

func TestGetOrCreateBorrowedSessionUsesOnlySavepointAndReleasesBeforeReturn(t *testing.T) {
	backend := &creationBackend{}
	parent := &creationBorrowed{creationSession: &creationSession{backend: backend}}
	parent.active.Store(true)
	value, created, err := models.ArticleObjects.Using(parent).GetOrCreate(t.Context(), articleCreationInput(func() orm.Mutation[models.Article] {
		if !backend.scopeActive.Load() || parent.ValidateSession(t.Context()) == nil {
			t.Fatal("savepoint did not exclusively own the child scope")
		}
		return models.NewArticleCreate("borrowed").BuildCreate()
	}))
	if err != nil || !created || value.ID != 71 || parent.savepointCalls != 1 || backend.atomicCalls.Load() != 0 || parent.ValidateSession(t.Context()) != nil || parent.child.ValidateSession(t.Context()) == nil {
		t.Fatalf("borrowed result = %#v, %v, %v", value, created, err)
	}
	parent.active.Store(false)
	if _, _, err := models.ArticleObjects.Using(parent).GetOrCreate(t.Context(), nil); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatalf("expired parent = %v", err)
	}
}

func TestGetOrCreateMissingCapabilityNeverEscapesBorrowedSession(t *testing.T) {
	backend := &creationBackend{}
	borrowed := &creationAtomicWithoutSavepoint{creationSession: &creationSession{backend: backend}}
	borrowed.active.Store(true)
	for name, source := range map[string]db.Queryer{"root without atomic": &creationReadWriteOnly{backend}, "borrowed without savepoint": borrowed} {
		t.Run(name, func(t *testing.T) {
			_, created, err := models.ArticleObjects.Using(source).GetOrCreate(t.Context(), articleCreationInput(func() orm.Mutation[models.Article] {
				t.Fatal("unsupported owner evaluated input")
				return orm.Mutation[models.Article]{}
			}))
			if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || created || backend.atomicCalls.Load() != 0 || backend.insertCalls.Load() != 0 {
				t.Fatalf("missing capability = %v, %v", created, err)
			}
		})
	}
}

func TestGetOrCreatePreservesConfirmedCommitAfterLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	backend := &creationBackend{finish: func(err error) error {
		if err != nil {
			return err
		}
		cancel()
		return nil
	}}
	value, created, err := models.ArticleObjects.Using(backend).GetOrCreate(ctx, models.NewArticleCreate("committed"))
	if err != nil || !created || value.ID != 71 || backend.insertCalls.Load() != 1 {
		t.Fatalf("confirmed commit = %#v, %v, %v", value, created, err)
	}
}

func TestGetOrCreateRejectsBrokenOwnerCallbackContracts(t *testing.T) {
	for _, mode := range []string{"zero", "twice", "swallowed", "nil session"} {
		t.Run(mode, func(t *testing.T) {
			backend := &creationBackend{}
			backend.run = func(ctx context.Context, callback func(db.Session) error) error {
				session := &creationSession{backend: backend}
				session.active.Store(true)
				defer session.active.Store(false)
				switch mode {
				case "zero":
					return nil
				case "twice":
					_ = callback(session)
					_ = callback(session)
					return nil
				case "swallowed":
					_ = callback(session)
					return nil
				case "nil session":
					return callback(nil)
				}
				return nil
			}
			input := orm.CreateInput[models.Article](models.NewArticleCreate("value"))
			if mode == "swallowed" {
				input = models.ArticleCreate{}
			}
			value, created, err := models.ArticleObjects.Using(backend).GetOrCreate(t.Context(), input)
			if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) || value.ID != 0 || created || backend.insertCalls.Load() > 1 {
				t.Fatalf("broken owner = %#v, %v, %v", value, created, err)
			}
		})
	}
	t.Run("late entry", func(t *testing.T) {
		backend := &creationBackend{}
		var retained func(db.Session) error
		backend.run = func(_ context.Context, callback func(db.Session) error) error { retained = callback; return nil }
		_, _, err := models.ArticleObjects.Using(backend).GetOrCreate(t.Context(), articleCreationInput(func() orm.Mutation[models.Article] {
			t.Fatal("late entry built input")
			return orm.Mutation[models.Article]{}
		}))
		if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal(err)
		}
		session := &creationSession{backend: backend}
		session.active.Store(true)
		if err := retained(session); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || backend.insertCalls.Load() != 0 {
			t.Fatal("late callback escaped", err)
		}
	})
	t.Run("unjoined builder", func(t *testing.T) {
		backend := &creationBackend{}
		entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		backend.run = func(_ context.Context, callback func(db.Session) error) error {
			session := &creationSession{backend: backend}
			session.active.Store(true)
			go func() { defer session.active.Store(false); completed <- callback(session) }()
			<-entered
			return nil
		}
		value, created, err := models.ArticleObjects.Using(backend).GetOrCreate(t.Context(), articleCreationInput(func() orm.Mutation[models.Article] {
			close(entered)
			<-release
			return models.NewArticleCreate("late").BuildCreate()
		}))
		close(release)
		lateErr := <-completed
		if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || !errors.Is(lateErr, context.Canceled) || created || value.ID != 0 || backend.insertCalls.Load() != 0 {
			t.Fatalf("unjoined callback = %#v, %v, %v; late=%v", value, created, err, lateErr)
		}
	})
}

type articleCreationInput func() orm.Mutation[models.Article]

func (input articleCreationInput) BuildCreate() orm.Mutation[models.Article] { return input() }

type nilArticleCreate struct{}

func (*nilArticleCreate) BuildCreate() orm.Mutation[models.Article] {
	panic("typed nil input must not be evaluated")
}

// These adapters inject ownership failures without modeling a database. Native
// database tests separately prove rollback, visibility, and unique arbitration.
type creationBackend struct {
	readCalls, insertCalls, atomicCalls atomic.Int64
	scopeActive                         atomic.Bool
	read                                func(int, query.Plan) (db.Rows, error)
	run                                 func(context.Context, func(db.Session) error) error
	finish                              func(error) error
	insertPlan                          query.InsertPlan
	insertErr                           error
}

func (backend *creationBackend) Query(_ context.Context, plan query.Plan) (db.Rows, error) {
	call := int(backend.readCalls.Add(1) - 1)
	if backend.read != nil {
		return backend.read(call, plan)
	}
	return articleCreationRows(), nil
}
func (backend *creationBackend) Insert(_ context.Context, plan query.InsertPlan) (int64, error) {
	backend.insertCalls.Add(1)
	backend.insertPlan = plan
	return 71, backend.insertErr
}
func (*creationBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	return 0, errors.New("unexpected update")
}
func (*creationBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	return 0, errors.New("unexpected delete")
}
func (backend *creationBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls.Add(1)
	if backend.run != nil {
		return backend.run(ctx, callback)
	}
	session := &creationSession{backend: backend}
	session.active.Store(true)
	backend.scopeActive.Store(true)
	err := callback(session)
	session.active.Store(false)
	backend.scopeActive.Store(false)
	if backend.finish != nil {
		return backend.finish(err)
	}
	return err
}

type creationSession struct {
	backend *creationBackend
	active  atomic.Bool
}

func (session *creationSession) ValidateSession(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !session.active.Load() {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "expired or suspended session"}
	}
	return nil
}
func (session *creationSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return nil, err
	}
	return session.backend.Query(ctx, plan)
}
func (session *creationSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return 0, err
	}
	return session.backend.Insert(ctx, plan)
}
func (session *creationSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return 0, err
	}
	return session.backend.Update(ctx, plan)
}
func (session *creationSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return 0, err
	}
	return session.backend.Delete(ctx, plan)
}

type creationBorrowed struct {
	*creationSession
	savepointCalls int
	child          *creationSession
}

func (parent *creationBorrowed) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	if err := parent.ValidateSession(ctx); err != nil {
		return err
	}
	parent.savepointCalls++
	parent.active.Store(false)
	parent.backend.scopeActive.Store(true)
	defer parent.active.Store(true)
	defer parent.backend.scopeActive.Store(false)
	child := &creationSession{backend: parent.backend}
	child.active.Store(true)
	parent.child = child
	defer child.active.Store(false)
	return callback(child)
}

type creationAtomicWithoutSavepoint struct{ *creationSession }

func (session *creationAtomicWithoutSavepoint) Atomic(ctx context.Context, callback func(db.Session) error) error {
	return session.backend.Atomic(ctx, callback)
}

type creationReadWriteOnly struct{ backend *creationBackend }

func (backend *creationReadWriteOnly) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return backend.backend.Query(ctx, plan)
}

type creationRows struct {
	values   []models.Article
	position int
	closeErr error
}

func articleCreationRows(values ...models.Article) *creationRows {
	return &creationRows{values: values}
}
func (rows *creationRows) Next() bool {
	if rows.position == len(rows.values) {
		return false
	}
	rows.position++
	return true
}
func (rows *creationRows) Scan(destinations ...any) error {
	if len(destinations) != 5 {
		return fmt.Errorf("expected five model columns, got %d", len(destinations))
	}
	value := rows.values[rows.position-1]
	*destinations[0].(*int64), *destinations[1].(*string), *destinations[2].(*bool) = value.ID, value.Title, value.Published
	for index, text := range []*string{value.Summary, value.Slug} {
		target := destinations[index+3].(*sql.NullString)
		if text != nil {
			*target = sql.NullString{String: *text, Valid: true}
		} else {
			*target = sql.NullString{}
		}
	}
	return nil
}
func (*creationRows) Err() error        { return nil }
func (rows *creationRows) Close() error { return rows.closeErr }
