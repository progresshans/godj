package helpdesk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
)

func TestDeadlineCalendarOutputFailureDoesNotPublishRows(t *testing.T) {
	category := output.Object(output.Field("id", output.Int64(), func(value models.Category) int64 { return value.ID }))
	response, err := prepareDeadlineCalendarOutput(category)
	if err != nil {
		t.Fatal(err)
	}
	valid := deadlineCalendarRow{day: orm.Some(calendar.Date{Year: 2024, Month: 2, Day: 29}), total: 2, open: 1, overdue: 1, first: orm.Some(clock.Time{}), last: orm.Some(clock.Time{Hour: 23, Minute: 59, Second: 59, Microsecond: 999999})}
	for _, invalid := range []deadlineCalendarRow{
		{day: orm.Some(calendar.Date{Year: 2023, Month: 2, Day: 29}), total: 1},
		{day: orm.Some(calendar.Date{Year: 10000, Month: 1, Day: 1}), total: 1},
		{first: orm.Some(clock.Time{Hour: 24}), total: 1},
		{last: orm.Some(clock.Time{Microsecond: 1000000}), total: 1},
		{total: 0}, {total: 1, open: -1}, {total: 1, overdue: -1},
	} {
		page := deadlineCalendarPage{query: deadlineCalendarQuery{page: 1, asOf: time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)}, groups: orm.ResultPage[deadlineCalendarRow]{Total: 2, Rows: []deadlineCalendarRow{valid, invalid}}}
		if encoded, err := response.Encode(t.Context(), page); err == nil || encoded != nil {
			t.Fatal("invalid calendar output published a prefix", err)
		}
		prepared, err := response.Prepare(t.Context(), 200, page)
		if err == nil {
			t.Fatal("invalid calendar output prepared")
		}
		if published, err := response.Response(prepared); err == nil || published.Status() != 0 {
			t.Fatal("invalid calendar output became an HTTP response")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := response.Prepare(ctx, 200, deadlineCalendarPage{}); !errors.Is(err, context.Canceled) {
		t.Fatal("calendar output lost cancellation", err)
	}
}
