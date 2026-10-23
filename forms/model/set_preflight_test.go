package model_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	article "github.com/progresshans/godj/examples/article/models"
	helpdesk "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func preflightLabelSpec(t *testing.T, readonly bool, clean forms.CrossValidator, setClean forms.SetValidator) forms.SetSpec {
	t.Helper()
	definition := formmodel.Definition{Fields: []string{"name", "category"}, Overrides: []formmodel.Override{
		formmodel.OverrideField("name", formmodel.WithStringNormalizer(strings.ToUpper)),
	}}
	if clean != nil {
		definition.Validators = []forms.CrossValidator{clean}
	}
	row, err := definition.Spec((helpdesk.LabelDescriptor{}).Metadata())
	if err != nil {
		t.Fatal(err)
	}
	row, err = row.WithModelChoices("category", forms.Choice{Value: forms.Integer(1), Label: "One"}, forms.Choice{Value: forms.Integer(2), Label: "Two"}, forms.Choice{Value: forms.Integer(3), Label: "Three"})
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix, config.ExtraForms, config.MaxForms, config.AbsoluteMax = "items", 0, 3, 4
	config.CanDelete, config.ReadOnlyInitial = true, readonly
	var validators []forms.SetValidator
	if setClean != nil {
		validators = []forms.SetValidator{setClean}
	}
	spec, err := forms.NewSetSpec(row, config, validators...)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func preflightLabelData() map[string][]string {
	return map[string][]string{
		"items-TOTAL_FORMS": {"3"}, "items-INITIAL_FORMS": {"2"},
		"items-0-id": {"+0011"}, "items-0-name": {"two"}, "items-0-category": {"2"},
		"items-1-id": {"10"}, "items-1-name": {"one"}, "items-1-category": {"1"},
		"items-2-name": {"extra"}, "items-2-category": {"3"},
	}
}

func TestModelSetPreflightOwnsReorderedCohortAndRunsValidationOnce(t *testing.T) {
	rows, models, sets := 0, 0, 0
	spec := preflightLabelSpec(t, false, forms.CrossValidatorFunc(func(forms.Values) validation.Errors {
		rows++
		return validation.Errors{}
	}), forms.SetValidatorFunc(func([]forms.SetForm) validation.Errors {
		sets++
		return validation.Errors{}
	}))
	current := inlineLabelCurrent()
	current[1].CategoryID = 2
	postClean := formmodel.PostClean{Clean: func(forms.Values) (forms.Values, validation.Errors) {
		models++
		return forms.Values{}, validation.Errors{}
	}}
	input, err := formmodel.PreflightSet(t.Context(), helpdesk.LabelObjects, spec, forms.NewData(preflightLabelData()), current, postClean)
	if err != nil || !input.AdmissionValid() || rows != 0 || models != 0 || sets != 0 {
		t.Fatal("preflight cleaned model data", err, rows, models, sets)
	}
	current[0].Name, current[1].Name = "caller-one", "caller-two"
	for index, key := range []int64{11, 10} {
		value, present, err := input.Current(index)
		identity, known := input.Identity(index)
		id, _ := identity.AsInteger()
		if err != nil || !present || !known || value.ID != key || id != key || strings.HasPrefix(value.Name, "caller") {
			t.Fatal("preflight resolved by original index or retained caller state", index, err, value)
		}
	}
	if _, present, err := input.Current(2); err != nil || present {
		t.Fatal("extra row acquired a current identity", err)
	}
	if readonly, err := input.ReadOnly(2); err != nil || readonly {
		t.Fatal("extra row lost its writable shape", err)
	}
	name, err := input.IdentityName(0)
	if err != nil || name != "items-0-id" {
		t.Fatal("identity input name lost", err)
	}
	choices := forms.NewRowModelChoices(map[int]map[string][]forms.Choice{
		0: {"category": {{Value: forms.Integer(2), Label: "Current two"}}},
		1: {"category": {{Value: forms.Integer(1), Label: "Current one"}}},
		2: {"category": {{Value: forms.Integer(3), Label: "Current extra"}}},
	}, nil)
	bound, err := input.Bind(t.Context(), choices)
	if err != nil || !bound.Valid() || rows != 3 || models != 3 || sets != 1 {
		t.Fatal("model continuation did not use one validation lifecycle", err, rows, models, sets)
	}
	prepared, err := bound.Prepare()
	if err != nil || len(prepared.Rows()) != 3 {
		t.Fatal("preflight result could not be prepared", err)
	}
	for index, row := range prepared.Rows() {
		value, err := row.Model()
		if err != nil || value.Name != []string{"TWO", "ONE", "EXTRA"}[index] || value.CategoryID != []int64{2, 1, 3}[index] {
			t.Fatal("typed candidate used another row's scope", index, err, value)
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", input), "Current") {
		t.Fatal("preflight diagnostic exposed a snapshot")
	}
}

func TestModelSetPreflightRejectsCohortBeforeResolutionIncludingDeletedRows(t *testing.T) {
	spec := preflightLabelSpec(t, false, nil, nil)
	current := inlineLabelCurrent()
	for _, mode := range []string{"foreign", "duplicate", "missing", "repeated", "extra identity", "management", "short total", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			data := preflightLabelData()
			data["items-0-DELETE"], data["items-1-DELETE"], data["items-2-DELETE"] = []string{"on"}, []string{"on"}, []string{"on"}
			switch mode {
			case "foreign":
				data["items-0-id"] = []string{"99"}
			case "duplicate":
				data["items-0-id"] = []string{"10"}
			case "missing":
				delete(data, "items-0-id")
			case "repeated":
				data["items-0-id"] = []string{"11", "11"}
			case "extra identity":
				data["items-2-id"] = []string{"10"}
			case "management":
				data["items-INITIAL_FORMS"] = []string{"0"}
			case "short total":
				data["items-TOTAL_FORMS"] = []string{"1"}
			case "overflow":
				data["items-TOTAL_FORMS"] = []string{"5"}
			}
			input, err := formmodel.PreflightSet(t.Context(), helpdesk.LabelObjects, spec, forms.NewData(data), current, formmodel.PostClean{})
			if err != nil || input.AdmissionValid() {
				t.Fatal("malformed cohort admitted before relation I/O", err)
			}
			if _, _, err := input.Current(0); err == nil {
				t.Fatal("invalid preflight exposed a fallback current row")
			}
			if _, known := input.Identity(0); known {
				t.Fatal("invalid preflight exposed a trusted identity")
			}
			bound, err := input.Bind(t.Context(), forms.RowModelChoices{})
			if err != nil || bound.Valid() || bound.FormSet().NonFormErrors().Empty() {
				t.Fatal("deleted rows hid admission failure", err)
			}
		})
	}
}

