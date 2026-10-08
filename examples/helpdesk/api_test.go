package helpdesk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"mime"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/web"
)

func TestHelpdeskAPICompositionAndNamedContractsWithoutIO(t *testing.T) {
	application := helpdeskConstructionApplication(t)
	recording := &helpdeskRecordingAuthentication{inspectBindings: true}
	adapter, err := application.API(helpdeskAPIConfig(helpdeskDescribedAuthentication{recording}))
	if err != nil {
		t.Fatal(err)
	}
	document, err := adapter.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeHelpdeskDocument(t, document.Bytes())
	assertHelpdeskOperationContracts(t, decoded, adapter.Routes())
	boundRoutes := adapter.Routes()
	if len(recording.permissions) != len(boundRoutes) || len(recording.additional) != len(boundRoutes) {
		t.Fatal("incomplete authentication composition")
	}
	for _, route := range boundRoutes {
		permission, additional := helpdeskExpectedAuthorization(t, route.Name)
		binding, err := route.Handler(nil)
		if err != nil || binding.Header().Get("X-Test-Permission") != string(permission) || !slices.Equal(binding.Header().Values("X-Test-Additional"), permissionStrings(additional)) {
			t.Fatalf("authentication binding %s differs", route.Name)
		}
	}

	if _, found := decoded.Components.Schemas[openapi.ErrorSchemaName]; !found {
		t.Fatal("the shared API error component is absent")
	}
	ticket := decoded.Components.Schemas["Ticket"]
	if !slices.Equal(ticket.Required, []string{"id", "subject", "details", "closed", "category", "priority", "resolution", "due_at", "reviewed", "service_on", "service_at", "elapsed", "effort", "expected_cost", "external_reference", "external_payload", "external_payload_digest", "external_url", "labels"}) || len(ticket.Properties) != 19 || ticket.AdditionalProperties {
		t.Fatalf("Ticket fields = %+v", ticket)
	}
	if labels := ticket.Properties["labels"]; labels.Type != "array" || labels.Items == nil || labels.Items.Type != "integer" || labels.Items.Format != "int64" || !labels.ReadOnly || labels.allowsType("null") {
		t.Fatal("Ticket collection response contract")
	}
	if ticket.Properties["subject"].MaxLength != 120 || !ticket.Properties["category"].ReadOnly || !ticket.Properties["details"].allowsType("null") {
		t.Fatal("Ticket response lost its encoder's field constraints")
	}
	input := decoded.Components.Schemas["TicketCreate"]
	update := decoded.Components.Schemas["TicketUpdate"]
	patch := decoded.Components.Schemas["TicketPatch"]
	urlOutput := ticket.Properties["external_url"]
	if len(urlOutput.AnyOf) != 2 || urlOutput.AnyOf[0].Type != "string" || urlOutput.AnyOf[0].MaxLength != 200 || urlOutput.AnyOf[0].Format != "" || !urlOutput.allowsType("null") {
		t.Fatal("URL output must retain string/null and stored bound without grammar")
	}
	digest := ticket.Properties["external_payload_digest"]
	if !digest.ReadOnly || len(digest.AnyOf) != 2 || digest.AnyOf[0].Type != "string" || !digest.allowsType("null") || digest.AnyOf[0].Pattern == "" {
		t.Fatal("read-only binary response contract")
	}
	for _, request := range []helpdeskDocumentSchema{input, update, patch} {
		if _, present := request.Properties["external_payload_digest"]; present {
			t.Fatal("server digest entered OpenAPI input")
		}
		field := request.Properties["external_url"]
		if !field.allowsType("null") || len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || field.AnyOf[0].MaxLength != 0 || field.AnyOf[0].Format != "" || !bytes.Contains(field.Normalization, []byte(`"maxLengthAfterTrim":200`)) {
			t.Fatal("URL request lost its nullable normalized input policy")
		}
	}
	for _, field := range []helpdeskDocumentSchema{ticket.Properties["external_reference"], input.Properties["external_reference"], update.Properties["external_reference"], patch.Properties["external_reference"]} {
		if len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || field.AnyOf[0].Format != "uuid" || field.AnyOf[0].MinLength != 36 || field.AnyOf[0].MaxLength != 36 || !field.allowsType("null") {
			t.Fatal("UUID OpenAPI canonical string/nullable contract changed")
		}
	}
	for _, field := range []helpdeskDocumentSchema{ticket.Properties["expected_cost"], input.Properties["expected_cost"], update.Properties["expected_cost"], patch.Properties["expected_cost"]} {
		if len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || field.AnyOf[0].MinLength != 4 || field.AnyOf[0].MaxLength != 16 || field.AnyOf[0].Pattern != `^-?(0|[1-9][0-9]{0,11})\.[0-9]{2}$` || !field.allowsType("null") {
			t.Fatal("Decimal OpenAPI precision/scale/nullable domain changed")
		}
	}
	for _, field := range []helpdeskDocumentSchema{ticket.Properties["effort"], input.Properties["effort"], update.Properties["effort"], patch.Properties["effort"]} {
		if len(field.AnyOf) != 2 || field.AnyOf[0].Type != "number" || field.AnyOf[0].Format != "double" || !field.allowsType("null") {
			t.Fatal("Float OpenAPI domain changed")
		}
		var min, max float64
		if json.Unmarshal(field.AnyOf[0].Minimum, &min) != nil || json.Unmarshal(field.AnyOf[0].Maximum, &max) != nil || min != -math.MaxFloat64 || max != math.MaxFloat64 {
			t.Fatal("Float finite bounds missing")
		}
	}
	for _, schema := range []helpdeskDocumentSchema{input, update, patch, ticket} {
		field, present := schema.Properties["reviewed"]
		if !present || !field.allowsType("boolean") || !field.allowsType("null") || len(field.Default) != 0 {
			t.Fatal("nullable Boolean schema invented false or lost null")
		}
	}
	if !slices.Equal(update.Required, []string{"subject"}) || string(update.Properties["closed"].Default) != "false" || len(patch.Required) != 0 || len(patch.Properties["closed"].Default) != 0 || len(patch.Properties) != 16 {
		t.Fatal("PUT/PATCH lost required/default/omission policy")
	}
	if len(input.Properties["priority"].AnyOf) != 2 || string(input.Properties["priority"].AnyOf[0].Enum) != "[1,0,-1]" || len(ticket.Properties["priority"].Enum) != 0 || len(ticket.Properties["priority"].AnyOf[0].Enum) != 0 {
		t.Fatal("choices input and existing-row output domains were conflated")
	}
	if !slices.Equal(input.Required, []string{"subject"}) || len(input.Properties) != 16 || input.AdditionalProperties || string(input.Properties["closed"].Default) != "false" || !input.Properties["details"].allowsType("null") || !input.Properties["priority"].allowsType("null") || !ticket.Properties["priority"].allowsType("null") {
		t.Fatalf("TicketCreate presence/defaults = %+v", input)
	}
	if !input.Properties["resolution"].allowsType("null") || !ticket.Properties["resolution"].allowsType("null") || ticket.Properties["resolution"].MaxLength != 0 || input.Properties["resolution"].MaxLength != 0 {
		t.Fatal("Text schema lost null or imposed a model length limit")
	}
	for _, field := range []helpdeskDocumentSchema{input.Properties["service_at"], ticket.Properties["service_at"], update.Properties["service_at"], patch.Properties["service_at"]} {
		if !field.allowsType("null") || len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || field.AnyOf[0].Format != "" || field.AnyOf[0].Pattern != `^([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]{6})?$` || field.AnyOf[0].MinLength != 8 || field.AnyOf[0].MaxLength != 15 {
			t.Fatal("clock schema conflates timezone format or loses its clock bounds")
		}
	}
	for _, field := range []helpdeskDocumentSchema{input.Properties["service_on"], ticket.Properties["service_on"], update.Properties["service_on"], patch.Properties["service_on"]} {
		if !field.allowsType("null") || len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || field.AnyOf[0].Format != "date" {
			t.Fatal("calendar date wire format or presence missing")
		}
	}
	for _, field := range []helpdeskDocumentSchema{input.Properties["due_at"], ticket.Properties["due_at"]} {
		if !field.allowsType("null") || len(field.AnyOf) != 2 || field.AnyOf[0].Type != "string" || field.AnyOf[0].Format != "date-time" {
			t.Fatal("datetime null or format disappeared from OpenAPI")
		}
	}
	if _, found := input.Properties["id"]; found {
		t.Fatal("generated id became writable")
	}
	if _, found := input.Properties["category"]; found {
		t.Fatal("assigned category became writable")
	}
	if input.Properties["subject"].MaxLength != 0 || !bytes.Contains(input.Properties["subject"].Normalization, []byte(`"maxLengthAfterTrim":120`)) {
		t.Fatal("input normalization was replaced with a raw string bound")
	}
	category := decoded.Components.Schemas["CategorySummary"]
	if !slices.Equal(category.Required, []string{"id", "name"}) || len(category.Properties) != 2 || category.Properties["name"].Type != "string" || category.Properties["name"].MaxLength != 0 {
		t.Fatal("CategorySummary differs from the manually encoded category projection")
	}
	detail := decoded.Components.Schemas["TicketDetail"]
	if !slices.Equal(detail.Required, []string{"ticket", "category"}) || len(detail.Properties) != 2 || detail.Properties["ticket"].Ref != "#/components/schemas/Ticket" || detail.Properties["category"].Ref != "#/components/schemas/CategorySummary" {
		t.Fatal("TicketDetail does not reference both explicit projection identities")
	}
	routes := adapter.Routes()
	routes[0].Path, routes[0].Handler = "/changed/", nil
	detached := document.Routes()
	detached[0].Name = "changed"
	encoded := document.Bytes()
	encoded[0] = '!'
	again, err := adapter.OpenAPI()
	if err != nil || !bytes.Equal(again.Bytes(), document.Bytes()) || adapter.Routes()[0].Path != "/api/tickets/" || adapter.Routes()[0].Handler == nil || len(recording.permissions) != len(boundRoutes) {
		t.Fatalf("reading or mutating snapshots changed the API or repeated authentication: %v", err)
	}
}

