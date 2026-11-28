package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	identitymodels "github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func collectionSetFixture(t *testing.T) (CollectionFormSetConfig[models.Ticket], auth.Principal) {
	t.Helper()
	policy := forms.DefaultSetConfig()
	policy.Prefix, policy.MinForms, policy.ExtraForms, policy.MaxForms, policy.AbsoluteMax = "tickets", 1, 1, 4, 4
	policy.ValidateMin, policy.ValidateMax = true, true
	// Core Admin tests bind the complete generated model graph directly. The
	// host's account runtime itself composes Admin and belongs in consumer tests.
	binding, err := orm.BindProject(identitymodels.GoDjRelationSchema(), models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	return CollectionFormSetConfig[models.Ticket]{Name: "create", Label: "Create tickets", Manager: models.TicketObjects,
		Project: binding, Source: ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "ticket"},
		Permissions: Permissions{View: "helpdesk.view_ticket", Add: "helpdesk.add_ticket", Change: "helpdesk.change_ticket", Delete: "helpdesk.delete_ticket"},
		Form: FormConfig{Definition: formmodel.Definition{Fields: []string{"subject", "closed", "external_reference", "labels"}, PostClean: formmodel.PostClean{Fields: []string{"category"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
			return forms.NewValues(map[string]forms.Value{"category": forms.Integer(7)}), validation.NewErrors()
		}}}, RelatedChoices: []RelatedChoices{testRelationChoices(t, (models.TicketDescriptor{}).Metadata(), "helpdesk", "labels", "helpdesk.view_label", func(context.Context, auth.Principal) ([]forms.Choice, error) {
			return []forms.Choice{{Value: forms.Integer(9), Label: "Owned <&>"}}, nil
		})}}, Set: policy,
		Load: func(context.Context, auth.Principal, CollectionSetAccess) (CollectionSetSnapshot[models.Ticket], error) {
			return CollectionSetSnapshot[models.Ticket]{}, nil
		},
		Run: func(context.Context, auth.Principal, CollectionSetSubmission) (CollectionSetResult, error) {
			return CollectionSetResult{}, errors.New("writer not configured")
		},
	}, sitePrincipal(t, "actor", true, "helpdesk.add_ticket", "helpdesk.view_ticket", "helpdesk.view_label")
}

func collectionSetData() forms.Data {
	return forms.NewData(url.Values{"tickets-TOTAL_FORMS": {"2"}, "tickets-INITIAL_FORMS": {"0"}, "tickets-0-subject": {"  First <&>  "}, "tickets-0-labels": {"9"}, "tickets-1-subject": {"Second"}})
}
func collectionTestAccess(actor auth.Principal) CollectionSetAccess {
	return CollectionSetAccess{View: actor.Has("helpdesk.view_ticket") || actor.Has("helpdesk.change_ticket"), Add: actor.Has("helpdesk.add_ticket"), Change: actor.Has("helpdesk.change_ticket"), Delete: actor.Has("helpdesk.delete_ticket")}
}
func collectionTestChoices(_ context.Context, input formmodel.SetPreflight[models.Ticket]) (forms.RowModelChoices, error) {
	rows := make(map[int]map[string][]forms.Choice)
	for index := 0; index < input.TotalForms(); index++ {
		rows[index] = map[string][]forms.Choice{"labels": {{Value: forms.Integer(9), Label: "Current label"}}}
	}
	return forms.NewRowModelChoices(rows, nil), nil
}
func collectionTestBind(t *testing.T, definition ModelCollectionFormSet[models.Ticket], actor auth.Principal, data *forms.Data) collectionFormSetBound {
	t.Helper()
	bound, err := definition.View().bind(t.Context(), actor, collectionTestAccess(actor), data)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}
