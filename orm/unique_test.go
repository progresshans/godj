package orm_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

type uniqueQuery func(context.Context, query.Plan) (db.Rows, error)

func (run uniqueQuery) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return run(ctx, plan)
}

type uniqueRows struct {
	exists, advanced bool
	err, closeErr    error
	closeCount       int
	onNext           func()
}

func (rows *uniqueRows) Next() bool {
	if rows.onNext != nil {
		rows.onNext()
	}
	if rows.advanced {
		return false
	}
	rows.advanced = true
	return rows.exists
}
func (*uniqueRows) Scan(...any) error {
	return errors.New("advisory validation must not decode a model")
}
func (rows *uniqueRows) Err() error   { return rows.err }
func (rows *uniqueRows) Close() error { rows.closeCount++; return rows.closeErr }

func uniqueArticleManager() (orm.Manager[models.Article], *mutableManagerDescriptor) {
	metadata := (models.ArticleDescriptor{}).Metadata()
	for i := range metadata.Fields {
		metadata.Fields[i].Unique = !metadata.Fields[i].PrimaryKey
	}
	descriptor := &mutableManagerDescriptor{metadata: metadata}
	return orm.NewManager[models.Article](descriptor), descriptor
}

func TestValidateUniqueCreateUsesDefaultsDeclarationOrderAndOwnedMetadata(t *testing.T) {
	manager, descriptor := uniqueArticleManager()
	metadata := descriptor.metadata.Clone()
	input := models.NewArticleCreate("private-title").WithSummary("")
	// The manager must retain its original metadata even when callbacks change
	// later descriptor results or callers mutate plan getter slices.
	descriptor.metadata.Fields[1].Unique = false
	descriptor.metadata.DBTable = "other_table"
	var plans []query.Plan
	var acquired []*uniqueRows
	backend := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
		plans = append(plans, plan)
		fields := plan.SourceFields()
		fields[0] = query.FieldRef{}
		rows := &uniqueRows{exists: true}
		acquired = append(acquired, rows)
		return rows, nil
	})
	violations, err := manager.ValidateUniqueCreate(t.Context(), backend, input)
	if err != nil || violations.Len() != 3 || len(plans) != 3 {
		t.Fatal("missing declared unique fields", violations.All(), err)
	}
	for index, plan := range plans {
		field := metadata.Fields[index+1]
		condition := plan.Conditions()
		if plan.Table() != metadata.DBTable || len(condition) != 1 || condition[0].Field().Name() != field.Name || condition[0].Lookup() != query.LookupExact {
			t.Fatal("validation used changed metadata or reordered fields", plan)
		}
		if limit, limited := plan.Limit(); !limited || limit != 1 || plan.ResultShape().Kind() != query.ResultProjection {
			t.Fatal("uniqueness read was not bounded")
		}
		expressions := plan.ResultShape().Expressions()
		selected, ok := expressions[0].Field()
		if len(expressions) != 1 || !ok || selected.Name() != "id" || acquired[index].closeCount != 1 {
			t.Fatal("validation materialized full rows or leaked its cursor")
		}
		violation := violations.All()[index]
		if violation.Field() != validation.Field(field.Name) || violation.Code() != validation.CodeUnique || len(violation.Params()) != 0 {
			t.Fatal("unstable or value-bearing duplicate diagnostic", violation)
		}
	}
	if boolean, ok := plans[1].Conditions()[0].Value().Boolean(); !ok || boolean {
		t.Fatal("default false was not validated")
	}
	if text, ok := plans[2].Conditions()[0].Value().String(); !ok || text != "" {
		t.Fatal("empty string was treated as SQL NULL")
	}
	if descriptor.calls.Load() != 1 {
		t.Fatal("validation re-read mutable metadata")
	}
}

func TestValidateUniqueUpdateExcludesPresentZeroKeyAndIgnoresOmittedNullFields(t *testing.T) {
	manager, _ := uniqueArticleManager()
	current := models.NewArticleWithID(0)
	current.Title = "self"
	var plans []query.Plan
	backend := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
		plans = append(plans, plan)
		return &uniqueRows{}, nil
	})
	for _, patch := range []models.ArticlePatch{models.ArticlePatch{}.WithSummaryNull(), models.ArticlePatch{}.WithTitle("self")} {
		violations, err := manager.ValidateUniqueUpdate(t.Context(), backend, current, patch)
		if err != nil || !violations.Empty() {
			t.Fatal(violations.All(), err)
		}
	}
	if len(plans) != 1 {
		t.Fatal("omitted or SQL NULL values issued a uniqueness query", len(plans))
	}
	where, present := plans[0].Where()
	children := where.Children()
	if !present || where.Kind() != query.ExpressionAnd || len(children) != 2 || children[0].Kind() != query.ExpressionNot {
		t.Fatal("update lacks exact self exclusion", where)
	}
	self, _ := children[0].Children()[0].Condition()
	key, ok := self.Value().Integer()
	if self.Field().Name() != "id" || self.Lookup() != query.LookupExact || !ok || key != 0 {
		t.Fatal("presence-aware zero key was not excluded")
	}
	if _, err := manager.ValidateUniqueUpdate(t.Context(), backend, models.Article{ID: 0, Title: "self"}, models.ArticlePatch{}.WithTitle("self")); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeMissingPrimaryKey}) || len(plans) != 1 {
		t.Fatal("unset key was treated as an existing row", err)
	}
}

