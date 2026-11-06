package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/admin/testdata/browser/collectionproject"
	"github.com/progresshans/godj/admin/testdata/browser/inventory"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const collectionBrowserPath = "/admin/assets/collection-set/edit/"

//go:embed widgets/*.html
var collectionWidgetFiles embed.FS

var collectionPermissions = admin.Permissions{View: "browser_inventory.view_asset", Add: "browser_inventory.add_asset", Change: "browser_inventory.change_asset", Delete: "browser_inventory.delete_asset"}

// The fixture's audit receipt is synchronized independently of the real database
// transaction. Publication attempts remain observable after a DB rollback.
type collectionAttempt struct {
	Number       int              `json:"number"`
	Actor        string           `json:"actor"`
	Committed    bool             `json:"committed"`
	Writes       int              `json:"writes"`
	Ordered      []int            `json:"ordered"`
	Publications []map[string]any `json:"publications"`
}

type collectionBrowserFixture struct {
	backend                           *sqlite.Backend
	storage                           *storage.Filesystem
	storageDirectory, uploadDirectory string
	binding                           orm.ProjectBinding
	relations                         collectionproject.Relations
	choices                           formmodel.ModelChoices[models.Label]
	mu                                sync.Mutex
	attempts                          []collectionAttempt
	retained                          []uploads.File
	seedCategories                    []models.Category
	seedLabels                        []models.Label
	foreign                           inventory.Asset
}

func runCollectionFixture() (runErr error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	directory, err := os.MkdirTemp("", "godj-collection-browser-")
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, os.RemoveAll(directory)) }()
	fixture := &collectionBrowserFixture{storageDirectory: filepath.Join(directory, "files"), uploadDirectory: filepath.Join(directory, "uploads")}
	for _, path := range []string{fixture.storageDirectory, fixture.uploadDirectory} {
		if err := os.Mkdir(path, 0700); err != nil {
			return err
		}
	}
	fixture.backend, err = sqlite.Open(ctx, "file:"+filepath.Join(directory, "fixture.sqlite3")+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, fixture.backend.Close()) }()
	fixture.storage, err = storage.OpenFilesystem(ctx, storage.FilesystemConfig{Directory: fixture.storageDirectory})
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, fixture.storage.Close()) }()
	fixture.binding, err = collectionproject.Bind()
	if err != nil {
		return err
	}
	fixture.relations, err = collectionproject.BindRelations()
	if err != nil {
		return err
	}
	fixture.choices, err = formmodel.NewRelationChoices(fixture.binding, ir.ModelIdentity{AppLabel: "browser_inventory", ModelName: "asset"}, "labels", models.LabelObjects, formmodel.ModelChoiceConfig[models.Label]{
		Label: func(label models.Label) (string, error) { return label.Name, nil },
		Search: func(term string) ([]orm.Predicate[models.Label], error) {
			if term == "" {
				return nil, nil
			}
			return []orm.Predicate[models.Label]{models.LabelFields.Name.IContains(term)}, nil
		}, Order: []orm.Ordering[models.Label]{models.LabelFields.ID.Asc()},
	})
	if err != nil {
		return err
	}
	if err = fixture.migrate(ctx); err != nil {
		return err
	}
	if err = fixture.seed(ctx); err != nil {
		return err
	}
	installed := append(helpdesk.InstalledApps(), apps.Config{Name: "github.com/progresshans/godj/admin/testdata/browser/inventory", Label: "browser_inventory"})
	configured, err := settings.New(settings.Definition{ProjectName: "collection_browser", InstalledApps: installed})
	if err != nil {
		return err
	}
	builder := admin.NewBuilder(configured.Apps())
	if err := fixture.register(builder); err != nil {
		return err
	}
	registry, err := builder.Build()
	if err != nil {
		return err
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		return err
	}
	password, err := hasher.Hash(ctx, "demo-password")
	if err != nil {
		return err
	}
	var credentials []auth.Credential
	for _, role := range []string{"editor", "readonly", "noadd", "viewer"} {
		grants := []auth.Permission{collectionPermissions.View, "helpdesk.view_label"}
		if role == "editor" || role == "readonly" {
			grants = append(grants, collectionPermissions.Add)
		}
		if role == "editor" || role == "noadd" {
			grants = append(grants, collectionPermissions.Change, collectionPermissions.Delete)
		}
		actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: role, Active: true, Staff: true, Permissions: grants})
		if err != nil {
			return err
		}
		credential, err := auth.NewCredential(role, password, actor)
		if err != nil {
			return err
		}
		credentials = append(credentials, credential)
	}
	authenticator, err := auth.NewMemoryAuthenticator(credentials, hasher, auth.LoginInputConfig{Label: "Username", MaxLength: auth.MaximumUsernameBytes, Normalization: auth.LoginExact})
	if err != nil {
		return err
	}
	store, err := sessions.NewMemoryStore(64)
	if err != nil {
		return err
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		return err
	}
	allowed, err := admin.SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		return err
	}
	authentication, err := sessionauth.New(sessionauth.Config{Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{}, SessionCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{Path: "/", AllowInsecure: true}, LoginPath: "/admin/login/", FallbackPath: "/admin/", AllowedNextPaths: allowed})
	if err != nil {
		return err
	}
	widgetFiles, err := fs.Sub(collectionWidgetFiles, "widgets")
	if err != nil {
		return err
	}
	uploadPolicy := uploads.DefaultConfig()
	uploadPolicy.MemoryBytes, uploadPolicy.TempDir = 0, fixture.uploadDirectory
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "browser_inventory", Registry: registry, Auth: authentication, Uploads: &uploadPolicy,
		Widgets: admin.WidgetConfig{Templates: widgetFiles, Entries: []admin.WidgetTemplate{{Name: "asset_code", Widget: forms.TextInput, Template: "code.html"}, {Name: "asset_file", Widget: forms.ClearableFileInput, Template: "file.html"}}},
	})
	if err != nil {
		return err
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes(), Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: application, ReadHeaderTimeout: 5 * time.Second}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	fmt.Printf("http://%s%s\nfixture_pid=%d\n", listener.Addr(), collectionBrowserPath, os.Getpid())
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return err
		}
		select {
		case err := <-result:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		case <-shutdown.Done():
			return shutdown.Err()
		}
		return fixture.record(shutdown)
	}
}

