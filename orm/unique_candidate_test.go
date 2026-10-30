package orm_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func TestUniqueCandidateSeparatesFieldAndConstraintStages(t *testing.T) {
	_, descriptor := namedArticleManager()
	descriptor.metadata.Fields[1].Unique = true
	manager := orm.NewManager[models.Article](descriptor)
	values := map[string]query.Value{"title": query.String("same"), "published": query.Boolean(false), "summary": query.String("")}
	var plans []query.Plan
	backend := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
		plans = append(plans, plan)
		return &uniqueRows{exists: true}, nil
	})
	errors, err := manager.ValidateUniqueFields(t.Context(), backend, values, nil)
	if err != nil || errors.Len() != 1 || errors.All()[0].Field() != "title" || len(plans) != 1 {
		t.Fatal("field stage included named constraints", err, errors.All())
	}
	delete(values, "title") // Apply the field error before checking constraints.
	errors, err = manager.ValidateUniqueConstraints(t.Context(), backend, values, nil)
	if err != nil || !errors.Empty() || len(plans) != 1 {
		t.Fatal("excluded member was loaded from another source", err)
	}
	values["title"] = query.String("different")
	plans = nil
	errors, err = manager.ValidateUniqueConstraints(t.Context(), backend, values, nil)
	if err != nil || errors.Len() != 3 || len(plans) != 3 {
		t.Fatal("constraint stage lost included members or included excluded PK", err)
	}
	want := [][]string{{"title"}, {"published", "title"}, {"title", "summary"}}
	for i, plan := range plans {
		var names []string
		for _, condition := range plan.Conditions() {
			names = append(names, condition.Field().Name())
		}
		if !reflect.DeepEqual(names, want[i]) {
			t.Fatal("constraint declaration/canonical order changed", names)
		}
		if limit, ok := plan.Limit(); !ok || limit != 1 || plan.ResultShape().Kind() != query.ResultProjection {
			t.Fatal("advisory check materialized unbounded models")
		}
	}
}

func TestUniqueCandidatePreservesNullEmptyAndPresentZeroSelf(t *testing.T) {
	manager, _ := uniqueArticleManager()
	current := models.NewArticleWithID(0)
	for _, test := range []struct {
		name    string
		value   query.Value
		queries int
	}{{"empty", query.String(""), 1}, {"null", query.Null(), 0}} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			backend := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
				calls++
				where, ok := plan.Where()
				children := where.Children()
				if !ok || len(children) != 2 || children[0].Kind() != query.ExpressionNot {
					t.Fatal("missing self exclusion")
				}
				self, ok := children[0].Children()[0].Condition()
				if !ok || self.Field().Name() != "id" || !self.Value().Equal(query.Integer(0)) {
					t.Fatal("present zero key was ignored")
				}
				return &uniqueRows{}, nil
			})
			failures, err := manager.ValidateUniqueFields(t.Context(), backend, map[string]query.Value{"summary": test.value}, &current)
			if err != nil || !failures.Empty() || calls != test.queries {
				t.Fatal("null/empty candidate semantics", err, calls)
			}
		})
	}
}

func TestUniqueCandidateRejectsMalformedSubsetBeforeAnyIO(t *testing.T) {
	manager, _ := uniqueArticleManager()
	for _, stage := range []string{"fields", "constraints"} {
		for _, test := range []struct {
			name    string
			values  map[string]query.Value
			current *models.Article
		}{
			{"unknown", map[string]query.Value{"unknown": query.String("x")}, nil},
			{"wrong_type", map[string]query.Value{"title": query.Integer(1)}, nil},
			{"invalid_zero_value", map[string]query.Value{"title": {}}, nil},
			{"new_primary", map[string]query.Value{"id": query.Integer(1)}, nil},
			{"missing_primary", map[string]query.Value{"title": query.String("x")}, &models.Article{}},
			{"different_primary", map[string]query.Value{"id": query.Integer(2)}, articlePointer(models.NewArticleWithID(1))},
		} {
			t.Run(stage+"/"+test.name, func(t *testing.T) {
				calls := 0
				backend := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) { calls++; return &uniqueRows{}, nil })
				var failures validation.Errors
				var err error
				if stage == "fields" {
					failures, err = manager.ValidateUniqueFields(t.Context(), backend, test.values, test.current)
				} else {
					failures, err = manager.ValidateUniqueConstraints(t.Context(), backend, test.values, test.current)
				}
				if err == nil || !failures.Empty() || calls != 0 {
					t.Fatal("invalid candidate reached I/O", err)
				}
			})
		}
	}
}

