// Package helpdesk demonstrates a second model shape: read-only Categories and
// editable Tickets whose relation is assigned by trusted application policy.
// It owns no listener, credentials, migrations, or database connection lifetime.
package helpdesk

import (
	"context"
	"errors"
	"net/http"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const (
	ViewCategory auth.Permission = "helpdesk.view_category"
	ViewTicket   auth.Permission = "helpdesk.view_ticket"
	AddTicket    auth.Permission = "helpdesk.add_ticket"
	ChangeTicket auth.Permission = "helpdesk.change_ticket"
	DeleteTicket auth.Permission = "helpdesk.delete_ticket"
)

type Backend interface {
	db.Queryer
	db.Mutator
	db.Atomic
}

type Application struct {
	backend    Backend
	categoryID int64
	registry   admin.Registry
	input      serializers.Spec
	encoder    serializers.ModelEncoder[models.Ticket]
	parser     api.Parser
	relations  project.Relations
	objects    project.Models
}

// New binds the selected category but performs no I/O. The caller chooses the
// category, while clients may edit only subject/details/closed. Every create
// checks category existence in its transaction; it is never taken from input.
func New(backend Backend, categoryID int64) (*Application, error) {
	if categoryID <= 0 {
		return nil, errors.New("helpdesk: category id must be positive")
	}
	objects, err := project.Using(backend)
	if err != nil {
		return nil, err
	}
	installed, err := apps.New(InstalledApps())
	if err != nil {
		return nil, err
	}
	builder := admin.NewBuilder(installed)
	a := &Application{backend: backend, categoryID: categoryID, objects: objects}
	a.relations, err = project.BindRelations()
	if err != nil {
		return nil, err
	}
	if err := a.register(builder); err != nil {
		return nil, err
	}
	a.registry, err = builder.Build()
	if err != nil {
		return nil, err
	}
	metadata := (models.TicketDescriptor{}).Metadata()
	a.input, err = serializers.FromModel(metadata,
		serializers.ModelField{Name: "subject"}, serializers.ModelField{Name: "details", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "closed"})
	if err != nil {
		return nil, err
	}
	output, err := serializers.FromModel(metadata,
		serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "subject"}, serializers.ModelField{Name: "details", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "closed"}, serializers.ModelField{Name: "category", ReadOnly: true})
	if err != nil {
		return nil, err
	}
	a.encoder, err = serializers.NewModelEncoder(output, metadata, (models.TicketDescriptor{}).WriteFieldValue)
	if err != nil {
		return nil, err
	}
	a.parser, err = api.NewParser(api.ParserConfig{MaxBodyBytes: 4096})
	if err != nil {
		return nil, err
	}
	return a, nil
}

func InstalledApps() []apps.Config {
	return []apps.Config{{Name: "github.com/progresshans/godj/examples/helpdesk/models", Label: "helpdesk"}}
}
func Permissions() []auth.Permission {
	return []auth.Permission{ViewCategory, ViewTicket, AddTicket, ChangeTicket, DeleteTicket}
}
func (a *Application) Registry() admin.Registry { return a.registry }

func (a *Application) register(builder *admin.Builder) error {
	categoryDescriptor := models.CategoryDescriptor{}
	categoryProjector, err := admin.NewModelProjector(categoryDescriptor.Metadata(), categoryDescriptor.WriteFieldValue, "id", "name")
	if err != nil {
		return err
	}
	if err := admin.RegisterModel(builder, admin.ModelConfig[models.Category]{
		AppLabel: "helpdesk", Slug: "categories", Model: categoryDescriptor.Metadata(), ReadOnly: true,
		ListFields: []string{"id", "name"}, SearchFields: []string{"name"}, Permissions: admin.Permissions{View: ViewCategory},
		List: func(ctx context.Context, request admin.ListRequest) (admin.Page[models.Category], error) {
			query := models.CategoryObjects.Using(a.backend).OrderBy(models.CategoryFields.ID.Asc())
			if request.Search != "" {
				query = query.Filter(models.CategoryFields.Name.IContains(request.Search))
			}
			total, err := query.Count(ctx)
			if err != nil {
				return admin.Page[models.Category]{}, err
			}
			query, err = query.Offset(request.Offset)
			if err != nil {
				return admin.Page[models.Category]{}, err
			}
			query, err = query.Limit(request.Limit)
			if err != nil {
				return admin.Page[models.Category]{}, err
			}
			items, err := query.All(ctx)
			return admin.Page[models.Category]{Items: items, Total: total, Offset: request.Offset, Limit: request.Limit}, err
		},
		Snapshot: func(value models.Category) (admin.Object, error) {
			return categoryProjector.Project(value, value.ID, value.Name)
		},
	}); err != nil {
		return err
	}
	descriptor := models.TicketDescriptor{}
	metadata := descriptor.Metadata()
	fields := []string{"subject", "details", "closed"}
	form, err := formmodel.NewSpecForFields(metadata, fields)
	if err != nil {
		return err
	}
	ticketProjector, err := admin.NewModelProjector(metadata, descriptor.WriteFieldValue, "id", "subject", "details", "closed", "category")
	if err != nil {
		return err
	}
	return admin.RegisterModel(builder, admin.ModelConfig[models.Ticket]{
		AppLabel: "helpdesk", Slug: "tickets", Model: metadata, FormFields: fields,
		ListFields: []string{"id", "subject", "category", "closed"}, SearchFields: []string{"subject"},
		Permissions: admin.Permissions{View: ViewTicket, Add: AddTicket, Change: ChangeTicket, Delete: DeleteTicket},
		List:        a.list,
		Get: func(ctx context.Context, id int64) (models.Ticket, bool, error) {
			value, found, err := ticket(ctx, a.backend, id)
			if !found || err != nil || value.CategoryID != a.categoryID {
				return models.Ticket{}, false, err
			}
			return value, true, nil
		},
		Snapshot: func(value models.Ticket) (admin.Object, error) {
			return ticketProjector.Project(value, value.ID, value.Subject)
		},
		Initial: func(value models.Ticket) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, form, value, descriptor.WriteFieldValue)
		},
		Create: func(ctx context.Context, _ auth.Principal, values forms.Values) (models.Ticket, error) {
			input, err := fromForm(values)
			if err != nil {
				return models.Ticket{}, err
			}
			return a.create(ctx, input)
		},
		Update: func(ctx context.Context, _ auth.Principal, id int64, values forms.Values) (models.Ticket, []string, error) {
			input, err := fromForm(values)
			if err != nil {
				return models.Ticket{}, nil, err
			}
			return a.update(ctx, id, input)
		},
		Delete: func(ctx context.Context, _ auth.Principal, id int64) (models.Ticket, error) { return a.delete(ctx, id) },
	})
}

