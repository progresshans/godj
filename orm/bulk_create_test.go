package orm_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// These adapters exercise orchestration and hostile capability contracts.
// Generated native consumers establish database rollback and conflict behavior.
type bulkBackend struct {
	*creationBackend
	limits      db.BulkInsertLimits
	limitErr    error
	plans       []query.BulkInsertPlan
	next        int64
	insert      func(int, query.BulkInsertPlan) (db.BulkInsertResult, error)
	runBulk     func(context.Context, func(db.Session) error, *bulkSession) error
	finishInner func(error) error
	child       *bulkSession
	savepoints  int
}

func newBulkBackend() *bulkBackend {
	return &bulkBackend{creationBackend: &creationBackend{}, limits: db.BulkInsertLimits{Rows: 65535, Parameters: 65535}, next: 100}
}

func (backend *bulkBackend) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	return backend.limits, errors.Join(backend.limitErr, ctx.Err())
}
func (*bulkBackend) BulkInsert(context.Context, query.BulkInsertPlan) (db.BulkInsertResult, error) {
	return db.BulkInsertResult{}, errors.New("bulk write escaped its transaction")
}
func (backend *bulkBackend) session() *bulkSession {
	session := &bulkSession{creationSession: &creationSession{backend: backend.creationBackend}, owner: backend}
	session.active.Store(true)
	return session
}
func (backend *bulkBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls.Add(1)
	session := backend.session()
	backend.child = session
	backend.scopeActive.Store(true)
	defer session.active.Store(false)
	defer backend.scopeActive.Store(false)
	var err error
	if backend.runBulk != nil {
		err = backend.runBulk(ctx, callback, session)
	} else {
		err = callback(session)
	}
	if backend.finish != nil {
		return backend.finish(err)
	}
	return err
}

type bulkSession struct {
	*creationSession
	owner *bulkBackend
}

func (session *bulkSession) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return db.BulkInsertLimits{}, err
	}
	return session.owner.BulkInsertLimits(ctx)
}
func (session *bulkSession) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return db.BulkInsertResult{}, err
	}
	owner := session.owner
	owner.plans = append(owner.plans, plan)
	if owner.insert != nil {
		return owner.insert(len(owner.plans)-1, plan)
	}
	result := db.BulkInsertResult{RowsAffected: int64(plan.RowCount())}
	if !plan.ReturnsKeys() {
		return result, nil
	}
	keyColumn := slices.Index(plan.Fields(), plan.Key())
	for _, row := range plan.Rows() {
		if keyColumn >= 0 {
			key, _ := row[keyColumn].Integer()
			result.Keys = append(result.Keys, key)
		} else {
			owner.next++
			result.Keys = append(result.Keys, owner.next)
		}
	}
	return result, nil
}
func (parent *bulkSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	if err := parent.ValidateSession(ctx); err != nil {
		return err
	}
	parent.owner.savepoints++
	parent.active.Store(false)
	defer parent.active.Store(true)
	child := parent.owner.session()
	parent.owner.child = child
	defer child.active.Store(false)
	err := callback(child)
	if parent.owner.finishInner != nil {
		return parent.owner.finishInner(err)
	}
	return err
}

func requireZeroBulk(t *testing.T, value orm.BulkCreateResult[models.Article], err error) {
	t.Helper()
	if err == nil || value.Objects != nil || value.RowsAffected != 0 || value.ReturnedKeys {
		t.Fatalf("partial or successful result after failure: %#v / %v", value, err)
	}
}

func TestBulkCreateMixedKeysBatchOrderAndOwnership(t *testing.T) {
	backend := newBulkBackend()
	backend.limits = db.BulkInsertLimits{Rows: 3, Parameters: 10}
	summary := "owned input"
	values := []models.Article{{Title: "auto first", Summary: &summary}, models.NewArticleWithID(0), models.NewArticleWithID(9007199254740993), {Title: "auto last", Summary: &summary}, models.NewArticleWithID(-9)}
	values[1].Title, values[2].Title, values[4].Title = "zero", "large", "negative"
	before := slices.Clone(values)
	result, err := models.ArticleObjects.BulkCreate(t.Context(), backend, values, orm.BulkBatchSize[models.Article](2))
	if err != nil || !result.ReturnedKeys || result.RowsAffected != 5 || len(result.Objects) != 5 || len(backend.plans) != 3 || backend.atomicCalls.Load() != 1 {
		t.Fatalf("bulk result: %#v / %v; plans %d", result, err, len(backend.plans))
	}
	for index, key := range []int64{101, 0, 9007199254740993, 102, -9} {
		value := result.Objects[index]
		_, present := (models.ArticleDescriptor{}).PrimaryKey(value)
		if value.ID != key || !present || value.Title != values[index].Title {
			t.Fatalf("input-order key %d: %#v", index, value)
		}
	}
	for index, shape := range [][2]int{{2, 5}, {1, 5}, {2, 4}} {
		if backend.plans[index].RowCount() != shape[0] || len(backend.plans[index].Fields()) != shape[1] {
			t.Fatal("wrong explicit-first parameter split", backend.plans)
		}
	}
	if !reflect.DeepEqual(values, before) || result.Objects[0].Summary == &summary || result.Objects[0].Summary == result.Objects[3].Summary {
		t.Fatal("bulk mutated or borrowed caller models")
	}
	*result.Objects[0].Summary = "caller changed result"
	if summary != "owned input" || *result.Objects[3].Summary != "owned input" {
		t.Fatal("result aliases another input/output")
	}
	if backend.child.ValidateSession(t.Context()) == nil {
		t.Fatal("result escaped while child scope remained active")
	}
}