func TestValidateUniqueRejectsMalformedInputsWithoutIOOrCallerMutation(t *testing.T) {
	manager, _ := uniqueArticleManager()
	metadata := (models.ArticleDescriptor{}).Metadata()
	summary := "unchanged"
	current := models.NewArticleWithID(7)
	current.Title, current.Summary = "before", &summary
	forged := current
	forged.ID, forged.Title = 8, "after"
	forgedPatch := injectedArticlePatch{mutation: orm.NewPatchMutation(forged, metadata.DBTable, []query.Assignment{orm.NewAssignment(metadata.Fields[1], query.String("after"))})}
	calls := 0
	backend := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) { calls++; return &uniqueRows{}, nil })
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	var nilBackend uniqueQuery
	var nilInput *injectedArticleCreate
	tests := []struct {
		name string
		run  func() (validation.Errors, error)
	}{
		{"nil_context", func() (validation.Errors, error) {
			return manager.ValidateUniqueCreate(nil, backend, models.NewArticleCreate("value"))
		}},
		{"canceled", func() (validation.Errors, error) {
			return manager.ValidateUniqueCreate(canceled, backend, models.NewArticleCreate("value"))
		}},
		{"nil_backend", func() (validation.Errors, error) {
			return manager.ValidateUniqueCreate(t.Context(), nilBackend, models.NewArticleCreate("value"))
		}},
		{"nil_input", func() (validation.Errors, error) { return manager.ValidateUniqueCreate(t.Context(), backend, nilInput) }},
		{"required", func() (validation.Errors, error) {
			return manager.ValidateUniqueCreate(t.Context(), backend, models.ArticleCreate{})
		}},
		{"empty_patch", func() (validation.Errors, error) {
			return manager.ValidateUniqueUpdate(t.Context(), backend, current, models.ArticlePatch{})
		}},
		{"forged_key", func() (validation.Errors, error) {
			return manager.ValidateUniqueUpdate(t.Context(), backend, current, forgedPatch)
		}},
		{"omitted_alias", func() (validation.Errors, error) {
			return manager.ValidateUniqueUpdate(t.Context(), backend, current, aliasingArticlePatch{})
		}},
		{"foreign_assignment", func() (validation.Errors, error) {
			return manager.ValidateUniqueCreate(t.Context(), backend, injectedArticleCreate{mutation: orm.NewCreateMutation(models.Article{Title: "value"}, metadata.DBTable, []query.Assignment{query.NewAssignment(query.NewFieldRef("title", "other_column", query.FieldString, false), query.String("value"))})})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			violations, err := test.run()
			if err == nil || !violations.Empty() || calls != 0 || summary != "unchanged" {
				t.Fatal("malformed validation mutated input, read storage or published violations", violations.All(), err)
			}
		})
	}
}

func TestValidateUniqueDiscardsPartialViolationsAndPreservesRowFailures(t *testing.T) {
	queryErr, rowErr, closeErr := errors.New("query failed"), errors.New("iteration failed"), errors.New("close failed")
	for _, mode := range []string{"query", "query_with_rows", "nil_rows", "typed_nil_rows", "iteration", "close", "cancel_next", "cancel_query"} {
		t.Run(mode, func(t *testing.T) {
			manager, _ := uniqueArticleManager()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			first, second := &uniqueRows{exists: true}, &uniqueRows{exists: true}
			backend := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) {
				calls++
				if calls == 1 {
					return first, nil
				}
				switch mode {
				case "query":
					return nil, queryErr
				case "query_with_rows":
					second.closeErr = closeErr
					return second, queryErr
				case "nil_rows":
					return nil, nil
				case "typed_nil_rows":
					var rows *uniqueRows
					return rows, nil
				case "iteration":
					second.err = rowErr
				case "close":
					second.closeErr = closeErr
				case "cancel_next":
					second.onNext = cancel
				case "cancel_query":
					cancel()
				}
				return second, nil
			})
			violations, err := manager.ValidateUniqueCreate(ctx, backend, models.NewArticleCreate("one").WithSummary("three"))
			if err == nil || !violations.Empty() || calls != 2 || first.closeCount != 1 {
				t.Fatal("failure lost resource ownership or published partial violations", violations.All(), err, calls)
			}
			wantClose := 1
			if mode == "query" || mode == "nil_rows" || mode == "typed_nil_rows" {
				wantClose = 0
			}
			if second.closeCount != wantClose {
				t.Fatal("error cursor close count", second.closeCount)
			}
			for _, expected := range []struct {
				needed bool
				cause  error
			}{{mode == "query" || mode == "query_with_rows", queryErr}, {mode == "iteration", rowErr}, {mode == "close" || mode == "query_with_rows", closeErr}, {mode == "cancel_next" || mode == "cancel_query", context.Canceled}} {
				if expected.needed && !errors.Is(err, expected.cause) {
					t.Fatal("lost error cause", err)
				}
			}
		})
	}
}

