package helpdesk

import (
	"context"
	"errors"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

const (
	ServicePreviewPath        = "/tickets/service-preview/"
	servicePreviewPageSize    = 20
	servicePreviewMaximumPage = admin.MaximumListOffset/servicePreviewPageSize + 1
)

type servicePreviewQuery struct {
	page    int
	shift   duration.Duration
	minimum *time.Time
}
type servicePreviewRow struct {
	id                      int64
	subject                 string
	day                     *calendar.Date
	clock                   *clock.Time
	elapsed                 *duration.Duration
	due                     *time.Time
	start, adjusted, finish orm.Optional[time.Time]
	slack                   orm.Optional[duration.Duration]
}
type servicePreviewPage struct {
	category models.Category
	query    servicePreviewQuery
	tickets  orm.ResultPage[servicePreviewRow]
	found    bool
}

func (a *Application) readServicePreview(ctx context.Context, actor auth.Principal, input servicePreviewQuery) (servicePreviewPage, error) {
	var zero servicePreviewPage
	if !actor.Authenticated() || !actor.Has(ViewTicket) {
		return zero, errors.New("helpdesk: service preview lacks admitted permission")
	}
	if input.page < 1 || input.page > servicePreviewMaximumPage || !input.shift.Valid() {
		return zero, errors.New("helpdesk: service preview input was not validated")
	}
	if input.minimum != nil {
		canonical, err := temporal.Canonical(*input.minimum)
		if err != nil || canonical != *input.minimum {
			return zero, errors.New("helpdesk: service preview minimum was not validated")
		}
	}
	value, err := readSnapshotValue(ctx, a.backend, func(work context.Context, reader db.Queryer) (servicePreviewPage, error) {
		if nilFormReader(reader) {
			return zero, errors.New("helpdesk: nil service preview reader")
		}
		category, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Get(work)
		if errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			return zero, nil
		}
		if err != nil {
			return zero, err
		}
		fields := models.TicketFields
		start := orm.Date(orm.F(fields.ServiceOn)).AddExpression(orm.Time(orm.F(fields.ServiceAt)).Difference(clock.Time{}))
		adjusted := start.Add(input.shift)
		finish := adjusted.AddExpression(orm.F(fields.Elapsed))
		slack := orm.DateTime(orm.F(fields.DueAt)).DifferenceExpression(finish)
		source := models.TicketObjects.Using(reader).Filter(fields.CategoryID.Exact(a.categoryID))
		if input.minimum != nil {
			source = source.Filter(adjusted.GreaterThanOrEqual(*input.minimum))
		}
		original := orm.Project5(fields.ID, fields.Subject, fields.ServiceOn, fields.ServiceAt, fields.Elapsed,
			func(id int64, subject string, day *calendar.Date, wall *clock.Time, elapsed *duration.Duration) servicePreviewRow {
				return servicePreviewRow{id: id, subject: subject, day: day, clock: wall, elapsed: elapsed}
			})
		computed := orm.Project5(fields.DueAt, start, adjusted, finish, slack,
			func(due *time.Time, start, adjusted, finish orm.Optional[time.Time], slack orm.Optional[duration.Duration]) servicePreviewRow {
				return servicePreviewRow{due: due, start: start, adjusted: adjusted, finish: finish, slack: slack}
			})
		projection := orm.CombineProjections(original, computed, func(original, computed servicePreviewRow) servicePreviewRow {
			original.due, original.start, original.adjusted, original.finish, original.slack = computed.due, computed.start, computed.adjusted, computed.finish, computed.slack
			return original
		})
		selected, err := orm.SelectQuery(source.OrderBy(adjusted.Asc().NullsLast(), fields.ID.Asc()), projection)
		if err != nil {
			return zero, err
		}
		page, err := selected.Page(work, servicePreviewPageSize, (input.page-1)*servicePreviewPageSize)
		if err != nil {
			return zero, err
		}
		for _, row := range page.Rows {
			hasStart := row.day != nil && row.clock != nil
			hasFinish := hasStart && row.elapsed != nil
			if row.id <= 0 || row.start.Valid() != hasStart || row.adjusted.Valid() != hasStart || row.finish.Valid() != hasFinish || row.slack.Valid() != (hasFinish && row.due != nil) {
				return zero, errors.New("helpdesk: inconsistent service preview result")
			}
		}
		return servicePreviewPage{category: category, query: input, tickets: page, found: true}, nil
	})
	if err != nil {
		return zero, err
	}
	if !value.found {
		return zero, admin.ErrObjectNotFound
	}
	return value, nil
}