func TestModelSetPreflightReadonlyRowsIgnoreSubmittedRelationScope(t *testing.T) {
	spec := preflightLabelSpec(t, true, nil, nil)
	data := preflightLabelData()
	data["items-0-category"], data["items-1-category"] = []string{"999"}, []string{"999"}
	current := inlineLabelCurrent()
	input, err := formmodel.PreflightSet(t.Context(), helpdesk.LabelObjects, spec, forms.NewData(data), current, formmodel.PostClean{})
	if err != nil || !input.AdmissionValid() {
		t.Fatal(err)
	}
	for index, expected := range []bool{true, true, false} {
		readonly, err := input.ReadOnly(index)
		if err != nil || readonly != expected {
			t.Fatal("read-only policy mismatch", index, err)
		}
	}
	bound, err := input.Bind(t.Context(), forms.NewRowModelChoices(map[int]map[string][]forms.Choice{2: {"category": {{Value: forms.Integer(3), Label: "Extra"}}}}, nil))
	if err != nil || !bound.Valid() {
		t.Fatal("readonly forged values required resolution", err)
	}
	prepared, err := bound.Prepare()
	if err != nil || len(prepared.Rows()) != 1 || prepared.Rows()[0].Existing() {
		t.Fatal("readonly current row became writable", err)
	}
}

