package model_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	helpdesk "github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
	"github.com/progresshans/godj/validation"
)

type uniqueSetObservation struct {
	Name, Model string
	Fields      []string
	Data        map[string]string
	Options     map[string]json.RawMessage
	MaxNum      int `json:"max_num"`
	Valid       bool
	Forms       []struct {
		Valid         bool
		CleanedFields []string             `json:"cleaned_fields"`
		Errors        map[string][]*string `json:"errors"`
	}
	NonFormErrors []*string `json:"non_form_errors"`
	CleanCalls    int       `json:"clean_calls"`
}

func TestInstanceSetUniqueAgainstPinnedDjango(t *testing.T) {
	raw, err := os.ReadFile("testdata/formset-unique-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, Python, Backend string
		Source                  string `json:"source_sha256"`
		Unchanged               bool   `json:"storage_unchanged"`
		Cleanup                 bool   `json:"cleanup_empty"`
		Cases                   []uniqueSetObservation
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != "sqlite" ||
		reference.Source != "858772e26a8e1d023782b410d3de67b9fc73dfb98120683fdf14584c0d5b0910" ||
		!reference.Unchanged || !reference.Cleanup || len(reference.Cases) != 23 {
		t.Fatal("unexpected native model formset unique reference")
	}
	for _, observed := range reference.Cases {
		t.Run(observed.Name, func(t *testing.T) {
			switch observed.Model {
			case "ticket":
				compareSetUnique(t, helpdesk.TicketObjects, observed)
			case "label":
				compareSetUnique(t, helpdesk.LabelObjects, observed)
			default:
				t.Fatal("unknown native model")
			}
		})
	}
}

func compareSetUnique[M any](t *testing.T, manager orm.Manager[M], observed uniqueSetObservation) {
	t.Helper()
	metadata, err := manager.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	row, err := formmodel.NewSpecForFields(metadata, observed.Fields)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(observed.Fields, "category") {
		row, err = row.WithModelChoices("category", forms.Choice{Value: forms.Integer(1), Label: "one"}, forms.Choice{Value: forms.Integer(2), Label: "two"}, forms.Choice{Value: forms.Integer(12), Label: "twelve"})
		if err != nil {
			t.Fatal(err)
		}
	}
	config := forms.DefaultSetConfig()
	config.Prefix, config.CanDelete, config.MaxForms, config.AbsoluteMax = "items", true, observed.MaxNum, 12
	for name, raw := range observed.Options {
		var target any
		switch name {
		case "min_num":
			target = &config.MinForms
		case "validate_min":
			target = &config.ValidateMin
		case "validate_max":
			target = &config.ValidateMax
		default:
			t.Fatal("unexpected native option", name)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatal(err)
		}
	}
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string][]string{}
	for name, value := range observed.Data {
		values[name] = []string{value}
	}
	cleanCalls := 0
	post := formmodel.PostClean{Clean: func(candidate forms.Values) (forms.Values, validation.Errors) {
		cleanCalls++
		subject, _ := candidate.String("subject")
		if strings.HasPrefix(subject, "same-candidate") {
			key, err := uuid.Parse("12345678-1234-4234-8234-123456789003")
			if err != nil {
				t.Fatal(err)
			}
			return forms.NewValues(map[string]forms.Value{"external_reference": forms.UUID(key)}), validation.Errors{}
		}
		return forms.Values{}, validation.Errors{}
	}}
	if observed.Model == "ticket" {
		post.Fields = []string{"external_reference"}
	}
	bound, err := formmodel.BindSet(manager, spec, forms.NewData(values), nil, post)
	if err != nil {
		t.Fatal(err)
	}
	set := bound.FormSet()
	if set.Valid() != observed.Valid || set.TotalForms() != len(observed.Forms) || cleanCalls != observed.CleanCalls {
		t.Fatal("native validity/count/clean calls differ", set.Valid(), cleanCalls)
	}
	wantNonForm := map[string][]string{}
	if len(observed.NonFormErrors) > 0 {
		wantNonForm[string(validation.NonField)] = nativeUniqueCodes(observed.NonFormErrors)
	}
	if got := modelSetCodes(set.NonFormErrors()); !reflect.DeepEqual(got, wantNonForm) {
		t.Fatal("native non-form errors differ", got, wantNonForm)
	}
	for index, row := range set.Forms() {
		want := observed.Forms[index]
		wantErrors := map[string][]string{}
		for field, codes := range want.Errors {
			wantErrors[field] = nativeUniqueCodes(codes)
		}
		if row.Form().Valid() != want.Valid || !reflect.DeepEqual(modelSetCodes(row.Form().Errors()), wantErrors) {
			t.Fatal("native row errors differ", index, row.Form().Errors().All(), wantErrors)
		}
		gotFields := []string{}
		for _, field := range row.Form().Cleaned().All() {
			gotFields = append(gotFields, field.Name())
		}
		slices.Sort(gotFields)
		wantFields := slices.DeleteFunc(slices.Clone(want.CleanedFields), func(name string) bool { return name == "id" })
		if !slices.Equal(gotFields, wantFields) {
			t.Fatal("rejected tuple remains in cleaned data", index, gotFields, wantFields)
		}
		if instance, evaluated := bound.Instance(index); evaluated {
			if !reflect.DeepEqual(modelSetCodes(instance.BoundForm().Form().Errors()), wantErrors) ||
				!reflect.DeepEqual(instance.BoundForm().Form().Cleaned().All(), row.Form().Cleaned().All()) {
				t.Fatal("typed/core row rejection diverged")
			}
			for field := range wantErrors {
				if field == string(validation.NonField) {
					if _, err := instance.Prepare(); err == nil {
						t.Fatal("duplicate row could be prepared independently")
					}
				}
			}
		}
	}
	if !bound.Valid() {
		if _, err := bound.Prepare(); err == nil {
			t.Fatal("invalid set prepared")
		}
	}
	if cleanCalls != observed.CleanCalls {
		t.Fatal("inspection/preparation repeated clean")
	}
}

