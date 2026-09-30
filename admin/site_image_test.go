package admin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

func TestAdminImageUploadVerifiedContentAndStorage(t *testing.T) {
	payloads := map[string]string{}
	for _, source := range []struct{ name, prefix string }{{"image-codecs-django61.json", ""}, {"apng-django61.json", "apng_"}} {
		var reference struct{ Payloads map[string]string }
		raw, err := os.ReadFile("../uploads/testdata/" + source.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &reference); err != nil {
			t.Fatal(err)
		}
		for name, value := range reference.Payloads {
			payloads[source.prefix+name] = value
		}
	}
	payload := func(name string) []byte {
		t.Helper()
		content, err := base64.StdEncoding.DecodeString(payloads[name])
		if err != nil || len(content) == 0 {
			t.Fatal("missing native image codec", name, err)
		}
		return content
	}
	var pngContent bytes.Buffer
	if err := png.Encode(&pngContent, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	variants := []struct {
		name, extension, mime string
		frames                int
		content               []byte
	}{
		{"png", ".png", "image/png", 1, pngContent.Bytes()},
		{"bmp", ".bmp", "image/bmp", 1, payload("bmp_palette")},
		{"dib", ".dib", "image/bmp", 1, payload("dib_rgb")},
		{"tiff", ".tiff", "image/tiff", 2, payload("tiff_pages")},
		{"apng", ".apng", "image/png", 2, payload("apng_rgba")},
		{"apng_poster", ".apng", "image/png", 3, payload("apng_poster")},
	}
	for _, command := range []bool{false, true} {
		for _, backendName := range []string{"filesystem", "memory"} {
			for _, variant := range variants {
				t.Run(map[bool]string{false: "create", true: "command"}[command]+"/"+backendName+"/"+variant.name, func(t *testing.T) {
					encoded := bytes.NewBuffer(variant.content)
					var backend storage.Backend
					var close func() error
					if backendName == "memory" {
						memory, err := storage.NewMemory(storage.MemoryConfig{})
						if err != nil {
							t.Fatal(err)
						}
						backend, close = memory, memory.Close
					} else {
						filesystem, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
						if err != nil {
							t.Fatal(err)
						}
						backend, close = filesystem, filesystem.Close
					}
					t.Cleanup(func() {
						if err := close(); err != nil {
							t.Error(err)
						}
					})
					policy := uploadTestPolicy(t)
					config := validRegistryConfig(t)
					field, err := forms.ImageField("photo")
					if err != nil {
						t.Fatal(err)
					}
					spec, err := forms.NewSpec([]forms.Field{field})
					if err != nil {
						t.Fatal(err)
					}
					var received uploads.File
					var saved storage.Info
					calls := 0
					consume := func(ctx context.Context, values forms.Values) error {
						calls++
						value, ok := values.File("photo")
						if !ok {
							return errors.New("image command value missing")
						}
						info, verified := value.Image()
						if !verified || info.Width() != 3 || info.Height() != 2 || info.ContentType() != variant.mime || info.Frames() != variant.frames {
							return errors.New("unverified image reached publication")
						}
						received, ok = value.Upload()
						if !ok {
							return errors.New("image upload capability missing")
						}
						var err error
						saved, err = storage.SaveUpload(ctx, backend, "images/"+received.Name(), received, storage.SaveOptions{})
						return err
					}
					target := "/admin/articles/add/"
					if command {
						config.Commands = []CommandConfig{{Name: "attach", Label: "Attach", Permission: config.Permissions.Change, Form: spec, Run: func(ctx context.Context, _ auth.Principal, mutation Mutation, values forms.Values) (CommandResult, error) {
							if err := consume(ctx, values); err != nil {
								return CommandResult{}, err
							}
							return CommandResult{ID: mutation.ID}, nil
						}}}
						target = "/admin/articles/command/attach/?id=1"
					} else {
						config.CreateForm = &FormConfig{Definition: formmodel.Definition{ExtraFields: []forms.Field{field}}}
						original := config.Create
						config.Create = func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm, inlines InlineSubmission) (registryArticle, error) {
							values, err := bound.Input()
							if err != nil {
								return registryArticle{}, err
							}
							if err = consume(ctx, values); err != nil {
								return registryArticle{}, err
							}
							return original(ctx, actor, bound, inlines)
						}
					}
					client, _, _ := uploadTestSite(t, config, policy, auth.PrincipalAuthorizer{})
					client.login(t, "admin", "secret", "/admin/")
					page := client.do(http.MethodGet, target, nil)
					if page.Code != 200 || !strings.Contains(page.Body.String(), `<input type="file" name="photo" accept="image/*" required>`) || !strings.Contains(page.Body.String(), `enctype="multipart/form-data"`) {
						t.Fatal("image widget unavailable", page.Code, page.Body.String())
					}
					values := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}}
					if !command {
						values.Set("title", "Image upload")
					}
					failures := []struct{ name, content, code string }{{"photo" + variant.extension, "text pretending to be an image", "invalid_image"}, {"photo.txt", encoded.String(), "invalid_extension"}}
					if variant.name == "tiff" {
						failures = append(failures, struct{ name, content, code string }{"later.tiff", string(payload("tiff_late_truncated")), "invalid_image"})
					}
					if strings.HasPrefix(variant.name, "apng") {
						for _, broken := range []string{"apng_late_pixel_error", "apng_later_control_sequence"} {
							failures = append(failures, struct{ name, content, code string }{"later.apng", string(payload(broken)), "invalid_image"})
						}
					}
					for _, test := range failures {
						response := siteUploadSubmit(t, client, target, values, siteUploadPart{"photo", test.name, test.content})
						if response.Code != 200 || calls != 0 || !strings.Contains(response.Body.String(), `data-error-code="`+test.code+`"`) || !strings.Contains(response.Body.String(), `data-file-reselect="photo"`) {
							t.Fatal("image input error lost", response.Code, calls, response.Body.String())
						}
						assertUploadTempEmpty(t, policy.TempDir)
					}
					denied := url.Values{}
					for key, value := range values {
						denied[key] = append([]string(nil), value...)
					}
					denied.Set("csrfmiddlewaretoken", "forged")
					response := siteUploadSubmit(t, client, target, denied, siteUploadPart{"photo", "photo" + variant.extension, encoded.String()})
					if response.Code != 403 || calls != 0 {
						t.Fatal("CSRF bypass", response.Code, calls)
					}
					assertUploadTempEmpty(t, policy.TempDir)
					response = siteUploadSubmit(t, client, target, values, siteUploadPart{"photo", "photo" + variant.extension, encoded.String()})
					if response.Code != http.StatusFound || calls != 1 {
						t.Fatal("verified image did not publish", response.Code, calls, response.Body.String())
					}
					assertUploadTempEmpty(t, policy.TempDir)
					if _, err = received.Open(t.Context()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
						t.Fatal("image outlived request", err)
					}
					reader, err := backend.Open(t.Context(), saved.Name())
					if err != nil {
						t.Fatal(err)
					}
					content, err := io.ReadAll(reader)
					closeErr := reader.Close()
					if err != nil || closeErr != nil || !bytes.Equal(content, encoded.Bytes()) {
						t.Fatal("published content changed", err, closeErr)
					}
				})
			}
		}
	}
}