func TestInlinePreflightChecksParentBeforeResolutionAndKeepsPendingKey(t *testing.T) {
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	rows := inlineRows(t, (helpdesk.LabelDescriptor{}).Metadata(), []string{"name"})
	spec, err := formmodel.NewInlineSpec(binding, helpdesk.CategoryObjects, helpdesk.LabelObjects, "category", rows)
	if err != nil {
		t.Fatal(err)
	}
	data := preflightLabelData()
	data["items-0-category"], data["items-1-category"], data["items-2-category"] = []string{"1"}, []string{"1"}, []string{"1"}
	for _, mode := range []string{"valid", "deleted foreign parent", "extra foreign parent", "repeated parent"} {
		t.Run(mode, func(t *testing.T) {
			copied := map[string][]string{}
			for name, raw := range data {
				copied[name] = append([]string(nil), raw...)
			}
			switch mode {
			case "deleted foreign parent":
				copied["items-0-category"], copied["items-0-DELETE"] = []string{"9"}, []string{"on"}
			case "extra foreign parent":
				copied["items-2-category"] = []string{"9"}
			case "repeated parent":
				copied["items-0-category"] = []string{"1", "1"}
			}
			input, err := spec.Preflight(t.Context(), forms.NewData(copied), helpdesk.NewCategoryWithID(1), inlineLabelCurrent(), formmodel.PostClean{})
			if err != nil || input.AdmissionValid() != (mode == "valid") {
				t.Fatal("parent admission mismatch", err)
			}
			bound, err := input.Bind(t.Context(), forms.RowModelChoices{})
			if err != nil || bound.Valid() != (mode == "valid") {
				t.Fatal("parent admission did not survive final binding", err)
			}
		})
	}
	newData := forms.NewData(map[string][]string{"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"0"}, "items-0-name": {"new"}})
	input, err := spec.Preflight(t.Context(), newData, helpdesk.Category{}, nil, formmodel.PostClean{})
	if err != nil || !input.AdmissionValid() || !input.ParentIdentity().IsNull() {
		t.Fatal("pending parent preflight", err)
	}
	if _, present, err := input.Current(0); err != nil || present {
		t.Fatal("new parent's child acquired a current value", err)
	}
	if _, err := input.Bind(t.Context(), forms.NewRowModelChoices(map[int]map[string][]forms.Choice{0: {"category": nil}}, nil)); err == nil {
		t.Fatal("per-row choice snapshot changed server-owned parent")
	}
	bound, err := input.Bind(t.Context(), forms.RowModelChoices{})
	if err != nil || !bound.Valid() {
		t.Fatal(err)
	}
	if _, err := bound.Prepare(); err == nil {
		t.Fatal("unsaved parent became preparable")
	}
	prepared, err := bound.PrepareWithParent(helpdesk.NewCategoryWithID(7))
	if err != nil || len(prepared.Rows()) != 1 {
		t.Fatal("pending key handoff failed", err)
	}
	value, err := prepared.Rows()[0].Model()
	if err != nil || value.CategoryID != 7 {
		t.Fatal("saved parent key lost", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := input.Bind(ctx, forms.RowModelChoices{}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled final binding accepted", err)
	}
}

func TestModelSetPreflightCurrentCopiesAndConcurrentContinuation(t *testing.T) {
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	spec := articleSetSpec(t, config)
	current := setArticles()
	input, err := formmodel.PreflightSet(t.Context(), article.ArticleObjects, spec, modelSetData(map[string]string{"id": "2", "title": "two"}, map[string]string{"id": "1", "title": "one"}), current, formmodel.PostClean{})
	if err != nil {
		t.Fatal(err)
	}
	*current[0].Summary = "caller"
	value, _, err := input.Current(1)
	if err != nil || value.Summary == nil || *value.Summary != "stored-one" {
		t.Fatal("preflight retained a caller pointer", err)
	}
	*value.Summary = "getter"
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			bound, err := input.Bind(t.Context(), forms.RowModelChoices{})
			if err != nil || !bound.Valid() {
				t.Error("shared preflight could not bind independently", err)
				return
			}
			current, _, err := bound.Current(1)
			if err != nil || current.Summary == nil || *current.Summary != "stored-one" {
				t.Error("snapshot leaked across continuations", err)
			}
		})
	}
	workers.Wait()
}

func TestModelSetPreflightCapturesRelatedInitialBeforeChoiceResolution(t *testing.T) {
	definition := formmodel.Definition{Fields: []string{"subject", "labels"}}
	row, err := definition.Spec((helpdesk.TicketDescriptor{}).Metadata())
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix, config.MaxForms, config.AbsoluteMax = "items", 2, 3
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	one, two := helpdesk.NewTicketWithID(7), helpdesk.NewTicketWithID(8)
	one.CategoryID, two.CategoryID = 1, 1
	one.Subject, two.Subject = "One", "Two"
	keys := map[int64][]int64{7: {1}, 8: {2}}
	reads := 0
	input, err := formmodel.PreflightSet(t.Context(), helpdesk.TicketObjects, spec, forms.NewData(map[string][]string{
		"items-TOTAL_FORMS": {"2"}, "items-INITIAL_FORMS": {"2"},
		"items-0-id": {"8"}, "items-0-subject": {"Two changed"}, "items-0-labels": {"2"},
		"items-1-id": {"7"}, "items-1-subject": {"One changed"}, "items-1-labels": {"1"},
	}), []helpdesk.Ticket{one, two}, formmodel.PostClean{}, func(current helpdesk.Ticket, field ir.ManyToManyField) ([]int64, bool) {
		reads++
		return keys[current.ID], field.Name == "labels"
	})
	if err != nil || !input.AdmissionValid() || reads != 2 {
		t.Fatal("related snapshot preflight failed", err, reads)
	}
	keys[7][0], keys[8][0] = 91, 92
	bound, err := input.Bind(t.Context(), forms.NewRowModelChoices(map[int]map[string][]forms.Choice{
		0: {"labels": {{Value: forms.Integer(2), Label: "Row eight"}}},
		1: {"labels": {{Value: forms.Integer(1), Label: "Row seven"}}},
	}, nil))
	if err != nil || !bound.Valid() || reads != 2 {
		t.Fatal("binding repeated relation reading or changed scopes", err, reads)
	}
	for index, row := range bound.FormSet().Forms() {
		initial, found := row.Form().Initial().Integers("labels")
		if !found || len(initial) != 1 || initial[0] != []int64{2, 1}[index] {
			t.Fatal("related initial snapshot retained the reader's slice", index, initial)
		}
	}
}
