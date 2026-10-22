package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

func checkedInlineSubmission[P, C any](t *testing.T, inline ModelInline[P, C], actor auth.Principal, id int64, access InlineAccess, data forms.Data) InlineSubmission {
	t.Helper()
	view := inline.View()
	entry, err := view.bind(t.Context(), actor, id, access, &data)
	if err != nil || !entry.set.Valid() {
		t.Fatal("initial inline submission", err, entry.set.NonFormErrors())
	}
	owner := &inlineToken{}
	registered := registeredModel{inlines: []Inline{view}, inlineOwner: owner}
	submission, err := registered.checkedInlines(t.Context(), actor, id, InlineSubmission{owner: owner, parentID: id, entries: []inlineBound{entry}})
	if err != nil {
		t.Fatal(err)
	}
	return submission
}

func reportChangeInput(summary string) forms.Data {
	return forms.NewData(map[string][]string{
		"reports-TOTAL_FORMS": {"1"}, "reports-INITIAL_FORMS": {"1"},
		"reports-0-id": {"11"}, "reports-0-ticket": {"7"}, "reports-0-summary": {summary},
	})
}

func TestModelInlineRebindUsesDeclaredPolicyAndFreshExcludedValues(t *testing.T) {
	config, actor := reportInlineFixture(t)
	config.Form.Definition.Fields = []string{"summary"}
	config.Form.Definition.Overrides = []formmodel.Override{formmodel.OverrideField("summary", formmodel.WithStringNormalizer(strings.ToUpper))}
	loads, validations := 0, 0
	initial := config.Load
	config.Load = func(ctx context.Context, actor auth.Principal, id int64) (InlineSnapshot[models.ServiceReport], error) {
		loads++
		return initial(ctx, actor, id)
	}
	config.Validate = func(context.Context, auth.Principal, formmodel.InlineSet[models.Ticket, models.ServiceReport]) error {
		validations++
		return nil
	}
	inline, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	access := InlineAccess{View: true, Change: true}
	submission := checkedInlineSubmission(t, inline, actor, 7, access, reportChangeInput("  changed  "))
	config.Form.Definition.Fields[0] = "forged"
	config.Form.Definition.Overrides[0] = formmodel.OverrideField("completed")
	current := models.NewServiceReportWithID(11)
	current.TicketID, current.Summary, current.Completed = 7, "fresh", false
	snapshot := []models.ServiceReport{current}
	calls := 0
	ctx := t.Context()
	bound, present, err := inline.BindCurrent(ctx, actor, submission, func(work context.Context, admitted InlineAccess) (InlineCurrent[models.Ticket, models.ServiceReport], error) {
		calls++
		if work != ctx || admitted != access {
			t.Fatal("writer lost context or admitted rights")
		}
		return InlineCurrent[models.Ticket, models.ServiceReport]{Parent: models.NewTicketWithID(7), Children: InlineSnapshot[models.ServiceReport]{Current: snapshot}}, nil
	})
	if err != nil || !present || !bound.Set().Valid() || bound.Access() != access || calls != 1 || loads != 1 || validations != 1 {
		t.Fatal("current writer used initial loaders or changed policy", err, present, calls, loads, validations)
	}
	snapshot[0].Completed = true
	prepared, err := bound.Set().Prepare()
	if err != nil || len(prepared.Rows()) != 1 {
		t.Fatal("prepare current inline", err)
	}
	value, err := prepared.Rows()[0].Model()
	if err != nil || value.ID != 11 || value.TicketID != 7 || value.Summary != "CHANGED" || value.Completed {
		t.Fatal("current candidate lost source, normalizer or fresh excluded field", err, value)
	}
	if strings.Contains(fmt.Sprintf("%+v", bound), "CHANGED") || strings.Contains(fmt.Sprintf("%+v", inline), "fresh") {
		t.Fatal("inline diagnostic disclosed request values")
	}
}