func TestHelpdeskAPIConstructionRejectsIncompleteAuthentication(t *testing.T) {
	application := helpdeskConstructionApplication(t)
	if result, err := application.API(helpdesk.APIConfig{Authentication: &helpdeskRecordingAuthentication{}}); result != nil || err == nil {
		t.Fatal("accepted missing transactional audit")
	}
	var typedNil *helpdeskRecordingAuthentication
	for _, authentication := range []api.Authentication{nil, typedNil} {
		if result, err := application.API(helpdeskAPIConfig(authentication)); result != nil || err == nil {
			t.Fatal("accepted nil authentication")
		}
	}
	if result, err := (*helpdesk.Application)(nil).API(helpdeskAPIConfig(&helpdeskRecordingAuthentication{})); result != nil || err == nil {
		t.Fatal("accepted nil application")
	}
	for index := 1; index <= 31; index++ {
		for _, recording := range []*helpdeskRecordingAuthentication{{failAt: index}, {nilAt: index}} {
			if result, err := application.API(helpdeskAPIConfig(helpdeskDescribedAuthentication{recording})); result != nil || err == nil || len(recording.permissions) != index {
				t.Fatalf("published a partial authentication composition at operation %d", index)
			}
		}
	}
	if result, err := application.API(helpdeskAPIConfig(&helpdeskRecordingAuthentication{})); result != nil || err == nil {
		t.Fatal("typed endpoint accepted an undocumented authentication profile")
	}
	adapter, err := application.API(helpdeskAPIConfig(helpdeskDescribedAuthentication{&helpdeskRecordingAuthentication{}}))
	if err != nil || len(adapter.Routes()) != 31 {
		t.Fatalf("custom authentication cannot serve routes: %v", err)
	}
	if _, err := adapter.OpenAPI(); err != nil {
		t.Fatal("documented custom authentication cannot describe its routes", err)
	}
	if routes := (*helpdesk.API)(nil).Routes(); routes != nil {
		t.Fatal("nil API published routes")
	}
	if _, err := (*helpdesk.API)(nil).OpenAPI(); err == nil {
		t.Fatal("nil API published a document")
	}
}

