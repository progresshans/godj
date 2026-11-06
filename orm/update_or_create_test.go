package orm_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type articlePatchInput func(models.Article) orm.Mutation[models.Article]

func (input articlePatchInput) BuildPatch(value models.Article) orm.Mutation[models.Article] {
	return input(value)
}

// These adapters exercise the ORM's failure/ownership boundary. The native
// generated consumers separately establish actual locks, rollback and races.
type upsertBackend struct {
	*creationBackend
	policy              db.ReadModifyWritePolicy
	updates, savepoints atomic.Int64
	depth               atomic.Int64
	updatePlan          query.UpdatePlan
	updateErr           error
	updateRows          int64
	outer, inner        func(context.Context, func(db.Session) error, db.Session) error
	finishInner         func(error) error
}

func newUpsertBackend() *upsertBackend {
	return &upsertBackend{creationBackend: &creationBackend{}, policy: db.ReadModifyWriteRowLock, updateRows: 1}
}

func (backend *upsertBackend) Update(_ context.Context, plan query.UpdatePlan) (int64, error) {
	backend.updates.Add(1)
	backend.updatePlan = plan
	return backend.updateRows, backend.updateErr
}

func (backend *upsertBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls.Add(1)
	session := backend.session(1)
	backend.scopeActive.Store(true)
	backend.depth.Store(1)
	defer session.active.Store(false)
	defer backend.scopeActive.Store(false)
	defer backend.depth.Store(0)
	var err error
	if backend.outer != nil {
		err = backend.outer(ctx, callback, session)
	} else {
		err = callback(session)
	}
	if backend.finish != nil {
		return backend.finish(err)
	}
	return err
}

func (backend *upsertBackend) session(depth int64) *upsertSession {
	value := &upsertSession{creationSession: &creationSession{backend: backend.creationBackend}, owner: backend, depth: depth}
	value.active.Store(true)
	return value
}

type upsertSession struct {
	*creationSession
	owner *upsertBackend
	depth int64
}

func (session *upsertSession) ReadModifyWritePolicy(ctx context.Context) (db.ReadModifyWritePolicy, error) {
	return session.owner.policy, session.ValidateSession(ctx)
}

func (session *upsertSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return 0, err
	}
	return session.owner.Update(ctx, plan)
}

func (parent *upsertSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	if err := parent.ValidateSession(ctx); err != nil {
		return err
	}
	parent.owner.savepoints.Add(1)
	parent.active.Store(false)
	defer parent.active.Store(true)
	child := parent.owner.session(parent.depth + 1)
	defer child.active.Store(false)
	parent.owner.depth.Store(child.depth)
	defer parent.owner.depth.Store(parent.depth)
	var err error
	if parent.owner.inner != nil {
		err = parent.owner.inner(ctx, callback, child)
	} else {
		err = callback(child)
	}
	if parent.owner.finishInner != nil {
		return parent.owner.finishInner(err)
	}
	return err
}

func TestUpdateOrCreateUsesCurrentLockedValueAndPreservesManagerAndCache(t *testing.T) {
	for _, policy := range []db.ReadModifyWritePolicy{db.ReadModifyWriteRowLock, db.ReadModifyWriteConflict} {
		t.Run(string(policy), func(t *testing.T) {
			backend := newUpsertBackend()
			backend.policy = policy
			oldSummary := "cached"
			backend.read = func(call int, plan query.Plan) (db.Rows, error) {
				lock, locked := plan.RowLock()
				if call == 0 {
					if locked || backend.scopeActive.Load() {
						t.Fatal("ordinary warm read changed")
					}
					return articleCreationRows(models.Article{ID: 7, Title: "old", Summary: &oldSummary}), nil
				}
				if call != 1 || !backend.scopeActive.Load() || locked != (policy == db.ReadModifyWriteRowLock) || plan.Table() != "godj_conformance_article" {
					t.Fatalf("lookup ownership: call=%d lock=%v table=%s", call, locked, plan.Table())
				}
				if locked && (len(lock.Targets()) != 1 || !lock.Targets()[0].Self() || lock.WaitPolicy() != query.LockWait) {
					t.Fatal("upsert did not lock its root")
				}
				return articleCreationRows(models.Article{ID: 7, Title: "current", Summary: &oldSummary}), nil
			}
			descriptor := &mutableManagerDescriptor{metadata: (models.ArticleDescriptor{}).Metadata()}
			source := orm.NewManager[models.Article](descriptor).Using(backend).Filter(models.ArticleFields.ID.Exact(7))
			warm, err := source.All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			descriptor.metadata.DBTable = "foreign_table"
			builds := 0
			value, created, err := source.UpdateOrCreate(t.Context(), nil, articlePatchInput(func(current models.Article) orm.Mutation[models.Article] {
				builds++
				if backend.depth.Load() != 1 || current.Title != "current" || current.Summary == warm[0].Summary {
					t.Fatal("patch did not receive an owned current model inside the transaction")
				}
				return (models.ArticlePatch{}.WithTitle(current.Title + " patched")).BuildPatch(current)
			}))
			if err != nil || created || value.Title != "current patched" || builds != 1 || backend.updates.Load() != 1 || backend.insertCalls.Load() != 0 || backend.savepoints.Load() != 0 || descriptor.calls.Load() != 1 {
				t.Fatalf("updated result=%#v/%v/%v", value, created, err)
			}
			*value.Summary = "caller change"
			cached, err := source.All(t.Context())
			if err != nil || cached[0].Title != "old" || *cached[0].Summary != "cached" || oldSummary != "cached" || backend.readCalls.Load() != 2 {
				t.Fatal("upsert changed or shared the source cache", err)
			}
		})
	}
}

