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
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const (
	ViewCategory        auth.Permission = "helpdesk.view_category"
	ViewTicket          auth.Permission = "helpdesk.view_ticket"
	AddTicket           auth.Permission = "helpdesk.add_ticket"
	ChangeTicket        auth.Permission = "helpdesk.change_ticket"
	DeleteTicket        auth.Permission = "helpdesk.delete_ticket"
	ViewServiceReport   auth.Permission = "helpdesk.view_service_report"
	AddServiceReport    auth.Permission = "helpdesk.add_service_report"
	ChangeServiceReport auth.Permission = "helpdesk.change_service_report"
	DeleteServiceReport auth.Permission = "helpdesk.delete_service_report"
	ViewLabel           auth.Permission = "helpdesk.view_label"
	AddLabel            auth.Permission = "helpdesk.add_label"
	ChangeLabel         auth.Permission = "helpdesk.change_label"
	DeleteLabel         auth.Permission = "helpdesk.delete_label"
	ViewTicketLabel     auth.Permission = "helpdesk.view_ticket_label"
	AddTicketLabel      auth.Permission = "helpdesk.add_ticket_label"
	ChangeTicketLabel   auth.Permission = "helpdesk.change_ticket_label"
	DeleteTicketLabel   auth.Permission = "helpdesk.delete_ticket_label"
)

type Backend interface {
	db.Queryer
	db.SnapshotReader
	db.Mutator
	db.Atomic
	db.RelationAtomic
}

