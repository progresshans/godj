package admin

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/uploads"
)

type registryPhotograph struct {
	id            int64
	title         string
	photo         *string
	width, height *int64
}

func TestAdminModelImageDimensionsStayPrivateAndReachPersistence(t *testing.T) {
	policy := uploadTestPolicy(t)
	backend, err := storage.NewMemory(storage.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	s, err := schema.Build(schema.Definition{AppLabel: "godj_conformance", Models: []schema.Model{{Name: "article", GoName: "Article", Fields: []schema.Field{
		schema.CharField("title", "Title", 40), schema.ImageField("photo", "Photo", schema.Blank(), schema.Nullable(), schema.ImageDimensions("width", "height")),
		schema.IntegerField("width", "Width", schema.Nullable()), schema.IntegerField("height", "Height", schema.Nullable()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	current := registryPhotograph{id: 1, title: "Photo"}
	writes := 0
	initialMode := ""
	var received uploads.File
	var published []string
	nullable := func(value *int64) templates.Value {
		if value == nil {
			return templates.Null()
		}
		return templates.Integer(*value)
	}
	config := ModelConfig[registryPhotograph]{AppLabel: s.AppLabel, Slug: "articles", Model: s.Models[0], ListFields: []string{"id", "title", "photo"}, SearchFields: []string{"photo"}, Permissions: validRegistryConfig(t).Permissions,
		List: func(_ context.Context, _ auth.Principal, request ListRequest) (Page[registryPhotograph], error) {
			return Page[registryPhotograph]{Items: []registryPhotograph{current}, Total: 1, Offset: request.Offset, Limit: request.Limit}, nil
		},
		Get: func(_ context.Context, _ auth.Principal, id int64) (registryPhotograph, bool, error) {
			return current, id == current.id, nil
		},
		Snapshot: func(value registryPhotograph) (Object, error) {
			photo := templates.Null()
			if value.photo != nil {
				photo = templates.String(*value.photo)
			}
			return NewObject(value.id, value.title, map[string]templates.Value{"id": templates.Integer(value.id), "title": templates.String(value.title), "photo": photo, "width": nullable(value.width), "height": nullable(value.height)})
		},
		Initial: func(value registryPhotograph) (map[string]forms.Value, error) {
			values := map[string]forms.Value{"title": forms.String(value.title), "photo": forms.Null(), "width": forms.Null(), "height": forms.Null()}
			var err error
			if value.photo != nil {
				values["photo"], err = forms.ExistingFile(*value.photo)
			}
			if value.width != nil {
				values["width"] = forms.Integer(*value.width)
			}
			if value.height != nil {
				values["height"] = forms.Integer(*value.height)
			}
			switch initialMode {
			case "missing":
				delete(values, "width")
			case "mismatch":
				values["width"] = forms.Integer(999)
			case "invalid":
				values["width"] = forms.String("999")
			}
			return values, err
		},
		Delete: func(context.Context, auth.Principal, Mutation) (registryPhotograph, error) { return current, nil },
	}
	apply := func(ctx context.Context, bound formmodel.BoundForm) (registryPhotograph, error) {
		input, err := bound.Input()
		if err != nil {
			return registryPhotograph{}, err
		}
		next := current
		next.title, _ = input.String("title")
		file, ok := input.File("photo")
		if ok {
			if upload, present := file.Upload(); present {
				info, verified := file.Image()
				width, wok := input.Integer("width")
				height, hok := input.Integer("height")
				if !verified || !wok || !hok || width != int64(info.Width()) || height != int64(info.Height()) {
					return registryPhotograph{}, errors.New("image dimensions did not reach Admin input")
				}
				stored, err := storage.SaveUpload(ctx, backend, "photos/"+upload.Name(), upload, storage.SaveOptions{})
				if err != nil {
					return registryPhotograph{}, err
				}
				next.photo = new(stored.Name())
				next.width, next.height = new(width), new(height)
				received = upload
				published = append(published, stored.Name())
			} else {
				next.photo = new(file.Name())
				if file.Clear() {
					width, _ := input.Get("width")
					height, _ := input.Get("height")
					if !width.IsNull() || !height.IsNull() {
						return registryPhotograph{}, errors.New("clear retained image dimensions")
					}
					next.width, next.height = nil, nil
				}
			}
		}
		current = next
		writes++
		return next, nil
	}
	config.Create = func(ctx context.Context, _ auth.Principal, bound formmodel.BoundForm, _ InlineSubmission) (registryPhotograph, error) {
		return apply(ctx, bound)
	}
	config.Update = func(ctx context.Context, _ auth.Principal, _ Mutation, bound formmodel.BoundForm, _ InlineSubmission) (registryPhotograph, []string, error) {
		value, err := apply(ctx, bound)
		changed := bound.Form().Changed()
		if file, ok := bound.Form().Cleaned().File("photo"); ok {
			_, uploaded := file.Upload()
			if uploaded || file.Clear() {
				changed = append(changed, "width", "height")
			}
		}
		return value, changed, err
	}
	conflicting := config
	conflicting.Model = config.Model.Clone()
	conflicting.Model.Fields[3].Nullable = false
	conflicting.RevisionField = "width"
	if err := RegisterModel(NewBuilder(mustApps(t)), conflicting); errorCode(err) != "invalid" {
		t.Fatal("image dimension became revision owner", err)
	}
	client, _, _ := uploadTestSite(t, config, policy, auth.PrincipalAuthorizer{})
	client.login(t, "admin", "secret", "/admin/")
	for _, mode := range []string{"create", "replace", "retain", "invalid", "clear", "retain_empty"} {
		t.Run(mode, func(t *testing.T) {
			target := "/admin/articles/change/?id=1"
			if mode == "create" {
				target = "/admin/articles/add/"
			}
			page := client.do(http.MethodGet, target, nil)
			if page.Code != 200 || !strings.Contains(page.Body.String(), `type="file" name="photo" accept="image/*"`) || strings.Contains(page.Body.String(), `name="width"`) || strings.Contains(page.Body.String(), `name="height"`) {
				t.Fatal("image widget/dimension privacy", page.Code, page.Body.String())
			}
			raw := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}, "title": {"Photo"}}
			var parts []siteUploadPart
			if mode == "create" || mode == "replace" {
				width, height := 3, 2
				if mode == "replace" {
					width, height = 7, 5
				}
				var data bytes.Buffer
				if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
					t.Fatal(err)
				}
				parts = []siteUploadPart{{"photo", "same.png", data.String()}}
			}
			if mode == "invalid" {
				parts = []siteUploadPart{{"photo", "bad.png", "invalid"}}
			}
			if mode == "clear" {
				raw.Set("photo-clear", "on")
			}
			if mode == "retain" {
				raw.Set("photo", "other/private.png")
			}
			count := writes
			raw.Set("width", "999")
			raw.Set("height", "888")
			forged := siteUploadSubmit(t, client, target, raw, parts...)
			if forged.Code != 400 || writes != count {
				t.Fatal("forged dimension input bypassed Admin allowlist", forged.Code)
			}
			assertUploadTempEmpty(t, policy.TempDir)
			raw.Del("width")
			raw.Del("height")
			response := siteUploadSubmit(t, client, target, raw, parts...)
			if mode == "invalid" {
				if response.Code != 200 || writes != count || !strings.Contains(response.Body.String(), `data-error-code="invalid_image"`) || !strings.Contains(response.Body.String(), `data-file-reselect="photo"`) {
					t.Fatal("invalid image reached persistence", response.Code, writes)
				}
			} else if response.Code != http.StatusFound || writes != count+1 {
				t.Fatal("model image update", response.Code, response.Body.String())
			}
			if mode == "replace" || mode == "retain" || mode == "invalid" {
				if current.photo == nil || *current.photo != published[1] || current.width == nil || *current.width != 7 || current.height == nil || *current.height != 5 {
					t.Fatal("dimensions or reference forged/lost")
				}
			}
			if mode == "clear" || mode == "retain_empty" {
				if current.photo == nil || *current.photo != "" || current.width != nil || current.height != nil {
					t.Fatal("image clear state")
				}
			}
			assertUploadTempEmpty(t, policy.TempDir)
		})
	}
	if _, err := received.Open(t.Context()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
		t.Fatal("image outlived request", err)
	}
	for _, name := range published {
		if _, err := backend.Stat(t.Context(), name); err != nil {
			t.Fatal("clear removed published image", err)
		}
	}
	for _, mode := range []string{"missing", "mismatch", "invalid"} {
		initialMode = mode
		response := client.do(http.MethodGet, "/admin/articles/change/?id=1", nil)
		if response.Code != 500 {
			t.Fatal("bad private dimension snapshot admitted", mode, response.Code)
		}
	}
}
