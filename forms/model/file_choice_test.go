package model_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

func fileChoiceModel(t *testing.T, image bool) ir.Model {
	t.Helper()
	fields := []schema.Field{
		schema.FileField("document", "Document", schema.MaxLength(40), schema.Blank(), schema.Choices(schema.Choice("files/a.txt", "A"), schema.Choice("files/b.txt", "B"))),
		schema.FileField("optional", "Optional", schema.MaxLength(40), schema.Blank(), schema.Nullable(), schema.Choices(schema.Choice("files/a.txt", "A"))),
		schema.FileField("defaulted", "Defaulted", schema.MaxLength(40), schema.Blank(), schema.Default("files/a.txt"), schema.Choices(schema.Choice("files/a.txt", "A"), schema.Choice("files/b.txt", "B"))),
	}
	if image {
		fields = []schema.Field{
			schema.ImageField("photo", "Photo", schema.MaxLength(40), schema.Blank(), schema.Nullable(), schema.ImageDimensions("width", "height"), schema.Choices(schema.Choice("images/a.png", "A"), schema.Choice("images/b.png", "B"), schema.Choice("images/absent.png", "Missing"))),
			schema.IntegerField("width", "Width", schema.Nullable()), schema.IntegerField("height", "Height", schema.Nullable()),
		}
	}
	s, err := schema.Build(schema.Definition{AppLabel: "choices", Models: []schema.Model{{Name: "document", GoName: "Document", Fields: fields}}})
	if err != nil {
		t.Fatal(err)
	}
	return s.Models[0]
}

