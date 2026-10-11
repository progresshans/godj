package model_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

func imageModel(t *testing.T) ir.Model {
	t.Helper()
	s, err := schema.Build(schema.Definition{AppLabel: "photos", Models: []schema.Model{{Name: "photo", GoName: "Photo", Fields: []schema.Field{
		schema.CharField("title", "Title", 30, schema.Unique()),
		schema.ImageField("photo", "Photo", schema.Nullable(), schema.Blank(), schema.ImageDimensions("width", "height")),
		schema.IntegerField("width", "Width", schema.Nullable()), schema.IntegerField("height", "Height", schema.Nullable()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return s.Models[0]
}

func imageUpload(t *testing.T, name string, width, height int) uploads.File {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	file, err := uploads.NewFile(name, "application/x-untrusted", out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestModelImagesAgainstPinnedDjangoObservations(t *testing.T) {
	data, err := os.ReadFile("testdata/model-image-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	type observed struct {
		Photo         *string
		Width, Height *int64
	}
	var reference struct {
		Django, Python, Pillow string
		Sources                map[string]string
		Cases                  []struct {
			Case struct {
				Name, Filename                                                  string
				Existing, Clear, Upload, Invalid, Forged, Spoof, Late, Excluded bool
				Width, Height                                                   int64
			}
			Valid         bool
			Changed       []string
			Errors        map[string][]string
			Photo         *string
			Width, Height *int64
			Reads         int
			Clean         []observed
		}
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Pillow != "12.3.0" || len(reference.Cases) != 15 || reference.Sources["django.db.models.fields.files"] != "fed8e0f0f32feb483bcc96fb16ce417f77493079981c252182a4fee17b4298c8" {
		t.Fatal("incomplete model image reference")
	}
	model := imageModel(t)
	for _, want := range reference.Cases {
		t.Run(want.Case.Name, func(t *testing.T) {
			test := want.Case
			initial := map[string]forms.Value{"title": forms.String(test.Name)}
			if test.Existing {
				initial["photo"] = forms.String("photos/old.png")
				width, height := int64(3), int64(2)
				if test.Width != 0 {
					width = test.Width
				}
				if test.Height != 0 {
					height = test.Height
				}
				initial["width"], initial["height"] = forms.Integer(width), forms.Integer(height)
			}
			names := []string{"photo"}
			if test.Excluded {
				names = []string{"title"}
			}
			spec, err := formmodel.NewSpecForFields(model, names)
			if err != nil {
				t.Fatal(err)
			}
			if test.Late {
				spec, err = forms.NewSpec(spec.Fields(), forms.CrossValidatorFunc(func(forms.Values) validation.Errors {
					return validation.NewErrors(validation.New(validation.NonField, "late"))
				}))
				if err != nil {
					t.Fatal(err)
				}
			}
			raw := map[string][]string{}
			files := map[string][]uploads.File{}
			if test.Excluded {
				raw["title"] = []string{test.Name}
			}
			if test.Clear {
				raw["photo-clear"] = []string{"on"}
			}
			if test.Forged {
				raw["photo"] = []string{"other/private.png"}
			}
			if test.Spoof {
				raw["width"], raw["height"] = []string{"999"}, []string{"888"}
			}
			if test.Upload || test.Invalid {
				name := test.Filename
				if name == "" {
					name = "new.png"
				}
				file := imageUpload(t, name, 7, 5)
				if test.Invalid {
					file, err = uploads.NewFile(name, "image/png", []byte("invalid"))
					if err != nil {
						t.Fatal(err)
					}
				}
				files["photo"] = []uploads.File{file}
			}
			var cleans []forms.Values
			post := formmodel.PostClean{Clean: func(values forms.Values) (forms.Values, validation.Errors) {
				cleans = append(cleans, values)
				return forms.Values{}, validation.Errors{}
			}}
			bound, err := formmodel.Bind(t.Context(), model, spec, forms.NewDataWithFiles(raw, files), initial, post)
			if err != nil {
				t.Fatal(err)
			}
			if bound.Form().Valid() != want.Valid || !slices.Equal(bound.Form().Changed(), want.Changed) {
				t.Fatal("native validity/changes", bound.Form().Errors(), bound.Form().Changed())
			}
			codes := map[string][]string{}
			for _, failure := range bound.Form().Errors().All() {
				name := string(failure.Field())
				if failure.Field() == validation.NonField {
					name = "__all__"
				}
				codes[name] = append(codes[name], string(failure.Code()))
			}
			if !reflect.DeepEqual(codes, want.Errors) {
				t.Fatal("native errors", codes, want.Errors)
			}
			expected := observed{want.Photo, want.Width, want.Height}
			if test.Name == "retain_stale_dimensions" {
				if want.Reads != 1 || want.Width == nil || *want.Width != 3 || want.Height == nil || *want.Height != 2 {
					t.Fatal("native reread difference lost")
				}
				expected.Width, expected.Height = &test.Width, &test.Height // Go binding never opens a storage reference.
			}
			check := func(values forms.Values) {
				t.Helper()
				value, present := values.Get("photo")
				if !present {
					t.Fatal("photo missing")
				}
				if expected.Photo == nil {
					if !value.IsNull() {
						t.Fatal("NULL photo lost")
					}
				} else if got, ok := value.AsString(); !ok || got != *expected.Photo {
					t.Fatal("photo candidate", got)
				}
				for name, number := range map[string]*int64{"width": expected.Width, "height": expected.Height} {
					value, present := values.Get(name)
					if !present {
						t.Fatal("dimension missing", name)
					}
					if number == nil {
						if !value.IsNull() {
							t.Fatal("dimension must be NULL", name)
						}
					} else if got, ok := value.AsInteger(); !ok || got != *number {
						t.Fatal("dimension candidate", name, got, *number)
					}
				}
			}
			check(bound.Candidate())
			if len(cleans) != 1 || len(want.Clean) != 1 {
				t.Fatal("model clean must run once", len(cleans), len(want.Clean))
			}
			check(cleans[0])
			input, err := bound.Input()
			if !want.Valid {
				if err == nil {
					t.Fatal("invalid candidate became input")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.Upload && !test.Excluded || test.Clear {
				for _, name := range []string{"width", "height"} {
					value, ok := input.Get(name)
					candidate, _ := bound.Candidate().Get(name)
					if !ok || !value.Equal(candidate) {
						t.Fatal("derived dimension omitted from prepared input", name)
					}
				}
			} else {
				if _, ok := input.Get("width"); ok {
					t.Fatal("retained dimension became a write")
				}
			}
			if test.Upload && !test.Excluded {
				file, ok := input.File("photo")
				info, verified := file.Image()
				if !ok || !verified || info.Width() != 7 || info.Height() != 5 {
					t.Fatal("image proof lost")
				}
			}
		})
	}
}

func TestModelImageDimensionInputsCannotBypassOwnershipOrContentInspection(t *testing.T) {
	model := imageModel(t)
	spec, err := formmodel.NewSpec(model)
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Fields(); len(got) != 2 || got[1].Kind() != forms.FieldImage {
		t.Fatal("default form exposes dimensions")
	}
	for _, name := range []string{"width", "height"} {
		if _, err := formmodel.NewSpecForFields(model, []string{name}); err == nil {
			t.Fatal("explicit editable dimension accepted")
		}
		if _, err := formmodel.NewSpec(model, formmodel.OverrideField(name, formmodel.WithRequired(false))); err == nil {
			t.Fatal("managed dimension override accepted")
		}
		input, err := forms.IntegerField(name)
		if err != nil {
			t.Fatal(err)
		}
		manual, err := forms.NewSpec([]forms.Field{input})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := formmodel.Bind(t.Context(), model, manual, forms.NewData(nil), nil, formmodel.PostClean{}); err == nil {
			t.Fatal("hand-written form acquired dimension authority")
		}
		clean := formmodel.PostClean{Fields: []string{name}, Clean: func(forms.Values) (forms.Values, validation.Errors) { return forms.Values{}, validation.Errors{} }}
		if _, err := formmodel.Bind(t.Context(), model, spec, forms.NewData(nil), nil, clean); err == nil {
			t.Fatal("model clean acquired dimension authority")
		}
		if _, err := serializers.FromModel(model, serializers.ModelField{Name: name}); err == nil {
			t.Fatal("JSON writable dimension")
		}
	}
	if _, err := serializers.FromModel(model, serializers.ModelField{Name: "photo"}); err == nil {
		t.Fatal("JSON writable image reference")
	}
	if _, err := serializers.FromModel(model, serializers.ModelField{Name: "photo", ReadOnly: true}, serializers.ModelField{Name: "width", ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	unsafe, err := forms.FileField("photo")
	if err != nil {
		t.Fatal(err)
	}
	manual, err := forms.NewSpec([]forms.Field{unsafe})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := formmodel.Bind(t.Context(), model, manual, forms.NewData(nil), nil, formmodel.PostClean{}); err == nil {
		t.Fatal("ImageField weakened to FileField")
	}
	narrow, err := formmodel.NewSpecForFields(model, []string{"photo"}, formmodel.OverrideField("photo", formmodel.WithImageLimits(uploads.ImageLimits{MaxWidth: 6})))
	if err != nil {
		t.Fatal(err)
	}
	data := forms.NewDataWithFiles(nil, map[string][]uploads.File{"photo": {imageUpload(t, "large.png", 7, 5)}})
	bound, err := formmodel.Bind(t.Context(), model, narrow, data, nil, formmodel.PostClean{})
	if err != nil || bound.Form().Valid() || len(bound.Form().Errors().All()) != 1 || bound.Form().Errors().All()[0].Code() != "image_pixels" {
		t.Fatal("image limits override lost", err)
	}
	if _, err := formmodel.NewSpec(model, formmodel.OverrideField("title", formmodel.WithImageLimits(uploads.ImageLimits{}))); err == nil {
		t.Fatal("image policy silently ignored on string")
	}
	if _, err := formmodel.NewSpec(model, formmodel.OverrideField("photo", formmodel.WithImageLimits(uploads.ImageLimits{MaxWidth: -1}))); err == nil {
		t.Fatal("invalid image policy")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := formmodel.Bind(ctx, model, narrow, data, nil, formmodel.PostClean{}); !errors.Is(err, context.Canceled) {
		t.Fatal("model binding dropped cancellation", err)
	}
	// A nullable image can clear into a non-nullable dimension candidate, but
	// preparation must fail before typed conversion or any storage/DB work.
	model.Fields[3].Nullable = false
	clear, err := formmodel.Bind(t.Context(), model, narrow, forms.NewData(map[string][]string{"photo-clear": {"on"}}), nil, formmodel.PostClean{})
	if err != nil || !clear.Form().Valid() {
		t.Fatal("clear candidate", err)
	}
	if _, err := clear.Input(); err == nil {
		t.Fatal("NULL dimension became integer zero")
	}
}