func TestUpdateOrCreateCreatesOnlyInsideItsInnerSavepoint(t *testing.T) {
	backend := newUpsertBackend()
	builds := 0
	value, created, err := models.ArticleObjects.Using(backend).Filter(models.ArticleFields.Title.Exact("lookup")).UpdateOrCreate(t.Context(), articleCreationInput(func() orm.Mutation[models.Article] {
		builds++
		if backend.depth.Load() != 2 {
			t.Fatal("creation input was evaluated outside its savepoint")
		}
		return models.NewArticleCreate("explicit input").BuildCreate()
	}), nil)
	if err != nil || !created || value.ID != 71 || value.Title != "explicit input" || builds != 1 || backend.atomicCalls.Load() != 1 || backend.savepoints.Load() != 1 || backend.insertCalls.Load() != 1 || backend.updates.Load() != 0 {
		t.Fatalf("create result=%#v/%v/%v", value, created, err)
	}
}

func TestUpdateOrCreateEmptyPatchStillValidatesTheCompleteMutation(t *testing.T) {
	for _, mode := range []string{"valid", "changed_omitted", "changed_key", "absent_key", "wrong_table", "forged_empty_error", "nil", "typed_nil"} {
		t.Run(mode, func(t *testing.T) {
			backend := newUpsertBackend()
			summary := "owned"
			backend.read = func(int, query.Plan) (db.Rows, error) {
				return articleCreationRows(models.Article{ID: 7, Title: "original", Summary: &summary}), nil
			}
			var input orm.PatchInput[models.Article] = articlePatchInput(func(current models.Article) orm.Mutation[models.Article] {
				table := (models.ArticleDescriptor{}).Metadata().DBTable
				switch mode {
				case "changed_omitted":
					*current.Summary = "injected"
				case "changed_key":
					current.ID++
				case "absent_key":
					(models.ArticleDescriptor{}).ClearPrimaryKey(&current)
				case "wrong_table":
					table = "foreign"
				case "forged_empty_error":
					return orm.InvalidMutation[models.Article](&query.Error{Code: query.CodeEmptyPatch})
				}
				return orm.NewPatchMutation(current, table, nil)
			})
			if mode == "nil" {
				input = nil
			}
			if mode == "typed_nil" {
				input = articlePatchInput(nil)
			}
			value, created, err := models.ArticleObjects.Using(backend).UpdateOrCreate(t.Context(), nil, input)
			if created || backend.updates.Load() != 0 || backend.insertCalls.Load() != 0 || summary != "owned" {
				t.Fatal("empty patch wrote or aliased caller memory")
			}
			if mode == "valid" {
				if err != nil || value.ID != 7 || value.Summary == &summary {
					t.Fatalf("valid no-op=%#v/%v", value, err)
				}
				if _, err := models.ArticleObjects.Patch(t.Context(), backend, value, models.ArticlePatch{}); !errors.Is(err, &query.Error{Code: query.CodeEmptyPatch}) {
					t.Fatal("ordinary Update lost its empty-patch policy", err)
				}
				if _, err := models.ArticleObjects.ValidateUniqueUpdate(t.Context(), backend, value, models.ArticlePatch{}); !errors.Is(err, &query.Error{Code: query.CodeEmptyPatch}) {
					t.Fatal("unique patch validation lost its empty-patch policy", err)
				}
			} else if err == nil || value.ID != 0 {
				t.Fatalf("invalid empty patch accepted=%#v/%v", value, err)
			}
		})
	}
}

