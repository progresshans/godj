package forms_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func scopedChoiceSet(t *testing.T, config forms.SetConfig, rowClean forms.CrossValidator, setClean forms.SetValidator) forms.SetSpec {
	t.Helper()
	owner, err := forms.ModelChoiceField("owner", forms.WithChoices(
		forms.Choice{Value: forms.Integer(1), Label: "Global one"},
		forms.Choice{Value: forms.Integer(2), Label: "Global two"},
	))
	if err != nil {
		t.Fatal(err)
	}
	title, err := forms.CharField("title", forms.WithStringNormalizer(strings.ToUpper))
	if err != nil {
		t.Fatal(err)
	}
	var cross []forms.CrossValidator
	if rowClean != nil {
		cross = append(cross, rowClean)
	}
	row, err := forms.NewSpec([]forms.Field{title, owner}, cross...)
	if err != nil {
		t.Fatal(err)
	}
	var validators []forms.SetValidator
	if setClean != nil {
		validators = append(validators, setClean)
	}
	spec, err := forms.NewSetSpec(row, config, validators...)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func scopedChoicePolicy() forms.SetConfig {
	config := forms.DefaultSetConfig()
	config.Prefix, config.ExtraForms, config.MaxForms, config.AbsoluteMax = "items", 0, 3, 4
	config.CanDelete, config.ValidateMax = true, true
	return config
}

func TestSetPreflightDefersValidationAndOwnsPerRowChoices(t *testing.T) {
	rows, sets, processed := 0, 0, 0
	spec := scopedChoiceSet(t, scopedChoicePolicy(), forms.CrossValidatorFunc(func(forms.Values) validation.Errors {
		rows++
		return validation.Errors{}
	}), forms.SetValidatorFunc(func([]forms.SetForm) validation.Errors {
		sets++
		return validation.Errors{}
	}))
	data := setData(2, 2, map[string]string{"title": "one", "owner": "2"}, map[string]string{"title": "two", "owner": "1"})
	input, err := spec.Preflight(t.Context(), data, 2)
	if err != nil || !input.AdmissionValid() || input.TotalForms() != 2 || input.InitialForms() != 2 || rows != 0 || sets != 0 {
		t.Fatal("preflight ran row validation or lost counts", err, rows, sets)
	}
	supplied := map[int]map[string][]forms.Choice{
		0: {"owner": {{Value: forms.Integer(2), Label: "Row zero"}}},
		1: {"owner": {{Value: forms.Integer(1), Label: "Row one"}}},
	}
	choices := forms.NewRowModelChoices(supplied, nil)
	supplied[0]["owner"][0].Value = forms.Integer(99)
	delete(supplied, 1)
	initial := []map[string]forms.Value{{"title": forms.String("old one")}, {"title": forms.String("old two")}}
	bound, err := input.BindWith(t.Context(), initial, forms.SetProcessor{Form: func(row forms.SetForm) (forms.Form, error) {
		processed++
		return row.Form(), nil
	}}, choices)
	if err != nil || !bound.Valid() || rows != 2 || sets != 1 || processed != 2 {
		t.Fatal("scoped bind changed or repeated validators", err, rows, sets, processed)
	}
	for index, row := range bound.Forms() {
		value, _ := row.Form().Cleaned().String("title")
		if value != []string{"ONE", "TWO"}[index] || len(row.Fields()[1].Choices()) != 1 {
			t.Fatal("snapshot replaced field structure or normalizer", index, value)
		}
	}
	if strings.Contains(fmt.Sprintf("%+v %+v", input, choices), "Row zero") {
		t.Fatal("preflight or choices diagnostic exposed labels")
	}
	if _, err := input.Bind(t.Context(), initial[:1], choices); err == nil {
		t.Fatal("preflight accepted another initial cohort size")
	}
}

func TestRowModelChoicesNeverUnionOrUsePrototypeFallback(t *testing.T) {
	spec := scopedChoiceSet(t, scopedChoicePolicy(), nil, nil)
	initial := []map[string]forms.Value{{}, {}}
	data := setData(2, 2, map[string]string{"title": "one", "owner": "1"}, map[string]string{"title": "two", "owner": "2"})
	input, err := spec.Preflight(t.Context(), data, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, choices := range []forms.RowModelChoices{
		forms.NewRowModelChoices(map[int]map[string][]forms.Choice{0: {"owner": {{Value: forms.Integer(2), Label: "Two"}}}, 1: {"owner": {{Value: forms.Integer(1), Label: "One"}}}}, nil),
		forms.NewRowModelChoices(map[int]map[string][]forms.Choice{0: {}}, map[string][]forms.Choice{"owner": {{Value: forms.Integer(1), Label: "Prototype"}, {Value: forms.Integer(2), Label: "Prototype two"}}}),
		{},
	} {
		bound, err := input.Bind(t.Context(), initial, choices)
		if err != nil || bound.Valid() || bound.Forms()[0].Form().Valid() || bound.Forms()[1].Form().Valid() {
			t.Fatal("missing or foreign scope inherited another row/global/template choice", err)
		}
	}
	prototype := map[string][]forms.Choice{"owner": {{Value: forms.Integer(2), Label: "Prototype"}}}
	derived, err := spec.WithRowModelChoices(forms.NewRowModelChoices(nil, prototype))
	if err != nil {
		t.Fatal(err)
	}
	prototype["owner"][0].Label = "caller mutation"
	empty, err := derived.EmptyForm()
	if err != nil || empty.Fields()[1].Choices()[0].Label != "Prototype" {
		t.Fatal("prototype snapshot lost", err)
	}
	unbound, err := derived.Unbound(initial)
	if err != nil || len(unbound.Forms()[0].Fields()[1].Choices()) != 0 {
		t.Fatal("prototype became an ordinary row fallback", err)
	}
	legacy, err := spec.Bind(t.Context(), data, initial)
	if err != nil || !legacy.Valid() {
		t.Fatal("one-call binding lost its declared choices", err)
	}
}

func TestRowModelChoicesRejectStructuralChangesAndSurvivePolicyCopies(t *testing.T) {
	spec := scopedChoiceSet(t, scopedChoicePolicy(), nil, nil)
	for _, rows := range []map[int]map[string][]forms.Choice{
		{-1: {}}, {4: {}}, {0: {"missing": nil}}, {0: {"title": nil}},
		{0: {"owner": {{Value: forms.String("one"), Label: "Wrong type"}}}},
	} {
		if _, err := spec.WithRowModelChoices(forms.NewRowModelChoices(rows, nil)); err == nil {
			t.Fatal("invalid row choice snapshot accepted", rows)
		}
	}
	choices := forms.NewRowModelChoices(map[int]map[string][]forms.Choice{0: {"owner": {{Value: forms.Integer(1), Label: "One"}}}}, nil)
	derived, err := spec.WithRowModelChoices(choices)
	if err != nil {
		t.Fatal(err)
	}
	derived, err = derived.WithConfig(derived.Config())
	if err != nil {
		t.Fatal(err)
	}
	field, err := forms.CharField("title", forms.WithStringNormalizer(strings.ToUpper))
	if err != nil {
		t.Fatal(err)
	}
	derived, err = derived.WithFormField(field)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := derived.Bind(t.Context(), setData(1, 1, map[string]string{"title": "one", "owner": "2"}), []map[string]forms.Value{{}})
	if err != nil || bound.Valid() {
		t.Fatal("policy copy discarded row restriction", err)
	}
	parent, err := forms.InlineParentField("owner", forms.Integer(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := derived.WithFormField(parent); err == nil {
		t.Fatal("choice snapshot targeted a server-owned parent field")
	}
}

func TestSetPreflightManagementReadonlyAndCancellation(t *testing.T) {
	config := scopedChoicePolicy()
	config.ReadOnlyInitial = true
	spec := scopedChoiceSet(t, config, nil, nil)
	for _, data := range []forms.Data{
		forms.NewData(nil), setData(-1, 1), setData(1, 0), setData(0, 1), setData(5, 1),
	} {
		input, err := spec.Preflight(t.Context(), data, 1)
		if err != nil || input.AdmissionValid() {
			t.Fatal("invalid management admitted before choice I/O", err)
		}
		bound, err := input.Bind(t.Context(), []map[string]forms.Value{{"title": forms.String("server"), "owner": forms.Integer(2)}}, forms.RowModelChoices{})
		if err != nil || bound.Valid() || bound.NonFormErrors().Empty() {
			t.Fatal("management failure was lost", err)
		}
	}
	data := setData(2, 1, map[string]string{"title": "forged", "owner": "999"}, map[string]string{"title": "extra", "owner": "1"})
	input, err := spec.Preflight(t.Context(), data, 1)
	if err != nil || !input.AdmissionValid() {
		t.Fatal(err)
	}
	for index, expected := range []bool{true, false} {
		readonly, err := input.ReadOnly(index)
		if err != nil || readonly != expected {
			t.Fatal("read-only scope differs", index, err)
		}
	}
	bound, err := input.Bind(t.Context(), []map[string]forms.Value{{"title": forms.String("server"), "owner": forms.Integer(2)}}, forms.NewRowModelChoices(map[int]map[string][]forms.Choice{1: {"owner": {{Value: forms.Integer(1), Label: "One"}}}}, nil))
	if err != nil || !bound.Valid() {
		t.Fatal("read-only initial row required submitted choice resolution", err)
	}
	if value, _ := bound.Forms()[0].Form().Cleaned().Integer("owner"); value != 2 {
		t.Fatal("read-only submitted choice replaced server value")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := input.Bind(ctx, []map[string]forms.Value{{}}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled continuation admitted", err)
	}
}

func TestRowModelChoicesConcurrentBindingsRemainIndependent(t *testing.T) {
	spec := scopedChoiceSet(t, scopedChoicePolicy(), nil, nil)
	var workers sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		workers.Go(func() {
			key := int64(worker%2 + 1)
			input, err := spec.Preflight(t.Context(), setData(1, 0, map[string]string{"title": "new", "owner": fmt.Sprint(key)}), 0)
			if err != nil {
				t.Error(err)
				return
			}
			bound, err := input.Bind(t.Context(), nil, forms.NewRowModelChoices(map[int]map[string][]forms.Choice{0: {"owner": {{Value: forms.Integer(key), Label: "Current"}}}}, nil))
			if err != nil || !bound.Valid() {
				t.Error("concurrent row snapshot crossed requests", err)
			}
		})
	}
	workers.Wait()
}

func TestSetPreflightLeavesDeletableMaxCountForFinalBinding(t *testing.T) {
	config := scopedChoicePolicy()
	config.MaxForms = 1
	spec := scopedChoiceSet(t, config, nil, nil)
	input, err := spec.Preflight(t.Context(), setData(2, 2,
		map[string]string{"title": "deleted", "owner": "999", "DELETE": "on"},
		map[string]string{"title": "survivor", "owner": "2"},
	), 2)
	if err != nil || !input.AdmissionValid() {
		t.Fatal("preflight applied MaxForms before effective deletions", err)
	}
	bound, err := input.Bind(t.Context(), []map[string]forms.Value{{}, {}}, forms.NewRowModelChoices(map[int]map[string][]forms.Choice{
		1: {"owner": {{Value: forms.Integer(2), Label: "Survivor"}}},
	}, nil))
	if err != nil || !bound.Valid() {
		t.Fatal("deleted row's missing choice scope blocked an admitted deletion", err)
	}
	deleted, err := bound.DeletedForms()
	if err != nil || len(deleted) != 1 || deleted[0].Form().Valid() {
		t.Fatal("deleted row lost its data diagnostics", err)
	}
}

func TestRowModelChoicesScopeModelMultipleChoicesIndependently(t *testing.T) {
	field, err := forms.ModelMultipleChoiceField("labels", forms.WithChoices(forms.Choice{Value: forms.Integer(1), Label: "One"}, forms.Choice{Value: forms.Integer(2), Label: "Two"}))
	if err != nil {
		t.Fatal(err)
	}
	row, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSetSpec(row, scopedChoicePolicy())
	if err != nil {
		t.Fatal(err)
	}
	choices := forms.NewRowModelChoices(map[int]map[string][]forms.Choice{
		0: {"labels": {{Value: forms.Integer(1), Label: "One"}}},
		1: {"labels": {{Value: forms.Integer(2), Label: "Two"}}},
	}, nil)
	for _, mixed := range []bool{false, true} {
		data := map[string][]string{"items-TOTAL_FORMS": {"2"}, "items-INITIAL_FORMS": {"2"}, "items-0-labels": {"1"}, "items-1-labels": {"2"}}
		if mixed {
			data["items-0-labels"], data["items-1-labels"] = []string{"1", "2"}, []string{"1", "2"}
		}
		input, err := spec.Preflight(t.Context(), forms.NewData(data), 2)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := input.Bind(t.Context(), []map[string]forms.Value{{}, {}}, choices)
		if err != nil || bound.Valid() == mixed {
			t.Fatal("multiple choice borrowed another row's scope", mixed, err)
		}
	}
}
