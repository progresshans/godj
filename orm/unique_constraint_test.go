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
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func namedArticleManager() (orm.Manager[models.Article], *mutableManagerDescriptor) {
	metadata := (models.ArticleDescriptor{}).Metadata()
	// Deliberately noncanonical input order; member order remains significant.
	metadata.UniqueConstraints = []ir.UniqueConstraint{
		{Name: "z_summary", Fields: []string{"title", "summary"}},
		{Name: "b_published", Fields: []string{"published", "title"}},
		{Name: "c_identity", Fields: []string{"id", "title"}},
		{Name: "a_title", Fields: []string{"title"}},
	}
	descriptor := &mutableManagerDescriptor{metadata: metadata}
	return orm.NewManager[models.Article](descriptor), descriptor
}

func TestValidateNamedUniqueCreateOwnsCanonicalConstraintsAndResolvedDefaults(t *testing.T) {
	_, descriptor := namedArticleManager()
	descriptor.metadata.Fields[1].Unique = true
	manager := orm.NewManager[models.Article](descriptor)
	descriptor.metadata.UniqueConstraints[0].Fields[0] = "summary"
	descriptor.metadata.UniqueConstraints[1].Name = "changed"
	var plans []query.Plan
	backend := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
		plans = append(plans, plan)
		return &uniqueRows{exists: true}, nil
	})
	violations, err := manager.ValidateUniqueCreate(t.Context(), backend, models.NewArticleCreate("private").WithSummary(""))
	if err != nil || violations.Len() != 4 || len(plans) != 4 {
		t.Fatal("field/named owners were lost or generated PK was queried", violations.All(), err, len(plans))
	}
	wantNames := [][]string{{"title"}, {"title"}, {"published", "title"}, {"title", "summary"}}
	for index, plan := range plans {
		var names []string
		for _, condition := range plan.Conditions() {
			names = append(names, condition.Field().Name())
			if condition.Lookup() != query.LookupExact {
				t.Fatal("non-exact uniqueness query")
			}
		}
		if !reflect.DeepEqual(names, wantNames[index]) {
			t.Fatal("constraint ordering or snapshot changed", names)
		}
		violation := violations.All()[index]
		field, code := validation.Field("title"), validation.CodeUnique
		if index >= 2 {
			field, code = validation.NonField, validation.CodeUniqueTogether
		}
		if violation.Field() != field || violation.Code() != code || len(violation.Params()) != 0 {
			t.Fatal("unexpected or value-bearing diagnostic", violation)
		}
	}
	if value, ok := plans[2].Conditions()[0].Value().Boolean(); !ok || value {
		t.Fatal("default false was omitted")
	}
	if value, ok := plans[3].Conditions()[1].Value().String(); !ok || value != "" {
		t.Fatal("empty string was treated as NULL")
	}
}

func TestValidateNamedUniqueUpdateUsesWholeCandidateAndPresentZeroSelf(t *testing.T) {
	_, descriptor := namedArticleManager()
	// Only tuples: no single-field check can conceal an omitted member bug.
	descriptor.metadata.UniqueConstraints = descriptor.metadata.UniqueConstraints[:3]
	manager := orm.NewManager[models.Article](descriptor)
	current := models.NewArticleWithID(0)
	current.Title, current.Published, current.Summary = "before", true, ptrUniqueString("retained")
	for _, tc := range []struct {
		name  string
		patch models.ArticlePatch
		want  [][]query.Value
	}{
		{"title", models.ArticlePatch{}.WithTitle("after"), [][]query.Value{{query.Boolean(true), query.String("after")}, {query.Integer(0), query.String("after")}, {query.String("after"), query.String("retained")}}},
		{"published", models.ArticlePatch{}.WithPublished(false), [][]query.Value{{query.Boolean(false), query.String("before")}}},
		{"summary", models.ArticlePatch{}.WithSummary(""), [][]query.Value{{query.String("before"), query.String("")}}},
		{"null", models.ArticlePatch{}.WithSummaryNull(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got [][]query.Value
			backend := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
				if len(got) >= len(tc.want) {
					t.Fatal("untouched or NULL constraint issued an extra query")
				}
				where, ok := plan.Where()
				children := where.Children()
				if !ok || where.Kind() != query.ExpressionAnd || len(children) != len(tc.want[len(got)])+1 || children[0].Kind() != query.ExpressionNot {
					t.Fatal("tuple query lacks conjunctive self exclusion", where)
				}
				self, ok := children[0].Children()[0].Condition()
				if !ok || self.Field().Name() != "id" || !self.Value().Equal(query.Integer(0)) {
					t.Fatal("present zero key was not excluded")
				}
				var values []query.Value
				for _, child := range children[1:] {
					condition, ok := child.Condition()
					if !ok {
						t.Fatal("constraint member was not an AND condition")
					}
					values = append(values, condition.Value())
				}
				got = append(got, values)
				return &uniqueRows{}, nil
			})
			violations, err := manager.ValidateUniqueUpdate(t.Context(), backend, current, tc.patch)
			if err != nil || !violations.Empty() || !reflect.DeepEqual(got, tc.want) {
				t.Fatal("partial patch did not use the complete candidate", got, tc.want, violations.All(), err)
			}
			if current.Title != "before" || !current.Published || *current.Summary != "retained" || current.ID != 0 {
				t.Fatal("advisory validation mutated current")
			}
		})
	}
	// Summary is now outside every constraint, so changing it performs no query.
	descriptor.metadata.UniqueConstraints = descriptor.metadata.UniqueConstraints[1:]
	manager = orm.NewManager[models.Article](descriptor)
	noQuery := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) {
		t.Fatal("untouched constraint queried")
		return nil, nil
	})
	if violations, err := manager.ValidateUniqueUpdate(t.Context(), noQuery, current, models.ArticlePatch{}.WithSummary("unrelated")); err != nil || !violations.Empty() {
		t.Fatal(violations.All(), err)
	}
}

