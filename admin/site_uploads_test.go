package admin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type siteUploadPart struct{ field, name, content string }

func siteUploadBody(t *testing.T, values url.Values, files ...siteUploadPart) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range values[name] {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, file := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": file.field, "filename": file.name}))
		header.Set("Content-Type", "application/octet-stream")
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(part, file.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes(), writer.FormDataContentType()
}

func siteUploadRequest(client *siteHTTPClient, target string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "http://example.test"+target, body)
	request.Header.Set("Content-Type", contentType)
	for _, cookie := range client.cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	client.application.ServeHTTP(response, request)
	return response
}

func siteUploadSubmit(t *testing.T, client *siteHTTPClient, target string, values url.Values, files ...siteUploadPart) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := siteUploadBody(t, values, files...)
	return siteUploadRequest(client, target, bytes.NewReader(body), contentType)
}

func uploadTestSite(t *testing.T, config ModelConfig[registryArticle], policy uploads.Config, authorizer auth.Authorizer) (*siteHTTPClient, *Site, *siteCountingStore) {
	t.Helper()
	installed := mustApps(t)
	builder := NewBuilder(installed)
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		t.Fatal(err)
	}
	runtime, store := siteTestRuntimeConfigured(t, paths, "/admin/login/", "/admin/", siteTestIdentities(t), "/", "/", authorizer)
	site, err := NewSite(SiteConfig{Apps: installed, Namespace: "godj_conformance", Registry: registry, Auth: runtime, Uploads: &policy})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "admin_upload_test", InstalledApps: installed.All()})
	if err != nil {
		t.Fatal(err)
	}
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	if err != nil {
		t.Fatal(err)
	}
	return newSiteHTTPClient(app), site, store
}

func uploadTestPolicy(t *testing.T) uploads.Config {
	t.Helper()
	policy := uploads.DefaultConfig()
	policy.MemoryBytes, policy.TempDir = 0, t.TempDir()
	return policy
}

func uploadTestField(t *testing.T, options ...forms.FieldOption) forms.Field {
	t.Helper()
	field, err := forms.FileField("document", options...)
	if err != nil {
		t.Fatal(err)
	}
	return field
}

func assertUploadTempEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("upload resources leaked: count %d, %v", len(entries), err)
	}
}

func TestAdminUploadCreateAndCommandPreserveContentAndRequestLifetime(t *testing.T) {
	for _, command := range []bool{false, true} {
		name := "create"
		if command {
			name = "command"
		}
		t.Run(name, func(t *testing.T) {
			policy := uploadTestPolicy(t)
			config := validRegistryConfig(t)
			field := uploadTestField(t)
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			var received uploads.File
			var reader *uploads.Reader
			calls := 0
			consume := func(ctx context.Context, values forms.Values) {
				t.Helper()
				calls++
				value, present := values.File("document")
				if !present {
					t.Fatal("file absent at final callback")
				}
				received, present = value.Upload()
				if !present || received.Name() != "report.txt" || received.Size() != 10 {
					t.Fatal("file metadata changed")
				}
				reader, err = received.Open(ctx)
				if err != nil {
					t.Fatal(err)
				}
				content, readErr := io.ReadAll(reader)
				if readErr != nil || string(content) != "attachment" {
					t.Fatal("file content changed", readErr)
				}
				entries, err := os.ReadDir(policy.TempDir)
				if err != nil || len(entries) != 1 {
					t.Fatal("disk spill not exercised", err)
				}
			}
			target := "/admin/articles/add/"
			if command {
				config.Commands = []CommandConfig{{Name: "attach", Label: "Attach", Permission: config.Permissions.Change, Form: spec,
					Run: func(ctx context.Context, _ auth.Principal, mutation Mutation, values forms.Values) (CommandResult, error) {
						consume(ctx, values)
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
					if _, present := bound.Candidate().Get("document"); present {
						t.Fatal("file entered stored model candidate")
					}
					consume(ctx, values)
					return original(ctx, actor, bound, inlines)
				}
			}
			client, _, _ := uploadTestSite(t, config, policy, auth.PrincipalAuthorizer{})
			client.login(t, "admin", "secret", "/admin/")
			page := client.do(http.MethodGet, target, nil)
			if page.Code != 200 || !strings.Contains(page.Body.String(), `enctype="multipart/form-data"`) || !strings.Contains(page.Body.String(), `<input type="file" name="document" required>`) {
				t.Fatal("file form unavailable", page.Code, page.Body.String())
			}
			values := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}}
			if !command {
				values.Set("title", "Upload")
			}
			response := siteUploadSubmit(t, client, target, values, siteUploadPart{"document", `C:\private\report.txt`, "attachment"})
			if response.Code != http.StatusFound || calls != 1 {
				t.Fatal("upload callback", response.Code, calls, response.Body.String())
			}
			if _, err := received.Open(context.Background()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
				t.Fatal("upload survived request", err)
			}
			if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, &uploads.Error{Code: "closed"}) {
				t.Fatal("reader survived request", err)
			}
			assertUploadTempEmpty(t, policy.TempDir)
		})
	}
}

