package helpdesk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/timezone"
)

func TestDeadlineCalendarOutputFailureDoesNotPublishRows(t *testing.T) {
	category := output.Object(output.Field("id", output.Int64(), func(value models.Category) int64 { return value.ID }))
	response, err := prepareDeadlineCalendarOutput(category)
	if err != nil {
		t.Fatal(err)
	}
	valid := deadlineCalendarRow{start: orm.Some(calendar.Date{Year: 2024, Month: 2, Day: 29}), total: 2, open: 1, overdue: 1, first: orm.Some(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)), last: orm.Some(time.Date(2024, 2, 29, 23, 59, 59, 999999000, time.UTC))}
	validPage := deadlineCalendarPage{query: deadlineCalendarQuery{zone: timezone.UTC(), page: 1, period: "day", asOf: time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)}, groups: orm.ResultPage[deadlineCalendarRow]{Total: 1, Rows: []deadlineCalendarRow{valid}}}
	if encoded, err := response.Encode(t.Context(), validPage); err != nil || len(encoded) == 0 {
		t.Fatal("valid calendar output did not reach projection", err)
	}
	invalidZone := validPage
	invalidZone.query.zone = timezone.Zone{}
	if encoded, err := response.Encode(t.Context(), invalidZone); err == nil || encoded != nil {
		t.Fatal("zero timezone was published as a valid calendar")
	}
	for _, invalid := range []deadlineCalendarRow{
		{start: orm.Some(calendar.Date{Year: 2023, Month: 2, Day: 29}), total: 1},
		{start: orm.Some(calendar.Date{Year: 10000, Month: 1, Day: 1}), total: 1},
		{first: orm.Some(time.Date(0, 12, 31, 23, 59, 59, 0, time.UTC)), total: 1},
		{last: orm.Some(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)), total: 1},
		{total: 0}, {total: 1, open: -1}, {total: 1, overdue: -1},
	} {
		page := deadlineCalendarPage{query: deadlineCalendarQuery{zone: timezone.UTC(), page: 1, period: "day", asOf: time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)}, groups: orm.ResultPage[deadlineCalendarRow]{Total: 2, Rows: []deadlineCalendarRow{valid, invalid}}}
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
	for _, period := range []string{"", "Day", "hour", "unknown"} {
		page := deadlineCalendarPage{query: deadlineCalendarQuery{zone: timezone.UTC(), page: 1, period: period, asOf: time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)}, groups: orm.ResultPage[deadlineCalendarRow]{Total: 1, Rows: []deadlineCalendarRow{valid}}}
		if body, err := response.Encode(t.Context(), page); err == nil || body != nil {
			t.Fatal("invalid period escaped the output choice domain", body, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := response.Prepare(ctx, 200, deadlineCalendarPage{}); !errors.Is(err, context.Canceled) {
		t.Fatal("calendar output lost cancellation", err)
	}
}