func TestValidateNamedUniqueRejectsMalformedLaterConstraintBeforeIO(t *testing.T) {
	for _, constraints := range [][]ir.UniqueConstraint{
		{{Name: "empty"}},
		{{Name: "missing", Fields: []string{"title", "absent"}}},
		{{Name: "repeated", Fields: []string{"title", "title"}}},
		{{Name: "same", Fields: []string{"title"}}, {Name: "same", Fields: []string{"published"}}},
		{{Name: "invalid name", Fields: []string{"title"}}},
	} {
		_, descriptor := uniqueArticleManager()
		descriptor.metadata.UniqueConstraints = constraints
		manager := orm.NewManager[models.Article](descriptor)
		calls := 0
		backend := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) { calls++; return &uniqueRows{exists: true}, nil })
		violations, err := manager.ValidateUniqueCreate(t.Context(), backend, models.NewArticleCreate("private"))
		if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || !violations.Empty() || calls != 0 {
			t.Fatal("malformed constraint reached storage or emitted partial violations", constraints, violations.All(), err, calls)
		}
	}
}

type invalidCandidateDescriptor struct{ *mutableManagerDescriptor }

func (d invalidCandidateDescriptor) WriteFieldValue(value models.Article, field ir.Field) (query.Value, bool) {
	if field.Name == "published" {
		return query.String("not a boolean"), true
	}
	return d.mutableManagerDescriptor.WriteFieldValue(value, field)
}

func TestValidateNamedUniqueRejectsInvalidOmittedMemberBeforeAnyQuery(t *testing.T) {
	_, descriptor := namedArticleManager()
	descriptor.metadata.Fields[1].Unique = true
	manager := orm.NewManager[models.Article](invalidCandidateDescriptor{descriptor})
	current := models.NewArticleWithID(1)
	current.Title = "before"
	calls := 0
	backend := uniqueQuery(func(context.Context, query.Plan) (db.Rows, error) { calls++; return &uniqueRows{}, nil })
	violations, err := manager.ValidateUniqueUpdate(t.Context(), backend, current, models.ArticlePatch{}.WithTitle("after"))
	if !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue}) || !violations.Empty() || calls != 0 {
		t.Fatal("invalid candidate was shortened or queried", violations.All(), err, calls)
	}
}

type namedRelationDescriptor struct{ relationWriteDescriptor }

func (namedRelationDescriptor) Metadata() ir.Model {
	model := relationWriteDescriptor{}.Metadata()
	model.UniqueConstraints = []ir.UniqueConstraint{{Name: "pair", Fields: []string{"reviewer", "author"}}}
	return model
}

func TestValidateNamedUniqueForeignKeysUseOrderedStorageColumns(t *testing.T) {
	descriptor := namedRelationDescriptor{}
	model := descriptor.Metadata()
	reviewer := int64(10)
	input := injectedRelationWriteCreate{mutation: orm.NewCreateMutation(relationWriteModel{AuthorID: 9, ReviewerID: &reviewer}, model.DBTable, []query.Assignment{orm.NewAssignment(model.Fields[1], query.Integer(9)), orm.NewAssignment(model.Fields[2], query.Integer(10))})}
	calls := 0
	backend := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
		calls++
		conditions := plan.Conditions()
		if len(conditions) != 2 || conditions[0].Field().Column() != "reviewer_id" || conditions[1].Field().Column() != "author_id" || !conditions[0].Value().Equal(query.Integer(10)) || !conditions[1].Value().Equal(query.Integer(9)) || len(plan.RelationProjections()) != 0 {
			t.Fatal("FK tuple used relationships instead of ordered stored keys", conditions)
		}
		return &uniqueRows{exists: true}, nil
	})
	violations, err := orm.NewManager[relationWriteModel](descriptor).ValidateUniqueCreate(t.Context(), backend, input)
	if err != nil || calls != 1 || violations.Len() != 1 || violations.All()[0].Field() != validation.NonField || violations.All()[0].Code() != validation.CodeUniqueTogether || reviewer != 10 {
		t.Fatal("named FK validation", violations.All(), err)
	}
}
