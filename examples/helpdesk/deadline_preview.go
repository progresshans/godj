package helpdesk

import (
	"context"
	"errors"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

const (
	DeadlinePreviewPath        = "/tickets/deadline-preview/"
	deadlinePreviewPageSize    = 20
	deadlinePreviewMaximumPage = admin.MaximumListOffset/deadlinePreviewPageSize + 1
)

type deadlinePreviewQuery struct {
	page    int
	asOf    time.Time
	shift   duration.Duration
	minimum *duration.Duration
}

type deadlinePreviewRow struct {
	id        int64
	subject   string
	original  *time.Time
	adjusted  orm.Optional[time.Time]
	remaining orm.Optional[duration.Duration]
}

type deadlinePreviewPage struct {
	category models.Category
	query    deadlinePreviewQuery
	tickets  orm.ResultPage[deadlinePreviewRow]
	found    bool
}

func (a *Application) readDeadlinePreview(ctx context.Context, actor auth.Principal, input deadlinePreviewQuery) (deadlinePreviewPage, error) {
	var zero deadlinePreviewPage
	if !actor.Authenticated() || !actor.Has(ViewTicket) {
		return zero, errors.New("helpdesk: deadline preview lacks admitted permission")
	}
	reference, err := temporal.Canonical(input.asOf)
	if err != nil || reference != input.asOf || input.page < 1 || input.page > deadlinePreviewMaximumPage || !input.shift.Valid() || input.minimum != nil && !input.minimum.Valid() {
		return zero, errors.New("helpdesk: deadline preview input was not validated")
	}
	value, err := readSnapshotValue(ctx, a.backend, func(work context.Context, reader db.Queryer) (deadlinePreviewPage, error) {
		if interfaceNil(reader) {
			return zero, errors.New("helpdesk: nil deadline preview reader")
		}
		category, err := models.CategoryObjects.Using(reader).Filter(models.CategoryFields.ID.Exact(a.categoryID)).Get(work)
		if errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			return zero, nil
		}
		if err != nil {
			return zero, err
		}
		fields := models.TicketFields
		source := models.TicketObjects.Using(reader).Filter(fields.CategoryID.Exact(a.categoryID))
		named, adjusted, err := orm.Annotate(source, "adjusted_due_at", orm.DateTime(orm.F(fields.DueAt)).Add(input.shift))
		if err != nil {
			return zero, err
		}
		named, remaining, err := orm.Annotate(named, "remaining", orm.DateTime(orm.OptionalOperand(adjusted)).Difference(input.asOf))
		if err != nil {
			return zero, err
		}
		if input.minimum != nil {
			named = named.Filter(remaining.GreaterThanOrEqual(orm.Some(*input.minimum)))
		}
		selected, err := orm.SelectNamed(named.OrderBy(remaining.Asc().NullsLast(), fields.ID.Asc()),
			orm.Project5(fields.ID, fields.Subject, fields.DueAt, adjusted, remaining,
				func(id int64, subject string, original *time.Time, adjusted orm.Optional[time.Time], remaining orm.Optional[duration.Duration]) deadlinePreviewRow {
					return deadlinePreviewRow{id: id, subject: subject, original: original, adjusted: adjusted, remaining: remaining}
				}))
		if err != nil {
			return zero, err
		}
		page, err := selected.Page(work, deadlinePreviewPageSize, (input.page-1)*deadlinePreviewPageSize)
		if err != nil {
			return zero, err
		}
		for _, row := range page.Rows {
			if row.id <= 0 || (row.original != nil) != row.adjusted.Valid() || row.adjusted.Valid() != row.remaining.Valid() {
				return zero, errors.New("helpdesk: inconsistent deadline preview result")
			}
		}
		return deadlinePreviewPage{category: category, query: input, tickets: page, found: true}, nil
	})
	if err != nil {
		return zero, err
	}
	if !value.found {
		return zero, admin.ErrObjectNotFound
	}
	return value, nil
}
