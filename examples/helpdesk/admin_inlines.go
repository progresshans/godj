package helpdesk

import (
	"context"
	"errors"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/validation"
)

const reportInlinePrefix = "reports"

// AdminConfig enables the Ticket / ServiceReport inline editor. AppendAudit
// must use the supplied session and propagate every error to the caller.
// The immutable principal and Site deny-overlay admission are retained; the
// writer re-reads the current category, parent and children in its transaction.
type AdminConfig struct {
	AppendAudit func(context.Context, db.Session, admin.PreparedEvent) error
}

// AdminRegistry builds an independent registration with atomic inline editing.
// It does no I/O and does not change the application's standalone registry.
func (a *Application) AdminRegistry(config AdminConfig) (admin.Registry, error) {
	if a == nil || nilFormReader(a.backend) || config.AppendAudit == nil {
		return admin.Registry{}, errors.New("helpdesk: inline Admin requires application and transactional audit")
	}
	installed, err := apps.New(InstalledApps())
	if err != nil {
		return admin.Registry{}, err
	}
	scoped := *a
	scoped.inlineAudit = config.AppendAudit
	builder := admin.NewBuilder(installed)
	if err := scoped.register(builder); err != nil {
		return admin.Registry{}, err
	}
	return builder.Build()
}

func reportInlineConfig() forms.SetConfig {
	config := forms.DefaultSetConfig()
	config.Prefix, config.ExtraForms, config.MaxForms, config.AbsoluteMax = reportInlinePrefix, 1, 1, 2
	config.ValidateMax, config.CanDelete = true, true
	return config
}

func (a *Application) ticketAdminInlines() ([]admin.Inline, error) {
	if a.inlineAudit == nil {
		return nil, nil
	}
	binding, err := project.Bind()
	if err != nil {
		return nil, err
	}
	inline, err := admin.NewInline(admin.InlineConfig[models.Ticket, models.ServiceReport]{
		Project: binding, Parent: models.TicketObjects, Child: models.ServiceReportObjects,
		ParentWithID: models.NewTicketWithID, ForeignKey: "ticket", Label: "Service report",
		Form:        admin.FormConfig{Definition: formmodel.Definition{Fields: []string{"summary", "completed"}}},
		Set:         reportInlineConfig(),
		Permissions: admin.Permissions{View: ViewServiceReport, Add: AddServiceReport, Change: ChangeServiceReport, Delete: DeleteServiceReport},
		Load: func(ctx context.Context, actor auth.Principal, id int64) (admin.InlineSnapshot[models.ServiceReport], error) {
			current, err := a.adminReports(ctx, a.backend, id)
			return admin.InlineSnapshot[models.ServiceReport]{Current: current}, err
		},
		Validate: func(ctx context.Context, actor auth.Principal, set formmodel.InlineSet[models.Ticket, models.ServiceReport]) error {
			return a.checkAdminReports(ctx, a.backend, set)
		},
	})
	if err != nil {
		return nil, err
	}
	return []admin.Inline{inline}, nil
}

func (a *Application) adminReports(ctx context.Context, reader db.Queryer, id int64) ([]models.ServiceReport, error) {
	present, err := models.TicketObjects.Using(reader).Filter(models.TicketFields.ID.Exact(id), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).Exists(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, admin.ErrObjectNotFound
	}
	rows, err := models.ServiceReportObjects.Using(reader).Filter(a.relations.ModelsServiceReport.Ticket.ID.Exact(id), a.relations.ModelsServiceReport.Ticket.Category().ID.Exact(a.categoryID)).OrderBy(models.ServiceReportFields.ID.Asc()).Limit(2)
	if err != nil {
		return nil, err
	}
	current, err := rows.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(current) > 1 {
		return nil, errors.New("helpdesk: report OneToOne scope overflow")
	}
	return current, nil
}

func (a *Application) checkAdminReports(ctx context.Context, reader db.Queryer, set formmodel.InlineSet[models.Ticket, models.ServiceReport]) error {
	for _, row := range set.FormSet().Forms() {
		if row.DeletionRequested() || row.Form().ReadOnly() {
			continue
		}
		instance, found := set.Instance(row.Index())
		if !found {
			continue
		}
		current, existing, err := set.Current(row.Index())
		if err != nil {
			return err
		}
		var before *models.ServiceReport
		if existing {
			before = &current
		}
		failures, err := checkFormUnique(ctx, reader, models.ServiceReportObjects, before, instance.BoundForm())
		if err != nil {
			return err
		}
		if !failures.Empty() {
			return admin.RejectInline(reportInlinePrefix, row.Index(), failures, nil)
		}
	}
	return nil
}

type adminReportSet struct {
	set    formmodel.InlineSet[models.Ticket, models.ServiceReport]
	access admin.InlineAccess
}

