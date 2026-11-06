package orm_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	fixtureblog "github.com/progresshans/godj/conformance/relationfixture/blog"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type queryUpdateBackend struct {
	*bulkBackend
	plans       []query.QueryUpdatePlan
	checks      int
	checkError  error
	updateQuery func(context.Context, query.QueryUpdatePlan) (int64, error)
}

func newQueryUpdateBackend() *queryUpdateBackend {
	return &queryUpdateBackend{bulkBackend: newBulkBackend()}
}
func (backend *queryUpdateBackend) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) error {
	backend.checks++
	return errors.Join(ctx.Err(), backend.checkError, plan.Validate())
}
func (*queryUpdateBackend) QueryUpdate(context.Context, query.QueryUpdatePlan) (int64, error) {
	return 0, errors.New("query update escaped its transaction")
}
func (backend *queryUpdateBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	return backend.bulkBackend.Atomic(ctx, func(session db.Session) error {
		if session == nil {
			return callback(nil)
		}
		return callback(&queryUpdateSession{session, backend})
	})
}

type queryUpdateSession struct {
	db.Session
	owner *queryUpdateBackend
}

func (session *queryUpdateSession) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session *queryUpdateSession) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) error {
	if err := session.ValidateSession(ctx); err != nil {
		return err
	}
	return session.owner.CheckQueryUpdate(ctx, plan)
}
func (session *queryUpdateSession) QueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (int64, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return 0, err
	}
	session.owner.plans = append(session.owner.plans, plan)
	if session.owner.updateQuery != nil {
		return session.owner.updateQuery(ctx, plan)
	}
	return 3, nil
}
func (session *queryUpdateSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	return db.WithSavepoint(ctx, session.Session, func(child db.Session) error { return callback(&queryUpdateSession{child, session.owner}) })
}

func TestQueryUpdateTypedDynamicAndOwnedSource(t *testing.T) {
	descriptor := &mutableManagerDescriptor{metadata: (models.ArticleDescriptor{}).Metadata()}
	manager := orm.NewManager[models.Article](descriptor)
	backend := newQueryUpdateBackend()
	source := manager.Using(backend).Filter(models.ArticleFields.Title.Exact("scope")).OrderBy(models.ArticleFields.ID.Desc()).Distinct()
	original := source.Plan()
	descriptor.metadata.DBTable = "foreign"
	descriptor.metadata.Fields[1].Column = "changed"
	assignments := []orm.UpdateAssignment[models.Article]{orm.Assign(models.ArticleFields.Title, "owned"), orm.AssignNull(models.ArticleFields.Summary), orm.AssignExpression(models.ArticleFields.ID, orm.Add(orm.F(models.ArticleFields.ID), int64(1)))}
	backend.updateQuery = func(_ context.Context, plan query.QueryUpdatePlan) (int64, error) {
		assignments[0] = orm.Assign(models.ArticleFields.Title, "later")
		return 3, nil
	}
	if count, err := source.Update(t.Context(), assignments...); err != nil || count != 3 || backend.atomicCalls.Load() != 1 || len(backend.plans) != 1 {
		t.Fatal(count, err)
	}
	plan := backend.plans[0]
	literal, ok := plan.Assignments()[0].Expression().Literal()
	if !ok || !literal.Equal(query.String("owned")) || plan.Selection().Table() != original.Table() || len(plan.Selection().Conditions()) != 1 || len(plan.Selection().Orderings()) != 0 || plan.Selection().Distinct() {
		t.Fatal("lost source or input ownership", plan)
	}
	if !source.Plan().Equal(original) {
		t.Fatal("query plan mutated")
	}
	backend.updateQuery = nil
	if count, err := source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "title", Value: "owned"}, orm.DynamicUpdateInput{Field: "summary", Value: nil}, orm.DynamicUpdateInput{Field: "id", Value: orm.DynamicF("id").Add(int64(1))}); err != nil || count != 3 || !plan.Equal(backend.plans[1]) {
		t.Fatal("typed and dynamic plans diverged", count, err)
	}
	for name, call := range map[string]func() error{
		"zero": func() error { _, err := source.Update(t.Context(), orm.UpdateAssignment[models.Article]{}); return err },
		"nil_field": func() error {
			_, err := source.Update(t.Context(), orm.Assign[models.Article, int64](nil, 1))
			return err
		},
		"nil_operand": func() error {
			_, err := source.Update(t.Context(), orm.AssignExpression[models.Article, int64](models.ArticleFields.ID, nil))
			return err
		},
		"foreign_metadata": func() error {
			field := orm.NewStringField[models.Article](ir.Field{Name: "title", GoName: "Title", Column: "wrong", Kind: ir.FieldChar, MaxLength: 100})
			_, err := source.Update(t.Context(), orm.Assign(field, "x"))
			return err
		},
		"duplicate": func() error {
			_, err := source.Update(t.Context(), orm.Assign(models.ArticleFields.Title, "a"), orm.Assign(models.ArticleFields.Title, "b"))
			return err
		},
		"unknown": func() error {
			_, err := source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "missing", Value: 1})
			return err
		},
		"unknown_operand": func() error {
			_, err := source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "id", Value: orm.DynamicF("missing").Add(1)})
			return err
		},
		"joined_operand": func() error {
			_, err := source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "title", Value: orm.DynamicF("category__name")})
			return err
		},
		"wrong_kind": func() error {
			_, err := source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "id", Value: orm.DynamicF("title")})
			return err
		},
		"mixed_numeric": func() error {
			_, err := source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "id", Value: orm.DynamicF("id").Add(0.5)})
			return err
		},
		"null_nonnullable": func() error {
			_, err := source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "title", Value: nil})
			return err
		},
		"mutable_raw": func() error {
			_, err := source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "title", Value: []byte("text")})
			return err
		},
		"zero_dynamic": func() error {
			_, err := source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "id", Value: orm.DynamicExpression{}})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := len(backend.plans)
			calls := backend.atomicCalls.Load()
			if err := call(); err == nil || len(backend.plans) != before || backend.atomicCalls.Load() != calls {
				t.Fatal("invalid update entered transaction", err)
			}
		})
	}
	deep := orm.DynamicF("id")
	for index := 0; index < query.MaximumScalarDepth; index++ {
		deep = deep.Negate()
	}
	if _, err := source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "id", Value: deep}); err == nil {
		t.Fatal("unbounded dynamic depth")
	}
}

