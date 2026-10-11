package helpdesk

import "github.com/progresshans/godj/serializers"

// Conversion consumes only successfully bound values from the shared Ticket input spec.
func ticketInputFromValues(values serializers.Values) ticketInput {
	subject, _ := values.Get("subject")
	text, _ := subject.AsString()
	closed, _ := values.Get("closed")
	boolean, _ := closed.AsBoolean()
	input := ticketInput{subject: text, closed: boolean}
	if labels, present := values.Get("labels"); present {
		input.labels, _ = labels.AsIntegers()
	}
	if priority, present := values.Get("priority"); present && !priority.IsNull() {
		integer, _ := priority.AsInteger()
		input.priority = &integer
	}
	if details, present := values.Get("details"); present && !details.IsNull() {
		text, _ := details.AsString()
		input.details = &text
	}
	if resolution, present := values.Get("resolution"); present && !resolution.IsNull() {
		text, _ := resolution.AsString()
		input.resolution = &text
	}
	if dueAt, present := values.Get("due_at"); present && !dueAt.IsNull() {
		instant, _ := dueAt.AsDateTime()
		input.dueAt = &instant
	}
	if reviewed, present := values.Get("reviewed"); present && !reviewed.IsNull() {
		boolean, _ := reviewed.AsBoolean()
		input.reviewed = &boolean
	}
	if serviceOn, present := values.Get("service_on"); present && !serviceOn.IsNull() {
		date, _ := serviceOn.AsDate()
		input.serviceOn = &date
	}
	if reference, present := values.Get("external_reference"); present && !reference.IsNull() {
		identifier, _ := reference.AsUUID()
		input.externalReference = &identifier
	}
	if address, present := values.Get("external_url"); present && !address.IsNull() {
		text, _ := address.AsString()
		input.externalURL = &text
	}
	if payload, present := values.Get("external_payload"); present && !payload.IsNull() {
		document, _ := payload.AsJSON()
		input.externalPayload = &document
	}
	if cost, present := values.Get("expected_cost"); present && !cost.IsNull() {
		number, _ := cost.AsDecimal()
		input.expectedCost = &number
	}
	if effort, present := values.Get("effort"); present && !effort.IsNull() {
		floatValue, _ := effort.AsFloat()
		input.effort = &floatValue
	}
	if elapsed, present := values.Get("elapsed"); present && !elapsed.IsNull() {
		durationValue, _ := elapsed.AsDuration()
		input.elapsed = &durationValue
	}
	if serviceAt, present := values.Get("service_at"); present && !serviceAt.IsNull() {
		clockValue, _ := serviceAt.AsTime()
		input.serviceAt = &clockValue
	}
	return input
}