func TestAdminImageReadFailureIsOperational(t *testing.T) {
	policy := uploadTestPolicy(t)
	config := validRegistryConfig(t)
	field, _ := forms.ImageField("photo")
	calls := 0
	// A title validator simulates a disappeared staging file after parsing, before
	// ImageField opens its own reader. The real handler must surface 500, not an
	// invalid-image form error or a successful write callback.
	config.CreateForm = &FormConfig{Definition: formmodel.Definition{ExtraFields: []forms.Field{field}, Overrides: []formmodel.Override{formmodel.OverrideField("title", formmodel.WithValidators(forms.FieldValidatorFunc(func(forms.Value) validation.Errors {
		_ = filepath.WalkDir(policy.TempDir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				return os.Remove(path)
			}
			return nil
		})
		return validation.Errors{}
	})))}}}
	original := config.Create
	config.Create = func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm, inlines InlineSubmission) (registryArticle, error) {
		calls++
		return original(ctx, actor, bound, inlines)
	}
	client, _, _ := uploadTestSite(t, config, policy, auth.PrincipalAuthorizer{})
	client.login(t, "admin", "secret", "/admin/")
	page := client.do(http.MethodGet, "/admin/articles/add/", nil)
	values := url.Values{"title": {"Image read failure"}, "csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	response := siteUploadSubmit(t, client, "/admin/articles/add/", values, siteUploadPart{"photo", "photo.png", encoded.String()})
	if response.Code != 500 || calls != 0 {
		t.Fatal("operational image read failure became input diagnostics", response.Code, calls)
	}
	assertUploadTempEmpty(t, policy.TempDir)
}