func TestAdminUploadValidationRedisplayAndRejectBeforeCallback(t *testing.T) {
	for _, test := range []struct {
		name   string
		values url.Values
		parts  []siteUploadPart
		status int
		code   string
		called bool
	}{
		{"missing", nil, nil, 200, "required", false},
		{"text cannot upload", url.Values{"document": {"private/forged.txt"}}, nil, 200, "required", false},
		{"duplicate", nil, []siteUploadPart{{"document", "a.txt", "a"}, {"document", "b.txt", "b"}}, 200, "multiple", false},
		{"empty", nil, []siteUploadPart{{"document", "empty.txt", ""}}, 200, "empty", false},
		{"invalid scalar", url.Values{"title": {""}}, []siteUploadPart{{"document", "<private>.txt", "ok"}}, 200, "required", false},
		{"callback rejection", url.Values{"title": {"reject"}}, []siteUploadPart{{"document", "a.txt", "a"}}, 200, "rejected", true},
		{"csrf missing", url.Values{"csrfmiddlewaretoken": {""}}, []siteUploadPart{{"document", "a.txt", "a"}}, 403, "", false},
		{"csrf repeated", url.Values{"csrfmiddlewaretoken": {"a", "b"}}, []siteUploadPart{{"document", "a.txt", "a"}}, 403, "", false},
		{"undeclared file", nil, []siteUploadPart{{"other", "a.txt", "a"}}, 400, "", false},
		{"scalar file", nil, []siteUploadPart{{"title", "a.txt", "a"}}, 400, "", false},
		{"csrf file", nil, []siteUploadPart{{"csrfmiddlewaretoken", "a.txt", "a"}}, 400, "", false},
		{"text bound", url.Values{"title": {strings.Repeat("x", MaximumInputBytes+1)}}, []siteUploadPart{{"document", "a.txt", "a"}}, 400, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := uploadTestPolicy(t)
			config := validRegistryConfig(t)
			config.CreateForm = &FormConfig{Definition: formmodel.Definition{ExtraFields: []forms.Field{uploadTestField(t)}}}
			called := false
			config.Create = func(context.Context, auth.Principal, formmodel.BoundForm, InlineSubmission) (registryArticle, error) {
				called = true
				return registryArticle{}, validation.Reject(validation.NewErrors(validation.New("document", "rejected")), nil)
			}
			client, _, _ := uploadTestSite(t, config, policy, auth.PrincipalAuthorizer{})
			client.login(t, "admin", "secret", "/admin/")
			page := client.do(http.MethodGet, "/admin/articles/add/", nil)
			values := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}, "title": {"Upload"}}
			for name, raw := range test.values {
				values[name] = raw
			}
			response := siteUploadSubmit(t, client, "/admin/articles/add/", values, test.parts...)
			if response.Code != test.status || called != test.called {
				t.Fatal("invalid admission", response.Code, called, response.Body.String())
			}
			body := response.Body.String()
			if test.code != "" && !strings.Contains(body, `data-error-code="`+test.code+`"`) {
				t.Fatal("missing field diagnostic", body)
			}
			if response.Code == 200 && len(test.parts) != 0 && !strings.Contains(body, `data-file-reselect="document"`) {
				t.Fatal("no reselect guidance", body)
			}
			if strings.Contains(body, `value="private/forged.txt"`) || strings.Contains(body, "<private>") || strings.Contains(body, policy.TempDir) {
				t.Fatal("private upload text redisplayed")
			}
			assertUploadTempEmpty(t, policy.TempDir)
		})
	}
}

