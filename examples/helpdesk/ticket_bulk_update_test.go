package helpdesk_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/uuid"
)

func verifyHelpdeskBulkUpdates(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Bulk update outside"))
	if err != nil {
		t.Fatal(err)
	}
	foreignLabel, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("private foreign update label", outside.ID))
	if err != nil {
		t.Fatal(err)
	}
	unique, err := uuid.Parse("bf0e8ae7-13b8-4fda-b3f3-3db240000001")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("private foreign update ticket", outside.ID).WithExternalReference(unique))
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{"api", "admin"} {
		t.Run(surface, func(t *testing.T) {
			cases := []string{"updated", "unchanged", "multiple_batches", "missing", "foreign", "update_second_batch_error", "partial_update_count", "update_late_scope", "update_late_missing", "late_ticket_scope", "late_label_scope", "stored_foreign_labels", "unchanged_foreign_labels", "output_error", "audit_error", "canceled_audit", "canceled_bulk", "post_commit_cancel", "missing_callback", "nil_session", "repeated_callback", "concurrent_callback", "swallowed_callback", "rollback_unknown", "commit_unknown", "denied_change", "denied_label", "csrf", "duplicate_csrf", "query", "empty", "too_many", "aggregate_output_budget"}
			if surface == "api" {
				cases = append(cases, "different_masks", "labels_only", "payload_and_scalars", "duplicate_ids", "missing_id", "zero_id", "negative_id", "fractional_id", "boolean_id", "string_id", "overflow_id", "invalid_second", "duplicate_reference", "existing_reference", "update_native_conflict", "update_native_later_conflict", "foreign_label", "link_error", "forged_category", "forged_digest", "unknown_field", "duplicate_field", "scalar_item", "body_limit", "per_item_limit", "denied_before_body")
			} else {
				cases = append(cases, "reopen", "denied_view", "unknown_form")
			}
			for _, mode := range cases {
				t.Run(mode, func(t *testing.T) {
					category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Update "+surface+" "+mode))
					if err != nil {
						t.Fatal(err)
					}
					label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate(fmt.Sprintf("Kept label %d", category.ID), category.ID))
					if err != nil {
						t.Fatal(err)
					}
					nextLabel, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("New label", category.ID))
					if err != nil {
						t.Fatal(err)
					}
					count := 2
					if mode == "multiple_batches" || mode == "aggregate_output_budget" {
						count = 40
					}
					if mode == "update_second_batch_error" || mode == "update_native_later_conflict" {
						count = 21
					}
					if mode == "too_many" {
						count = 41
					}
					originals := make([]models.Ticket, count)
					keys := make([]int64, count)
					oldLinks := make(map[int64]map[int64]int64)
					for index := range originals {
						resolution := "preserved excluded text"
						if mode == "aggregate_output_budget" {
							resolution = strings.Repeat("x", 30_000)
						}
						input := models.NewTicketCreate(fmt.Sprintf("Original %02d <&>", index), category.ID).WithResolution(resolution).WithClosed(mode == "reopen")
						if mode == "unchanged" || mode == "unchanged_foreign_labels" {
							payload, e := jsonvalue.Parse([]byte(`{"legacy":1}`))
							if e != nil {
								t.Fatal(e)
							}
							input = input.WithExternalPayload(payload)
						}
						originals[index], err = models.TicketObjects.Create(ctx, runtime, input)
						if err != nil {
							t.Fatal(err)
						}
						keys[index] = originals[index].ID
						if mode != "update_late_missing" {
							if _, err = models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(keys[index], label.ID)); err != nil {
								t.Fatal(err)
							}
						}
						oldLinks[keys[index]] = collectionLinks(t, ctx, runtime, keys[index])
					}
					if mode == "stored_foreign_labels" || mode == "unchanged_foreign_labels" {
						if _, err := models.LabelObjects.Update(ctx, runtime, label, models.LabelPatch{}.WithCategoryID(outside.ID)); err != nil {
							t.Fatal(err)
						}
					}
					backend := &bulkTicketBackend{Backend: runtime, category: category.ID, outside: outside.ID, label: label.ID}
					application, err := helpdesk.New(backend, category.ID)
					if err != nil {
						t.Fatal(err)
					}
					requestContext, cancel := context.WithCancel(ctx)
					defer cancel()
					backend.cancel = cancel
					audits := 0
					appendAudit := func(work context.Context, session db.Session, event admin.PreparedEvent) error {
						audits++
						if err := runtime.AppendAudit(work, session, event); err != nil {
							return err
						}
						if audits == 2 && (mode == "audit_error" || mode == "rollback_unknown" || mode == "swallowed_callback") {
							return errors.New("private later update audit failure")
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
					if page := client.request("GET", "/admin/", "", false); page.Code != 200 {
						t.Fatal("update session admission", page.Code)
					}
					ids := slices.Clone(keys)
					slices.Reverse(ids)
					inputs := make([]map[string]any, len(ids))
					for index, id := range ids {
						inputs[index] = map[string]any{"id": id, "closed": true}
					}
					method, path, want, committed := "PATCH", "/api/tickets/bulk/", 200, true
					if surface == "admin" {
						method, path, want = "POST", "/admin/tickets/action/close/", 302
					}
					switch mode {
					case "unchanged", "unchanged_foreign_labels":
						for _, input := range inputs {
							delete(input, "closed")
						}
						if surface == "admin" {
							path = "/admin/tickets/action/reopen/"
						}
					case "reopen":
						path = "/admin/tickets/action/reopen/"
					case "different_masks":
						inputs[0]["subject"] = "Changed first"
						delete(inputs[0], "closed")
					case "labels_only":
						for _, input := range inputs {
							delete(input, "closed")
							input["labels"] = []int64{label.ID, nextLabel.ID}
						}
					case "payload_and_scalars":
						inputs[0]["external_payload"] = json.RawMessage(`{"":9007199254740993,"normalized":1e2}`)
						inputs[0]["reviewed"] = false
						inputs[0]["expected_cost"] = "12.30"
						inputs[0]["service_at"] = "00:00:00"
						inputs[0]["labels"] = []int64{}
						inputs[1]["details"] = nil
						inputs[1]["labels"] = []int64{label.ID, nextLabel.ID, label.ID}
					case "missing":
						inputs[1]["id"], ids[1], want, committed = int64(1<<60), int64(1<<60), 404, false
					case "foreign":
						inputs[1]["id"], ids[1], want, committed = foreign.ID, foreign.ID, 404, false
					case "duplicate_ids":
						inputs[1]["id"], want, committed = ids[0], 400, false
					case "missing_id":
						delete(inputs[1], "id")
						want, committed = 400, false
					case "zero_id":
						inputs[1]["id"], want, committed = 0, 400, false
					case "negative_id":
						inputs[1]["id"], want, committed = -1, 400, false
					case "fractional_id":
						inputs[1]["id"], want, committed = json.RawMessage(`1.0`), 400, false
					case "boolean_id":
						inputs[1]["id"], want, committed = true, 400, false
					case "string_id":
						inputs[1]["id"], want, committed = "1", 400, false
					case "overflow_id":
						inputs[1]["id"], want, committed = json.RawMessage(`9223372036854775808`), 400, false
					case "invalid_second":
						inputs[1]["subject"], want, committed = "", 400, false
					case "duplicate_reference":
						for _, input := range inputs {
							input["external_reference"] = "bf0e8ae7-13b8-4fda-b3f3-3db240000002"
						}
						want, committed = 400, false
					case "existing_reference", "update_native_conflict", "update_native_later_conflict":
						inputs[len(inputs)-1]["external_reference"], want, committed = unique.String(), 400, false
					case "foreign_label":
						inputs[1]["labels"], want, committed = []int64{foreignLabel.ID}, 400, false
					case "link_error":
						for _, input := range inputs {
							input["labels"] = []int64{label.ID, nextLabel.ID}
						}
						want, committed = 500, false
					case "forged_category":
						inputs[1]["category"], want, committed = outside.ID, 400, false
					case "forged_digest":
						inputs[1]["external_payload_digest"], want, committed = "AAAA", 400, false
					case "unknown_field":
						inputs[1]["unrecognized"], want, committed = true, 400, false
					case "empty", "too_many", "query", "duplicate_field", "scalar_item", "per_item_limit", "unknown_form":
						want, committed = 400, false
					case "body_limit":
						want, committed = 413, false
					case "denied_change", "denied_label", "denied_view", "denied_before_body", "csrf", "duplicate_csrf":
						want, committed = 403, false
					case "partial_update_count", "update_late_scope", "update_late_missing", "late_ticket_scope":
						want, committed = 404, false
					case "update_second_batch_error", "late_label_scope", "stored_foreign_labels", "output_error", "audit_error", "canceled_audit", "canceled_bulk", "missing_callback", "nil_session", "repeated_callback", "concurrent_callback", "swallowed_callback", "rollback_unknown", "aggregate_output_budget":
						want, committed = 500, false
					case "commit_unknown":
						want = 500
					}
					if mode == "unchanged_foreign_labels" || mode == "stored_foreign_labels" {
						want, committed = 500, false
					}
					if mode == "query" {
						path += "?forged=yes"
					}
					data, err := json.Marshal(inputs)
					if err != nil {
						t.Fatal(err)
					}
					body := string(data)
					if surface == "api" {
						switch mode {
						case "empty":
							body = "[]"
						case "duplicate_field":
							body = fmt.Sprintf(`[{"id":%d,"id":%d,"closed":true}]`, ids[0], ids[1])
						case "scalar_item":
							body = `[false]`
						case "body_limit":
							body = strings.Repeat(" ", 163841)
						case "per_item_limit":
							body = fmt.Sprintf(`[{"id":%d,"resolution":"%s"}]`, ids[0], strings.Repeat("x", 4096))
						case "denied_before_body":
							body = "not JSON"
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
					response := client.requestContext(requestContext, method, path, body, surface == "api")
					if response.Code != want {
						t.Fatalf("bulk update %s/%s status=%d want=%d body=%s", surface, mode, response.Code, want, response.Body.String())
					}
					if strings.Contains(response.Body.String(), "private") {
						t.Fatal("bulk update disclosed private data")
					}
					stored, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.In(keys...)).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
					if err != nil || len(stored) != len(originals) {
						t.Fatal("bulk update lost an original row", len(stored), err)
					}
					for index, row := range stored {
						links := collectionLinks(t, ctx, runtime, row.ID)
						history, err := runtime.AuditHistory(ctx, "helpdesk.ticket", row.ID, 10)
						if err != nil {
							t.Fatal(err)
						}
						if !committed || mode == "unchanged" {
							if !reflect.DeepEqual(row, originals[index]) || !maps.Equal(links, oldLinks[row.ID]) || len(history) != 0 {
								t.Fatal("failed/no-op bulk update changed stored rows, links or audit", row.ID, len(history))
							}
							continue
						}
						if row.CategoryID != category.ID || row.Resolution == nil || *row.Resolution != "preserved excluded text" || len(history) != 1 || history[0].Action != admin.ActionChange || history[0].DisplayLabel != row.Subject {
							t.Fatal("bulk update scope/excluded fields/audit", row.ID, history)
						}
						wantClosed := mode != "reopen" && mode != "labels_only"
						if mode == "different_masks" && row.ID == ids[0] {
							wantClosed = false
							if row.Subject != "Changed first" {
								t.Fatal("per-row mask did not change subject")
							}
						}
						if row.Closed != wantClosed {
							t.Fatal("bulk closed value", row.ID, row.Closed, wantClosed)
						}
						if mode != "labels_only" && mode != "payload_and_scalars" {
							if !maps.Equal(links, oldLinks[row.ID]) {
								t.Fatal("omission changed label membership")
							}
						} else if mode == "payload_and_scalars" && row.ID == ids[0] {
							if len(links) != 0 {
								t.Fatal("explicit empty labels did not clear")
							}
						} else if len(links) != 2 || links[label.ID] != oldLinks[row.ID][label.ID] || links[nextLabel.ID] == 0 {
							t.Fatal("bulk relation update lost retained link identity")
						}
						if mode == "payload_and_scalars" && row.ID == ids[0] {
							if row.ExternalPayload == nil || row.ExternalPayloadDigest == nil || row.Reviewed == nil || *row.Reviewed || row.ExpectedCost == nil || row.ServiceAt == nil {
								t.Fatal("bulk scalar presence lost")
							}
							cost, err := row.ExpectedCost.Fixed(2)
							if err != nil || cost != "12.30" {
								t.Fatal("bulk decimal value", cost, err)
							}
							sum := sha256.Sum256([]byte(row.ExternalPayload.Text))
							if row.ExternalPayloadDigest.Base64() != base64.StdEncoding.EncodeToString(sum[:]) {
								t.Fatal("stored bulk payload digest differs")
							}
						}
					}
					if committed {
						wantUpdates := (count + 19) / 20
						if mode == "unchanged" || mode == "labels_only" {
							wantUpdates = 0
						}
						if mode == "different_masks" || mode == "payload_and_scalars" {
							wantUpdates = 2
						}
						wantAudits := count
						if mode == "unchanged" {
							wantAudits = 0
						}
						if backend.atomics != 1 || backend.updates != wantUpdates || backend.bulks != 0 || backend.singleInserts != 0 || audits != wantAudits {
							t.Fatal("native bulk update ownership/count", backend.atomics, backend.updates, wantUpdates, backend.bulks, audits, wantAudits)
						}
						if mode != "payload_and_scalars" && backend.singleUpdates != 0 {
							t.Fatal("scalar batch fell back to single updates", backend.singleUpdates)
						}
						if mode == "multiple_batches" && !slices.Equal(backend.updateRows, []int{20, 20}) {
							t.Fatal("batch budget", backend.updateRows)
						}
						if mode == "different_masks" && !reflect.DeepEqual(backend.updateMasks, [][]string{{"subject"}, {"closed"}}) {
							t.Fatal("different masks widened fields", backend.updateMasks)
						}
						if surface == "api" && want == 200 {
							var result []collectionTicketJSON
							if json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result) != len(ids) {
								t.Fatal("bulk update response envelope")
							}
							for index, row := range result {
								if row.ID != ids[index] {
									t.Fatal("bulk update result lost input order")
								}
							}
						}
					}
					if mode == "update_second_batch_error" || mode == "update_native_later_conflict" {
						if backend.updates != 2 || len(backend.keys) != 20 {
							t.Fatal("late failure lacked real first batch", backend.updates, len(backend.keys))
						}
					}
					if mode == "aggregate_output_budget" && (backend.updates != 2 || audits != 0) {
						t.Fatal("whole output limit was not checked before audit")
					}
					if mode == "audit_error" && audits != 2 {
						t.Fatal("late audit failure was not reached")
					}
					if len(deny) != 0 || mode == "csrf" || mode == "duplicate_csrf" {
						if backend.atomics != 0 || backend.updates != 0 || audits != 0 {
							t.Fatal("admission reached writer")
						}
					}
					if slices.Contains([]string{"invalid_second", "duplicate_reference", "existing_reference", "foreign_label", "duplicate_ids"}, mode) {
						if backend.updates != 0 || !strings.Contains(response.Body.String(), `"key":"index","value":"1"`) {
							t.Fatal("complete input validation lost row/no-write boundary", response.Body.String())
						}
					}
					if backend.retained != nil {
						reads := backend.reads
						err := runtime.AtomicRelation(ctx, func(session db.RelationSession) error { return backend.retained(session) })
						if err == nil || backend.reads != reads {
							t.Fatal("retained transaction callback remained usable", err)
						}
					}
				})
			}
		})
	}
}
