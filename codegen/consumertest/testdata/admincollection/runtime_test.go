package consumer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"example.com/godj-project-bundle/directory"
	"example.com/godj-project-bundle/inventory"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
	"golang.org/x/net/html"
)

func collectionChangePermission() auth.Permission {
	if evolved {
		return "inventory.manage_asset"
	}
	return "inventory.change_asset"
}

type collectionBackend struct {
	resourceBackend
	mode    string
	atomics int
	cancel  context.CancelFunc
}

func (b *collectionBackend) AtomicRelation(ctx context.Context, run func(db.RelationSession) error) error {
	b.atomics++
	err := b.resourceBackend.AtomicRelation(ctx, run)
	if err == nil && b.mode == "commit_unknown_keeps_native_state" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	if err != nil && b.mode == "rollback_unknown_preserves_failure" {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
	}
	if err == nil && b.mode == "post_commit_cancellation" {
		b.cancel()
	}
	return err
}

type collectionStorage struct {
	storage.Backend
	mode           string
	saves, deletes int
}

func (s *collectionStorage) Save(ctx context.Context, name string, input io.Reader, options storage.SaveOptions) (storage.Info, error) {
	s.saves++
	if s.saves == 2 && s.mode == "partial_file_publication" {
		return storage.Info{}, &storage.Error{Code: "write_failed", Outcome: storage.NotPublished}
	}
	info, err := s.Backend.Save(ctx, name, input, options)
	if err == nil && s.saves == 2 && s.mode == "unknown_file_publication" {
		return info, &storage.Error{Code: "publication_unknown", Outcome: storage.Uncertain}
	}
	return info, err
}
func (s *collectionStorage) Delete(ctx context.Context, name string) error {
	s.deletes++
	return s.Backend.Delete(ctx, name)
}

type collectionHarness struct {
	t                                                       *testing.T
	backend                                                 *collectionBackend
	storage                                                 *collectionStorage
	application                                             *web.Application
	jar                                                     http.CookieJar
	workspace                                               directory.Workspace
	initial                                                 []inventory.Asset
	load                                                    func(context.Context, db.Queryer) ([]inventory.Asset, error)
	beforeWrite                                             func()
	writerCalls, initialLoads, currentLoads, writes, audits int
	borrowed                                                db.RelationSession
	keys                                                    []int64
	uploads                                                 []uploads.File
	receipts                                                []formmodel.SetFilePublication
	uploadPolicy                                            uploads.Config
	mode                                                    string
}