func helpdeskExpectedAuthorization(t *testing.T, name string) (auth.Permission, []auth.Permission) {
	t.Helper()
	switch name {
	case "helpdesk:ticket-list", "helpdesk:ticket-detail", "helpdesk:ticket-summary":
		return helpdesk.ViewTicket, nil
	case "helpdesk:ticket-create", "helpdesk:ticket-bulk-create":
		return helpdesk.AddTicket, []auth.Permission{helpdesk.ViewLabel}
	case "helpdesk:ticket-update", "helpdesk:ticket-patch", "helpdesk:ticket-bulk-update", "helpdesk:ticket-raise-priority":
		return helpdesk.ChangeTicket, []auth.Permission{helpdesk.ViewLabel}
	case "helpdesk:ticket-delete":
		return helpdesk.DeleteTicket, nil
	case "helpdesk:service-report-list", "helpdesk:service-report-detail", "helpdesk:ticket-service-report":
		return helpdesk.ViewServiceReport, nil
	case "helpdesk:service-report-create":
		return helpdesk.AddServiceReport, nil
	case "helpdesk:service-report-update", "helpdesk:service-report-patch":
		return helpdesk.ChangeServiceReport, nil
	case "helpdesk:service-report-delete":
		return helpdesk.DeleteServiceReport, nil
	case "helpdesk:ticket-service-report-save":
		return helpdesk.ChangeServiceReport, []auth.Permission{helpdesk.AddServiceReport, helpdesk.ViewServiceReport}
	case "helpdesk:label-list", "helpdesk:label-detail":
		return helpdesk.ViewLabel, nil
	case "helpdesk:label-create":
		return helpdesk.AddLabel, nil
	case "helpdesk:label-update", "helpdesk:label-patch":
		return helpdesk.ChangeLabel, nil
	case "helpdesk:label-delete":
		return helpdesk.DeleteLabel, nil
	case "helpdesk:label-ensure":
		return helpdesk.AddLabel, []auth.Permission{helpdesk.ViewLabel}
	case "helpdesk:ticket-label-list", "helpdesk:ticket-label-detail":
		return helpdesk.ViewTicketLabel, nil
	case "helpdesk:ticket-label-create":
		return helpdesk.AddTicketLabel, []auth.Permission{helpdesk.ViewTicket, helpdesk.ViewLabel}
	case "helpdesk:ticket-label-update", "helpdesk:ticket-label-patch":
		return helpdesk.ChangeTicketLabel, []auth.Permission{helpdesk.ViewTicket, helpdesk.ViewLabel}
	case "helpdesk:ticket-label-delete":
		return helpdesk.DeleteTicketLabel, nil
	default:
		t.Fatal("unexpected Helpdesk route", name)
		return "", nil
	}
}
func permissionStrings(values []auth.Permission) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func assertHelpdeskOperationContracts(t *testing.T, document helpdeskDocument, routes []web.Route) {
	t.Helper()
	if !strings.HasPrefix(document.OpenAPI, "3.1.") || len(document.Paths) != 13 || len(routes) != 31 || len(document.Paths["/api/tickets/summary/"]) != 1 || len(document.Paths["/api/tickets/raise-priority/"]) != 1 || len(document.Paths["/api/tickets/bulk/"]) != 2 || len(document.Paths["/api/tickets/"]) != 2 || len(document.Paths["/api/tickets/{id}/"]) != 4 || len(document.Paths["/api/service-reports/"]) != 2 || len(document.Paths["/api/service-reports/{id}/"]) != 4 || len(document.Paths["/api/tickets/{id}/service-report/"]) != 2 || len(document.Paths["/api/labels/"]) != 2 || len(document.Paths["/api/labels/{id}/"]) != 4 || len(document.Paths["/api/ticket-labels/"]) != 2 || len(document.Paths["/api/ticket-labels/{id}/"]) != 4 {
		t.Fatal("Helpdesk document changed the declared operation surface")
	}
	for _, route := range routes {
		permission, additional := helpdeskExpectedAuthorization(t, route.Name)

		path := strings.ReplaceAll(route.Path, "<int64:id>", "{id}")
		operation := document.Paths[path][strings.ToLower(route.Method)]
		if operation.OperationID != route.Name || operation.Permission != string(permission) || !slices.Equal(operation.AdditionalPermissions, permissionStrings(additional)) || route.Handler == nil || len(operation.Security) != 1 {
			t.Fatalf("route %s %s differs from its documented operation", route.Method, path)
		}
		securityCount := 1
		if route.Method != http.MethodGet {
			securityCount = 3
		}
		if len(operation.Security[0]) != securityCount {
			t.Fatalf("operation %s lost its authentication/CSRF requirements", route.Name)
		}
		if _, advertised := operation.Responses["406"]; advertised {
			t.Fatal("Helpdesk advertises Accept negotiation that it does not install")
		}
		if _, documented := operation.Responses["403"]; !documented {
			t.Fatal("authentication denial is undocumented")
		}
	}
	assertServiceReportContracts(t, document)
	assertLabelContracts(t, document)
	assertTicketLabelContracts(t, document)
	summary := document.Paths["/api/tickets/summary/"]["get"]
	summarySchema := document.Components.Schemas["TicketSummary"]
	if summary.Responses["200"].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/TicketSummary" || len(summary.Parameters) != 2 || summary.Parameters[0].Name != "p" || summary.Parameters[1].Name != "min_open" || summarySchema.AdditionalProperties || !slices.Equal(summarySchema.Required, []string{"category", "page", "page_size", "min_open", "total_groups", "results"}) {
		t.Fatal("summary operation lost its closed page contract")
	}
	if string(summary.Parameters[0].Schema.Default) != "1" || string(summary.Parameters[1].Schema.Default) != "0" || summary.Parameters[0].Schema.QueryInteger != "canonical-decimal" || summary.Parameters[1].Schema.QueryInteger != "canonical-decimal" || summary.Parameters[0].Required || summary.Parameters[1].Required || summary.Parameters[0].AllowEmptyValue || summary.Parameters[1].AllowEmptyValue {
		t.Fatal("summary query lost its typed omission or lexical policy")
	}
	summaryItems := summarySchema.Properties["results"]
	if summaryItems.Type != "array" || summaryItems.MaxItems != 20 || summaryItems.Items == nil || summaryItems.Items.AdditionalProperties || !slices.Equal(summaryItems.Items.Required, []string{"priority", "priority_label", "total", "open", "expected_cost_total", "effort_average", "elapsed_total"}) {
		t.Fatal("summary result lost its required bounded groups")
	}
	summaryCost := summaryItems.Items.Properties["expected_cost_total"]
	if len(summaryCost.AnyOf) != 2 || !summaryCost.allowsType("null") || summaryCost.AnyOf[0].Type != "string" || summaryCost.AnyOf[0].MinLength != 1 || summaryCost.AnyOf[0].MaxLength != 2002 || summaryCost.AnyOf[0].Pattern != `^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$` {
		t.Fatal("summary cost must preserve canonical exact results beyond the source precision")
	}
	summaryEffort := summaryItems.Items.Properties["effort_average"]
	if len(summaryEffort.AnyOf) != 2 || !summaryEffort.allowsType("null") || summaryEffort.AnyOf[0].Type != "number" || summaryEffort.AnyOf[0].Format != "double" {
		t.Fatal("summary effort must preserve nullable floating-point results")
	}
	summaryElapsed := summaryItems.Items.Properties["elapsed_total"]
	if len(summaryElapsed.AnyOf) != 2 || !summaryElapsed.allowsType("null") || summaryElapsed.AnyOf[0].Type != "string" || summaryElapsed.AnyOf[0].Pattern != document.Components.Schemas["Ticket"].Properties["elapsed"].AnyOf[0].Pattern {
		t.Fatal("summary elapsed must preserve canonical duration results")
	}
	summaryPriority := summaryItems.Items.Properties["priority"]
	if len(summaryPriority.AnyOf) != 2 || !summaryPriority.allowsType("null") || summaryPriority.AnyOf[0].Format != "int64" || len(summaryPriority.AnyOf[0].Enum) != 0 || string(summaryPriority.AnyOf[0].Minimum) != "-9223372036854775808" || string(summaryPriority.AnyOf[0].Maximum) != "9223372036854775807" {
		t.Fatal("summary priority must preserve SQL null and the complete legacy int64 domain")
	}
	for _, status := range []string{"400", "403", "404"} {
		if summary.Responses[status].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+openapi.ErrorSchemaName {
			t.Fatal("summary failure contract", status)
		}
	}
	raise := document.Paths["/api/tickets/raise-priority/"]["post"]
	selection := document.Components.Schemas["TicketPriorityRaise"]
	ids := selection.Properties["ids"]
	if raise.RequestBody == nil || !raise.RequestBody.Required || raise.RequestBody.Content[api.JSONContentType].Schema.Ref != "#/components/schemas/TicketPriorityRaise" || len(selection.Properties) != 1 || !slices.Equal(selection.Required, []string{"ids"}) || selection.AdditionalProperties || ids.Type != "array" || ids.MinItems != 1 || ids.MaxItems != 40 || ids.Items == nil || ids.Items.Format != "int64" || string(ids.Items.Minimum) != "1" || string(ids.Items.Maximum) != "9223372036854775807" {
		t.Fatal("priority selection's required bounded exact integer contract")
	}
	result := raise.Responses["200"].Content[api.JSONContentType].Schema
	if result.Type != "array" || result.MinItems != 1 || result.MaxItems != 40 || result.Items == nil || result.Items.Ref != "#/components/schemas/Ticket" {
		t.Fatal("priority complete result contract")
	}
	for _, status := range []string{"400", "403", "404", "413", "415"} {
		if raise.Responses[status].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+openapi.ErrorSchemaName {
			t.Fatal("priority failure contract", status)
		}
	}
	bulk := document.Paths["/api/tickets/bulk/"]["post"]
	if bulk.RequestBody == nil || !bulk.RequestBody.Required {
		t.Fatal("bulk body absent")
	}
	for _, entry := range []struct {
		schema helpdeskDocumentSchema
		ref    string
	}{{bulk.RequestBody.Content[api.JSONContentType].Schema, "TicketCreate"}, {bulk.Responses["201"].Content[api.JSONContentType].Schema, "Ticket"}} {
		if entry.schema.Type != "array" || entry.schema.MinItems != 1 || entry.schema.MaxItems != 40 || entry.schema.Items == nil || entry.schema.Items.Ref != "#/components/schemas/"+entry.ref {
			t.Fatal("bulk count or model schema lost")
		}
	}
	for _, status := range []string{"400", "403", "404", "413", "415"} {
		if bulk.Responses[status].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+openapi.ErrorSchemaName {
			t.Fatal("bulk error contract", status)
		}
	}
	bulkUpdate := document.Paths["/api/tickets/bulk/"]["patch"]
	if bulkUpdate.RequestBody == nil || !bulkUpdate.RequestBody.Required {
		t.Fatal("bulk update body absent")
	}
	for _, entry := range []struct {
		schema helpdeskDocumentSchema
		ref    string
	}{
		{bulkUpdate.RequestBody.Content[api.JSONContentType].Schema, "TicketBulkPatch"},
		{bulkUpdate.Responses["200"].Content[api.JSONContentType].Schema, "Ticket"},
	} {
		if entry.schema.Type != "array" || entry.schema.MinItems != 1 || entry.schema.MaxItems != 40 || entry.schema.Items == nil || entry.schema.Items.Ref != "#/components/schemas/"+entry.ref {
			t.Fatal("bulk update array contract")
		}
	}
	for _, status := range []string{"400", "403", "404", "413", "415"} {
		if bulkUpdate.Responses[status].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+openapi.ErrorSchemaName {
			t.Fatal("bulk update error contract", status)
		}
	}
	bulkPatch := document.Components.Schemas["TicketBulkPatch"]
	if !slices.Equal(bulkPatch.Required, []string{"id"}) || len(bulkPatch.Properties) != 17 || bulkPatch.AdditionalProperties || string(bulkPatch.Properties["id"].Minimum) != "1" || string(bulkPatch.Properties["id"].Maximum) != "9223372036854775807" || bulkPatch.Properties["id"].Format != "int64" || len(bulkPatch.Properties["closed"].Default) != 0 {
		t.Fatal("bulk patch lost exact identity or omission policy")
	}
	if _, present := bulkPatch.Properties["external_payload_digest"]; present {
		t.Fatal("bulk patch admitted server digest")
	}
	if _, present := bulkPatch.Properties["category"]; present {
		t.Fatal("bulk patch admitted server category")
	}
	list := document.Paths["/api/tickets/"]["get"]
	create := document.Paths["/api/tickets/"]["post"]
	detail := document.Paths["/api/tickets/{id}/"]["get"]
	listSchema := list.Responses["200"].Content[api.JSONContentType].Schema
	if len(list.Parameters) != 2 || list.Parameters[0].Name != "search" || list.Parameters[1].Name != "source" || list.Parameters[0].In != "query" || list.Parameters[1].In != "query" || list.Responses["400"].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+openapi.ErrorSchemaName || listSchema.Type != "array" || listSchema.Items == nil || listSchema.Items.Ref != "#/components/schemas/Ticket" {
		t.Fatal("list lost its Ticket array or documented search/source parameters")
	}
	if create.RequestBody == nil || !create.RequestBody.Required || create.RequestBody.Content[api.JSONContentType].Schema.Ref != "#/components/schemas/TicketCreate" || create.Responses["201"].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/Ticket" {
		t.Fatal("create input/output does not use the named Ticket contracts")
	}
	for name := range create.Responses["201"].Headers {
		if strings.EqualFold(name, "Location") {
			t.Fatal("create advertises a Location header it does not return")
		}
	}
	if detail.Responses["200"].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/TicketDetail" || len(detail.Parameters) != 1 || detail.Parameters[0].Name != "id" || detail.Parameters[0].In != "path" {
		t.Fatal("detail does not document its relationship projection and path identifier")
	}
	for _, status := range []string{"400", "404", "413", "415"} {
		if _, documented := create.Responses[status]; !documented {
			t.Fatalf("create failure %s is undocumented", status)
		}
	}
	for method, schema := range map[string]string{"put": "TicketUpdate", "patch": "TicketPatch"} {
		operation := document.Paths["/api/tickets/{id}/"][method]
		if operation.RequestBody == nil || !operation.RequestBody.Required || operation.RequestBody.Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+schema || operation.Responses["200"].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/Ticket" || len(operation.Security[0]) != 3 {
			t.Fatal("update request/response or CSRF contract differs")
		}
		for _, status := range []string{"400", "404", "413", "415"} {
			if _, exists := operation.Responses[status]; !exists {
				t.Fatal("update failure is undocumented")
			}
		}
	}
	var session, csrfCookie, csrfHeader string
	for name, scheme := range document.Components.SecuritySchemes {
		switch {
		case scheme.In == "header":
			csrfHeader = name
		case scheme.In == "cookie" && strings.Contains(strings.ToLower(scheme.Name), "csrf"):
			csrfCookie = name
		case scheme.In == "cookie":
			session = name
		}
	}
	if session == "" || csrfCookie == "" || csrfHeader == "" || len(list.Security[0]) != 1 || len(create.Security[0]) != 3 {
		t.Fatal("session read/write security does not preserve the CSRF transport boundary")
	}
	for _, scheme := range []string{session, csrfCookie, csrfHeader} {
		if _, required := create.Security[0][scheme]; !required {
			t.Fatalf("create does not require security scheme %q", scheme)
		}
	}
}

