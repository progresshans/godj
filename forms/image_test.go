package forms_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

type imageReference struct {
	Django, Python, Pillow string
	Payloads               map[string]string
	Cases                  []struct {
		Name, Filename                              string
		Payload                                     *string
		Required, Existing, Clear, Valid, Multipart bool
		AllowEmpty                                  bool `json:"allow_empty"`
		Errors, Changed                             []string
		Calls                                       []struct {
			Name, Format  string
			ContentType   string `json:"content_type"`
			Width, Height int
		}
		Cleaned *struct {
			Kind, Name, Format    string
			ContentType           string `json:"content_type"`
			Width, Height, Cursor int
			BytesPreserved        bool `json:"bytes_preserved"`
		}
	}
}

func imageFixture(t *testing.T) imageReference {
	t.Helper()
	data, err := os.ReadFile("testdata/image-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var result imageReference
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Django != "6.1" || result.Python != "3.14.3" || result.Pillow != "12.3.0" || len(result.Cases) != 32 {
		t.Fatal("wrong pinned image fixture")
	}
	return result
}
func imageUpload(t *testing.T, fixture imageReference, payload, name string) (uploads.File, []byte) {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(fixture.Payloads[payload])
	if err != nil {
		t.Fatal(err)
	}
	file, err := uploads.NewFile(name, "application/x-untrusted", data)
	if err != nil {
		t.Fatal(err)
	}
	return file, data
}

func TestImageFieldPinnedDjangoObservation(t *testing.T) {
	fixture := imageFixture(t)
	for _, item := range fixture.Cases {
		t.Run(item.Name, func(t *testing.T) {
			var calls []string
			field, err := forms.ImageField("photo", forms.WithRequired(item.Required), forms.WithAllowEmptyFile(item.AllowEmpty), forms.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
				file, ok := value.AsFile()
				if !ok {
					t.Fatal("validator lost file")
				}
				info, ok := file.Image()
				if !ok {
					t.Fatal("validator saw an unverified upload")
				}
				calls = append(calls, fmt.Sprintf("%s:%s:%dx%d", file.Name(), info.ContentType(), info.Width(), info.Height()))
				return validation.Errors{}
			})))
			if err != nil {
				t.Fatal(err)
			}
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			if field.Kind() != forms.FieldImage || !field.IsFile() || !spec.IsMultipart() {
				t.Fatal("image transport lost")
			}
			var original uploads.File
			var payload []byte
			files := map[string][]uploads.File{}
			raw := map[string][]string{}
			initial := map[string]forms.Value{}
			if item.Payload != nil {
				original, payload = imageUpload(t, fixture, *item.Payload, item.Filename)
				files["photo"] = []uploads.File{original}
			}
			if item.Clear {
				raw["photo-clear"] = []string{"on"}
			}
			if item.Existing {
				initial["photo"], err = forms.ExistingFile("retained/old.png")
				if err != nil {
					t.Fatal(err)
				}
			}
			bound, err := spec.Bind(t.Context(), forms.NewDataWithFiles(raw, files), initial)
			if err != nil {
				t.Fatal(err)
			}
			var codes []string
			for _, e := range bound.Errors().All() {
				codes = append(codes, string(e.Code()))
			}
			// Animated WebP policy and full GIF decoding are explicit differences,
			// never counted as native parity. Pillow verify() accepts these broken tails.
			different := item.Name == "animated_webp" || item.Name == "truncated_gif" || item.Name == "corrupt_later_gif"
			if different {
				if !item.Valid || bound.Valid() || strings.Join(codes, ",") != "invalid_image" || len(calls) != 0 {
					t.Fatal("explicit image policy", bound.Valid(), codes, calls)
				}
				return
			}
			if bound.Valid() != item.Valid || strings.Join(codes, ",") != strings.Join(item.Errors, ",") || strings.Join(bound.Changed(), ",") != strings.Join(item.Changed, ",") {
				t.Fatal("native result mismatch", bound.Valid(), codes, bound.Changed(), item)
			}
			if len(calls) != len(item.Calls) {
				t.Fatal("validator lifecycle", calls, item.Calls)
			}
			for i, call := range item.Calls {
				want := fmt.Sprintf("%s:%s:%dx%d", call.Name, call.ContentType, call.Width, call.Height)
				if calls[i] != want {
					t.Fatal("validator metadata", calls, want)
				}
			}
			if !bound.Valid() {
				if _, ok := bound.Cleaned().Get("photo"); ok {
					t.Fatal("rejected image remains cleaned")
				}
				return
			}
			value, present := bound.Cleaned().Get("photo")
			if !present {
				t.Fatal("cleaned value missing")
			}
			if item.Cleaned.Kind == "null" {
				if !value.IsNull() {
					t.Fatal("missing optional image")
				}
				return
			}
			file, ok := value.AsFile()
			if !ok {
				t.Fatal("file value lost")
			}
			info, verified := file.Image()
			if item.Cleaned.Kind != "upload" {
				if verified || file.Clear() != (item.Cleaned.Kind == "clear") {
					t.Fatal("retained name was read or clear lost")
				}
				return
			}
			want := item.Cleaned
			if !verified || info.FormatName() != want.Format || info.ContentType() != want.ContentType || info.Width() != want.Width || info.Height() != want.Height || want.Cursor != 0 || !want.BytesPreserved {
				t.Fatal("verified metadata differs", info.FormatName(), info.Width(), info.Height(), want)
			}
			upload, ok := file.Upload()
			if !ok || !upload.Equal(original) || upload.ContentType() != "application/x-untrusted" {
				t.Fatal("borrowed upload metadata mutated")
			}
			reader, err := upload.Open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(reader)
			closeErr := reader.Close()
			if err != nil || closeErr != nil || !bytes.Equal(got, payload) {
				t.Fatal("image validation changed bytes/cursor", err, closeErr)
			}
		})
	}
}