func (fixture *collectionBrowserFixture) migrate(ctx context.Context) error {
	migration := migrations.Migration{App: "browser_inventory", Name: "0001_initial", Dependencies: []migrations.MigrationKey{{App: "helpdesk", Name: "0023_ticket_external_payload_digest"}}}
	schema := inventory.GoDjRelationSchema()
	for _, model := range schema.Models {
		model.ManyToMany = nil
		migration.Operations = append(migration.Operations, migrations.CreateModel{AppLabel: migration.App, Model: model})
	}
	// Explicit through models must exist before their collection metadata is
	// introduced, even when all operations belong to the same initial migration.
	for _, model := range schema.Models {
		for _, field := range model.ManyToMany {
			migration.Operations = append(migration.Operations, migrations.AddManyToMany{AppLabel: migration.App, ModelName: model.Name, Field: field})
		}
	}
	wire, err := definition.Encode(definition.Producer{Name: "collection-browser", Version: "1"}, migration)
	if err != nil {
		return err
	}
	sources := append(helpdesk.MigrationSources(), definition.Source{SourceID: "browser_inventory", Document: wire})
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		return err
	}
	_, err = (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	return err
}

func (fixture *collectionBrowserFixture) seed(ctx context.Context) error {
	for _, name := range []string{"Inventory category", "Foreign category"} {
		category, err := models.CategoryObjects.Create(ctx, fixture.backend, models.NewCategoryCreate(name))
		if err != nil {
			return err
		}
		fixture.seedCategories = append(fixture.seedCategories, category)
	}
	for index := 1; index <= 26; index++ {
		category := int64(1)
		if index == 26 {
			category = 2
		}
		label, err := models.LabelObjects.Create(ctx, fixture.backend, models.NewLabelCreate(fmt.Sprintf("Inventory label %02d", index), category))
		if err != nil {
			return err
		}
		fixture.seedLabels = append(fixture.seedLabels, label)
	}
	for index, code := range []string{"FIRST", "SECOND", "FOREIGN"} {
		category, label := int64(1), int64(25)
		if index == 1 {
			label = 2
		}
		if index == 2 {
			category, label = 2, 26
		}
		name := fmt.Sprintf("old/%d.txt", index+1)
		if _, err := fixture.storage.Save(ctx, name, strings.NewReader("old "+code), storage.SaveOptions{}); err != nil {
			return err
		}
		asset, err := inventory.AssetObjects.Create(ctx, fixture.backend, inventory.NewAssetCreate(code, category).WithEnabled(index != 1).WithAttachment(name))
		if err != nil {
			return err
		}
		if _, err := inventory.AssetLabelObjects.Create(ctx, fixture.backend, inventory.NewAssetLabelCreate(asset.ID, label)); err != nil {
			return err
		}
		if index == 2 {
			fixture.foreign = asset
		}
	}
	return nil
}

