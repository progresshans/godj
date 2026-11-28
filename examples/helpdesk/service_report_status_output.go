package helpdesk

import (
	"math"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
)

func prepareServiceReportStatusOutput(category output.Shape[models.Category]) (output.Output[serviceReportStatusPage], error) {
	row := output.Object(
		output.Field("id", output.Int64Range(1, math.MaxInt64), func(value serviceReportStatusRow) int64 { return value.id }),
		output.Field("ticket", output.Int64Range(1, math.MaxInt64), func(value serviceReportStatusRow) int64 { return value.ticketID }),
		output.Field("summary", output.String(), func(value serviceReportStatusRow) string { return value.summary }),
		output.Field("completed", output.Boolean(), func(value serviceReportStatusRow) bool { return value.completed }),
		output.Field("ticket_closed", output.Boolean(), func(value serviceReportStatusRow) bool { return value.ticketClosed != nil && *value.ticketClosed }),
		output.Field("matched", output.Boolean(), func(value serviceReportStatusRow) bool { matched, _ := value.matched.Get(); return matched }),
	)
	return output.New(output.Named("ServiceReportStatus", output.Object(
		output.Field("category", category, func(value serviceReportStatusPage) models.Category { return value.category }),
		output.Field("page", output.Int64Range(1, serviceReportStatusMaximumPage), func(value serviceReportStatusPage) int64 { return int64(value.query.page) }),
		output.Field("page_size", output.Int64Range(serviceReportStatusPageSize, serviceReportStatusPageSize), func(serviceReportStatusPage) int64 { return serviceReportStatusPageSize }),
		output.Field("comparison", output.String(), func(value serviceReportStatusPage) string { return value.query.comparison }),
		output.Field("total_reports", output.Int64Range(0, math.MaxInt64), func(value serviceReportStatusPage) int64 { return value.reports.Total }),
		output.Field("results", output.Array(row, 0, serviceReportStatusPageSize), func(value serviceReportStatusPage) []serviceReportStatusRow { return value.reports.Rows }),
	)), serializers.Limits{})
}
