package helpdesk

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
)

func TestLabelUsageOutputFailureDoesNotPublishRows(t *testing.T) {
	category := output.Object(output.Field("id", output.Int64(), func(value models.Category) int64 { return value.ID }))
	response, err := prepareLabelUsageOutput(category)
	if err != nil {
		t.Fatal(err)
	}
	valid := labelUsageRow{id: 1, name: "valid", total: 1, cost: orm.Some(decimal.Decimal{}), adjusted: orm.Some(decimal.Decimal{})}
	for _, invalid := range []labelUsageRow{
		{id: 2, name: "bad original", cost: orm.Some(decimal.Decimal{Coefficient: "invalid"})},
		{id: 2, name: "bad adjusted", adjusted: orm.Some(decimal.Decimal{Coefficient: "invalid"})},
		{id: 2, name: "bad\x00label"},
	} {
		page := labelUsagePage{query: labelUsageQuery{page: 1, usage: "all", factor: decimal.Decimal{Coefficient: "1"}}, labels: orm.ResultPage[labelUsageRow]{Total: 2, Rows: []labelUsageRow{valid, invalid}}}
		if encoded, err := response.Encode(t.Context(), page); err == nil || encoded != nil {
			t.Fatal("label usage output published a valid prefix", err)
		}
		prepared, err := response.Prepare(t.Context(), 200, page)
		if err == nil {
			t.Fatal("invalid label usage output was prepared")
		}
		if published, err := response.Response(prepared); err == nil || published.Status() != 0 {
			t.Fatal("invalid label usage output became an HTTP response")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := response.Prepare(ctx, 200, labelUsagePage{}); !errors.Is(err, context.Canceled) {
		t.Fatal("label usage output ignored cancellation", err)
	}
}