func (a *Application) list(ctx context.Context, request admin.ListRequest) (admin.Page[models.Ticket], error) {
	query := models.TicketObjects.Using(a.backend).Filter(a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).OrderBy(models.TicketFields.ID.Asc())
	if request.Search != "" {
		query = query.Filter(models.TicketFields.Subject.IContains(request.Search))
	}
	total, err := query.Count(ctx)
	if err != nil {
		return admin.Page[models.Ticket]{}, err
	}
	query, err = query.Offset(request.Offset)
	if err != nil {
		return admin.Page[models.Ticket]{}, err
	}
	query, err = query.Limit(request.Limit)
	if err != nil {
		return admin.Page[models.Ticket]{}, err
	}
	items, err := query.All(ctx)
	return admin.Page[models.Ticket]{Items: items, Total: total, Offset: request.Offset, Limit: request.Limit}, err
}

type ticketInput struct {
	subject string
	details *string
	closed  bool
}

func fromForm(values forms.Values) (ticketInput, error) {
	subject, subjectOK := values.String("subject")
	closed, closedOK := values.Boolean("closed")
	details, detailsOK := values.Get("details")
	if !subjectOK || !closedOK || !detailsOK || len(values.All()) != 3 {
		return ticketInput{}, errors.New("helpdesk: incomplete ticket form")
	}
	input := ticketInput{subject: subject, closed: closed}
	if !details.IsNull() {
		text, ok := details.AsString()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid details")
		}
		input.details = &text
	}
	return input, nil
}

func ticket(ctx context.Context, backend db.Queryer, id int64) (models.Ticket, bool, error) {
	return models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
}
func (a *Application) create(ctx context.Context, input ticketInput) (models.Ticket, error) {
	var created models.Ticket
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		if _, found, err := models.CategoryObjects.Using(session).Filter(models.CategoryFields.ID.Exact(a.categoryID)).OrderBy(models.CategoryFields.ID.Asc()).First(ctx); err != nil {
			return err
		} else if !found {
			return admin.ErrObjectNotFound
		}
		create := models.NewTicketCreate(input.subject, a.categoryID).WithClosed(input.closed)
		if input.details == nil {
			create = create.WithDetailsNull()
		} else {
			create = create.WithDetails(*input.details)
		}
		var err error
		created, err = models.TicketObjects.Create(ctx, session, create)
		return err
	})
	return created, err
}
func (a *Application) update(ctx context.Context, id int64, input ticketInput) (models.Ticket, []string, error) {
	var updated models.Ticket
	var changed []string
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		current, found, err := ticket(ctx, session, id)
		if err != nil {
			return err
		}
		if !found || current.CategoryID != a.categoryID {
			return admin.ErrObjectNotFound
		}
		patch := models.TicketPatch{}.WithSubject(input.subject).WithClosed(input.closed)
		if input.details == nil {
			patch = patch.WithDetailsNull()
		} else {
			patch = patch.WithDetails(*input.details)
		}
		next := current
		next.Subject, next.Details, next.Closed = input.subject, input.details, input.closed
		descriptor := models.TicketDescriptor{}
		for _, field := range descriptor.Metadata().Fields {
			before, _ := descriptor.WriteFieldValue(current, field)
			after, _ := descriptor.WriteFieldValue(next, field)
			if !before.Equal(after) {
				changed = append(changed, field.Name)
			}
		}
		if len(changed) == 0 {
			updated = current
			return nil
		}
		updated, err = models.TicketObjects.Update(ctx, session, current, patch)
		return err
	})
	return updated, changed, err
}
func (a *Application) delete(ctx context.Context, id int64) (models.Ticket, error) {
	var removed models.Ticket
	err := a.backend.Atomic(ctx, func(session db.Session) error {
		current, found, err := ticket(ctx, session, id)
		if err != nil {
			return err
		}
		if !found || current.CategoryID != a.categoryID {
			return admin.ErrObjectNotFound
		}
		removed = current
		_, err = models.TicketObjects.Delete(ctx, session, &current)
		return err
	})
	return removed, err
}

