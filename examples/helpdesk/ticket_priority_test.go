package helpdesk_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

func (session *bulkTicketSession) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) error {
	return session.RelationSession.(db.QueryUpdater).CheckQueryUpdate(ctx, plan)
}
func (session *bulkTicketSession) QueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (int64, error) {
	owner := session.owner
	owner.queryUpdates++
	if owner.mode == "first_statement_error" || owner.mode == "second_statement_error" && owner.queryUpdates == 2 {
		return 0, errors.New("private priority statement failure")
	}
	if owner.queryUpdates == 1 && slices.Contains([]string{"query_late_scope", "query_late_priority", "query_late_missing"}, owner.mode) {
		row, err := models.TicketObjects.Using(session.RelationSession).Filter(models.TicketFields.ID.Exact(owner.priorityKeys[0])).Get(ctx)
		if err != nil {
			return 0, err
		}
		switch owner.mode {
		case "query_late_scope":
			_, err = models.TicketObjects.Patch(ctx, session.RelationSession, row, models.TicketPatch{}.WithCategoryID(owner.outside))
		case "query_late_priority":
			_, err = models.TicketObjects.Patch(ctx, session.RelationSession, row, models.TicketPatch{}.WithPriority(0))
		case "query_late_missing":
			_, err = models.TicketObjects.Delete(ctx, session.RelationSession, &row)
		}
		if err != nil {
			return 0, err
		}
	}
	count, err := session.RelationSession.(db.QueryUpdater).QueryUpdate(ctx, plan)
	if err == nil {
		owner.keys = slices.Clone(owner.priorityKeys)
		if owner.mode == "partial_query_count" {
			count--
		}
		if owner.mode == "canceled_bulk" {
			owner.cancel()
		}
	}
	return count, err
}

