package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
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
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const choiceLargeID int64 = 9007199254740993
const choiceUnsafeLabel = `Choice <img src=x onerror="window.choiceInjected=true">`

type choiceTicket struct {
	models.Ticket
	labels []int64
}

type choiceFixture struct {
	backend        *sqlite.Backend
	binding        orm.ProjectBinding
	relations      project.Relations
	collections    project.Collections
	labels         formmodel.ModelChoices[models.Label]
	seedCategories []models.Category
	seedLabels     []models.Label
	seedTickets    []models.Ticket
}

func runChoiceFixture() (runErr error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	directory, err := os.MkdirTemp("", "godj-choice-browser-")
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, os.RemoveAll(directory)) }()
	backend, err := sqlite.Open(ctx, "file:"+filepath.Join(directory, "fixture.sqlite3")+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, backend.Close()) }()
	sources, _, err := definition.Load(helpdesk.MigrationSources()...)
	if err != nil {
		return err
	}
	if _, err = (migrations.Executor{Backend: backend}).Migrate(ctx, sources, migrations.LatestLifecycleRequest()); err != nil {
		return err
	}
	fixture := &choiceFixture{backend: backend}
	fixture.binding, err = project.Bind()
	if err != nil {
		return err
	}
	fixture.relations, err = project.BindRelations()
	if err != nil {
		return err
	}
	fixture.collections, err = project.BindCollections()
	if err != nil {
		return err
	}
	fixture.labels, err = fixture.labelChoices("ticket", "labels")
	if err != nil {
		return err
	}
	if err = fixture.seed(ctx); err != nil {
		return err
	}
	configured, err := settings.New(settings.Definition{ProjectName: "choice_browser", InstalledApps: helpdesk.InstalledApps()})
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
	for _, role := range []string{"editor", "readonly", "denied", "viewer"} {
		grants := []auth.Permission{permissions("category").View, permissions("ticket").View, permissions("tickets").View}
		if role != "viewer" {
			grants = append(grants, permissions("category").Change, permissions("ticket").Change)
		}
		if role == "editor" || role == "readonly" {
			grants = append(grants, permissions("label").View)
		}
		if role == "editor" {
			grants = append(grants, permissions("tickets").Add, permissions("tickets").Change, permissions("tickets").Delete,
				permissions("label").Change, permissions("ticket_label").View, permissions("ticket_label").Change)
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
	authenticator, err := auth.NewMemoryAuthenticator(credentials, hasher)
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
	fmt.Printf("http://%s/admin/tickets/change/?id=1\n", listener.Addr())
	fmt.Printf("fixture_pid=%d\n", os.Getpid())
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

func (fixture *choiceFixture) seed(ctx context.Context) error {
	for _, name := range []string{"Choice category", "Foreign category"} {
		value, err := models.CategoryObjects.Create(ctx, fixture.backend, models.NewCategoryCreate(name))
		if err != nil {
			return err
		}
		fixture.seedCategories = append(fixture.seedCategories, value)
	}
	for _, item := range []struct {
		id   int64
		name string
	}{{-7, "Choice negative"}, {0, "Choice zero"}} {
		value := models.NewLabelWithID(item.id)
		value.Name, value.CategoryID = item.name, 1
		if err := models.LabelObjects.Save(ctx, fixture.backend, &value, models.LabelForceInsert()); err != nil {
			return err
		}
		fixture.seedLabels = append(fixture.seedLabels, value)
	}
	for id := int64(1); id <= 32; id++ {
		name := fmt.Sprintf("Choice %02d", id)
		if id == 31 {
			name = "Choice stale"
		}
		if id == 32 {
			name = choiceUnsafeLabel
		}
		value := models.NewLabelWithID(id)
		value.Name, value.CategoryID = name, 1
		if err := models.LabelObjects.Save(ctx, fixture.backend, &value, models.LabelForceInsert()); err != nil {
			return err
		}
		fixture.seedLabels = append(fixture.seedLabels, value)
	}
	for _, item := range []struct {
		id, category int64
		name         string
	}{{choiceLargeID, 1, "Choice selected outside page"}, {choiceLargeID + 1, 2, "Choice foreign"}} {
		value := models.NewLabelWithID(item.id)
		value.Name, value.CategoryID = item.name, item.category
		if err := models.LabelObjects.Save(ctx, fixture.backend, &value, models.LabelForceInsert()); err != nil {
			return err
		}
		fixture.seedLabels = append(fixture.seedLabels, value)
	}
	for index, name := range []string{"Choice owner", "Foreign owner"} {
		value, err := models.TicketObjects.Create(ctx, fixture.backend, models.NewTicketCreate(name, int64(index+1)))
		if err != nil {
			return err
		}
		fixture.seedTickets = append(fixture.seedTickets, value)
		if _, err := models.TicketLabelObjects.Create(ctx, fixture.backend, models.NewTicketLabelCreate(value.ID, choiceLargeID+int64(index))); err != nil {
			return err
		}
	}
	return nil
}

func (fixture *choiceFixture) labelChoices(source, field string) (formmodel.ModelChoices[models.Label], error) {
	return formmodel.NewRelationChoices(fixture.binding, ir.ModelIdentity{AppLabel: "helpdesk", ModelName: source}, field, models.LabelObjects, formmodel.ModelChoiceConfig[models.Label]{
		Label: func(value models.Label) (string, error) { return value.Name, nil },
		Search: func(term string) ([]orm.Predicate[models.Label], error) {
			if term == "" {
				return nil, nil
			}
			return []orm.Predicate[models.Label]{models.LabelFields.Name.IContains(term)}, nil
		}, Order: []orm.Ordering[models.Label]{models.LabelFields.ID.Asc()},
	})
}
func (fixture *choiceFixture) predicates(category int64) []orm.Predicate[models.Label] {
	return []orm.Predicate[models.Label]{fixture.relations.ModelsLabel.Category.ID.Exact(category), models.LabelFields.Name.IContains("Choice")}
}
func (fixture *choiceFixture) source(source, field string) (admin.RelatedChoices, error) {
	choices, err := fixture.labelChoices(source, field)
	if err != nil {
		return nil, err
	}
	return admin.NewRelatedChoices(admin.RelatedChoiceConfig[models.Label]{Choices: choices, Permission: permissions("label").View,
		Scope: func(ctx context.Context, _ auth.Principal, origin admin.ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[models.Label]) error) error {
			category := int64(1)
			if _, parentID, inline := origin.Parent(); inline {
				category = parentID
			} else if id, existing := origin.ObjectID(); existing {
				if source == "ticket_label" {
					link, found, err := models.TicketLabelObjects.Using(fixture.backend).Filter(models.TicketLabelFields.ID.Exact(id)).OrderBy(models.TicketLabelFields.ID.Asc()).First(ctx)
					if err != nil {
						return err
					}
					if !found {
						return admin.ErrObjectNotFound
					}
					id = link.TicketID
				}
				ticket, found, err := models.TicketObjects.Using(fixture.backend).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
				if err != nil {
					return err
				}
				if !found {
					return admin.ErrObjectNotFound
				}
				category = ticket.CategoryID
			}
			return visit(fixture.backend, fixture.predicates(category))
		},
	})
}
func (fixture *choiceFixture) keys(ctx context.Context, reader db.Queryer, ticket int64) ([]int64, error) {
	rows, err := models.TicketLabelObjects.Using(reader).Filter(fixture.relations.ModelsTicketLabel.Ticket.ID.Exact(ticket)).OrderBy(models.TicketLabelFields.LabelID.Asc()).Limit(101)
	if err != nil {
		return nil, err
	}
	links, err := rows.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(links) > 100 {
		return nil, errors.New("choice fixture membership overflow")
	}
	keys := make([]int64, len(links))
	for index, link := range links {
		keys[index] = link.LabelID
	}
	return keys, nil
}
func (fixture *choiceFixture) ticket(ctx context.Context, reader db.Queryer, id int64) (choiceTicket, bool, error) {
	value, found, err := models.TicketObjects.Using(reader).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		return choiceTicket{}, false, err
	}
	keys, err := fixture.keys(ctx, reader, id)
	return choiceTicket{Ticket: value, labels: keys}, err == nil, err
}
func (fixture *choiceFixture) resolve(ctx context.Context, reader db.Queryer, category int64, keys []int64) ([]forms.Choice, error) {
	selected, err := fixture.labels.Filter(fixture.predicates(category)...).Resolve(ctx, reader, keys)
	if err != nil {
		return nil, err
	}
	return selected.Items(), nil
}
func choiceRelated(value choiceTicket, field ir.ManyToManyField) ([]int64, bool) {
	return slices.Clone(value.labels), field.Name == "labels" && value.labels != nil
}
func choiceRejected(field string) error {
	return validation.Reject(validation.NewErrors(validation.New(validation.Field(field), "invalid_choice")), nil)
}