func TestModelInlineWriterAdmissionPrecedesSnapshotLoading(t *testing.T) {
	config, actor := reportInlineFixture(t)
	inline, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	submission := checkedInlineSubmission(t, inline, actor, 7, InlineAccess{View: true, Change: true}, reportChangeInput("Changed"))
	other, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	unchecked := submission
	unchecked.writerActor = ""
	for _, item := range []struct {
		name       string
		inline     ModelInline[models.Ticket, models.ServiceReport]
		actor      auth.Principal
		ctx        context.Context
		submission InlineSubmission
	}{
		{"uninitialized", ModelInline[models.Ticket, models.ServiceReport]{}, actor, t.Context(), submission},
		{"unchecked", inline, actor, t.Context(), unchecked},
		{"declaration", other, actor, t.Context(), submission},
		{"actor", inline, sitePrincipal(t, "other", true, config.Permissions.View, config.Permissions.Change), t.Context(), submission},
		{"revoked", inline, sitePrincipal(t, actor.ID(), true, config.Permissions.View), t.Context(), submission},
		{"anonymous", inline, auth.Anonymous(), t.Context(), submission},
		{"canceled", inline, actor, canceled, submission},
		{"nil context", inline, actor, nil, submission},
	} {
		t.Run(item.name, func(t *testing.T) {
			calls := 0
			_, present, err := item.inline.BindCurrent(item.ctx, item.actor, item.submission, func(context.Context, InlineAccess) (InlineCurrent[models.Ticket, models.ServiceReport], error) {
				calls++
				return InlineCurrent[models.Ticket, models.ServiceReport]{}, nil
			})
			if err == nil || present || calls != 0 || item.name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("invalid writer reached current state", err, present, calls)
			}
			if item.name == "revoked" || item.name == "actor" || item.name == "anonymous" {
				operation, ok := err.(*OperationError)
				if !ok || operation.Code != OperationDenied {
					t.Fatal("current admission denial became an execution error", err)
				}
			}
		})
	}
	if _, _, err := inline.BindCurrent(t.Context(), actor, submission, nil); err == nil {
		t.Fatal("nil current loader accepted")
	}
}

func TestModelInlineCurrentParentCohortAndLoaderErrors(t *testing.T) {
	config, actor := reportInlineFixture(t)
	inline, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	submission := checkedInlineSubmission(t, inline, actor, 7, InlineAccess{View: true, Change: true}, reportChangeInput("Changed"))
	for _, mode := range []string{"parent", "foreign child", "missing cohort", "new cohort", "extra choices", "overflow", "loader error", "canceled loader"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("current scope failed")
			bound, present, err := inline.BindCurrent(ctx, actor, submission, func(context.Context, InlineAccess) (InlineCurrent[models.Ticket, models.ServiceReport], error) {
				child := models.NewServiceReportWithID(11)
				child.TicketID, child.Summary = 7, "Current"
				current := InlineCurrent[models.Ticket, models.ServiceReport]{Parent: models.NewTicketWithID(7), Children: InlineSnapshot[models.ServiceReport]{Current: []models.ServiceReport{child}}}
				switch mode {
				case "parent":
					current.Parent = models.NewTicketWithID(8)
				case "foreign child":
					current.Children.Current[0].TicketID = 8
				case "missing cohort":
					current.Children.Current = nil
				case "new cohort":
					current.Children.Current[0] = models.NewServiceReportWithID(12)
					current.Children.Current[0].TicketID = 7
				case "extra choices":
					current.ResolveChoices = func(context.Context, formmodel.InlinePreflight[models.Ticket, models.ServiceReport]) (forms.RowModelChoices, error) {
						return forms.NewRowModelChoices(map[int]map[string][]forms.Choice{0: {"summary": nil}}, nil), nil
					}
				case "overflow":
					current.Children.Current = []models.ServiceReport{child, child, child}
				case "loader error":
					return current, failure
				case "canceled loader":
					cancel()
					return current, failure
				}
				return current, nil
			})
			if err == nil && (!present || bound.Set().Valid()) {
				t.Fatal("stale or foreign current snapshot accepted", present)
			}
			if mode == "loader error" && err != failure || mode == "canceled loader" && (!errors.Is(err, failure) || !errors.Is(err, context.Canceled)) {
				t.Fatal("current scope error lost", err)
			}
		})
	}
}