func (fixture *collectionBrowserFixture) cohort(ctx context.Context, reader db.Queryer) (admin.CollectionSetSnapshot[inventory.Asset], error) {
	rows, err := inventory.AssetObjects.Using(reader).Filter(fixture.relations.InventoryAsset.Category.ID.Exact(1)).OrderBy(inventory.AssetFields.ID.Asc()).Limit(7)
	if err != nil {
		return admin.CollectionSetSnapshot[inventory.Asset]{}, err
	}
	current, err := rows.All(ctx)
	if err != nil {
		return admin.CollectionSetSnapshot[inventory.Asset]{}, err
	}
	if len(current) > 6 {
		return admin.CollectionSetSnapshot[inventory.Asset]{}, errors.New("collection browser cohort overflow")
	}
	keys := make([]int64, len(current))
	members := make(map[int64][]int64, len(current))
	for index, row := range current {
		keys[index] = row.ID
		members[row.ID] = []int64{}
	}
	if len(keys) != 0 {
		rows, err := inventory.AssetLabelObjects.Using(reader).Filter(fixture.relations.InventoryAssetLabel.Asset.ID.In(keys...)).OrderBy(inventory.AssetLabelFields.LabelID.Asc()).Limit(157)
		if err != nil {
			return admin.CollectionSetSnapshot[inventory.Asset]{}, err
		}
		links, err := rows.All(ctx)
		if err != nil {
			return admin.CollectionSetSnapshot[inventory.Asset]{}, err
		}
		if len(links) > 156 {
			return admin.CollectionSetSnapshot[inventory.Asset]{}, errors.New("collection browser membership overflow")
		}
		for _, link := range links {
			members[link.AssetID] = append(members[link.AssetID], link.LabelID)
		}
	}
	return admin.CollectionSetSnapshot[inventory.Asset]{Current: current, Related: func(value inventory.Asset, field ir.ManyToManyField) ([]int64, bool) {
		values, present := members[value.ID]
		return slices.Clone(values), present && field.Name == "labels"
	}}, nil
}

