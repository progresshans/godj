// Command browser is an isolated local Admin UI fixture, not a deployed server.
// It creates and removes its own SQLite database. Synthetic logins use
// editor, noadd, readonly or viewer with password demo-password.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	directory, err := os.MkdirTemp("", "godj-inline-browser-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	backend, err := sqlite.Open(ctx, "file:"+filepath.Join(directory, "fixture.sqlite3")+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer backend.Close()
	sources, _, err := definition.Load(helpdesk.MigrationSources()...)
	if err != nil {
		return err
	}
	if _, err = (migrations.Executor{Backend: backend}).Migrate(ctx, sources, migrations.LatestLifecycleRequest()); err != nil {
		return err
	}
	category, err := models.CategoryObjects.Create(ctx, backend, models.NewCategoryCreate("Browser category"))
	if err != nil {
		return err
	}
	if _, err = models.LabelObjects.Create(ctx, backend, models.NewLabelCreate("Original __prefix__", category.ID)); err != nil {
		return err
	}
	if _, err = models.TicketObjects.Create(ctx, backend, models.NewTicketCreate("Existing ticket", category.ID)); err != nil {
		return err
	}
	configured, err := settings.New(settings.Definition{ProjectName: "inline_browser", InstalledApps: helpdesk.InstalledApps()})
	if err != nil {
		return err
	}
	builder := admin.NewBuilder(configured.Apps())
	binding, err := project.Bind()
	if err != nil {
		return err
	}
	relations, err := project.BindRelations()
	if err != nil {
		return err
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		return err
	}
	loadLabels := func(ctx context.Context, q db.Queryer, id int64) ([]models.Label, error) {
		rows, err := models.LabelObjects.Using(q).Filter(relations.ModelsLabel.Category.ID.Exact(id)).OrderBy(models.LabelFields.ID.Asc()).Limit(5)
		if err != nil {
			return nil, err
		}
		return rows.All(ctx)
	}
	loadTickets := func(ctx context.Context, q db.Queryer, id int64) ([]models.Ticket, error) {
		rows, err := models.TicketObjects.Using(q).Filter(relations.ModelsTicket.Category.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).Limit(4)
		if err != nil {
			return nil, err
		}
		return rows.All(ctx)
	}
	labels, err := newInline(binding, models.LabelObjects, "labels", []string{"name"}, 0, 4, backend, loadLabels)
	if err != nil {
		return err
	}
	tickets, err := newInline(binding, models.TicketObjects, "tickets", []string{"subject", "closed"}, 1, 3, backend, loadTickets)
	if err != nil {
		return err
	}
	parentSpec, err := formmodel.NewSpecForFields(models.CategoryDescriptor{}.Metadata(), []string{"name"})
	if err != nil {
		return err
	}
	projector, err := admin.NewModelProjector(models.CategoryDescriptor{}.Metadata(), models.CategoryDescriptor{}.WriteFieldValue, "id", "name")
	if err != nil {
		return err
	}
	save := func(ctx context.Context, actor auth.Principal, id int64, bound formmodel.BoundForm, input admin.InlineSubmission) (models.Category, []string, error) {
		var parent models.Category
		var changed []string
		err := backend.AtomicRelation(ctx, func(session db.RelationSession) error {
			permission := auth.Permission("helpdesk.add_category")
			if id != 0 {
				permission = "helpdesk.change_category"
			}
			if !actor.Has(permission) {
				return admin.NewOperationError(admin.OperationDenied, nil)
			}
			if id != 0 {
				current, found, err := models.CategoryObjects.Using(session).Filter(models.CategoryFields.ID.Exact(id)).OrderBy(models.CategoryFields.ID.Asc()).First(ctx)
				if err != nil {
					return err
				}
				if !found {
					return admin.ErrObjectNotFound
				}
				parent = current
			}
			// The pending or saved parent is bound before assigning a new key.
			before := parent
			prepared, err := formmodel.PrepareInstance(models.CategoryObjects, bound, &parent)
			if err != nil {
				return err
			}
			parent, err = prepared.Model()
			if err != nil {
				return err
			}
			if id == 0 || parent.Name != before.Name {
				if err = prepared.Save(ctx, session, &parent); err != nil {
					return err
				}
				changed = append(changed, "name")
			}
			for index, save := range []func() (bool, error){
				func() (bool, error) {
					return saveRows(ctx, session, actor, binding, models.LabelObjects, before, parent, "labels", []string{"name"}, 0, 4, input, loadLabels, deleters.ModelsLabel.DeleteInSession)
				},
				func() (bool, error) {
					return saveRows(ctx, session, actor, binding, models.TicketObjects, before, parent, "tickets", []string{"subject", "closed"}, 1, 3, input, loadTickets, deleters.ModelsTicket.DeleteInSession)
				},
			} {
				any, err := save()
				if err != nil {
					return err
				}
				if any {
					changed = append(changed, []string{"labels", "tickets"}[index])
				}
			}
			return nil
		})
		return parent, changed, err
	}
	err = admin.RegisterModel(builder, admin.ModelConfig[models.Category]{AppLabel: "helpdesk", Slug: "categories", Model: models.CategoryDescriptor{}.Metadata(), FormFields: []string{"name"}, ListFields: []string{"id", "name"}, Inlines: []admin.Inline{labels, tickets}, Permissions: permissions("category"),
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[models.Category], error) {
			rows := models.CategoryObjects.Using(backend).OrderBy(models.CategoryFields.ID.Asc())
			total, err := rows.Count(ctx)
			if err != nil {
				return admin.Page[models.Category]{}, err
			}
			rows, err = rows.Offset(request.Offset)
			if err != nil {
				return admin.Page[models.Category]{}, err
			}
			rows, err = rows.Limit(request.Limit)
			if err != nil {
				return admin.Page[models.Category]{}, err
			}
			items, err := rows.All(ctx)
			return admin.Page[models.Category]{Items: items, Total: total, Offset: request.Offset, Limit: request.Limit}, err
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (models.Category, bool, error) {
			return models.CategoryObjects.Using(backend).Filter(models.CategoryFields.ID.Exact(id)).OrderBy(models.CategoryFields.ID.Asc()).First(ctx)
		},
		Snapshot: func(value models.Category) (admin.Object, error) {
			return projector.Project(value, value.ID, value.Name)
		},
		Initial: func(value models.Category) (map[string]forms.Value, error) {
			return formmodel.InitialValues(models.CategoryDescriptor{}.Metadata(), parentSpec, value, models.CategoryDescriptor{}.WriteFieldValue)
		},
		Create: func(ctx context.Context, p auth.Principal, bound formmodel.BoundForm, input admin.InlineSubmission) (models.Category, error) {
			value, _, err := save(ctx, p, 0, bound, input)
			return value, err
		},
		Update: func(ctx context.Context, p auth.Principal, m admin.Mutation, bound formmodel.BoundForm, input admin.InlineSubmission) (models.Category, []string, error) {
			return save(ctx, p, m.ID, bound, input)
		},
		Delete: func(context.Context, auth.Principal, admin.Mutation) (models.Category, error) {
			return models.Category{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
	})
	if err != nil {
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
	for _, role := range []string{"editor", "noadd", "readonly", "viewer"} {
		grants := []auth.Permission{"helpdesk.view_category"}
		if role != "viewer" {
			grants = append(grants, "helpdesk.add_category", "helpdesk.change_category")
		}
		for _, child := range []string{"labels", "tickets"} {
			grants = append(grants, auth.Permission("helpdesk.view_"+child))
			if role == "editor" || role == "noadd" {
				grants = append(grants, auth.Permission("helpdesk.change_"+child), auth.Permission("helpdesk.delete_"+child))
			}
			if role == "editor" {
				grants = append(grants, auth.Permission("helpdesk.add_"+child))
			}
		}
		principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: role, Active: true, Staff: true, Permissions: grants})
		if err != nil {
			return err
		}
		credential, err := auth.NewCredential(role, password, principal)
		if err != nil {
			return err
		}
		credentials = append(credentials, credential)
	}
	authenticator, err := auth.NewMemoryAuthenticator(credentials, hasher)
	if err != nil {
		return err
	}
	memory, err := sessions.NewMemoryStore(64)
	if err != nil {
		return err
	}
	manager, err := sessions.NewManager(memory, sessions.Config{})
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
	site, err := admin.NewSite(admin.SiteConfig{Apps: configured.Apps(), Namespace: "helpdesk", Registry: registry, Auth: authentication})
	if err != nil {
		return err
	}
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes(), Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	fmt.Printf("http://%s/admin/categories/change/?id=%d\n", listener.Addr(), category.ID)
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func permissions(model string) admin.Permissions {
	return admin.Permissions{View: auth.Permission("helpdesk.view_" + model), Add: auth.Permission("helpdesk.add_" + model), Change: auth.Permission("helpdesk.change_" + model), Delete: auth.Permission("helpdesk.delete_" + model)}
}
func policy(prefix string, extra, maximum int) forms.SetConfig {
	c := forms.DefaultSetConfig()
	c.Prefix = prefix
	c.ExtraForms = extra
	if prefix == "tickets" {
		c.ExtraForms = 0
		c.MinForms = 2
		c.ValidateMin = true
	}
	c.MaxForms = maximum
	c.AbsoluteMax = maximum + 1
	c.ValidateMax = true
	c.CanDelete = true
	return c
}
func newInline[C any](binding orm.ProjectBinding, manager orm.Manager[C], prefix string, fields []string, extra, maximum int, reader db.Queryer, load func(context.Context, db.Queryer, int64) ([]C, error)) (admin.Inline, error) {
	return admin.NewInline(admin.InlineConfig[models.Category, C]{Project: binding, Parent: models.CategoryObjects, Child: manager, ParentWithID: models.NewCategoryWithID, ForeignKey: "category", Label: prefix, Form: admin.FormConfig{Definition: formmodel.Definition{Fields: fields}}, Set: policy(prefix, extra, maximum), Permissions: permissions(prefix), Load: func(ctx context.Context, _ auth.Principal, id int64) (admin.InlineSnapshot[C], error) {
		current, err := load(ctx, reader, id)
		if err == nil && len(current) > maximum {
			err = errors.New("fixture cohort overflow")
		}
		return admin.InlineSnapshot[C]{Current: current}, err
	}})
}

func saveRows[C any](ctx context.Context, session db.RelationSession, actor auth.Principal, binding orm.ProjectBinding, manager orm.Manager[C], before, parent models.Category, prefix string, fields []string, extra, maximum int, input admin.InlineSubmission, load func(context.Context, db.Queryer, int64) ([]C, error), remove func(context.Context, db.RelationSession, C) (int64, error)) (bool, error) {
	data, access, present := input.Lookup(prefix)
	if !present {
		return false, nil
	}
	var current []C
	var err error
	if before.ID != 0 && (access.View || access.Change) {
		current, err = load(ctx, session, before.ID)
		if err != nil {
			return false, err
		}
	}
	metadata, err := manager.Metadata()
	if err != nil {
		return false, err
	}
	row, err := formmodel.NewSpecForFields(metadata, fields)
	if err != nil {
		return false, err
	}
	config := policy(prefix, extra, maximum)
	config.ReadOnlyInitial = !access.Change
	config.CanDelete = access.Delete
	if !access.Add {
		config.ExtraForms = 0
	}
	rows, err := forms.NewSetSpec(row, config)
	if err != nil {
		return false, err
	}
	spec, err := formmodel.NewInlineSpec(binding, models.CategoryObjects, manager, "category", rows)
	if err != nil {
		return false, err
	}
	set, err := spec.Bind(data, before, current, formmodel.PostClean{})
	if err != nil {
		return false, err
	}
	if !set.Valid() {
		return false, admin.RejectInline(prefix, -1, validation.NewErrors(validation.New(validation.NonField, "stale_inline")), nil)
	}
	prepared, err := set.PrepareWithParent(parent)
	if err != nil {
		return false, err
	}
	changed := false
	for _, deleted := range prepared.Deleted() {
		if !access.Delete || !actor.Has(permissions(prefix).Delete) {
			return false, admin.NewOperationError(admin.OperationDenied, nil)
		}
		value, err := deleted.Model()
		if err != nil {
			return false, err
		}
		if _, err = remove(ctx, session, value); err != nil {
			return false, err
		}
		changed = true
	}
	for _, row := range prepared.Rows() {
		if row.Existing() && len(row.Changed()) == 0 {
			continue
		}
		if row.Existing() && (!access.Change || !actor.Has(permissions(prefix).Change)) || !row.Existing() && (!access.Add || !actor.Has(permissions(prefix).Add)) {
			return false, admin.NewOperationError(admin.OperationDenied, nil)
		}
		value, err := row.Model()
		if err != nil {
			return false, err
		}
		if err = row.Prepared().Save(ctx, session, &value); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}