func TestImageFieldBudgetsConfigurationAndConcurrency(t *testing.T) {
	fixture := imageFixture(t)
	upload, _ := imageUpload(t, fixture, "png", "photo.png")
	animated, _ := imageUpload(t, fixture, "animated_gif", "photo.gif")
	for _, test := range []struct {
		name   string
		limits uploads.ImageLimits
		file   uploads.File
		code   string
	}{
		{"bytes", uploads.ImageLimits{MaxBytes: 1}, upload, "image_bytes"}, {"width", uploads.ImageLimits{MaxWidth: 2}, upload, "image_pixels"}, {"height", uploads.ImageLimits{MaxHeight: 1}, upload, "image_pixels"}, {"pixels", uploads.ImageLimits{MaxPixels: 5}, upload, "image_pixels"}, {"frames", uploads.ImageLimits{MaxFrames: 1}, animated, "image_frames"}, {"aggregate", uploads.ImageLimits{MaxTotalPixels: 11}, animated, "image_pixels"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			field, err := forms.ImageField("photo", forms.WithImageLimits(test.limits), forms.WithValidators(forms.FieldValidatorFunc(func(forms.Value) validation.Errors { calls++; return validation.Errors{} })))
			if err != nil {
				t.Fatal(err)
			}
			spec, _ := forms.NewSpec([]forms.Field{field})
			bound, err := spec.Bind(t.Context(), forms.NewDataWithFiles(nil, map[string][]uploads.File{"photo": {test.file}}), nil)
			if err != nil || bound.Valid() || bound.Errors().Len() != 1 || string(bound.Errors().All()[0].Code()) != test.code || calls != 0 {
				t.Fatal("budget failed", err, bound.Errors(), calls)
			}
		})
	}
	for _, constructor := range []func(string, ...forms.FieldOption) (forms.Field, error){forms.CharField, forms.FileField} {
		if _, err := constructor("photo", forms.WithImageLimits(uploads.ImageLimits{})); err == nil {
			t.Fatal("image budget accepted on another kind")
		}
	}
	if _, err := forms.ImageField("photo", forms.WithImageLimits(uploads.ImageLimits{MaxBytes: -1})); err == nil {
		t.Fatal("invalid image budget")
	}
	if _, err := forms.ImageField("photo", forms.WithWidget(forms.TextInput)); err == nil {
		t.Fatal("text widget accepted")
	}
	field, _ := forms.ImageField("photo")
	limits, ok := field.ImageLimits()
	if !ok || limits.MaxBytes <= 0 {
		t.Fatal("missing resolved policy")
	}
	limits.MaxBytes = 1
	spec, _ := forms.NewSpec([]forms.Field{field})
	data := forms.NewDataWithFiles(nil, map[string][]uploads.File{"photo": {upload}})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			bound, err := spec.Bind(t.Context(), data, nil)
			if err != nil || !bound.Valid() {
				t.Error("shared image spec", err, bound.Errors())
				return
			}
			file, _ := bound.Cleaned().File("photo")
			info, _ := file.Image()
			if info.Width() != 3 {
				t.Error("metadata crossed bindings")
			}
		})
	}
	wg.Wait()
	ordinary, _ := forms.FileField("photo")
	plain, _ := forms.NewSpec([]forms.Field{ordinary})
	unchecked, err := plain.Bind(t.Context(), data, nil)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := spec.Bind(t.Context(), data, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := unchecked.Cleaned().Get("photo")
	b, _ := checked.Cleaned().Get("photo")
	if a.Equal(b) {
		t.Fatal("verified metadata absent from value equality")
	}
}

