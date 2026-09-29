package helpdesk

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const TicketEditorPath = "/tickets/edit/"

// TicketEditorMaxResponseBytes also belongs in web.Config when mounting the
// editor. Forty rows of escaped label choices can exceed Web's 1 MiB default.
const TicketEditorMaxResponseBytes = 8 << 20

const (
	ticketEditorPageSize  = 20
	ticketEditorMaxForms  = 40
	ticketEditorMaxLabels = 256
)

//go:embed editor_templates/*.html
var ticketEditorFiles embed.FS

type TicketEditorConfig struct {
	Auth *sessionauth.Runtime
	// AppendAudit must append through the supplied transaction, never open or
	// commit another transaction. An error aborts the complete batch.
	AppendAudit func(context.Context, db.Session, admin.PreparedEvent) error
}

// TicketEditor is an immutable, concurrent-use HTML editor over the category
// assigned to its Application. It owns no listener, connection or credentials.
type TicketEditor struct {
	app         *Application
	auth        *sessionauth.Runtime
	appendAudit func(context.Context, db.Session, admin.PreparedEvent) error
	engine      *templates.Engine
	row         forms.Spec
	binding     orm.ProjectBinding
}

func (a *Application) TicketEditor(config TicketEditorConfig) (*TicketEditor, error) {
	if a == nil || nilFormReader(a.backend) || config.Auth == nil || config.AppendAudit == nil {
		return nil, errors.New("helpdesk: ticket editor requires application, authentication and transactional audit")
	}
	if !config.Auth.CookiesApplyTo(TicketEditorPath) || !config.Auth.AllowsNext(TicketEditorPath) {
		return nil, errors.New("helpdesk: ticket editor requires matching cookie paths and an allowed login destination")
	}
	row, err := formmodel.NewSpecForFields(models.TicketDescriptor{}.Metadata(), []string{"subject", "closed", "external_reference", "labels"})
	if err != nil {
		return nil, err
	}
	files, err := fs.Sub(ticketEditorFiles, "editor_templates")
	if err != nil {
		return nil, err
	}
	engine, err := templates.New(files, templates.Config{Limits: templates.Limits{MaxLoopItems: 20_000, MaxOutputBytes: TicketEditorMaxResponseBytes}})
	if err != nil {
		return nil, err
	}
	binding, err := project.Bind()
	if err != nil {
		return nil, err
	}
	return &TicketEditor{app: a, auth: config.Auth, appendAudit: config.AppendAudit, engine: engine, row: row, binding: binding}, nil
}

func (editor *TicketEditor) Routes() []web.Route {
	return []web.Route{
		{Name: "helpdesk:ticket-editor", Method: http.MethodGet, Path: TicketEditorPath, Handler: editor.auth.Require(ViewTicket, editor.get)},
		{Name: "helpdesk:ticket-editor-save", Method: http.MethodPost, Path: TicketEditorPath, Handler: editor.auth.Require(ViewTicket, editor.post)},
	}
}

type ticketEditorPermissions struct{ add, delete bool }

// Like the existing ticket writer, the transaction consumes this request's
// admitted immutable principal. Authorizer is a deny overlay, not a grant.
func (editor *TicketEditor) permissions(ctx context.Context, actor auth.Principal) (ticketEditorPermissions, bool, error) {
	var result ticketEditorPermissions
	for _, permission := range []auth.Permission{ChangeTicket, ViewLabel} {
		allowed, err := editor.auth.Authorized(ctx, actor, permission)
		if err != nil || !allowed {
			return result, false, err
		}
	}
	var err error
	result.add, err = editor.auth.Authorized(ctx, actor, AddTicket)
	if err != nil {
		return result, false, err
	}
	result.delete, err = editor.auth.Authorized(ctx, actor, DeleteTicket)
	return result, err == nil, err
}

type ticketEditorPage struct {
	set         formmodel.InlineSet[models.Category, models.Ticket]
	choices     []forms.Choice
	next        bool
	page        int
	permissions ticketEditorPermissions
}