func collectionTestDeclaration(t *testing.T, config CollectionFormSetConfig[models.Ticket], run func(BoundCollectionFormSet[models.Ticket]) (CollectionSetResult, error)) ModelCollectionFormSet[models.Ticket] {
	t.Helper()
	var definition ModelCollectionFormSet[models.Ticket]
	config.Run = func(ctx context.Context, actor auth.Principal, submission CollectionSetSubmission) (CollectionSetResult, error) {
		bound, err := definition.BindCurrent(ctx, actor, submission, func(context.Context, CollectionSetAccess) (CollectionSetCurrent[models.Ticket], error) {
			return CollectionSetCurrent[models.Ticket]{ResolveChoices: collectionTestChoices}, nil
		})
		if err != nil {
			return CollectionSetResult{}, err
		}
		return run(bound)
	}
	var err error
	definition, err = NewCollectionFormSet(config)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestCollectionFormSetOwnsTypedCandidatesPermissionsAndResult(t *testing.T) {
	config, actor := collectionSetFixture(t)
	loads, cleans, writes := 0, 0, 0
	config.Form.Definition.Validators = []forms.CrossValidator{forms.CrossValidatorFunc(func(forms.Values) validation.Errors { cleans++; return validation.NewErrors() })}
	config.Load = func(context.Context, auth.Principal, CollectionSetAccess) (CollectionSetSnapshot[models.Ticket], error) {
		loads++
		return CollectionSetSnapshot[models.Ticket]{}, nil
	}
	returned := []CollectionSetWrite{{Index: 1, Kind: "create", ID: 22}, {Index: 0, Kind: "create", ID: 21}}
	definition := collectionTestDeclaration(t, config, func(bound BoundCollectionFormSet[models.Ticket]) (CollectionSetResult, error) {
		prepared, err := bound.Set().Prepare()
		if err != nil || len(prepared.Rows()) != 2 || bound.Access() != collectionTestAccess(actor) {
			t.Fatal("typed candidate", err)
		}
		for index, row := range prepared.Rows() {
			value, err := row.Model()
			if err != nil || row.Existing() || value.ID != 0 || value.Closed || value.CategoryID != 7 || row.Index() != index {
				t.Fatal("new row state", err)
			}
			if index == 0 && value.Subject != "First <&>" {
				t.Fatal("cleaned text lost")
			}
		}
		keys, present := prepared.Rows()[0].Prepared().Collections().Integers("labels")
		if !present || len(keys) != 1 || keys[0] != 9 {
			t.Fatal("typed collection lost")
		}
		writes++
		return bound.Result(returned)
	})
	config.Form.Definition.Fields[0] = "closed"
	config.Form.RelatedChoices[0] = testChoicesWithPermission(config.Form.RelatedChoices[0], "helpdesk.delete_ticket")
	descriptor := collectionFormSetDescriptors([]CollectionFormSet{definition.View()})[0]
	descriptor.Permissions.Add = "helpdesk.delete_ticket"
	descriptor.FormFields[0] = cField(t, "forged")
	denied := sitePrincipal(t, "denied", true, "helpdesk.add_ticket")
	if _, err := definition.View().bind(t.Context(), denied, CollectionSetAccess{Add: true}, nil); err == nil || loads != 0 {
		t.Fatal("loaded before target admission", err)
	}
	view := collectionTestBind(t, definition, actor, nil)
	if view.set.Bound() || view.set.TotalForms() != 2 || view.empty == nil || view.empty.Prefix() != "tickets-__prefix__" || loads != 1 || cleans != 0 {
		t.Fatal("unbound formset")
	}
	if _, err := view.run(t.Context()); err == nil || writes != 0 {
		t.Fatal("unbound set reached writer", err)
	}
	data := collectionSetData()
	bound := collectionTestBind(t, definition, actor, &data)
	if !bound.set.Valid() || cleans != 2 {
		t.Fatal("initial typed binding", cleans)
	}
	result, err := bound.run(t.Context())
	if err != nil || writes != 1 || cleans != 4 || loads != 2 {
		t.Fatal("current binding or loader ownership", err, cleans, loads)
	}
	returned[0].ID = 999
	ids := result.Writes()
	if len(ids) != 2 || ids[0].Index != 0 || ids[0].ID != 21 || ids[1].ID != 22 {
		t.Fatal("result ownership/order")
	}
	ids[0].ID = 999
	if result.Writes()[0].ID != 21 {
		t.Fatal("result accessor aliases storage")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := bound.run(ctx); !errors.Is(err, context.Canceled) || writes != 1 {
		t.Fatal("canceled request reached writer", err)
	}
	row, err := formSetRowContext(*bound.empty, definition.View().primary, forms.Null(), false, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := row.Member("identity")
	if text, _ := identity.AsString(); text != "" {
		t.Fatal("new-row prototype copied a current identity")
	}
	if strings.Contains(fmt.Sprintf("%+v %+v", definition, result), "Current label") {
		t.Fatal("diagnostic disclosed row data")
	}
}

func TestCollectionFormSetRejectsUnsafeConfigurationAndUnconfirmedResults(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*CollectionFormSetConfig[models.Ticket])
	}{
		{"name", func(c *CollectionFormSetConfig[models.Ticket]) { c.Name = "../create" }},
		{"label", func(c *CollectionFormSetConfig[models.Ticket]) { c.Label = "" }},
		{"run", func(c *CollectionFormSetConfig[models.Ticket]) { c.Run = nil }},
		{"load", func(c *CollectionFormSetConfig[models.Ticket]) { c.Load = nil }},
		{"permission", func(c *CollectionFormSetConfig[models.Ticket]) { c.Permissions.Add = "" }},
		{"additional", func(c *CollectionFormSetConfig[models.Ticket]) {
			c.AdditionalPermissions = []auth.Permission{"invalid"}
		}},
		{"project", func(c *CollectionFormSetConfig[models.Ticket]) { c.Project = orm.ProjectBinding{} }},
		{"source", func(c *CollectionFormSetConfig[models.Ticket]) { c.Source.ModelName = "label" }},
		{"unbounded", func(c *CollectionFormSetConfig[models.Ticket]) { c.Set.AbsoluteMax = MaximumInlineRows + 1 }},
		{"prefix", func(c *CollectionFormSetConfig[models.Ticket]) { c.Set.Prefix = "../../" }},
		{"no count validation", func(c *CollectionFormSetConfig[models.Ticket]) { c.Set.ValidateMax = false }},
		{"revision", func(c *CollectionFormSetConfig[models.Ticket]) { c.RevisionField = "subject" }},
		{"missing choices", func(c *CollectionFormSetConfig[models.Ticket]) { c.Form.RelatedChoices = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := collectionSetFixture(t)
			test.edit(&c)
			if _, err := NewCollectionFormSet(c); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	for _, test := range []struct {
		name   string
		writes []CollectionSetWrite
	}{
		{"missing", nil}, {"short", []CollectionSetWrite{{0, "create", 1}}},
		{"extra", []CollectionSetWrite{{0, "create", 1}, {1, "create", 2}, {2, "create", 3}}},
		{"duplicate_id", []CollectionSetWrite{{0, "create", 1}, {1, "create", 1}}},
		{"duplicate_index", []CollectionSetWrite{{0, "create", 1}, {0, "create", 2}}},
		{"zero", []CollectionSetWrite{{0, "create", 1}, {1, "create", 0}}},
		{"wrong_kind", []CollectionSetWrite{{0, "update", 1}, {1, "create", 2}}},
		{"foreign_index", []CollectionSetWrite{{0, "create", 1}, {7, "create", 2}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, actor := collectionSetFixture(t)
			definition := collectionTestDeclaration(t, config, func(bound BoundCollectionFormSet[models.Ticket]) (CollectionSetResult, error) {
				return bound.Result(test.writes)
			})
			data := collectionSetData()
			bound := collectionTestBind(t, definition, actor, &data)
			result, err := bound.run(t.Context())
			if len(result.Writes()) != 0 || !errors.Is(err, ErrReconciliationRequired) {
				t.Fatal("published unconfirmed writes", err)
			}
		})
	}
	config, actor := collectionSetFixture(t)
	config.Run = func(context.Context, auth.Principal, CollectionSetSubmission) (CollectionSetResult, error) {
		return CollectionSetResult{}, nil
	}
	definition, err := NewCollectionFormSet(config)
	if err != nil {
		t.Fatal(err)
	}
	data := collectionSetData()
	bound := collectionTestBind(t, definition, actor, &data)
	if _, err := bound.run(t.Context()); !errors.Is(err, ErrReconciliationRequired) {
		t.Fatal("zero result accepted", err)
	}
	view := definition.View()
	if _, err := prepareCollectionFormSets([]CollectionFormSet{view, view}, view.model, view.identity); err == nil {
		t.Fatal("duplicate definition")
	}
	if _, err := prepareCollectionFormSets([]CollectionFormSet{view}, models.CategoryDescriptor{}.Metadata(), ir.ModelIdentity{AppLabel: "helpdesk", ModelName: "category"}); err == nil {
		t.Fatal("foreign model accepted")
	}
	if _, err := prepareCollectionFormSets([]CollectionFormSet{view}, view.model, ir.ModelIdentity{AppLabel: "other", ModelName: "ticket"}); err == nil {
		t.Fatal("foreign app accepted")
	}
}

func TestCollectionFormSetRedisplaysOnlyDirectOwnedDiagnostics(t *testing.T) {
	config, actor := collectionSetFixture(t)
	definition, err := NewCollectionFormSet(config)
	if err != nil {
		t.Fatal(err)
	}
	data := collectionSetData()
	bound := collectionTestBind(t, definition, actor, &data)
	rows := map[int]validation.Errors{1: validation.NewErrors(validation.New("subject", "invalid"))}
	rejected := RejectCollectionFormSet(rows, validation.NewErrors(), errors.New("private database value"))
	rows[1] = validation.NewErrors(validation.New("forged", "invalid"))
	updated, err := bound.withRejection(t.Context(), rejected)
	if err != nil || updated.set.Valid() || updated.run != nil || updated.set.Forms()[1].Form().Errors().Len() != 1 || strings.Contains(rejected.Error(), "private") {
		t.Fatal("rejection ownership", err)
	}
	if _, err := bound.withRejection(t.Context(), errors.Join(rejected, errors.New("rollback failed"))); err == nil {
		t.Fatal("rollback failure became validation")
	}
	for _, failure := range []error{RejectCollectionFormSet(map[int]validation.Errors{7: validation.NewErrors(validation.New("subject", "invalid"))}, validation.Errors{}, nil), RejectCollectionFormSet(map[int]validation.Errors{0: validation.NewErrors(validation.New("forged", "invalid"))}, validation.Errors{}, nil)} {
		if _, err := bound.withRejection(t.Context(), failure); err == nil {
			t.Fatal("foreign row/field diagnostic accepted")
		}
	}
}