type Application struct {
	resources       modelResources
	adminAudit      func(context.Context, db.Session, admin.PreparedEvent) error
	backend         Backend
	categoryID      int64
	registry        admin.Registry
	input           serializers.Spec
	ticketInput     input.Body[orm.ModelInput[models.Ticket]]
	output          serializers.Spec
	encoder         serializers.ModelEncoder[ticketRecord]
	responses       apiOutputs
	queries         apiQueries
	parser          api.Parser
	relations       project.Relations
	collections     project.Collections
	objects         project.Models
	deleters        project.RelationDeleters
	reportInput     input.Body[reportAPIInput]
	reportSaveInput input.Body[reportAPIInput]
	reportOutput    serializers.Spec
	reportEncoder   serializers.ModelEncoder[models.ServiceReport]
	labelInput      input.Body[orm.ModelInput[models.Label]]
	labelOutput     serializers.Spec
	labelEncoder    serializers.ModelEncoder[models.Label]
	linkInput       serializers.Spec
	linkOutput      serializers.Spec
	linkEncoder     serializers.ModelEncoder[models.TicketLabel]
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
	a.collections, err = project.BindCollections()
	if err != nil {
		return nil, err
	}
	a.deleters, err = project.BindRelationDeleters()
	if err != nil {
		return nil, err
	}
	if err = a.initResources(); err != nil {
		return nil, err
	}
	if err = a.initReports(); err != nil {
		return nil, err
	}
	if err = a.initLabels(); err != nil {
		return nil, err
	}
	if err = a.initTicketLabels(); err != nil {
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
		serializers.ModelField{Name: "resolution", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "due_at", Optional: true}, serializers.ModelField{Name: "reviewed", Optional: true}, serializers.ModelField{Name: "service_on", Optional: true}, serializers.ModelField{Name: "service_at", Optional: true}, serializers.ModelField{Name: "elapsed", Optional: true}, serializers.ModelField{Name: "effort", Optional: true}, serializers.ModelField{Name: "expected_cost", Optional: true}, serializers.ModelField{Name: "external_reference", Optional: true}, serializers.ModelField{Name: "external_payload", Optional: true}, serializers.ModelField{Name: "external_payload_digest"}, serializers.ModelField{Name: "external_url", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "labels", Optional: true})
	if err != nil {
		return nil, err
	}
	a.output, err = serializers.FromModel(metadata,
		serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "subject"}, serializers.ModelField{Name: "details", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "closed"}, serializers.ModelField{Name: "category", ReadOnly: true}, serializers.ModelField{Name: "priority", Optional: true},
		serializers.ModelField{Name: "resolution", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "due_at", Optional: true}, serializers.ModelField{Name: "reviewed", Optional: true}, serializers.ModelField{Name: "service_on", Optional: true}, serializers.ModelField{Name: "service_at", Optional: true}, serializers.ModelField{Name: "elapsed", Optional: true}, serializers.ModelField{Name: "effort", Optional: true}, serializers.ModelField{Name: "expected_cost", Optional: true}, serializers.ModelField{Name: "external_reference", Optional: true}, serializers.ModelField{Name: "external_payload", Optional: true}, serializers.ModelField{Name: "external_payload_digest"}, serializers.ModelField{Name: "external_url", Optional: true, AllowEmpty: true}, serializers.ModelField{Name: "labels", ReadOnly: true})
	if err != nil {
		return nil, err
	}
	a.encoder, err = serializers.NewModelEncoder(a.output, metadata, ticketScalar, ticketCollection)
	if err != nil {
		return nil, err
	}
	a.responses, err = prepareAPIOutputs(a.encoder, a.labelEncoder)
	if err != nil {
		return nil, err
	}
	a.ticketInput, err = prepareTicketAPIInput(a.resources.ticket, a.input)
	if err != nil {
		return nil, err
	}
	a.queries, err = prepareAPIQueries()
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
	return []auth.Permission{ViewCategory, ViewTicket, AddTicket, ChangeTicket, DeleteTicket, ViewServiceReport, AddServiceReport, ChangeServiceReport, DeleteServiceReport, ViewLabel, AddLabel, ChangeLabel, DeleteLabel, ViewTicketLabel, AddTicketLabel, ChangeTicketLabel, DeleteTicketLabel}
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
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[models.Category], error) {
			var filters []orm.Predicate[models.Category]
			if request.Search != "" {
				filters = append(filters, models.CategoryFields.Name.IContains(request.Search))
			}
			page, err := a.resources.category.List(ctx, a.backend, crud.ListOptions[models.Category]{Filter: filters, Offset: request.Offset, Limit: request.Limit})
			if err != nil {
				return admin.Page[models.Category]{}, err
			}
			return admin.Page[models.Category]{Items: page.Items, Total: page.Total, Offset: page.Offset, Limit: page.Limit}, nil
		},
		Snapshot: func(value models.Category) (admin.Object, error) {
			return categoryProjector.Project(value, value.ID, value.Name)
		},
	}); err != nil {
		return err
	}
	descriptor := models.TicketDescriptor{}
	metadata := descriptor.Metadata()
	fields := a.resources.ticket.Fields()
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
	ticketProjector, err := admin.NewModelProjector(metadata, descriptor.WriteFieldValue, "id", "subject", "details", "closed", "category", "priority", "resolution", "due_at", "reviewed", "service_on", "service_at", "elapsed", "effort", "expected_cost", "external_reference", "external_payload", "external_url", "external_payload_digest")
	if err != nil {
		return err
	}
	inlines, err := a.ticketAdminInlines()
	if err != nil {
		return err
	}
	bulkForms, err := a.ticketBulkFormSets(fields, overrides)
	if err != nil {
		return err
	}
	if err := admin.RegisterModel(builder, admin.ModelConfig[ticketRecord]{
		Inlines:            inlines,
		CollectionFormSets: bulkForms,
		Actions:            a.ticketBulkActions(),
		AppLabel:           "helpdesk", Slug: "tickets", Model: metadata, FormFields: fields,
		FormOverrides: overrides,
		ReadOnlyFields: []admin.ReadOnlyField[ticketRecord]{{Name: "external_payload_digest", Label: "Payload SHA-256 (base64)", Value: func(value ticketRecord) (string, error) {
			if value.ExternalPayloadDigest == nil {
				return "No digest", nil
			}
			return value.ExternalPayloadDigest.Base64(), nil
		}}},
		RelatedChoices: []admin.RelatedChoices{{Field: "labels", Permission: ViewLabel, Load: a.ticketLabelChoices}},
		ListFields:     []string{"id", "subject", "category", "closed", "priority", "due_at", "reviewed", "service_on", "service_at", "elapsed", "effort", "expected_cost", "external_reference", "external_payload", "external_url", "external_payload_digest"}, SearchFields: []string{"subject", "external_payload"},
		Permissions: admin.Permissions{View: ViewTicket, Add: AddTicket, Change: ChangeTicket, Delete: DeleteTicket},
		List: func(ctx context.Context, _ auth.Principal, request admin.ListRequest) (admin.Page[ticketRecord], error) {
			return a.list(ctx, request)
		},
		Get: func(ctx context.Context, _ auth.Principal, id int64) (ticketRecord, bool, error) {
			return a.readTicketRecord(ctx, a.objects, id)
		},
		Snapshot: func(value ticketRecord) (admin.Object, error) {
			return ticketSnapshot(value, ticketProjector)
		},
		Initial: func(value ticketRecord) (map[string]forms.Value, error) {
			return formmodel.InitialValues(metadata, form, value, ticketScalar, ticketCollection)
		},
		ValidateCreate: func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
			return a.checkTicketForm(ctx, actor, 0, bound)
		},
		ValidateChange: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm) error {
			return a.checkTicketForm(ctx, actor, mutation.ID, bound)
		},
		Create: func(ctx context.Context, principal auth.Principal, bound formmodel.BoundForm, inlines admin.InlineSubmission) (ticketRecord, error) {
			created, _, err := a.saveTicketForm(ctx, principal, 0, bound, inlines)
			return created, err
		},
		Update: func(ctx context.Context, principal auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, inlines admin.InlineSubmission) (ticketRecord, []string, error) {
			return a.saveTicketForm(ctx, principal, mutation.ID, bound, inlines)
		},
		Delete: func(ctx context.Context, _ auth.Principal, mutation admin.Mutation) (ticketRecord, error) {
			id := mutation.ID
			return a.delete(ctx, id)
		},
	}); err != nil {
		return err
	}
	if err := a.registerReports(builder); err != nil {
		return err
	}
	if err := a.registerLabels(builder); err != nil {
		return err
	}
	return a.registerTicketLabels(builder)
}