func nativeUniqueCodes(codes []*string) []string {
	result := make([]string, len(codes))
	for index, code := range codes {
		result[index] = "unique" // Django's cross-row messages have no machine code.
		if code != nil {
			result[index] = *code
		}
	}
	return result
}

type overlapLabelDescriptor struct{ helpdesk.LabelDescriptor }

func (overlapLabelDescriptor) Metadata() ir.Model {
	metadata := (helpdesk.LabelDescriptor{}).Metadata()
	for index := range metadata.Fields {
		if metadata.Fields[index].Name == "name" {
			metadata.Fields[index].Unique = true
		}
	}
	return metadata
}

func TestInstanceSetUniqueKeepsCompleteOverlappingTuplesAndOwnsErrors(t *testing.T) {
	manager := orm.NewManager[helpdesk.Label](overlapLabelDescriptor{})
	metadata, err := manager.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	row, err := formmodel.NewSpecForFields(metadata, []string{"category", "name"})
	if err != nil {
		t.Fatal(err)
	}
	row, err = row.WithModelChoices("category", forms.Choice{Value: forms.Integer(1), Label: "one"}, forms.Choice{Value: forms.Integer(2), Label: "two"})
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	customCalls := 0
	spec, err := forms.NewSetSpec(row, config, forms.SetValidatorFunc(func(rows []forms.SetForm) validation.Errors {
		customCalls++
		if rows[2].Form().Valid() || rows[3].Form().Valid() {
			t.Fatal("custom set validation ran before model uniqueness")
		}
		return validation.NewErrors(validation.New(validation.NonField, "custom_set"))
	}))
	if err != nil {
		t.Fatal(err)
	}
	data := map[string][]string{"items-TOTAL_FORMS": {"4"}, "items-INITIAL_FORMS": {"0"}}
	for index, name := range []string{"one-secret", "two-secret", "one-secret", "two-secret"} {
		data[fmt.Sprintf("items-%d-name", index)] = []string{name}
		data[fmt.Sprintf("items-%d-category", index)] = []string{strconv.Itoa(1 + index/2)}
	}
	bound, err := formmodel.BindSet(manager, spec, forms.NewData(data), nil, formmodel.PostClean{})
	if err != nil || bound.Valid() || customCalls != 1 {
		t.Fatal("expected overlap rejection", err)
	}
	got := modelSetCodes(bound.FormSet().NonFormErrors())[string(validation.NonField)]
	if !slices.Equal(got, []string{"unique", "unique", "custom_set"}) {
		t.Fatal("partial tuple produced a false collision or lost model diagnostics", got)
	}
	for _, index := range []int{2, 3} {
		instance, _ := bound.Instance(index)
		if _, present := instance.BoundForm().Form().Cleaned().Get("category"); !present {
			t.Fatal("unrelated member rejected by partial tuple comparison")
		}
		if name, _ := instance.BoundForm().Candidate().String("name"); !strings.Contains(name, "secret") {
			t.Fatal("candidate was erased by cleaned-data rejection")
		}
	}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(verb, bound), "secret") || strings.Contains(fmt.Sprintf(verb, bound.FormSet().NonFormErrors()), "secret") {
			t.Fatal("duplicate input leaked into diagnostics")
		}
	}
}

func TestInstanceSetUniqueConcurrentBinding(t *testing.T) {
	row, err := formmodel.NewSpecForFields((helpdesk.TicketDescriptor{}).Metadata(), []string{"subject", "external_reference"})
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for index := 0; index < 16; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			data := map[string][]string{"items-TOTAL_FORMS": {"2"}, "items-INITIAL_FORMS": {"0"}, "items-0-subject": {"one"}, "items-1-subject": {"two"}, "items-0-external_reference": {"12345678-1234-4234-8234-123456789001"}, "items-1-external_reference": {"12345678-1234-4234-8234-123456789001"}}
			bound, err := formmodel.BindSet(helpdesk.TicketObjects, spec, forms.NewData(data), nil, formmodel.PostClean{})
			if err != nil || bound.Valid() || bound.FormSet().NonFormErrors().Len() != 1 {
				t.Error("concurrent binding lost duplicate rejection", err)
			}
		}()
	}
	workers.Wait()
}