func (a *Application) bindAdminReports(ctx context.Context, session db.RelationSession, actor auth.Principal, id int64, submission admin.InlineSubmission) (adminReportSet, bool, error) {
	data, access, present := submission.Lookup(reportInlinePrefix)
	if !present {
		return adminReportSet{}, false, nil
	}
	if a.inlineAudit == nil {
		return adminReportSet{}, false, errors.New("helpdesk: inline audit is missing")
	}
	for _, right := range []struct {
		enabled    bool
		permission auth.Permission
	}{{access.Add, AddServiceReport}, {access.Change, ChangeServiceReport}, {access.Delete, DeleteServiceReport}} {
		if right.enabled && !actor.Has(right.permission) {
			return adminReportSet{}, false, admin.NewOperationError(admin.OperationDenied, nil)
		}
	}
	if access.View && !actor.Has(ViewServiceReport) && !actor.Has(ChangeServiceReport) {
		return adminReportSet{}, false, admin.NewOperationError(admin.OperationDenied, nil)
	}
	var parent models.Ticket
	var current []models.ServiceReport
	var err error
	if id != 0 {
		parent = models.NewTicketWithID(id)
		if access.View || access.Change {
			current, err = a.adminReports(ctx, session, id)
			if err != nil {
				return adminReportSet{}, false, err
			}
		}
	}
	row, err := formmodel.NewSpecForFields(models.ServiceReportDescriptor{}.Metadata(), []string{"summary", "completed"})
	if err != nil {
		return adminReportSet{}, false, err
	}
	policy := reportInlineConfig()
	policy.ReadOnlyInitial = !access.Change
	policy.CanDelete = access.Delete
	if !access.Add {
		policy.ExtraForms = 0
	}
	rows, err := forms.NewSetSpec(row, policy)
	if err != nil {
		return adminReportSet{}, false, err
	}
	binding, err := project.Bind()
	if err != nil {
		return adminReportSet{}, false, err
	}
	spec, err := formmodel.NewInlineSpec(binding, models.TicketObjects, models.ServiceReportObjects, "ticket", rows)
	if err != nil {
		return adminReportSet{}, false, err
	}
	set, err := spec.Bind(data, parent, current, formmodel.PostClean{})
	if err != nil {
		return adminReportSet{}, false, err
	}
	for _, row := range set.FormSet().Forms() {
		if row.DeletionRequested() && !access.Delete || row.Index() >= set.FormSet().InitialForms() && len(row.Form().Changed()) > 0 && !access.Add {
			return adminReportSet{}, false, admin.NewOperationError(admin.OperationDenied, nil)
		}
	}
	if !set.Valid() {
		return adminReportSet{}, false, admin.RejectInline(reportInlinePrefix, -1, validation.NewErrors(validation.New(validation.NonField, "stale_inline")), nil)
	}
	// Database uniqueness runs before deletions, like the native ModelFormSet.
	// Replacing a deleted OneToOne row must not bypass that database check.
	if err := a.checkAdminReports(ctx, session, set); err != nil {
		return adminReportSet{}, false, err
	}
	return adminReportSet{set: set, access: access}, true, nil
}

func (a *Application) saveAdminReports(ctx context.Context, session db.RelationSession, actor auth.Principal, parent models.Ticket, input adminReportSet) (bool, error) {
	prepared, err := input.set.PrepareWithParent(parent)
	if err != nil {
		return false, err
	}
	changed := false
	for _, row := range prepared.Deleted() {
		current, err := row.Model()
		if err != nil {
			return false, err
		}
		if !input.access.Delete || !actor.Has(DeleteServiceReport) {
			return false, admin.NewOperationError(admin.OperationDenied, nil)
		}
		removed := current
		if _, err := models.ServiceReportObjects.Delete(ctx, session, &current); err != nil {
			return false, err
		}
		event, err := admin.PrepareEvent(actor.ID(), "helpdesk.service_report", removed.ID, admin.ActionDelete, nil, "Service report")
		if err != nil {
			return false, err
		}
		if err := a.inlineAudit(ctx, session, event); err != nil {
			return false, err
		}
		changed = true
	}
	for _, row := range prepared.Rows() {
		candidate, err := row.Model()
		if err != nil {
			return false, err
		}
		if row.Existing() && len(row.Changed()) == 0 {
			continue
		}
		permission, admitted := AddServiceReport, input.access.Add
		if row.Existing() {
			permission, admitted = ChangeServiceReport, input.access.Change
		}
		if !admitted || !actor.Has(permission) {
			return false, admin.NewOperationError(admin.OperationDenied, nil)
		}
		values, err := models.ServiceReportObjects.ModelValues(candidate)
		if err != nil {
			return false, err
		}
		delete(values, "id")
		current, existing, err := input.set.Current(row.Index())
		if err != nil {
			return false, err
		}
		var before *models.ServiceReport
		if existing {
			before = &current
		}
		failures, err := models.ServiceReportObjects.ValidateUniqueFields(ctx, session, values, before)
		if err != nil {
			return false, err
		}
		if !failures.Empty() {
			return false, admin.RejectInline(reportInlinePrefix, row.Index(), failures, nil)
		}
		if existing {
			err = models.ServiceReportObjects.Save(ctx, session, &candidate, orm.UpdateFieldNames[models.ServiceReport](row.Changed()...))
		} else {
			err = row.Prepared().Save(ctx, session, &candidate)
		}
		if err != nil {
			if failures, rejected := validation.Rejected(writeRejection(err)); rejected {
				return false, admin.RejectInline(reportInlinePrefix, row.Index(), failures, err)
			}
			return false, err
		}
		if _, err = a.publishableReport(ctx, session, candidate.ID); err != nil {
			return false, err
		}
		action := admin.ActionAdd
		if existing {
			action = admin.ActionChange
		}
		event, err := admin.PrepareEvent(actor.ID(), "helpdesk.service_report", candidate.ID, action, row.Changed(), "Service report")
		if err != nil {
			return false, err
		}
		if err = a.inlineAudit(ctx, session, event); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}