func TestBindingContextAndImageFormsetLifecycle(t *testing.T) {
	fixture := imageFixture(t)
	upload, _ := imageUpload(t, fixture, "png", "photo.png")
	bad, _ := imageUpload(t, fixture, "text", "photo.png")
	calls := 0
	field, _ := forms.ImageField("photo", forms.WithValidators(forms.FieldValidatorFunc(func(forms.Value) validation.Errors { calls++; return validation.Errors{} })))
	spec, _ := forms.NewSpec([]forms.Field{field})
	data := forms.NewDataWithFiles(nil, map[string][]uploads.File{"photo": {upload}})
	if _, err := spec.Bind(nil, data, nil); err == nil {
		t.Fatal("nil binding context")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := spec.Bind(cancelled, data, nil); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled binding ran", err, calls)
	}
	policy := forms.DefaultSetConfig()
	policy.Prefix = "pictures"
	policy.MaxForms = 2
	policy.AbsoluteMax = 2
	policy.CanDelete = true
	setSpec, _ := forms.NewSetSpec(spec, policy)
	raw := map[string][]string{"pictures-TOTAL_FORMS": {"2"}, "pictures-INITIAL_FORMS": {"0"}}
	set, err := setSpec.Bind(t.Context(), forms.NewDataWithFiles(raw, map[string][]uploads.File{"pictures-0-photo": {upload}, "other-0-photo": {bad}}), nil)
	if err != nil || !set.Valid() || calls != 1 || len(set.Forms()[1].Form().Cleaned().All()) != 0 {
		t.Fatal("prefix or empty extra lifecycle", err, calls)
	}
	value, _ := set.Forms()[0].Form().Cleaned().File("photo")
	if _, verified := value.Image(); !verified {
		t.Fatal("formset lost verification")
	}
	policy.ReadOnlyInitial = true
	setSpec, _ = forms.NewSetSpec(spec, policy)
	raw["pictures-INITIAL_FORMS"] = []string{"1"}
	existing, _ := forms.ExistingFile("old.png")
	set, err = setSpec.Bind(t.Context(), forms.NewDataWithFiles(raw, map[string][]uploads.File{"pictures-0-photo": {bad}}), []map[string]forms.Value{{"photo": existing}})
	if err != nil || !set.Valid() || calls != 1 || !set.Forms()[0].Form().ReadOnly() {
		t.Fatal("readonly row read an image", err, calls)
	}
	// Processor cancellation after the last row must stop set-level validators.
	policy.ReadOnlyInitial = false
	setSpec, _ = forms.NewSetSpec(spec, policy)
	raw["pictures-TOTAL_FORMS"] = []string{"1"}
	raw["pictures-INITIAL_FORMS"] = []string{"0"}
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	_, err = setSpec.BindWith(ctx, forms.NewDataWithFiles(raw, map[string][]uploads.File{"pictures-0-photo": {upload}}), nil, forms.SetProcessor{Form: func(row forms.SetForm) (forms.Form, error) { stop(); return row.Form(), nil }})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("set swallowed context cancellation", err)
	}
}

type imageBindingContext struct {
	context.Context
	checks int
}

func (ctx *imageBindingContext) Err() error {
	ctx.checks++
	if ctx.checks >= 7 {
		return context.Canceled
	}
	return nil
}
func TestImageBindingPassesContextToContentReads(t *testing.T) {
	fixture := imageFixture(t)
	file, _ := imageUpload(t, fixture, "png", "photo.png")
	field, _ := forms.ImageField("photo")
	spec, _ := forms.NewSpec([]forms.Field{field})
	ctx := &imageBindingContext{Context: t.Context()}
	bound, err := spec.Bind(ctx, forms.NewDataWithFiles(nil, map[string][]uploads.File{"photo": {file}}), nil)
	if !errors.Is(err, context.Canceled) || bound.Bound() || ctx.checks < 7 {
		t.Fatal("request context did not reach image reader", err, ctx.checks)
	}
}