func TestBulkCreateBuildersDefaultsMetadataAndWarmCache(t *testing.T) {
	descriptor := &mutableManagerDescriptor{metadata: (models.ArticleDescriptor{}).Metadata()}
	manager := orm.NewManager[models.Article](descriptor)
	backend := newBulkBackend()
	backend.read = func(int, query.Plan) (db.Rows, error) {
		return articleCreationRows(models.Article{ID: 3, Title: "cached"}), nil
	}
	source := manager.Using(backend).Filter(models.ArticleFields.Title.Exact("read-only predicate")).OrderBy(models.ArticleFields.ID.Desc())
	warm, err := source.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before := source.Plan()
	descriptor.metadata.DBTable = "mutated"
	descriptor.metadata.Fields[1].Column = "mutated"
	builds := 0
	inputs := []orm.CreateInput[models.Article]{articleCreationInput(func() orm.Mutation[models.Article] {
		builds++
		if !backend.scopeActive.Load() || len(backend.plans) != 0 {
			t.Fatal("builder evaluated outside preparation scope")
		}
		return models.NewArticleCreate("explicit input").BuildCreate()
	}), models.NewArticleCreate("second").WithPublished(true)}
	result, err := source.BulkCreateInputs(t.Context(), inputs)
	if err != nil || builds != 1 || len(result.Objects) != 2 || result.Objects[0].Title != "explicit input" || result.Objects[0].Published || result.Objects[0].Summary != nil || !result.Objects[1].Published {
		t.Fatalf("builders/defaults: %#v / %v", result, err)
	}
	cached, err := source.All(t.Context())
	if err != nil || !reflect.DeepEqual(cached, warm) || backend.readCalls.Load() != 1 || !source.Plan().Equal(before) || backend.plans[0].Table() != before.Table() || descriptor.calls.Load() != 1 {
		t.Fatal("bulk changed query cache, plan or metadata snapshot", err)
	}
	adapted := orm.CreateInputs[models.Article]([]models.ArticleCreate{models.NewArticleCreate("adapted")})
	result, err = manager.BulkCreateInputs(t.Context(), backend, adapted)
	if err != nil || result.Objects[0].Title != "adapted" {
		t.Fatal("generated slice adapter", err)
	}
}

func TestBulkCreateValidatesEveryInputBeforeFirstInsert(t *testing.T) {
	for _, mode := range []string{"nil", "typed_nil", "invalid_mutation", "nonzero_without_presence", "canceled_last_builder"} {
		t.Run(mode, func(t *testing.T) {
			backend := newBulkBackend()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			inputs := []orm.CreateInput[models.Article]{models.NewArticleCreate("valid"), nil}
			switch mode {
			case "typed_nil":
				inputs[1] = (*nilArticleCreate)(nil)
			case "invalid_mutation":
				inputs[1] = models.ArticleCreate{}
			case "canceled_last_builder":
				inputs[1] = articleCreationInput(func() orm.Mutation[models.Article] { cancel(); return models.NewArticleCreate("late").BuildCreate() })
			}
			var result orm.BulkCreateResult[models.Article]
			var err error
			if mode == "nonzero_without_presence" {
				result, err = models.ArticleObjects.BulkCreate(ctx, backend, []models.Article{{Title: "valid"}, {ID: 9, Title: "invalid state"}}, orm.BulkBatchSize[models.Article](1))
			} else {
				result, err = models.ArticleObjects.BulkCreateInputs(ctx, backend, inputs, orm.BulkBatchSize[models.Article](1))
			}
			requireZeroBulk(t, result, err)
			if len(backend.plans) != 0 {
				t.Fatal("earlier batch wrote before final input validation")
			}
		})
	}
}

