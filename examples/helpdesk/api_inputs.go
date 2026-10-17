package helpdesk

import (
	"context"
	"net/http"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/uuid"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

// This create DTO intentionally collapses omitted optional values to model
// null/empty. Partial updates retain their separate presence-aware policy.
func prepareTicketAPIInput(spec serializers.Spec) (input.Body[ticketInput], error) {
	return input.New(spec, api.ParserConfig{MaxBodyBytes: maximumJSONBodyBytes},
		input.Field("subject", input.String(), func(r *ticketInput, v input.Presence[string]) { r.subject, _ = v.Get() }),
		input.Field("details", input.Nullable(input.String()), func(r *ticketInput, v input.Presence[*string]) { r.details, _ = v.Get() }),
		input.Field("closed", input.Boolean(), func(r *ticketInput, v input.Presence[bool]) { r.closed, _ = v.Get() }),
		input.Field("priority", input.Nullable(input.Integer()), func(r *ticketInput, v input.Presence[*int64]) { r.priority, _ = v.Get() }),
		input.Field("resolution", input.Nullable(input.String()), func(r *ticketInput, v input.Presence[*string]) { r.resolution, _ = v.Get() }),
		input.Field("due_at", input.Nullable(input.DateTime()), func(r *ticketInput, v input.Presence[*time.Time]) { r.dueAt, _ = v.Get() }),
		input.Field("reviewed", input.Nullable(input.Boolean()), func(r *ticketInput, v input.Presence[*bool]) { r.reviewed, _ = v.Get() }),
		input.Field("service_on", input.Nullable(input.Date()), func(r *ticketInput, v input.Presence[*calendar.Date]) { r.serviceOn, _ = v.Get() }),
		input.Field("service_at", input.Nullable(input.Time()), func(r *ticketInput, v input.Presence[*clock.Time]) { r.serviceAt, _ = v.Get() }),
		input.Field("elapsed", input.Nullable(input.Duration()), func(r *ticketInput, v input.Presence[*duration.Duration]) { r.elapsed, _ = v.Get() }),
		input.Field("effort", input.Nullable(input.Float()), func(r *ticketInput, v input.Presence[*float64]) { r.effort, _ = v.Get() }),
		input.Field("expected_cost", input.Nullable(input.Decimal()), func(r *ticketInput, v input.Presence[*decimal.Decimal]) { r.expectedCost, _ = v.Get() }),
		input.Field("external_reference", input.Nullable(input.UUID()), func(r *ticketInput, v input.Presence[*uuid.UUID]) { r.externalReference, _ = v.Get() }),
		input.Field("external_payload", input.Nullable(input.JSON()), func(r *ticketInput, v input.Presence[*jsonvalue.Value]) { r.externalPayload, _ = v.Get() }),
		input.Field("external_url", input.Nullable(input.String()), func(r *ticketInput, v input.Presence[*string]) { r.externalURL, _ = v.Get() }),
		input.Field("labels", input.Integers(), func(r *ticketInput, v input.Presence[[]int64]) { r.labels, _ = v.Get() }),
	)
}

func validateTicketAPIInput(ctx context.Context, value ticketInput) (validation.Errors, error) {
	if err := ctx.Err(); err != nil {
		return validation.Errors{}, err
	}
	if value.externalPayload != nil {
		return externalPayloadErrors(*value.externalPayload), nil
	}
	return validation.Errors{}, nil
}

type labelAPIInput struct {
	name input.Presence[string]
}

type reportAPIInput struct {
	ticketID  input.Presence[int64]
	summary   input.Presence[string]
	completed input.Presence[bool]
}

func prepareLabelAPIInput(spec serializers.Spec) (input.Body[labelAPIInput], error) {
	return input.New(spec, api.ParserConfig{MaxBodyBytes: maximumJSONBodyBytes},
		input.Field("name", input.String(), func(result *labelAPIInput, value input.Presence[string]) { result.name = value }),
	)
}

func prepareReportAPIInput(spec serializers.Spec, withTicket bool) (input.Body[reportAPIInput], error) {
	properties := []input.Property[reportAPIInput]{
		input.Field("summary", input.String(), func(result *reportAPIInput, value input.Presence[string]) { result.summary = value }),
		input.Field("completed", input.Boolean(), func(result *reportAPIInput, value input.Presence[bool]) { result.completed = value }),
	}
	if withTicket {
		properties = append(properties, input.Field("ticket", input.Integer(), func(result *reportAPIInput, value input.Presence[int64]) { result.ticketID = value }))
	}
	return input.New(spec, api.ParserConfig{MaxBodyBytes: maximumJSONBodyBytes}, properties...)
}

func bindTypedInput[T any](request *web.Request, body input.Body[T], mode serializers.Mode) (T, web.Response, bool, error) {
	value, diagnostics, err := body.Parse(request, mode)
	if err != nil {
		response, handled, responseErr := api.RequestErrorResponse(err)
		if handled {
			return value, response, true, responseErr
		}
		return value, web.Response{}, false, err
	}
	if !diagnostics.Empty() {
		response, err := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
		return value, response, true, err
	}
	return value, web.Response{}, false, nil
}
