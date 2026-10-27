package helpdesk

import (
	"context"
	"errors"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/timezone"
)

const (
	DeadlineCalendarPath        = "/tickets/deadline-calendar/"
	deadlineCalendarPageSize    = 20
	deadlineCalendarMaximumPage = admin.MaximumListOffset/deadlineCalendarPageSize + 1
)

type deadlineCalendarQuery struct {
	page        int
	period      string
	zone        timezone.Zone
	asOf        time.Time
	from, until *calendar.Date
}

func (input deadlineCalendarQuery) validRange() bool {
	return (input.from == nil || input.from.Valid()) && (input.until == nil || input.until.Valid()) &&
		(input.from == nil || input.until == nil || input.from.String() <= input.until.String())
}

type deadlineCalendarRow struct {
	start                orm.Optional[calendar.Date]
	total, open, overdue int64
	first, last          orm.Optional[time.Time]
}

func deadlineCalendarPeriods() []string { return []string{"day", "week", "month", "quarter", "year"} }

func deadlineCalendarKey(period string, day orm.DateExpression[models.Ticket]) (orm.DateExpression[models.Ticket], error) {
	switch period {
	case "day":
		return day, nil
	case "week":
		return day.TruncWeek(), nil
	case "month":
		return day.TruncMonth(), nil
	case "quarter":
		return day.TruncQuarter(), nil
	case "year":
		return day.TruncYear(), nil
	default:
		return orm.DateExpression[models.Ticket]{}, errors.New("helpdesk: deadline calendar period was not validated")
	}
}

// These UTC-shaped values represent civil dates, not real instants. The end
// may be year 10000 as an exclusive sentinel. Local midnight need not exist and
// a day need not last 24 hours. The database owns grouping and pagination;
// this only validates returned rows and never regroups a partial result.
func deadlineCalendarDateBounds(period string, start calendar.Date) (time.Time, time.Time, bool) {
	if !start.Valid() {
		return time.Time{}, time.Time{}, false
	}
	first := time.Date(start.Year, start.Month, start.Day, 0, 0, 0, 0, time.UTC)
	switch period {
	case "day":
		return first, first.AddDate(0, 0, 1), true
	case "week":
		return first, first.AddDate(0, 0, 7), first.Weekday() == time.Monday
	case "month":
		return first, first.AddDate(0, 1, 0), start.Day == 1
	case "quarter":
		return first, first.AddDate(0, 3, 0), start.Day == 1 && (start.Month-1)%3 == 0
	case "year":
		return first, first.AddDate(1, 0, 0), start.Day == 1 && start.Month == time.January
	default:
		return time.Time{}, time.Time{}, false
	}
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
	location, err := input.zone.Location()
	if err != nil {
		return zero, errors.New("helpdesk: deadline calendar timezone was not validated")
	}
	fields := models.TicketFields
	instant := orm.DateTime(orm.F(fields.DueAt))
	periodStart, err := deadlineCalendarKey(input.period, instant.InZone(input.zone).Date())
	if err != nil {
		return zero, err
	}
	value, err := readApplication(ctx, a, actor, requireAccess(ViewTicket), func(work context.Context, reader db.Queryer, _ auth.Principal) (deadlineCalendarPage, error) {
		if interfaceNil(reader) {
			return zero, errors.New("helpdesk: nil deadline calendar reader")
		}
		category, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Get(work)
		if errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			return zero, nil
		}
		if err != nil {
			return zero, err
		}
		key := orm.GroupKey(periodStart)
		count := orm.CountRows[models.Ticket]()
		opened := orm.Count(fields.ID).Where(fields.Closed.Exact(false))
		late := orm.Count(fields.ID).Where(orm.And(fields.Closed.Exact(false), fields.DueAt.LessThan(input.asOf)))
		grouped, err := orm.GroupBy(models.TicketObjects.Using(reader).Filter(fields.CategoryID.Exact(a.categoryID)),
			orm.Project1(periodStart, func(start orm.Optional[calendar.Date]) orm.Optional[calendar.Date] { return start }),
			orm.Aggregate5(count, opened, late, orm.Min(instant), orm.Max(instant),
				func(total, open, overdue int64, first, last orm.Optional[time.Time]) deadlineCalendarRow {
					return deadlineCalendarRow{total: total, open: open, overdue: overdue, first: first, last: last}
				}),
			func(start orm.Optional[calendar.Date], row deadlineCalendarRow) deadlineCalendarRow {
				row.start = start
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
			start, dated := row.start.Get()
			first, firstPresent := row.first.Get()
			last, lastPresent := row.last.Get()
			if row.total <= 0 || row.open < 0 || row.open > row.total || row.overdue < 0 || row.overdue > row.open ||
				dated != firstPresent || dated != lastPresent ||
				!dated && (row.overdue != 0 || input.from != nil || input.until != nil) ||
				dated && (input.from != nil && start.String() < input.from.String() || input.until != nil && start.String() > input.until.String()) {
				return zero, errors.New("helpdesk: inconsistent deadline calendar result")
			}
			if dated {
				begin, end, aligned := deadlineCalendarDateBounds(input.period, start)
				canonicalFirst, firstError := temporal.Canonical(first)
				canonicalLast, lastError := temporal.Canonical(last)
				localDate := func(instant time.Time) (time.Time, bool) {
					local := instant.In(location)
					day := calendar.Date{Year: local.Year(), Month: local.Month(), Day: local.Day()}
					return time.Date(day.Year, day.Month, day.Day, 0, 0, 0, 0, time.UTC), day.Valid()
				}
				firstDay, firstValid := localDate(first)
				lastDay, lastValid := localDate(last)
				if !aligned || firstError != nil || lastError != nil || canonicalFirst != first || canonicalLast != last ||
					!firstValid || !lastValid || last.Before(first) || firstDay.Before(begin) || !firstDay.Before(end) || lastDay.Before(begin) || !lastDay.Before(end) {
					return zero, errors.New("helpdesk: deadline instants do not belong to their calendar period")
				}
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
