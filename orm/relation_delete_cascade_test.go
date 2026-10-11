package orm

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func cascadeTestDeleter(t *testing.T) RelationDeleter[relationDeleteTestAuthor] {
	t.Helper()
	authors, blog := relationDeleteTestSchemas()
	blog.Models[0].Fields[2].Relation.OnDelete = ir.DeleteCascade
	children, err := schema.Build(schema.Definition{AppLabel: "children", Models: []schema.Model{
		{Name: "leaf", GoName: "Leaf", Fields: []schema.Field{schema.ForeignKey("post", "PostID", schema.Target("blog", "post"), schema.NoReverse(), schema.Cascade)}},
		{Name: "leaf_guard", GoName: "LeafGuard", Fields: []schema.Field{schema.ForeignKey("leaf", "LeafID", schema.Target("children", "leaf"), schema.NoReverse(), schema.Protect)}},
		{Name: "root_guard", GoName: "RootGuard", Fields: []schema.Field{schema.ForeignKey("author", "AuthorID", schema.Target("authors", "author"), schema.NoReverse(), schema.Protect)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := BindProject(authors, blog, children)
	if err != nil {
		t.Fatal(err)
	}
	deleter, err := BindRelationDeleter(binding, ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, relationDeleteTestAuthorDescriptor{}, relationDeleteTestActualFingerprint(t, binding))
	if err != nil {
		t.Fatal(err)
	}
	return deleter
}

func TestCascadeDeleteCollectsDeepRowsBeforeWritesAndReturnsTotal(t *testing.T) {
	deleter := cascadeTestDeleter(t)
	rowSets := []*relationDeleteTestRows{
		{values: [][2]any{{int64(10), int64(1)}, {int64(10), int64(1)}}},
		{},
		{values: [][2]any{{int64(20), int64(10)}}},
		{},
	}
	session := &relationDeleteTestSession{queryRows: []db.Rows{rowSets[0], rowSets[1], rowSets[2], rowSets[3]}, deleteCount: 1}
	session.onSetNull = func() {
		for _, rows := range rowSets {
			if rows.closeCalls != 1 || rows.errCalls != 1 {
				t.Error("mutation preceded full graph row cleanup")
			}
		}
	}
	target := relationDeleteTestAuthorValue(1)
	count, err := deleter.Delete(t.Context(), relationDeleteConformingBackend(session), &target)
	if err != nil || count != 3 || target.ID != 0 || target.primaryKeyPresent {
		t.Fatal("deep delete count or caller publication", count, err, target)
	}
	var tables []string
	for _, plan := range session.deletePlans {
		tables = append(tables, plan.Table())
	}
	if !reflect.DeepEqual(tables, []string{"children_leaf", "blog_post", "authors_author"}) || !reflect.DeepEqual(session.mutationOrder, []string{"UPDATE", "DELETE", "DELETE", "DELETE"}) {
		t.Fatal("unexpected exact-row delete order", tables, session.mutationOrder)
	}
}

func TestCascadeDeleteCompletesAllProtectChecksWithoutMutation(t *testing.T) {
	deleter := cascadeTestDeleter(t)
	session := &relationDeleteTestSession{queryRows: []db.Rows{
		&relationDeleteTestRows{values: [][2]any{{int64(10), int64(1)}}},
		&relationDeleteTestRows{values: [][2]any{{int64(9), int64(1)}}},
		&relationDeleteTestRows{values: [][2]any{{int64(20), int64(10)}}},
		&relationDeleteTestRows{values: [][2]any{{int64(9), int64(20)}}},
	}, deleteCount: 1}
	target := relationDeleteTestAuthorValue(1)
	before := target
	count, err := deleter.Delete(t.Context(), relationDeleteConformingBackend(session), &target)
	var protected *query.ProtectedForeignKeyError
	if count != 0 || !errors.As(err, &protected) || protected.ProtectedSourceRows() != 2 || target != before || len(session.queryPlans) != 4 || len(session.mutationOrder) != 0 {
		t.Fatal("partial, overlapping-model or mutating protect result", count, err, session.mutationOrder)
	}
}

func TestCascadeDeleteNestedReadFailureDiscardsEarlierProtection(t *testing.T) {
	for _, fault := range []string{"query", "rows_and_query", "scan", "iteration", "close", "nil_rows", "wrong_key", "cancel_before_scan"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			deleter := cascadeTestDeleter(t)
			failure := errors.New("nested " + fault)
			rows := &relationDeleteTestRows{values: [][2]any{{int64(20), int64(10)}}}
			var supplied db.Rows = rows
			var queryErr error
			switch fault {
			case "query":
				supplied = nil
				queryErr = failure
			case "rows_and_query":
				queryErr = failure
				rows.closeErr = errRelationDeleteClose
			case "scan":
				rows.scanErr = failure
			case "iteration":
				rows.rowsErr = failure
			case "close":
				rows.closeErr = failure
			case "nil_rows":
				supplied = nil
			case "wrong_key":
				rows.values[0][1] = int64(999)
			case "cancel_before_scan":
				rows.onNext = func(int) { cancel() }
			}
			session := &relationDeleteTestSession{queryRows: []db.Rows{
				&relationDeleteTestRows{values: [][2]any{{int64(10), int64(1)}}},
				&relationDeleteTestRows{values: [][2]any{{int64(9), int64(1)}}}, supplied,
			}, queryErrs: []error{nil, nil, queryErr}, deleteCount: 1}
			target := relationDeleteTestAuthorValue(1)
			before := target
			count, err := deleter.Delete(ctx, relationDeleteConformingBackend(session), &target)
			var protected *query.ProtectedForeignKeyError
			if count != 0 || err == nil || errors.As(err, &protected) || target != before || len(session.mutationOrder) != 0 || len(session.queryPlans) != 3 {
				t.Fatal("nested failure published protection, mutation or caller state", count, err)
			}
			switch fault {
			case "nil_rows", "wrong_key":
				if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
					t.Fatal("malformed nested rows lost invalid-plan ownership", err)
				}
			case "cancel_before_scan":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("nested cancellation was lost", err)
				}
			default:
				if !errors.Is(err, failure) {
					t.Fatal("nested native cause was lost", err)
				}
			}
			if supplied != nil && rows.closeCalls != 1 {
				t.Fatal("nested rows were not closed once", rows.closeCalls)
			}
			if fault == "rows_and_query" && !errors.Is(err, errRelationDeleteClose) {
				t.Fatal("rows returned with error lost their cleanup failure", err)
			}
		})
	}
}

type cascadeLateDeleteSession struct {
	*relationDeleteTestSession
	failAt  int
	failure error
	count   int64
}

func (s *cascadeLateDeleteSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if len(s.deletePlans) == s.failAt {
		s.deletePlans = append(s.deletePlans, plan)
		return s.count, s.failure
	}
	return s.relationDeleteTestSession.Delete(ctx, plan)
}
func TestCascadeDeleteLateMutationAndAtomicFailuresPreserveCaller(t *testing.T) {
	for _, fault := range []string{"late_delete", "wrong_count", "set_null", "commit_unknown", "rollback_unknown"} {
		t.Run(fault, func(t *testing.T) {
			deleter := cascadeTestDeleter(t)
			session := &relationDeleteTestSession{queryRows: []db.Rows{
				&relationDeleteTestRows{values: [][2]any{{int64(10), int64(1)}}}, &relationDeleteTestRows{},
				&relationDeleteTestRows{values: [][2]any{{int64(20), int64(10)}}}, &relationDeleteTestRows{},
			}, deleteCount: 1}
			failure := errors.New("failure at " + fault)
			var relationSession db.RelationSession = session
			switch fault {
			case "late_delete", "rollback_unknown":
				relationSession = &cascadeLateDeleteSession{relationDeleteTestSession: session, failAt: 2, failure: failure}
			case "wrong_count":
				relationSession = &cascadeLateDeleteSession{relationDeleteTestSession: session, failAt: 1, count: 0}
			case "set_null":
				session.setNullErrs = []error{failure}
			}
			backend := relationDeleteConformingBackend(relationSession)
			if fault == "commit_unknown" || fault == "rollback_unknown" {
				backend.invoke = func(_ context.Context, callback func(db.RelationSession) error) error {
					err := callback(relationSession)
					code := query.CodeCommitOutcomeUnknown
					if fault == "rollback_unknown" {
						code = query.CodeTransactionOutcomeUnknown
					}
					return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: code, Cause: failure})
				}
			}
			target := relationDeleteTestAuthorValue(1)
			before := target
			count, err := deleter.Delete(t.Context(), backend, &target)
			if count != 0 || err == nil || target != before || backend.calls != 1 {
				t.Fatal("failed graph delete published or retried", count, err, target, backend.calls)
			}
			if fault != "wrong_count" && !errors.Is(err, failure) {
				t.Fatal("graph failure lost its cause", err)
			}
		})
	}
}