func (a *Application) list(ctx context.Context, request admin.ListRequest) (admin.Page[ticketRecord], error) {
	return a.listMatching(ctx, request, "")
}

func (a *Application) listMatching(ctx context.Context, request admin.ListRequest, source string) (admin.Page[ticketRecord], error) {
	rows := a.objects.ModelsTicket.Filter(a.resources.ticket.Predicates()...).OrderBy(models.TicketFields.ID.Asc())
	if request.Search != "" {
		rows = rows.Filter(orm.Or(models.TicketFields.Subject.IContains(request.Search), models.TicketFields.ExternalPayload.IContains(request.Search)))
	}
	if source != "" {
		rows = rows.Filter(models.TicketFields.ExternalPayload.At(query.JSONKey("source")).IContains(source))
	}
	page, err := crud.ListQuery(ctx, a.withTicketLabels(rows), request.Offset, request.Limit)
	if err != nil {
		return admin.Page[ticketRecord]{}, err
	}
	selected := page.Items
	items := make([]ticketRecord, len(selected))
	for index, row := range selected {
		items[index], err = ticketRecordFromModel(ctx, row)
		if err != nil {
			return admin.Page[ticketRecord]{}, err
		}
	}
	return admin.Page[ticketRecord]{Items: items, Total: page.Total, Offset: page.Offset, Limit: page.Limit}, nil
}