func verifyHelpdeskPriorityRaise(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Priority outside"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("private outside priority", outside.ID).WithPriority(-1))
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{"api", "admin"} {
		t.Run(surface, func(t *testing.T) {
			cases := []string{"mixed", "unchanged", "maximum", "digest", "missing", "foreign", "first_statement_error", "second_statement_error", "partial_query_count", "query_late_scope", "query_late_priority", "query_late_missing", "late_ticket_scope", "late_label_scope", "stored_foreign_labels", "unchanged_foreign_labels", "output_error", "aggregate_output_budget", "audit_error", "canceled_audit", "canceled_bulk", "post_commit_cancel", "missing_callback", "nil_session", "repeated_callback", "concurrent_callback", "swallowed_callback", "rollback_unknown", "commit_unknown", "denied_change", "denied_label", "csrf", "duplicate_csrf", "query", "empty", "too_many"}
			if surface == "api" {
				cases = append(cases, "duplicate_ids", "missing_ids", "zero_id", "negative_id", "fractional_id", "exponent_id", "boolean_id", "string_id", "overflow_id", "null_id", "null_ids", "scalar_ids", "unknown_field", "forged_priority", "duplicate_field", "array_body", "trailing_data", "body_limit", "denied_before_body")
			} else {
				cases = append(cases, "denied_view", "unknown_form")
			}
			for _, mode := range cases {
				t.Run(mode, func(t *testing.T) {
					category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Priority "+surface+" "+mode))
					if err != nil {
						t.Fatal(err)
					}
					label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate(fmt.Sprintf("Priority owned %d <&>", category.ID), category.ID))
					if err != nil {
						t.Fatal(err)
					}
					count := 6
					if mode == "maximum" || mode == "aggregate_output_budget" {
						count = 40
					}
					if mode == "too_many" {
						count = 41
					}
					originals := make([]models.Ticket, count)
					keys := make([]int64, count)
					links := make(map[int64]map[int64]int64)
					expected := make(map[int64]int64)
					changed := make(map[int64]bool)
					for index := range originals {
						input := models.NewTicketCreate(fmt.Sprintf("Priority %02d <&>", index), category.ID).WithClosed(index%2 == 0)
						value := []int64{-1, 0, 0, 1, math.MinInt64, math.MaxInt64}[index%6]
						unset := index%6 == 2
						if mode == "unchanged" || mode == "unchanged_foreign_labels" {
							value, unset = 1, false
						}
						if !unset {
							input = input.WithPriority(value)
						}
						if mode == "digest" || mode == "unchanged" {
							payload, err := jsonvalue.Parse([]byte(`{"large":9007199254740993}`))
							if err != nil {
								t.Fatal(err)
							}
							input = input.WithExternalPayload(payload)
						}
						if mode == "aggregate_output_budget" {
							input = input.WithResolution(strings.Repeat("x", 30000))
						}
						row, err := models.TicketObjects.Create(ctx, runtime, input)
						if err != nil {
							t.Fatal(err)
						}
						originals[index], keys[index] = row, row.ID
						if mode != "query_late_missing" {
							if _, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(row.ID, label.ID)); err != nil {
								t.Fatal(err)
							}
						}
						links[row.ID] = collectionLinks(t, ctx, runtime, row.ID)
						expected[row.ID], changed[row.ID] = value, unset || value == -1 || value == 0
						if unset {
							expected[row.ID] = 0
						} else if changed[row.ID] {
							expected[row.ID]++
						}
					}
					if mode == "stored_foreign_labels" || mode == "unchanged_foreign_labels" {
						if _, err := models.LabelObjects.Patch(ctx, runtime, label, models.LabelPatch{}.WithCategoryID(outside.ID)); err != nil {
							t.Fatal(err)
						}
					}
					backend := &bulkTicketBackend{Backend: runtime, category: category.ID, outside: outside.ID, label: label.ID, priorityKeys: slices.Clone(keys)}
					application, err := helpdesk.New(backend, category.ID)
					if err != nil {
						t.Fatal(err)
					}
					work, cancel := context.WithCancel(ctx)
					defer cancel()
					backend.cancel = cancel
					audits := 0
					appendAudit := func(work context.Context, session db.Session, event admin.PreparedEvent) error {
						audits++
						if err := runtime.AppendAudit(work, session, event); err != nil {
							return err
						}
						if audits == 2 && slices.Contains([]string{"audit_error", "swallowed_callback", "rollback_unknown"}, mode) {
							return errors.New("private priority audit failure")
						}
						if audits == 2 && mode == "canceled_audit" {
							cancel()
							return work.Err()
						}
						return nil
					}
					registry, err := application.AdminRegistry(helpdesk.AdminConfig{AppendAudit: appendAudit})
					if err != nil {
						t.Fatal(err)
					}
					var deny helpdeskDeniedPermissions
					switch mode {
					case "denied_change", "denied_before_body":
						deny = []auth.Permission{helpdesk.ChangeTicket}
					case "denied_label":
						deny = []auth.Permission{helpdesk.ViewLabel}
					case "denied_view":
						deny = []auth.Permission{helpdesk.ViewTicket}
					}
					client := helpdeskHTTPRegistry(t, application, registry, runtime, deny, appendAudit)
					client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
					if response := client.request("GET", "/admin/", "", false); response.Code != 200 {
						t.Fatal("priority session admission", response.Code)
					}
					ids := slices.Clone(keys)
					slices.Reverse(ids)
					path, want, committed := "/api/tickets/raise-priority/", 200, true
					if surface == "admin" {
						path, want = "/admin/tickets/action/raise-priority/", 302
					}
					switch mode {
					case "missing":
						ids[1], want, committed = 1<<60, 404, false
					case "foreign":
						ids[1], want, committed = foreign.ID, 404, false
					case "partial_query_count", "query_late_scope", "query_late_priority", "query_late_missing", "late_ticket_scope":
						want, committed = 404, false
					case "first_statement_error", "second_statement_error", "late_label_scope", "stored_foreign_labels", "unchanged_foreign_labels", "output_error", "aggregate_output_budget", "audit_error", "canceled_audit", "canceled_bulk", "missing_callback", "nil_session", "repeated_callback", "concurrent_callback", "swallowed_callback", "rollback_unknown":
						want, committed = 500, false
					case "commit_unknown":
						want = 500
					case "denied_change", "denied_label", "denied_view", "denied_before_body", "csrf", "duplicate_csrf":
						want, committed = 403, false
					case "body_limit":
						want, committed = 413, false
					case "mixed", "unchanged", "maximum", "digest", "post_commit_cancel":
					default:
						want, committed = 400, false
					}
					bodyBytes, err := json.Marshal(map[string]any{"ids": ids})
					if err != nil {
						t.Fatal(err)
					}
					body := string(bodyBytes)
					if mode == "query" {
						path += "?forged=1"
					}
					if surface == "api" {
						switch mode {
						case "empty":
							body = `{"ids":[]}`
						case "duplicate_ids":
							body = fmt.Sprintf(`{"ids":[%d,%d]}`, ids[0], ids[0])
						case "missing_ids":
							body = `{}`
						case "zero_id":
							body = `{"ids":[0]}`
						case "negative_id":
							body = `{"ids":[-1]}`
						case "fractional_id":
							body = `{"ids":[1.0]}`
						case "exponent_id":
							body = `{"ids":[1e0]}`
						case "boolean_id":
							body = `{"ids":[true]}`
						case "string_id":
							body = `{"ids":["1"]}`
						case "overflow_id":
							body = `{"ids":[9223372036854775808]}`
						case "null_id":
							body = `{"ids":[null]}`
						case "null_ids":
							body = `{"ids":null}`
						case "scalar_ids":
							body = `{"ids":1}`
						case "unknown_field":
							body = fmt.Sprintf(`{"ids":[%d],"unknown":true}`, ids[0])
						case "forged_priority":
							body = fmt.Sprintf(`{"ids":[%d],"priority":1}`, ids[0])
						case "duplicate_field":
							body = `{"ids":[1],"ids":[2]}`
						case "array_body":
							body = `[{"ids":[1]}]`
						case "trailing_data":
							body += `true`
						case "body_limit":
							body = strings.Repeat(" ", 4097)
						case "denied_before_body":
							body = `not JSON`
						}
					} else {
						form := url.Values{"csrfmiddlewaretoken": {client.csrf}}
						for _, id := range ids {
							form.Add("selected", strconv.FormatInt(id, 10))
						}
						if mode == "empty" {
							form.Del("selected")
						}
						if mode == "csrf" {
							form.Del("csrfmiddlewaretoken")
						}
						if mode == "duplicate_csrf" {
							form.Add("csrfmiddlewaretoken", client.csrf)
						}
						if mode == "unknown_form" {
							form.Set("forged", "1")
						}
						body = form.Encode()
					}
					if mode == "csrf" {
						client.csrf = ""
					}
					if mode == "duplicate_csrf" && surface == "api" {
						client.csrf += "," + client.csrf
					}
					backend.mode = mode
					response := client.requestContext(work, "POST", path, body, surface == "api")
					if response.Code != want {
						t.Fatalf("priority %s/%s: %d want %d: %s", surface, mode, response.Code, want, response.Body.String())
					}
					if strings.Contains(response.Body.String(), "private") {
						t.Fatal("priority response leaked private data")
					}
					changedCount := 0
					for _, original := range originals {
						row, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(original.ID)).Get(ctx)
						if err != nil {
							t.Fatal(err)
						}
						history, err := runtime.AuditHistory(ctx, "helpdesk.ticket", row.ID, 10)
						if err != nil {
							t.Fatal(err)
						}
						if !maps.Equal(collectionLinks(t, ctx, runtime, row.ID), links[row.ID]) {
							t.Fatal("priority command changed label links")
						}
						if !committed || !changed[row.ID] {
							if !reflect.DeepEqual(row, original) || len(history) != 0 {
								t.Fatal("failed/no-op priority command changed row or audit", row.ID)
							}
							continue
						}
						changedCount++
						if row.Priority == nil || *row.Priority != expected[row.ID] || len(history) != 1 || history[0].Action != admin.ActionChange || history[0].DisplayLabel != row.Subject {
							t.Fatal("priority/audit persistence", row.ID, history)
						}
						fields := []string{"priority"}
						if mode == "digest" {
							sum := sha256.Sum256([]byte(row.ExternalPayload.Text))
							if row.ExternalPayloadDigest == nil || row.ExternalPayloadDigest.Data != string(sum[:]) {
								t.Fatal("priority digest not derived from stored JSON")
							}
							fields = append(fields, "external_payload_digest")
						}
						if !slices.Equal(history[0].ChangedFields, fields) {
							t.Fatal("priority audit fields", history[0].ChangedFields, fields)
						}
						before := row
						before.Priority, before.ExternalPayloadDigest = original.Priority, original.ExternalPayloadDigest
						if !reflect.DeepEqual(before, original) {
							t.Fatal("priority assignment changed an unrelated field")
						}
					}
					if committed {
						statements := 2
						if mode == "unchanged" {
							statements = 0
						}
						if backend.atomics != 1 || backend.queryUpdates != statements || backend.updates != 0 || backend.bulks != 0 || backend.singleInserts != 0 || audits != changedCount {
							t.Fatal("priority native expression ownership", backend.atomics, backend.queryUpdates, statements, audits, changedCount)
						}
						if mode != "digest" && backend.singleUpdates != 0 {
							t.Fatal("priority fell back to per-row updates")
						}
						if surface == "api" && want == 200 {
							var rows []struct {
								ID       int64
								Priority *int64
							}
							if json.Unmarshal(response.Body.Bytes(), &rows) != nil || len(rows) != len(ids) {
								t.Fatal("priority complete response")
							}
							for index, row := range rows {
								if row.ID != ids[index] || row.Priority == nil || *row.Priority != expected[row.ID] {
									t.Fatal("priority output order/value")
								}
							}
						}
						if surface == "admin" && want == 302 {
							notice := client.requestContext(ctx, "GET", response.Header().Get("Location"), "", false)
							if notice.Code != 200 || !strings.Contains(notice.Body.String(), fmt.Sprintf(`data-admin-message="priority-raised" data-affected="%d"`, changedCount)) || !strings.Contains(notice.Body.String(), fmt.Sprintf("Priority raised for %d ticket(s).", changedCount)) {
								t.Fatal("priority notice actual changes", notice.Code)
							}
						}
					}
					if surface == "admin" && want != 302 && response.Header().Get("Location") != "" {
						t.Fatal("unconfirmed priority command published success")
					}
					if mode == "second_statement_error" && backend.queryUpdates != 2 {
						t.Fatal("later failure did not follow numeric UPDATE")
					}
					if mode == "audit_error" && audits != 2 {
						t.Fatal("later audit failure not reached")
					}
					if mode == "aggregate_output_budget" && (backend.queryUpdates != 2 || audits != 0) {
						t.Fatal("complete output was not bounded before audit")
					}
					if len(deny) != 0 || mode == "csrf" || mode == "duplicate_csrf" || want == 400 || want == 413 {
						if backend.atomics != 0 || backend.queryUpdates != 0 || audits != 0 {
							t.Fatal("priority input/admission failure reached writer")
						}
					}
					if backend.retained != nil {
						reads := backend.reads
						err := runtime.AtomicRelation(ctx, func(session db.RelationSession) error { return backend.retained(session) })
						if err == nil || backend.reads != reads {
							t.Fatal("escaped priority callback remained live")
						}
					}
				})
			}
		})
	}
}
