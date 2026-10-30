package orm_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	helpdesk "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type preparedDefaultsDescriptor struct{ models.ArticleDescriptor }

func (preparedDefaultsDescriptor) Metadata() ir.Model {
	metadata := (models.ArticleDescriptor{}).Metadata()
	metadata.Fields[2].Default.Boolean = true
	metadata.Fields[3].Default = &ir.Scalar{Kind: ir.ScalarString, String: "excluded-default"}
	return metadata
}

func TestPreparedModelAppliesExcludedDefaultsAndOwnsSnapshots(t *testing.T) {
	manager := orm.NewManager[models.Article](preparedDefaultsDescriptor{})
	input := map[string]query.Value{"title": query.String("prepared")}
	prepared, err := manager.PrepareValues(nil, input)
	if err != nil {
		t.Fatal(err)
	}
	input["title"] = query.String("caller-change")
	value, err := prepared.Model()
	if err != nil || value.Title != "prepared" || !value.Published || value.Summary == nil || *value.Summary != "excluded-default" {
		t.Fatal("new model did not apply excluded defaults", err)
	}
	*value.Summary = "result-change"
	copy, err := prepared.Model()
	if err != nil || *copy.Summary != "excluded-default" {
		t.Fatal("prepared result shares pointers", err)
	}
	current := models.NewArticleWithID(0)
	current.Title, current.Summary = "current", new("stored")
	prepared, err = manager.PrepareValues(&current, map[string]query.Value{"title": query.String("update")})
	if err != nil {
		t.Fatal(err)
	}
	*current.Summary = "caller-later"
	copy, err = prepared.Model()
	if err != nil || copy.Published || *copy.Summary != "stored" || copy.Title != "update" || !slices.Equal(prepared.ChangedFields(), []string{"title"}) {
		t.Fatal("current values were overwritten by model defaults", err)
	}
	changed := prepared.ChangedFields()
	changed[0] = "forged"
	if !slices.Equal(prepared.ChangedFields(), []string{"title"}) {
		t.Fatal("changed field getter aliases state")
	}
	for _, input := range []map[string]query.Value{
		{"id": query.Integer(1)}, {"unknown": query.String("private")}, {"title": query.Null()}, {"published": query.Integer(1)},
	} {
		if result, err := manager.PrepareValues(&current, input); err == nil || !reflect.DeepEqual(result, orm.PreparedModel[models.Article]{}) {
			t.Fatal("invalid input published a candidate", err)
		}
	}
}

func TestPreparedModelRejectsMissingRequiredValuesBeforeGoZeroCanHideThem(t *testing.T) {
	// category has no default. The Go zero CategoryID is not an instruction to
	// create a relationship with key zero when the client omitted the field.
	if prepared, err := helpdesk.TicketObjects.PrepareValues(nil, map[string]query.Value{"subject": query.String("missing category")}); err == nil || !reflect.DeepEqual(prepared, orm.PreparedModel[helpdesk.Ticket]{}) {
		t.Fatal("required missing relation became a Go zero", err)
	}
	prepared, err := helpdesk.TicketObjects.PrepareValues(nil, map[string]query.Value{"subject": query.String("explicit zero"), "category": query.Integer(0)})
	if err != nil {
		t.Fatal(err)
	}
	value, err := prepared.Model()
	if err != nil || value.CategoryID != 0 || value.Closed || value.Priority != nil || value.ExternalPayload != nil {
		t.Fatal("explicit zero or excluded null/default changed", err)
	}
	// Unrepresentable defaults are startup/preparation errors, even when the
	// particular incoming field is excluded from the request allowlist.
	metadata := (models.ArticleDescriptor{}).Metadata()
	metadata.Fields[2].Default = &ir.Scalar{Kind: ir.ScalarString, String: "private"}
	if _, err := orm.InitialModelValues(metadata); err == nil {
		t.Fatal("invalid excluded default accepted")
	}
}