// APIRoutes exposes a deliberately small authenticated collection API. The
// same typed create path backs Admin and JSON; category is never writable.
func (a *Application) APIRoutes(authentication api.Authentication) ([]web.Route, error) {
	if a == nil || authentication == nil {
		return nil, errors.New("helpdesk: missing application or authentication")
	}
	detail, err := authentication.Require(ViewTicket, a.detail)
	if err != nil {
		return nil, err
	}
	list, err := authentication.Require(ViewTicket, func(request *web.Request, _ auth.Principal) (web.Response, error) {
		page, err := a.list(request.Context(), admin.ListRequest{Limit: 20})
		if err != nil {
			return web.Response{}, err
		}
		values := make([]serializers.Value, 0, len(page.Items))
		for _, value := range page.Items {
			item, err := a.encoder.Encode(value)
			if err != nil {
				return web.Response{}, err
			}
			values = append(values, item)
		}
		value, err := serializers.NewList(values...)
		if err != nil {
			return web.Response{}, err
		}
		return api.JSON(http.StatusOK, value)
	})
	if err != nil {
		return nil, err
	}
	create, err := authentication.Require(AddTicket, func(request *web.Request, _ auth.Principal) (web.Response, error) {
		object, err := a.parser.ParseObject(request)
		if err != nil {
			response, handled, responseErr := api.RequestErrorResponse(err)
			if handled {
				return response, responseErr
			}
			return web.Response{}, err
		}
		bound, err := a.input.Bind(object, serializers.ModeFull)
		if err != nil {
			return web.Response{}, err
		}
		if !bound.Valid() {
			return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, bound.Errors())
		}
		values := bound.Values()
		subject, _ := values.Get("subject")
		text, _ := subject.AsString()
		closed, _ := values.Get("closed")
		boolean, _ := closed.AsBoolean()
		input := ticketInput{subject: text, closed: boolean}
		if details, present := values.Get("details"); present && !details.IsNull() {
			text, _ := details.AsString()
			input.details = &text
		}
		created, err := a.create(request.Context(), input)
		if errors.Is(err, admin.ErrObjectNotFound) {
			return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
		}
		if err != nil {
			return web.Response{}, err
		}
		value, err := a.encoder.Encode(created)
		if err != nil {
			return web.Response{}, err
		}
		return api.JSON(http.StatusCreated, value)
	})
	if err != nil {
		return nil, err
	}
	return []web.Route{
		{Name: "helpdesk:ticket-list", Method: http.MethodGet, Path: "/api/tickets/", Handler: list},
		{Name: "helpdesk:ticket-create", Method: http.MethodPost, Path: "/api/tickets/", Handler: create},
		{Name: "helpdesk:ticket-detail", Method: http.MethodGet, Path: "/api/tickets/<int64:id>/", Handler: detail},
	}, nil
}

// Viewing a ticket includes its assigned category's identity and label. The
// standalone Category Admin remains protected by ViewCategory.
func (a *Application) detail(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, valid := request.Int64Parameter("id")
	if !valid || id <= 0 {
		return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
	}
	selected, found, err := a.objects.ModelsTicket.
		Filter(models.TicketFields.ID.Exact(id), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).
		OrderBy(models.TicketFields.ID.Asc()).
		SelectRelated(a.objects.ModelsTicket.Related.Category).First(request.Context())
	if err != nil {
		return web.Response{}, err
	}
	if !found {
		return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
	}
	category, err := selected.Category(request.Context())
	if err != nil {
		return web.Response{}, err
	}
	raw, err := selected.Unwrap()
	if err != nil {
		return web.Response{}, err
	}
	ticketValue, err := a.encoder.Encode(raw)
	if err != nil {
		return web.Response{}, err
	}
	categoryValue, err := serializers.NewObject(
		serializers.MemberOf("id", serializers.Integer(category.ID)),
		serializers.MemberOf("name", serializers.String(category.Name)),
	)
	if err != nil {
		return web.Response{}, err
	}
	value, err := serializers.NewObject(serializers.MemberOf("ticket", ticketValue), serializers.MemberOf("category", categoryValue.Value()))
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(http.StatusOK, value.Value())
}
