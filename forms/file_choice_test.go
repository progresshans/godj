package forms_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

func TestFileChoiceInputsPreserveExactNamesAndEmptySemantics(t *testing.T) {
	choices := []forms.Choice{{Value: forms.String(" a.txt "), Label: "Whitespace"}, {Value: forms.String("가.txt"), Label: "Unicode"}}
	initial, err := forms.ExistingFile(" a.txt ")
	if err != nil {
		t.Fatal(err)
	}
	for _, nullable := range []bool{false, true} {
		for _, required := range []bool{false, true} {
			options := []forms.FieldOption{forms.WithChoices(choices...), forms.WithRequired(required)}
			if nullable {
				options = append(options, forms.WithNullable())
			}
			field, err := forms.FileField("file", options...)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				values  []string
				code    string
				changed bool
			}{
				{[]string{" a.txt "}, "", false}, {[]string{"가.txt"}, "", true}, {[]string{"a.txt"}, "invalid_choice", true},
				{[]string{"other.txt"}, "invalid_choice", true}, {[]string{" a.txt ", "가.txt"}, "multiple", true}, {[]string{""}, "", true}, {nil, "", true},
			} {
				form, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"file": tc.values}), map[string]forms.Value{"file": initial})
				if err != nil {
					t.Fatal(err)
				}
				code := tc.code
				empty := len(tc.values) == 0 || tc.values[0] == ""
				if empty && required {
					code = "required"
				}
				if form.Valid() != (code == "") || (len(form.Changed()) != 0) != tc.changed {
					t.Fatal("choice validity or changed", form.Errors(), form.Changed())
				}
				if code != "" {
					if all := form.Errors().All(); len(all) != 1 || string(all[0].Code()) != code {
						t.Fatal("choice error", all)
					}
					continue
				}
				value, _ := form.Cleaned().Get("file")
				if empty {
					if nullable != value.IsNull() {
						t.Fatal("nullable empty semantics")
					}
					if !nullable {
						file, ok := value.AsFile()
						if !ok || !file.Clear() || file.Name() != "" {
							t.Fatal("empty choice did not clear")
						}
					}
				}
			}
		}
	}
}

