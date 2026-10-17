package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func reportInlineFixture(t *testing.T) (InlineConfig[models.Ticket, models.ServiceReport], auth.Principal) {
	t.Helper()
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix = "reports"
	config.MaxForms = 1
	config.AbsoluteMax = 2
	config.CanDelete = true
	config.ValidateMax = true
	permissions := Permissions{View: "helpdesk.view_report", Add: "helpdesk.add_report", Change: "helpdesk.change_report", Delete: "helpdesk.delete_report"}
	return InlineConfig[models.Ticket, models.ServiceReport]{Project: binding, Parent: models.TicketObjects, Child: models.ServiceReportObjects, ParentWithID: models.NewTicketWithID, ForeignKey: "ticket", Label: "Report", Form: FormConfig{Definition: formmodel.Definition{Fields: []string{"summary", "completed"}}}, Set: config, Permissions: permissions, Load: func(context.Context, auth.Principal, int64) (InlineSnapshot[models.ServiceReport], error) {
		current := models.NewServiceReportWithID(11)
		current.TicketID, current.Summary, current.Completed = 7, "Server report", true
		return InlineSnapshot[models.ServiceReport]{Current: []models.ServiceReport{current}}, nil
	}}, sitePrincipal(t, "actor", true, permissions.View, permissions.Add, permissions.Change, permissions.Delete)
}

func TestInlineAdmissionOwnershipReadOnlyAndDiagnostics(t *testing.T) {
	config, actor := reportInlineFixture(t)
	loads, checks := 0, 0
	original := config.Load
	config.Load = func(ctx context.Context, p auth.Principal, id int64) (InlineSnapshot[models.ServiceReport], error) {
		loads++
		return original(ctx, p, id)
	}
	config.Validate = func(context.Context, auth.Principal, formmodel.InlineSet[models.Ticket, models.ServiceReport]) error {
		checks++
		return nil
	}
	inline, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	access := InlineAccess{View: true, Add: true, Change: true, Delete: true}
	current, err := inline.bind(t.Context(), actor, 7, access, nil)
	if err != nil || loads != 1 || checks != 0 {
		t.Fatal("GET admission", err, loads, checks)
	}
	data := forms.NewData(map[string][]string{"reports-TOTAL_FORMS": {"1"}, "reports-INITIAL_FORMS": {"1"}, "reports-0-id": {"11"}, "reports-0-ticket": {"7"}, "reports-0-summary": {"Changed"}})
	bound, err := inline.bind(t.Context(), actor, 7, access, &data)
	if err != nil || !bound.set.Valid() {
		t.Fatal("valid bind", err, bound.set.NonFormErrors())
	}
	readOnly := access
	readOnly.Change = false
	bound, err = inline.bind(t.Context(), actor, 7, readOnly, &data)
	if err != nil || !bound.set.Valid() {
		t.Fatal(err)
	}
	row := bound.set.Forms()[0].Form()
	value, _ := row.Cleaned().String("summary")
	if !row.ReadOnly() || value != "Server report" {
		t.Fatal("readonly form trusted input", value)
	}
	view, err := inlineContext(InlineSubmission{entries: []inlineBound{bound}})
	if err != nil {
		t.Fatal(err)
	}
	_ = view
	// New parents and add-only rights do not read existing children.
	before := loads
	for _, item := range []struct {
		id     int64
		access InlineAccess
	}{{0, access}, {7, InlineAccess{Add: true}}} {
		if _, err = inline.bind(t.Context(), actor, item.id, item.access, nil); err != nil {
			t.Fatal(err)
		}
	}
	if loads != before {
		t.Fatal("child read without saved visible parent")
	}
	denied := sitePrincipal(t, "denied", true)
	if _, err = inline.bind(t.Context(), denied, 7, access, &data); err == nil || loads != before {
		t.Fatal("admission occurred after query")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = inline.bind(cancelled, actor, 7, access, &data); !errors.Is(err, context.Canceled) || loads != before {
		t.Fatal("cancelled load", err)
	}
	noDelete := access
	noDelete.Delete = false
	deleted := forms.NewData(map[string][]string{"reports-TOTAL_FORMS": {"1"}, "reports-INITIAL_FORMS": {"1"}, "reports-0-id": {"11"}, "reports-0-DELETE": {"on"}})
	if _, err = inline.bind(t.Context(), actor, 7, noDelete, &deleted); err == nil {
		t.Fatal("forged DELETE allowed")
	}
	owner := &inlineToken{}
	model := registeredModel{inlines: []Inline{inline}, inlineOwner: owner}
	valid, err := inline.bind(t.Context(), actor, 7, access, &data)
	if err != nil {
		t.Fatal(err)
	}
	submission := InlineSubmission{owner: owner, parentID: 7, entries: []inlineBound{valid}}
	if _, err = model.checkedInlines(t.Context(), actor, 7, submission); err != nil {
		t.Fatal(err)
	}
	for _, other := range []InlineSubmission{{}, {owner: &inlineToken{}, parentID: 7, entries: submission.entries}, {owner: owner, parentID: 8, entries: submission.entries}, {owner: owner, parentID: 7, entries: []inlineBound{current}}, {owner: owner, parentID: 7, entries: []inlineBound{valid, valid}}} {
		if _, err = model.checkedInlines(t.Context(), actor, 7, other); err == nil {
			t.Fatal("forged/unbound origin allowed")
		}
	}
	failure := RejectInline("reports", 0, validation.NewErrors(validation.New("summary", "unique")), nil)
	updated, handled, err := submission.withRejection(t.Context(), failure)
	if err != nil || !handled || updated.valid() || !submission.valid() {
		t.Fatal("mutable or ineffective rejection", handled, err)
	}
	for _, failure := range []error{fmt.Errorf("rollback: %w", failure), errors.Join(failure, errors.New("unknown outcome"))} {
		if _, handled, err := submission.withRejection(t.Context(), failure); handled || err != nil {
			t.Fatal("wrapped failure downgraded to validation")
		}
	}
	if _, handled, err := submission.withRejection(cancelled, failure); !handled || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled rejection lost failure")
	}
	if _, _, err := submission.withRejection(t.Context(), RejectInline("other", 0, validation.NewErrors(validation.New("summary", "unique")), nil)); err == nil {
		t.Fatal("foreign inline diagnostic accepted")
	}
	if strings.Contains(fmt.Sprintf("%+v", submission), "Server report") {
		t.Fatal("submission logging disclosed values")
	}
}

