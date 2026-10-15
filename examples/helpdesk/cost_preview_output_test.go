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

func TestCostPreviewOutputFailureDoesNotPublishRows(t *testing.T) {
	category := output.Object(output.Field("id", output.Int64(), func(value models.Category) int64 { return value.ID }))
	response, err := prepareCostPreviewOutput(category)
	if err != nil {
		t.Fatal(err)
	}
	valid := costPreviewRow{id: 1, subject: "valid", original: new(decimal.Decimal{}), adjusted: orm.Some(decimal.Decimal{})}
	for _, invalid := range []costPreviewRow{
		{id: 2, subject: "bad source", original: new(decimal.Decimal{Coefficient: "invalid"})},
		{id: 2, subject: "bad result", adjusted: orm.Some(decimal.Decimal{Coefficient: "invalid"})},
		{id: 2, subject: "bad\x00subject"},
	} {
		page := costPreviewPage{query: costPreviewQuery{page: 1, factor: decimal.Decimal{Coefficient: "1"}}, tickets: orm.ResultPage[costPreviewRow]{Total: 2, Rows: []costPreviewRow{valid, invalid}}}
		if encoded, err := response.Encode(t.Context(), page); err == nil || encoded != nil {
			t.Fatal("cost preview output published a valid prefix", err)
		}
		prepared, err := response.Prepare(t.Context(), 200, page)
		if err == nil {
			t.Fatal("invalid cost preview was prepared")
		}
		if published, err := response.Response(prepared); err == nil || published.Status() != 0 {
			t.Fatal("invalid cost preview became an HTTP response")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := response.Prepare(ctx, 200, costPreviewPage{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cost preview output ignored cancellation", err)
	}
}
