// Package helpdesk demonstrates a second model shape: read-only Categories and
// editable Tickets whose relation is assigned by trusted application policy.
// It owns no listener, credentials, migrations, or database connection lifetime.
package helpdesk

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/uuid"
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
	output     serializers.Spec
	encoder    serializers.ModelEncoder[models.Ticket]
	parser     api.Parser
	relations  project.Relations
	objects    project.Models
}

// New binds the selected category but performs no I/O. The caller chooses the
// category, while clients may edit the selected ticket fields and external
// reference/payload. Every create
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
		serializers.ModelField{Name: "subject"}, serializers.ModelField{Name: "details", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "closed"}, serializers.ModelField{Name: "priority", Optional: true},
		serializers.ModelField{Name: "resolution", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "due_at", Optional: true}, serializers.ModelField{Name: "reviewed", Optional: true}, serializers.ModelField{Name: "service_on", Optional: true}, serializers.ModelField{Name: "service_at", Optional: true}, serializers.ModelField{Name: "elapsed", Optional: true}, serializers.ModelField{Name: "effort", Optional: true}, serializers.ModelField{Name: "expected_cost", Optional: true}, serializers.ModelField{Name: "external_reference", Optional: true}, serializers.ModelField{Name: "external_payload", Optional: true})
	if err != nil {
		return nil, err
	}
	a.output, err = serializers.FromModel(metadata,
		serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "subject"}, serializers.ModelField{Name: "details", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "closed"}, serializers.ModelField{Name: "category", ReadOnly: true}, serializers.ModelField{Name: "priority", Optional: true},
		serializers.ModelField{Name: "resolution", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "due_at", Optional: true}, serializers.ModelField{Name: "reviewed", Optional: true}, serializers.ModelField{Name: "service_on", Optional: true}, serializers.ModelField{Name: "service_at", Optional: true}, serializers.ModelField{Name: "elapsed", Optional: true}, serializers.ModelField{Name: "effort", Optional: true}, serializers.ModelField{Name: "expected_cost", Optional: true}, serializers.ModelField{Name: "external_reference", Optional: true}, serializers.ModelField{Name: "external_payload", Optional: true})
	if err != nil {
		return nil, err
	}
	a.encoder, err = serializers.NewModelEncoder(a.output, metadata, (models.TicketDescriptor{}).WriteFieldValue)
	if err != nil {
		return nil, err
	}
	a.parser, err = api.NewParser(api.ParserConfig{MaxBodyBytes: maximumJSONBodyBytes})
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
	fields := []string{"subject", "details", "closed", "priority", "resolution", "due_at", "reviewed", "service_on", "service_at", "elapsed", "effort", "expected_cost", "external_reference", "external_payload"}
	overrides := []formmodel.Override{formmodel.OverrideField("external_payload", formmodel.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		if value.IsNull() {
			return validation.NewErrors()
		}
		document, _ := value.AsJSON()
		return externalPayloadErrors(document)
	})))}
	form, err := formmodel.NewSpecForFields(metadata, fields, overrides...)
	if err != nil {
		return err
	}
	ticketProjector, err := admin.NewModelProjector(metadata, descriptor.WriteFieldValue, "id", "subject", "details", "closed", "category", "priority", "resolution", "due_at", "reviewed", "service_on", "service_at", "elapsed", "effort", "expected_cost", "external_reference", "external_payload")
	if err != nil {
		return err
	}
	return admin.RegisterModel(builder, admin.ModelConfig[models.Ticket]{
		AppLabel: "helpdesk", Slug: "tickets", Model: metadata, FormFields: fields,
		FormOverrides: overrides,
		ListFields:    []string{"id", "subject", "category", "closed", "priority", "due_at", "reviewed", "service_on", "service_at", "elapsed", "effort", "expected_cost", "external_reference", "external_payload"}, SearchFields: []string{"subject"},
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
	subject           string
	details           *string
	closed            bool
	priority          *int64
	resolution        *string
	dueAt             *time.Time
	reviewed          *bool
	serviceOn         *calendar.Date
	serviceAt         *clock.Time
	elapsed           *duration.Duration
	effort            *float64
	expectedCost      *decimal.Decimal
	externalReference *uuid.UUID
	externalPayload   *jsonvalue.Value
}

func fromForm(values forms.Values) (ticketInput, error) {
	subject, subjectOK := values.String("subject")
	closed, closedOK := values.Boolean("closed")
	details, detailsOK := values.Get("details")
	priority, priorityOK := values.Get("priority")
	resolution, resolutionOK := values.Get("resolution")
	dueAt, dueAtOK := values.Get("due_at")
	reviewed, reviewedOK := values.Get("reviewed")
	serviceOn, serviceOnOK := values.Get("service_on")
	serviceAt, serviceAtOK := values.Get("service_at")
	elapsed, elapsedOK := values.Get("elapsed")
	effort, effortOK := values.Get("effort")
	expectedCost, expectedCostOK := values.Get("expected_cost")
	externalReference, externalReferenceOK := values.Get("external_reference")
	externalPayload, externalPayloadOK := values.Get("external_payload")
	if !subjectOK || !closedOK || !detailsOK || !priorityOK || !resolutionOK || !dueAtOK || !reviewedOK || !serviceOnOK || !serviceAtOK || !elapsedOK || !effortOK || !expectedCostOK || !externalReferenceOK || !externalPayloadOK || len(values.All()) != 14 {
		return ticketInput{}, errors.New("helpdesk: incomplete ticket form")
	}
	input := ticketInput{subject: subject, closed: closed}
	if !priority.IsNull() {
		integer, ok := priority.AsInteger()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid priority")
		}
		input.priority = &integer
	}
	if !details.IsNull() {
		text, ok := details.AsString()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid details")
		}
		input.details = &text
	}
	if !resolution.IsNull() {
		text, ok := resolution.AsString()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid resolution")
		}
		input.resolution = &text
	}
	if !dueAt.IsNull() {
		instant, ok := dueAt.AsDateTime()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid due_at")
		}
		input.dueAt = &instant
	}
	if !reviewed.IsNull() {
		boolean, ok := reviewed.AsBoolean()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid reviewed")
		}
		input.reviewed = &boolean
	}
	if !serviceOn.IsNull() {
		date, ok := serviceOn.AsDate()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid service_on")
		}
		input.serviceOn = &date
	}
	if !externalReference.IsNull() {
		identifier, ok := externalReference.AsUUID()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid external_reference")
		}
		input.externalReference = &identifier
	}
	if !externalPayload.IsNull() {
		document, ok := externalPayload.AsJSON()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid external_payload")
		}
		input.externalPayload = &document
	}
	if !expectedCost.IsNull() {
		number, ok := expectedCost.AsDecimal()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid expected_cost")
		}
		input.expectedCost = &number
	}
	if !effort.IsNull() {
		floatValue, ok := effort.AsFloat()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid effort")
		}
		input.effort = &floatValue
	}
	if !elapsed.IsNull() {
		durationValue, ok := elapsed.AsDuration()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid elapsed")
		}
		input.elapsed = &durationValue
	}
	if !serviceAt.IsNull() {
		clockValue, ok := serviceAt.AsTime()
		if !ok {
			return ticketInput{}, errors.New("helpdesk: invalid service_at")
		}
		input.serviceAt = &clockValue
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
		if input.priority == nil {
			create = create.WithPriorityNull()
		} else {
			create = create.WithPriority(*input.priority)
		}
		if input.details == nil {
			create = create.WithDetailsNull()
		} else {
			create = create.WithDetails(*input.details)
		}
		if input.resolution == nil {
			create = create.WithResolutionNull()
		} else {
			create = create.WithResolution(*input.resolution)
		}
		if input.dueAt == nil {
			create = create.WithDueAtNull()
		} else {
			create = create.WithDueAt(*input.dueAt)
		}
		if input.reviewed == nil {
			create = create.WithReviewedNull()
		} else {
			create = create.WithReviewed(*input.reviewed)
		}
		if input.serviceOn == nil {
			create = create.WithServiceOnNull()
		} else {
			create = create.WithServiceOn(*input.serviceOn)
		}
		if input.externalReference == nil {
			create = create.WithExternalReferenceNull()
		} else {
			create = create.WithExternalReference(*input.externalReference)
		}
		if input.externalPayload == nil {
			create = create.WithExternalPayloadNull()
		} else {
			create = create.WithExternalPayload(*input.externalPayload)
		}
		if input.expectedCost == nil {
			create = create.WithExpectedCostNull()
		} else {
			create = create.WithExpectedCost(*input.expectedCost)
		}
		if input.effort == nil {
			create = create.WithEffortNull()
		} else {
			create = create.WithEffort(*input.effort)
		}
		if input.elapsed == nil {
			create = create.WithElapsedNull()
		} else {
			create = create.WithElapsed(*input.elapsed)
		}
		if input.serviceAt == nil {
			create = create.WithServiceAtNull()
		} else {
			create = create.WithServiceAt(*input.serviceAt)
		}
		var err error
		created, err = models.TicketObjects.Create(ctx, session, create)
		if err == nil {
			created, err = a.publishableTicket(ctx, session, created.ID)
		}
		return err
	})
	if err != nil {
		return models.Ticket{}, err
	}
	return created, nil
}
func (a *Application) update(ctx context.Context, id int64, input ticketInput) (models.Ticket, []string, error) {
	patch := models.TicketPatch{}.WithSubject(input.subject).WithClosed(input.closed)
	if input.priority == nil {
		patch = patch.WithPriorityNull()
	} else {
		patch = patch.WithPriority(*input.priority)
	}
	if input.details == nil {
		patch = patch.WithDetailsNull()
	} else {
		patch = patch.WithDetails(*input.details)
	}
	if input.resolution == nil {
		patch = patch.WithResolutionNull()
	} else {
		patch = patch.WithResolution(*input.resolution)
	}
	if input.dueAt == nil {
		patch = patch.WithDueAtNull()
	} else {
		patch = patch.WithDueAt(*input.dueAt)
	}

	if input.reviewed == nil {
		patch = patch.WithReviewedNull()
	} else {
		patch = patch.WithReviewed(*input.reviewed)
	}
	if input.serviceOn == nil {
		patch = patch.WithServiceOnNull()
	} else {
		patch = patch.WithServiceOn(*input.serviceOn)
	}
	if input.externalReference == nil {
		patch = patch.WithExternalReferenceNull()
	} else {
		patch = patch.WithExternalReference(*input.externalReference)
	}
	if input.externalPayload == nil {
		patch = patch.WithExternalPayloadNull()
	} else {
		patch = patch.WithExternalPayload(*input.externalPayload)
	}
	if input.expectedCost == nil {
		patch = patch.WithExpectedCostNull()
	} else {
		patch = patch.WithExpectedCost(*input.expectedCost)
	}
	if input.effort == nil {
		patch = patch.WithEffortNull()
	} else {
		patch = patch.WithEffort(*input.effort)
	}
	if input.elapsed == nil {
		patch = patch.WithElapsedNull()
	} else {
		patch = patch.WithElapsed(*input.elapsed)
	}
	if input.serviceAt == nil {
		patch = patch.WithServiceAtNull()
	} else {
		patch = patch.WithServiceAt(*input.serviceAt)
	}
	return a.updatePatch(ctx, id, patch, true)
}

