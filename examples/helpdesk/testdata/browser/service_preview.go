package main

import (
	"context"
	"errors"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
)

// These writes use the borrowed transaction session to prepare the disposable
// database before any read-only snapshot or browser request. Existing
// summary/cost/deadline fixtures retain their data.
func seedServicePreview(ctx context.Context, session db.Session) error {
	rows, err := models.TicketObjects.Using(session).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
	if err != nil {
		return err
	}
	if len(rows) != 30 {
		return errors.New("service browser fixture requires thirty tickets")
	}
	fields := models.TicketFields
	for _, row := range rows {
		if row.ID == 1 {
			continue
		}
		day := calendar.Date{Year: 2024, Month: 2, Day: 29}
		wall := clock.Time{Hour: 12}
		if row.ID == 2 {
			day.Day = 28
			wall = clock.Time{Hour: 23, Minute: 59, Second: 59, Microsecond: 999999}
		}
		if row.ID == 3 || row.ID == 5 {
			wall = clock.Time{}
		}
		if row.ID >= 7 {
			day.Month, day.Day = 3, int(row.ID)-6
			wall = clock.Time{Hour: 12, Minute: 34, Second: 56}
		}
		var assignments []orm.UpdateAssignment[models.Ticket]
		if row.ID != 5 {
			assignments = append(assignments, orm.Assign(fields.ServiceOn, day))
		}
		if row.ID != 4 {
			assignments = append(assignments, orm.Assign(fields.ServiceAt, wall))
		}
		if row.ID == 30 {
			assignments = append(assignments, orm.Assign(fields.Elapsed, duration.Duration{}))
		}
		count, err := models.TicketObjects.Using(session).Filter(fields.ID.Exact(row.ID)).Update(ctx, assignments...)
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("service browser fixture lost its selected ticket")
		}
	}
	return nil
}
