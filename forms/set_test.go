package forms_test

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func setRowSpec(t *testing.T, validators ...forms.CrossValidator) forms.Spec {
	t.Helper()
	field, err := forms.CharField("title", forms.WithMaxLength(8))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field}, validators...)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func setData(total, initial int, rows ...map[string]string) forms.Data {
	values := map[string][]string{"items-TOTAL_FORMS": {strconv.Itoa(total)}, "items-INITIAL_FORMS": {strconv.Itoa(initial)}}
	for index, row := range rows {
		for name, value := range row {
			values[fmt.Sprintf("items-%d-%s", index, name)] = []string{value}
		}
	}
	return forms.NewData(values)
}

func setErrorCodes(errors validation.Errors) map[string][]string {
	result := map[string][]string{}
	for _, error := range errors.All() {
		field := string(error.Field())
		result[field] = append(result[field], string(error.Code()))
	}
	return result
}

func TestFormSetsAgainstPinnedDjango(t *testing.T) {
	data, err := os.ReadFile("testdata/formset-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, Python string
		Cases          []struct {
			Name                 string
			Options              map[string]json.RawMessage
			Initial              []map[string]string
			Data                 map[string]string
			Bound, Unique, Valid bool
			Total                int
			InitialCount         int                 `json:"initial_count"`
			ManagementErrors     map[string][]string `json:"management_errors"`
			Errors               []string
			Forms                []struct {
				Prefix         string
				Fields         []string
				Bound, Valid   bool
				Changed        *bool
				EmptyPermitted bool `json:"empty_permitted"`
				Errors         map[string][]string
				Cleaned        map[string]any
			}
			Deleted, Ordered []int
			CleanCalls       []string `json:"clean_calls"`
		}
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || len(reference.Cases) != 33 {
		t.Fatal("unexpected native formset reference")
	}
	deviations := 0
	for _, observed := range reference.Cases {
		t.Run(observed.Name, func(t *testing.T) {
			config := forms.DefaultSetConfig()
			config.Prefix = "items"
			for name, raw := range observed.Options {
				var target any
				switch name {
				case "extra":
					target = &config.ExtraForms
				case "min_num":
					target = &config.MinForms
				case "max_num":
					target = &config.MaxForms
				case "absolute_max":
					target = &config.AbsoluteMax
				case "validate_min":
					target = &config.ValidateMin
				case "validate_max":
					target = &config.ValidateMax
				case "can_order":
					target = &config.CanOrder
				case "can_delete":
					target = &config.CanDelete
				case "can_delete_extra":
					target = &config.CanDeleteExtra
				default:
					t.Fatal("unexpected native option", name)
				}
				if err := json.Unmarshal(raw, target); err != nil {
					t.Fatal(err)
				}
			}
			if _, present := observed.Options["absolute_max"]; !present {
				config.AbsoluteMax = config.MaxForms + 1000
			}
			calls := 0
			row := setRowSpec(t, forms.CrossValidatorFunc(func(forms.Values) validation.Errors { calls++; return validation.Errors{} }))
			var validators []forms.SetValidator
			if observed.Unique {
				validators = append(validators, forms.SetValidatorFunc(func(rows []forms.SetForm) validation.Errors {
					seen := map[string]bool{}
					for _, row := range rows {
						if !row.Form().Valid() || row.DeletionRequested() {
							continue
						}
						title, _ := row.Form().Cleaned().String("title")
						if title == "" {
							continue
						}
						if seen[title] {
							return validation.NewErrors(validation.New(validation.NonField, "duplicate_title"))
						}
						seen[title] = true
					}
					return validation.Errors{}
				}))
			}
			spec, err := forms.NewSetSpec(row, config, validators...)
			if err != nil {
				t.Fatal(err)
			}
			initial := make([]map[string]forms.Value, len(observed.Initial))
			for index, values := range observed.Initial {
				initial[index] = map[string]forms.Value{}
				for name, value := range values {
					initial[index][name] = forms.String(value)
				}
			}
			var result forms.Set
			if observed.Bound {
				input := map[string][]string{}
				for name, value := range observed.Data {
					input[name] = []string{value}
				}
				result, err = spec.Bind(t.Context(), forms.NewData(input), initial)
			} else {
				result, err = spec.Unbound(initial)
			}
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains([]string{"negative_total", "initial_exceeds_total", "forged_initial_count"}, observed.Name) {
				deviations++
				if !observed.Valid || result.Valid() || result.Management().Errors().Empty() || result.NonFormErrors().Empty() {
					t.Fatal("documented count refusal disappeared")
				}
				if _, err := result.ActiveForms(); err == nil {
					t.Fatal("forged counts supplied a persistence selection")
				}
				return
			}
			if result.Valid() != observed.Valid || result.Bound() != observed.Bound || result.TotalForms() != observed.Total || result.InitialForms() != observed.InitialCount || !reflect.DeepEqual(setErrorCodes(result.Management().Errors()), observed.ManagementErrors) {
				t.Fatalf("set/management mismatch: valid=%v total=%d initial=%d errors=%v", result.Valid(), result.TotalForms(), result.InitialForms(), setErrorCodes(result.Management().Errors()))
			}
			if !slices.Equal(setErrorCodes(result.NonFormErrors())[string(validation.NonField)], observed.Errors) {
				t.Fatal("non-form errors differ", setErrorCodes(result.NonFormErrors()))
			}
			rows := result.Forms()
			if len(rows) != len(observed.Forms) {
				t.Fatal("native row count differs")
			}
			for index, want := range observed.Forms {
				got := rows[index]
				names := []string{}
				for _, field := range got.Fields() {
					names = append(names, field.Name())
				}
				if got.Index() != index || got.Prefix() != want.Prefix || !slices.Equal(names, want.Fields) || got.EmptyPermitted() != want.EmptyPermitted || got.Form().Bound() != want.Bound || got.Form().Valid() != want.Valid || !reflect.DeepEqual(setErrorCodes(got.Form().Errors()), want.Errors) {
					t.Fatalf("row %d differs: prefix=%s fields=%v valid=%v errors=%v", index, got.Prefix(), names, got.Form().Valid(), setErrorCodes(got.Form().Errors()))
				}
				if want.Changed != nil && (len(got.Form().Changed()) != 0) != *want.Changed {
					t.Fatal("row changed differs", index, got.Form().Changed())
				}
				cleaned := map[string]any{}
				for _, entry := range got.Form().Cleaned().All() {
					value := entry.Value()
					switch value.Kind() {
					case forms.ValueNull:
						cleaned[entry.Name()] = nil
					case forms.ValueString:
						cleaned[entry.Name()], _ = value.AsString()
					case forms.ValueBoolean:
						cleaned[entry.Name()], _ = value.AsBoolean()
					case forms.ValueInteger:
						number, _ := value.AsInteger()
						cleaned[entry.Name()] = float64(number)
					default:
						t.Fatal("unexpected cleaned kind")
					}
				}
				if observed.Bound && !reflect.DeepEqual(cleaned, want.Cleaned) {
					t.Fatal("cleaned row differs", index, cleaned, want.Cleaned)
				}
			}
			if calls != len(observed.CleanCalls) {
				t.Fatal("empty row validation ran or a row was not validated", calls, observed.CleanCalls)
			}
			if !result.Valid() {
				if _, err := result.ActiveForms(); err == nil {
					t.Fatal("invalid/unbound active selection")
				}
				if _, err := result.DeletedForms(); err == nil {
					t.Fatal("invalid/unbound deleted selection")
				}
				if _, err := result.OrderedForms(); err == nil {
					t.Fatal("invalid/unbound ordered selection")
				}
				return
			}
			indices := func(rows []forms.SetForm) []int {
				result := []int{}
				for _, row := range rows {
					result = append(result, row.Index())
				}
				return result
			}
			deleted, err := result.DeletedForms()
			if err != nil || !slices.Equal(indices(deleted), observed.Deleted) {
				t.Fatal("deleted rows differ", err, indices(deleted))
			}
			if config.CanOrder {
				ordered, err := result.OrderedForms()
				if err != nil || !slices.Equal(indices(ordered), observed.Ordered) {
					t.Fatal("ordered rows differ", err, indices(ordered))
				}
			} else if _, err := result.OrderedForms(); err == nil {
				t.Fatal("disabled ordering looked supported")
			}
			if calls != len(observed.CleanCalls) {
				t.Fatal("selection reran validators")
			}
		})
	}
	if deviations != 3 {
		t.Fatal("native hardening differences not accounted for", deviations)
	}
}

func TestFormSetBoundsPrefixAndInvalidConfiguration(t *testing.T) {
	base := setRowSpec(t)
	defaults := forms.DefaultSetConfig()
	defaults.Prefix = "items"
	for name, change := range map[string]func(*forms.SetConfig){
		"prefix":      func(c *forms.SetConfig) { c.Prefix = "bad-prefix" },
		"long_prefix": func(c *forms.SetConfig) { c.Prefix = strings.Repeat("a", 129) },
		"extra":       func(c *forms.SetConfig) { c.ExtraForms = -1 },
		"min":         func(c *forms.SetConfig) { c.MinForms = -1 },
		"min_max":     func(c *forms.SetConfig) { c.MinForms = c.MaxForms + 1 },
		"absolute":    func(c *forms.SetConfig) { c.AbsoluteMax = c.MaxForms - 1 },
	} {
		t.Run(name, func(t *testing.T) {
			config := defaults
			change(&config)
			if _, err := forms.NewSetSpec(base, config); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	if _, err := forms.NewSetSpec(forms.Spec{}, defaults); err == nil {
		t.Fatal("zero form accepted")
	}
	var nilValidator forms.SetValidatorFunc
	if _, err := forms.NewSetSpec(base, defaults, nilValidator); err == nil {
		t.Fatal("typed nil set validator accepted")
	}
	for _, name := range []string{"ORDER", "DELETE"} {
		field, err := forms.CharField(name)
		if err != nil {
			t.Fatal(err)
		}
		row, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		config := defaults
		config.CanOrder = true
		config.CanDelete = true
		if _, err := forms.NewSetSpec(row, config); err == nil {
			t.Fatal("reserved field was overwritten")
		}
	}
	config := defaults
	config.MaxForms = 1
	config.AbsoluteMax = 2
	config.ExtraForms = math.MaxInt
	calls := 0
	row := setRowSpec(t, forms.CrossValidatorFunc(func(forms.Values) validation.Errors { calls++; return validation.Errors{} }))
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	unbound, err := spec.Unbound(nil)
	if err != nil || unbound.TotalForms() != 1 || calls != 0 {
		t.Fatal("display overflow or validation", err)
	}
	tooMany, err := spec.Bind(t.Context(), setData(math.MaxInt, 0, map[string]string{"title": "one"}, map[string]string{"title": "two"}), nil)
	if err != nil || tooMany.Valid() || tooMany.TotalForms() != 2 || calls != 2 {
		t.Fatal("allocation cap did not bound callbacks", err, calls)
	}
	if _, err := spec.Unbound([]map[string]forms.Value{{}, {}, {}}); err == nil {
		t.Fatal("server initial exceeded allocation cap")
	}
	for _, raw := range []map[string][]string{
		{"items-TOTAL_FORMS": {"1", "2"}, "items-INITIAL_FORMS": {"0"}},
		{"items-TOTAL_FORMS": {"9223372036854775808"}, "items-INITIAL_FORMS": {"0"}},
		{"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"-1"}},
	} {
		result, err := spec.Bind(t.Context(), forms.NewData(raw), nil)
		if err != nil || result.Valid() || result.Management().Errors().Empty() {
			t.Fatal("invalid count accepted", err)
		}
	}
	other := map[string][]string{"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"0"}, "items-0-title": {"right"}, "elsewhere-0-title": {"wrong"}, "items-01-title": {"alias"}, "items-2-title": {"outside"}}
	result, err := spec.Bind(t.Context(), forms.NewData(other), nil)
	if err != nil || !result.Valid() {
		t.Fatal(err)
	}
	text, _ := result.Forms()[0].Form().Cleaned().String("title")
	if text != "right" {
		t.Fatal("prefix/index boundaries mixed inputs")
	}
	fieldName, err := result.Forms()[0].FieldName("title")
	if err != nil || fieldName != "items-0-title" {
		t.Fatal("wrong prefixed field", err)
	}
	if _, err := result.Forms()[0].FieldName("forged"); err == nil {
		t.Fatal("unknown field received a name")
	}
	if _, err := (forms.SetSpec{}).EmptyForm(); err == nil {
		t.Fatal("zero spec produced a template")
	}
}

func TestFormSetEmptyChoicesOwnershipRedactionAndConcurrentReuse(t *testing.T) {
	choice, err := forms.CharField("title", forms.WithChoices(forms.Choice{Value: forms.String("one"), Label: "One"}))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	row, err := forms.NewSpec([]forms.Field{choice}, forms.CrossValidatorFunc(func(forms.Values) validation.Errors { calls++; return validation.Errors{} }))
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	config.CanDelete = true
	config.CanOrder = true
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := spec.Bind(t.Context(), setData(1, 0), nil)
	if err != nil || !empty.Valid() || calls != 0 || empty.Changed() {
		t.Fatal("unchanged required choice ran validation", err, calls)
	}
	integer, err := forms.IntegerField("choice", forms.WithChoices(forms.Choice{Value: forms.Integer(1), Label: "One"}))
	if err != nil {
		t.Fatal(err)
	}
	intRow, err := forms.NewSpec([]forms.Field{integer})
	if err != nil {
		t.Fatal(err)
	}
	intSpec, err := forms.NewSetSpec(intRow, config)
	if err != nil {
		t.Fatal(err)
	}
	intEmpty, err := intSpec.Bind(t.Context(), setData(1, 0), nil)
	if err != nil || !intEmpty.Valid() {
		t.Fatal("unchanged integer choice ran validation", err)
	}
	template, err := spec.EmptyForm()
	if err != nil || template.Prefix() != "items-__prefix__" || !template.EmptyPermitted() || template.Form().Bound() {
		t.Fatal("invalid empty template", err)
	}
	// Use a callback-free spec for concurrent binding. User callbacks retain
	// the same pure/concurrent contract as an ordinary reusable Form spec.
	spec, err = forms.NewSetSpec(setRowSpec(t), config)
	if err != nil {
		t.Fatal(err)
	}
	initial := []map[string]forms.Value{{"title": forms.String("private")}}
	input := map[string][]string{"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"1"}, "items-0-title": {"edited"}, "items-0-ORDER": {"1"}}
	result, err := spec.Bind(t.Context(), forms.NewData(input), initial)
	if err != nil || !result.Valid() {
		t.Fatal(err)
	}
	initial[0]["title"] = forms.String("changed")
	input["items-0-title"][0] = "changed"
	owned := result.Forms()
	owned[0] = forms.SetForm{}
	current := result.Forms()[0]
	stored, _ := current.Form().Initial().String("title")
	submitted, _ := current.Form().Submitted().Get("title")
	if stored != "private" || !slices.Equal(submitted, []string{"edited"}) {
		t.Fatal("set shares caller storage")
	}
	for _, value := range []any{spec, result, current, current.Form(), result.Submitted()} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			printed := fmt.Sprintf(verb, value)
			if strings.Contains(printed, "private") || strings.Contains(printed, "edited") {
				t.Fatal("formset formatting leaked input")
			}
		}
	}
	rejected, err := result.WithErrors(validation.NewErrors(validation.New(validation.NonField, "conflict")))
	if err != nil || rejected.Valid() || !result.Valid() || !result.NonFormErrors().Empty() {
		t.Fatal("rejection mutated existing result", err)
	}
	if _, err := result.WithErrors(validation.NewErrors(validation.New("title", "invalid"))); err == nil {
		t.Fatal("row error silently became a set error")
	}
	bad, err := forms.NewSetSpec(setRowSpec(t), config, forms.SetValidatorFunc(func([]forms.SetForm) validation.Errors {
		return validation.NewErrors(validation.New("title", "invalid"))
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Bind(t.Context(), setData(0, 0), nil); err == nil {
		t.Fatal("invalid callback diagnostic accepted")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bound, err := spec.Bind(t.Context(), setData(1, 0, map[string]string{"title": "parallel", "ORDER": "2"}), nil)
			if err != nil || !bound.Valid() {
				failures <- fmt.Errorf("concurrent bind failed: %v", err)
				return
			}
			rows, err := bound.OrderedForms()
			if err != nil || len(rows) != 1 {
				failures <- fmt.Errorf("concurrent selection failed: %v", err)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