func assertHelpdeskResponseDocumented(t *testing.T, document helpdeskDocument, method, path string, response *httptest.ResponseRecorder) {
	t.Helper()
	status := http.StatusText(response.Code)
	declared, ok := document.Paths[path][strings.ToLower(method)].Responses[strconv.Itoa(response.Code)]
	if !ok {
		t.Fatalf("%s %s returned undocumented status %s", method, path, status)
	}
	if response.Code == http.StatusNoContent {
		if len(declared.Content) != 0 || response.Body.Len() != 0 || response.Header().Get("Content-Type") != "" {
			t.Fatal("no-content response declared or emitted a body")
		}
		return
	}
	mediaType, _, err := mime.ParseMediaType(response.Header().Get("Content-Type"))
	if err != nil {
		t.Fatalf("%s %s returned an invalid content type: %v", method, path, err)
	}
	if _, ok := declared.Content[mediaType]; !ok {
		t.Fatalf("%s %s returned undocumented content type %q", method, path, response.Header().Get("Content-Type"))
	}
}

type helpdeskDocument struct {
	OpenAPI    string
	Paths      map[string]map[string]helpdeskDocumentOperation
	Components struct {
		Schemas         map[string]helpdeskDocumentSchema
		SecuritySchemes map[string]struct{ Type, In, Name string }
	}
}

