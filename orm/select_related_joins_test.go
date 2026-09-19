package orm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

func TestForwardSelectMixedJoinsFailureDoesNotPublishPartialDuplicates(t *testing.T) {
	for _, failureKind := range []string{"scan", "rows", "close", "cancel", "integrity"} {
		t.Run(failureKind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New(failureKind + " failure")
			value := selectRelatedRequiredValue()
			reviewerID := value.source.AuthorID
			value.source.ReviewerID = &reviewerID
			failed := &selectRelatedRows{values: []selectRelatedJoinedValue{value, value}}
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
			case "integrity":
				corrupt := value
				corrupt.target = &relationObjectTestAuthor{ID: 999, Name: "wrong target"}
				failed.values = append(failed.values, corrupt)
				failure = &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectProjection}
			}
			success := &selectRelatedRows{values: []selectRelatedJoinedValue{value, value}}
			backend := &selectRelatedBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
				statement, _, err := sqlite.Compile(plan)
				if err != nil {
					return nil, err
				}
				if strings.Count(statement, " JOIN ") != 2 {
					t.Fatalf("missing filter/projection join: %s", statement)
				}
				if call == 0 {
					return failed, nil
				}
				return success, nil
			}}
			eager := orderedRequiredSelectQuery(t, backend)
			projectionProjections := eager.plan.RelationProjections()
			ok := len(projectionProjections) == 1
			var projection query.RelationProjection
			if ok {
				projection = projectionProjections[0]
			}
			if !ok {
				t.Fatal("missing selected relation")
			}
			hop := projection.TerminalHop()
			path, err := query.NewForwardRelationPath(hop.Source(), hop.SourceTable(), "reviewer", "reviewer_id", hop.Target(), hop.TargetTable(), hop.TargetPrimaryKeyColumn(), true, query.NewFieldRef("name", "name", query.FieldString, false))
			if err != nil {
				t.Fatal(err)
			}
			eager.plan, err = eager.plan.WithConditions(query.NewRelatedCondition(path, query.LookupExact, query.String("Ada")))
			if err != nil {
				t.Fatal(err)
			}
			if rows, err := eager.All(ctx); rows != nil || !errors.Is(err, failure) {
				t.Fatalf("failed All=%v,%v", rows, err)
			}
			if _, ready := eager.evaluation.cachedValues(); ready {
				t.Fatal("partial duplicate batch published")
			}
			rows, err := eager.All(t.Context())
			if err != nil || len(rows) != 2 || rows[0] == rows[1] {
				t.Fatalf("retry duplicates=%v,%v", rows, err)
			}
			if count, err := eager.Count(t.Context()); err != nil || count != 2 {
				t.Fatalf("warm Count=%d,%v", count, err)
			}
			if row, found, err := eager.First(t.Context()); err != nil || !found || row == nil {
				t.Fatalf("warm First=%v,%v,%v", row, found, err)
			}
			if backend.callCount() != 2 || failed.closeCalls.Load() != 1 || success.closeCalls.Load() != 1 {
				t.Fatal("retry or rows lifetime changed")
			}
		})
	}
}