// Resolve the current row and apply explicit changes in the same transaction.
// Comparing assignments preserves omitted fields and does not expose or mutate
// the generated mutation's private model value.
func (a *Application) updatePatch(ctx context.Context, id int64, patch models.TicketPatch, formInput bool) (models.Ticket, []string, error) {
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
		mutation := patch.BuildPatch(current)
		if err := mutation.Err(); err != nil {
			var queryError *query.Error
			if errors.As(err, &queryError) && queryError.Code == query.CodeEmptyPatch {
				updated = current
				return nil
			}
			return err
		}
		if formInput {
			for _, assignment := range mutation.Assignments() {
				if assignment.Field().Name() != "external_payload" {
					continue
				}
				before, after := jsonvalue.Null(), jsonvalue.Null()
				if current.ExternalPayload != nil {
					before = *current.ExternalPayload
				}
				if !assignment.Value().IsNull() {
					var valid bool
					after, valid = assignment.Value().JSON()
					if !valid {
						return errors.New("helpdesk: invalid JSON form assignment")
					}
				}
				// Form ignores object order and equivalent floating spellings,
				// and cannot distinguish SQL NULL from a stored JSON null. Keep
				// the original document even when another field changes. API
				// input retains its separate explicit-null and exact-token rules.
				if forms.JSON(before).Equal(forms.JSON(after)) {
					if current.ExternalPayload == nil {
						patch = patch.WithExternalPayloadNull()
					} else {
						patch = patch.WithExternalPayload(before)
					}
				}
			}
			mutation = patch.BuildPatch(current)
			if err := mutation.Err(); err != nil {
				return err
			}
		}
		descriptor := models.TicketDescriptor{}
		assignments := mutation.Assignments()
		for _, field := range descriptor.Metadata().Fields {
			for _, assignment := range assignments {
				if assignment.Field().Name() != field.Name {
					continue
				}
				before, valid := descriptor.WriteFieldValue(current, field)
				if !valid {
					return errors.New("helpdesk: field cannot be read for update")
				}
				if !ticketValuesEqual(before, assignment.Value()) {
					changed = append(changed, field.Name)
				}
			}
		}
		if len(changed) == 0 {
			updated = current
			return nil
		}
		updated, err = models.TicketObjects.Update(ctx, session, current, patch)
		if err == nil {
			updated, err = a.publishableTicket(ctx, session, updated.ID)
		}
		return err
	})
	if err != nil {
		return models.Ticket{}, nil, err
	}
	return updated, changed, nil
}