func newCollectionHarness(t *testing.T, backend resourceBackend, mode, name string) *collectionHarness {
	t.Helper()
	h := &collectionHarness{t: t, backend: &collectionBackend{resourceBackend: backend, mode: mode}, mode: mode}
	var err error
	h.workspace, err = directory.WorkspaceObjects.Create(t.Context(), backend, directory.NewWorkspaceCreate(name))
	check(t, err)
	root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
	check(t, err)
	t.Cleanup(func() { check(t, root.Close()) })
	h.storage = &collectionStorage{Backend: root, mode: mode}
	for index := range 2 {
		info, err := root.Save(t.Context(), fmt.Sprintf("%s/old-%d.txt", name, index), strings.NewReader(fmt.Sprintf("old-%d", index)), storage.SaveOptions{MaxLength: 100})
		check(t, err)
		value, err := inventory.AssetObjects.Create(t.Context(), backend, inventory.NewAssetCreate(strings.ToUpper(fmt.Sprintf("%s-%d", name, index)), h.workspace.ID).WithAttachment(info.Name()).WithNote("preserved note"))
		check(t, err)
		h.initial = append(h.initial, value)
		h.keys = append(h.keys, value.ID)
	}
	binding, err := project.Bind()
	check(t, err)
	relations, err := project.BindRelations()
	check(t, err)
	deleters, err := project.BindRelationDeleters()
	check(t, err)
	h.load = func(ctx context.Context, reader db.Queryer) ([]inventory.Asset, error) {
		rows, err := inventory.AssetObjects.Using(reader).Filter(relations.InventoryAsset.Workspace.ID.Exact(h.workspace.ID)).OrderBy(inventory.AssetFields.ID.Asc()).Limit(5)
		if err != nil {
			return nil, err
		}
		values, err := rows.All(ctx)
		if err == nil && len(values) > 4 {
			err = errors.New("collection cohort exceeds four rows")
		}
		return values, err
	}
	fields := []string{"code", "enabled", "attachment"}
	if evolved {
		fields = append(fields, "priority")
	}
	policy := forms.DefaultSetConfig()
	policy.Prefix, policy.ExtraForms, policy.MaxForms, policy.AbsoluteMax = "assets", 1, 4, 4
	policy.CanDelete, policy.CanOrder, policy.ValidateMin, policy.ValidateMax = true, true, true, true
	permissions := admin.Permissions{View: "inventory.view_asset", Add: "inventory.add_asset", Change: collectionChangePermission(), Delete: "inventory.delete_asset"}
	var declaration admin.ModelCollectionFormSet[inventory.Asset]
	declaration, err = admin.NewCollectionFormSet(admin.CollectionFormSetConfig[inventory.Asset]{
		Name: "edit", Label: "Edit assets", Project: binding, Source: ir.ModelIdentity{AppLabel: "inventory", ModelName: "asset"}, Manager: inventory.AssetObjects, Permissions: permissions, Set: policy, RevisionField: "revision",
		Form: admin.FormConfig{Definition: formmodel.Definition{
			Fields: fields, Overrides: []formmodel.Override{formmodel.OverrideField("code", formmodel.WithStringNormalizer(strings.ToUpper), formmodel.WithWidgetTemplate("asset_code")), formmodel.OverrideField("attachment", formmodel.WithWidgetTemplate("asset_file"))},
			PostClean: formmodel.PostClean{Fields: []string{"workspace"}, Clean: func(forms.Values) (forms.Values, validation.Errors) {
				return forms.NewValues(map[string]forms.Value{"workspace": forms.Integer(h.workspace.ID)}), validation.Errors{}
			}},
		}},
		Load: func(ctx context.Context, _ auth.Principal, _ admin.CollectionSetAccess) (admin.CollectionSetSnapshot[inventory.Asset], error) {
			h.initialLoads++
			rows, err := h.load(ctx, backend)
			return admin.CollectionSetSnapshot[inventory.Asset]{Current: rows}, err
		},
		Run: func(ctx context.Context, actor auth.Principal, submission admin.CollectionSetSubmission) (admin.CollectionSetResult, error) {
			h.writerCalls++
			if h.beforeWrite != nil {
				hook := h.beforeWrite
				h.beforeWrite = nil
				hook()
			}
			result, err := db.RelationAtomicResult(ctx, h.backend, func(work context.Context, session db.RelationSession) (admin.CollectionSetResult, error) {
				h.borrowed = session
				bound, err := declaration.BindCurrent(work, actor, submission, func(current context.Context, _ admin.CollectionSetAccess) (admin.CollectionSetCurrent[inventory.Asset], error) {
					h.currentLoads++
					workspace, err := directory.WorkspaceObjects.Using(session).Filter(directory.WorkspaceFields.ID.Exact(h.workspace.ID)).Get(current)
					if err != nil {
						return admin.CollectionSetCurrent[inventory.Asset]{}, err
					}
					if !workspace.Writable {
						return admin.CollectionSetCurrent[inventory.Asset]{}, admin.NewOperationError(admin.OperationDenied, nil)
					}
					rows, err := h.load(current, session)
					return admin.CollectionSetCurrent[inventory.Asset]{Rows: admin.CollectionSetSnapshot[inventory.Asset]{Current: rows}}, err
				})
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				if !bound.Set().Valid() {
					return admin.CollectionSetResult{}, admin.RejectCollectionFormSet(nil, validation.NewErrors(validation.New(validation.NonField, "cohort_changed")), nil)
				}
				for index := 0; index < bound.Set().FormSet().TotalForms(); index++ {
					files, _ := submission.FormSet().Submitted().Files(fmt.Sprintf("assets-%d-attachment", index))
					h.uploads = append(h.uploads, files...)
				}
				prepared, err := bound.Set().Prepare()
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				pending := false
				for _, row := range prepared.Rows() {
					pending = pending || len(row.Prepared().PendingFiles()) != 0
				}
				if pending {
					if _, err := prepared.SavePlan(); err == nil {
						return admin.CollectionSetResult{}, errors.New("pending file reached a database plan")
					}
				}
				prepared, receipts, err := prepared.SaveFiles(work, func(row formmodel.PreparedSetRow[inventory.Asset]) ([]formmodel.FileSaver[inventory.Asset], error) {
					return []formmodel.FileSaver[inventory.Asset]{{Field: "attachment", Backend: h.storage, Name: func(_ inventory.Asset, file uploads.File) (string, error) {
						return fmt.Sprintf("%s/%d/%s", h.workspace.Name, row.Index(), file.Name()), nil
					}}}, nil
				})
				h.receipts = receipts
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				plan, err := prepared.SavePlan()
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				var writes []admin.CollectionSetWrite
				fence := func(scope db.Session, value *inventory.Asset) error {
					count, err := inventory.AssetObjects.Using(scope).Filter(inventory.AssetFields.ID.Exact(value.ID), inventory.AssetFields.Revision.Exact(value.Revision), relations.InventoryAsset.Workspace.ID.Exact(h.workspace.ID)).Update(work, orm.Assign(inventory.AssetFields.Revision, value.Revision+1))
					if err != nil {
						return err
					}
					if count != 1 {
						return admin.NewOperationError(admin.OperationConflict, nil)
					}
					value.Revision++
					return nil
				}
				audit := func(kind string, value inventory.Asset) error {
					h.audits++
					if _, err := inventory.AuditObjects.Create(work, session, inventory.NewAuditCreate(kind, value.ID, actor.ID(), value.Revision)); err != nil {
						return err
					}
					if h.audits == 2 && (h.mode == "late_audit_rollback_keeps_files" || h.mode == "rollback_unknown_preserves_failure") {
						return errors.New("private late native audit failure")
					}
					return nil
				}
				_, err = plan.Save(work, session, formmodel.SetSaveOptions[inventory.Asset]{
					Save: func(current context.Context, scope db.Session, row *formmodel.SetSaveRow[inventory.Asset]) error {
						kind, allowed, permission := "create", bound.Access().Add, permissions.Add
						if row.Existing() {
							kind, allowed, permission = "update", bound.Access().Change, permissions.Change
						}
						if current != work || scope != session || !allowed || !actor.Has(permission) {
							return admin.NewOperationError(admin.OperationDenied, nil)
						}
						if row.Existing() {
							if err := fence(scope, row.Model()); err != nil {
								return err
							}
						}
						if err := row.Save(current, scope); err != nil {
							return err
						}
						h.writes++
						h.keys = append(h.keys, row.Model().ID)
						if err := audit(kind, *row.Model()); err != nil {
							return err
						}
						writes = append(writes, admin.CollectionSetWrite{Index: row.Index(), Kind: kind, ID: row.Model().ID})
						return nil
					},
					Delete: func(current context.Context, scope db.Session, row formmodel.DeletedSetRow[inventory.Asset]) error {
						if current != work || scope != session || !bound.Access().Delete || !actor.Has(permissions.Delete) {
							return admin.NewOperationError(admin.OperationDenied, nil)
						}
						value, err := row.Model()
						if err != nil {
							return err
						}
						if err := fence(scope, &value); err != nil {
							return err
						}
						if _, err := deleters.InventoryAsset.DeleteInSession(current, session, value); err != nil {
							return err
						}
						h.writes++
						if err := audit("delete", value); err != nil {
							return err
						}
						writes = append(writes, admin.CollectionSetWrite{Index: row.Index(), Kind: "delete", ID: value.ID})
						return nil
					},
				})
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				return bound.Result(writes)
			})
			if protected, ok := err.(*query.ProtectedForeignKeyError); ok && protected.ProtectedSourceRows() > 0 {
				return admin.CollectionSetResult{}, admin.RejectCollectionFormSet(nil, validation.NewErrors(validation.New(validation.NonField, "protected")), err)
			}
			return result, err
		},
	})
	check(t, err)
	configured, err := settings.New(settings.Definition{ProjectName: "collection_consumer", InstalledApps: []apps.Config{{Name: "example.com/godj-project-bundle/directory", Label: "directory"}, {Name: "example.com/godj-project-bundle/inventory", Label: "inventory"}}})
	check(t, err)
	builder := admin.NewBuilder(configured.Apps())
	metadata := (inventory.AssetDescriptor{}).Metadata()
	projector, err := admin.NewModelProjector(metadata, (inventory.AssetDescriptor{}).WriteFieldValue, "id", "code")
	check(t, err)
	// The ordinary route has a narrow scalar form. The collection declaration
	// owns its complete typed policy, independently of this presentation.
	check(t, admin.RegisterModel(builder, admin.ModelConfig[inventory.Asset]{
		AppLabel: "inventory", Slug: "assets", Model: metadata, FormFields: []string{"code"}, ListFields: []string{"id", "code"}, Permissions: permissions, CollectionFormSets: []admin.CollectionFormSet{declaration.View()},
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[inventory.Asset], error) {
			rows, err := h.load(ctx, backend)
			return admin.Page[inventory.Asset]{Items: rows, Total: int64(len(rows)), Offset: request.Offset, Limit: request.Limit}, err
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (inventory.Asset, bool, error) {
			return inventory.AssetObjects.Using(backend).Filter(inventory.AssetFields.ID.Exact(id)).OrderBy(inventory.AssetFields.ID.Asc()).First(ctx)
		},
		Snapshot: func(value inventory.Asset) (admin.Object, error) {
			return projector.Project(value, value.ID, value.Code)
		},
		Initial: func(value inventory.Asset) (map[string]forms.Value, error) {
			return map[string]forms.Value{"code": forms.String(value.Code)}, nil
		},
		Create: func(context.Context, auth.Principal, formmodel.BoundForm, admin.InlineSubmission) (admin.MutationResult, error) {
			return admin.MutationResult{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
		Update: func(context.Context, auth.Principal, admin.Mutation, formmodel.BoundForm, admin.InlineSubmission) (admin.MutationResult, []string, error) {
			return admin.MutationResult{}, nil, admin.NewOperationError(admin.OperationDenied, nil)
		},
		Delete: func(context.Context, auth.Principal, admin.Mutation) (admin.MutationResult, error) {
			return admin.MutationResult{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
	}))
	registry, err := builder.Build()
	check(t, err)
	store, err := sessions.NewMemoryStore(8)
	check(t, err)
	manager, err := sessions.NewManager(store, sessions.Config{})
	check(t, err)
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	check(t, err)
	password, err := hasher.Hash(t.Context(), "collection-password")
	check(t, err)
	var credentials []auth.Credential
	for _, name := range []string{"editor", "readonly", "old"} {
		grants := []auth.Permission{permissions.View, "directory.view_workspace"}
		if name == "old" {
			grants = append(grants, "inventory.change_asset")
		} else {
			grants = append(grants, permissions.Add)
		}
		if name == "editor" {
			grants = append(grants, permissions.Change, permissions.Delete)
		}
		actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: name, Active: true, Staff: true, Permissions: grants})
		check(t, err)
		credential, err := auth.NewCredential(name, password, actor)
		check(t, err)
		credentials = append(credentials, credential)
	}
	authenticator, err := auth.NewMemoryAuthenticator(credentials, hasher, auth.LoginInputConfig{Label: "Username", MaxLength: auth.MaximumUsernameBytes, Normalization: auth.LoginExact})
	check(t, err)
	allowed, err := admin.SiteAllowedNextPaths(registry, "/admin")
	check(t, err)
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{}, SessionCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: allowed})
	check(t, err)
	h.uploadPolicy = uploads.DefaultConfig()
	h.uploadPolicy.MemoryBytes, h.uploadPolicy.TempDir = 0, t.TempDir()
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "inventory", Registry: registry, Auth: runtime, Uploads: &h.uploadPolicy, Widgets: admin.WidgetConfig{Templates: fstest.MapFS{
		"code.html": {Data: []byte(`<input data-asset-code name="{{ field.name }}" id="{{ field.control_id }}" value="{{ field.value }}"{% if field.readonly %} disabled{% endif %}{% if field.invalid %} aria-invalid="true" aria-describedby="{{ field.error_id }}"{% endif %}>`)},
		"file.html": {Data: []byte(`<input type="file" data-asset-file name="{{ field.name }}" id="{{ field.control_id }}"{% if field.readonly %} disabled{% endif %}{% if field.invalid %} aria-invalid="true" aria-describedby="{{ field.error_id }}"{% endif %}>`)},
	}, Entries: []admin.WidgetTemplate{{Name: "asset_code", Widget: forms.TextInput, Template: "code.html"}, {Name: "asset_file", Widget: forms.ClearableFileInput, Template: "file.html"}}}})
	check(t, err)
	h.application, err = web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	check(t, err)
	h.login("editor")
	return h
}