func TestQueryUpdateForeignKeyTypedMetadata(t *testing.T) {
	backend := newQueryUpdateBackend()
	source := fixtureblog.PostObjects.Using(backend)
	fields := fixtureblog.PostFields
	count, err := source.Update(t.Context(), orm.AssignExpression(fields.AuthorID, orm.F(fields.AuthorID)), orm.AssignNull(fields.ReviewerID))
	if err != nil || count != 3 {
		t.Fatal(count, err)
	}
	count, err = source.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "author", Value: orm.DynamicF("author")}, orm.DynamicUpdateInput{Field: "reviewer", Value: nil})
	if err != nil || count != 3 || !backend.plans[0].Equal(backend.plans[1]) {
		t.Fatal("typed and dynamic foreign key assignments diverged", count, err)
	}
	metadata := (fixtureblog.PostDescriptor{}).Metadata()
	for _, field := range metadata.Fields {
		if field.Name != "author" {
			continue
		}
		for _, invalid := range []orm.UpdateAssignment[fixtureblog.Post]{
			orm.Assign(orm.NewIntegerField[fixtureblog.Post](field), int64(1)),
			orm.AssignNull(orm.NewNullableForeignKeyField[fixtureblog.Post](field)),
		} {
			before := backend.atomicCalls.Load()
			if count, err := source.Update(t.Context(), invalid); count != 0 || err == nil || backend.atomicCalls.Load() != before {
				t.Fatal("foreign key metadata mismatch entered I/O", count, err)
			}
		}
		return
	}
	t.Fatal("foreign key fixture missing")
}

