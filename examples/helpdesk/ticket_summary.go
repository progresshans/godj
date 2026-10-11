package helpdesk

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

const (
	TicketSummaryPath        = "/tickets/summary/"
	ticketSummaryPageSize    = 20
	ticketSummaryMaximumPage = admin.MaximumListOffset/ticketSummaryPageSize + 1
)

type ticketSummaryQuery struct {
	page        int
	minimumOpen int64
}

type ticketSummaryRow struct {
	priority    *int64
	raiseTo     int64
	total, open int64
	cost        orm.Optional[decimal.Decimal]
	effort      orm.Optional[float64]
	elapsed     orm.Optional[duration.Duration]
}
type ticketSummaryKey struct {
	priority *int64
	raiseTo  orm.Optional[int64]
}
type ticketSummaryComputedRow struct {
	key     ticketSummaryKey
	metrics ticketSummaryRow
}
type ticketSummaryPage struct {
	category models.Category
	found    bool
	query    ticketSummaryQuery
	groups   orm.GroupPage[ticketSummaryRow]
}

// All observations, including category existence/name and the single-statement
// group page, use one borrowed read snapshot. Nothing is published until its
// owner confirms cleanup. Reads never load ticket bodies, labels or digests.
func (a *Application) readTicketSummary(ctx context.Context, input ticketSummaryQuery) (ticketSummaryPage, error) {
	var zero ticketSummaryPage
	if ctx == nil {
		return zero, errors.New("helpdesk: ticket summary requires context")
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var result ticketSummaryPage
	var callbackErr error
	entries, completed, sealed := 0, false, false
	err := a.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		mu.Lock()
		if sealed {
			mu.Unlock()
			return errors.New("helpdesk: ticket summary callback outlived its owner")
		}
		entries++
		if entries != 1 {
			mu.Unlock()
			return errors.New("helpdesk: repeated ticket summary callback")
		}
		mu.Unlock()
		value, failure := a.ticketSummaryInSnapshot(work, reader, input)
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
		return zero, errors.Join(errors.New("helpdesk: invalid ticket summary read ownership"), err, failure)
	}
	if err = errors.Join(err, ctx.Err()); err != nil {
		return zero, err
	}
	if !value.found {
		return zero, admin.ErrObjectNotFound
	}
	return value, nil
}

func (a *Application) ticketSummaryInSnapshot(ctx context.Context, reader db.Queryer, input ticketSummaryQuery) (ticketSummaryPage, error) {
	var result ticketSummaryPage
	if nilFormReader(reader) {
		return result, errors.New("helpdesk: nil ticket summary reader")
	}
	category, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Get(ctx)
	if errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	fields := models.TicketFields
	raised := raisedPriorityExpression()
	opened := orm.Count(fields.ID).Where(fields.Closed.Exact(false))
	grouped, err := orm.GroupBy(models.TicketObjects.Using(reader).Filter(fields.CategoryID.Exact(a.categoryID)),
		orm.Project2(fields.Priority, raised, func(value *int64, raised orm.Optional[int64]) ticketSummaryKey {
			return ticketSummaryKey{priority: value, raiseTo: raised}
		}),
		orm.Aggregate5(orm.CountRows[models.Ticket](), opened, orm.Sum(fields.ExpectedCost), orm.Avg(fields.Effort), orm.Sum(fields.Elapsed),
			func(total, open int64, cost orm.Optional[decimal.Decimal], effort orm.Optional[float64], elapsed orm.Optional[duration.Duration]) ticketSummaryRow {
				return ticketSummaryRow{total: total, open: open, cost: cost, effort: effort, elapsed: elapsed}
			}),
		func(key ticketSummaryKey, row ticketSummaryRow) ticketSummaryComputedRow {
			return ticketSummaryComputedRow{key: key, metrics: row}
		})
	if err != nil {
		return result, err
	}
	groups, err := grouped.Having(opened.GreaterThanOrEqual(input.minimumOpen)).OrderBy(opened.Desc(), orm.GroupKey(fields.Priority).Desc().NullsLast()).Page(ctx, ticketSummaryPageSize, (input.page-1)*ticketSummaryPageSize)
	if err != nil {
		return result, err
	}
	rows := make([]ticketSummaryRow, len(groups.Rows))
	for index, value := range groups.Rows {
		raised, present := value.key.raiseTo.Get()
		expected, _ := raisedTicketPriority(value.key.priority)
		if !present || raised != expected {
			return result, errors.New("helpdesk: priority preview differs from the command policy")
		}
		row := value.metrics
		row.priority, row.raiseTo = value.key.priority, raised
		rows[index] = row
	}
	return ticketSummaryPage{category: category, found: true, query: input, groups: orm.GroupPage[ticketSummaryRow]{Total: groups.Total, Rows: rows}}, nil
}

func summaryOptionalPointer[V any](value orm.Optional[V]) *V {
	if present, valid := value.Get(); valid {
		return &present
	}
	return nil
}

func ticketSummaryPriorityLabel(value *int64) string {
	if value == nil {
		return "Not set"
	}
	for _, field := range (models.TicketDescriptor{}).Metadata().Fields {
		if field.Name == "priority" {
			for _, choice := range field.Choices {
				if choice.Value.Integer == *value {
					return choice.Label
				}
			}
			break
		}
	}
	return "Other (" + strconv.FormatInt(*value, 10) + ")"
}
