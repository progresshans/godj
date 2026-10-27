package helpdesk

import (
	"context"
	"errors"
	"math"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

const (
	LabelUsagePath        = "/labels/usage/"
	labelUsagePageSize    = 20
	labelUsageMaximumPage = admin.MaximumListOffset/labelUsagePageSize + 1
)

type labelUsageQuery struct {
	page                          int
	usage                         string
	factor, delta                 decimal.Decimal
	elapsedFactor, elapsedDivisor decimal.Decimal
}
type labelUsageRow struct {
	id              int64
	name            string
	total, open     int64
	cost            orm.Optional[decimal.Decimal]
	adjusted        orm.Optional[decimal.Decimal]
	effort          orm.Optional[float64]
	elapsed         orm.Optional[duration.Duration]
	adjustedElapsed orm.Optional[duration.Duration]
}
type labelUsagePage struct {
	category models.Category
	query    labelUsageQuery
	labels   orm.ResultPage[labelUsageRow]
	found    bool
}

func (a *Application) readLabelUsage(ctx context.Context, actor auth.Principal, input labelUsageQuery) (labelUsagePage, error) {
	var zero labelUsagePage
	if !actor.Authenticated() || !actor.Has(ViewLabel) || !actor.Has(ViewTicket) {
		return zero, errors.New("helpdesk: label usage lacks admitted permissions")
	}
	value, err := readApplication(ctx, a, actor, requireAccess(ViewLabel, ViewTicket), func(work context.Context, reader db.Queryer, _ auth.Principal) (labelUsagePage, error) {
		return a.labelUsageInSnapshot(work, reader, input)
	})
	if err != nil {
		return zero, err
	}
	if !value.found {
		return zero, admin.ErrObjectNotFound
	}
	return value, nil
}
func (a *Application) labelUsageInSnapshot(ctx context.Context, reader db.Queryer, input labelUsageQuery) (labelUsagePage, error) {
	var zero labelUsagePage
	if interfaceNil(reader) {
		return zero, errors.New("helpdesk: nil label usage reader")
	}
	divisorSign, divisorErr := input.elapsedDivisor.Compare(decimal.Decimal{})
	if input.page < 1 || input.page > labelUsageMaximumPage || input.usage != "all" && input.usage != "used" && input.usage != "unused" || !input.factor.Fits(6, 4) || !input.delta.Fits(14, 2) || !input.elapsedFactor.Fits(6, 4) || !input.elapsedDivisor.Fits(6, 4) || divisorErr != nil || divisorSign <= 0 {
		return zero, errors.New("helpdesk: label usage input was not validated")
	}
	category, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Get(ctx)
	if errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
		return zero, nil
	}
	if err != nil {
		return zero, err
	}
	fields := models.LabelFields
	tickets := a.relations.ModelsLabel.Tickets
	// Label is the root so unused labels survive. Scope every aggregate to the
	// current Category: even a physical cross-category link cannot reveal a
	// ticket's presence, status or numeric values through this read surface.
	scope := tickets.Category().ID.Exact(a.categoryID)
	total := orm.Count(tickets.ID).Where(scope)
	opened := orm.Count(tickets.ID).Where(orm.And(scope, tickets.Closed.Exact(false)))
	cost, effort, elapsed := orm.Sum(tickets.ExpectedCost).Where(scope), orm.Avg(tickets.Effort).Where(scope), orm.Sum(tickets.Elapsed).Where(scope)
	adjusted := orm.Sum(orm.Decimal(tickets.ExpectedCost).Multiply(input.factor).Add(input.delta)).Where(scope)
	adjustedElapsed := orm.Sum(orm.DivideDuration(orm.ScaleDuration(tickets.Elapsed, input.elapsedFactor), input.elapsedDivisor)).Where(scope)
	metrics := orm.Aggregate6(total, opened, cost, effort, elapsed, adjusted, func(total, open int64, cost orm.Optional[decimal.Decimal], effort orm.Optional[float64], elapsed orm.Optional[duration.Duration], adjusted orm.Optional[decimal.Decimal]) labelUsageRow {
		return labelUsageRow{total: total, open: open, cost: cost, effort: effort, elapsed: elapsed, adjusted: adjusted}
	})
	metrics = orm.CombineAggregates(metrics, orm.Aggregate1(adjustedElapsed, func(value orm.Optional[duration.Duration]) orm.Optional[duration.Duration] { return value }), func(row labelUsageRow, elapsed orm.Optional[duration.Duration]) labelUsageRow {
		row.adjustedElapsed = elapsed
		return row
	})
	grouped, err := orm.GroupBy(models.LabelObjects.Using(reader).Filter(fields.CategoryID.Exact(a.categoryID)),
		orm.Project2(fields.ID, fields.Name, func(id int64, name string) labelUsageRow { return labelUsageRow{id: id, name: name} }),
		metrics, func(key, row labelUsageRow) labelUsageRow { row.id, row.name = key.id, key.name; return row })
	if err != nil {
		return zero, err
	}
	switch input.usage {
	case "used":
		grouped = grouped.Having(total.GreaterThan(0))
	case "unused":
		grouped = grouped.Having(total.Exact(0))
	}
	page, err := grouped.OrderBy(fields.ID.Asc()).Page(ctx, labelUsagePageSize, (input.page-1)*labelUsagePageSize)
	if err != nil {
		return zero, err
	}
	for _, row := range page.Rows {
		effort, present := row.effort.Get()
		if row.id <= 0 || row.total < 0 || row.open < 0 || row.open > row.total || row.cost.Valid() != row.adjusted.Valid() || row.elapsed.Valid() != row.adjustedElapsed.Valid() || present && (math.IsNaN(effort) || math.IsInf(effort, 0)) || row.total == 0 && (row.cost.Valid() || row.adjusted.Valid() || row.effort.Valid() || row.elapsed.Valid() || row.adjustedElapsed.Valid()) {
			return zero, errors.New("helpdesk: inconsistent label usage result")
		}
	}
	return labelUsagePage{category: category, query: input, labels: page, found: true}, nil
}