func TestBulkCreateConflictOptionsValidateBeforeScopeAndOwnSlices(t *testing.T) {
	var nilField *orm.StringField[models.Article]
	for _, options := range [][]orm.BulkCreateOption[models.Article]{
		{{}}, {orm.BulkBatchSize[models.Article](0)}, {orm.BulkBatchSize[models.Article](-1)},
		{orm.BulkBatchSize[models.Article](1), orm.BulkBatchSize[models.Article](2)},
		{orm.BulkIgnoreConflicts[models.Article](), orm.BulkIgnoreConflicts[models.Article]()},
		{orm.BulkIgnoreConflicts[models.Article](), orm.BulkUpdateConflictNames[models.Article]([]string{"id"}, []string{"title"})},
		{orm.BulkUpdateConflictNames[models.Article]([]string{"missing"}, []string{"title"})},
		{orm.BulkUpdateConflictNames[models.Article]([]string{"title"}, []string{"published"})},
		{orm.BulkUpdateConflictNames[models.Article]([]string{"id"}, []string{"id"})},
		{orm.BulkUpdateConflictNames[models.Article]([]string{"id", "id"}, []string{"title"})},
		{orm.BulkUpdateConflictNames[models.Article]([]string{"id"}, []string{"title", "title"})},
		{orm.BulkUpdateConflicts([]orm.BulkConflictField[models.Article]{nilField}, models.ArticleFields.Title)},
		{orm.BulkUpdateConflicts([]orm.BulkConflictField[models.Article]{models.ArticleFields.ID}, nilField)},
		{orm.BulkUpdateConflicts([]orm.BulkConflictField[models.Article]{models.ArticleFields.ID})},
	} {
		backend := newBulkBackend()
		result, err := models.ArticleObjects.BulkCreate(t.Context(), backend, nil, options...)
		requireZeroBulk(t, result, err)
		if backend.atomicCalls.Load() != 0 || len(backend.plans) != 0 {
			t.Fatal("invalid policy opened a transaction")
		}
	}

	metadata := (models.ArticleDescriptor{}).Metadata()
	metadata.UniqueConstraints = []ir.UniqueConstraint{{Name: "title_published", Fields: []string{"title", "published"}}}
	descriptor := &mutableManagerDescriptor{metadata: metadata}
	manager := orm.NewManager[models.Article](descriptor)
	names, updates := []string{"published", "title"}, []string{"summary"}
	dynamic := orm.BulkUpdateConflictNames[models.Article](names, updates)
	names[0], updates[0] = "missing", "id"
	target := []orm.BulkConflictField[models.Article]{models.ArticleFields.ID}
	fields := []orm.WritableField[models.Article]{models.ArticleFields.Title}
	typed := orm.BulkUpdateConflicts(target, fields...)
	target[0], fields[0] = models.ArticleFields.Summary, nil
	descriptor.metadata.UniqueConstraints[0].Fields[0] = "changed"
	for _, option := range []orm.BulkCreateOption[models.Article]{typed, dynamic} {
		backend := newBulkBackend()
		result, err := manager.BulkCreateInputs(t.Context(), backend, orm.CreateInputs[models.Article]([]models.ArticleCreate{models.NewArticleCreate("original")}), option)
		if err != nil || !result.ReturnedKeys || backend.plans[0].Conflict().Mode() != query.BulkConflictUpdate {
			t.Fatalf("owned policy: %#v / %v", result, err)
		}
	}
}

