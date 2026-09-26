package query_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestRelationSetNullPlanRetainsComparableImmutableState(t *testing.T) {
	t.Parallel()

	reviewer := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	plan := query.NewRelationSetNullPlan("blog_post", reviewer, query.Integer(2))
	assertRelationMutationComparable(plan)

	if plan.Table() != "blog_post" || !plan.ForeignKey().Equal(reviewer) || !plan.TargetKey().Equal(query.Integer(2)) {
		t.Fatalf("RelationSetNullPlan getters = (%q, %#v, %#v)", plan.Table(), plan.ForeignKey(), plan.TargetKey())
	}
	if !plan.Equal(query.NewRelationSetNullPlan("blog_post", reviewer, query.Integer(2))) {
		t.Fatal("equal relation SET_NULL plans differ")
	}
	for _, different := range []query.RelationSetNullPlan{
		query.NewRelationSetNullPlan("other_post", reviewer, query.Integer(2)),
		query.NewRelationSetNullPlan(
			"blog_post",
			query.NewFieldRef("editor", "editor_id", query.FieldInteger, true),
			query.Integer(2),
		),
		query.NewRelationSetNullPlan("blog_post", reviewer, query.Integer(3)),
	} {
		if plan.Equal(different) {
			t.Fatalf("plan omitted differing state: %#v", different)
		}
	}

	var zero query.RelationSetNullPlan
	if !zero.Equal(query.RelationSetNullPlan{}) || zero.Table() != "" ||
		!zero.ForeignKey().Equal(query.FieldRef{}) || !zero.TargetKey().Equal(query.Value{}) {
		t.Fatalf("zero RelationSetNullPlan getters = (%q, %#v, %#v)", zero.Table(), zero.ForeignKey(), zero.TargetKey())
	}
}

func TestRelationMutationErrorCodesAreStableAndDistinct(t *testing.T) {
	t.Parallel()

	codes := []string{
		query.CodeUnsavedRelatedObject,
		query.CodeProtectedForeignKey,
		query.CodeCommitOutcomeUnknown,
		query.CodeTransactionOutcomeUnknown,
	}
	want := []string{
		"unsaved_related_object",
		"protected_foreign_key",
		"commit_outcome_unknown",
		"transaction_outcome_unknown",
	}
	seen := make(map[string]struct{}, len(codes))
	for index, code := range codes {
		if code != want[index] {
			t.Fatalf("relation mutation error code[%d] = %q, want %q", index, code, want[index])
		}
		if _, duplicate := seen[code]; duplicate {
			t.Fatalf("relation mutation error code %q is duplicated", code)
		}
		seen[code] = struct{}{}
	}
}

func TestProtectedForeignKeyErrorIsConstructibleAndPreservesTaxonomy(t *testing.T) {
	t.Parallel()

	for _, protectedSourceRows := range []int64{1, 2, math.MaxInt64} {
		protected, err := query.NewProtectedForeignKeyError(protectedSourceRows)
		if err != nil {
			t.Fatalf("NewProtectedForeignKeyError(%d) error = %v", protectedSourceRows, err)
		}
		if protected == nil || protected.ProtectedSourceRows() != protectedSourceRows {
			t.Fatalf("NewProtectedForeignKeyError(%d) = %#v", protectedSourceRows, protected)
		}
		if !errors.Is(protected, &query.Error{
			Category: query.CategoryIntegrity,
			Code:     query.CodeProtectedForeignKey,
		}) {
			t.Fatalf("protected error = %v, want integrity_error/protected_foreign_key", protected)
		}

		var typed *query.ProtectedForeignKeyError
		if !errors.As(protected, &typed) || typed != protected {
			t.Fatalf("errors.As protected = %#v, want original %#v", typed, protected)
		}
		var classified *query.Error
		if !errors.As(protected, &classified) || classified.Category != query.CategoryIntegrity ||
			classified.Code != query.CodeProtectedForeignKey {
			t.Fatalf("errors.As query error = %#v", classified)
		}
		if !strings.Contains(protected.Error(), "protected_foreign_key") ||
			!strings.Contains(protected.Error(), "protected source rows") {
			t.Fatalf("ProtectedForeignKeyError.Error() = %q", protected.Error())
		}
	}
}

func TestProtectedForeignKeyErrorRejectsNonPositiveCounts(t *testing.T) {
	t.Parallel()

	for _, protectedSourceRows := range []int64{0, -1, math.MinInt64} {
		protected, err := query.NewProtectedForeignKeyError(protectedSourceRows)
		if protected != nil {
			t.Fatalf("NewProtectedForeignKeyError(%d) = %#v, want nil", protectedSourceRows, protected)
		}
		if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
			t.Fatalf("NewProtectedForeignKeyError(%d) error = %v, want query_error/invalid_plan", protectedSourceRows, err)
		}
	}
}

func TestProtectedForeignKeyErrorZeroAndNilValuesFailClosed(t *testing.T) {
	t.Parallel()

	values := []*query.ProtectedForeignKeyError{nil, {}}
	for _, value := range values {
		if value.ProtectedSourceRows() != 0 || value.Unwrap() != nil {
			t.Fatalf("invalid protected error = count %d unwrap %#v", value.ProtectedSourceRows(), value.Unwrap())
		}
		if value.Error() == "" {
			t.Fatal("invalid protected error returned an empty diagnostic")
		}
		if errors.Is(value, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}) {
			t.Fatal("invalid protected error exposed a valid integrity taxonomy")
		}
	}
}

func assertRelationMutationComparable[T comparable](T) {}