// Native storage can normalize a JSON numeric token into a longer spelling.
// Re-read and validate the real response inside the transaction, including the
// detail/list wrapper, so a successful write cannot precede a renderer failure.
func (a *Application) publishableTicket(ctx context.Context, session db.Session, id int64) (models.Ticket, error) {
	stored, found, err := ticket(ctx, session, id)
	if err != nil {
		return models.Ticket{}, err
	}
	if !found {
		return models.Ticket{}, admin.ErrObjectNotFound
	}
	value, err := a.encoder.Encode(stored)
	if err != nil {
		return models.Ticket{}, err
	}
	list, err := serializers.NewList(value)
	if err != nil {
		return models.Ticket{}, err
	}
	if _, err := serializers.Encode(list, serializers.Limits{}); err != nil {
		return models.Ticket{}, err
	}
	return stored, nil
}

// This application's form and API share an input envelope small enough for a
// ticket inside a detail/list response. Model JSON and generic forms keep their
// own broader limits and NUL policy.
func externalPayloadErrors(document jsonvalue.Value) validation.Errors {
	limits := serializers.Limits{MaxDocumentBytes: maximumJSONBodyBytes, MaxDepth: serializers.DefaultMaxDepth - 2}
	if _, err := serializers.Encode(serializers.JSON(document), limits); err != nil {
		return validation.NewErrors(validation.New("external_payload", "invalid"))
	}
	return validation.NewErrors()
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
	if err != nil {
		return models.Ticket{}, err
	}
	return removed, nil
}