func TestValidateUniqueSharesImmutableManagerButNeverCachesResults(t *testing.T) {
	manager, _ := uniqueArticleManager()
	const workers = 8
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			calls := 0
			backend := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) {
				calls++
				return &uniqueRows{exists: calls > 2}, nil
			})
			first, err := manager.ValidateUniqueCreate(t.Context(), backend, models.NewArticleCreate("one"))
			if err != nil || !first.Empty() {
				t.Error("first validation", first.All(), err)
				return
			}
			second, err := manager.ValidateUniqueCreate(t.Context(), backend, models.NewArticleCreate("one"))
			if err != nil || second.Len() != 2 || calls != 4 {
				t.Error("validation reused stale results", second.All(), err, calls)
			}
		})
	}
	group.Wait()
}

func TestValidateUniqueBuildsEveryPlanBeforeQueries(t *testing.T) {
	manager, descriptor := uniqueArticleManager()
	metadata := descriptor.metadata
	// A structurally invalid later reference is accepted by neither AST nor
	// backend. It must prevent the earlier valid field query too.
	metadata.Fields[3].Column = ""
	manager = orm.NewManager[models.Article](&mutableManagerDescriptor{metadata: metadata})
	input := models.NewArticleCreate("one").WithSummary("last").BuildCreate()
	assignments := input.Assignments()
	for i, assignment := range assignments {
		if assignment.Field().Name() == "summary" {
			assignments[i] = orm.NewAssignment(metadata.Fields[3], query.String("last"))
		}
	}
	value := models.Article{Title: "one", Summary: ptrUniqueString("last")}
	bad := injectedArticleCreate{mutation: orm.NewCreateMutation(value, metadata.DBTable, assignments)}
	queries := 0
	backend := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) { queries++; return &uniqueRows{}, nil })
	violations, err := manager.ValidateUniqueCreate(t.Context(), backend, bad)
	if err == nil || !violations.Empty() || queries != 0 {
		t.Fatal("partial AST validation reached backend", err)
	}
	if !reflect.DeepEqual(value, models.Article{Title: "one", Summary: ptrUniqueString("last")}) {
		t.Fatal("validation changed its input")
	}
}

func ptrUniqueString(value string) *string { return &value }

type uniqueRelationDescriptor struct{ relationWriteDescriptor }

func (uniqueRelationDescriptor) Metadata() ir.Model {
	model := relationWriteDescriptor{}.Metadata()
	model.Fields[1].Unique, model.Fields[2].Unique = true, true
	return model
}

func TestValidateUniqueForeignKeyUsesStoredKeyWithoutRelationshipQueries(t *testing.T) {
	descriptor := uniqueRelationDescriptor{}
	metadata := descriptor.Metadata()
	manager := orm.NewManager[relationWriteModel](descriptor)
	input := injectedRelationWriteCreate{mutation: orm.NewCreateMutation(relationWriteModel{AuthorID: 9}, metadata.DBTable, []query.Assignment{orm.NewAssignment(metadata.Fields[1], query.Integer(9)), orm.NewAssignment(metadata.Fields[2], query.Null())})}
	calls := 0
	backend := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
		calls++
		conditions := plan.Conditions()
		if plan.Table() != metadata.DBTable || len(conditions) != 1 || conditions[0].Field().Column() != "author_id" || conditions[0].Value() != query.Integer(9) || len(plan.RelationProjections()) != 0 {
			t.Fatal("FK validation did not use its stored key", plan)
		}
		return &uniqueRows{exists: true}, nil
	})
	violations, err := manager.ValidateUniqueCreate(t.Context(), backend, input)
	if err != nil || calls != 1 || violations.Len() != 1 || violations.All()[0].Field() != "author" {
		t.Fatal("unique FK validation", violations.All(), err)
	}
}