func (editor *TicketEditor) load(ctx context.Context, reader db.Queryer, page int, permissions ticketEditorPermissions, submitted *forms.Data) (ticketEditorPage, error) {
	present, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(editor.app.categoryID)).Exists(ctx)
	if err != nil {
		return ticketEditorPage{}, err
	}
	if !present {
		return ticketEditorPage{}, admin.ErrObjectNotFound
	}
	rows := models.TicketObjects.Using(reader).Filter(editor.app.relations.ModelsTicket.Category.ID.Exact(editor.app.categoryID)).OrderBy(models.TicketFields.ID.Asc())
	rows, err = rows.Offset((page - 1) * ticketEditorPageSize)
	if err != nil {
		return ticketEditorPage{}, err
	}
	rows, err = rows.Limit(ticketEditorPageSize + 1)
	if err != nil {
		return ticketEditorPage{}, err
	}
	current, err := rows.All(ctx)
	if err != nil {
		return ticketEditorPage{}, err
	}
	next := len(current) > ticketEditorPageSize
	if next {
		current = current[:ticketEditorPageSize]
	}
	labels := models.LabelObjects.Using(reader).Filter(editor.app.relations.ModelsLabel.Category.ID.Exact(editor.app.categoryID)).OrderBy(models.LabelFields.ID.Asc())
	labels, err = labels.Limit(ticketEditorMaxLabels + 1)
	if err != nil {
		return ticketEditorPage{}, err
	}
	available, err := labels.All(ctx)
	if err != nil {
		return ticketEditorPage{}, err
	}
	if len(available) > ticketEditorMaxLabels {
		return ticketEditorPage{}, errors.New("helpdesk: ticket editor label choice limit exceeded")
	}
	choices := make([]forms.Choice, len(available))
	for index, value := range available {
		choices[index] = forms.Choice{Value: forms.Integer(value.ID), Label: value.Name}
	}
	keys := make([]int64, len(current))
	members := make(map[int64][]int64, len(current))
	for index, value := range current {
		keys[index] = value.ID
		members[value.ID] = []int64{}
	}
	if len(keys) > 0 {
		links, err := models.TicketLabelObjects.Using(reader).Filter(editor.app.relations.ModelsTicketLabel.Ticket.ID.In(keys...), editor.app.relations.ModelsTicketLabel.Label.Category().ID.Exact(editor.app.categoryID)).OrderBy(models.TicketLabelFields.ID.Asc()).All(ctx)
		if err != nil {
			return ticketEditorPage{}, err
		}
		for _, link := range links {
			members[link.TicketID] = append(members[link.TicketID], link.LabelID)
		}
	}
	row, err := editor.row.WithModelChoices("labels", choices...)
	if err != nil {
		return ticketEditorPage{}, err
	}
	config := forms.DefaultSetConfig()
	config.Prefix, config.MaxForms, config.AbsoluteMax = "tickets", ticketEditorMaxForms, ticketEditorMaxForms
	config.ValidateMax, config.CanDelete = true, true
	config.ExtraForms = 0
	if permissions.add {
		config.ExtraForms = 2
	}
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		return ticketEditorPage{}, err
	}
	inline, err := formmodel.NewInlineSpec(editor.binding, models.CategoryObjects, models.TicketObjects, "category", spec)
	if err != nil {
		return ticketEditorPage{}, err
	}
	parent := models.NewCategoryWithID(editor.app.categoryID)
	readerFn := func(value models.Ticket, field ir.ManyToManyField) ([]int64, bool) {
		values, found := members[value.ID]
		return values, found && field.Name == "labels"
	}
	view := ticketEditorPage{choices: choices, next: next, page: page, permissions: permissions}
	if submitted == nil {
		view.set, err = inline.Unbound(parent, current, readerFn)
	} else {
		view.set, err = inline.Bind(*submitted, parent, current, formmodel.PostClean{}, readerFn)
	}
	return view, err
}

func (editor *TicketEditor) get(request *web.Request, actor auth.Principal) (web.Response, error) {
	permissions, allowed, err := editor.permissions(request.Context(), actor)
	if err != nil {
		return web.Response{}, err
	}
	if !allowed {
		return ticketEditorText(http.StatusForbidden)
	}
	page, err := ticketEditorPageNumber(request)
	if err != nil {
		return ticketEditorText(http.StatusBadRequest)
	}
	var view ticketEditorPage
	calls := 0
	var callbackErr error
	err = editor.app.backend.ReadSnapshot(request.Context(), func(reader db.Queryer) error {
		calls++
		if calls != 1 {
			return errors.New("helpdesk: repeated editor read callback")
		}
		var err error
		view, err = editor.load(request.Context(), reader, page, permissions, nil)
		callbackErr = err
		return err
	})
	if callbackErr != nil && (err == nil || !errors.Is(err, callbackErr)) {
		return web.Response{}, errors.Join(errors.New("helpdesk: editor read failure was suppressed"), err, callbackErr)
	}
	if err != nil {
		return ticketEditorOperationError(err)
	}
	if calls != 1 {
		return web.Response{}, errors.New("helpdesk: missing editor read callback")
	}
	return editor.render(request, view)
}

func (editor *TicketEditor) post(request *web.Request, actor auth.Principal) (web.Response, error) {
	permissions, allowed, err := editor.permissions(request.Context(), actor)
	if err != nil {
		return web.Response{}, err
	}
	if !allowed {
		return ticketEditorText(http.StatusForbidden)
	}
	page, err := ticketEditorPageNumber(request)
	if err != nil {
		return ticketEditorText(http.StatusBadRequest)
	}
	values, err := ticketEditorData(request)
	if err != nil {
		return ticketEditorText(http.StatusBadRequest)
	}
	if err = editor.auth.VerifyCSRF(request, values["csrfmiddlewaretoken"]); err != nil {
		var csrf *sessionauth.Error
		if errors.As(err, &csrf) && csrf.Code == sessionauth.CodeCSRFRejected {
			return ticketEditorText(http.StatusForbidden)
		}
		return web.Response{}, err
	}
	view, err := editor.save(request.Context(), actor, page, permissions, forms.NewData(values))
	if _, rejected := validation.Rejected(err); rejected && view.set.FormSet().Bound() {
		return editor.render(request, view)
	}
	if err != nil {
		return ticketEditorOperationError(err)
	}
	header := ticketEditorHeaders("text/plain; charset=utf-8")
	header.Set("Location", TicketEditorPath+"?p="+strconv.Itoa(page))
	return web.NewResponse(http.StatusSeeOther, header, []byte("See Other\n"))
}