func TestPreparedModelSaveUsesChangedColumnsWithoutResurrectingMissingRows(t *testing.T) {
	current := models.NewArticleWithID(7)
	current.Title, current.Published, current.Summary = "stored", true, new("preserved")
	prepared, err := models.ArticleObjects.PrepareValues(&current, map[string]query.Value{"title": query.String("changed"), "published": query.Boolean(true)})
	if err != nil {
		t.Fatal(err)
	}
	value, err := prepared.Model()
	if err != nil {
		t.Fatal(err)
	}
	backend := &saveBackendSpy{updateRows: []int64{1, 0}}
	if err := prepared.Save(t.Context(), backend, &value); err != nil {
		t.Fatal(err)
	}
	assertAssignmentNames(t, backend.updatePlans[0].Assignments(), "title")
	if err := prepared.Save(t.Context(), backend, &value); !errors.Is(err, &query.Error{Code: query.CodeUpdateFieldsMissingRow}) || !slices.Equal(backend.calls, []string{"update", "update"}) {
		t.Fatal("missing row was resurrected or failure was discarded", err, backend.calls)
	}
	serverEdit, _ := prepared.Model()
	serverEdit.Summary = new("server")
	backend = &saveBackendSpy{}
	if err := prepared.Save(t.Context(), backend, &serverEdit); err != nil {
		t.Fatal(err)
	}
	assertAssignmentNames(t, backend.updatePlans[0].Assignments(), "title", "summary")
	changedKey := models.NewArticleWithID(9)
	changedKey.Title = "wrong owner"
	backend = &saveBackendSpy{}
	if err := prepared.Save(t.Context(), backend, &changedKey); err == nil || len(backend.calls) != 0 {
		t.Fatal("changed primary key reached storage", err)
	}
	unchanged, err := models.ArticleObjects.PrepareValues(&current, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, _ = unchanged.Model()
	if err := unchanged.Save(t.Context(), backend, &value); err != nil || len(backend.calls) != 0 {
		t.Fatal("unchanged model wrote storage", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := unchanged.Save(ctx, backend, &value); !errors.Is(err, context.Canceled) || len(backend.calls) != 0 {
		t.Fatal("no-op hid cancellation", err)
	}
}

func TestPreparedModelSaveRetainsGeneratedIdentityAndTerminalErrors(t *testing.T) {
	prepared, err := models.ArticleObjects.PrepareValues(nil, map[string]query.Value{"title": query.String("new")})
	if err != nil {
		t.Fatal(err)
	}
	value, err := prepared.Model()
	if err != nil {
		t.Fatal(err)
	}
	backend := &saveBackendSpy{insertIDs: []int64{42}}
	if err := prepared.Save(t.Context(), backend, &value); err != nil || value.ID != 42 {
		t.Fatal("new model was not inserted", err)
	}
	if err := prepared.Save(t.Context(), backend, &value); err != nil || !slices.Equal(backend.calls, []string{"insert", "update"}) {
		t.Fatal("deferred pointer lost generated identity", err)
	}
	unknown := &query.Error{Code: query.CodeCommitOutcomeUnknown}
	backend.updateErrors = []error{nil, unknown}
	if err := prepared.Save(t.Context(), backend, &value); !errors.Is(err, unknown) || !slices.Equal(backend.calls, []string{"insert", "update", "update"}) {
		t.Fatal("terminal error was hidden or retried", err)
	}
	if err := (orm.PreparedModel[models.Article]{}).Save(t.Context(), backend, &value); err == nil {
		t.Fatal("zero prepared model wrote storage")
	}
}

type stagedUniqueDescriptor struct{ models.ArticleDescriptor }

func (stagedUniqueDescriptor) Metadata() ir.Model {
	model := (models.ArticleDescriptor{}).Metadata()
	for i := range model.Fields {
		model.Fields[i].Unique = model.Fields[i].Name == "title" || model.Fields[i].Name == "summary"
	}
	model.UniqueConstraints = []ir.UniqueConstraint{
		{Name: "summary_published", Fields: []string{"summary", "published"}},
		{Name: "title_published", Fields: []string{"title", "published"}},
	}
	return model
}

func TestPreparedUniqueExclusionDefersWholeConstraintsUntilServerValueExists(t *testing.T) {
	manager := orm.NewManager[models.Article](stagedUniqueDescriptor{})
	prepared, err := manager.PrepareValues(nil, map[string]query.Value{"title": query.String("candidate"), "summary": query.String("unavailable-server-value")})
	if err != nil {
		t.Fatal(err)
	}
	row, err := prepared.Model()
	if err != nil {
		t.Fatal(err)
	}
	var plans []query.Plan
	reader := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
		plans = append(plans, plan)
		return &uniqueRows{exists: true}, nil
	})
	excluded := []string{"summary"}
	diagnostics, err := prepared.ValidateUniqueExcluding(t.Context(), reader, row, excluded...)
	if err != nil || len(plans) != 2 || diagnostics.Len() != 2 {
		t.Fatal("excluded scalar or tuple was queried", err, len(plans), diagnostics.All())
	}
	for _, plan := range plans {
		for _, condition := range plan.Conditions() {
			if condition.Field().Name() == "summary" {
				t.Fatal("server placeholder leaked into uniqueness query")
			}
		}
	}
	if excluded[0] != "summary" {
		t.Fatal("caller exclusions mutated")
	}
	for _, names := range [][]string{{"missing"}, {"summary", "summary"}, {""}} {
		plans = nil
		diagnostics, err := prepared.ValidateUniqueExcluding(t.Context(), reader, row, names...)
		if err == nil || !diagnostics.Empty() || len(plans) != 0 {
			t.Fatal("invalid exclusions performed I/O")
		}
	}
	plans = nil
	row.Summary = new("actual-server-value")
	diagnostics, err = prepared.ValidateUnique(t.Context(), reader, row)
	if err != nil || len(plans) != 4 || diagnostics.Len() != 4 {
		t.Fatal("final full check omitted deferred constraints", err, len(plans))
	}
	seen := 0
	for _, plan := range plans {
		for _, condition := range plan.Conditions() {
			if condition.Field().Name() == "summary" {
				text, valid := condition.Value().String()
				if !valid || text != "actual-server-value" {
					t.Fatal("final check reused placeholder")
				}
				seen++
			}
		}
	}
	if seen != 2 {
		t.Fatal("final field and tuple not checked")
	}
}

func TestPreparedUniqueAfterDeletionPreservesSelfAndSignedExclusions(t *testing.T) {
	manager, _ := uniqueArticleManager()
	current := models.NewArticleWithID(11)
	current.Title = "current"
	prepared, err := manager.PrepareValues(&current, map[string]query.Value{"title": query.String("replacement"), "published": query.Boolean(true)})
	if err != nil {
		t.Fatal(err)
	}
	row, err := prepared.Model()
	if err != nil {
		t.Fatal(err)
	}
	deleted := []int64{7, 0, -3, 0}
	var plans []query.Plan
	reader := uniqueQuery(func(_ context.Context, plan query.Plan) (db.Rows, error) {
		plans = append(plans, plan)
		return &uniqueRows{}, nil
	})
	diagnostics, err := prepared.ValidateUniqueAfterDeletion(t.Context(), reader, row, deleted)
	if err != nil || !diagnostics.Empty() || len(plans) != 2 || !slices.Equal(deleted, []int64{7, 0, -3, 0}) {
		t.Fatal("deletion-aware unique validation mutated caller or omitted constraints", err, len(plans))
	}
	for _, plan := range plans {
		where, present := plan.Where()
		if !present {
			t.Fatal("uniqueness predicate missing")
		}
		self, removed := false, false
		var inspect func(query.Expression, bool)
		inspect = func(expression query.Expression, negated bool) {
			if expression.Kind() == query.ExpressionNot {
				negated = !negated
			}
			if condition, leaf := expression.Condition(); leaf && condition.Field().Name() == "id" {
				if !negated {
					t.Fatal("excluded identity became a positive match")
				}
				switch condition.Lookup() {
				case query.LookupExact:
					self = condition.Value().Equal(query.Integer(11))
				case query.LookupIn:
					values, present := condition.Values()
					removed = present && slices.EqualFunc(values, []query.Value{query.Integer(-3), query.Integer(0), query.Integer(7)}, query.Value.Equal)
				}
			}
			for _, child := range expression.Children() {
				inspect(child, negated)
			}
		}
		inspect(where, false)
		if !self || !removed {
			t.Fatal("self exclusion or exact signed deletion keys lost")
		}
	}
}