func TestBulkCreateRejectsIncompleteResultsAndScopeFailures(t *testing.T) {
	for _, mode := range []string{"short_keys", "extra_keys", "short_count", "duplicate_key", "cross_batch_duplicate", "wrong_explicit_key", "ignored_keys", "ignored_negative", "ignored_excess", "late_batch", "commit_unknown", "rollback_unknown", "cleanup_failure"} {
		t.Run(mode, func(t *testing.T) {
			backend := newBulkBackend()
			failure := errors.New("late native failure")
			values := []models.Article{{Title: "first"}, {Title: "second"}}
			options := []orm.BulkCreateOption[models.Article]{}
			if mode == "cross_batch_duplicate" || mode == "late_batch" {
				options = append(options, orm.BulkBatchSize[models.Article](1))
			}
			if mode == "wrong_explicit_key" {
				values = []models.Article{models.NewArticleWithID(-9), models.NewArticleWithID(0)}
			}
			if mode == "ignored_keys" || mode == "ignored_negative" || mode == "ignored_excess" {
				options = append(options, orm.BulkIgnoreConflicts[models.Article]())
			}
			backend.insert = func(call int, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
				result := db.BulkInsertResult{Keys: []int64{101, 102}, RowsAffected: 2}
				switch mode {
				case "short_keys":
					result.Keys = result.Keys[:1]
				case "extra_keys":
					result.Keys = append(result.Keys, 103)
				case "short_count":
					result.RowsAffected = 1
				case "duplicate_key":
					result.Keys[1] = 101
				case "cross_batch_duplicate":
					result.Keys, result.RowsAffected = []int64{101}, 1
				case "ignored_negative":
					result.Keys, result.RowsAffected = nil, -1
				case "ignored_excess":
					result.Keys, result.RowsAffected = nil, 3
				case "late_batch":
					if call == 1 {
						return db.BulkInsertResult{}, failure
					}
					result.Keys, result.RowsAffected = []int64{101}, 1
				case "rollback_unknown":
					return db.BulkInsertResult{}, failure
				}
				return result, nil
			}
			switch mode {
			case "commit_unknown":
				backend.finish = func(error) error {
					return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
				}
			case "rollback_unknown":
				backend.finish = func(err error) error {
					return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
				}
			case "cleanup_failure":
				backend.finish = func(error) error { return failure }
			}
			before := slices.Clone(values)
			result, err := models.ArticleObjects.BulkCreate(t.Context(), backend, values, options...)
			requireZeroBulk(t, result, err)
			if !reflect.DeepEqual(values, before) || backend.atomicCalls.Load() != 1 || len(backend.plans) > 2 {
				t.Fatal("failure mutated caller or retried")
			}
			if mode == "late_batch" || mode == "cross_batch_duplicate" {
				if len(backend.plans) != 2 {
					t.Fatal("did not reach later batch")
				}
			}
		})
	}
}

func TestBulkCreatePreservesConfirmedErrorAndCleanupOwnership(t *testing.T) {
	for _, borrowed := range []bool{false, true} {
		for _, mode := range []string{"native", "cleanup", "canceled"} {
			t.Run(fmt.Sprintf("borrowed_%t/%s", borrowed, mode), func(t *testing.T) {
				backend := newBulkBackend()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				failure := &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}
				cleanup := errors.New("rollback failed")
				backend.insert = func(int, query.BulkInsertPlan) (db.BulkInsertResult, error) {
					if mode == "canceled" {
						cancel()
					}
					return db.BulkInsertResult{}, failure
				}
				if mode == "cleanup" {
					finish := func(err error) error { return errors.Join(err, cleanup) }
					if borrowed {
						backend.finishInner = finish
					} else {
						backend.finish = finish
					}
				}
				var source db.Queryer = backend
				if borrowed {
					source = backend.session()
				}
				result, err := models.ArticleObjects.BulkCreate(ctx, source, []models.Article{{Title: "rejected"}})
				requireZeroBulk(t, result, err)
				if !errors.Is(err, failure) || mode == "native" && err != failure || mode == "cleanup" && !errors.Is(err, cleanup) || mode == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatal("lost direct failure or additional error owner", err)
				}
			})
		}
	}
}

func TestBulkCreateIgnoreAndUpdateDoNotInventSavedObjectState(t *testing.T) {
	for _, mode := range []string{"ignore", "update"} {
		t.Run(mode, func(t *testing.T) {
			backend := newBulkBackend()
			values := []models.Article{{Title: "first"}, models.NewArticleWithID(-4)}
			values[1].Title = "explicit"
			var option orm.BulkCreateOption[models.Article]
			if mode == "ignore" {
				option = orm.BulkIgnoreConflicts[models.Article]()
				backend.insert = func(call int, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
					return db.BulkInsertResult{RowsAffected: int64(call)}, nil
				}
			} else {
				option = orm.BulkUpdateConflicts([]orm.BulkConflictField[models.Article]{models.ArticleFields.ID}, models.ArticleFields.Title)
				backend.insert = func(int, query.BulkInsertPlan) (db.BulkInsertResult, error) {
					return db.BulkInsertResult{Keys: []int64{44}, RowsAffected: 1}, nil
				}
			}
			result, err := models.ArticleObjects.BulkCreate(t.Context(), backend, values, option)
			if err != nil || len(result.Objects) != 2 {
				t.Fatal("conflict result", result, err)
			}
			if mode == "ignore" {
				_, present := (models.ArticleDescriptor{}).PrimaryKey(result.Objects[0])
				if result.ReturnedKeys || result.RowsAffected != 1 || present || result.Objects[0].ID != 0 || result.Objects[1].ID != -4 {
					t.Fatal("ignore guessed saved keys", result)
				}
			} else if !result.ReturnedKeys || result.RowsAffected != 2 || result.Objects[0].ID != 44 || result.Objects[1].ID != 44 || result.Objects[1].Title != "explicit" {
				t.Fatal("update rejected repeated returned key or lost input order", result)
			}
		})
	}
}

