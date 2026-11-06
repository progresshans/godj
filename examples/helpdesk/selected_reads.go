package helpdesk

import (
	"context"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/orm"
)

type businessLookup[T any] struct {
	value T
	found bool
}

func (a *Application) modelsForRead(scope db.Queryer) (project.Models, error) {
	if a.selected == nil {
		return a.objects, nil
	}
	return project.UsingReadScope(scope)
}

func readBusinessModel[T any](ctx context.Context, a *Application, actor auth.Principal, policy accessPolicy, read func(context.Context, db.Queryer) (T, bool, error)) (T, bool, error) {
	result, err := readBusiness(ctx, a, actor, policy, func(work context.Context, scope db.Queryer, _ auth.Principal) (businessLookup[T], error) {
		value, found, err := read(work, scope)
		return businessLookup[T]{value, found}, err
	})
	return result.value, result.found, err
}

func (a *Application) listCategories(ctx context.Context, actor auth.Principal, request admin.ListRequest) (admin.Page[models.Category], error) {
	return readBusiness(ctx, a, actor, requireAccess(ViewCategory), func(work context.Context, scope db.Queryer, _ auth.Principal) (admin.Page[models.Category], error) {
		var filters []orm.Predicate[models.Category]
		if request.Search != "" {
			filters = append(filters, models.CategoryFields.Name.IContains(request.Search))
		}
		page, err := a.resources.category.List(work, scope, crud.ListOptions[models.Category]{Filter: filters, Offset: request.Offset, Limit: request.Limit})
		if err != nil {
			return admin.Page[models.Category]{}, err
		}
		return admin.Page[models.Category]{Items: page.Items, Total: page.Total, Offset: page.Offset, Limit: page.Limit}, nil
	})
}

func (a *Application) getTicket(ctx context.Context, actor auth.Principal, id int64) (ticketRecord, bool, error) {
	return readBusinessModel(ctx, a, actor, a.readPolicy(ViewTicket, ChangeTicket), func(work context.Context, scope db.Queryer) (ticketRecord, bool, error) {
		objects, err := a.modelsForRead(scope)
		if err != nil {
			return ticketRecord{}, false, err
		}
		return a.readTicketRecord(work, objects, id)
	})
}

func (a *Application) getReport(ctx context.Context, actor auth.Principal, id int64) (models.ServiceReport, bool, error) {
	return readBusinessModel(ctx, a, actor, a.readPolicy(ViewServiceReport, ChangeServiceReport), func(work context.Context, scope db.Queryer) (models.ServiceReport, bool, error) {
		return a.report(work, scope, id)
	})
}

func (a *Application) getLabel(ctx context.Context, actor auth.Principal, id int64) (models.Label, bool, error) {
	return readBusinessModel(ctx, a, actor, a.readPolicy(ViewLabel, ChangeLabel), func(work context.Context, scope db.Queryer) (models.Label, bool, error) {
		return a.label(work, scope, id)
	})
}

func (a *Application) getTicketLabel(ctx context.Context, actor auth.Principal, id int64) (models.TicketLabel, bool, error) {
	return readBusinessModel(ctx, a, actor, a.readPolicy(ViewTicketLabel, ChangeTicketLabel), func(work context.Context, scope db.Queryer) (models.TicketLabel, bool, error) {
		return a.ticketLabel(work, scope, id)
	})
}

func (a *Application) readTicketDetail(ctx context.Context, actor auth.Principal, id int64) (ticketDetailResponse, bool, error) {
	return readBusinessModel(ctx, a, actor, requireAccess(ViewTicket), func(work context.Context, scope db.Queryer) (ticketDetailResponse, bool, error) {
		objects, err := a.modelsForRead(scope)
		if err != nil {
			return ticketDetailResponse{}, false, err
		}
		selected, found, err := a.withTicketLabels(objects.ModelsTicket.Filter(models.TicketFields.ID.Exact(id), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).OrderBy(models.TicketFields.ID.Asc())).SelectRelated(objects.ModelsTicket.Related.Category).First(work)
		if err != nil || !found {
			return ticketDetailResponse{}, found, err
		}
		category, err := selected.Category(work)
		if err != nil {
			return ticketDetailResponse{}, false, err
		}
		raw, err := ticketRecordFromModel(work, selected)
		if err != nil {
			return ticketDetailResponse{}, false, err
		}
		return ticketDetailResponse{ticket: raw, category: models.Category{ID: category.ID, Name: category.Name}}, true, nil
	})
}

func (a *Application) readTicketReport(ctx context.Context, actor auth.Principal, id int64) (*models.ServiceReport, bool, error) {
	return readBusinessModel(ctx, a, actor, requireAccess(ViewServiceReport), func(work context.Context, scope db.Queryer) (*models.ServiceReport, bool, error) {
		objects, err := a.modelsForRead(scope)
		if err != nil {
			return nil, false, err
		}
		owner, found, err := objects.ModelsTicket.Filter(models.TicketFields.ID.Exact(id), a.relations.ModelsTicket.Category.ID.Exact(a.categoryID)).OrderBy(models.TicketFields.ID.Asc()).SelectRelated(objects.ModelsTicket.Related.ServiceReport).First(work)
		if err != nil || !found {
			return nil, found, err
		}
		child, present, err := owner.ServiceReport(work)
		if err != nil || !present {
			return nil, true, err
		}
		raw, err := child.Unwrap()
		return &raw, true, err
	})
}