func (fixture *collectionBrowserFixture) register(builder *admin.Builder) error {
	labelSource, err := admin.NewRelatedChoices(admin.RelatedChoiceConfig[models.Label]{Choices: fixture.choices, Permission: "helpdesk.view_label",
		Scope: func(ctx context.Context, actor auth.Principal, origin admin.ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[models.Label]) error) error {
			if !actor.Has("helpdesk.view_label") {
				return admin.NewOperationError(admin.OperationDenied, nil)
			}
			if key, present := origin.RowIdentity(); present {
				found, err := inventory.AssetObjects.Using(fixture.backend).Filter(inventory.AssetFields.ID.Exact(key), fixture.relations.InventoryAsset.Category.ID.Exact(1)).Exists(ctx)
				if err != nil {
					return err
				}
				if !found {
					return admin.ErrObjectNotFound
				}
			}
			return visit(fixture.backend, []orm.Predicate[models.Label]{fixture.relations.HelpdeskLabel.Category.ID.Exact(1)})
		},
	})
	if err != nil {
		return err
	}
	collections, err := collectionproject.BindCollections()
	if err != nil {
		return err
	}
	saver, err := formmodel.SaveManyToMany(collections.InventoryAssetLabels)
	if err != nil {
		return err
	}
	deleters, err := collectionproject.BindRelationDeleters()
	if err != nil {
		return err
	}
	policy := forms.DefaultSetConfig()
	policy.Prefix, policy.ExtraForms, policy.MaxForms, policy.AbsoluteMax = "assets", 0, 6, 6
	policy.CanOrder, policy.CanDelete, policy.ValidateMin, policy.ValidateMax = true, true, true, true
	var declaration admin.ModelCollectionFormSet[inventory.Asset]
	declaration, err = admin.NewCollectionFormSet(admin.CollectionFormSetConfig[inventory.Asset]{Name: "edit", Label: "Edit inventory", Project: fixture.binding, Source: ir.ModelIdentity{AppLabel: "browser_inventory", ModelName: "asset"}, Manager: inventory.AssetObjects, Permissions: collectionPermissions, RevisionField: "revision", Set: policy,
		Form: admin.FormConfig{Definition: formmodel.Definition{Fields: []string{"code", "enabled", "attachment", "labels"}, Overrides: []formmodel.Override{
			formmodel.OverrideField("code", formmodel.WithStringNormalizer(strings.ToUpper), formmodel.WithWidgetTemplate("asset_code"), formmodel.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
				code, _ := value.AsString()
				if strings.HasPrefix(code, "REJECT") {
					return validation.NewErrors(validation.New("code", "code_review"))
				}
				return validation.Errors{}
			}))), formmodel.OverrideField("attachment", formmodel.WithWidgetTemplate("asset_file")),
		}, PostClean: formmodel.PostClean{Fields: []string{"category"}, Clean: func(values forms.Values) (forms.Values, validation.Errors) {
			var failures validation.Errors
			if code, _ := values.String("code"); code == "" {
				failures = validation.NewErrors(validation.New(validation.NonField, "model_review"))
			}
			return forms.NewValues(map[string]forms.Value{"category": forms.Integer(1)}), failures
		}}}, RelatedChoices: []admin.RelatedChoices{labelSource}},
		Validators: []forms.SetValidator{forms.SetValidatorFunc(func(rows []forms.SetForm) validation.Errors {
			for _, row := range rows {
				if !row.DeletionRequested() && !row.Form().Valid() {
					return validation.NewErrors(validation.New(validation.NonField, "collection_review"))
				}
			}
			return validation.Errors{}
		})},
		Load: func(ctx context.Context, _ auth.Principal, _ admin.CollectionSetAccess) (admin.CollectionSetSnapshot[inventory.Asset], error) {
			var snapshot admin.CollectionSetSnapshot[inventory.Asset]
			err := fixture.backend.ReadSnapshot(ctx, func(reader db.Queryer) error { var err error; snapshot, err = fixture.cohort(ctx, reader); return err })
			return snapshot, err
		},
		Run: func(ctx context.Context, actor auth.Principal, submitted admin.CollectionSetSubmission) (admin.CollectionSetResult, error) {
			fixture.mu.Lock()
			number := len(fixture.attempts) + 1
			fixture.attempts = append(fixture.attempts, collectionAttempt{Number: number, Actor: actor.ID(), Ordered: []int{}, Publications: []map[string]any{}})
			fixture.mu.Unlock()
			attempt := collectionAttempt{Number: number, Actor: actor.ID(), Ordered: []int{}, Publications: []map[string]any{}}
			result, failure := db.RelationAtomicResult(ctx, fixture.backend, func(work context.Context, session db.RelationSession) (admin.CollectionSetResult, error) {
				bound, err := declaration.BindCurrent(work, actor, submitted, func(current context.Context, access admin.CollectionSetAccess) (admin.CollectionSetCurrent[inventory.Asset], error) {
					rows, err := fixture.cohort(current, session)
					if err != nil {
						return admin.CollectionSetCurrent[inventory.Asset]{}, err
					}
					return admin.CollectionSetCurrent[inventory.Asset]{Rows: rows, ResolveChoices: func(current context.Context, input formmodel.SetPreflight[inventory.Asset]) (forms.RowModelChoices, error) {
						selected := make(map[int]map[string][]forms.Choice)
						for index := 0; index < input.TotalForms(); index++ {
							keys, valid, err := input.ChoiceKeys(index, "labels")
							if err != nil {
								return forms.RowModelChoices{}, err
							}
							if !valid {
								continue
							}
							if !actor.Has("helpdesk.view_label") || !access.ChoiceAllowed("labels") {
								return forms.RowModelChoices{}, admin.NewOperationError(admin.OperationDenied, nil)
							}
							resolved, err := fixture.choices.Filter(fixture.relations.HelpdeskLabel.Category.ID.Exact(1)).Resolve(current, session, keys)
							if err != nil {
								return forms.RowModelChoices{}, err
							}
							selected[index] = map[string][]forms.Choice{"labels": resolved.Items()}
						}
						return forms.NewRowModelChoices(selected, nil), nil
					}}, nil
				})
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				if !bound.Set().Valid() {
					return admin.CollectionSetResult{}, admin.RejectCollectionFormSet(nil, validation.NewErrors(validation.New(validation.NonField, "current_cohort")), nil)
				}
				if bound.Access().Change {
					ordered, err := bound.Set().FormSet().OrderedForms()
					if err != nil {
						return admin.CollectionSetResult{}, err
					}
					for _, row := range ordered {
						attempt.Ordered = append(attempt.Ordered, row.Index())
					}
				}
				for index := 0; index < submitted.FormSet().TotalForms(); index++ {
					files, _ := submitted.FormSet().Submitted().Files(fmt.Sprintf("assets-%d-attachment", index))
					fixture.mu.Lock()
					fixture.retained = append(fixture.retained, files...)
					fixture.mu.Unlock()
				}
				prepared, err := bound.Set().Prepare()
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				prepared, publications, err := prepared.SaveFiles(work, func(row formmodel.PreparedSetRow[inventory.Asset]) ([]formmodel.FileSaver[inventory.Asset], error) {
					return []formmodel.FileSaver[inventory.Asset]{{Field: "attachment", Backend: fixture.storage, Name: func(_ inventory.Asset, file uploads.File) (string, error) {
						return fmt.Sprintf("attempt-%d/%d/%s", number, row.Index(), file.Name()), nil
					}}}, nil
				})
				for _, publication := range publications {
					file := publication.File()
					attempt.Publications = append(attempt.Publications, map[string]any{"index": publication.Index(), "field": file.Field(), "name": file.Info().Name(), "published": file.Outcome() == storage.Published, "error": file.Err() != nil})
				}
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				plan, err := prepared.SavePlan()
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				var writes []admin.CollectionSetWrite
				fence := func(value *inventory.Asset) error {
					count, err := inventory.AssetObjects.Using(session).Filter(inventory.AssetFields.ID.Exact(value.ID), inventory.AssetFields.Revision.Exact(value.Revision), fixture.relations.InventoryAsset.Category.ID.Exact(1)).Update(work, orm.Assign(inventory.AssetFields.Revision, value.Revision+1))
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
					attempt.Writes++
					if _, err := inventory.AuditObjects.Create(work, session, inventory.NewAuditCreate(kind, value.ID, actor.ID(), value.Revision)); err != nil {
						return err
					}
					if value.Code == "AUDIT_FAIL" {
						return admin.RejectCollectionFormSet(nil, validation.NewErrors(validation.New(validation.NonField, "writer_review")), nil)
					}
					return nil
				}
				_, err = plan.Save(work, session, formmodel.SetSaveOptions[inventory.Asset]{Save: func(current context.Context, scope db.Session, row *formmodel.SetSaveRow[inventory.Asset]) error {
					kind, allowed, permission := "create", bound.Access().Add, collectionPermissions.Add
					if row.Existing() {
						kind, allowed, permission = "update", bound.Access().Change, collectionPermissions.Change
					}
					if current != work || scope != session || !allowed || !actor.Has(permission) {
						return admin.NewOperationError(admin.OperationDenied, nil)
					}
					if row.Existing() {
						if err := fence(row.Model()); err != nil {
							return err
						}
					}
					if err := row.Save(current, scope, saver); err != nil {
						return err
					}
					if err := audit(kind, *row.Model()); err != nil {
						return err
					}
					writes = append(writes, admin.CollectionSetWrite{Index: row.Index(), Kind: kind, ID: row.Model().ID})
					return nil
				}, Delete: func(current context.Context, scope db.Session, row formmodel.DeletedSetRow[inventory.Asset]) error {
					if current != work || scope != session || !bound.Access().Delete || !actor.Has(collectionPermissions.Delete) {
						return admin.NewOperationError(admin.OperationDenied, nil)
					}
					value, err := row.Model()
					if err != nil {
						return err
					}
					if err := fence(&value); err != nil {
						return err
					}
					if _, err := deleters.InventoryAsset.DeleteInSession(current, session, value); err != nil {
						return err
					}
					if err := audit("delete", value); err != nil {
						return err
					}
					writes = append(writes, admin.CollectionSetWrite{Index: row.Index(), Kind: "delete", ID: value.ID})
					return nil
				}})
				if err != nil {
					return admin.CollectionSetResult{}, err
				}
				return bound.Result(writes)
			})
			attempt.Committed = failure == nil
			fixture.mu.Lock()
			fixture.attempts[number-1] = attempt
			fixture.mu.Unlock()
			return result, failure
		},
	})
	if err != nil {
		return err
	}
	projector, err := admin.NewModelProjector(inventory.AssetDescriptor{}.Metadata(), inventory.AssetDescriptor{}.WriteFieldValue, "id", "code")
	if err != nil {
		return err
	}
	return admin.RegisterModel(builder, admin.ModelConfig[inventory.Asset]{AppLabel: "browser_inventory", Slug: "assets", Model: inventory.AssetDescriptor{}.Metadata(), FormFields: []string{"code"}, ListFields: []string{"id", "code"}, Permissions: collectionPermissions, CollectionFormSets: []admin.CollectionFormSet{declaration.View()},
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[inventory.Asset], error) {
			return choicePage(ctx, inventory.AssetObjects.Using(fixture.backend).Filter(fixture.relations.InventoryAsset.Category.ID.Exact(1)).OrderBy(inventory.AssetFields.ID.Asc()), request)
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (inventory.Asset, bool, error) {
			return inventory.AssetObjects.Using(fixture.backend).Filter(inventory.AssetFields.ID.Exact(id), fixture.relations.InventoryAsset.Category.ID.Exact(1)).OrderBy(inventory.AssetFields.ID.Asc()).First(ctx)
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
	})
}