func articlePointer(value models.Article) *models.Article { return &value }

func TestUniqueCandidateOwnsInputBeforeCallbacksAndNeverCachesReads(t *testing.T) {
	manager, descriptor := uniqueArticleManager()
	descriptor.metadata.DBTable = "changed"
	values := map[string]query.Value{"title": query.String("original"), "published": query.Boolean(false)}
	calls := 0
	backend := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
		calls++
		if plan.Table() == "changed" {
			t.Fatal("manager borrowed metadata")
		}
		if calls == 1 {
			values["published"] = query.Boolean(true)
			delete(values, "title")
		}
		if calls == 2 && !plan.Conditions()[0].Value().Equal(query.Boolean(false)) {
			t.Fatal("query retained mutable candidate")
		}
		return &uniqueRows{exists: calls == 1}, nil
	})
	first, err := manager.ValidateUniqueFields(t.Context(), backend, values, nil)
	if err != nil || first.Len() != 1 || calls != 2 {
		t.Fatal(first.All(), err, calls)
	}
	second, err := manager.ValidateUniqueFields(t.Context(), backend, values, nil)
	if err != nil || !second.Empty() || calls != 3 {
		t.Fatal("candidate results were cached", err)
	}
}

func TestUniqueCandidateFailureDiscardsPartialDiagnosticsAndClosesRows(t *testing.T) {
	for _, stage := range []string{"fields", "constraints"} {
		for _, mode := range []string{"query", "close", "cancel"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				manager, _ := uniqueArticleManager()
				if stage == "constraints" {
					manager, _ = namedArticleManager()
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				failure := errors.New("second read failed")
				calls := 0
				first, second := &uniqueRows{exists: true}, &uniqueRows{exists: true}
				backend := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) {
					calls++
					if calls == 1 {
						return first, nil
					}
					if mode == "query" {
						return second, failure
					}
					if mode == "close" {
						second.closeErr = failure
					}
					if mode == "cancel" {
						second.onNext = cancel
					}
					return second, nil
				})
				values := map[string]query.Value{"title": query.String("same"), "published": query.Boolean(false), "summary": query.String("same")}
				var failures validation.Errors
				var err error
				if stage == "fields" {
					failures, err = manager.ValidateUniqueFields(ctx, backend, values, nil)
				} else {
					failures, err = manager.ValidateUniqueConstraints(ctx, backend, values, nil)
				}
				if err == nil || !failures.Empty() || calls != 2 || first.closeCount != 1 || second.closeCount != 1 {
					t.Fatal("partial result or leaked rows", err, calls)
				}
				if mode == "cancel" {
					failure = context.Canceled
				}
				if !errors.Is(err, failure) {
					t.Fatal("read failure cause lost", err)
				}
			})
		}
	}
}

func TestUniqueCandidateValidatesExecutionScopeEvenForEmptySubset(t *testing.T) {
	manager, _ := uniqueArticleManager()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	var nilBackend uniqueQuery
	backend := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) {
		t.Fatal("invalid scope issued a read")
		return nil, nil
	})
	for _, method := range []func(context.Context, db.Queryer, map[string]query.Value, *models.Article) (validation.Errors, error){manager.ValidateUniqueFields, manager.ValidateUniqueConstraints} {
		for _, pair := range []struct {
			ctx     context.Context
			backend db.Queryer
		}{{nil, backend}, {canceled, backend}, {t.Context(), nilBackend}} {
			failures, err := method(pair.ctx, pair.backend, nil, nil)
			if err == nil || !failures.Empty() {
				t.Fatal("empty subset bypassed execution configuration")
			}
		}
	}
}

func TestUniqueCandidateNullReadDoesNotRelaxWriteValidation(t *testing.T) {
	manager, _ := uniqueArticleManager()
	backend := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) {
		t.Fatal("SQL NULL issued a uniqueness read")
		return nil, nil
	})
	failures, err := manager.ValidateUniqueFields(t.Context(), backend, map[string]query.Value{"title": query.Null()}, nil)
	if err != nil || !failures.Empty() {
		t.Fatal("model candidate null was treated as a write", err)
	}
	metadata := (models.ArticleDescriptor{}).Metadata()
	input := injectedArticleCreate{mutation: orm.NewCreateMutation(models.Article{}, metadata.DBTable, []query.Assignment{query.NewAssignment(query.NewFieldRef("title", "title", query.FieldString, false), query.Null())})}
	failures, err = manager.ValidateUniqueCreate(t.Context(), backend, input)
	if err == nil || !failures.Empty() {
		t.Fatal("advisory candidate support weakened generated write validation")
	}
}
