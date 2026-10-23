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

func newBulkUpdateBackend() *bulkBackend {
	backend := newBulkBackend()
	backend.updateLimit = query.MaximumBulkRows
	return backend
}
func (backend *bulkBackend) BulkUpdateBatchSize(ctx context.Context, _ query.BulkUpdateSpec) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return backend.updateLimit, backend.updateLimitErr
}
func (*bulkBackend) BulkUpdate(context.Context, query.BulkUpdatePlan) (int64, error) {
	return 0, errors.New("bulk update escaped its transaction")
}
func (session *bulkSession) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (int, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return 0, err
	}
	return session.owner.BulkUpdateBatchSize(ctx, spec)
}
func (session *bulkSession) BulkUpdate(ctx context.Context, plan query.BulkUpdatePlan) (int64, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return 0, err
	}
	owner := session.owner
	owner.updatePlans = append(owner.updatePlans, plan)
	if owner.update != nil {
		return owner.update(len(owner.updatePlans)-1, plan)
	}
	unique := make(map[int64]bool)
	for _, key := range plan.Keys() {
		unique[key] = true
	}
	return int64(len(unique)), nil
}

func bulkUpdateValues(keys ...int64) []models.Article {
	values := make([]models.Article, len(keys))
	for i, key := range keys {
		values[i] = models.NewArticleWithID(key)
		values[i].Title = fmt.Sprint("title-", i)
	}
	return values
}
func bulkTitleMask() orm.BulkUpdateOption[models.Article] {
	return orm.BulkUpdateFields(models.ArticleFields.Title)
}
func requireZeroBulkUpdate(t *testing.T, count int64, err error) {
	t.Helper()
	if count != 0 || err == nil {
		t.Fatal("partial count or success after failure", count, err)
	}
}

func TestBulkUpdateSelectedInputOwnershipAndBatchCounts(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		t.Run(fmt.Sprint("dynamic_", dynamic), func(t *testing.T) {
			backend := newBulkUpdateBackend()
			backend.updateLimit = 3
			values := bulkUpdateValues(0, -9, 9007199254740993, 0, 888)
			summary := "owned"
			for i := range values {
				values[i].Summary = &summary
			}
			fields := []orm.WritableField[models.Article]{models.ArticleFields.Summary, models.ArticleFields.Title, models.ArticleFields.Title}
			mask := orm.BulkUpdateFields(fields...)
			fields[0] = nil
			if dynamic {
				names := []string{"summary", "title", "title"}
				mask = orm.BulkUpdateFieldNames[models.Article](names...)
				names[0] = "id"
			}
			before := slices.Clone(values)
			backend.update = func(call int, plan query.BulkUpdatePlan) (int64, error) {
				if call == 0 {
					values[4].Title = "caller changed later input"
					summary = "caller changed pointer"
				}
				if plan.Spec().Fields()[0].Name() != "title" || plan.Spec().Fields()[1].Name() != "summary" {
					t.Fatal("field order not normalized", plan.Spec().Fields())
				}
				for i, row := range plan.Rows() {
					if !row[0].Equal(query.String(before[call*3+i].Title)) || !row[1].Equal(query.String("owned")) {
						t.Fatal("prepared values alias caller input", call, i, row)
					}
				}
				return int64(plan.RowCount()), nil
			}
			count, err := models.ArticleObjects.BulkUpdate(t.Context(), backend, values, mask, orm.BulkUpdateBatchSize[models.Article](4))
			if err != nil || count != 5 || len(backend.updatePlans) != 2 || backend.atomicCalls.Load() != 1 {
				t.Fatal(count, err, len(backend.updatePlans))
			}
			if !slices.Equal(backend.updatePlans[0].Keys(), []int64{0, -9, 9007199254740993}) || !slices.Equal(backend.updatePlans[1].Keys(), []int64{0, 888}) {
				t.Fatal("key order lost")
			}
		})
	}
	for _, requested := range []int{1, 2, 3} {
		backend := newBulkUpdateBackend()
		count, err := models.ArticleObjects.BulkUpdate(t.Context(), backend, bulkUpdateValues(1, 1, 2), bulkTitleMask(), orm.BulkUpdateBatchSize[models.Article](requested))
		want := int64(2)
		if requested == 1 {
			want = 3
		}
		if err != nil || count != want {
			t.Fatal("batch-local duplicate count", requested, count, err)
		}
	}
	for _, count := range []int64{0, 1} {
		backend := newBulkUpdateBackend()
		backend.update = func(int, query.BulkUpdatePlan) (int64, error) { return count, nil }
		got, err := models.ArticleObjects.BulkUpdate(t.Context(), backend, bulkUpdateValues(1, 2), bulkTitleMask())
		if err != nil || got != count {
			t.Fatal("missing/filtered keys should reduce matched count", got, err)
		}
	}
}

