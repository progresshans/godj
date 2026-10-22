package orm_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

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