func TestInlineConfigurationAndInputBounds(t *testing.T) {
	base, actor := reportInlineFixture(t)
	cases := []struct {
		name   string
		change func(*InlineConfig[models.Ticket, models.ServiceReport])
	}{
		{"missing loader", func(c *InlineConfig[models.Ticket, models.ServiceReport]) { c.Load = nil }},
		{"bad parent constructor", func(c *InlineConfig[models.Ticket, models.ServiceReport]) { c.ParentWithID = nil }},
		{"missing label", func(c *InlineConfig[models.Ticket, models.ServiceReport]) { c.Label = "" }},
		{"wrong FK", func(c *InlineConfig[models.Ticket, models.ServiceReport]) { c.ForeignKey = "summary" }},
		{"invalid revision", func(c *InlineConfig[models.Ticket, models.ServiceReport]) { c.RevisionField = "summary" }},
		{"unbounded rows", func(c *InlineConfig[models.Ticket, models.ServiceReport]) { c.Set.AbsoluteMax = MaximumInlineRows + 1 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.change(&config)
			if _, err := NewInline(config); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	inline, err := NewInline(base)
	if err != nil {
		t.Fatal(err)
	}
	config := base
	config.ParentWithID = func(int64) models.Ticket { return models.NewTicketWithID(99) }
	bad, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bad.bind(t.Context(), actor, 7, InlineAccess{View: true}, nil); err == nil {
		t.Fatal("incorrect factory key accepted")
	}
	installed, err := apps.New([]apps.Config{{Name: "github.com/progresshans/godj/examples/helpdesk/models", Label: "helpdesk"}})
	if err != nil {
		t.Fatal(err)
	}
	parent := models.TicketDescriptor{}.Metadata()
	if _, err = prepareInlines([]Inline{inline}, installed, "helpdesk", parent); err != nil {
		t.Fatal(err)
	}
	for _, entries := range [][]Inline{{{}}, {inline, inline}} {
		if _, err = prepareInlines(entries, installed, "helpdesk", parent); err == nil {
			t.Fatal("invalid/duplicate adapter accepted")
		}
	}
	if _, err = prepareInlines([]Inline{inline}, installed, "other", parent); err == nil {
		t.Fatal("wrong canonical application accepted")
	}
	for _, name := range []string{"reports-2-summary", "reports--1-summary", "reports-01-summary", "reports-0-unknown", "reports-0-summary-extra", "other-0-summary"} {
		if _, ok := inlineInputRule(inline, name); ok {
			t.Fatal("unknown/outside input accepted", name)
		}
	}
	for _, name := range []string{"reports-TOTAL_FORMS", "reports-0-id", "reports-1-summary", "reports-0-ticket", "reports-0-DELETE"} {
		if _, ok := inlineInputRule(inline, name); !ok {
			t.Fatal("valid input rejected", name)
		}
	}
	raw := url.Values{"reports-0-summary": {"one", "two"}, "reports-TOTAL_FORMS": {"1"}}
	if _, err := parseSiteValues(raw.Encode(), inputRules{}, inline); err != nil {
		t.Fatal("parser lost field multiple-value diagnostics", err)
	}
	if _, err := parseSiteValues("reports-999999999999999999999-summary=x", inputRules{}, inline); err == nil {
		t.Fatal("oversized row index accepted")
	}
	descriptor := inlineDescriptors([]Inline{inline})[0]
	descriptor.Model.Fields[0].Name = "changed"
	descriptor.FormFields[0] = forms.Field{}
	if fresh := inlineDescriptors([]Inline{inline})[0]; fresh.Model.Fields[0].Name == "changed" || fresh.FormFields[0].Name() == "" {
		t.Fatal("descriptor aliases adapter")
	}
}

// A small pure descriptor gives the revision condition its own integer storage
// while retaining the generated model's key-presence and clone behavior.
type revisionReportDescriptor struct{ models.ServiceReportDescriptor }

func (revisionReportDescriptor) Metadata() ir.Model {
	model := models.ServiceReportDescriptor{}.Metadata()
	for i := range model.Fields {
		if model.Fields[i].Name == "completed" {
			model.Fields[i].Kind = ir.FieldInteger
			model.Fields[i].Default = nil
		}
	}
	return model
}
func (descriptor revisionReportDescriptor) WriteFieldValue(value models.ServiceReport, field ir.Field) (query.Value, bool) {
	if field.Name == "completed" {
		if value.Completed {
			return query.Integer(2), true
		}
		return query.Integer(1), true
	}
	return descriptor.ServiceReportDescriptor.WriteFieldValue(value, field)
}
func (descriptor revisionReportDescriptor) SetFieldValue(value *models.ServiceReport, field ir.Field, input query.Value) bool {
	if field.Name == "completed" {
		number, ok := input.Integer()
		if !ok || value == nil || number < 1 || number > 2 {
			return false
		}
		value.Completed = number == 2
		return true
	}
	return descriptor.ServiceReportDescriptor.SetFieldValue(value, field, input)
}

func TestInlineRevisionConditionCoversDeletedAndReadOnlyRows(t *testing.T) {
	config, actor := reportInlineFixture(t)
	schema := models.GoDjRelationSchema()
	for i := range schema.Models {
		if schema.Models[i].Name == "service_report" {
			schema.Models[i] = revisionReportDescriptor{}.Metadata()
		}
	}
	binding, err := orm.BindProject(schema)
	if err != nil {
		t.Fatal(err)
	}
	config.Project = binding
	config.Child = orm.NewManager[models.ServiceReport](revisionReportDescriptor{})
	config.Form.Definition.Fields = []string{"summary"}
	config.RevisionField = "completed"
	inline, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, readonly := range []bool{false, true} {
		for _, deleted := range []bool{false, true} {
			for _, revision := range []string{"2", "1", "02", ""} {
				values := map[string][]string{"reports-TOTAL_FORMS": {"1"}, "reports-INITIAL_FORMS": {"1"}, "reports-0-id": {"11"}, "reports-0-summary": {"Server report"}, "reports-0-expected_revision": {revision}}
				if deleted {
					values["reports-0-DELETE"] = []string{"on"}
				}
				data := forms.NewData(values)
				bound, err := inline.bind(t.Context(), actor, 7, InlineAccess{View: true, Change: !readonly, Delete: true}, &data)
				if revision == "2" {
					if err != nil || !bound.set.Valid() {
						t.Fatal("matching revision", readonly, deleted, err)
					}
				} else if err == nil {
					t.Fatal("stale/malformed revision accepted", readonly, deleted, revision)
				}
			}
		}
	}
}

func TestInlineConcurrentBindingsKeepSubmissionAndCurrentDetached(t *testing.T) {
	config, actor := reportInlineFixture(t)
	inline, err := NewInline(config)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the startup selection after construction cannot alter requests.
	config.Form.Definition.Fields[0] = "forged"
	failures := make(chan error, 32)
	for index := 0; index < 32; index++ {
		go func(index int) {
			text := fmt.Sprintf("request %d", index)
			data := forms.NewData(map[string][]string{"reports-TOTAL_FORMS": {"1"}, "reports-INITIAL_FORMS": {"1"}, "reports-0-id": {"11"}, "reports-0-summary": {text}})
			bound, err := inline.bind(context.Background(), actor, 7, InlineAccess{View: true, Change: true}, &data)
			if err == nil {
				value, present := bound.set.Forms()[0].Form().Cleaned().String("summary")
				if !present || value != text || !bound.set.Valid() {
					err = errors.New("request values crossed inline bindings")
				}
			}
			failures <- err
		}(index)
	}
	for index := 0; index < 32; index++ {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
}