func TestBulkCreateEmptyUnsupportedLimitsContextAndBorrowedLifetime(t *testing.T) {
	backend := newBulkBackend()
	for _, options := range [][]orm.BulkCreateOption[models.Article]{nil, {orm.BulkIgnoreConflicts[models.Article]()}} {
		result, err := models.ArticleObjects.BulkCreate(t.Context(), backend, nil, options...)
		if err != nil || result.Objects == nil || len(result.Objects) != 0 || result.ReturnedKeys != (len(options) == 0) || backend.atomicCalls.Load() != 0 {
			t.Fatal("empty operation performed I/O or lost policy", result, err)
		}
	}
	for _, limits := range []db.BulkInsertLimits{{Rows: 0, Parameters: 100}, {Rows: 10, Parameters: 0}, {Rows: 10, Parameters: 3}} {
		backend := newBulkBackend()
		backend.limits = limits
		result, err := models.ArticleObjects.BulkCreate(t.Context(), backend, []models.Article{{Title: "row"}})
		requireZeroBulk(t, result, err)
		if len(backend.plans) != 0 {
			t.Fatal("invalid parameter budget wrote data")
		}
	}
	for _, target := range []db.Queryer{nil, &creationBackend{}, &creationReadWriteOnly{backend: &creationBackend{}}} {
		result, err := models.ArticleObjects.BulkCreate(t.Context(), target, nil)
		requireZeroBulk(t, result, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		result, err := models.ArticleObjects.BulkCreate(ctx, backend, nil)
		requireZeroBulk(t, result, err)
	}
	parent := backend.session()
	result, err := models.ArticleObjects.BulkCreate(t.Context(), parent, []models.Article{{Title: "borrowed"}})
	if err != nil || result.Objects[0].ID == 0 || backend.savepoints != 1 || backend.atomicCalls.Load() != 0 || parent.ValidateSession(t.Context()) != nil || backend.child.ValidateSession(t.Context()) == nil {
		t.Fatal("borrowed ownership/lifetime", result, err)
	}
	parent.active.Store(false)
	result, err = models.ArticleObjects.BulkCreate(t.Context(), parent, nil)
	requireZeroBulk(t, result, err)
	if backend.savepoints != 1 {
		t.Fatal("expired empty operation opened savepoint")
	}
	ctx, cancelAfterCommit := context.WithCancel(t.Context())
	defer cancelAfterCommit()
	backend.finish = func(err error) error {
		if err == nil {
			cancelAfterCommit()
		}
		return err
	}
	result, err = models.ArticleObjects.BulkCreate(ctx, backend, []models.Article{{Title: "committed"}})
	if err != nil || !result.ReturnedKeys || result.RowsAffected != 1 || ctx.Err() == nil {
		t.Fatal("late cancellation changed confirmed commit to failure", result, err)
	}
}

func TestBulkCreateRejectsBrokenOwners(t *testing.T) {
	for _, mode := range []string{"zero", "twice", "swallowed", "nil_session", "late_entry", "missing_capability"} {
		t.Run(mode, func(t *testing.T) {
			backend := newBulkBackend()
			failure := errors.New("input failed")
			var late func(db.Session) error
			backend.runBulk = func(ctx context.Context, callback func(db.Session) error, session *bulkSession) error {
				switch mode {
				case "zero":
					return nil
				case "twice":
					_ = callback(session)
					return callback(session)
				case "swallowed":
					_ = callback(session)
					return nil
				case "nil_session":
					return callback(nil)
				case "late_entry":
					late = callback
					return nil
				case "missing_capability":
					return callback(session.creationSession)
				}
				return failure
			}
			builds := 0
			result, err := models.ArticleObjects.BulkCreateInputs(t.Context(), backend, []orm.CreateInput[models.Article]{articleCreationInput(func() orm.Mutation[models.Article] { builds++; return orm.InvalidMutation[models.Article](failure) })})
			requireZeroBulk(t, result, err)
			if late != nil && late(backend.session()) == nil {
				t.Fatal("late callback accepted")
			}
			if builds > 1 || len(backend.plans) != 0 {
				t.Fatal("broken owner rebuilt inputs or wrote")
			}
		})
	}
}