type helpdeskDocumentOperation struct {
	OperationID           string
	Permission            string   `json:"x-godj-permission"`
	AdditionalPermissions []string `json:"x-godj-additional-permissions"`
	Security              []map[string][]string
	Parameters            []struct {
		Name, In                  string
		Schema                    helpdeskDocumentSchema
		Required, AllowEmptyValue bool
	}
	Responses map[string]struct {
		Content map[string]struct{ Schema helpdeskDocumentSchema }
		Headers map[string]json.RawMessage
	}
	RequestBody *struct {
		Required bool
		Content  map[string]struct{ Schema helpdeskDocumentSchema }
	}
}

type helpdeskDocumentSchema struct {
	Ref                  string `json:"$ref"`
	Type                 string
	Required             []string
	Format               string
	Pattern              string
	Minimum, Maximum     json.RawMessage
	MinItems, MaxItems   int
	MinLength            int
	Properties           map[string]helpdeskDocumentSchema
	Items                *helpdeskDocumentSchema
	AnyOf                []helpdeskDocumentSchema
	Default              json.RawMessage
	Enum                 json.RawMessage
	Normalization        json.RawMessage `json:"x-godj-normalization"`
	QueryInteger         string          `json:"x-godj-query-integer"`
	MaxBytes             int             `json:"x-godj-max-bytes"`
	NoNUL                bool            `json:"x-godj-no-nul"`
	MaxLength            int
	ReadOnly             bool
	AdditionalProperties bool
}