const collectionPath = "/admin/assets/collection-set/edit/"

var collectionCSRF = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)

func (h *collectionHarness) request(ctx context.Context, method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	location, err := url.Parse("http://collection.test" + path)
	check(h.t, err)
	request := httptest.NewRequest(method, location.String(), body).WithContext(ctx)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	for _, cookie := range h.jar.Cookies(location) {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	h.application.ServeHTTP(response, request)
	h.jar.SetCookies(location, response.Result().Cookies())
	return response
}
func (h *collectionHarness) send(method, path string, values url.Values) *httptest.ResponseRecorder {
	return h.request(h.t.Context(), method, path, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded")
}
func (h *collectionHarness) login(actor string) {
	var err error
	h.jar, err = cookiejar.New(nil)
	check(h.t, err)
	page := h.send("GET", "/admin/login/", nil)
	match := collectionCSRF.FindStringSubmatch(page.Body.String())
	if page.Code != 200 || len(match) != 2 {
		h.t.Fatal("login CSRF", page.Code)
	}
	response := h.send("POST", "/admin/login/", url.Values{"username": {actor}, "password": {"collection-password"}, "csrfmiddlewaretoken": {match[1]}})
	if response.Code != 302 {
		h.t.Fatal("collection login", response.Code, response.Body.String())
	}
}

// Read actual successful HTML controls, including readonly fieldset ancestry.
// Prototypes never become submitted rows until the caller explicitly adds one.
func (h *collectionHarness) input() url.Values {
	page := h.send("GET", collectionPath, nil)
	if page.Code != 200 {
		h.t.Fatal("collection GET", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), `data-asset-code`) || !strings.Contains(page.Body.String(), `data-asset-file`) {
		h.t.Fatal("generated consumer omitted its registered control templates")
	}
	document, err := html.Parse(strings.NewReader(page.Body.String()))
	check(h.t, err)
	values := url.Values{}
	attribute := func(node *html.Node, key string) (string, bool) {
		for _, attr := range node.Attr {
			if attr.Key == key {
				return attr.Val, true
			}
		}
		return "", false
	}
	var content func(*html.Node) string
	content = func(node *html.Node) string {
		if node.Type == html.TextNode {
			return node.Data
		}
		var result string
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			result += content(child)
		}
		return result
	}
	var walk func(*html.Node, bool, bool)
	walk = func(node *html.Node, selected, disabled bool) {
		if node.Type == html.ElementNode && node.Data == "template" {
			return
		}
		if node.Type == html.ElementNode && node.Data == "form" {
			action, _ := attribute(node, "action")
			selected = action == collectionPath
		}
		_, off := attribute(node, "disabled")
		disabled = disabled || off
		name, has := attribute(node, "name")
		if selected && has && !disabled {
			value, _ := attribute(node, "value")
			switch node.Data {
			case "input":
				typ, _ := attribute(node, "type")
				_, checked := attribute(node, "checked")
				if typ != "file" && typ != "submit" && (typ != "checkbox" && typ != "radio" || checked) {
					values.Add(name, value)
				}
			case "textarea":
				values.Add(name, content(node))
			case "select":
				_, multiple := attribute(node, "multiple")
				found := false
				first := ""
				seen := false
				for option := node.FirstChild; option != nil; option = option.NextSibling {
					if option.Type == html.ElementNode && option.Data == "option" {
						value, _ := attribute(option, "value")
						if !seen {
							first = value
							seen = true
						}
						if _, yes := attribute(option, "selected"); yes {
							values.Add(name, value)
							found = true
						}
					}
				}
				if !multiple && !found && seen {
					values.Add(name, first)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, selected, disabled)
		}
	}
	walk(document, false, false)
	if values.Get("csrfmiddlewaretoken") == "" || values.Get("assets-INITIAL_FORMS") != "2" || values.Get("assets-0-id") != strconv.FormatInt(h.initial[0].ID, 10) || values.Has("assets-0-workspace") || values.Has("assets-0-stamp") || values.Has("assets-0-revision") || values.Get("assets-0-expected_revision") != "1" {
		h.t.Fatal("collection input exposed server fields or lost identity/revision")
	}
	if strings.Contains(page.Body.String(), `name="assets-0-priority"`) != evolved {
		h.t.Fatal("generated model change did not reach the same form")
	}
	return values
}

func (h *collectionHarness) submit(ctx context.Context, values url.Values, indices ...int) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range values[name] {
			check(h.t, writer.WriteField(name, value))
		}
	}
	for _, index := range indices {
		part, err := writer.CreateFormFile(fmt.Sprintf("assets-%d-attachment", index), "document.txt")
		check(h.t, err)
		_, err = fmt.Fprintf(part, "payload-row-%d", index)
		check(h.t, err)
	}
	check(h.t, writer.Close())
	return h.request(ctx, "POST", collectionPath, &body, writer.FormDataContentType())
}

