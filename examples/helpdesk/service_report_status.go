package helpdesk

import (
	"context"
	"errors"
	"sync"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

const (
	ServiceReportStatusPath        = "/service-reports/status/"
	serviceReportStatusPageSize    = 20
	serviceReportStatusMaximumPage = admin.MaximumListOffset/serviceReportStatusPageSize + 1
)

type serviceReportStatusQuery struct {
	page       int
	comparison string
}
type serviceReportStatusRow struct {
	id, ticketID int64
	summary      string
	completed    bool
	ticketClosed *bool
	matched      orm.Optional[bool]
}
type serviceReportStatusPage struct {
	category models.Category
	query    serviceReportStatusQuery
	reports  orm.ResultPage[serviceReportStatusRow]
	found    bool
}

// The two statuses describe separate work. Comparison is read-only and does
// not infer an error, authorize a mutation, or load/repair full ticket records.
// Category, count and rows belong to one confirmed read-snapshot lifetime.
func (a *Application) readServiceReportStatus(ctx context.Context, actor auth.Principal, input serviceReportStatusQuery) (serviceReportStatusPage, error) {
	var zero serviceReportStatusPage
	if ctx == nil {
		return zero, errors.New("helpdesk: report status requires context")
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !actor.Authenticated() || !actor.Has(ViewServiceReport) || !actor.Has(ViewTicket) {
		return zero, errors.New("helpdesk: report status lacks admitted permissions")
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var result serviceReportStatusPage
	var callbackErr error
	entries, completed, sealed := 0, false, false
	err := a.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		mu.Lock()
		if sealed {
			mu.Unlock()
			return errors.New("helpdesk: report status callback outlived its owner")
		}
		entries++
		if entries != 1 {
			mu.Unlock()
			return errors.New("helpdesk: repeated report status callback")
		}
		mu.Unlock()
		value, failure := a.serviceReportStatusInSnapshot(work, reader, input)
		mu.Lock()
		result, callbackErr, completed = value, failure, true
		mu.Unlock()
		return failure
	})
	mu.Lock()
	sealed = true
	valid := entries <= 1 && (entries == 0 && err != nil || completed)
	value, failure := result, callbackErr
	mu.Unlock()
	cancel()
	if !valid || failure != nil && (err == nil || !errors.Is(err, failure)) {
		return zero, errors.Join(errors.New("helpdesk: invalid report status read ownership"), err, failure)
	}
	if err = errors.Join(err, ctx.Err()); err != nil {
		return zero, err
	}
	if !value.found {
		return zero, admin.ErrObjectNotFound
	}
	return value, nil
}

func (a *Application) serviceReportStatusInSnapshot(ctx context.Context, reader db.Queryer, input serviceReportStatusQuery) (serviceReportStatusPage, error) {
	var zero serviceReportStatusPage
	if nilFormReader(reader) {
		return zero, errors.New("helpdesk: nil report status reader")
	}
	if input.page < 1 || input.page > serviceReportStatusMaximumPage || input.comparison != "all" && input.comparison != "same" && input.comparison != "different" {
		return zero, errors.New("helpdesk: report status input was not validated")
	}
	category, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Get(ctx)
	if errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
		return zero, nil
	}
	if err != nil {
		return zero, err
	}
	fields := models.ServiceReportFields
	ticket := a.relations.ModelsServiceReport.Ticket
	same := orm.ExactExpressions(orm.F(fields.Completed), ticket.Closed)
	matched := orm.Case(orm.Value[models.ServiceReport](false), orm.When(same, orm.Value[models.ServiceReport](true)))
	rows := models.ServiceReportObjects.Using(reader).Filter(ticket.Category().ID.Exact(a.categoryID)).OrderBy(fields.ID.Asc())
	switch input.comparison {
	case "same":
		rows = rows.Filter(same)
	case "different":
		rows = rows.Filter(orm.Not(same))
	}
	projection := orm.CombineProjections(
		orm.Project5(fields.ID, fields.TicketID, fields.Summary, fields.Completed, ticket.Closed,
			func(id, ticketID int64, summary string, completed bool, closed *bool) serviceReportStatusRow {
				return serviceReportStatusRow{id: id, ticketID: ticketID, summary: summary, completed: completed, ticketClosed: closed}
			}),
		orm.Project1(matched, func(value orm.Optional[bool]) orm.Optional[bool] { return value }),
		func(row serviceReportStatusRow, matched orm.Optional[bool]) serviceReportStatusRow {
			row.matched = matched
			return row
		},
	)
	selected, err := orm.SelectQuery(rows, projection)
	if err != nil {
		return zero, err
	}
	page, err := selected.Page(ctx, serviceReportStatusPageSize, (input.page-1)*serviceReportStatusPageSize)
	if err != nil {
		return zero, err
	}
	for _, row := range page.Rows {
		matched, present := row.matched.Get()
		if row.id <= 0 || row.ticketID <= 0 || row.ticketClosed == nil || !present || matched != (row.completed == *row.ticketClosed) {
			return zero, errors.New("helpdesk: inconsistent report status result")
		}
	}
	return serviceReportStatusPage{category: category, query: input, reports: page, found: true}, nil
}