func TestQueryUpdateCacheSuccessFailureAndNoOp(t *testing.T) {
	for _, mode := range []string{"success", "empty_assignments", "empty_filter", "failure", "commit_unknown"} {
		t.Run(mode, func(t *testing.T) {
			backend := newQueryUpdateBackend()
			backend.read = func(call int, _ query.Plan) (db.Rows, error) {
				return articleCreationRows(models.Article{ID: 1, Title: fmt.Sprint("snapshot-", call)}), nil
			}
			source := models.ArticleObjects.Using(backend)
			if mode == "empty_filter" {
				source = source.Filter(models.ArticleFields.ID.In())
			}
			before, err := source.All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			sibling := source.Filter()
			siblingBefore, err := sibling.All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			assignment := []orm.UpdateAssignment[models.Article]{orm.Assign(models.ArticleFields.Title, "updated")}
			if mode == "empty_assignments" {
				assignment = nil
			}
			if mode == "failure" {
				backend.updateQuery = func(context.Context, query.QueryUpdatePlan) (int64, error) { return 99, errors.New("native rejected") }
			}
			if mode == "commit_unknown" {
				backend.finish = func(error) error {
					return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
				}
			}
			count, err := source.Update(t.Context(), assignment...)
			failed := mode == "failure" || mode == "commit_unknown"
			if (err != nil) != failed || failed && count != 0 {
				t.Fatal(count, err)
			}
			if (mode == "empty_assignments" || mode == "empty_filter") && (count != 0 || len(backend.plans) != 0 || backend.atomicCalls.Load() != 0) {
				t.Fatal("no-op performed a write", count)
			}
			after, err := source.All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			siblingAfter, err := sibling.All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if mode != "empty_filter" {
				if (after[0].Title == before[0].Title) != (mode == "failure") {
					t.Fatal("wrong own-cache lifetime", before, after)
				}
				if siblingAfter[0].Title != siblingBefore[0].Title {
					t.Fatal("derived query cache changed")
				}
			}
		})
	}
	backend := newQueryUpdateBackend()
	source, err := models.ArticleObjects.Using(backend).Limit(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Update(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || backend.atomicCalls.Load() != 0 {
		t.Fatal("sliced no-op accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if count, err := source.Update(ctx, orm.UpdateAssignment[models.Article]{}); count != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("context precedence", count, err)
	}
	backend = newQueryUpdateBackend()
	backend.checkError = errors.New("unsupported backend expression")
	if _, err := models.ArticleObjects.Using(backend).Update(t.Context()); err == nil || backend.atomicCalls.Load() != 0 {
		t.Fatal("no-op bypassed backend validation", err)
	}
}

func TestQueryUpdateTransactionAndHostileOwners(t *testing.T) {
	for _, mode := range []string{"negative_count", "native_error", "cancel", "missing_callback", "nil_session", "repeat_callback", "swallowed_error", "commit_unknown", "rollback_unknown"} {
		t.Run(mode, func(t *testing.T) {
			backend := newQueryUpdateBackend()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("native failure")
			switch mode {
			case "negative_count":
				backend.updateQuery = func(context.Context, query.QueryUpdatePlan) (int64, error) { return -1, nil }
			case "native_error":
				backend.updateQuery = func(context.Context, query.QueryUpdatePlan) (int64, error) { return 11, failure }
			case "cancel":
				backend.updateQuery = func(context.Context, query.QueryUpdatePlan) (int64, error) { cancel(); return 3, nil }
			case "missing_callback":
				backend.runBulk = func(context.Context, func(db.Session) error, *bulkSession) error { return nil }
			case "nil_session":
				backend.runBulk = func(_ context.Context, callback func(db.Session) error, _ *bulkSession) error { return callback(nil) }
			case "repeat_callback":
				backend.runBulk = func(_ context.Context, callback func(db.Session) error, session *bulkSession) error {
					_ = callback(session)
					return callback(session)
				}
			case "swallowed_error":
				backend.updateQuery = func(context.Context, query.QueryUpdatePlan) (int64, error) { return 0, failure }
				backend.finish = func(error) error { return nil }
			case "commit_unknown":
				backend.finish = func(error) error {
					return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
				}
			case "rollback_unknown":
				backend.updateQuery = func(context.Context, query.QueryUpdatePlan) (int64, error) { return 0, failure }
				backend.finish = func(err error) error {
					return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
				}
			}
			count, err := models.ArticleObjects.Update(ctx, backend, orm.Assign(models.ArticleFields.Title, "new"))
			if count != 0 || err == nil {
				t.Fatal("owner published incomplete result", count, err)
			}
			if mode == "native_error" || mode == "swallowed_error" || mode == "rollback_unknown" {
				if !errors.Is(err, failure) {
					t.Fatal("original error lost", err)
				}
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if len(backend.plans) > 1 {
				t.Fatal("repeated callback wrote twice")
			}
		})
	}
	backend := newQueryUpdateBackend()
	parent := &queryUpdateSession{backend.bulkBackend.session(), backend}
	if count, err := models.ArticleObjects.Using(parent).Update(t.Context(), orm.Assign(models.ArticleFields.Title, "inner")); err != nil || count != 3 || backend.savepoints != 1 || backend.atomicCalls.Load() != 0 {
		t.Fatal("borrowed update did not use a savepoint", count, err)
	}
	if err := parent.ValidateSession(t.Context()); err != nil {
		t.Fatal("parent lost ownership after savepoint", err)
	}
}