type countedUploadReader struct{ reads int }

func (r *countedUploadReader) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("unexpected upload read")
}

func TestAdminUploadAdmissionDoesNotReadBodyOrMutateSession(t *testing.T) {
	for _, username := range []string{"", "modeldenied", "viewer", "admin"} {
		t.Run("role="+username, func(t *testing.T) {
			policy := uploadTestPolicy(t)
			config := validRegistryConfig(t)
			config.CreateForm = &FormConfig{Definition: formmodel.Definition{ExtraFields: []forms.Field{uploadTestField(t)}}}
			client, _, store := uploadTestSite(t, config, policy, siteDenyAuthorizer{denied: config.Permissions.Add})
			if username != "" {
				client.login(t, username, "secret", "/admin/")
			}
			before := store.counts()
			body := &countedUploadReader{}
			response := siteUploadRequest(client, "/admin/articles/add/", body, "multipart/form-data; boundary=admission")
			if response.Code != 302 && response.Code != 403 || body.reads != 0 {
				t.Fatal("read body before admission", response.Code, body.reads)
			}
			after := store.counts()
			if after.accesses != before.accesses || after.creates != before.creates || after.rotates != before.rotates || after.deletes != before.deletes {
				t.Fatal("preflight mutated session")
			}
			assertUploadTempEmpty(t, policy.TempDir)
		})
	}
}

func TestAdminUploadPolicySnapshotAndFailureClassification(t *testing.T) {
	policy := uploadTestPolicy(t)
	client, site, _ := uploadTestSite(t, validRegistryConfig(t), policy, auth.PrincipalAuthorizer{})
	if site.uploads.MaxValueBytes != MaximumInputBytes || site.uploads.MaxValueTotalBytes != MaximumFormBodyBytes {
		t.Fatal("Admin text budgets changed")
	}
	invalid := policy
	invalid.MaxBodyBytes = 0
	if _, err := NewSite(SiteConfig{Apps: mustApps(t), Namespace: "godj_conformance", Registry: site.registry, Auth: site.auth, Uploads: &invalid}); errorCode(err) != "invalid" {
		t.Fatal("invalid upload policy accepted", err)
	}
	snapshot, err := NewSite(SiteConfig{Apps: mustApps(t), Namespace: "godj_conformance", Registry: site.registry, Auth: site.auth, Uploads: &policy})
	if err != nil {
		t.Fatal(err)
	}
	policy.TempDir = "changed"
	if snapshot.uploads.TempDir == policy.TempDir {
		t.Fatal("upload config aliases caller")
	}
	sentinel := errors.New("storage failure")
	for _, failure := range []error{&uploads.Error{Code: "read_failed", Cause: sentinel}, &uploads.Error{Code: "cleanup_failed", Cause: sentinel}, &uploads.Error{Code: "write_failed", Cause: sentinel}, &uploads.Error{Code: "canceled", Cause: context.Canceled}, &uploads.Error{Code: "config_mismatch"}, &ConfigError{Code: "read_failed", Cause: sentinel}} {
		if _, err := siteFormResponse(failure); err != failure {
			t.Fatal("execution failure became client input error", failure, err)
		}
	}
	client.login(t, "admin", "secret", "/admin/")
	response := siteUploadRequest(client, "/admin/articles/add/", &countedUploadReader{}, "multipart/form-data; boundary=readfailure")
	if response.Code != 500 {
		t.Fatal("HTTP body I/O failure became input rejection", response.Code)
	}
	response = siteUploadRequest(client, "/admin/articles/add/", strings.NewReader("--broken\r\n"), "multipart/form-data; boundary=broken")
	if response.Code != 400 {
		t.Fatal("malformed body not rejected", response.Code)
	}
	// A storage failure must not be mistaken for a malformed upload.
	site.uploads.TempDir = filepath.Join(t.TempDir(), "missing")
	response = siteUploadSubmit(t, client, "/admin/articles/add/", nil, siteUploadPart{"undeclared", "a.txt", "a"})
	if response.Code != 500 {
		t.Fatal("temporary storage failure became 400", response.Code)
	}
}

