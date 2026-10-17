package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
)

func TestAdminModelFileCreateChangeRetentionAndClear(t *testing.T) {
	policy := uploadTestPolicy(t)
	root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	config := validRegistryConfig(t)
	for i := range config.Model.Fields {
		field := &config.Model.Fields[i]
		if field.Name == "summary" {
			field.Kind, field.MaxLength, field.Blank = ir.FieldFile, 100, true
		}
	}
	current := registryArticle{id: 1, title: "Document"}
	config.Get = func(_ context.Context, _ auth.Principal, id int64) (registryArticle, bool, error) {
		return current, id == current.id, nil
	}
	initial := config.Initial
	config.Initial = func(value registryArticle) (map[string]forms.Value, error) {
		values, err := initial(value)
		if err != nil {
			return nil, err
		}
		if value.summary != nil {
			values["summary"], err = forms.ExistingFile(*value.summary)
		}
		return values, err
	}
	var incoming uploads.File
	var names []string
	writes := 0
	apply := func(ctx context.Context, bound formmodel.BoundForm) (registryArticle, error) {
		input, err := bound.Input()
		if err != nil {
			return registryArticle{}, err
		}
		title, _ := input.String("title")
		next := current
		next.title = title
		if file, present := input.File("summary"); present {
			if upload, received := file.Upload(); received {
				incoming = upload
				info, err := storage.SaveUpload(ctx, root, "documents/"+upload.Name(), upload, storage.SaveOptions{MaxLength: 100})
				if err != nil {
					return registryArticle{}, err
				}
				names = append(names, info.Name())
				next.summary = new(info.Name())
			} else {
				next.summary = new(file.Name())
			}
		}
		writes++
		current = next
		return next, nil
	}
	config.Create = func(ctx context.Context, _ auth.Principal, bound formmodel.BoundForm, _ InlineSubmission) (registryArticle, error) {
		return apply(ctx, bound)
	}
	config.Update = func(ctx context.Context, _ auth.Principal, _ Mutation, bound formmodel.BoundForm, _ InlineSubmission) (registryArticle, []string, error) {
		value, err := apply(ctx, bound)
		return value, []string{"title", "summary"}, err
	}
	client, _, _ := uploadTestSite(t, config, policy, auth.PrincipalAuthorizer{})
	client.login(t, "admin", "secret", "/admin/")
	for index, mode := range []string{"create", "replace", "retain", "invalid", "clear", "retain_empty"} {
		t.Run(mode, func(t *testing.T) {
			target := "/admin/articles/change/?id=1"
			if mode == "create" {
				target = "/admin/articles/add/"
			}
			page := client.do(http.MethodGet, target, nil)
			if page.Code != 200 || !strings.Contains(page.Body.String(), `enctype="multipart/form-data"`) || !strings.Contains(page.Body.String(), `type="file" name="summary"`) {
				t.Fatal("model file widget unavailable", page.Code, page.Body.String())
			}
			if current.summary != nil && *current.summary != "" && mode != "create" && !strings.Contains(page.Body.String(), *current.summary) {
				t.Fatal("current file name absent from form")
			}
			values := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}, "title": {"Document"}}
			var parts []siteUploadPart
			switch mode {
			case "create", "replace":
				parts = []siteUploadPart{{"summary", "same.txt", mode}}
			case "retain":
				values.Set("summary", "forged/private.txt")
			case "invalid":
				values.Set("title", "")
				parts = []siteUploadPart{{"summary", "rejected.txt", "rejected"}}
			case "clear":
				values.Set("summary-clear", "on")
			}
			before := writes
			response := siteUploadSubmit(t, client, target, values, parts...)
			if mode == "invalid" {
				if response.Code != 200 || writes != before || len(names) != 2 || !strings.Contains(response.Body.String(), `data-error-field="title"`) {
					t.Fatal("invalid model form reached storage callback", response.Code, writes, response.Body.String())
				}
			} else {
				if response.Code != http.StatusFound || writes != before+1 {
					t.Fatal("model file save rejected", index, response.Code, response.Body.String())
				}
				if mode == "retain" && (current.summary == nil || *current.summary != names[1]) {
					t.Fatal("text input forged stored reference")
				}
				if (mode == "clear" || mode == "retain_empty") && (current.summary == nil || *current.summary != "") {
					t.Fatal("clear did not retain empty reference")
				}
			}
			assertUploadTempEmpty(t, policy.TempDir)
		})
	}
	if len(names) != 2 || names[0] == names[1] {
		t.Fatal("replacement overwrote existing name")
	}
	for i, name := range names {
		reader, err := root.Open(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil || closeErr != nil || string(data) != []string{"create", "replace"}[i] {
			t.Fatal("replace/clear deleted file content", err, closeErr)
		}
	}
	if _, err := incoming.Open(t.Context()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
		t.Fatal("model upload outlived HTTP request", err)
	}
}