func TestUpdateOrCreateRecoversOnlyTheActualUniqueCreate(t *testing.T) {
	unique := &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}
	other := errors.New("independent failure")
	for _, mode := range []string{"recover", "transparent_wrap", "still_absent", "multiple", "retry_error", "create_input", "non_unique", "rollback_unknown", "cleanup_error", "same_code_cleanup", "update_unique", "outer_commit_unknown"} {
		t.Run(mode, func(t *testing.T) {
			backend := newUpsertBackend()
			backend.insertErr = unique
			backend.read = func(call int, plan query.Plan) (db.Rows, error) {
				if !backend.scopeActive.Load() || backend.depth.Load() != 1 {
					t.Fatal("winner lookup escaped the outer transaction or ran in the failed child")
				}
				if _, locked := plan.RowLock(); !locked {
					t.Fatal("winner lookup lost locking")
				}
				if call == 0 && mode != "update_unique" || mode == "still_absent" {
					return articleCreationRows(), nil
				}
				if mode == "retry_error" {
					return nil, other
				}
				if mode == "multiple" {
					return articleCreationRows(models.Article{ID: 1}, models.Article{ID: 2}), nil
				}
				return articleCreationRows(models.Article{ID: 12, Title: "winner"}), nil
			}
			switch mode {
			case "transparent_wrap":
				backend.finishInner = func(err error) error { return fmt.Errorf("confirmed rollback: %w", err) }
			case "non_unique":
				backend.insertErr = &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}
			case "rollback_unknown":
				backend.finishInner = func(err error) error {
					return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
				}
			case "cleanup_error":
				backend.finishInner = func(err error) error { return errors.Join(err, other) }
			case "same_code_cleanup":
				backend.finishInner = func(err error) error {
					return errors.Join(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint})
				}
			case "update_unique":
				backend.updateErr = unique
			case "outer_commit_unknown":
				backend.insertErr = nil
				backend.finish = func(error) error {
					return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
				}
			}
			creates, patches := 0, 0
			value, created, err := models.ArticleObjects.Using(backend).UpdateOrCreate(t.Context(), articleCreationInput(func() orm.Mutation[models.Article] {
				creates++
				if mode == "create_input" {
					return orm.InvalidMutation[models.Article](unique)
				}
				return models.NewArticleCreate("attempt").BuildCreate()
			}), articlePatchInput(func(current models.Article) orm.Mutation[models.Article] {
				patches++
				return (models.ArticlePatch{}.WithTitle("patched " + current.Title)).BuildPatch(current)
			}))
			if created || creates > 1 || patches > 1 || backend.atomicCalls.Load() != 1 || backend.savepoints.Load() > 1 {
				t.Fatal("operation retried a scope or input")
			}
			if mode == "recover" || mode == "transparent_wrap" {
				if err != nil || value.ID != 12 || value.Title != "patched winner" || creates != 1 || patches != 1 || backend.readCalls.Load() != 2 {
					t.Fatalf("recovery=%#v/%v", value, err)
				}
			} else if err == nil || value.ID != 0 {
				t.Fatalf("unrecoverable result=%#v/%v", value, err)
			}
			if mode == "create_input" && (backend.insertCalls.Load() != 0 || backend.readCalls.Load() != 1) {
				t.Fatal("input error manufactured a unique recovery")
			}
			if mode == "update_unique" && (creates != 0 || backend.insertCalls.Load() != 0 || backend.readCalls.Load() != 1) {
				t.Fatal("update failure entered creation recovery")
			}
			if mode == "rollback_unknown" || mode == "cleanup_error" || mode == "same_code_cleanup" || mode == "non_unique" {
				if patches != 0 || backend.readCalls.Load() != 1 {
					t.Fatal("uncertain or non-unique failure reread the database")
				}
			}
		})
	}
}

func TestUpdateOrCreateRequiresAuthoritativeReadAndNativePolicy(t *testing.T) {
	for _, mode := range []string{"forged_absence", "close_failure", "multiple", "unknown_policy", "missing_policy", "skip_locked", "explicit_on_conflict", "zero_update", "too_many_updates"} {
		t.Run(mode, func(t *testing.T) {
			backend := newUpsertBackend()
			failure := errors.New("read close failure")
			backend.read = func(int, query.Plan) (db.Rows, error) {
				switch mode {
				case "forged_absence":
					return nil, &query.Error{Code: query.CodeDoesNotExist}
				case "close_failure":
					return &creationRows{closeErr: failure}, nil
				case "multiple":
					return articleCreationRows(models.Article{ID: 1}, models.Article{ID: 2}), nil
				}
				return articleCreationRows(models.Article{ID: 1}), nil
			}
			source := models.ArticleObjects.Using(backend)
			switch mode {
			case "unknown_policy":
				backend.policy = ""
			case "missing_policy":
				backend.outer = func(ctx context.Context, callback func(db.Session) error, session db.Session) error {
					return callback(session.(*upsertSession).creationSession)
				}
			case "skip_locked":
				source = source.SelectForUpdate(orm.RowLockOptions{SkipLocked: true})
			case "explicit_on_conflict":
				backend.policy = db.ReadModifyWriteConflict
				source = source.SelectForUpdate(orm.RowLockOptions{})
			case "zero_update":
				backend.updateRows = 0
			case "too_many_updates":
				backend.updateRows = 2
			}
			value, created, err := source.UpdateOrCreate(t.Context(), articleCreationInput(func() orm.Mutation[models.Article] {
				t.Fatal("invalid read/policy evaluated create")
				return orm.Mutation[models.Article]{}
			}), models.ArticlePatch{}.WithTitle("changed"))
			if err == nil || created || value.ID != 0 || backend.insertCalls.Load() != 0 {
				t.Fatalf("invalid result=%#v/%v/%v", value, created, err)
			}
			if mode == "unknown_policy" || mode == "missing_policy" || mode == "skip_locked" || mode == "explicit_on_conflict" {
				if backend.readCalls.Load() != 0 || backend.updates.Load() != 0 {
					t.Fatal("unsafe policy executed I/O")
				}
			}
		})
	}
}