func TestAdminFileWidgetRetainsInitialClearAndNeverPopulatesFileValue(t *testing.T) {
	site := newSiteApplicationHarness(t, 2).site
	initial, err := forms.ExistingFile(`private/<report>.txt`)
	if err != nil {
		t.Fatal(err)
	}
	upload, err := uploads.NewFile("new.txt", "text/plain", []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name                                   string
		required, plain, bound, replace, clear bool
	}{
		{name: "optional"}, {name: "required", required: true}, {name: "plain", plain: true},
		{name: "clear", bound: true, clear: true}, {name: "contradiction", bound: true, clear: true, replace: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := []forms.FieldOption{forms.WithRequired(test.required)}
			if test.plain {
				options = append(options, forms.WithWidget(forms.FileInput))
			}
			field := uploadTestField(t, options...)
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			raw := map[string][]string{"document": {"forged-value"}}
			if test.clear {
				raw["document-clear"] = []string{"on"}
			}
			files := map[string][]uploads.File{}
			if test.replace {
				files["document"] = []uploads.File{upload}
			}
			form, err := spec.Unbound(map[string]forms.Value{"document": initial})
			if test.bound {
				form, err = spec.Bind(forms.NewDataWithFiles(raw, files), map[string]forms.Value{"document": initial})
			}
			if err != nil {
				t.Fatal(err)
			}
			fields, err := formFieldContext(spec.Fields(), form, raw, "rows-0-", true)
			if err != nil {
				t.Fatal(err)
			}
			values, err := templates.NewContext(map[string]templates.Value{"field": fields[0]})
			if err != nil {
				t.Fatal(err)
			}
			html, err := site.templates.Render(t.Context(), "field.html", values, templates.Capabilities{})
			if err != nil {
				t.Fatal(err)
			}
			body := string(html)
			if !strings.Contains(body, `<input type="file" name="rows-0-document">`) || strings.Contains(body, "forged-value") || strings.Contains(body, `href=`) || strings.Contains(body, `<report>`) {
				t.Fatal("unsafe file widget", body)
			}
			if strings.Contains(body, `name="rows-0-document-clear"`) != (!test.required && !test.plain) {
				t.Fatal("incorrect clear widget", body)
			}
			if !test.plain && !strings.Contains(body, "private/&lt;report&gt;.txt") {
				t.Fatal("initial name missing", body)
			}
			if test.clear && !strings.Contains(body, `value="on" checked`) {
				t.Fatal("clear selection lost", body)
			}
			if test.replace && !strings.Contains(body, `data-file-reselect="rows-0-document"`) {
				t.Fatal("upload falsely retained", body)
			}
		})
	}
}