func ticket(ctx context.Context, backend db.Queryer, id int64) (models.Ticket, bool, error) {
	return models.TicketObjects.Using(backend).Filter(models.TicketFields.ID.Exact(id)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
}
func (a *Application) createTicket(ctx context.Context, principal auth.Principal, input orm.ModelInput[models.Ticket]) (output.Prepared[ticketRecord], error) {
	return runApplicationRelationAtomic(ctx, a.backend, func(ctx context.Context, session db.RelationSession) (output.Prepared[ticketRecord], error) {
		created, err := a.createTicketInSession(ctx, session, principal, input)
		if err != nil {
			return output.Prepared[ticketRecord]{}, err
		}
		return a.responses.ticket.Prepare(ctx, http.StatusCreated, created)
	})
}

func (a *Application) createTicketInSession(ctx context.Context, session db.RelationSession, principal auth.Principal, input orm.ModelInput[models.Ticket]) (ticketRecord, error) {
	if err := ticketWritePermission(ctx, principal, AddTicket); err != nil {
		return ticketRecord{}, err
	}
	mutation, err := a.resources.ticket.CreateInSession(ctx, session, input, a.ticketSaveOptions(principal, input, true, false))
	if err != nil {
		return ticketRecord{}, resourceError(err)
	}
	return a.publishableTicket(ctx, session, mutation.Value.ID, true)
}

type updatedTicket struct {
	value    ticketRecord
	changed  []string
	response output.Prepared[ticketRecord]
}

// Resolve the current row and selected collection inside the owned write.
// Response preparation and digest synchronization complete before commit.
func (a *Application) updateTicket(ctx context.Context, principal auth.Principal, id int64, input orm.ModelInput[models.Ticket]) (updatedTicket, error) {
	return runApplicationRelationAtomic(ctx, a.backend, func(work context.Context, session db.RelationSession) (updatedTicket, error) {
		if err := ticketWritePermission(work, principal, ChangeTicket); err != nil {
			return updatedTicket{}, err
		}
		mutation, err := a.resources.ticket.UpdateInSession(work, session, id, input, a.ticketSaveOptions(principal, input, false, false))
		if err != nil {
			return updatedTicket{}, resourceError(err)
		}
		scalarWritten := false
		for _, field := range mutation.ChangedFields {
			if field != "labels" {
				scalarWritten = true
			}
		}
		stored, err := a.publishableTicket(work, session, mutation.Value.ID, scalarWritten)
		if err != nil {
			return updatedTicket{}, err
		}
		changed := payloadDigestChanges(mutation.ChangedFields, *mutation.Before, stored.Ticket)
		response, err := a.responses.ticket.Prepare(work, http.StatusOK, stored)
		if err != nil {
			return updatedTicket{}, err
		}
		return updatedTicket{value: stored, changed: changed, response: response}, nil
	})
}

// Ticket's numeric no-op policy treats equal decimal values and both float
// zero signs alike. Retain the exact current representation before the generic
// preparation computes the write mask; query AST identity stays unchanged.
func preserveTicketNumericValues(current models.Ticket, candidate *models.Ticket) {
	if current.Effort != nil && candidate.Effort != nil && *current.Effort == *candidate.Effort {
		value := *current.Effort
		candidate.Effort = &value
	}
	if current.ExpectedCost != nil && candidate.ExpectedCost != nil && current.ExpectedCost.Equal(*candidate.ExpectedCost) {
		value := *current.ExpectedCost
		candidate.ExpectedCost = &value
	}
}

// Native storage can normalize a JSON numeric token into a longer spelling.
// Re-read and validate the real response inside the transaction, including the
// detail/list wrapper, so a successful write cannot precede a renderer failure.
func (a *Application) publishableTicket(ctx context.Context, session db.Session, id int64, scalarWritten bool) (ticketRecord, error) {
	objects, err := project.UsingSession(session)
	if err != nil {
		return ticketRecord{}, err
	}
	stored, found, err := a.readTicketRecord(ctx, objects, id)
	if err != nil {
		return ticketRecord{}, err
	}
	if !found {
		return ticketRecord{}, admin.ErrObjectNotFound
	}
	if err := a.validateStoredTicketLabels(ctx, session, stored); err != nil {
		return ticketRecord{}, err
	}
	if scalarWritten {
		stored.Ticket, err = synchronizePayloadDigest(ctx, session, stored.Ticket)
		if err != nil {
			return ticketRecord{}, err
		}
	}
	value, err := a.encoder.Encode(stored)
	if err != nil {
		return ticketRecord{}, err
	}
	list, err := serializers.NewList(value)
	if err != nil {
		return ticketRecord{}, err
	}
	if _, err := serializers.Encode(list, serializers.Limits{}); err != nil {
		return ticketRecord{}, err
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

func (a *Application) delete(ctx context.Context, id int64) (ticketRecord, error) {
	return runApplicationRelationAtomic(ctx, a.backend, func(work context.Context, session db.RelationSession) (ticketRecord, error) {
		var removed ticketRecord
		_, err := a.resources.ticket.DeleteInSession(work, session, id, crud.DeleteOptions[models.Ticket]{
			Before: func(ctx context.Context, backend db.Session, value models.Ticket) error {
				objects, err := project.UsingSession(backend)
				if err != nil {
					return err
				}
				var found bool
				removed, found, err = a.readTicketRecord(ctx, objects, value.ID)
				if err != nil {
					return err
				}
				if !found || removed.CategoryID != a.categoryID {
					return admin.ErrObjectNotFound
				}
				return nil
			},
			Delete: func(ctx context.Context, backend db.Session, value models.Ticket) (int64, error) {
				relations, ok := backend.(db.RelationSession)
				if !ok {
					return 0, errors.New("helpdesk: deletion lost relation session")
				}
				return a.deleters.ModelsTicket.DeleteInSession(ctx, relations, value)
			},
		})
		if err != nil {
			return ticketRecord{}, resourceError(err)
		}
		return removed, nil
	})
}

func (a *Application) apiList(request *web.Request, _ auth.Principal) (web.Response, error) {
	filters, diagnostics := parseTicketListQuery(request.HTTP().URL.RawQuery)
	if !diagnostics.Empty() {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
	}
	page, err := a.listMatching(request.Context(), admin.ListRequest{Limit: 20, Search: filters.search}, filters.source)
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
	// A page can contain many individually bounded JSON documents. Keep its
	// aggregate budget explicit without weakening request or other responses.
	return api.JSONWithLimits(http.StatusOK, value, serializers.Limits{MaxValues: maximumJSONListValues})
}

// Viewing a ticket includes its assigned category's identity and label. The
// standalone Category Admin remains protected by ViewCategory.
func (a *Application) detail(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, valid := request.Int64Parameter("id")
	if !valid || id <= 0 {
		return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
	}
	selected, found, err := a.withTicketLabels(a.objects.ModelsTicket.
		Filter(models.TicketFields.ID.Exact(id), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).
		OrderBy(models.TicketFields.ID.Asc())).
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
	raw, err := ticketRecordFromModel(request.Context(), selected)
	if err != nil {
		return web.Response{}, err
	}
	return a.responses.detail.JSON(request.Context(), http.StatusOK, ticketDetailResponse{ticket: raw, category: models.Category{ID: category.ID, Name: category.Name}})
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
