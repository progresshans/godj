package helpdesk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
)

func TestDeadlinePreviewOutputFailureDoesNotPublishRows(t *testing.T) {
	category := output.Object(output.Field("id", output.Int64(), func(value models.Category) int64 { return value.ID }))
	response, err := prepareDeadlinePreviewOutput(category)
	if err != nil {
		t.Fatal(err)
	}
	valid := deadlinePreviewRow{id: 1, subject: "valid", original: new(time.Time{}), adjusted: orm.Some(time.Time{}), remaining: orm.Some(duration.Duration{})}
	invalidTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, invalid := range []deadlinePreviewRow{
		{id: 2, subject: "bad source", original: &invalidTime},
		{id: 2, subject: "bad shifted instant", adjusted: orm.Some(invalidTime)},
		{id: 2, subject: "bad period", remaining: orm.Some(duration.Duration{Days: duration.MaxDays + 1})},
		{id: 2, subject: "bad\x00subject"},
	} {
		page := deadlinePreviewPage{query: deadlinePreviewQuery{page: 1}, tickets: orm.ResultPage[deadlinePreviewRow]{Total: 2, Rows: []deadlinePreviewRow{valid, invalid}}}
		if encoded, err := response.Encode(t.Context(), page); err == nil || encoded != nil {
			t.Fatal("deadline preview published a valid prefix", err)
		}
		prepared, err := response.Prepare(t.Context(), 200, page)
		if err == nil {
			t.Fatal("invalid deadline preview prepared")
		}
		if published, err := response.Response(prepared); err == nil || published.Status() != 0 {
			t.Fatal("invalid deadline preview became an HTTP response")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := response.Prepare(ctx, 200, deadlinePreviewPage{}); !errors.Is(err, context.Canceled) {
		t.Fatal("deadline output ignored cancellation", err)
	}
}