func TestBulkUpdateRetainsManagerPredicateAndWarmCache(t *testing.T) {
	descriptor := &mutableManagerDescriptor{metadata: (models.ArticleDescriptor{}).Metadata()}
	manager := orm.NewManager[models.Article](descriptor)
	backend := newBulkUpdateBackend()
	backend.read = func(int, query.Plan) (db.Rows, error) {
		return articleCreationRows(models.Article{ID: 3, Title: "cached"}), nil
	}
	source := manager.Using(backend).Filter(models.ArticleFields.Title.Exact("scope")).OrderBy(models.ArticleFields.ID.Desc()).Distinct()
	warm, err := source.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	original := source.Plan()
	descriptor.metadata.DBTable = "foreign_table"
	descriptor.metadata.Fields[1].Column = "foreign_column"
	values := bulkUpdateValues(3)
	before := slices.Clone(values)
	count, err := source.BulkUpdate(t.Context(), values, bulkTitleMask())
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	planned := backend.updatePlans[0].Spec().Selection()
	gotWhere, _ := planned.Where()
	wantWhere, _ := original.Where()
	if !gotWhere.Equal(wantWhere) || planned.Table() != original.Table() || planned.Distinct() || len(planned.Orderings()) != 0 || !source.Plan().Equal(original) || !reflect.DeepEqual(values, before) {
		t.Fatal("bulk update lost source ownership")
	}
	cached, err := source.All(t.Context())
	if err != nil || !reflect.DeepEqual(cached, warm) || backend.readCalls.Load() != 1 || descriptor.calls.Load() != 1 {
		t.Fatal("bulk update changed read cache or metadata snapshot", err)
	}
}

type bulkUpdateDescriptor struct {
	models.ArticleDescriptor
	key   func(models.Article) (query.Value, bool)
	field func(models.Article, ir.Field) (query.Value, bool)
}

func (d bulkUpdateDescriptor) PrimaryKey(value models.Article) (query.Value, bool) {
	if d.key != nil {
		return d.key(value)
	}
	return d.ArticleDescriptor.PrimaryKey(value)
}
func (d bulkUpdateDescriptor) WriteFieldValue(value models.Article, field ir.Field) (query.Value, bool) {
	if d.field != nil {
		return d.field(value, field)
	}
	return d.ArticleDescriptor.WriteFieldValue(value, field)
}

