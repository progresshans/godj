package model_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/conformance/relationfixture/authors"
	"github.com/progresshans/godj/conformance/relationfixture/blog"
	helpdesk "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

type defaultChoicePostDescriptor struct {
	blog.PostDescriptor
	model ir.Model
}

func (descriptor defaultChoicePostDescriptor) Metadata() ir.Model { return descriptor.model.Clone() }

func defaultChoicePostModel() ir.Model {
	model := (blog.PostDescriptor{}).Metadata()
	for index := range model.Fields {
		if model.Fields[index].Name == "reviewer" {
			model.Fields[index].Blank = true
			model.Fields[index].Default = &ir.Scalar{Kind: ir.ScalarInteger, Integer: 2}
		}
	}
	return model
}

func reviewerSpec(t *testing.T, model ir.Model, required bool, choices ...forms.Choice) forms.Spec {
	t.Helper()
	spec, err := formmodel.NewSpecForFields(model, []string{"reviewer"}, formmodel.OverrideField("reviewer", formmodel.WithRequired(required)))
	if err == nil {
		spec, err = spec.WithModelChoices("reviewer", choices...)
	}
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestOmittedDefaultModelChoiceRequiresCurrentMembership(t *testing.T) {
	model := defaultChoicePostModel()
	for _, snapshot := range []struct {
		name  string
		value forms.Value
	}{{"positive", forms.Integer(7)}, {"zero", forms.Integer(0)}, {"negative", forms.Integer(-4)}, {"null", forms.Null()}} {
		current := snapshot.value
		for _, existing := range []bool{false, true} {
			for _, authorized := range []bool{false, true} {
				t.Run(fmt.Sprintf("existing_%t/current_%s/authorized_%t", existing, snapshot.name, authorized), func(t *testing.T) {
					retained := forms.Integer(2)
					var initial map[string]forms.Value
					if existing {
						retained = current
						initial = map[string]forms.Value{"reviewer": current}
					}
					var choices []forms.Choice
					if authorized && !retained.IsNull() {
						choices = []forms.Choice{{Value: retained, Label: "Authorized current"}}
					}
					calls := 0
					bound, err := formmodel.Bind(t.Context(), model, reviewerSpec(t, model, false, choices...), forms.NewData(nil), initial, formmodel.PostClean{Clean: func(candidate forms.Values) (forms.Values, validation.Errors) {
						calls++
						if value, _ := candidate.Get("reviewer"); !value.Equal(retained) {
							t.Fatal("choice rejection discarded the retained candidate")
						}
						return forms.Values{}, validation.Errors{}
					}})
					valid := authorized || retained.IsNull()
					if err != nil || bound.Form().Valid() != valid || calls != 1 {
						t.Fatal("retained relation bypassed selection or skipped model clean", err, calls)
					}
					if value, _ := bound.Candidate().Get("reviewer"); !value.Equal(retained) {
						t.Fatal("retained candidate changed")
					}
					if _, supplied := bound.Form().Submitted().Get("reviewer"); supplied {
						t.Fatal("default lookup manufactured raw form input")
					}
					input, inputErr := bound.Input()
					if valid {
						value, present := input.Get("reviewer")
						cleaned, cleanedPresent := bound.Form().Cleaned().Get("reviewer")
						if inputErr != nil || !present || !value.Equal(retained) || !cleanedPresent || !cleaned.IsNull() {
							t.Fatal("authorized candidate/default changed cleaned data", inputErr)
						}
					} else if inputErr == nil || !slices.Equal(modelSetCodes(bound.Form().Errors())["reviewer"], []string{"invalid_choice"}) {
						t.Fatal("unauthorized retained default became writable", inputErr)
					}
				})
			}
		}
	}
}

func TestDefaultModelChoiceKeepsExplicitEmptyRequiredExcludedAndPostCleanSemantics(t *testing.T) {
	model := defaultChoicePostModel()
	for _, raw := range [][]string{nil, {""}} {
		bound, err := formmodel.Bind(t.Context(), model, reviewerSpec(t, model, false), forms.NewData(map[string][]string{"reviewer": raw}), map[string]forms.Value{"reviewer": forms.Integer(7)}, formmodel.PostClean{})
		value, present := bound.Candidate().Get("reviewer")
		if err != nil || !bound.Form().Valid() || !present || !value.IsNull() {
			t.Fatal("present empty relation retained current/default", err)
		}
	}
	required, err := formmodel.Bind(t.Context(), model, reviewerSpec(t, model, true), forms.NewData(nil), nil, formmodel.PostClean{})
	if err != nil || required.Form().Valid() || !slices.Equal(modelSetCodes(required.Form().Errors())["reviewer"], []string{"required"}) {
		t.Fatal("required omission became a default membership check", err)
	}
	withoutDefault := model.Clone()
	for index := range withoutDefault.Fields {
		if withoutDefault.Fields[index].Name == "reviewer" {
			withoutDefault.Fields[index].Default = nil
		}
	}
	ordinary, err := formmodel.Bind(t.Context(), withoutDefault, reviewerSpec(t, withoutDefault, false), forms.NewData(nil), map[string]forms.Value{"reviewer": forms.Integer(7)}, formmodel.PostClean{})
	if value, _ := ordinary.Candidate().Get("reviewer"); err != nil || !ordinary.Form().Valid() || !value.IsNull() {
		t.Fatal("non-defaulted omission acquired current key preservation", err)
	}
	excludedSpec, err := formmodel.NewSpecForFields(model, []string{"title"})
	if err != nil {
		t.Fatal(err)
	}
	excluded, err := formmodel.Bind(t.Context(), model, excludedSpec, forms.NewData(map[string][]string{"title": {"New"}}), nil, formmodel.PostClean{})
	if err != nil || !excluded.Form().Valid() {
		t.Fatal("excluded default relation required choices", err)
	}
	if input, err := excluded.Input(); err != nil {
		t.Fatal(err)
	} else if _, selected := input.Get("reviewer"); selected {
		t.Fatal("excluded default relation became selected input")
	}
	for _, mode := range []string{"explicit_allowed", "retained_allowed", "retained_denied"} {
		data := map[string][]string{}
		key, replacement := int64(2), int64(99)
		if mode == "explicit_allowed" {
			data["reviewer"], key = []string{"1"}, 1
		}
		choices := []forms.Choice{{Value: forms.Integer(key), Label: "Allowed"}}
		if mode == "retained_denied" {
			choices, replacement = nil, 1
		}
		calls := 0
		bound, err := formmodel.Bind(t.Context(), model, reviewerSpec(t, model, false, choices...), forms.NewData(data), nil, formmodel.PostClean{Fields: []string{"reviewer"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
			calls++
			return forms.NewValues(map[string]forms.Value{"reviewer": forms.Integer(replacement)}), validation.Errors{}
		}})
		value, _ := bound.Candidate().Integer("reviewer")
		if err != nil || calls != 1 || value != replacement || bound.Form().Valid() != (mode != "retained_denied") {
			t.Fatal("model clean was revalidated, skipped or erased earlier membership failure", mode, err, calls)
		}
	}
}

func defaultChoicePostSet(t *testing.T, readonly bool, clean forms.CrossValidator) (orm.Manager[blog.Post], forms.SetSpec) {
	t.Helper()
	manager := orm.NewManager[blog.Post](defaultChoicePostDescriptor{model: defaultChoicePostModel()})
	// A manual display default must not replace the Schema IR candidate default.
	field, err := forms.ModelChoiceField("reviewer", forms.WithRequired(false), forms.WithNullable(), forms.WithDefault(forms.Integer(99)))
	if err != nil {
		t.Fatal(err)
	}
	title, err := forms.CharField("title")
	if err != nil {
		t.Fatal(err)
	}
	var validators []forms.CrossValidator
	if clean != nil {
		validators = []forms.CrossValidator{clean}
	}
	row, err := forms.NewSpec([]forms.Field{title, field}, validators...)
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix, config.ExtraForms, config.MaxForms, config.AbsoluteMax = "items", 0, 4, 5
	config.ReadOnlyInitial, config.CanDelete = readonly, true
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	return manager, spec
}

func defaultChoicePostRows() ([]blog.Post, map[string][]string) {
	one, two, three := blog.NewPostWithID(10), blog.NewPostWithID(11), blog.NewPostWithID(12)
	one.Title, two.Title, three.Title = "One", "Two", "Three"
	one.AuthorID, two.AuthorID, three.AuthorID = 5, 5, 5
	one.ReviewerID, two.ReviewerID = new(int64(-4)), new(int64(0))
	return []blog.Post{one, two, three}, map[string][]string{
		"items-TOTAL_FORMS": {"4"}, "items-INITIAL_FORMS": {"3"},
		"items-0-id": {"11"}, "items-0-title": {"Two changed"},
		"items-1-id": {"10"}, "items-1-title": {"One changed"},
		"items-2-id": {"12"}, "items-2-title": {"Three changed"},
		"items-3-title": {"New"},
	}
}

func TestPreflightChoiceKeysUseRetainedCurrentAndIRDefaults(t *testing.T) {
	fieldCalls, modelCalls := 0, 0
	manager, spec := defaultChoicePostSet(t, false, forms.CrossValidatorFunc(func(forms.Values) validation.Errors {
		fieldCalls++
		return validation.Errors{}
	}))
	current, data := defaultChoicePostRows()
	input, err := formmodel.PreflightSet(t.Context(), manager, spec, forms.NewData(data), current, formmodel.PostClean{Clean: func(forms.Values) (forms.Values, validation.Errors) {
		modelCalls++
		return forms.Values{}, validation.Errors{}
	}})
	if err != nil || !input.AdmissionValid() {
		t.Fatal(err)
	}
	*current[0].ReviewerID = 444
	snapshots := map[int]map[string][]forms.Choice{}
	for index, want := range [][]int64{{0}, {-4}, nil, {2}} {
		keys, valid, err := input.ChoiceKeys(index, "reviewer")
		if err != nil || !valid || !slices.Equal(keys, want) || fieldCalls != 0 || modelCalls != 0 {
			t.Fatal("preflight used a display default, stale index or validator", index, keys, err)
		}
		choices := make([]forms.Choice, len(keys))
		for offset, key := range keys {
			choices[offset] = forms.Choice{Value: forms.Integer(key), Label: "Resolved current"}
		}
		snapshots[index] = map[string][]forms.Choice{"reviewer": choices}
	}
	bound, err := input.Bind(t.Context(), forms.NewRowModelChoices(snapshots, nil))
	if err != nil || !bound.Valid() || fieldCalls != 4 || modelCalls != 4 {
		t.Fatal("resolved retained defaults failed or repeated validation", err, fieldCalls, modelCalls)
	}
	for index, want := range []forms.Value{forms.Integer(0), forms.Integer(-4), forms.Null(), forms.Integer(2)} {
		instance, present := bound.Instance(index)
		value, _ := instance.BoundForm().Candidate().Get("reviewer")
		if !present || !value.Equal(want) {
			t.Fatal("final candidate differs from resolved key", index)
		}
	}
	denied, err := input.Bind(t.Context(), forms.RowModelChoices{})
	if err != nil || denied.Valid() || !denied.FormSet().Forms()[2].Form().Valid() {
		t.Fatal("closed default scopes bypassed membership or rejected retained NULL", err)
	}
}

func TestPreflightChoiceKeysRespectPresenceRequiredReadonlyAndAdmission(t *testing.T) {
	for _, mode := range []string{"explicit_empty", "present_empty_slice", "required", "malformed", "readonly", "foreign_pk"} {
		t.Run(mode, func(t *testing.T) {
			manager, spec := defaultChoicePostSet(t, mode == "readonly", nil)
			current, data := defaultChoicePostRows()
			want, valid := []int64{0}, true
			switch mode {
			case "explicit_empty":
				data["items-0-reviewer"], want = []string{""}, nil
			case "present_empty_slice":
				data["items-0-reviewer"], want = []string{}, nil
			case "required":
				field, err := forms.ModelChoiceField("reviewer", forms.WithNullable())
				if err == nil {
					spec, err = spec.WithFormField(field)
				}
				if err != nil {
					t.Fatal(err)
				}
				want = nil
			case "malformed":
				data["items-0-reviewer"], want, valid = []string{"not a key"}, nil, false
			case "readonly":
				data["items-0-reviewer"] = []string{"999"}
			case "foreign_pk":
				data["items-0-id"] = []string{"999"}
			}
			input, err := formmodel.PreflightSet(t.Context(), manager, spec, forms.NewData(data), current, formmodel.PostClean{})
			if err != nil {
				t.Fatal(err)
			}
			keys, parsed, err := input.ChoiceKeys(0, "reviewer")
			if mode == "foreign_pk" {
				if err == nil || parsed || len(keys) != 0 {
					t.Fatal("invalid identity exposed relation lookup scope")
				}
				return
			}
			if err != nil || parsed != valid || !slices.Equal(keys, want) {
				t.Fatal("choice lookup changed omission/read-only semantics", keys, parsed, err)
			}
			for _, field := range []string{"unknown", "title", "id", "author"} {
				if _, _, err := input.ChoiceKeys(0, field); err == nil {
					t.Fatal("non-selected relation supplied lookup keys", field)
				}
			}
			for _, index := range []int{-1, input.TotalForms()} {
				if _, _, err := input.ChoiceKeys(index, "reviewer"); err == nil {
					t.Fatal("out-of-scope row supplied lookup keys")
				}
			}
		})
	}
}

func TestInlinePreflightChoiceKeysPreserveServerParentOwnership(t *testing.T) {
	manager, rows := defaultChoicePostSet(t, false, nil)
	app := blog.GoDjRelationSchema()
	for index := range app.Models {
		if app.Models[index].Name == "post" {
			app.Models[index] = defaultChoicePostModel()
		}
	}
	project, err := orm.BindProject(authors.GoDjRelationSchema(), app)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := formmodel.NewInlineSpec(project, authors.AuthorObjects, manager, "author", rows)
	if err != nil {
		t.Fatal(err)
	}
	current, data := defaultChoicePostRows()
	input, err := spec.Preflight(t.Context(), forms.NewData(data), authors.NewAuthorWithID(5), current, formmodel.PostClean{})
	if err != nil || !input.AdmissionValid() {
		t.Fatal(err)
	}
	if keys, valid, err := input.ChoiceKeys(3, "reviewer"); err != nil || !valid || !slices.Equal(keys, []int64{2}) {
		t.Fatal("inline lost pending child model default", keys, err)
	}
	if _, _, err := input.ChoiceKeys(0, "author"); err == nil {
		t.Fatal("server-owned parent became a selected relation")
	}
}

func TestPreflightChoiceKeysUseReadonlyManyToManyInitial(t *testing.T) {
	for _, readonly := range []bool{false, true} {
		row, err := formmodel.NewSpecForFields((helpdesk.TicketDescriptor{}).Metadata(), []string{"subject", "labels"}, formmodel.OverrideField("labels", formmodel.WithRequired(false)))
		if err != nil {
			t.Fatal(err)
		}
		config := forms.DefaultSetConfig()
		config.Prefix, config.ReadOnlyInitial = "items", readonly
		spec, err := forms.NewSetSpec(row, config)
		if err != nil {
			t.Fatal(err)
		}
		current := helpdesk.NewTicketWithID(7)
		current.Subject, current.CategoryID = "Current", 1
		input, err := formmodel.PreflightSet(t.Context(), helpdesk.TicketObjects, spec, forms.NewData(map[string][]string{
			"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"1"}, "items-0-id": {"7"}, "items-0-subject": {"Changed"},
		}), []helpdesk.Ticket{current}, formmodel.PostClean{}, func(helpdesk.Ticket, ir.ManyToManyField) ([]int64, bool) { return []int64{1, 2}, true })
		if err != nil {
			t.Fatal(err)
		}
		keys, valid, err := input.ChoiceKeys(0, "labels")
		var want []int64
		if readonly {
			want = []int64{1, 2}
		}
		if err != nil || !valid || !slices.Equal(keys, want) {
			t.Fatal("multiple choice omission retained writable initial or lost readonly selection", readonly, keys, err)
		}
		if len(keys) > 0 {
			keys[0] = 99
			again, _, err := input.ChoiceKeys(0, "labels")
			if err != nil || !reflect.DeepEqual(again, []int64{1, 2}) {
				t.Fatal("lookup key accessor exposed initial collection storage", err)
			}
		}
	}
}