func (h *collectionHarness) current() []inventory.Asset {
	rows, err := h.load(h.t.Context(), h.backend)
	check(h.t, err)
	return rows
}
func (h *collectionHarness) auditRows() []inventory.Audit {
	rows, err := inventory.AuditObjects.Using(h.backend).Filter(inventory.AuditFields.AssetKey.In(h.keys...)).OrderBy(inventory.AuditFields.ID.Asc()).All(h.t.Context())
	check(h.t, err)
	return rows
}

func (h *collectionHarness) checkFiles(want []int, outcomes ...storage.Outcome) {
	h.t.Helper()
	if len(h.receipts) != len(want) || h.storage.saves != len(want) || h.storage.deletes != 0 {
		h.t.Fatal("file attempts/receipts changed", len(h.receipts), h.storage.saves, want)
	}
	for position, index := range want {
		receipt := h.receipts[position]
		outcome := storage.Published
		if len(outcomes) > position {
			outcome = outcomes[position]
		}
		if receipt.Index() != index || receipt.File().Field() != "attachment" || receipt.File().Outcome() != outcome {
			h.t.Fatal("file receipt lost row or terminal outcome")
		}
		if outcome != storage.NotPublished {
			reader, err := h.storage.Open(h.t.Context(), receipt.File().Info().Name())
			check(h.t, err)
			content, err := io.ReadAll(reader)
			check(h.t, err)
			check(h.t, reader.Close())
			if string(content) != fmt.Sprintf("payload-row-%d", index) {
				h.t.Fatal("native file bytes differ")
			}
		}
	}
	for _, value := range h.initial {
		if _, err := h.storage.Stat(h.t.Context(), value.Attachment); err != nil {
			h.t.Fatal("prior file removed automatically", err)
		}
	}
	for _, file := range h.uploads {
		if _, err := file.Open(context.Background()); !errors.Is(err, &uploads.Error{Code: "closed"}) {
			h.t.Fatal("request upload capability survived", err)
		}
	}
	entries, err := os.ReadDir(h.uploadPolicy.TempDir)
	check(h.t, err)
	if len(entries) != 0 {
		h.t.Fatal("request staging was not cleaned")
	}
	if h.borrowed != nil {
		if _, err := inventory.AssetObjects.Using(h.borrowed).Count(context.Background()); err == nil {
			h.t.Fatal("borrowed write session survived its owner")
		}
	}
}

