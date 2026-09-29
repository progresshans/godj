package forms_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

func TestFileFieldPinnedDjangoObservation(t *testing.T) {
	var reference struct {
		Django, Python string
		Cases          []struct {
			Name                                            string
			Required, Existing, Clearable, Valid, Multipart bool
			AllowEmpty                                      bool `json:"allow_empty"`
			Maximum                                         *int
			Files                                           [][2]string
			Raw                                             map[string]string
			Errors, Changed, Calls                          []string
			Cleaned                                         *struct {
				Kind, Name string
				Size       int64
			}
		}
	}
	data, err := os.ReadFile("testdata/file-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || len(reference.Cases) != 22 {
		t.Fatal("wrong native fixture")
	}
	for _, item := range reference.Cases {
		t.Run(item.Name, func(t *testing.T) {
			var calls []string
			options := []forms.FieldOption{forms.WithRequired(item.Required), forms.WithAllowEmptyFile(item.AllowEmpty), forms.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
				file, ok := value.AsFile()
				if !ok {
					t.Fatal("non-file validator")
				}
				calls = append(calls, file.Name())
				return validation.Errors{}
			}))}
			if item.Maximum != nil {
				options = append(options, forms.WithMaxLength(*item.Maximum))
			}
			if !item.Clearable {
				options = append(options, forms.WithWidget(forms.FileInput))
			}
			field, err := forms.FileField("document", options...)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			raw := map[string][]string{}
			for k, v := range item.Raw {
				raw[k] = []string{v}
			}
			files := map[string][]uploads.File{}
			for _, pair := range item.Files {
				file, err := uploads.NewFile(pair[0], "text/plain", []byte(pair[1]))
				if err != nil {
					t.Fatal(err)
				}
				files["document"] = append(files["document"], file)
			}
			initial := map[string]forms.Value{}
			if item.Existing {
				initial["document"], err = forms.ExistingFile("private/old.txt")
				if err != nil {
					t.Fatal(err)
				}
			}
			bound, err := spec.Bind(forms.NewDataWithFiles(raw, files), initial)
			if err != nil {
				t.Fatal(err)
			}
			var codes []string
			for _, problem := range bound.Errors().All() {
				codes = append(codes, string(problem.Code()))
			}
			if item.Name == "repeated_file" || item.Name == "non_boolean_clear" {
				code := "multiple"
				if item.Name == "non_boolean_clear" {
					code = "invalid"
				}
				if !item.Valid || bound.Valid() || !reflect.DeepEqual(codes, []string{code}) || len(calls) != 0 {
					t.Fatal("explicit stricter input policy", codes, calls)
				}
				return
			}
			if spec.IsMultipart() != item.Multipart || bound.Valid() != item.Valid || strings.Join(codes, ",") != strings.Join(item.Errors, ",") || strings.Join(bound.Changed(), ",") != strings.Join(item.Changed, ",") || strings.Join(calls, ",") != strings.Join(item.Calls, ",") {
				t.Fatalf("valid=%v errors=%v changed=%v calls=%v; native=%+v", bound.Valid(), codes, bound.Changed(), calls, item)
			}
			if !item.Valid {
				return
			}
			value, present := bound.Cleaned().Get("document")
			if !present {
				t.Fatal("missing cleaned file")
			}
			if item.Cleaned.Kind == "null" {
				if !value.IsNull() {
					t.Fatal("optional file was not null")
				}
				return
			}
			file, ok := value.AsFile()
			if !ok {
				t.Fatal("wrong cleaned kind")
			}
			if item.Cleaned.Kind == "clear" {
				if !file.Clear() {
					t.Fatal("clear lost")
				}
				return
			}
			if file.Name() != item.Cleaned.Name || file.Clear() {
				t.Fatal("file identity changed")
			}
			upload, present := file.Upload()
			if present != (item.Cleaned.Kind == "upload") || present && upload.Size() != item.Cleaned.Size {
				t.Fatal("new/retained file distinction lost")
			}
		})
	}
}