func TestModelFileChoicesAgainstPinnedDjango(t *testing.T) {
	data, err := os.ReadFile("testdata/file-choice-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Django, Python, Pillow string
		Commit                 string `json:"django_commit"`
		Hash                   string `json:"file_fields_sha256"`
		Cases                  []struct {
			Case, Model, Field, Widget, Initial, Name, Exception string
			NameField                                            string `json:"name_field"`
			HasUpload                                            bool   `json:"has_upload"`
			Multipart, Valid                                     bool
			Input                                                map[string]string
			Changed, Reads                                       []string
			Errors                                               map[string][]string
			Width, Height                                        *int64
		}
	}
	if err := json.Unmarshal(data, &native); err != nil {
		t.Fatal(err)
	}
	if native.Django != "6.1" || native.Python != "3.14.3" || native.Pillow != "12.3.0" || native.Commit != "fe0a859f537d4238cf49fca39073513206f83122" || native.Hash != "fed8e0f0f32feb483bcc96fb16ce417f77493079981c252182a4fee17b4298c8" || len(native.Cases) != 20 {
		t.Fatal("incomplete native choice reference")
	}
	for _, want := range native.Cases {
		t.Run(want.Case, func(t *testing.T) {
			isImage := want.Model == "Photograph"
			model := fileChoiceModel(t, isImage)
			root, err := storage.NewMemory(storage.MemoryConfig{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			})
			for index, name := range []string{"images/a.png", "images/b.png"} {
				width, height := 3+4*index, 2+3*index
				if _, err := storage.SaveUpload(t.Context(), root, name, imageUpload(t, "source.png", width, height), storage.SaveOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			inspector, err := storage.NewImageInspector(root)
			if err != nil {
				t.Fatal(err)
			}
			var overrides []formmodel.Override
			if isImage {
				overrides = []formmodel.Override{formmodel.OverrideField("photo", formmodel.WithImageChoiceInspector(inspector.Inspect))}
			}
			spec, err := formmodel.NewSpecForFields(model, []string{want.NameField}, overrides...)
			if err != nil {
				t.Fatal(err)
			}
			if spec.IsMultipart() || want.Multipart || want.Field != "TypedChoiceField" || want.Widget != "Select" || spec.Fields()[0].Widget() != forms.Select || spec.Fields()[0].AcceptsUpload() {
				t.Fatal("reference choice became upload")
			}
			initial := map[string]forms.Value{want.NameField: forms.String(want.Initial)}
			if isImage {
				initial["width"], initial["height"] = forms.Integer(19), forms.Integer(23)
			}
			raw := map[string][]string{}
			for name, value := range want.Input {
				raw[name] = []string{value}
			}
			files := map[string][]uploads.File{}
			if want.HasUpload {
				file, err := uploads.NewFile("a.txt", "text/plain", []byte("synthetic"))
				if err != nil {
					t.Fatal(err)
				}
				files[want.NameField] = []uploads.File{file}
			}
			var cleanValues forms.Values
			bound, err := formmodel.Bind(t.Context(), model, spec, forms.NewDataWithFiles(raw, files), initial, formmodel.PostClean{Clean: func(values forms.Values) (forms.Values, validation.Errors) {
				cleanValues = values
				return forms.Values{}, validation.Errors{}
			}})
			if want.Exception != "" {
				if want.Exception != "FileNotFoundError" || !errors.Is(err, fs.ErrNotExist) {
					t.Fatal("missing declared image did not preserve I/O error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if bound.Form().Valid() != want.Valid || !slices.Equal(bound.Form().Changed(), want.Changed) {
				t.Fatal("native validity/changes", bound.Form().Errors(), bound.Form().Changed())
			}
			codes := map[string][]string{}
			for _, failure := range bound.Form().Errors().All() {
				codes[string(failure.Field())] = append(codes[string(failure.Field())], string(failure.Code()))
			}
			if !reflect.DeepEqual(codes, want.Errors) {
				t.Fatal("native errors differ", codes, want.Errors)
			}
			if name, _ := bound.Candidate().String(want.NameField); name != want.Name {
				t.Fatal("native candidate name", name, want.Name)
			}
			if isImage {
				width, height := *want.Width, *want.Height
				// An empty/omitted nullable reference keeps the current snapshot.
				// GoDj deliberately performs no implicit retained-file I/O.
				if want.Case == "image_empty" || want.Case == "image_omitted" {
					width, height = 19, 23
				}
				for _, values := range []forms.Values{bound.Candidate(), cleanValues} {
					w, wok := values.Integer("width")
					h, hok := values.Integer("height")
					if !wok || !hok || w != width || h != height {
						t.Fatal("dimensions were not available before model clean", w, h)
					}
				}
			}
			if want.Valid {
				input, err := bound.Input()
				if err != nil {
					t.Fatal(err)
				}
				name, _ := input.String(want.NameField)
				if file, ok := input.File(want.NameField); ok {
					name = file.Name()
					if _, pending := file.Upload(); pending {
						t.Fatal("choice became pending publication")
					}
				}
				if name != want.Name {
					t.Fatal("prepared reference differs", name, want.Name)
				}
			}
		})
	}
}

func TestModelFileChoicePolicyCannotBeWidenedBeforeImageIO(t *testing.T) {
	model := fileChoiceModel(t, true)
	called := 0
	inspect := func(ctx context.Context, name string, limits uploads.ImageLimits) (uploads.ImageInfo, error) {
		called++
		return uploads.ImageInfo{}, errors.New("must not inspect")
	}
	for _, choice := range []bool{false, true} {
		var options []forms.FieldOption
		if choice {
			options = append(options, forms.WithChoices(forms.Choice{Value: forms.String("other/private.png"), Label: "Other"}), forms.WithImageChoiceInspector(inspect))
		}
		field, err := forms.ImageField("photo", options...)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := formmodel.Bind(t.Context(), model, spec, forms.NewData(map[string][]string{"photo": {"other/private.png"}}), nil, formmodel.PostClean{}); err == nil {
			t.Fatal("noncanonical image choice accepted")
		}
	}
	if called != 0 {
		t.Fatal("unauthorized image was inspected")
	}
	if _, err := formmodel.NewSpec(model); err == nil {
		t.Fatal("image choices silently skipped inspection")
	}
	if _, err := serializers.FromModel(model, serializers.ModelField{Name: "photo"}); err == nil {
		t.Fatal("JSON choice became writable")
	}
}

func TestImageChoiceClearAndOmittedDefaultKeepDimensionOwnership(t *testing.T) {
	for _, defaulted := range []bool{false, true} {
		model := fileChoiceModel(t, true)
		model.Fields[1].Nullable = false
		if defaulted {
			model.Fields[1].Default = &ir.Scalar{Kind: ir.ScalarString, String: "images/a.png"}
		}
		inspect := func(context.Context, string, uploads.ImageLimits) (uploads.ImageInfo, error) {
			t.Error("empty image selection performed I/O")
			return uploads.ImageInfo{}, errors.New("unexpected inspection")
		}
		spec, err := formmodel.NewSpec(model, formmodel.OverrideField("photo", formmodel.WithImageChoiceInspector(inspect)))
		if err != nil {
			t.Fatal(err)
		}
		for _, omitted := range []bool{false, true} {
			raw := map[string][]string{}
			if !omitted {
				raw["photo"] = []string{""}
			}
			bound, err := formmodel.Bind(t.Context(), model, spec, forms.NewData(raw), map[string]forms.Value{"photo": forms.String("images/b.png"), "width": forms.Integer(7), "height": forms.Integer(5)}, formmodel.PostClean{})
			if err != nil || !bound.Form().Valid() {
				t.Fatal("empty image choice bind", err, bound.Form().Errors())
			}
			input, err := bound.Input()
			if err != nil {
				t.Fatal(err)
			}
			if defaulted && omitted {
				if name, _ := input.String("photo"); name != "images/b.png" {
					t.Fatal("omitted default replaced current reference", name)
				}
				if _, present := input.Get("width"); present {
					t.Fatal("omitted default rewrote dimensions")
				}
			} else {
				file, ok := input.File("photo")
				if !ok || !file.Clear() {
					t.Fatal("explicit empty reference lost clear intent")
				}
				for _, name := range []string{"width", "height"} {
					value, present := input.Get(name)
					if !present || !value.IsNull() {
						t.Fatal("clear retained derived dimension", name)
					}
				}
			}
		}
	}
}
