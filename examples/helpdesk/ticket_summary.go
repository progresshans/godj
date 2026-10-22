package helpdesk

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"

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
	page                   int
	minimumOpen            int64
	minimumOpenBasisPoints int64
}

type ticketSummaryRow struct {
	priority                *int64
	raiseTo                 int64
	total, open             int64
	closed, openBasisPoints int64
	cost                    orm.Optional[decimal.Decimal]
	effort                  orm.Optional[float64]
	elapsed                 orm.Optional[duration.Duration]
}
type ticketSummaryKey struct {
	priority *int64
	raiseTo  orm.Optional[int64]
}
type ticketSummaryComputedRow struct {
	key                     ticketSummaryKey
	metrics                 ticketSummaryRow
	closed, openBasisPoints orm.Optional[int64]
}
type ticketSummaryPage struct {
	category models.Category
	found    bool
	query    ticketSummaryQuery
	groups   orm.ResultPage[ticketSummaryRow]
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
	value, err := readSnapshotValue(ctx, a.backend, func(work context.Context, reader db.Queryer) (ticketSummaryPage, error) {
		return a.ticketSummaryInSnapshot(work, reader, input)
	})
	if err != nil {
		return zero, err
	}
	if !value.found {
		return zero, admin.ErrObjectNotFound
	}
	return value, nil
}

func (a *Application) ticketSummaryInSnapshot(ctx context.Context, reader db.Queryer, input ticketSummaryQuery) (ticketSummaryPage, error) {
	var result ticketSummaryPage
	if interfaceNil(reader) {
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
	count, cost, effort, elapsed := orm.CountRows[models.Ticket](), orm.Sum(fields.ExpectedCost), orm.Avg(fields.Effort), orm.Sum(fields.Elapsed)
	grouped, err := orm.GroupBy(models.TicketObjects.Using(reader).Filter(fields.CategoryID.Exact(a.categoryID)),
		orm.Project2(fields.Priority, raised, func(value *int64, raised orm.Optional[int64]) ticketSummaryKey {
			return ticketSummaryKey{priority: value, raiseTo: raised}
		}),
		orm.Aggregate5(count, opened, cost, effort, elapsed,
			func(total, open int64, cost orm.Optional[decimal.Decimal], effort orm.Optional[float64], elapsed orm.Optional[duration.Duration]) ticketSummaryRow {
				return ticketSummaryRow{total: total, open: open, cost: cost, effort: effort, elapsed: elapsed}
			}),
		func(key ticketSummaryKey, row ticketSummaryRow) ticketSummaryComputedRow {
			return ticketSummaryComputedRow{key: key, metrics: row}
		})
	if err != nil {
		return result, err
	}
	source, err := orm.Derive(grouped.Having(opened.GreaterThanOrEqual(input.minimumOpen)))
	if err != nil {
		return result, err
	}
	priority, raiseTo := orm.SelectedField(source, fields.Priority), orm.SelectedField(source, raised)
	total, open := orm.SelectedAggregate(source, count), orm.SelectedAggregate(source, opened)
	closed := orm.SubtractExpressions(total, open)
	rate := orm.Case(orm.Value[orm.ResultOf[models.Ticket]](int64(0)),
		orm.When(total.GreaterThan(0), orm.DivideExpressions(orm.Multiply(open, int64(10000)), total)))
	keys := orm.Project2(priority, raiseTo, func(priority *int64, raiseTo orm.Optional[int64]) ticketSummaryKey {
		return ticketSummaryKey{priority: priority, raiseTo: raiseTo}
	})
	metrics := orm.Project5(total, open, orm.SelectedAggregate(source, cost), orm.SelectedAggregate(source, effort), orm.SelectedAggregate(source, elapsed),
		func(total, open int64, cost orm.Optional[decimal.Decimal], effort orm.Optional[float64], elapsed orm.Optional[duration.Duration]) ticketSummaryRow {
			return ticketSummaryRow{total: total, open: open, cost: cost, effort: effort, elapsed: elapsed}
		})
	base := orm.CombineProjections(keys, metrics, func(key ticketSummaryKey, metrics ticketSummaryRow) ticketSummaryComputedRow {
		return ticketSummaryComputedRow{key: key, metrics: metrics}
	})
	derived := orm.Project2(closed, rate, func(closed, rate orm.Optional[int64]) [2]orm.Optional[int64] {
		return [2]orm.Optional[int64]{closed, rate}
	})
	projection := orm.CombineProjections(base, derived, func(row ticketSummaryComputedRow, derived [2]orm.Optional[int64]) ticketSummaryComputedRow {
		row.closed, row.openBasisPoints = derived[0], derived[1]
		return row
	})
	selected, err := orm.SelectResults(source, projection)
	if err != nil {
		return result, err
	}
	groups, err := selected.Filter(rate.GreaterThanOrEqual(input.minimumOpenBasisPoints)).OrderBy(open.Desc(), priority.Desc().NullsLast()).Page(ctx, ticketSummaryPageSize, (input.page-1)*ticketSummaryPageSize)
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
		closed, closedPresent := value.closed.Get()
		rate, ratePresent := value.openBasisPoints.Get()
		if !closedPresent || !ratePresent || row.total <= 0 || row.open < 0 || row.open > row.total || closed != row.total-row.open || rate < 0 || rate > 10000 {
			return result, errors.New("helpdesk: invalid derived ticket metrics")
		}
		// Verify the public integer-ratio policy without replacing the DB's
		// derived value or weakening its native overflow/error boundary.
		var expectedRate big.Int
		expectedRate.Mul(big.NewInt(row.open), big.NewInt(10000))
		expectedRate.Quo(&expectedRate, big.NewInt(row.total))
		if rate != expectedRate.Int64() {
			return result, errors.New("helpdesk: open ratio differs from the summary policy")
		}
		row.closed, row.openBasisPoints = closed, rate
		row.priority, row.raiseTo = value.key.priority, raised
		rows[index] = row
	}
	return ticketSummaryPage{category: category, found: true, query: input, groups: orm.ResultPage[ticketSummaryRow]{Total: groups.Total, Rows: rows}}, nil
}

func ticketSummaryOpenRateLabel(basisPoints int64) string {
	return fmt.Sprintf("%d.%02d%%", basisPoints/100, basisPoints%100)
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
