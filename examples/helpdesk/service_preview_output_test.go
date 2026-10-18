package helpdesk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
)

func TestServicePreviewOutputFailureDoesNotPublishRows(t *testing.T) {
	category := output.Object(output.Field("id", output.Int64(), func(value models.Category) int64 { return value.ID }))
	response, err := prepareServicePreviewOutput(category)
	if err != nil {
		t.Fatal(err)
	}
	valid := servicePreviewRow{id: 1, subject: "valid", day: new(calendar.Date{Year: 1, Month: 1, Day: 1}), clock: new(clock.Time{}), elapsed: new(duration.Duration{}), start: orm.Some(time.Time{}), adjusted: orm.Some(time.Time{}), finish: orm.Some(time.Time{})}
	invalidTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, invalid := range []servicePreviewRow{
		{id: 2, subject: "bad source date", day: new(calendar.Date{})},
		{id: 2, subject: "bad source clock", clock: new(clock.Time{Hour: 24})},
		{id: 2, subject: "bad elapsed", elapsed: new(duration.Duration{Days: duration.MaxDays + 1})},
		{id: 2, subject: "bad deadline", due: &invalidTime},
		{id: 2, subject: "bad start", start: orm.Some(invalidTime)},
		{id: 2, subject: "bad adjustment", adjusted: orm.Some(invalidTime)},
		{id: 2, subject: "bad finish", finish: orm.Some(invalidTime)},
		{id: 2, subject: "bad slack", slack: orm.Some(duration.Duration{Days: duration.MaxDays + 1})},
		{id: 2, subject: "bad\x00subject"},
	} {
		page := servicePreviewPage{query: servicePreviewQuery{page: 1}, tickets: orm.ResultPage[servicePreviewRow]{Total: 2, Rows: []servicePreviewRow{valid, invalid}}}
		if data, err := response.Encode(t.Context(), page); err == nil || data != nil {
			t.Fatal("service preview published a valid prefix", err)
		}
		prepared, err := response.Prepare(t.Context(), 200, page)
		if err == nil {
			t.Fatal("invalid service output prepared")
		}
		if published, err := response.Response(prepared); err == nil || published.Status() != 0 {
			t.Fatal("invalid service output became a response")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := response.Prepare(ctx, 200, servicePreviewPage{}); !errors.Is(err, context.Canceled) {
		t.Fatal("service output ignored cancellation", err)
	}
}
