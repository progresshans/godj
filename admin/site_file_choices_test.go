package admin

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
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

func TestAdminStoredImageChoicesRespectHTTPAdmissionAndCurrentPolicy(t *testing.T) {
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
		var data bytes.Buffer
		if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 3+4*index, 2+3*index))); err != nil {
			t.Fatal(err)
		}
		if _, err := root.Save(t.Context(), name, &data, storage.SaveOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	inspector, err := storage.NewImageInspector(root)
	if err != nil {
		t.Fatal(err)
	}
	reads, writes := 0, 0
	inspect := func(ctx context.Context, name string, limits uploads.ImageLimits) (uploads.ImageInfo, error) {
		reads++
		return inspector.Inspect(ctx, name, limits)
	}
	s, err := schema.Build(schema.Definition{AppLabel: "godj_conformance", Models: []schema.Model{{Name: "article", GoName: "Article", Fields: []schema.Field{
		schema.CharField("title", "Title", 40),
		schema.ImageField("photo", "Photo", schema.Blank(), schema.Nullable(), schema.ImageDimensions("width", "height"), schema.Choices(schema.Choice("images/a.png", "<A&>"), schema.Choice("images/b.png", "B"), schema.Choice("images/missing.png", "Missing"))),
		schema.IntegerField("width", "Width", schema.Nullable()), schema.IntegerField("height", "Height", schema.Nullable()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	current := registryPhotograph{id: 1, title: "Selected", photo: new("images/a.png"), width: new(int64(19)), height: new(int64(23))}
	config := ModelConfig[registryPhotograph]{AppLabel: s.AppLabel, Slug: "articles", Model: s.Models[0], ListFields: []string{"id", "title", "photo"}, Permissions: validRegistryConfig(t).Permissions,
		FormOverrides: []formmodel.Override{formmodel.OverrideField("photo", formmodel.WithImageChoiceInspector(inspect))},
		List: func(_ context.Context, _ auth.Principal, request ListRequest) (Page[registryPhotograph], error) {
			return Page[registryPhotograph]{Items: []registryPhotograph{current}, Total: 1, Offset: request.Offset, Limit: request.Limit}, nil
		},
		Get: func(_ context.Context, _ auth.Principal, id int64) (registryPhotograph, bool, error) {
			return current, id == current.id, nil
		},
		Snapshot: func(value registryPhotograph) (Object, error) {
			return NewObject(value.id, value.title, map[string]templates.Value{"id": templates.Integer(value.id), "title": templates.String(value.title), "photo": templates.String(*value.photo), "width": templates.Integer(*value.width), "height": templates.Integer(*value.height)})
		},
		Initial: func(value registryPhotograph) (map[string]forms.Value, error) {
			file, err := forms.ExistingFile(*value.photo)
			return map[string]forms.Value{"title": forms.String(value.title), "photo": file, "width": forms.Integer(*value.width), "height": forms.Integer(*value.height)}, err
		},
		Delete: func(context.Context, auth.Principal, Mutation) (registryPhotograph, error) { return current, nil },
	}
	apply := func(bound formmodel.BoundForm) (registryPhotograph, error) {
		values, err := bound.Input()
		if err != nil {
			return registryPhotograph{}, err
		}
		next := current
		next.title, _ = values.String("title")
		if file, present := values.File("photo"); present {
			if _, upload := file.Upload(); upload {
				return registryPhotograph{}, errors.New("choice became upload")
			}
			if info, verified := file.Image(); verified {
				width, wok := values.Integer("width")
				height, hok := values.Integer("height")
				if !wok || !hok || width != int64(info.Width()) || height != int64(info.Height()) {
					return registryPhotograph{}, errors.New("dimensions absent from Admin candidate")
				}
				next.photo, next.width, next.height = new(file.Name()), new(width), new(height)
			}
		}
		current = next
		writes++
		return next, nil
	}
	config.Create = func(_ context.Context, _ auth.Principal, bound formmodel.BoundForm, _ InlineSubmission) (registryPhotograph, error) {
		return apply(bound)
	}
	config.Update = func(_ context.Context, _ auth.Principal, _ Mutation, bound formmodel.BoundForm, _ InlineSubmission) (registryPhotograph, []string, error) {
		value, err := apply(bound)
		changed := bound.Form().Changed()
		if file, present := bound.Form().Cleaned().File("photo"); present {
			if _, verified := file.Image(); verified {
				changed = append(changed, "width", "height")
			}
		}
		return value, changed, err
	}
	policy := uploadTestPolicy(t)
	client, _, _ := uploadTestSite(t, config, policy, auth.PrincipalAuthorizer{})
	client.login(t, "admin", "secret", "/admin/")
	for _, target := range []string{"/admin/articles/add/", "/admin/articles/change/?id=1"} {
		page := client.do(http.MethodGet, target, nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), `<select name="photo"`) || !strings.Contains(page.Body.String(), `&lt;A&amp;&gt;`) || strings.Contains(page.Body.String(), `type="file"`) || strings.Contains(page.Body.String(), `multipart/form-data`) {
			t.Fatal("stored-image select rendering", page.Code, page.Body.String())
		}
		if reads != 0 || writes != 0 {
			t.Fatal("unbound choice opened storage")
		}
	}
	target := "/admin/articles/change/?id=1"
	page := client.do(http.MethodGet, target, nil)
	if !strings.Contains(page.Body.String(), `value="images/a.png" selected`) {
		t.Fatal("initial file name not selected", page.Body.String())
	}
	token := siteCSRFToken(t, page.Body.String())
	for _, tc := range []struct {
		name, selected, code string
		status               int
		extra                url.Values
		upload               bool
	}{
		{"unknown", "images/private.png", "invalid_choice", 200, nil, false},
		{"duplicate", "images/a.png", "multiple", 200, url.Values{"photo": {"images/a.png", "images/b.png"}}, false},
		{"spoof_dimensions", "images/a.png", "", 400, url.Values{"width": {"999"}}, false},
		{"clear_control", "images/a.png", "", 400, url.Values{"photo-clear": {"on"}}, false},
		{"csrf", "images/a.png", "", 403, url.Values{"csrfmiddlewaretoken": {"wrong"}}, false},
		{"upload", "images/a.png", "", 400, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := url.Values{"csrfmiddlewaretoken": {token}, "title": {"Selected"}, "photo": {tc.selected}}
			for name, value := range tc.extra {
				raw[name] = value
			}
			var response *httptest.ResponseRecorder
			if tc.upload {
				response = siteUploadSubmit(t, client, target, raw, siteUploadPart{"photo", "injected.png", "not an image"})
			} else {
				response = client.do(http.MethodPost, target, raw)
			}
			if response.Code != tc.status || reads != 0 || writes != 0 {
				t.Fatal("input bypassed admission", response.Code, reads, writes, response.Body.String())
			}
			if tc.code != "" && !strings.Contains(response.Body.String(), `data-error-code="`+tc.code+`"`) {
				t.Fatal("choice error not redisplayed", response.Body.String())
			}
			assertUploadTempEmpty(t, policy.TempDir)
		})
	}
	for _, route := range []string{"/admin/articles/add/", target} {
		beforeReads, beforeWrites := reads, writes
		page := client.do(http.MethodGet, route, nil)
		response := client.do(http.MethodPost, route, url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}, "title": {"Selected"}, "photo": {"images/b.png"}})
		if response.Code != 302 || writes != beforeWrites+1 || reads <= beforeReads || *current.photo != "images/b.png" || *current.width != 7 || *current.height != 5 {
			t.Fatal("image selection failed", response.Code, response.Body.String(), reads, writes)
		}
	}
	beforeReads, beforeWrites := reads, writes
	response := client.do(http.MethodPost, target, url.Values{"csrfmiddlewaretoken": {token}, "title": {"Selected"}, "photo": {"images/missing.png"}})
	if response.Code != 500 || writes != beforeWrites || reads != beforeReads+1 || strings.Contains(response.Body.String(), "images/missing.png") {
		t.Fatal("storage failure lost execution boundary", response.Code, response.Body.String(), reads, writes)
	}
	viewer := newSiteHTTPClient(client.application)
	viewer.login(t, "viewer", "secret", "/admin/")
	view := viewer.do(http.MethodGet, target, nil)
	beforeReads = reads
	response = viewer.do(http.MethodPost, target, url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, view.Body.String())}, "title": {"Changed"}, "photo": {"images/a.png"}})
	if response.Code != 403 || reads != beforeReads || writes != beforeWrites {
		t.Fatal("readonly principal inspected or wrote choice", response.Code)
	}
}