func (a *Application) apiList(request *web.Request, _ auth.Principal) (web.Response, error) {
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
}

func (a *Application) apiCreate(request *web.Request, _ auth.Principal) (web.Response, error) {
	values, response, handled, err := a.bindInput(request, serializers.ModeFull)
	if handled || err != nil {
		return response, err
	}
	subject, _ := values.Get("subject")
	text, _ := subject.AsString()
	closed, _ := values.Get("closed")
	boolean, _ := closed.AsBoolean()
	input := ticketInput{subject: text, closed: boolean}
	if priority, present := values.Get("priority"); present && !priority.IsNull() {
		integer, _ := priority.AsInteger()
		input.priority = &integer
	}
	if details, present := values.Get("details"); present && !details.IsNull() {
		text, _ := details.AsString()
		input.details = &text
	}
	if resolution, present := values.Get("resolution"); present && !resolution.IsNull() {
		text, _ := resolution.AsString()
		input.resolution = &text
	}
	if dueAt, present := values.Get("due_at"); present && !dueAt.IsNull() {
		instant, _ := dueAt.AsDateTime()
		input.dueAt = &instant
	}
	if reviewed, present := values.Get("reviewed"); present && !reviewed.IsNull() {
		boolean, _ := reviewed.AsBoolean()
		input.reviewed = &boolean
	}
	if serviceOn, present := values.Get("service_on"); present && !serviceOn.IsNull() {
		date, _ := serviceOn.AsDate()
		input.serviceOn = &date
	}
	if reference, present := values.Get("external_reference"); present && !reference.IsNull() {
		identifier, _ := reference.AsUUID()
		input.externalReference = &identifier
	}
	if payload, present := values.Get("external_payload"); present && !payload.IsNull() {
		document, _ := payload.AsJSON()
		input.externalPayload = &document
	}
	if cost, present := values.Get("expected_cost"); present && !cost.IsNull() {
		number, _ := cost.AsDecimal()
		input.expectedCost = &number
	}
	if effort, present := values.Get("effort"); present && !effort.IsNull() {
		floatValue, _ := effort.AsFloat()
		input.effort = &floatValue
	}
	if elapsed, present := values.Get("elapsed"); present && !elapsed.IsNull() {
		durationValue, _ := elapsed.AsDuration()
		input.elapsed = &durationValue
	}
	if serviceAt, present := values.Get("service_at"); present && !serviceAt.IsNull() {
		clockValue, _ := serviceAt.AsTime()
		input.serviceAt = &clockValue
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

// Business values use numeric equality for Float and Decimal, including zero's
// sign. AST identity retains the canonical representation of each value.
func ticketValuesEqual(left, right query.Value) bool {
	if before, ok := left.Decimal(); ok {
		after, rightOK := right.Decimal()
		return rightOK && before.Equal(after)
	}
	if before, ok := left.Float(); ok {
		after, rightOK := right.Float()
		return rightOK && before == after
	}
	return left.Equal(right)
}
