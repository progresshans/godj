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
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

const (
	DeadlineCalendarPath        = "/tickets/deadline-calendar/"
	deadlineCalendarPageSize    = 20
	deadlineCalendarMaximumPage = admin.MaximumListOffset/deadlineCalendarPageSize + 1
)

type deadlineCalendarQuery struct {
	page        int
	asOf        time.Time
	from, until *calendar.Date
}

func (input deadlineCalendarQuery) validRange() bool {
	return (input.from == nil || input.from.Valid()) && (input.until == nil || input.until.Valid()) &&
		(input.from == nil || input.until == nil || input.from.String() <= input.until.String())
}

type deadlineCalendarRow struct {
	day                  orm.Optional[calendar.Date]
	total, open, overdue int64
	first, last          orm.Optional[clock.Time]
}

type deadlineCalendarPage struct {
	category models.Category
	query    deadlineCalendarQuery
	groups   orm.ResultPage[deadlineCalendarRow]
	found    bool
}

func (a *Application) readDeadlineCalendar(ctx context.Context, actor auth.Principal, input deadlineCalendarQuery) (deadlineCalendarPage, error) {
	var zero deadlineCalendarPage
	if !actor.Authenticated() || !actor.Has(ViewTicket) {
		return zero, errors.New("helpdesk: deadline calendar lacks admitted permission")
	}
	reference, err := temporal.Canonical(input.asOf)
	if err != nil || reference != input.asOf || input.page < 1 || input.page > deadlineCalendarMaximumPage || !input.validRange() {
		return zero, errors.New("helpdesk: deadline calendar input was not validated")
	}
	value, err := readSnapshotValue(ctx, a.backend, func(work context.Context, reader db.Queryer) (deadlineCalendarPage, error) {
		if nilFormReader(reader) {
			return zero, errors.New("helpdesk: nil deadline calendar reader")
		}
		category, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Get(work)
		if errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			return zero, nil
		}
		if err != nil {
			return zero, err
		}
		fields := models.TicketFields
		instant := orm.DateTime(orm.F(fields.DueAt))
		day, wall := instant.Date(), instant.Time()
		key := orm.GroupKey(day)
		count := orm.CountRows[models.Ticket]()
		opened := orm.Count(fields.ID).Where(fields.Closed.Exact(false))
		late := orm.Count(fields.ID).Where(orm.And(fields.Closed.Exact(false), fields.DueAt.LessThan(input.asOf)))
		grouped, err := orm.GroupBy(models.TicketObjects.Using(reader).Filter(fields.CategoryID.Exact(a.categoryID)),
			orm.Project1(day, func(day orm.Optional[calendar.Date]) orm.Optional[calendar.Date] { return day }),
			orm.Aggregate5(count, opened, late, orm.Min(wall), orm.Max(wall),
				func(total, open, overdue int64, first, last orm.Optional[clock.Time]) deadlineCalendarRow {
					return deadlineCalendarRow{total: total, open: open, overdue: overdue, first: first, last: last}
				}),
			func(day orm.Optional[calendar.Date], row deadlineCalendarRow) deadlineCalendarRow {
				row.day = day
				return row
			})
		if err != nil {
			return zero, err
		}
		if input.from != nil {
			grouped = grouped.Having(key.GreaterThanOrEqual(orm.Some(*input.from)))
		}
		if input.until != nil {
			grouped = grouped.Having(key.LessThanOrEqual(orm.Some(*input.until)))
		}
		groups, err := grouped.OrderBy(key.Asc().NullsLast()).Page(work, deadlineCalendarPageSize, (input.page-1)*deadlineCalendarPageSize)
		if err != nil {
			return zero, err
		}
		for _, row := range groups.Rows {
			day, dated := row.day.Get()
			first, firstPresent := row.first.Get()
			last, lastPresent := row.last.Get()
			if row.total <= 0 || row.open < 0 || row.open > row.total || row.overdue < 0 || row.overdue > row.open ||
				dated != firstPresent || dated != lastPresent ||
				dated && (!day.Valid() || !first.Valid() || !last.Valid() || first.String() > last.String()) ||
				!dated && (row.overdue != 0 || input.from != nil || input.until != nil) ||
				dated && (input.from != nil && day.String() < input.from.String() || input.until != nil && day.String() > input.until.String()) {
				return zero, errors.New("helpdesk: inconsistent deadline calendar result")
			}
		}
		return deadlineCalendarPage{category: category, query: input, groups: groups, found: true}, nil
	})
	if err != nil {
		return zero, err
	}
	if !value.found {
		return zero, admin.ErrObjectNotFound
	}
	return value, nil
}