func TestUpdateOrCreateRejectsBrokenOuterAndInnerOwners(t *testing.T) {
	for _, scope := range []string{"outer", "inner"} {
		for _, mode := range []string{"zero", "twice", "swallowed", "nil_session", "late_entry", "unjoined_builder"} {
			t.Run(scope+"/"+mode, func(t *testing.T) {
				backend := newUpsertBackend()
				failure := errors.New("input failed")
				var late func(db.Session) error
				entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan error, 1)
				var builds atomic.Int64
				broken := func(ctx context.Context, callback func(db.Session) error, session db.Session) error {
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
					case "nil_session":
						return callback(nil)
					case "late_entry":
						late = callback
						return nil
					case "unjoined_builder":
						go func() { completed <- callback(session) }()
						<-entered
						return nil
					}
					return errors.New("unknown broken owner")
				}
				if scope == "outer" {
					backend.outer = broken
				} else {
					backend.inner = broken
				}
				value, created, err := models.ArticleObjects.Using(backend).UpdateOrCreate(t.Context(), articleCreationInput(func() orm.Mutation[models.Article] {
					builds.Add(1)
					if mode == "unjoined_builder" {
						close(entered)
						<-release
						return models.NewArticleCreate("too late").BuildCreate()
					}
					return orm.InvalidMutation[models.Article](failure)
				}), nil)
				if err == nil || created || value.ID != 0 {
					t.Fatalf("broken owner returned success=%#v/%v/%v", value, created, err)
				}
				if late != nil {
					if err := late(backend.session(1)); err == nil || builds.Load() != 0 {
						t.Fatal("late callback was accepted", err)
					}
				}
				if mode == "unjoined_builder" {
					close(release)
					if err := <-completed; err == nil {
						t.Fatal("late builder retained a usable context")
					}
				}
				if builds.Load() > 1 || backend.insertCalls.Load() != 0 || backend.updates.Load() != 0 {
					t.Fatal("broken owner repeated input or wrote after returning")
				}
			})
		}
	}
}

func TestUpdateOrCreateBorrowedLifetimeAndLateCommitCancellation(t *testing.T) {
	for _, creating := range []bool{false, true} {
		t.Run(fmt.Sprint(creating), func(t *testing.T) {
			backend := newUpsertBackend()
			if !creating {
				backend.read = func(int, query.Plan) (db.Rows, error) {
					return articleCreationRows(models.Article{ID: 7, Title: "existing"}), nil
				}
			}
			parent := backend.session(1)
			value, created, err := models.ArticleObjects.Using(parent).UpdateOrCreate(t.Context(), models.NewArticleCreate("new"), models.ArticlePatch{})
			wantScopes := int64(1)
			if creating {
				wantScopes++
			}
			if err != nil || created != creating || value.ID == 0 || backend.atomicCalls.Load() != 0 || backend.savepoints.Load() != wantScopes || parent.ValidateSession(t.Context()) != nil {
				t.Fatalf("borrowed result=%#v/%v/%v", value, created, err)
			}
			parent.active.Store(false)
			if _, _, err := models.ArticleObjects.Using(parent).UpdateOrCreate(t.Context(), nil, nil); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("expired parent accepted", err)
			}
			callCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			backend.finish = func(err error) error {
				if err == nil {
					cancel()
				}
				return err
			}
			value, created, err = models.ArticleObjects.Using(backend).UpdateOrCreate(callCtx, models.NewArticleCreate("committed"), models.ArticlePatch{}.WithTitle("committed"))
			if err != nil || created != creating || value.ID == 0 || callCtx.Err() == nil {
				t.Fatalf("confirmed commit became cancellation=%#v/%v/%v", value, created, err)
			}
		})
	}
}