func (fixture *collectionBrowserFixture) record(ctx context.Context) error {
	categories, err := models.CategoryObjects.Using(fixture.backend).OrderBy(models.CategoryFields.ID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	labels, err := models.LabelObjects.Using(fixture.backend).OrderBy(models.LabelFields.ID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	assets, err := inventory.AssetObjects.Using(fixture.backend).OrderBy(inventory.AssetFields.ID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	links, err := inventory.AssetLabelObjects.Using(fixture.backend).OrderBy(inventory.AssetLabelFields.AssetID.Asc(), inventory.AssetLabelFields.LabelID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	audits, err := inventory.AuditObjects.Using(fixture.backend).OrderBy(inventory.AuditFields.ID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	if len(categories) != len(fixture.seedCategories) || len(labels) != len(fixture.seedLabels) {
		return errors.New("collection browser target cohort changed")
	}
	for index, before := range fixture.seedCategories {
		if same, err := unchangedStored(models.CategoryObjects, before, categories[index]); err != nil {
			return err
		} else if !same {
			return errors.New("collection browser category changed")
		}
	}
	for index, before := range fixture.seedLabels {
		if same, err := unchangedStored(models.LabelObjects, before, labels[index]); err != nil {
			return err
		} else if !same {
			return errors.New("collection browser target changed")
		}
	}
	foundForeign := false
	for _, asset := range assets {
		if asset.ID == fixture.foreign.ID {
			same, err := unchangedStored(inventory.AssetObjects, fixture.foreign, asset)
			if err != nil {
				return err
			}
			foundForeign = same
		}
	}
	if !foundForeign {
		return errors.New("collection browser foreign asset changed")
	}
	fixture.mu.Lock()
	attempts := slices.Clone(fixture.attempts)
	retained := slices.Clone(fixture.retained)
	fixture.mu.Unlock()
	for _, file := range retained {
		if reader, err := file.Open(ctx); !errors.Is(err, &uploads.Error{Code: "closed"}) {
			if reader != nil {
				_ = reader.Close()
			}
			return errors.New("collection browser upload survived its request")
		}
	}
	pending, err := os.ReadDir(fixture.uploadDirectory)
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return errors.New("collection browser multipart temporaries remain")
	}
	state := map[string]any{"seed_scalars_preserved": true, "upload_capabilities_closed": len(retained), "upload_temporaries_empty": true, "attempts": attempts}
	var assetValues, linkValues, auditValues, fileValues []map[string]any
	for _, value := range assets {
		assetValues = append(assetValues, map[string]any{"id": value.ID, "code": value.Code, "enabled": value.Enabled, "category": value.CategoryID, "attachment": value.Attachment, "revision": value.Revision, "note": value.Note})
	}
	for _, value := range links {
		linkValues = append(linkValues, map[string]any{"asset": value.AssetID, "label": value.LabelID})
	}
	for _, value := range audits {
		auditValues = append(auditValues, map[string]any{"id": value.ID, "action": value.Action, "asset": value.AssetKey, "actor": value.Actor, "revision": value.Revision})
	}
	err = filepath.WalkDir(fixture.storageDirectory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("collection browser storage has a non-file entry")
		}
		name, err := filepath.Rel(fixture.storageDirectory, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		reader, err := fixture.storage.Open(ctx, name)
		if err != nil {
			return err
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, 1025))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			return errors.Join(readErr, closeErr)
		}
		if len(content) > 1024 {
			return errors.New("collection browser unexpected file size")
		}
		fileValues = append(fileValues, map[string]any{"name": name, "content": string(content)})
		return nil
	})
	if err != nil {
		return err
	}
	state["assets"], state["links"], state["audits"], state["files"] = assetValues, linkValues, auditValues, fileValues
	counts := make(map[string]int64)
	for _, item := range []struct {
		name  string
		count func(context.Context) (int64, error)
	}{{"tickets", models.TicketObjects.Using(fixture.backend).Count}, {"ticket_links", models.TicketLabelObjects.Using(fixture.backend).Count}, {"reports", models.ServiceReportObjects.Using(fixture.backend).Count}} {
		count, err := item.count(ctx)
		if err != nil {
			return err
		}
		counts[item.name] = count
	}
	state["other_rows"] = counts
	state["categories"] = []map[string]any{{"id": categories[0].ID, "name": categories[0].Name}, {"id": categories[1].ID, "name": categories[1].Name}}
	var labelValues []map[string]any
	for _, label := range labels {
		labelValues = append(labelValues, map[string]any{"id": label.ID, "name": label.Name, "category": label.CategoryID})
	}
	state["labels"] = labelValues
	return json.NewEncoder(os.Stdout).Encode(state)
}