func TestFileFormsetOwnsPrefixesEmptyRowsAndReadonly(t *testing.T) {
	file, err := forms.FileField("document")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{file})
	if err != nil {
		t.Fatal(err)
	}
	c := forms.DefaultSetConfig()
	c.Prefix = "items"
	c.CanDelete = true
	setSpec, err := forms.NewSetSpec(spec, c)
	if err != nil {
		t.Fatal(err)
	}
	upload, err := uploads.NewFile("new.txt", "text/plain", []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string][]string{"items-TOTAL_FORMS": {"2"}, "items-INITIAL_FORMS": {"0"}}
	files := map[string][]uploads.File{"items-0-document": {upload}, "other-0-document": {upload}}
	data := forms.NewDataWithFiles(raw, files)
	files["items-0-document"][0] = uploads.File{}
	set, err := setSpec.Bind(data, nil)
	if err != nil || !set.Valid() || !setSpec.IsMultipart() {
		t.Fatal(err, set.NonFormErrors())
	}
	if !set.Changed() || len(set.Forms()[0].Form().Changed()) != 1 || len(set.Forms()[1].Form().Cleaned().All()) != 0 {
		t.Fatal("file did not activate only its own extra row")
	}
	got, ok := set.Forms()[0].Form().Cleaned().File("document")
	if !ok {
		t.Fatal("file prefix lost")
	}
	read, ok := got.Upload()
	if !ok || !read.Equal(upload) {
		t.Fatal("file capability changed")
	}
	returned, _ := data.Files("items-0-document")
	returned[0] = uploads.File{}
	if kept, _ := data.Files("items-0-document"); !kept[0].Equal(upload) {
		t.Fatal("file container alias")
	}
	old, err := forms.ExistingFile("stored/old.txt")
	if err != nil {
		t.Fatal(err)
	}
	c.ReadOnlyInitial = true
	setSpec, err = forms.NewSetSpec(spec, c)
	if err != nil {
		t.Fatal(err)
	}
	raw["items-INITIAL_FORMS"] = []string{"1"}
	set, err = setSpec.Bind(forms.NewDataWithFiles(raw, map[string][]uploads.File{"items-0-document": {upload}}), []map[string]forms.Value{{"document": old}})
	if err != nil || !set.Valid() {
		t.Fatal(err)
	}
	kept, ok := set.Forms()[0].Form().Cleaned().File("document")
	if !ok || kept.Name() != "stored/old.txt" {
		t.Fatal("readonly file replaced")
	}
	if _, present := kept.Upload(); present {
		t.Fatal("readonly row adopted upload")
	}
	if err := func() error {
		bad, _ := uploads.NewFile("empty.txt", "", nil)
		raw["items-1-DELETE"] = []string{"on"}
		result, e := setSpec.Bind(forms.NewDataWithFiles(raw, map[string][]uploads.File{"items-1-document": {bad}}), []map[string]forms.Value{{"document": old}})
		if e == nil && !result.Valid() {
			return fmt.Errorf("deleted invalid upload made set invalid")
		}
		return e
	}(); err != nil {
		t.Fatal(err)
	}
}

func TestFileValuesPrivacyAndConcurrentBinding(t *testing.T) {
	field, err := forms.FileField("document", forms.WithRequired(false))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	upload, err := uploads.NewFile("private-name.txt", "", []byte("private content"))
	if err != nil {
		t.Fatal(err)
	}
	data := forms.NewDataWithFiles(nil, map[string][]uploads.File{"document": {upload}})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			form, e := spec.Bind(data, nil)
			if e != nil || !form.Valid() {
				t.Error(e)
				return
			}
			file, _ := form.Cleaned().File("document")
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
				for _, value := range []any{data, form, form.Cleaned(), file} {
					if strings.Contains(fmt.Sprintf(verb, value), "private") {
						t.Error("format disclosed upload")
					}
				}
			}
			f, _ := file.Upload()
			r, e := f.Open(t.Context())
			if e != nil {
				t.Error(e)
				return
			}
			defer r.Close()
			content, e := io.ReadAll(r)
			if e != nil || string(content) != "private content" {
				t.Error("independent reads", e)
			}
		})
	}
	wg.Wait()
	if _, err = forms.CharField("bad", forms.WithAllowEmptyFile(true)); err == nil {
		t.Fatal("file-only option accepted by string field")
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{"document-clear": {"on", "false"}}), nil)
	if err != nil || bound.Valid() {
		t.Fatal("duplicate clear accepted", err)
	}
}