func TestAdminInlineUploadUsesActualChangeRouteAndAdmittedRows(t *testing.T) {
	for _, test := range []struct {
		name                           string
		existing, visible, add, change bool
		field, clear                   string
		status                         int
	}{
		{"existing upload", true, true, true, true, "reports-0-document", "", 302},
		{"new upload", false, true, true, true, "reports-0-document", "", 302},
		{"clear upload contradiction", true, true, true, true, "reports-0-document", "on", 200},
		{"clear only", true, true, true, true, "", "on", 302},
		{"duplicate clear", true, true, true, true, "", "duplicate", 200},
		{"readonly ignores ordinary upload", true, true, true, false, "reports-0-document", "", 302},
		{"no add upload only row", false, true, false, false, "reports-0-document", "", 403},
		{"hidden file only", false, false, false, false, "reports-0-document", "", 403},
		{"management is not a file", true, true, true, true, "reports-TOTAL_FORMS", "", 400},
		{"primary key is not a file", true, true, true, true, "reports-0-id", "", 400},
		{"foreign key is not a file", true, true, true, true, "reports-0-ticket", "", 400},
		{"noncanonical row", true, true, true, true, "reports-00-document", "", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			inlineConfig, _ := reportInlineFixture(t)
			inlineConfig.Set.ExtraForms = 0
			inlineConfig.Form.Definition.ExtraFields = []forms.Field{uploadTestField(t, forms.WithRequired(false))}
			if !test.existing {
				inlineConfig.Load = func(context.Context, auth.Principal, int64) (InlineSnapshot[models.ServiceReport], error) {
					return InlineSnapshot[models.ServiceReport]{}, nil
				}
			}
			inline, err := NewInline(inlineConfig)
			if err != nil {
				t.Fatal(err)
			}
			grants := []auth.Permission{"helpdesk.view_ticket", "helpdesk.change_ticket"}
			if test.visible {
				grants = append(grants, inlineConfig.Permissions.View)
			}
			if test.add {
				grants = append(grants, inlineConfig.Permissions.Add)
			}
			if test.change {
				grants = append(grants, inlineConfig.Permissions.Change)
			}
			actor := sitePrincipal(t, "upload-actor", true, grants...)
			installed, err := apps.New([]apps.Config{{Name: "github.com/progresshans/godj/examples/helpdesk", Label: "helpdesk"}})
			if err != nil {
				t.Fatal(err)
			}
			builder := NewBuilder(installed)
			parent := models.NewTicketWithID(7)
			parent.Subject, parent.CategoryID = "Server ticket", 1
			calls, consumed := 0, false
			var retained uploads.File
			config := ModelConfig[models.Ticket]{
				AppLabel: "helpdesk", Slug: "tickets", Model: models.TicketDescriptor{}.Metadata(), FormFields: []string{"subject"}, ListFields: []string{"id", "subject"}, Inlines: []Inline{inline},
				Permissions: Permissions{View: "helpdesk.view_ticket", Add: "helpdesk.add_ticket", Change: "helpdesk.change_ticket", Delete: "helpdesk.delete_ticket"},
				List: func(_ context.Context, _ auth.Principal, r ListRequest) (Page[models.Ticket], error) {
					return Page[models.Ticket]{Items: []models.Ticket{parent}, Total: 1, Limit: r.Limit, Offset: r.Offset}, nil
				},
				Get: func(context.Context, auth.Principal, int64) (models.Ticket, bool, error) { return parent, true, nil },
				Snapshot: func(value models.Ticket) (Object, error) {
					return NewObject(value.ID, value.Subject, map[string]templates.Value{"id": templates.Integer(value.ID), "subject": templates.String(value.Subject)})
				},
				Initial: func(value models.Ticket) (map[string]forms.Value, error) {
					return map[string]forms.Value{"subject": forms.String(value.Subject)}, nil
				},
				Create: func(context.Context, auth.Principal, formmodel.BoundForm, InlineSubmission) (models.Ticket, error) {
					return models.Ticket{}, errors.New("unexpected create")
				},
				Update: func(ctx context.Context, _ auth.Principal, _ Mutation, _ formmodel.BoundForm, submission InlineSubmission) (models.Ticket, []string, error) {
					calls++
					if len(submission.entries) != 1 {
						t.Fatal("lost admitted inline")
					}
					form := submission.entries[0].set.Forms()[0].Form()
					value, present := form.Cleaned().File("document")
					if test.existing && !test.change {
						if present || !form.ReadOnly() {
							t.Fatal("readonly row adopted file")
						}
					} else if test.field != "" {
						if !present {
							t.Fatal("inline file lost at composite callback")
						}
						retained, present = value.Upload()
						if !present {
							t.Fatal("inline upload lost")
						}
						reader, err := retained.Open(ctx)
						if err != nil {
							t.Fatal(err)
						}
						content, err := io.ReadAll(reader)
						if err != nil || string(content) != "inline attachment" {
							t.Fatal("wrong inline content", err)
						}
						consumed = true
					} else if !present || !value.Clear() {
						t.Fatal("inline clear intent lost")
					}
					return parent, nil, nil
				},
				Delete: func(context.Context, auth.Principal, Mutation) (models.Ticket, error) {
					return models.Ticket{}, errors.New("unexpected delete")
				},
			}
			if err = RegisterModel(builder, config); err != nil {
				t.Fatal(err)
			}
			registry, err := builder.Build()
			if err != nil {
				t.Fatal(err)
			}
			paths, err := SiteAllowedNextPaths(registry, "/admin")
			if err != nil {
				t.Fatal(err)
			}
			runtime, _ := siteTestRuntimeConfigured(t, paths, "/admin/login/", "/admin/", []siteIdentity{{"admin", actor}}, "/", "/", auth.PrincipalAuthorizer{})
			policy := uploadTestPolicy(t)
			site, err := NewSite(SiteConfig{Apps: installed, Namespace: "helpdesk", Registry: registry, Auth: runtime, Uploads: &policy})
			if err != nil {
				t.Fatal(err)
			}
			configured, err := settings.New(settings.Definition{ProjectName: "inline_upload", InstalledApps: installed.All()})
			if err != nil {
				t.Fatal(err)
			}
			app, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
			if err != nil {
				t.Fatal(err)
			}
			client := newSiteHTTPClient(app)
			client.login(t, "admin", "secret", "/admin/")
			target := "/admin/tickets/change/?id=7"
			page := client.do(http.MethodGet, target, nil)
			if page.Code != 200 {
				t.Fatal("inline GET", page.Code, page.Body.String())
			}
			if strings.Contains(page.Body.String(), `enctype="multipart/form-data"`) != (test.add || test.change) {
				t.Fatal("incorrect admitted multipart state")
			}
			if test.add && !strings.Contains(page.Body.String(), `type="file" name="reports-__prefix__-document"`) {
				t.Fatal("zero-extra prototype lost upload widget")
			}
			values := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}, "subject": {"Server ticket"}}
			if test.visible {
				values.Set("reports-TOTAL_FORMS", "1")
				values.Set("reports-INITIAL_FORMS", "0")
				values.Set("reports-0-ticket", "7")
				values.Set("reports-0-summary", "Server report")
				if test.existing {
					values.Set("reports-INITIAL_FORMS", "1")
					values.Set("reports-0-id", "11")
				}
			}
			if test.clear != "" {
				values.Set("reports-0-document-clear", test.clear)
			}
			if test.clear == "duplicate" {
				values["reports-0-document-clear"] = []string{"on", "off"}
			}
			var files []siteUploadPart
			if test.field != "" {
				files = append(files, siteUploadPart{test.field, "report.txt", "inline attachment"})
			}
			response := siteUploadSubmit(t, client, target, values, files...)
			if response.Code != test.status || (calls == 1) != (test.status == 302) {
				t.Fatal("inline upload outcome", response.Code, calls, response.Body.String())
			}
			if test.status == 200 {
				code := "contradiction"
				if test.clear == "duplicate" {
					code = "multiple"
				}
				if !strings.Contains(response.Body.String(), `data-error-code="`+code+`"`) {
					t.Fatal("inline diagnostic lost", response.Body.String())
				}
			}
			if consumed {
				if _, err := retained.Open(context.Background()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
					t.Fatal("inline upload survived", err)
				}
			}
			assertUploadTempEmpty(t, policy.TempDir)
		})
	}
}

