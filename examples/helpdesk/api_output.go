package helpdesk

import (
	"math"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
)

type ticketDetailResponse struct {
	ticket   ticketRecord
	category models.Category
}

type labelEnsureResponse struct {
	label   models.Label
	created bool
}

type apiOutputs struct {
	label       output.Output[models.Label]
	ticket      output.Output[ticketRecord]
	ticketBulk  output.Output[[]ticketRecord]
	detail      output.Output[ticketDetailResponse]
	summary     output.Output[ticketSummaryPage]
	labelEnsure output.Output[labelEnsureResponse]
}

func prepareAPIOutputs(encoder serializers.ModelEncoder[ticketRecord], labelEncoder serializers.ModelEncoder[models.Label]) (apiOutputs, error) {
	var result apiOutputs
	ticket := output.Named("Ticket", output.Model(encoder))
	// This two-field application summary keeps its existing wire contract. It
	// exposes no other Category fields; Ticket uses the model-derived allowlist.
	category := output.Named("CategorySummary", output.Object(
		output.Field("id", output.Int64(), func(value models.Category) int64 { return value.ID }),
		output.Field("name", output.String(), func(value models.Category) string { return value.Name }),
	))
	detail := output.Named("TicketDetail", output.Object(
		output.Field("ticket", ticket, func(value ticketDetailResponse) ticketRecord { return value.ticket }),
		output.Field("category", category, func(value ticketDetailResponse) models.Category { return value.category }),
	))
	count := output.Int64Range(0, math.MaxInt64)
	row := output.Object(
		output.Field("priority", output.Nullable(output.Int64()), func(value ticketSummaryRow) *int64 { return value.priority }),
		output.Field("priority_label", output.String(), func(value ticketSummaryRow) string { return ticketSummaryPriorityLabel(value.priority) }),
		output.Field("total", count, func(value ticketSummaryRow) int64 { return value.total }),
		output.Field("open", count, func(value ticketSummaryRow) int64 { return value.open }),
	)
	summary := output.Named("TicketSummary", output.Object(
		output.Field("category", category, func(value ticketSummaryPage) models.Category { return value.category }),
		output.Field("page", output.Int64Range(1, ticketSummaryMaximumPage), func(value ticketSummaryPage) int64 { return int64(value.query.page) }),
		output.Field("page_size", output.Int64Range(ticketSummaryPageSize, ticketSummaryPageSize), func(ticketSummaryPage) int64 { return ticketSummaryPageSize }),
		output.Field("min_open", count, func(value ticketSummaryPage) int64 { return value.query.minimumOpen }),
		output.Field("total_groups", count, func(value ticketSummaryPage) int64 { return value.groups.Total }),
		output.Field("results", output.Array(row, 0, ticketSummaryPageSize), func(value ticketSummaryPage) []ticketSummaryRow { return value.groups.Rows }),
	))
	var err error
	if result.ticket, err = output.New(ticket, serializers.Limits{}); err != nil {
		return apiOutputs{}, err
	}
	if result.ticketBulk, err = output.New(output.Array(ticket, 1, ticketBulkMaximum), serializers.Limits{MaxValues: maximumJSONListValues}); err != nil {
		return apiOutputs{}, err
	}
	if result.detail, err = output.New(detail, serializers.Limits{}); err != nil {
		return apiOutputs{}, err
	}
	if result.summary, err = output.New(summary, serializers.Limits{}); err != nil {
		return apiOutputs{}, err
	}
	label := output.Named("Label", output.Model(labelEncoder))
	if result.label, err = output.New(label, serializers.Limits{}); err != nil {
		return apiOutputs{}, err
	}
	ensure := output.Named("LabelEnsureResult", output.Object(
		output.Field("label", label, func(value labelEnsureResponse) models.Label { return value.label }),
		output.Field("created", output.Boolean(), func(value labelEnsureResponse) bool { return value.created }),
	))
	if result.labelEnsure, err = output.New(ensure, serializers.Limits{}); err != nil {
		return apiOutputs{}, err
	}
	return result, nil
}