func TestModelInlineCurrentRevisionCoversReadOnlyAndDeletedRows(t *testing.T) {
	config, actor := reportInlineFixture(t)
	schema := models.GoDjRelationSchema()
	for index := range schema.Models {
		if schema.Models[index].Name == "service_report" {
			schema.Models[index] = revisionReportDescriptor{}.Metadata()
		}
	}
	binding, err := orm.BindProject(schema)
	if err != nil {
		t.Fatal(err)
	}
	config.Project, config.Child = binding, orm.NewManager[models.ServiceReport](revisionReportDescriptor{})
	config.Form.Definition.Fields, config.RevisionField = []string{"summary"}, "completed"
	initial := config.Load
	config.Load = func(ctx context.Context, actor auth.Principal, id int64) (InlineSnapshot[models.ServiceReport], error) {
		current, err := initial(ctx, actor, id)
		current.Current[0].Completed = false // revision 1 at admission
		return current, err
	}
	inline, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, readonly := range []bool{false, true} {
		for _, deleted := range []bool{false, true} {
			data := map[string][]string{"reports-TOTAL_FORMS": {"1"}, "reports-INITIAL_FORMS": {"1"}, "reports-0-id": {"11"}, "reports-0-summary": {"Changed"}, "reports-0-expected_revision": {"1"}}
			if deleted {
				data["reports-0-DELETE"] = []string{"on"}
			}
			submission := checkedInlineSubmission(t, inline, actor, 7, InlineAccess{View: true, Change: !readonly, Delete: true}, forms.NewData(data))
			_, present, err := inline.BindCurrent(t.Context(), actor, submission, func(ctx context.Context, _ InlineAccess) (InlineCurrent[models.Ticket, models.ServiceReport], error) {
				current, err := initial(ctx, actor, 7) // revision 2 in the write scope
				return InlineCurrent[models.Ticket, models.ServiceReport]{Parent: models.NewTicketWithID(7), Children: current}, err
			})
			var operation *OperationError
			if !errors.As(err, &operation) || operation.Code != OperationConflict || present {
				t.Fatal("stale writer revision accepted", readonly, deleted, err)
			}
		}
	}
}

func TestModelInlineCurrentReadOnlyAndNewParentPreparation(t *testing.T) {
	config, actor := reportInlineFixture(t)
	inline, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	readonly := checkedInlineSubmission(t, inline, actor, 7, InlineAccess{View: true}, reportChangeInput("forged"))
	bound, present, err := inline.BindCurrent(t.Context(), actor, readonly, func(context.Context, InlineAccess) (InlineCurrent[models.Ticket, models.ServiceReport], error) {
		child := models.NewServiceReportWithID(11)
		child.TicketID, child.Summary = 7, "Fresh server"
		return InlineCurrent[models.Ticket, models.ServiceReport]{Parent: models.NewTicketWithID(7), Children: InlineSnapshot[models.ServiceReport]{Current: []models.ServiceReport{child}}}, nil
	})
	if err != nil || !present || !bound.Set().Valid() || bound.Access().Change {
		t.Fatal("read-only binding", present, err)
	}
	form := bound.Set().FormSet().Forms()[0].Form()
	if value, _ := form.Cleaned().String("summary"); !form.ReadOnly() || value != "Fresh server" {
		t.Fatal("read-only writer adopted submitted or stale text", value)
	}
	prepared, err := bound.Set().Prepare()
	if err != nil || len(prepared.Rows()) != 0 || len(prepared.Deleted()) != 0 {
		t.Fatal("read-only input became a write", err)
	}
	newData := forms.NewData(map[string][]string{"reports-TOTAL_FORMS": {"1"}, "reports-INITIAL_FORMS": {"0"}, "reports-0-summary": {"New report"}})
	newParent := checkedInlineSubmission(t, inline, actor, 0, InlineAccess{Add: true}, newData)
	bound, present, err = inline.BindCurrent(t.Context(), actor, newParent, func(context.Context, InlineAccess) (InlineCurrent[models.Ticket, models.ServiceReport], error) {
		return InlineCurrent[models.Ticket, models.ServiceReport]{}, nil
	})
	if err != nil || !present || !bound.Set().Valid() {
		t.Fatal("unsaved parent binding", present, err)
	}
	if _, err = bound.Set().Prepare(); err == nil {
		t.Fatal("unassigned parent became saveable")
	}
	prepared, err = bound.Set().PrepareWithParent(models.NewTicketWithID(27))
	if err != nil || len(prepared.Rows()) != 1 {
		t.Fatal("saved parent handoff", err)
	}
	value, err := prepared.Rows()[0].Model()
	_, hasKey := (models.ServiceReportDescriptor{}).PrimaryKey(value)
	if err != nil || value.TicketID != 27 || value.Summary != "New report" || hasKey {
		t.Fatal("new child lost typed parent/default state", err)
	}
}