func TestAdminUploadCommandClearLimitsAndCallbackFailures(t *testing.T) {
	for _, test := range []struct {
		name, clear, failure string
		parts                []siteUploadPart
		status, calls        int
		limit                int64
	}{
		{name: "clear via ordinary form", clear: "on", status: 302, calls: 1},
		{name: "clear upload conflict", clear: "on", parts: []siteUploadPart{{"document", "a", "a"}}, status: 200},
		{name: "file limit", parts: []siteUploadPart{{"document", "a", "ab"}}, limit: 1, status: 400},
		{name: "temporary file then callback error", parts: []siteUploadPart{{"document", "a", "a"}}, failure: "error", status: 500, calls: 1},
		{name: "temporary file then callback panic", parts: []siteUploadPart{{"document", "a", "a"}}, failure: "panic", status: 500, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validRegistryConfig(t)
			field := uploadTestField(t, forms.WithRequired(false))
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			policy := uploadTestPolicy(t)
			if test.limit != 0 {
				policy.MaxFileBytes = test.limit
			}
			calls := 0
			var retained uploads.File
			config.Commands = []CommandConfig{{Name: "attach", Label: "Attach", Permission: config.Permissions.Change, Form: spec, Run: func(ctx context.Context, _ auth.Principal, m Mutation, input forms.Values) (CommandResult, error) {
				calls++
				value, ok := input.File("document")
				if !ok {
					t.Fatal("command file value lost")
				}
				if test.clear != "" && !value.Clear() {
					t.Fatal("clear intent lost in command rebind")
				}
				retained, _ = value.Upload()
				if test.failure == "panic" {
					panic("synthetic upload callback panic")
				}
				if test.failure == "error" {
					return CommandResult{}, errors.New("synthetic upload callback failure")
				}
				return CommandResult{ID: m.ID}, nil
			}}}
			client, _, _ := uploadTestSite(t, config, policy, auth.PrincipalAuthorizer{})
			client.login(t, "admin", "secret", "/admin/")
			target := "/admin/articles/command/attach/?id=1"
			page := client.do(http.MethodGet, target, nil)
			values := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}}
			if test.clear != "" {
				values.Set("document-clear", test.clear)
			}
			var response *httptest.ResponseRecorder
			if len(test.parts) == 0 {
				response = client.do(http.MethodPost, target, values)
			} else {
				response = siteUploadSubmit(t, client, target, values, test.parts...)
			}
			if response.Code != test.status || calls != test.calls {
				t.Fatal("upload command", response.Code, calls, response.Body.String())
			}
			if response.Code == 200 && !strings.Contains(response.Body.String(), `data-error-code="contradiction"`) {
				t.Fatal("clear/upload contradiction lost")
			}
			if retained.Valid() {
				if _, err := retained.Open(context.Background()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
					t.Fatal("callback retained live file", err)
				}
			}
			assertUploadTempEmpty(t, policy.TempDir)
		})
	}
}