func TestImageChoiceInspectionIsExplicitBoundedAndBeforeValidators(t *testing.T) {
	fixture := imageFixture(t)
	// The existing independently observed PNG fixture supplies real inspection metadata.
	var pngName string
	for name := range fixture.Payloads {
		if name == "png" {
			pngName = name
		}
	}
	if pngName == "" {
		t.Fatal("missing PNG fixture")
	}
	file, _ := imageUpload(t, fixture, pngName, "asset.png")
	info, err := uploads.InspectImage(t.Context(), file, uploads.ImageLimits{})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	var fail atomic.Bool
	failure := errors.New("synthetic inspection failure")
	inspect := func(ctx context.Context, name string, limits uploads.ImageLimits) (uploads.ImageInfo, error) {
		calls.Add(1)
		if ctx != t.Context() || name != "images/a.png" || limits.MaxPixels != 100 {
			return uploads.ImageInfo{}, errors.New("lost context/name/limits")
		}
		if fail.Load() {
			return uploads.ImageInfo{}, failure
		}
		return info, nil
	}
	var validations atomic.Int64
	field, err := forms.ImageField("image", forms.WithChoices(forms.Choice{Value: forms.String("images/a.png"), Label: "A"}), forms.WithRequired(false), forms.WithNullable(), forms.WithImageLimits(uploads.ImageLimits{MaxPixels: 100}), forms.WithImageChoiceInspector(inspect), forms.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		file, ok := value.AsFile()
		checked, verified := file.Image()
		if !ok || !verified || checked.Width() != info.Width() {
			return validation.NewErrors(validation.New(validation.NonField, "unverified"))
		}
		validations.Add(1)
		return validation.Errors{}
	})))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := forms.ExistingFile("images/a.png")
	if _, err := spec.Unbound(map[string]forms.Value{"image": initial}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]string{nil, {""}, {"unknown"}, {"images/a.png", "images/a.png"}} {
		if _, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"image": raw}), map[string]forms.Value{"image": initial}); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 0 || validations.Load() != 0 {
		t.Fatal("empty, invalid or unbound inspected")
	}
	config := forms.DefaultSetConfig()
	config.Prefix = "images"
	config.ReadOnlyInitial = true
	setSpec, err := forms.NewSetSpec(spec, config)
	if err != nil {
		t.Fatal(err)
	}
	readonly, err := setSpec.Bind(t.Context(), forms.NewData(map[string][]string{"images-TOTAL_FORMS": {"1"}, "images-INITIAL_FORMS": {"1"}, "images-0-image": {"images/a.png"}}), []map[string]forms.Value{{"image": initial}})
	if err != nil || !readonly.Valid() || calls.Load() != 0 {
		t.Fatal("readonly choice inspected storage", err)
	}
	bind := func() forms.Form {
		form, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"image": {"images/a.png"}}), map[string]forms.Value{"image": initial})
		if err != nil || !form.Valid() || len(form.Changed()) != 0 {
			t.Error("same reference was not freshly inspected", err, form.Errors())
		}
		return form
	}
	cleaned := bind().Cleaned()
	value, _ := cleaned.Get("image")
	if _, err := spec.Unbound(map[string]forms.Value{"image": value}); err == nil {
		t.Fatal("old inspection metadata reused as initial")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { bind() })
	}
	wg.Wait()
	if calls.Load() != 9 || validations.Load() != 9 {
		t.Fatal("inspection cached or validator ordering lost", calls.Load(), validations.Load())
	}
	fail.Store(true)
	if form, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"image": {"images/a.png"}}), nil); !errors.Is(err, failure) || form.Bound() {
		t.Fatal("I/O failure became valid or a user validation error", err)
	}
	if validations.Load() != 9 {
		t.Fatal("validator ran after inspection error")
	}
}

func TestImageChoiceRejectsMissingOrInvalidInspectionCapability(t *testing.T) {
	choice := forms.WithChoices(forms.Choice{Value: forms.String("a.png"), Label: "A"})
	zero := func(context.Context, string, uploads.ImageLimits) (uploads.ImageInfo, error) {
		return uploads.ImageInfo{}, nil
	}
	for _, options := range [][]forms.FieldOption{{choice}, {forms.WithImageChoiceInspector(zero)}, {choice, forms.WithImageChoiceInspector(nil)}, {choice, forms.WithImageChoiceInspector(zero), forms.WithWidget(forms.FileInput)}} {
		if _, err := forms.ImageField("image", options...); err == nil {
			t.Fatal("invalid image inspection configuration accepted")
		}
	}
	if _, err := forms.FileField("file", choice, forms.WithImageChoiceInspector(zero)); err == nil {
		t.Fatal("file accepted image-only capability")
	}
	field, err := forms.ImageField("image", choice, forms.WithImageChoiceInspector(zero))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	form, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"image": {"a.png"}}), nil)
	var configuration *forms.ConfigError
	if !errors.As(err, &configuration) || configuration.Code != "invalid_result" || form.Bound() {
		t.Fatal("zero inspection accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := spec.Bind(ctx, forms.NewData(map[string][]string{"image": {"a.png"}}), nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	choices := []forms.Choice{{Value: forms.String("a.png"), Label: "A"}}
	f, err := forms.FileField("file", forms.WithChoices(choices...))
	if err != nil {
		t.Fatal(err)
	}
	choices[0].Label = "changed"
	copy := f.Choices()
	copy[0].Label = "again"
	if !reflect.DeepEqual(f.Choices(), []forms.Choice{{Value: forms.String("a.png"), Label: "A"}}) {
		t.Fatal("choices aliased caller")
	}
}