func TestBulkUpdateValidatesAllKeysAndOnlySelectedFieldsBeforeWriting(t *testing.T) {
	for _, mode := range []string{"missing_key", "nonzero_without_presence", "invalid_key", "invalid_selected", "canceled_preparation", "invalid_unselected"} {
		t.Run(mode, func(t *testing.T) {
			backend := newBulkUpdateBackend()
			values := bulkUpdateValues(1, 2)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			descriptor := bulkUpdateDescriptor{}
			switch mode {
			case "missing_key":
				values[1] = models.Article{Title: "late"}
			case "nonzero_without_presence":
				values[1] = models.Article{ID: 2, Title: "late"}
			case "invalid_key":
				descriptor.key = func(value models.Article) (query.Value, bool) {
					if value.ID == 2 {
						return query.String("2"), true
					}
					return query.Integer(value.ID), true
				}
			default:
				descriptor.field = func(value models.Article, field ir.Field) (query.Value, bool) {
					if value.ID == 2 && mode == "invalid_selected" {
						return query.Integer(2), true
					}
					if value.ID == 2 && mode == "canceled_preparation" {
						cancel()
					}
					if mode == "invalid_unselected" && field.Name == "summary" {
						t.Fatal("unselected field read")
						return query.Integer(1), true
					}
					return descriptor.ArticleDescriptor.WriteFieldValue(value, field)
				}
			}
			manager := orm.NewManager[models.Article](descriptor)
			count, err := manager.BulkUpdate(ctx, backend, values, bulkTitleMask(), orm.BulkUpdateBatchSize[models.Article](1))
			if mode == "invalid_unselected" {
				if err != nil || count != 2 {
					t.Fatal(count, err)
				}
				return
			}
			requireZeroBulkUpdate(t, count, err)
			if len(backend.updatePlans) != 0 {
				t.Fatal("late preparation failure wrote earlier rows")
			}
			if mode == "canceled_preparation" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestBulkUpdateRejectsInvalidMasksBeforeOpeningScope(t *testing.T) {
	var nilField *orm.StringField[models.Article]
	for index, options := range [][]orm.BulkUpdateOption[models.Article]{
		nil, {{}}, {orm.BulkUpdateFields[models.Article]()}, {orm.BulkUpdateFieldNames[models.Article]()},
		{bulkTitleMask(), orm.BulkUpdateFields(models.ArticleFields.Summary)},
		{bulkTitleMask(), orm.BulkUpdateFieldNames[models.Article]("summary")},
		{orm.BulkUpdateFieldNames[models.Article]("id")}, {orm.BulkUpdateFieldNames[models.Article]("missing")},
		{orm.BulkUpdateFields[models.Article](nil)}, {orm.BulkUpdateFields[models.Article](nilField)},
		{bulkTitleMask(), orm.BulkUpdateBatchSize[models.Article](0)},
		{bulkTitleMask(), orm.BulkUpdateBatchSize[models.Article](-1)},
		{bulkTitleMask(), orm.BulkUpdateBatchSize[models.Article](1), orm.BulkUpdateBatchSize[models.Article](2)},
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			backend := newBulkUpdateBackend()
			count, err := models.ArticleObjects.BulkUpdate(t.Context(), backend, nil, options...)
			requireZeroBulkUpdate(t, count, err)
			if backend.atomicCalls.Load() != 0 || len(backend.updatePlans) != 0 {
				t.Fatal("invalid mask opened scope")
			}
		})
	}
}

func TestBulkUpdateDiscardsCountsOnBackendScopeAndCancellationFailures(t *testing.T) {
	for _, borrowed := range []bool{false, true} {
		for _, mode := range []string{"native", "negative", "excess", "duplicate_excess", "late_batch", "commit_unknown", "rollback_unknown", "cleanup", "canceled", "expired_child"} {
			t.Run(fmt.Sprintf("borrowed_%t/%s", borrowed, mode), func(t *testing.T) {
				backend := newBulkUpdateBackend()
				values := bulkUpdateValues(1, 2)
				before := slices.Clone(values)
				options := []orm.BulkUpdateOption[models.Article]{bulkTitleMask()}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				failure := &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}
				cleanup := errors.New("cleanup failed")
				if mode == "late_batch" {
					options = append(options, orm.BulkUpdateBatchSize[models.Article](1))
				}
				if mode == "duplicate_excess" {
					values[1] = values[0]
					before = slices.Clone(values)
				}
				backend.update = func(call int, plan query.BulkUpdatePlan) (int64, error) {
					switch mode {
					case "native", "cleanup", "rollback_unknown":
						return 1, failure
					case "negative":
						return -1, nil
					case "excess":
						return 3, nil
					case "duplicate_excess":
						return 2, nil
					case "late_batch":
						if call == 1 {
							return 1, failure
						}
					case "canceled":
						cancel()
						return 1, failure
					case "expired_child":
						backend.child.active.Store(false)
					}
					return int64(plan.RowCount()), nil
				}
				var finish func(error) error
				switch mode {
				case "commit_unknown":
					finish = func(error) error {
						return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
					}
				case "rollback_unknown":
					finish = func(err error) error {
						return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
					}
				case "cleanup":
					finish = func(err error) error { return errors.Join(err, cleanup) }
				}
				var target db.Queryer = backend
				if borrowed {
					target = backend.session()
					backend.finishInner = finish
				} else {
					backend.finish = finish
				}
				count, err := models.ArticleObjects.BulkUpdate(ctx, target, values, options...)
				requireZeroBulkUpdate(t, count, err)
				if !reflect.DeepEqual(values, before) {
					t.Fatal("failed update mutated input")
				}
				if mode == "native" && err != failure || mode == "cleanup" && (!errors.Is(err, failure) || !errors.Is(err, cleanup)) || mode == "canceled" && (!errors.Is(err, failure) || !errors.Is(err, context.Canceled)) {
					t.Fatal("error cause/ownership lost", err)
				}
				if mode == "late_batch" && len(backend.updatePlans) != 2 {
					t.Fatal("late batch not reached")
				}
			})
		}
	}
}

func TestBulkUpdateEmptySliceLimitsCapabilityAndParentLifetime(t *testing.T) {
	backend := newBulkUpdateBackend()
	source, err := models.ArticleObjects.Using(backend).Limit(1)
	if err != nil {
		t.Fatal(err)
	}
	count, err := source.BulkUpdate(t.Context(), nil, bulkTitleMask())
	if err != nil || count != 0 || backend.atomicCalls.Load() != 0 {
		t.Fatal("empty sliced operation", count, err)
	}
	count, err = source.BulkUpdate(t.Context(), bulkUpdateValues(1), bulkTitleMask())
	requireZeroBulkUpdate(t, count, err)
	if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || backend.atomicCalls.Load() != 0 {
		t.Fatal("sliced write was widened", err)
	}
	for _, limit := range []int{0, -1} {
		backend := newBulkUpdateBackend()
		backend.updateLimit = limit
		count, err := models.ArticleObjects.BulkUpdate(t.Context(), backend, bulkUpdateValues(1), bulkTitleMask())
		requireZeroBulkUpdate(t, count, err)
		if len(backend.updatePlans) != 0 {
			t.Fatal("invalid limit wrote")
		}
	}
	for _, target := range []db.Queryer{nil, &creationBackend{}, &creationReadWriteOnly{backend: &creationBackend{}}} {
		count, err := models.ArticleObjects.BulkUpdate(t.Context(), target, nil, bulkTitleMask())
		requireZeroBulkUpdate(t, count, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		count, err := models.ArticleObjects.BulkUpdate(ctx, backend, nil, bulkTitleMask())
		requireZeroBulkUpdate(t, count, err)
	}
	parent := backend.session()
	count, err = models.ArticleObjects.BulkUpdate(t.Context(), parent, bulkUpdateValues(1), bulkTitleMask())
	if err != nil || count != 1 || backend.savepoints != 1 || backend.atomicCalls.Load() != 0 || parent.ValidateSession(t.Context()) != nil || backend.child.ValidateSession(t.Context()) == nil {
		t.Fatal("borrowed lifetime", count, err)
	}
	parent.active.Store(false)
	count, err = models.ArticleObjects.BulkUpdate(t.Context(), parent, nil, bulkTitleMask())
	requireZeroBulkUpdate(t, count, err)
	if backend.savepoints != 1 {
		t.Fatal("expired empty operation opened scope")
	}
	ctx, cancelAfterCommit := context.WithCancel(t.Context())
	defer cancelAfterCommit()
	backend.finish = func(err error) error {
		if err == nil {
			cancelAfterCommit()
		}
		return err
	}
	count, err = models.ArticleObjects.BulkUpdate(ctx, backend, bulkUpdateValues(1), bulkTitleMask())
	if err != nil || count != 1 || ctx.Err() == nil {
		t.Fatal("post-commit cancel lost confirmed count", count, err)
	}
}

func TestBulkUpdateRejectsBrokenScopeOwners(t *testing.T) {
	for _, mode := range []string{"zero", "twice", "swallowed", "nil_session", "late_entry", "missing_capability", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			backend := newBulkUpdateBackend()
			failure := errors.New("native failure")
			var late func(db.Session) error
			backend.update = func(int, query.BulkUpdatePlan) (int64, error) { return 0, failure }
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
				case "concurrent":
					entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
					backend.update = func(int, query.BulkUpdatePlan) (int64, error) { close(entered); <-release; return 1, nil }
					go func() { done <- callback(session) }()
					select {
					case <-entered:
					case err := <-done:
						return err
					case <-ctx.Done():
						close(release)
						<-done
						return ctx.Err()
					}
					second := callback(session)
					close(release)
					return errors.Join(second, <-done)
				}
				return failure
			}
			count, err := models.ArticleObjects.BulkUpdate(t.Context(), backend, bulkUpdateValues(1), bulkTitleMask())
			requireZeroBulkUpdate(t, count, err)
			if late != nil && late(backend.session()) == nil {
				t.Fatal("sealed callback accepted a late write")
			}
			if len(backend.updatePlans) > 1 {
				t.Fatal("broken owner repeated writes")
			}
		})
	}
}
