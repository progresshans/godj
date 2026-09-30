package model_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/uploads"
)

func TestModelFilesAgainstPinnedDjangoObservations(t *testing.T) {
	data, err := os.ReadFile("testdata/model-file-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django           string `json:"django"`
		DefaultMaxLength int    `json:"default_max_length"`
		Forms            []struct {
			Case struct {
				Name          string  `json:"name"`
				Initial       *string `json:"initial"`
				Optional      *string `json:"optional"`
				Upload        string  `json:"upload"`
				Text          string  `json:"text"`
				Clear         bool    `json:"clear"`
				OptionalClear bool    `json:"optional_clear"`
			} `json:"case"`
			Valid    bool                `json:"valid"`
			Document *string             `json:"document"`
			Optional *string             `json:"optional"`
			Changed  []string            `json:"changed"`
			Errors   map[string][]string `json:"errors"`
		} `json:"forms"`
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DefaultMaxLength != 100 || len(reference.Forms) != 11 {
		t.Fatal("incomplete pinned model file reference")
	}
	s, err := schema.Build(schema.Definition{AppLabel: "files", Models: []schema.Model{{Name: "document", GoName: "Document", Fields: []schema.Field{
		schema.FileField("document", "Document", schema.MaxLength(40), schema.Blank(), schema.Default("defaults/seed.txt")),
		schema.FileField("optional", "Optional", schema.Nullable(), schema.Blank()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := s.Models[0]
	definition := formmodel.Definition{Fields: []string{"document", "optional"}}
	for _, observation := range reference.Forms {
		t.Run(observation.Case.Name, func(t *testing.T) {
			initial := map[string]forms.Value{}
			if observation.Case.Initial != nil {
				initial["document"], err = forms.ExistingFile(*observation.Case.Initial)
				if err != nil {
					t.Fatal(err)
				}
			}
			if observation.Case.Optional != nil {
				initial["optional"], err = forms.ExistingFile(*observation.Case.Optional)
				if err != nil {
					t.Fatal(err)
				}
			}
			values := map[string][]string{}
			if observation.Case.Clear {
				values["document-clear"] = []string{"on"}
			}
			if observation.Case.OptionalClear {
				values["optional-clear"] = []string{"on"}
			}
			if observation.Case.Text != "" {
				values["document"] = []string{observation.Case.Text}
			}
			files := map[string][]uploads.File{}
			if observation.Case.Upload != "" {
				file, err := uploads.NewFile(observation.Case.Upload, "text/plain", []byte("new content"))
				if err != nil {
					t.Fatal(err)
				}
				files["document"] = []uploads.File{file}
			}
			bound, err := definition.Bind(metadata, forms.NewDataWithFiles(values, files), initial)
			if err != nil {
				t.Fatal(err)
			}
			if bound.Form().Valid() != observation.Valid || !slices.Equal(bound.Form().Changed(), observation.Changed) {
				t.Fatal("validity or changes differ from Django", bound.Form().Errors(), bound.Form().Changed())
			}
			codes := map[string][]string{}
			for _, failure := range bound.Form().Errors().All() {
				name := string(failure.Field())
				codes[name] = append(codes[name], string(failure.Code()))
			}
			if !reflect.DeepEqual(codes, observation.Errors) {
				t.Fatal("error codes differ", codes, observation.Errors)
			}
			for name, expected := range map[string]*string{"document": observation.Document, "optional": observation.Optional} {
				value, found := bound.Candidate().Get(name)
				if !found {
					t.Fatal("candidate lost file field", name)
				}
				if expected == nil {
					if !value.IsNull() {
						t.Fatal("SQL NULL reference changed", name)
					}
				} else if actual, ok := value.AsString(); !ok || actual != *expected {
					t.Fatal("candidate file name differs", name, actual, *expected)
				}
			}
			if observation.Valid && observation.Case.Upload != "" {
				input, err := bound.Input()
				if err != nil {
					t.Fatal(err)
				}
				file, ok := input.File("document")
				upload, pending := file.Upload()
				if !ok || !pending || !upload.Equal(files["document"][0]) {
					t.Fatal("model candidate consumed upload capability")
				}
			}
		})
	}
}