func (fixture *choiceFixture) register(builder *admin.Builder) error {
	if err := fixture.registerTickets(builder); err != nil {
		return err
	}
	if err := fixture.registerLinks(builder); err != nil {
		return err
	}
	if err := fixture.registerLabels(builder); err != nil {
		return err
	}
	return fixture.registerCategories(builder)
}
func (fixture *choiceFixture) registerTickets(builder *admin.Builder) error {
	metadata := (models.TicketDescriptor{}).Metadata()
	fields := []string{"subject", "labels"}
	spec, err := formmodel.NewSpecForFields(metadata, fields)
	if err != nil {
		return err
	}
	source, err := fixture.source("ticket", "labels")
	if err != nil {
		return err
	}
	saver, err := formmodel.SaveManyToMany(fixture.collections.ModelsTicketLabels)
	if err != nil {
		return err
	}
	return admin.RegisterModel(builder, admin.ModelConfig[choiceTicket]{AppLabel: "helpdesk", Slug: "tickets", Model: metadata, FormFields: fields, RelatedChoices: []admin.RelatedChoices{source}, ListFields: []string{"id", "subject"}, Permissions: permissions("ticket"),
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[choiceTicket], error) {
			page, err := choicePage(ctx, models.TicketObjects.Using(fixture.backend).OrderBy(models.TicketFields.ID.Asc()), request)
			if err != nil {
				return admin.Page[choiceTicket]{}, err
			}
			result := admin.Page[choiceTicket]{Total: page.Total, Offset: page.Offset, Limit: page.Limit}
			for _, row := range page.Items {
				keys, err := fixture.keys(ctx, fixture.backend, row.ID)
				if err != nil {
					return result, err
				}
				result.Items = append(result.Items, choiceTicket{Ticket: row, labels: keys})
			}
			return result, nil
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (choiceTicket, bool, error) {
			return fixture.ticket(ctx, fixture.backend, id)
		},
		Snapshot: func(value choiceTicket) (admin.Object, error) {
			keys := make([]templates.Value, len(value.labels))
			for i, key := range value.labels {
				keys[i] = templates.Integer(key)
			}
			return admin.NewObject(value.ID, value.Subject, map[string]templates.Value{"id": templates.Integer(value.ID), "subject": templates.String(value.Subject), "labels": templates.List(keys...)})
		},
		Initial: func(value choiceTicket) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, spec, value, func(value choiceTicket, field ir.Field) (queryValue query.Value, ok bool) {
				return (models.TicketDescriptor{}).WriteFieldValue(value.Ticket, field)
			}, choiceRelated)
		},
		Create: func(context.Context, auth.Principal, formmodel.BoundForm, admin.InlineSubmission) (choiceTicket, error) {
			return choiceTicket{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
		Update: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (choiceTicket, []string, error) {
			var saved choiceTicket
			err := fixture.backend.AtomicRelation(ctx, func(session db.RelationSession) error {
				if !actor.Has(permissions("ticket").Change) || !actor.Has(permissions("label").View) {
					return admin.NewOperationError(admin.OperationDenied, nil)
				}
				current, found, err := fixture.ticket(ctx, session, mutation.ID)
				if err != nil {
					return err
				}
				if !found {
					return admin.ErrObjectNotFound
				}
				input, err := bound.Input()
				if err != nil {
					return err
				}
				keys, ok := input.Integers("labels")
				if !ok {
					return errors.New("missing choice membership")
				}
				selected, err := fixture.resolve(ctx, session, current.CategoryID, keys)
				if err != nil {
					return err
				}
				if len(selected) != len(keys) {
					return choiceRejected("labels")
				}
				prepared, err := formmodel.PrepareInstance(models.TicketObjects, bound, &current.Ticket)
				if err != nil {
					return err
				}
				value, err := prepared.Model()
				if err != nil {
					return err
				}
				if err = prepared.Save(ctx, session, &value, saver); err != nil {
					return err
				}
				saved = choiceTicket{Ticket: value, labels: slices.Clone(keys)}
				return nil
			})
			return saved, []string{"subject", "labels"}, err
		},
		Delete: func(context.Context, auth.Principal, admin.Mutation) (choiceTicket, error) {
			return choiceTicket{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
	})
}

func (fixture *choiceFixture) registerLinks(builder *admin.Builder) error {
	metadata := (models.TicketLabelDescriptor{}).Metadata()
	fields := []string{"label"}
	spec, err := formmodel.NewSpecForFields(metadata, fields)
	if err != nil {
		return err
	}
	source, err := fixture.source("ticket_label", "label")
	if err != nil {
		return err
	}
	choices, err := fixture.labelChoices("ticket_label", "label")
	if err != nil {
		return err
	}
	projector, err := admin.NewModelProjector(metadata, models.TicketLabelDescriptor{}.WriteFieldValue, "id", "label")
	if err != nil {
		return err
	}
	get := func(ctx context.Context, reader db.Queryer, id int64) (models.TicketLabel, bool, error) {
		return models.TicketLabelObjects.Using(reader).Filter(models.TicketLabelFields.ID.Exact(id)).OrderBy(models.TicketLabelFields.ID.Asc()).First(ctx)
	}
	return admin.RegisterModel(builder, admin.ModelConfig[models.TicketLabel]{AppLabel: "helpdesk", Slug: "ticket-labels", Model: metadata, FormFields: fields, RelatedChoices: []admin.RelatedChoices{source}, ListFields: []string{"id", "label"}, Permissions: permissions("ticket_label"),
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[models.TicketLabel], error) {
			return choicePage(ctx, models.TicketLabelObjects.Using(fixture.backend).OrderBy(models.TicketLabelFields.ID.Asc()), request)
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (models.TicketLabel, bool, error) {
			return get(ctx, fixture.backend, id)
		},
		Snapshot: func(value models.TicketLabel) (admin.Object, error) {
			return projector.Project(value, value.ID, "Choice link")
		},
		Initial: func(value models.TicketLabel) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, spec, value, models.TicketLabelDescriptor{}.WriteFieldValue)
		},
		Create: func(context.Context, auth.Principal, formmodel.BoundForm, admin.InlineSubmission) (models.TicketLabel, error) {
			return models.TicketLabel{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
		Update: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (models.TicketLabel, []string, error) {
			var saved models.TicketLabel
			err := fixture.backend.AtomicRelation(ctx, func(session db.RelationSession) error {
				if !actor.Has(permissions("ticket_label").Change) || !actor.Has(permissions("label").View) {
					return admin.NewOperationError(admin.OperationDenied, nil)
				}
				current, found, err := get(ctx, session, mutation.ID)
				if err != nil {
					return err
				}
				if !found {
					return admin.ErrObjectNotFound
				}
				owner, found, err := fixture.ticket(ctx, session, current.TicketID)
				if err != nil {
					return err
				}
				if !found {
					return admin.ErrObjectNotFound
				}
				input, err := bound.Input()
				if err != nil {
					return err
				}
				key, ok := input.Integer("label")
				if !ok {
					return errors.New("missing choice label")
				}
				selected, err := choices.Filter(fixture.predicates(owner.CategoryID)...).Resolve(ctx, session, []int64{key})
				if err != nil {
					return err
				}
				if len(selected.Items()) != 1 {
					return choiceRejected("label")
				}
				prepared, err := formmodel.PrepareInstance(models.TicketLabelObjects, bound, &current)
				if err != nil {
					return err
				}
				saved, err = prepared.Model()
				if err != nil {
					return err
				}
				return prepared.Save(ctx, session, &saved)
			})
			return saved, []string{"label"}, err
		},
		Delete: func(context.Context, auth.Principal, admin.Mutation) (models.TicketLabel, error) {
			return models.TicketLabel{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
	})
}

func (fixture *choiceFixture) registerLabels(builder *admin.Builder) error {
	metadata := (models.LabelDescriptor{}).Metadata()
	spec, err := formmodel.NewSpecForFields(metadata, []string{"name"})
	if err != nil {
		return err
	}
	projector, err := admin.NewModelProjector(metadata, models.LabelDescriptor{}.WriteFieldValue, "id", "name")
	if err != nil {
		return err
	}
	get := func(ctx context.Context, reader db.Queryer, id int64) (models.Label, bool, error) {
		return models.LabelObjects.Using(reader).Filter(models.LabelFields.ID.Exact(id)).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
	}
	return admin.RegisterModel(builder, admin.ModelConfig[models.Label]{AppLabel: "helpdesk", Slug: "labels", Model: metadata, FormFields: []string{"name"}, ListFields: []string{"id", "name"}, Permissions: permissions("label"),
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[models.Label], error) {
			return choicePage(ctx, models.LabelObjects.Using(fixture.backend).Filter(models.LabelFields.ID.GreaterThan(0)).OrderBy(models.LabelFields.ID.Asc()), request)
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (models.Label, bool, error) {
			return get(ctx, fixture.backend, id)
		},
		Snapshot: func(value models.Label) (admin.Object, error) { return projector.Project(value, value.ID, value.Name) },
		Initial: func(value models.Label) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, spec, value, models.LabelDescriptor{}.WriteFieldValue)
		},
		Create: func(context.Context, auth.Principal, formmodel.BoundForm, admin.InlineSubmission) (models.Label, error) {
			return models.Label{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
		Update: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (models.Label, []string, error) {
			var saved models.Label
			err := fixture.backend.AtomicRelation(ctx, func(session db.RelationSession) error {
				if !actor.Has(permissions("label").Change) {
					return admin.NewOperationError(admin.OperationDenied, nil)
				}
				current, found, err := get(ctx, session, mutation.ID)
				if err != nil {
					return err
				}
				if !found {
					return admin.ErrObjectNotFound
				}
				prepared, err := formmodel.PrepareInstance(models.LabelObjects, bound, &current)
				if err != nil {
					return err
				}
				saved, err = prepared.Model()
				if err != nil {
					return err
				}
				return prepared.Save(ctx, session, &saved)
			})
			return saved, []string{"name"}, err
		},
		Delete: func(context.Context, auth.Principal, admin.Mutation) (models.Label, error) {
			return models.Label{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
	})
}

func (fixture *choiceFixture) children(ctx context.Context, reader db.Queryer, parent int64) (admin.InlineSnapshot[models.Ticket], error) {
	rows, err := models.TicketObjects.Using(reader).Filter(fixture.relations.ModelsTicket.Category.ID.Exact(parent)).OrderBy(models.TicketFields.ID.Asc()).Limit(5)
	if err != nil {
		return admin.InlineSnapshot[models.Ticket]{}, err
	}
	current, err := rows.All(ctx)
	if err != nil {
		return admin.InlineSnapshot[models.Ticket]{}, err
	}
	if len(current) > 4 {
		return admin.InlineSnapshot[models.Ticket]{}, errors.New("choice fixture inline overflow")
	}
	members := make(map[int64][]int64, len(current))
	for _, row := range current {
		keys, err := fixture.keys(ctx, reader, row.ID)
		if err != nil {
			return admin.InlineSnapshot[models.Ticket]{}, err
		}
		members[row.ID] = keys
	}
	return admin.InlineSnapshot[models.Ticket]{Current: current, Related: func(value models.Ticket, field ir.ManyToManyField) ([]int64, bool) {
		keys, ok := members[value.ID]
		return slices.Clone(keys), ok && field.Name == "labels"
	}}, nil
}

func (fixture *choiceFixture) registerCategories(builder *admin.Builder) error {
	source, err := fixture.source("ticket", "labels")
	if err != nil {
		return err
	}
	policy := forms.DefaultSetConfig()
	policy.Prefix = "tickets"
	policy.ExtraForms = 0
	policy.MaxForms = 3
	policy.AbsoluteMax = 4
	policy.ValidateMax = true
	policy.CanDelete = true
	inline, err := admin.NewInline(admin.InlineConfig[models.Category, models.Ticket]{Project: fixture.binding, Parent: models.CategoryObjects, Child: models.TicketObjects, ParentWithID: models.NewCategoryWithID, ForeignKey: "category", Label: "Tickets", Form: admin.FormConfig{Definition: formmodel.Definition{Fields: []string{"subject", "labels"}}, RelatedChoices: []admin.RelatedChoices{source}}, Set: policy, Permissions: permissions("tickets"), Load: func(ctx context.Context, _ auth.Principal, id int64) (admin.InlineSnapshot[models.Ticket], error) {
		return fixture.children(ctx, fixture.backend, id)
	}})
	if err != nil {
		return err
	}
	metadata := (models.CategoryDescriptor{}).Metadata()
	spec, err := formmodel.NewSpecForFields(metadata, []string{"name"})
	if err != nil {
		return err
	}
	projector, err := admin.NewModelProjector(metadata, models.CategoryDescriptor{}.WriteFieldValue, "id", "name")
	if err != nil {
		return err
	}
	saver, err := formmodel.SaveManyToMany(fixture.collections.ModelsTicketLabels)
	if err != nil {
		return err
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		return err
	}
	get := func(ctx context.Context, reader db.Queryer, id int64) (models.Category, bool, error) {
		return models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(id)).OrderBy(models.CategoryFields.ID.Asc()).First(ctx)
	}
	return admin.RegisterModel(builder, admin.ModelConfig[models.Category]{AppLabel: "helpdesk", Slug: "categories", Model: metadata, FormFields: []string{"name"}, ListFields: []string{"id", "name"}, Permissions: permissions("category"), Inlines: []admin.Inline{inline.View()},
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[models.Category], error) {
			return choicePage(ctx, models.CategoryObjects.Using(fixture.backend).OrderBy(models.CategoryFields.ID.Asc()), request)
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (models.Category, bool, error) {
			return get(ctx, fixture.backend, id)
		},
		Snapshot: func(value models.Category) (admin.Object, error) {
			return projector.Project(value, value.ID, value.Name)
		},
		Initial: func(value models.Category) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, spec, value, models.CategoryDescriptor{}.WriteFieldValue)
		},
		Create: func(context.Context, auth.Principal, formmodel.BoundForm, admin.InlineSubmission) (models.Category, error) {
			return models.Category{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
		Update: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, input admin.InlineSubmission) (models.Category, []string, error) {
			var saved models.Category
			err := fixture.backend.AtomicRelation(ctx, func(session db.RelationSession) error {
				if !actor.Has(permissions("category").Change) {
					return admin.NewOperationError(admin.OperationDenied, nil)
				}
				current, found, err := get(ctx, session, mutation.ID)
				if err != nil {
					return err
				}
				if !found {
					return admin.ErrObjectNotFound
				}
				boundInline, present, err := inline.BindCurrent(ctx, actor, input, func(work context.Context, access admin.InlineAccess) (admin.InlineCurrent[models.Category, models.Ticket], error) {
					children, err := fixture.children(work, session, current.ID)
					if err != nil {
						return admin.InlineCurrent[models.Category, models.Ticket]{}, err
					}
					return admin.InlineCurrent[models.Category, models.Ticket]{Parent: current, Children: children, ResolveChoices: func(work context.Context, preflight formmodel.InlinePreflight[models.Category, models.Ticket]) (forms.RowModelChoices, error) {
						rows := make(map[int]map[string][]forms.Choice, preflight.TotalForms())
						for index := 0; index < preflight.TotalForms(); index++ {
							keys, valid, err := preflight.ChoiceKeys(index, "labels")
							if err != nil {
								return forms.RowModelChoices{}, err
							}
							var items []forms.Choice
							readonly, err := preflight.ReadOnly(index)
							if err != nil {
								return forms.RowModelChoices{}, err
							}
							if readonly {
								for _, key := range keys {
									items = append(items, forms.Choice{Value: forms.Integer(key), Label: strconv.FormatInt(key, 10)})
								}
							} else if valid {
								if !actor.Has(permissions("label").View) {
									return forms.RowModelChoices{}, admin.NewOperationError(admin.OperationDenied, nil)
								}
								items, err = fixture.resolve(work, session, current.ID, keys)
								if err != nil {
									return forms.RowModelChoices{}, err
								}
							}
							rows[index] = map[string][]forms.Choice{"labels": items}
						}
						return forms.NewRowModelChoices(rows, nil), nil
					}}, nil
				})
				if err != nil {
					return err
				}
				if !present {
					return errors.New("missing choice inline")
				}
				if !boundInline.Set().Valid() {
					return admin.RejectInline("tickets", -1, validation.NewErrors(validation.New(validation.NonField, "stale_inline")), nil)
				}
				parent, err := formmodel.PrepareInstance(models.CategoryObjects, bound, &current)
				if err != nil {
					return err
				}
				saved, err = parent.Model()
				if err != nil {
					return err
				}
				if err = parent.Save(ctx, session, &saved); err != nil {
					return err
				}
				prepared, err := boundInline.Set().PrepareWithParent(saved)
				if err != nil {
					return err
				}
				plan, err := prepared.SavePlan()
				if err != nil {
					return err
				}
				access := boundInline.Access()
				_, err = plan.Save(ctx, session, formmodel.SetSaveOptions[models.Ticket]{Save: func(work context.Context, _ db.Session, row *formmodel.SetSaveRow[models.Ticket]) error {
					if row.Existing() && !access.Change || !row.Existing() && !access.Add {
						return admin.NewOperationError(admin.OperationDenied, nil)
					}
					return row.Save(work, session, saver)
				}, Delete: func(work context.Context, _ db.Session, row formmodel.DeletedSetRow[models.Ticket]) error {
					if !access.Delete {
						return admin.NewOperationError(admin.OperationDenied, nil)
					}
					value, err := row.Model()
					if err != nil {
						return err
					}
					_, err = deleters.ModelsTicket.DeleteInSession(work, session, value)
					return err
				}})
				return err
			})
			return saved, []string{"name", "tickets"}, err
		},
		Delete: func(context.Context, auth.Principal, admin.Mutation) (models.Category, error) {
			return models.Category{}, admin.NewOperationError(admin.OperationDenied, nil)
		},
	})
}

func choicePage[M any](ctx context.Context, rows orm.QuerySet[M], request admin.ListRequest) (admin.Page[M], error) {
	total, err := rows.Count(ctx)
	if err != nil {
		return admin.Page[M]{}, err
	}
	rows, err = rows.Offset(request.Offset)
	if err != nil {
		return admin.Page[M]{}, err
	}
	rows, err = rows.Limit(request.Limit)
	if err != nil {
		return admin.Page[M]{}, err
	}
	items, err := rows.All(ctx)
	return admin.Page[M]{Items: items, Total: total, Offset: request.Offset, Limit: request.Limit}, err
}

func (fixture *choiceFixture) record(ctx context.Context) error {
	categories, err := models.CategoryObjects.Using(fixture.backend).OrderBy(models.CategoryFields.ID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	labels, err := models.LabelObjects.Using(fixture.backend).OrderBy(models.LabelFields.ID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	tickets, err := models.TicketObjects.Using(fixture.backend).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	links, err := models.TicketLabelObjects.Using(fixture.backend).OrderBy(models.TicketLabelFields.TicketID.Asc(), models.TicketLabelFields.LabelID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	if len(categories) != 2 || len(labels) != len(fixture.seedLabels) || len(tickets) != 3 {
		return errors.New("choice fixture final cohort mismatch")
	}
	for index, before := range fixture.seedCategories {
		if same, err := unchangedStored(models.CategoryObjects, before, categories[index]); err != nil {
			return err
		} else if !same {
			return errors.New("choice category changed")
		}
	}
	for index, before := range fixture.seedLabels {
		if before.ID == 31 {
			before.Name = "Revoked target"
		}
		if same, err := unchangedStored(models.LabelObjects, before, labels[index]); err != nil {
			return err
		} else if !same {
			return errors.New("choice target scalar transition mismatch")
		}
	}
	for index, before := range fixture.seedTickets {
		if same, err := unchangedStored(models.TicketObjects, before, tickets[index]); err != nil {
			return err
		} else if !same {
			return errors.New("choice owner scalar changed")
		}
	}
	state := struct {
		SeedScalarsPreserved bool             `json:"seed_scalars_preserved"`
		Categories           []map[string]any `json:"categories"`
		Labels               []map[string]any `json:"labels"`
		Tickets              []map[string]any `json:"tickets"`
		Links                []map[string]any `json:"links"`
		Reports              int64            `json:"reports"`
	}{SeedScalarsPreserved: true}
	for _, value := range categories {
		state.Categories = append(state.Categories, map[string]any{"id": strconv.FormatInt(value.ID, 10), "name": value.Name})
	}
	for _, value := range labels {
		state.Labels = append(state.Labels, map[string]any{"id": strconv.FormatInt(value.ID, 10), "category": strconv.FormatInt(value.CategoryID, 10), "name": value.Name})
	}
	for _, value := range tickets {
		state.Tickets = append(state.Tickets, map[string]any{"id": strconv.FormatInt(value.ID, 10), "category": strconv.FormatInt(value.CategoryID, 10), "subject": value.Subject, "closed": value.Closed})
	}
	for _, value := range links {
		state.Links = append(state.Links, map[string]any{"ticket": strconv.FormatInt(value.TicketID, 10), "label": strconv.FormatInt(value.LabelID, 10)})
	}
	state.Reports, err = models.ServiceReportObjects.Using(fixture.backend).Count(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(state)
}