func TestAdminCollectionConsumer(t *testing.T) {
	withResourceBackends(t, func(t *testing.T, backend resourceBackend) {
		cases := []string{"mixed_current_and_files", "readonly_existing_new_file", "clear_and_preserve", "late_audit_rollback_keeps_files", "protected_delete_rolls_back_and_keeps_files", "partial_file_publication", "unknown_file_publication", "commit_unknown_keeps_native_state", "rollback_unknown_preserves_failure", "post_commit_cancellation", "current_scope_denial", "late_cohort_gain", "stale_revision", "readonly_stale_revision", "deleted_stale_revision", "reordered_identities", "foreign_identity", "cohort_overflow", "changed_policy"}
		for sequence, mode := range cases {
			t.Run(mode, func(t *testing.T) {
				h := newCollectionHarness(t, backend, mode, fmt.Sprintf("c%02d", sequence))
				if mode == "cohort_overflow" {
					for index := range 3 {
						row, err := inventory.AssetObjects.Create(t.Context(), backend, inventory.NewAssetCreate(fmt.Sprintf("%s-more%d", h.workspace.Name, index), h.workspace.ID))
						check(t, err)
						h.keys = append(h.keys, row.ID)
					}
					page := h.send("GET", collectionPath, nil)
					count, err := inventory.AssetObjects.Using(backend).Filter(inventory.AssetFields.ID.In(h.keys...)).Count(t.Context())
					check(t, err)
					if page.Code != 500 || count != 5 || h.writerCalls != 0 || h.currentLoads != 0 || h.writes != 0 || len(h.auditRows()) != 0 {
						t.Fatal("oversized cohort was truncated or reached writer", page.Code, count)
					}
					h.checkFiles(nil)
					return
				}
				if mode == "readonly_existing_new_file" || mode == "readonly_stale_revision" {
					h.login("readonly")
				}
				if mode == "changed_policy" {
					h.login("old")
					page := h.send("GET", collectionPath, nil)
					if evolved {
						if page.Code != 403 || h.initialLoads != 0 {
							t.Fatal("old permission reached changed declaration", page.Code, h.initialLoads)
						}
						response := h.send("POST", collectionPath, url.Values{"assets-TOTAL_FORMS": {"999999"}})
						if response.Code != 403 || h.writerCalls != 0 || h.initialLoads != 0 || len(h.auditRows()) != 0 {
							t.Fatal("old permission reached collection body/binding")
						}
						h.login("editor")
					} else if page.Code != 200 {
						t.Fatal("baseline permission unexpectedly rejected", page.Code)
					}
				}
				values := h.input()
				want := slices.Clone(h.initial)
				newCode := strings.ToUpper(h.workspace.Name + "-new")
				updatedCode := strings.ToUpper(h.workspace.Name + "-edit")
				wantStatus, wantWrites, wantAudits, wantWriter, wantCurrent := 302, 0, 0, 1, 1
				var files, wantFiles []int
				var outcomes []storage.Outcome
				var created, removed bool
				var foreign *inventory.Asset
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				h.backend.cancel = cancel
				switch mode {
				case "mixed_current_and_files":
					values.Set("assets-0-code", " "+strings.ToLower(updatedCode)+" ")
					values.Set("assets-1-DELETE", "on")
					values.Set("assets-1-code", "")
					values.Set("assets-2-code", strings.ToLower(newCode))
					if evolved {
						values.Set("assets-0-priority", "9")
						values.Set("assets-2-priority", "1")
					}
					h.beforeWrite = func() {
						current := h.initial[0]
						current.Note, current.Stamp = new("fresh excluded note"), "fresh server stamp"
						check(t, inventory.AssetObjects.Save(t.Context(), backend, &current))
					}
					want[0].Code, want[0].Note, want[0].Stamp, want[0].Revision = updatedCode, new("fresh excluded note"), "fresh server stamp", 2
					files, wantFiles = []int{0, 1, 2}, []int{0, 2}
					created, removed, wantWrites, wantAudits = true, true, 3, 3
				case "readonly_existing_new_file":
					if values.Has("assets-0-code") || values.Has("assets-0-ORDER") {
						t.Fatal("readonly row yielded editable controls")
					}
					values.Set("assets-0-code", "FORGED")
					values.Set("assets-2-code", newCode)
					files, wantFiles = []int{0, 2}, []int{2}
					created, wantWrites, wantAudits = true, 1, 1
				case "clear_and_preserve":
					values.Set("assets-0-attachment-clear", "on")
					want[0].Attachment, want[0].Revision = "", 2
					wantWrites, wantAudits = 1, 1
				case "late_audit_rollback_keeps_files", "rollback_unknown_preserves_failure":
					values.Set("assets-0-code", updatedCode)
					values.Set("assets-1-code", h.workspace.Name+"-other")
					values.Set("assets-2-code", newCode)
					files, wantFiles = []int{0, 2}, []int{0, 2}
					wantStatus, wantWrites = 500, 2
				case "protected_delete_rolls_back_and_keeps_files":
					_, err := inventory.HoldObjects.Create(t.Context(), backend, inventory.NewHoldCreate(h.initial[1].ID))
					check(t, err)
					values.Set("assets-0-code", updatedCode)
					values.Set("assets-1-DELETE", "on")
					values.Set("assets-2-code", newCode)
					files, wantFiles = []int{0, 2}, []int{0, 2}
					wantStatus, wantWrites = 200, 1
				case "partial_file_publication", "unknown_file_publication":
					values.Set("assets-0-code", updatedCode)
					values.Set("assets-2-code", newCode)
					files, wantFiles = []int{0, 2}, []int{0, 2}
					outcomes = []storage.Outcome{storage.Published, storage.NotPublished}
					if mode == "unknown_file_publication" {
						outcomes[1] = storage.Uncertain
					}
					wantStatus = 500
				case "commit_unknown_keeps_native_state", "post_commit_cancellation":
					values.Set("assets-0-code", updatedCode)
					files, wantFiles = []int{0}, []int{0}
					want[0].Code, want[0].Revision = updatedCode, 2
					wantWrites, wantAudits = 1, 1
					if mode == "commit_unknown_keeps_native_state" {
						wantStatus = 500
					}
				case "current_scope_denial":
					values.Set("assets-0-code", updatedCode)
					files = []int{0}
					h.beforeWrite = func() {
						value := h.workspace
						value.Writable = false
						check(t, directory.WorkspaceObjects.Save(t.Context(), backend, &value))
					}
					wantStatus = 403
				case "late_cohort_gain":
					values.Set("assets-0-code", updatedCode)
					files = []int{0}
					h.beforeWrite = func() {
						row, err := inventory.AssetObjects.Create(t.Context(), backend, inventory.NewAssetCreate(h.workspace.Name+"-gained", h.workspace.ID))
						check(t, err)
						want = append(want, row)
					}
					wantStatus = 200
				case "stale_revision", "readonly_stale_revision", "deleted_stale_revision":
					if mode == "readonly_stale_revision" {
						values.Set("assets-2-code", newCode)
						files = []int{2}
					} else {
						values.Set("assets-0-code", updatedCode)
						files = []int{0}
					}
					if mode == "deleted_stale_revision" {
						values.Set("assets-0-DELETE", "on")
					}
					h.beforeWrite = func() {
						current := h.initial[0]
						current.Revision, current.Note = 2, new("external revision")
						check(t, inventory.AssetObjects.Save(t.Context(), backend, &current))
						want[0] = current
					}
					wantStatus = 409
				case "reordered_identities":
					first, second := url.Values{}, url.Values{}
					for key, value := range values {
						if strings.HasPrefix(key, "assets-0-") {
							first[strings.TrimPrefix(key, "assets-0-")] = slices.Clone(value)
						}
						if strings.HasPrefix(key, "assets-1-") {
							second[strings.TrimPrefix(key, "assets-1-")] = slices.Clone(value)
						}
					}
					for key := range values {
						if strings.HasPrefix(key, "assets-0-") || strings.HasPrefix(key, "assets-1-") {
							delete(values, key)
						}
					}
					for key, value := range first {
						values["assets-1-"+key] = value
					}
					for key, value := range second {
						values["assets-0-"+key] = value
					}
					// Preserve each submitted position's ORDER to change only the
					// intended model, not the other row's form-order intent.
					values.Set("assets-0-ORDER", "1")
					values.Set("assets-1-ORDER", "2")
					values.Set("assets-0-code", updatedCode)
					files, wantFiles = []int{0}, []int{0}
					want[1].Code, want[1].Revision = updatedCode, 2
					wantWrites, wantAudits = 1, 1
				case "foreign_identity":
					workspace, err := directory.WorkspaceObjects.Create(t.Context(), backend, directory.NewWorkspaceCreate(h.workspace.Name+" outside"))
					check(t, err)
					value, err := inventory.AssetObjects.Create(t.Context(), backend, inventory.NewAssetCreate(h.workspace.Name+"-foreign", workspace.ID))
					check(t, err)
					foreign = &value
					values.Set("assets-0-id", strconv.FormatInt(value.ID, 10))
					values.Set("assets-0-DELETE", "on")
					files = []int{0}
					wantStatus, wantWriter, wantCurrent = 200, 0, 0
				case "changed_policy":
					values.Set("assets-0-code", updatedCode)
					if evolved {
						values.Set("assets-0-priority", "2")
						response := h.submit(t.Context(), values)
						if response.Code != 200 || h.writerCalls != 0 || !strings.Contains(response.Body.String(), "invalid_choice") {
							t.Fatal("changed IR choices bypassed form admission", response.Code)
						}
						values.Set("assets-0-priority", "9")
					}
					want[0].Code, want[0].Revision = updatedCode, 2
					wantWrites, wantAudits = 1, 1
				}
				response := h.submit(ctx, values, files...)
				if response.Code != wantStatus || h.writerCalls != wantWriter || h.currentLoads != wantCurrent || h.writes != wantWrites || h.backend.atomics != wantWriter {
					t.Fatal("native collection outcome", mode, response.Code, wantStatus, h.writerCalls, h.currentLoads, h.writes, wantWrites, response.Body.String())
				}
				if wantStatus == 302 && !strings.HasPrefix(response.Header().Get("Location"), "/admin/assets/?") || wantStatus != 302 && response.Header().Get("Location") != "" {
					t.Fatal("uncertain result became success redirect")
				}
				if strings.Contains(response.Body.String(), "private late native audit") {
					t.Fatal("private writer error disclosed")
				}
				if mode == "protected_delete_rolls_back_and_keeps_files" && !strings.Contains(response.Body.String(), `data-error-code="protected"`) {
					t.Fatal("protected delete diagnostic missing")
				}
				if mode == "foreign_identity" && !strings.Contains(response.Body.String(), `data-error-code="invalid_identity"`) {
					t.Fatal("forged deleted identity suppressed admission error")
				}
				rows := h.current()
				byID := map[int64]inventory.Asset{}
				for _, row := range rows {
					byID[row.ID] = row
				}
				committed := wantStatus == 302 || mode == "commit_unknown_keeps_native_state"
				if committed {
					for _, receipt := range h.receipts {
						if receipt.Index() == 0 {
							index := 0
							if mode == "reordered_identities" {
								index = 1
							}
							want[index].Attachment = receipt.File().Info().Name()
						}
					}
				}
				for index, expected := range want {
					if removed && index == 1 {
						if _, present := byID[expected.ID]; present {
							t.Fatal("deleted model survived")
						}
						continue
					}
					stored, present := byID[expected.ID]
					if !present {
						t.Fatal("current model disappeared", expected.ID)
					}
					// The evolving field is verified through the same generated
					// metadata API; this consumer has no second typed save mapping.
					if evolved && (mode == "mixed_current_and_files" || mode == "changed_policy") && index == 0 {
						values, err := inventory.AssetObjects.ModelValues(stored)
						check(t, err)
						if !values["priority"].Equal(query.Integer(9)) {
							t.Fatal("evolved field did not reach native storage")
						}
						expected, err = inventory.AssetObjects.ApplyValues(expected, map[string]query.Value{"priority": query.Integer(9)})
						check(t, err)
					}
					if !reflect.DeepEqual(stored, expected) {
						t.Fatal("scalar/revision/file reference readback differs", mode, stored, expected)
					}
				}
				wantCount := len(want)
				if removed {
					wantCount--
				}
				if created {
					wantCount++
				}
				if len(rows) != wantCount {
					t.Fatal("partial/new/cohort state mismatch", len(rows), wantCount)
				}
				if created {
					var found *inventory.Asset
					for index := range rows {
						if rows[index].Code == newCode {
							found = &rows[index]
						}
					}
					if found == nil || found.WorkspaceID != h.workspace.ID || found.Revision != 1 || found.Stamp != "server" || found.Note != nil || found.Attachment == "" {
						t.Fatal("new row lost generated defaults/server relation/file")
					}
					if evolved && mode == "mixed_current_and_files" {
						values, err := inventory.AssetObjects.ModelValues(*found)
						check(t, err)
						if !values["priority"].Equal(query.Integer(1)) {
							t.Fatal("new evolved choice lost")
						}
					}
				}
				audits := h.auditRows()
				if len(audits) != wantAudits {
					t.Fatal("audit did not commit/roll back with models", len(audits), wantAudits)
				}
				for _, event := range audits {
					if event.Actor == "" || event.Revision < 1 || event.Action != "create" && event.Action != "update" && event.Action != "delete" {
						t.Fatal("invalid native audit record")
					}
				}
				if foreign != nil {
					current, err := inventory.AssetObjects.Using(backend).Filter(inventory.AssetFields.ID.Exact(foreign.ID)).Get(t.Context())
					check(t, err)
					if !reflect.DeepEqual(current, *foreign) {
						t.Fatal("foreign object changed")
					}
				}
				if mode == "late_audit_rollback_keeps_files" || mode == "rollback_unknown_preserves_failure" {
					if h.audits != 2 {
						t.Fatal("late failure did not follow two actual audit inserts")
					}
				}
				if mode == "post_commit_cancellation" && ctx.Err() != context.Canceled {
					t.Fatal("confirmed commit cancellation fixture did not fire")
				}
				h.checkFiles(wantFiles, outcomes...)
			})
		}
	})
}
