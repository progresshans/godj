package admin

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/validation"
)

func collectionSetFixture(t *testing.T) (CollectionFormSetConfig[models.Ticket], auth.Principal) {
	t.Helper()
	policy := forms.DefaultSetConfig()
	policy.Prefix, policy.MinForms, policy.ExtraForms, policy.MaxForms, policy.AbsoluteMax = "tickets", 1, 1, 4, 4
	policy.ValidateMin, policy.ValidateMax = true, true
	return CollectionFormSetConfig[models.Ticket]{Name: "create", Label: "Create tickets", Manager: models.TicketObjects, Permission: "helpdesk.add_ticket",
		Form: FormConfig{Definition: formmodel.Definition{Fields: []string{"subject", "closed", "external_reference", "labels"}, PostClean: formmodel.PostClean{Fields: []string{"category"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
			return forms.NewValues(map[string]forms.Value{"category": forms.Integer(7)}), validation.NewErrors()
		}}}, RelatedChoices: []RelatedChoices{{Field: "labels", Permission: "helpdesk.view_label", Load: func(context.Context, auth.Principal) ([]forms.Choice, error) {
			return []forms.Choice{{Value: forms.Integer(9), Label: "Owned <&>"}}, nil
		}}}}, Set: policy,
		Run: func(context.Context, auth.Principal, formmodel.InstanceSet[models.Ticket]) ([]int64, error) {
			return []int64{21, 22}, nil
		},
	}, sitePrincipal(t, "actor", true, "helpdesk.add_ticket", "helpdesk.view_ticket", "helpdesk.view_label")
}

func collectionSetData() forms.Data {
	return forms.NewData(url.Values{"tickets-TOTAL_FORMS": {"2"}, "tickets-INITIAL_FORMS": {"0"}, "tickets-0-subject": {"  First <&>  "}, "tickets-0-labels": {"9"}, "tickets-1-subject": {"Second"}})
}

func TestCollectionFormSetOwnsTypedCandidatesPermissionsAndResult(t *testing.T) {
	config, actor := collectionSetFixture(t)
	loads, cleans, writes := 0, 0, 0
	config.Form.Definition.Validators = []forms.CrossValidator{forms.CrossValidatorFunc(func(forms.Values) validation.Errors { cleans++; return validation.NewErrors() })}
	loader := config.Form.RelatedChoices[0].Load
	config.Form.RelatedChoices[0].Load = func(ctx context.Context, actor auth.Principal) ([]forms.Choice, error) {
		loads++
		return loader(ctx, actor)
	}
	returned := []int64{21, 22}
	config.Run = func(ctx context.Context, principal auth.Principal, set formmodel.InstanceSet[models.Ticket]) ([]int64, error) {
		writes++
		prepared, err := set.Prepare()
		if err != nil || len(prepared.Rows()) != 2 || principal.ID() != actor.ID() {
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
		return returned, nil
	}
	definition, err := NewCollectionFormSet(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Form.Definition.Fields[0] = "closed"
	config.Form.RelatedChoices[0].Permission = "helpdesk.delete_ticket"
	descriptor := collectionFormSetDescriptors([]CollectionFormSet{definition})[0]
	descriptor.Permissions[0] = "helpdesk.delete_ticket"
	descriptor.FormFields[0] = cField(t, "forged")
	denied := sitePrincipal(t, "denied", true, "helpdesk.add_ticket")
	if _, err := definition.bind(t.Context(), denied, nil); err == nil || loads != 0 {
		t.Fatal("choices loaded without required permission", err)
	}
	view, err := definition.bind(t.Context(), actor, nil)
	if err != nil || view.set.Bound() || view.set.TotalForms() != 2 || view.empty.Prefix() != "tickets-__prefix__" || loads != 1 || cleans != 0 {
		t.Fatal("unbound formset", err)
	}
	if _, err := view.run(t.Context()); err == nil || writes != 0 {
		t.Fatal("unbound set reached writer", err)
	}
	data := collectionSetData()
	bound, err := definition.bind(t.Context(), actor, &data)
	if err != nil || !bound.set.Valid() || cleans != 2 {
		t.Fatal("typed binding", err, cleans)
	}
	ids, err := bound.run(t.Context())
	if err != nil || writes != 1 || cleans != 2 || len(ids) != 2 || ids[0] != 21 || ids[1] != 22 {
		t.Fatal("candidate was rebound or write result lost", err)
	}
	returned[0] = 999
	if ids[0] != 21 {
		t.Fatal("caller owns result identities")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := bound.run(ctx); !errors.Is(err, context.Canceled) || writes != 1 {
		t.Fatal("canceled request reached writer", err)
	}
	row, err := formSetRowContext(bound.empty, "", forms.Null(), false, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := row.Member("identity_name")
	text, _ := identity.AsString()
	if text != "" {
		t.Fatal("new-row prototype exposes an editable identity")
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
		{"permission", func(c *CollectionFormSetConfig[models.Ticket]) { c.Permission = "" }},
		{"unbounded", func(c *CollectionFormSetConfig[models.Ticket]) { c.Set.AbsoluteMax = MaximumInlineRows + 1 }},
		{"empty allowed", func(c *CollectionFormSetConfig[models.Ticket]) { c.Set.MinForms = 0 }},
		{"no count validation", func(c *CollectionFormSetConfig[models.Ticket]) { c.Set.ValidateMax = false }},
		{"delete", func(c *CollectionFormSetConfig[models.Ticket]) { c.Set.CanDelete = true }},
		{"order", func(c *CollectionFormSetConfig[models.Ticket]) { c.Set.CanOrder = true }},
		{"readonly", func(c *CollectionFormSetConfig[models.Ticket]) { c.Set.ReadOnlyInitial = true }},
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
		name    string
		ids     []int64
		failure error
	}{
		{"missing", nil, nil}, {"short", []int64{1}, nil}, {"extra", []int64{1, 2, 3}, nil}, {"duplicate", []int64{1, 1}, nil}, {"zero", []int64{1, 0}, nil}, {"failure", []int64{1, 2}, errors.New("private write error")},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, actor := collectionSetFixture(t)
			config.Run = func(context.Context, auth.Principal, formmodel.InstanceSet[models.Ticket]) ([]int64, error) {
				return test.ids, test.failure
			}
			definition, err := NewCollectionFormSet(config)
			if err != nil {
				t.Fatal(err)
			}
			data := collectionSetData()
			bound, err := definition.bind(t.Context(), actor, &data)
			if err != nil {
				t.Fatal(err)
			}
			ids, err := bound.run(t.Context())
			if ids != nil || err == nil || test.failure != nil && !errors.Is(err, test.failure) || test.failure == nil && !errors.Is(err, ErrReconciliationRequired) {
				t.Fatal("published unconfirmed identities", err)
			}
		})
	}
	config, _ := collectionSetFixture(t)
	definition, err := NewCollectionFormSet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareCollectionFormSets([]CollectionFormSet{definition, definition}, definition.model); err == nil {
		t.Fatal("duplicate definition")
	}
	if _, err := prepareCollectionFormSets([]CollectionFormSet{definition}, models.CategoryDescriptor{}.Metadata()); err == nil {
		t.Fatal("foreign model accepted")
	}
}

func TestCollectionFormSetRedisplaysOnlyDirectOwnedDiagnostics(t *testing.T) {
	config, actor := collectionSetFixture(t)
	definition, err := NewCollectionFormSet(config)
	if err != nil {
		t.Fatal(err)
	}
	data := collectionSetData()
	bound, err := definition.bind(t.Context(), actor, &data)
	if err != nil {
		t.Fatal(err)
	}
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
