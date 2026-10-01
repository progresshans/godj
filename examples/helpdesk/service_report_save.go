package helpdesk

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type appendReportAudit func(context.Context, db.Session, admin.PreparedEvent) error

type savedServiceReport struct {
	report   models.ServiceReport
	created  bool
	changed  []string
	response web.Response
}

func reportSavePermission(ctx context.Context, actor auth.Principal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !actor.Authenticated() || !actor.Has(ViewServiceReport) || !actor.Has(AddServiceReport) || !actor.Has(ChangeServiceReport) {
		return admin.NewOperationError(admin.OperationDenied, nil)
	}
	return nil
}

func (a *Application) saveServiceReport(ctx context.Context, actor auth.Principal, input serviceReportInput, appendAudit appendReportAudit) (savedServiceReport, error) {
	if ctx == nil || appendAudit == nil {
		return savedServiceReport{}, errors.New("helpdesk: report save requires context and transactional audit")
	}
	if err := reportSavePermission(ctx, actor); err != nil {
		return savedServiceReport{}, err
	}
	return runApplicationAtomic(ctx, a.backend, "report save", func(ctx context.Context, session db.Session) (savedServiceReport, error) {
		return a.saveServiceReportInSession(ctx, session, actor, input, appendAudit)
	})
}

// Lock the addressed ticket before its report. NO KEY UPDATE keeps category
// membership stable through commit while permitting foreign-key references.
// SQLite instead owns the same read/write transaction and propagates conflicts.
func (a *Application) requireSaveReportTicket(ctx context.Context, session db.Session, id int64) error {
	if id <= 0 {
		return admin.ErrObjectNotFound
	}
	capability, ok := session.(db.ReadModifyWriteSession)
	if !ok {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "report save requires native read/modify/write ownership"}
	}
	policy, err := capability.ReadModifyWritePolicy(ctx)
	if err != nil {
		return err
	}
	q := models.TicketObjects.Using(session).Filter(models.TicketFields.ID.Exact(id), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID))
	switch policy {
	case db.ReadModifyWriteRowLock:
		q = q.SelectForUpdate(orm.RowLockOptions{NoKey: true}, q.LockTarget())
	case db.ReadModifyWriteConflict:
	default:
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "report save received an unknown native write policy"}
	}
	_, err = q.Get(ctx)
	if errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
		return admin.ErrObjectNotFound
	}
	return err
}

type serviceReportSavePatch struct {
	input   serviceReportInput
	changed *[]string
}

func (input serviceReportSavePatch) BuildPatch(current models.ServiceReport) orm.Mutation[models.ServiceReport] {
	patch := models.ServiceReportPatch{}
	if current.Summary != input.input.summary {
		patch = patch.WithSummary(input.input.summary)
		*input.changed = append(*input.changed, "summary")
	}
	if current.Completed != input.input.completed {
		patch = patch.WithCompleted(input.input.completed)
		*input.changed = append(*input.changed, "completed")
	}
	return patch.BuildPatch(current)
}

func (a *Application) saveServiceReportInSession(ctx context.Context, session db.Session, actor auth.Principal, input serviceReportInput, appendAudit appendReportAudit) (savedServiceReport, error) {
	if err := reportSavePermission(ctx, actor); err != nil {
		return savedServiceReport{}, err
	}
	if err := a.requireSaveReportTicket(ctx, session, input.ticketID); err != nil {
		return savedServiceReport{}, err
	}
	var changed []string
	value, created, err := models.ServiceReportObjects.Using(session).Filter(
		a.relations.ModelsServiceReport.Ticket.ID.Exact(input.ticketID),
		a.relations.ModelsServiceReport.Ticket.Category().ID.Exact(a.categoryID),
	).UpdateOrCreate(ctx, models.NewServiceReportCreate(input.ticketID, input.summary).WithCompleted(input.completed), serviceReportSavePatch{input: input, changed: &changed})
	if err != nil {
		return savedServiceReport{}, writeRejection(err)
	}
	value, err = a.publishableReport(ctx, session, value.ID)
	if err != nil {
		return savedServiceReport{}, err
	}
	if value.TicketID != input.ticketID {
		return savedServiceReport{}, admin.ErrObjectNotFound
	}
	encoded, err := a.reportEncoder.Encode(value)
	if err != nil {
		return savedServiceReport{}, err
	}
	object, err := serializers.NewObject(serializers.MemberOf("report", encoded), serializers.MemberOf("created", serializers.Boolean(created)))
	if err != nil {
		return savedServiceReport{}, err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	response, err := api.JSON(status, object.Value())
	if err != nil {
		return savedServiceReport{}, err
	}
	if created || len(changed) > 0 {
		action := admin.ActionChange
		if created {
			action = admin.ActionAdd
		}
		event, err := admin.PrepareEvent(actor.ID(), "helpdesk.service_report", value.ID, action, changed, fmt.Sprintf("Report #%d", value.ID))
		if err != nil {
			return savedServiceReport{}, err
		}
		if err := appendAudit(ctx, session, event); err != nil {
			return savedServiceReport{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return savedServiceReport{}, err
	}
	return savedServiceReport{report: value, created: created, changed: changed, response: response}, nil
}

func (a *Application) reportSaveCommands(form forms.Spec) []admin.CollectionCommandConfig {
	if a.adminAudit == nil {
		return nil
	}
	return []admin.CollectionCommandConfig{{Name: "save", Label: "Save report for a ticket", Permission: ChangeServiceReport,
		AdditionalPermissions: []auth.Permission{AddServiceReport, ViewServiceReport}, Form: form,
		RelatedChoices: []admin.RelatedChoices{{Field: "ticket", Permission: ViewTicket, Load: a.reportTicketChoices}},
		Run: func(ctx context.Context, actor auth.Principal, values forms.Values) (admin.CollectionCommandResult, error) {
			ticket, ok := values.Integer("ticket")
			if !ok {
				return admin.CollectionCommandResult{}, errors.New("helpdesk: invalid report ticket")
			}
			summary, ok := values.String("summary")
			if !ok {
				return admin.CollectionCommandResult{}, errors.New("helpdesk: invalid report summary")
			}
			completed, ok := values.Boolean("completed")
			if !ok {
				return admin.CollectionCommandResult{}, errors.New("helpdesk: invalid report completion")
			}
			result, err := a.saveServiceReport(ctx, actor, serviceReportInput{ticketID: ticket, summary: summary, completed: completed}, a.adminAudit)
			if err != nil {
				return admin.CollectionCommandResult{}, err
			}
			return admin.CollectionCommandResult{ID: result.report.ID, Created: result.created, Changed: len(result.changed) > 0}, nil
		},
	}}
}

func (a *Application) apiReportSave(appendAudit appendReportAudit) api.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		if request.HTTP().URL.RawQuery != "" {
			return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, validation.NewErrors(validation.New(validation.NonField, "invalid")))
		}
		id, ok := objectRequestID(request)
		if !ok {
			return objectNotFound()
		}
		values, response, handled, err := a.bindInputSpec(request, a.reportSaveInput, serializers.ModeFull)
		if handled || err != nil {
			return response, err
		}
		summaryValue, _ := values.Get("summary")
		summary, _ := summaryValue.AsString()
		completedValue, _ := values.Get("completed")
		completed, _ := completedValue.AsBoolean()
		result, err := a.saveServiceReport(request.Context(), actor, serviceReportInput{ticketID: id, summary: summary, completed: completed}, appendAudit)
		if err != nil {
			return objectFailure(err)
		}
		return result.response, nil
	}
}