func TestAdminUploadOptionalOmissionPreservedInFinalCallback(t *testing.T) {
	for _, multipart := range []bool{false, true} {
		name := "urlencoded"
		if multipart {
			name = "multipart"
		}
		t.Run(name, func(t *testing.T) {
			config := validRegistryConfig(t)
			config.CreateForm = &FormConfig{Definition: formmodel.Definition{ExtraFields: []forms.Field{uploadTestField(t, forms.WithRequired(false))}}}
			original := config.Create
			called := false
			config.Create = func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm, submission InlineSubmission) (registryArticle, error) {
				called = true
				raw := bound.Form().Submitted()
				if _, present := raw.Files("document"); present {
					t.Fatal("omitted file became a supplied key")
				}
				if _, present := raw.Get("document-clear"); present {
					t.Fatal("omitted clear became a supplied key")
				}
				values, err := bound.Input()
				if err != nil {
					t.Fatal(err)
				}
				value, present := values.Get("document")
				if !present || !value.IsNull() {
					t.Fatal("optional omission did not clean to null")
				}
				return original(ctx, actor, bound, submission)
			}
			policy := uploadTestPolicy(t)
			client, _, _ := uploadTestSite(t, config, policy, auth.PrincipalAuthorizer{})
			client.login(t, "admin", "secret", "/admin/")
			page := client.do(http.MethodGet, "/admin/articles/add/", nil)
			values := url.Values{"csrfmiddlewaretoken": {siteCSRFToken(t, page.Body.String())}, "title": {"No attachment"}}
			var response *httptest.ResponseRecorder
			if multipart {
				response = siteUploadSubmit(t, client, "/admin/articles/add/", values)
			} else {
				response = client.do(http.MethodPost, "/admin/articles/add/", values)
			}
			if response.Code != 302 || !called {
				t.Fatal("optional file prevented save", response.Code, response.Body.String())
			}
			assertUploadTempEmpty(t, policy.TempDir)
		})
	}
}