func (schema helpdeskDocumentSchema) allowsType(kind string) bool {
	if schema.Type == kind {
		return true
	}
	for _, alternative := range schema.AnyOf {
		if alternative.allowsType(kind) {
			return true
		}
	}
	return false
}

func decodeHelpdeskDocument(t *testing.T, encoded []byte) helpdeskDocument {
	t.Helper()
	var document helpdeskDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

type helpdeskRecordingAuthentication struct {
	permissions     []auth.Permission
	additional      [][]auth.Permission
	failAt, nilAt   int
	inspectBindings bool
}

func (recording *helpdeskRecordingAuthentication) Require(permission auth.Permission, handler api.AuthenticatedHandler, additional ...auth.Permission) (web.Handler, error) {
	recording.permissions = append(recording.permissions, permission)
	recording.additional = append(recording.additional, append([]auth.Permission(nil), additional...))
	if len(recording.permissions) == recording.failAt {
		return nil, errors.New("injected authentication construction failure")
	}
	if len(recording.permissions) == recording.nilAt {
		return nil, nil
	}
	if recording.inspectBindings {
		// Observe each returned route's actual wrapper, independent of the
		// order used while preparing routes. No application I/O is invoked.
		header := http.Header{"X-Test-Permission": {string(permission)}, "X-Test-Additional": permissionStrings(additional)}
		return func(*web.Request) (web.Response, error) { return web.NewResponse(http.StatusOK, header, nil) }, nil
	}
	return func(request *web.Request) (web.Response, error) { return handler(request, auth.Anonymous()) }, nil
}

type helpdeskDescribedAuthentication struct {
	*helpdeskRecordingAuthentication
}

func (helpdeskDescribedAuthentication) DescribeAuthentication() (api.AuthenticationDescription, error) {
	return api.AuthenticationDescription{Kind: api.AuthenticationSession, SessionCookieName: "helpdesk_session", CSRFCookieName: "helpdesk_csrf", CSRFHeader: "X-Helpdesk-CSRF"}, nil
}

func helpdeskConstructionApplication(t *testing.T) *helpdesk.Application {
	t.Helper()
	application, err := helpdesk.New(helpdeskConstructionBackend{t: t}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return application
}

type helpdeskConstructionBackend struct{ t *testing.T }

func (backend helpdeskConstructionBackend) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	backend.t.Fatal("API construction opened a read snapshot")
	return nil
}

func (backend helpdeskConstructionBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.t.Fatal("API construction performed a query")
	return nil, nil
}
func (backend helpdeskConstructionBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	backend.t.Fatal("API construction inserted data")
	return 0, nil
}
func (backend helpdeskConstructionBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	backend.t.Fatal("API construction updated data")
	return 0, nil
}
func (backend helpdeskConstructionBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	backend.t.Fatal("API construction deleted data")
	return 0, nil
}
func (backend helpdeskConstructionBackend) Atomic(context.Context, func(db.Session) error) error {
	backend.t.Fatal("API construction opened a transaction")
	return nil
}

func (backend helpdeskConstructionBackend) AtomicRelation(context.Context, func(db.RelationSession) error) error {
	backend.t.Fatal("API construction performed a relation transaction")
	return nil
}

func assertServiceReportContracts(t *testing.T, document helpdeskDocument) {
	t.Helper()
	report := document.Components.Schemas["ServiceReport"]
	if !slices.Equal(report.Required, []string{"id", "ticket", "summary", "completed"}) || len(report.Properties) != 4 || report.AdditionalProperties || !report.Properties["id"].ReadOnly || !report.Properties["ticket"].ReadOnly || report.Properties["ticket"].Type != "integer" {
		t.Fatal("report response contract lost exact presence or relation key")
	}
	for _, name := range []string{"ServiceReportCreate", "ServiceReportUpdate", "ServiceReportPatch"} {
		schema := document.Components.Schemas[name]
		if len(schema.Properties) != 3 || schema.AdditionalProperties || schema.Properties["ticket"].Type != "integer" || schema.Properties["summary"].Type != "string" {
			t.Fatal("report input shape", name)
		}
		if name == "ServiceReportPatch" {
			if len(schema.Required) != 0 || len(schema.Properties["completed"].Default) != 0 {
				t.Fatal("partial update imposes defaults")
			}
		} else if !slices.Equal(schema.Required, []string{"ticket", "summary"}) || string(schema.Properties["completed"].Default) != "false" {
			t.Fatal("full report input lost required/default semantics", name)
		}
	}
	save := document.Components.Schemas["ServiceReportSave"]
	result := document.Components.Schemas["ServiceReportSaveResult"]
	if len(save.Properties) != 2 || !slices.Equal(save.Required, []string{"summary"}) || save.Properties["summary"].Type != "string" || string(save.Properties["completed"].Default) != "false" || save.AdditionalProperties {
		t.Fatal("report save input acquired identity or lost full-input defaults")
	}
	if len(result.Properties) != 2 || !slices.Equal(result.Required, []string{"report", "created"}) || result.Properties["report"].Ref != "#/components/schemas/ServiceReport" || result.Properties["created"].Type != "boolean" || result.AdditionalProperties {
		t.Fatal("report save result lost required presence")
	}
	operation := document.Paths["/api/tickets/{id}/service-report/"]["put"]
	if operation.Permission != string(helpdesk.ChangeServiceReport) || !slices.Equal(operation.AdditionalPermissions, []string{string(helpdesk.AddServiceReport), string(helpdesk.ViewServiceReport)}) || operation.RequestBody == nil || !operation.RequestBody.Required || operation.RequestBody.Content[api.JSONContentType].Schema.Ref != "#/components/schemas/ServiceReportSave" || len(operation.Parameters) != 1 || operation.Parameters[0].In != "path" || operation.Parameters[0].Name != "id" {
		t.Fatal("report save route, input or admission contract")
	}
	for _, status := range []string{"200", "201"} {
		if operation.Responses[status].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/ServiceReportSaveResult" {
			t.Fatal("report save outcome lost its explicit branch")
		}
	}
	for _, status := range []string{"400", "403", "404", "413", "415"} {
		if operation.Responses[status].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+openapi.ErrorSchemaName {
			t.Fatal("report save failure missing", status)
		}
	}
	list := document.Paths["/api/service-reports/"]["get"]
	if len(list.Parameters) != 0 || list.Responses["200"].Content[api.JSONContentType].Schema.Items.Ref != "#/components/schemas/ServiceReport" {
		t.Fatal("report list contract")
	}
	nullable := document.Paths["/api/tickets/{id}/service-report/"]["get"].Responses["200"].Content[api.JSONContentType].Schema
	if len(nullable.AnyOf) != 2 || nullable.AnyOf[0].Ref != "#/components/schemas/ServiceReport" || !nullable.allowsType("null") {
		t.Fatal("reverse report lost normal absence")
	}
	for _, entry := range []struct{ path, method, schema, status string }{{"/api/service-reports/", "post", "ServiceReportCreate", "201"}, {"/api/service-reports/{id}/", "put", "ServiceReportUpdate", "200"}, {"/api/service-reports/{id}/", "patch", "ServiceReportPatch", "200"}} {
		op := document.Paths[entry.path][entry.method]
		if op.RequestBody == nil || !op.RequestBody.Required || op.RequestBody.Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+entry.schema || op.Responses[entry.status].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/ServiceReport" {
			t.Fatal("report write contract", entry)
		}
		for _, status := range []string{"400", "403", "404", "413", "415"} {
			if op.Responses[status].Content[api.JSONContentType].Schema.Ref != "#/components/schemas/"+openapi.ErrorSchemaName {
				t.Fatal("missing report failure", entry, status)
			}
		}
	}
	removal := document.Paths["/api/service-reports/{id}/"]["delete"]
	response, found := removal.Responses["204"]
	if !found || len(response.Content) != 0 || removal.RequestBody != nil {
		t.Fatal("report deletion not represented as no content")
	}
}

// Construction tests must never turn missing persistence into successful audit.
func helpdeskAPIConfig(authentication api.Authentication) helpdesk.APIConfig {
	return helpdesk.APIConfig{Authentication: authentication, AppendAudit: func(context.Context, db.Session, admin.PreparedEvent) error {
		return errors.New("construction-only audit callback invoked")
	}}
}
