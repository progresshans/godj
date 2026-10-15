package helpdesk

import (
	"context"
	"errors"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

const (
	CostPreviewPath        = "/tickets/cost-preview/"
	costPreviewPageSize    = 20
	costPreviewMaximumPage = admin.MaximumListOffset/costPreviewPageSize + 1
)

type costPreviewQuery struct {
	page          int
	factor, delta decimal.Decimal
	minimum       *decimal.Decimal
}
type costPreviewRow struct {
	id       int64
	subject  string
	original *decimal.Decimal
	adjusted orm.Optional[decimal.Decimal]
}
type costPreviewPage struct {
	category models.Category
	query    costPreviewQuery
	tickets  orm.ResultPage[costPreviewRow]
	found    bool
}

func (a *Application) readCostPreview(ctx context.Context, actor auth.Principal, input costPreviewQuery) (costPreviewPage, error) {
	var zero costPreviewPage
	if !actor.Authenticated() || !actor.Has(ViewTicket) {
		return zero, errors.New("helpdesk: cost preview lacks admitted permission")
	}
	if input.page < 1 || input.page > costPreviewMaximumPage || !input.factor.Fits(6, 4) || !input.delta.Fits(14, 2) || input.minimum != nil && !input.minimum.Fits(21, 6) {
		return zero, errors.New("helpdesk: cost preview input was not validated")
	}
	value, err := readSnapshotValue(ctx, a.backend, func(work context.Context, reader db.Queryer) (costPreviewPage, error) {
		if nilFormReader(reader) {
			return zero, errors.New("helpdesk: nil cost preview reader")
		}
		category, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Get(work)
		if errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			return zero, nil
		}
		if err != nil {
			return zero, err
		}
		fields := models.TicketFields
		adjusted := orm.Decimal(orm.F(fields.ExpectedCost)).Multiply(input.factor).Add(input.delta)
		source := models.TicketObjects.Using(reader).Filter(fields.CategoryID.Exact(a.categoryID))
		if input.minimum != nil {
			source = source.Filter(adjusted.GreaterThanOrEqual(*input.minimum))
		}
		selected, err := orm.SelectQuery(source.OrderBy(adjusted.Desc().NullsLast(), fields.ID.Asc()),
			orm.Project4(fields.ID, fields.Subject, fields.ExpectedCost, adjusted, func(id int64, subject string, original *decimal.Decimal, adjusted orm.Optional[decimal.Decimal]) costPreviewRow {
				return costPreviewRow{id: id, subject: subject, original: original, adjusted: adjusted}
			}))
		if err != nil {
			return zero, err
		}
		page, err := selected.Page(work, costPreviewPageSize, (input.page-1)*costPreviewPageSize)
		if err != nil {
			return zero, err
		}
		for _, row := range page.Rows {
			if row.id <= 0 || (row.original != nil) != row.adjusted.Valid() {
				return zero, errors.New("helpdesk: inconsistent cost preview result")
			}
		}
		return costPreviewPage{category: category, query: input, tickets: page, found: true}, nil
	})
	if err != nil {
		return zero, err
	}
	if !value.found {
		return zero, admin.ErrObjectNotFound
	}
	return value, nil
}