var errTicketEditorPermission = errors.New("helpdesk: batch operation was not admitted")

func (editor *TicketEditor) save(ctx context.Context, actor auth.Principal, page int, permissions ticketEditorPermissions, data forms.Data) (ticketEditorPage, error) {
	var view ticketEditorPage
	var callbackErr error
	calls := 0
	err := editor.app.backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		calls++
		if calls != 1 {
			callbackErr = errors.New("helpdesk: repeated editor transaction callback")
			return callbackErr
		}
		callbackErr = func() error {
			if err := ticketWritePermission(ctx, actor, ChangeTicket); err != nil {
				return err
			}
			var err error
			view, err = editor.load(ctx, session, page, permissions, &data)
			if err != nil {
				return err
			}
			for _, row := range view.set.FormSet().Forms() {
				if row.DeletionRequested() && !permissions.delete {
					return errTicketEditorPermission
				}
				if row.Index() >= view.set.FormSet().InitialForms() && len(row.Form().Changed()) > 0 && !permissions.add {
					return errTicketEditorPermission
				}
			}
			if !view.set.Valid() {
				return validation.Reject(validation.NewErrors(validation.New(validation.NonField, "invalid_formset")), nil)
			}
			prepared, err := view.set.Prepare()
			if err != nil {
				return err
			}
			// Delete within the same borrowed session, including the complete
			// incoming PROTECT/CASCADE policy. A later failure restores it all.
			for _, row := range prepared.Deleted() {
				current, err := row.Model()
				if err != nil {
					return err
				}
				if !actor.Has(DeleteTicket) || !permissions.delete {
					return errTicketEditorPermission
				}
				if _, err = editor.app.deleters.ModelsTicket.DeleteInSession(ctx, session, current); err != nil {
					if protected, ok := err.(*query.ProtectedForeignKeyError); ok && protected.ProtectedSourceRows() > 0 {
						failures := validation.NewErrors(validation.New(validation.NonField, "protected", validation.NewParam("index", strconv.Itoa(row.Index()))))
						view.set, err = view.set.WithErrors(failures)
						if err != nil {
							return err
						}
						return validation.Reject(failures, protected)
					}
					return err
				}
				event, err := admin.PrepareEvent(actor.ID(), "helpdesk.ticket", current.ID, admin.ActionDelete, nil, current.Subject)
				if err != nil {
					return err
				}
				if err = editor.appendAudit(ctx, session, event); err != nil {
					return err
				}
			}
			for _, row := range prepared.Rows() {
				instance, found := view.set.Instance(row.Index())
				if !found {
					return errors.New("helpdesk: missing prepared editor row")
				}
				id := int64(0)
				if row.Existing() {
					current, _, err := view.set.Current(row.Index())
					if err != nil {
						return err
					}
					id = current.ID
				}
				saved, changed, err := editor.app.saveTicketFormInSession(ctx, session, actor, id, instance.BoundForm())
				if err != nil {
					if failures, rejected := validation.Rejected(err); rejected {
						view.set, callbackErr = view.set.WithRowErrors(row.Index(), failures)
						if callbackErr != nil {
							return callbackErr
						}
					}
					return err
				}
				if id != 0 && len(changed) == 0 {
					continue
				}
				action := admin.ActionChange
				if id == 0 {
					action = admin.ActionAdd
				}
				event, err := admin.PrepareEvent(actor.ID(), "helpdesk.ticket", saved.ID, action, changed, saved.Subject)
				if err != nil {
					return err
				}
				if err = editor.appendAudit(ctx, session, event); err != nil {
					return err
				}
			}
			return nil
		}()
		return callbackErr
	})
	if calls > 1 || calls == 0 && err == nil || callbackErr != nil && (err == nil || !errors.Is(err, callbackErr)) {
		return ticketEditorPage{}, errors.Join(errors.New("helpdesk: invalid editor transaction ownership"), err, callbackErr)
	}
	if err != nil {
		return view, operationError(ctx, err)
	}
	return view, nil
}

func ticketEditorOperationError(err error) (web.Response, error) {
	if err == admin.ErrObjectNotFound {
		return ticketEditorText(http.StatusNotFound)
	}
	if err == errTicketEditorPermission {
		return ticketEditorText(http.StatusForbidden)
	}
	return web.Response{}, err
}