func TestModelInlineCurrentRelationChoicesUseWriterScope(t *testing.T) {
	report, _ := reportInlineFixture(t)
	permissions := Permissions{View: "helpdesk.view_ticket", Add: "helpdesk.add_ticket", Change: "helpdesk.change_ticket", Delete: "helpdesk.delete_ticket"}
	actor := sitePrincipal(t, "writer", true, permissions.View, permissions.Change, "helpdesk.view_label")
	child := models.NewTicketWithID(11)
	child.CategoryID, child.Subject = 7, "Current"
	childSnapshot := InlineSnapshot[models.Ticket]{Current: []models.Ticket{child}, Related: func(models.Ticket, ir.ManyToManyField) ([]int64, bool) { return []int64{1}, true }}
	inline, err := NewInline(InlineConfig[models.Category, models.Ticket]{
		Project: report.Project, Parent: models.CategoryObjects, Child: models.TicketObjects, ParentWithID: models.NewCategoryWithID,
		ForeignKey: "category", Label: "Tickets", Set: report.Set, Permissions: permissions,
		Form: FormConfig{Definition: formmodel.Definition{Fields: []string{"subject", "labels"}}, RelatedChoices: []RelatedChoices{{Field: "labels", Permission: "helpdesk.view_label", Load: func(context.Context, auth.Principal) ([]forms.Choice, error) {
			return []forms.Choice{{Value: forms.Integer(1), Label: "Initial label"}}, nil
		}}}},
		Load: func(context.Context, auth.Principal, int64) (InlineSnapshot[models.Ticket], error) {
			return childSnapshot, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := forms.NewData(map[string][]string{"reports-TOTAL_FORMS": {"1"}, "reports-INITIAL_FORMS": {"1"}, "reports-0-id": {"11"}, "reports-0-category": {"7"}, "reports-0-subject": {"Changed"}, "reports-0-labels": {"1"}})
	submission := checkedInlineSubmission(t, inline, actor, 7, InlineAccess{View: true, Change: true}, data)
	for _, selected := range []int64{1, 2} {
		bound, present, err := inline.BindCurrent(t.Context(), actor, submission, func(context.Context, InlineAccess) (InlineCurrent[models.Category, models.Ticket], error) {
			return InlineCurrent[models.Category, models.Ticket]{Parent: models.NewCategoryWithID(7), Children: childSnapshot, ResolveChoices: func(context.Context, formmodel.InlinePreflight[models.Category, models.Ticket]) (forms.RowModelChoices, error) {
				return forms.NewRowModelChoices(map[int]map[string][]forms.Choice{0: {"labels": {{Value: forms.Integer(selected), Label: "Current label"}}}}, nil), nil
			}}, nil
		})
		if err != nil || !present || bound.Set().Valid() != (selected == 1) {
			t.Fatal("writer reused admission choices", selected, present, err)
		}
	}
	calls := 0
	revoked := sitePrincipal(t, actor.ID(), true, permissions.View, permissions.Change)
	if _, _, err = inline.BindCurrent(t.Context(), revoked, submission, func(context.Context, InlineAccess) (InlineCurrent[models.Category, models.Ticket], error) {
		calls++
		return InlineCurrent[models.Category, models.Ticket]{}, nil
	}); err == nil || calls != 0 {
		t.Fatal("revoked choice permission reached snapshot loader", err, calls)
	}
}

func TestModelInlineCurrentChoicesRemainPerRowAfterReordering(t *testing.T) {
	report, _ := reportInlineFixture(t)
	permissions := Permissions{View: "helpdesk.view_ticket", Add: "helpdesk.add_ticket", Change: "helpdesk.change_ticket", Delete: "helpdesk.delete_ticket"}
	actor := sitePrincipal(t, "writer", true, permissions.View, permissions.Change, "helpdesk.view_label")
	first, second := models.NewTicketWithID(11), models.NewTicketWithID(12)
	first.CategoryID, first.Subject = 7, "First"
	second.CategoryID, second.Subject = 7, "Second"
	children := InlineSnapshot[models.Ticket]{Current: []models.Ticket{first, second}, Related: func(row models.Ticket, _ ir.ManyToManyField) ([]int64, bool) {
		return []int64{row.ID - 10}, true
	}}
	config := forms.DefaultSetConfig()
	config.Prefix, config.ExtraForms, config.MaxForms, config.AbsoluteMax = "tickets", 0, 3, 4
	inline, err := NewInline(InlineConfig[models.Category, models.Ticket]{
		Project: report.Project, Parent: models.CategoryObjects, Child: models.TicketObjects, ParentWithID: models.NewCategoryWithID,
		ForeignKey: "category", Label: "Tickets", Set: config, Permissions: permissions,
		Form: FormConfig{Definition: formmodel.Definition{Fields: []string{"subject", "labels"}}, RelatedChoices: []RelatedChoices{{Field: "labels", Permission: "helpdesk.view_label", Load: func(context.Context, auth.Principal) ([]forms.Choice, error) {
			return []forms.Choice{{Value: forms.Integer(1), Label: "One"}, {Value: forms.Integer(2), Label: "Two"}}, nil
		}}}},
		Load: func(context.Context, auth.Principal, int64) (InlineSnapshot[models.Ticket], error) {
			return children, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"own", "cross_row", "missing_row", "prototype_only", "cohort_changed", "parent_changed", "resolver_error", "resolver_canceled"} {
		t.Run(mode, func(t *testing.T) {
			selected := "2"
			if mode == "cross_row" {
				selected = "1"
			}
			data := forms.NewData(map[string][]string{
				"tickets-TOTAL_FORMS": {"2"}, "tickets-INITIAL_FORMS": {"2"},
				"tickets-0-id": {"12"}, "tickets-0-category": {"7"}, "tickets-0-subject": {"Changed second"}, "tickets-0-labels": {selected},
				"tickets-1-id": {"11"}, "tickets-1-category": {"7"}, "tickets-1-subject": {"Changed first"}, "tickets-1-labels": {"1"},
			})
			submission := checkedInlineSubmission(t, inline, actor, 7, InlineAccess{View: true, Change: true}, data)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("selection read failed")
			calls := 0
			bound, present, err := inline.BindCurrent(ctx, actor, submission, func(context.Context, InlineAccess) (InlineCurrent[models.Category, models.Ticket], error) {
				snapshot := InlineCurrent[models.Category, models.Ticket]{Parent: models.NewCategoryWithID(7), Children: children}
				if mode == "cohort_changed" {
					snapshot.Children.Current = []models.Ticket{first}
				}
				if mode == "parent_changed" {
					snapshot.Parent = models.NewCategoryWithID(8)
				}
				snapshot.ResolveChoices = func(work context.Context, input formmodel.InlinePreflight[models.Category, models.Ticket]) (forms.RowModelChoices, error) {
					calls++
					if work != ctx || !input.AdmissionValid() || input.TotalForms() != 2 || input.InitialForms() != 2 {
						t.Fatal("selection resolver received unadmitted input")
					}
					if mode == "resolver_error" || mode == "resolver_canceled" {
						if mode == "resolver_canceled" {
							cancel()
						}
						return forms.RowModelChoices{}, failure
					}
					rows := make(map[int]map[string][]forms.Choice)
					for index, expected := range []int64{12, 11} {
						current, exists, err := input.Current(index)
						if err != nil || !exists || current.ID != expected {
							t.Fatal("resolver lost reordered current identity", index, err)
						}
						readonly, err := input.ReadOnly(index)
						if err != nil || readonly {
							t.Fatal("resolver lost admitted writable policy", err)
						}
						rowData, err := input.RowData(index)
						if err != nil {
							t.Fatal(err)
						}
						keyField, err := forms.ModelMultipleChoiceField("labels")
						if err != nil {
							t.Fatal(err)
						}
						keys, valid, err := keyField.ModelChoiceKeys(rowData)
						if err != nil || !valid || len(keys) != 1 {
							t.Fatal("row lookup input lost", keys, valid, err)
						}
						want := expected - 10
						if mode == "cross_row" && index == 0 {
							want = 1
						}
						if keys[0] != want {
							t.Fatal("row lookup read another input index", index, keys)
						}
						rows[index] = map[string][]forms.Choice{"labels": {{Value: forms.Integer(current.ID - 10), Label: "Own label"}}}
					}
					if mode == "missing_row" {
						delete(rows, 0)
					}
					if mode == "prototype_only" {
						return forms.NewRowModelChoices(nil, rows[0]), nil
					}
					return forms.NewRowModelChoices(rows, nil), nil
				}
				return snapshot, nil
			})
			if mode == "parent_changed" || mode == "cohort_changed" {
				if calls != 0 || err == nil && (!present || bound.Set().Valid()) {
					t.Fatal("rejected identity reached selection I/O", calls, err)
				}
				return
			}
			if mode == "resolver_error" || mode == "resolver_canceled" {
				if calls != 1 || !errors.Is(err, failure) || mode == "resolver_canceled" && !errors.Is(err, context.Canceled) {
					t.Fatal("selection execution error lost", calls, err)
				}
				return
			}
			if err != nil || !present || calls != 1 || bound.Set().Valid() != (mode == "own") {
				t.Fatal("row choices leaked or authorized selections rejected", mode, calls, err)
			}
			if mode == "own" {
				prepared, err := bound.Set().Prepare()
				if err != nil || len(prepared.Rows()) != 2 {
					t.Fatal("scoped rows did not prepare", err)
				}
			}
		})
	}
}
