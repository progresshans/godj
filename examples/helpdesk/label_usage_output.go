package helpdesk

import (
	"math"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
)

func prepareLabelUsageOutput(category output.Shape[models.Category]) (output.Output[labelUsagePage], error) {
	count := output.Int64Range(0, math.MaxInt64)
	row := output.Object(
		output.Field("id", output.Int64Range(1, math.MaxInt64), func(row labelUsageRow) int64 { return row.id }),
		output.Field("name", output.String(), func(row labelUsageRow) string { return row.name }),
		output.Field("total", count, func(row labelUsageRow) int64 { return row.total }),
		output.Field("open", count, func(row labelUsageRow) int64 { return row.open }),
		output.Field("closed", count, func(row labelUsageRow) int64 { return row.total - row.open }),
		output.Field("expected_cost_total", output.Nullable(output.Decimal()), func(row labelUsageRow) *decimal.Decimal { return summaryOptionalPointer(row.cost) }),
		output.Field("adjusted_cost_total", output.Nullable(output.Decimal()), func(row labelUsageRow) *decimal.Decimal { return summaryOptionalPointer(row.adjusted) }),
		output.Field("effort_average", output.Nullable(output.Float()), func(row labelUsageRow) *float64 { return summaryOptionalPointer(row.effort) }),
		output.Field("elapsed_total", output.Nullable(output.Duration()), func(row labelUsageRow) *duration.Duration { return summaryOptionalPointer(row.elapsed) }),
		output.Field("adjusted_elapsed_total", output.Nullable(output.Duration()), func(row labelUsageRow) *duration.Duration { return summaryOptionalPointer(row.adjustedElapsed) }),
	)
	return output.New(output.Named("LabelUsage", output.Object(
		output.Field("category", category, func(page labelUsagePage) models.Category { return page.category }),
		output.Field("page", output.Int64Range(1, labelUsageMaximumPage), func(page labelUsagePage) int64 { return int64(page.query.page) }),
		output.Field("page_size", output.Int64Range(labelUsagePageSize, labelUsagePageSize), func(labelUsagePage) int64 { return labelUsagePageSize }),
		output.Field("usage", output.String(), func(page labelUsagePage) string { return page.query.usage }),
		output.Field("factor", output.Decimal(), func(page labelUsagePage) decimal.Decimal { return page.query.factor }),
		output.Field("delta", output.Decimal(), func(page labelUsagePage) decimal.Decimal { return page.query.delta }),
		output.Field("elapsed_factor", output.Decimal(), func(page labelUsagePage) decimal.Decimal { return page.query.elapsedFactor }),
		output.Field("elapsed_divisor", output.Decimal(), func(page labelUsagePage) decimal.Decimal { return page.query.elapsedDivisor }),
		output.Field("total_labels", count, func(page labelUsagePage) int64 { return page.labels.Total }),
		output.Field("results", output.Array(row, 0, labelUsagePageSize), func(page labelUsagePage) []labelUsageRow { return page.labels.Rows }),
	)), serializers.Limits{})
}
