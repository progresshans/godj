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
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/uuid"
)

type bulkTicketBackend struct {
	helpdesk.Backend
	mode                                                    string
	category, outside, label                                int64
	atomics, savepoints, reads, bulks, singleInserts, links int
	batchRows                                               []int
	keys                                                    []int64
	cancel                                                  context.CancelFunc
	retained                                                func(db.RelationSession) error
}

func (backend *bulkTicketBackend) AtomicRelation(ctx context.Context, run func(db.RelationSession) error) error {
	backend.atomics++
	if backend.mode == "missing_callback" {
		backend.retained = run
		return nil
	}
	err := backend.Backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		if backend.mode == "late_category" {
			category := models.NewCategoryWithID(backend.category)
			if _, err := models.CategoryObjects.Delete(ctx, session, &category); err != nil {
				return err
			}
		}
		wrapped := &bulkTicketSession{RelationSession: session, owner: backend}
		if backend.mode == "nil_session" {
			return run(nil)
		}
		if backend.mode == "concurrent_callback" {
			failures := make(chan error, 2)
			var workers sync.WaitGroup
			for range 2 {
				workers.Go(func() { failures <- run(wrapped) })
			}
			workers.Wait()
			return errors.Join(<-failures, <-failures)
		}
		err := run(wrapped)
		if err == nil && backend.mode == "repeated_callback" {
			return run(wrapped)
		}
		return err
	})
	if backend.mode == "swallowed_callback" {
		return nil
	}
	if err == nil && backend.mode == "commit_unknown" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	if err != nil && backend.mode == "rollback_unknown" {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
	}
	if err == nil && backend.mode == "post_commit_cancel" {
		backend.cancel()
	}
	return err
}

type bulkTicketSession struct {
	db.RelationSession
	owner *bulkTicketBackend
}

func (session *bulkTicketSession) ValidateSession(ctx context.Context) error {
	return session.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}

func (session *bulkTicketSession) Savepoint(ctx context.Context, run func(db.Session) error) error {
	session.owner.savepoints++
	err := db.WithSavepoint(ctx, session.RelationSession, func(child db.Session) error {
		relation, ok := child.(db.RelationSession)
		if !ok {
			return errors.New("bulk fixture child lost relation capability")
		}
		return run(&bulkTicketSession{RelationSession: relation, owner: session.owner})
	})
	if err != nil {
		return err
	}
	backend := session.owner
	if backend.mode == "late_ticket_scope" && len(backend.keys) > 0 {
		value, err := models.TicketObjects.Using(session.RelationSession).Filter(models.TicketFields.ID.Exact(backend.keys[0])).Get(ctx)
		if err != nil {
			return err
		}
		_, err = models.TicketObjects.Update(ctx, session.RelationSession, value, models.TicketPatch{}.WithCategoryID(backend.outside))
		return err
	}
	if backend.mode == "late_label_scope" {
		value, err := models.LabelObjects.Using(session.RelationSession).Filter(models.LabelFields.ID.Exact(backend.label)).Get(ctx)
		if err != nil {
			return err
		}
		_, err = models.LabelObjects.Update(ctx, session.RelationSession, value, models.LabelPatch{}.WithCategoryID(backend.outside))
		return err
	}
	return nil
}

func (session *bulkTicketSession) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	return session.RelationSession.(db.BulkInserter).BulkInsertLimits(ctx)
}

func (session *bulkTicketSession) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	backend := session.owner
	backend.bulks++
	backend.batchRows = append(backend.batchRows, len(plan.Rows()))
	if backend.mode == "second_batch_error" && backend.bulks == 2 {
		return db.BulkInsertResult{}, errors.New("private later bulk failure")
	}
	result, err := session.RelationSession.(db.BulkInserter).BulkInsert(ctx, plan)
	if err == nil {
		backend.keys = append(backend.keys, result.Keys...)
		if backend.mode == "bad_returned_keys" {
			result.Keys = result.Keys[:len(result.Keys)-1]
		}
		if backend.mode == "canceled_bulk" {
			backend.cancel()
		}
	}
	return result, err
}

func (session *bulkTicketSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend := session.owner
	backend.reads++
	if plan.Table() == "helpdesk_ticket" {
		if backend.bulks == 0 && (backend.mode == "native_conflict" || backend.mode == "native_later_conflict") {
			return uniqueEmptyRows{}, nil
		}
		if backend.bulks > 0 && backend.mode == "output_error" {
			return nil, errors.New("private output read failure")
		}
	}
	return session.RelationSession.Query(ctx, plan)
}

func (session *bulkTicketSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if plan.Table() == "helpdesk_ticket" {
		session.owner.singleInserts++
	}
	return session.RelationSession.Insert(ctx, plan)
}

func (session *bulkTicketSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	session.owner.links++
	inserted, err := session.RelationSession.(db.ConflictInserter).InsertOnConflict(ctx, plan)
	if err == nil && session.owner.mode == "link_error" && session.owner.links == 2 {
		return false, errors.New("private second link failure")
	}
	return inserted, err
}

func verifyHelpdeskBulkTickets(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	t.Helper()
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Bulk outside"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("private outside bulk label", outside.ID))
	if err != nil {
		t.Fatal(err)
	}
	reference, err := uuid.Parse("18452167-cccc-4aaa-8444-019028364555")
	if err != nil {
		t.Fatal(err)
	}
	old, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("private unique bulk ticket", outside.ID).WithExternalReference(reference))
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{"api", "admin"} {
		t.Run(surface, func(t *testing.T) {
			cases := []string{"created", "multiple_batches", "invalid_second", "duplicate_batch", "duplicate_existing", "native_conflict", "native_later_conflict", "second_batch_error", "foreign_label", "late_category", "late_ticket_scope", "late_label_scope", "output_error", "bad_returned_keys", "link_error", "audit_error", "canceled_audit", "canceled_bulk", "post_commit_cancel", "missing_callback", "nil_session", "repeated_callback", "concurrent_callback", "swallowed_callback", "rollback_unknown", "commit_unknown", "denied_add", "denied_label", "csrf", "duplicate_csrf", "forged_id", "forged_category", "forged_digest", "query", "empty", "too_many", "duplicate_field"}
			if surface == "api" {
				cases = append(cases, "aggregate_output_budget", "many_diagnostics", "diagnostic_overflow", "scalar_item", "body_limit", "per_item_limit", "read_only_body")
			} else {
				cases = append(cases, "denied_view", "missing_management", "forged_initial", "duplicate_management", "unknown_index", "escaped_redisplay")
			}
			for _, mode := range cases {
				t.Run(mode, func(t *testing.T) {
					category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Bulk "+surface+" "+mode))
					if err != nil {
						t.Fatal(err)
					}
					var label models.Label
					if mode != "late_category" {
						label, err = models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Bulk owned <&>", category.ID))
						if err != nil {
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
					appendAudit := func(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
						audits++
						if err := runtime.AppendAudit(ctx, session, event); err != nil {
							return err
						}
						if audits == 2 && (mode == "audit_error" || mode == "rollback_unknown" || mode == "swallowed_callback") {
							return errors.New("private second audit failure")
						}
						if mode == "canceled_audit" && audits == 2 {
							cancel()
							return ctx.Err()
						}
						return nil
					}
					registry, err := application.AdminRegistry(helpdesk.AdminConfig{AppendAudit: appendAudit})
					if err != nil {
						t.Fatal(err)
					}
					var deny helpdeskDeniedPermissions
					switch mode {
					case "denied_add", "read_only_body":
						deny = []auth.Permission{helpdesk.AddTicket}
					case "denied_label":
						deny = []auth.Permission{helpdesk.ViewLabel}
					case "denied_view":
						deny = []auth.Permission{helpdesk.ViewTicket, helpdesk.ChangeTicket}
					}
					client := helpdeskHTTPRegistry(t, application, registry, runtime, deny, appendAudit)
					client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
					if page := client.request("GET", "/admin/", "", false); page.Code != 200 {
						t.Fatal("admitted bulk session", page.Code)
					}
					path := "/api/tickets/bulk/"
					var form url.Values
					if surface == "admin" {
						path = "/admin/tickets/collection-set/create/"
						page := client.request("GET", path, "", false)
						if len(deny) == 0 {
							form, _ = adminFormSubmission(t, page, path)
							if form.Get("tickets-TOTAL_FORMS") != "2" || form.Get("tickets-INITIAL_FORMS") != "0" || form.Has("tickets-0-id") || form.Has("tickets-0-category") || form.Has("tickets-0-external_payload_digest") || !strings.Contains(page.Body.String(), "data-inline-empty") || strings.Contains(page.Body.String(), "private outside bulk label") {
								t.Fatal("bulk form ownership or discovery")
							}
						} else {
							if page.Code != 403 {
								t.Fatal("denied bulk form", page.Code)
							}
							form = url.Values{"csrfmiddlewaretoken": {client.csrf}}
						}
					}
					count := 2
					if mode == "multiple_batches" || mode == "aggregate_output_budget" {
						count = 40
					}
					if mode == "second_batch_error" || mode == "native_later_conflict" {
						count = 21
					}
					if mode == "many_diagnostics" || mode == "diagnostic_overflow" {
						count = 40
					}
					inputs := make([]map[string]any, count)
					for index := range inputs {
						inputs[index] = map[string]any{"subject": fmt.Sprintf("  Bulk %02d <&>  ", index)}
						if label.ID != 0 {
							inputs[index]["labels"] = []int64{label.ID, label.ID}
						}
						if mode == "created" && index == 0 {
							inputs[index]["external_payload"] = json.RawMessage(`{"":9007199254740993,"normalized":1e2}`)
							inputs[index]["expected_cost"] = "12.30"
							inputs[index]["reviewed"] = false
							inputs[index]["service_at"] = "00:00:00"
						}
					}
					want, committed := 201, true
					if surface == "admin" {
						want = 302
					}
					validationStatus := 400
					if surface == "admin" {
						validationStatus = 200
					}
					switch mode {
					case "invalid_second", "escaped_redisplay":
						inputs[1]["subject"] = strings.Repeat("x", 121)
						want, committed = validationStatus, false
					case "duplicate_batch":
						inputs[0]["external_reference"] = "29452167-cccc-4aaa-8444-019028364555"
						inputs[1]["external_reference"] = inputs[0]["external_reference"]
						want, committed = validationStatus, false
					case "duplicate_existing", "native_conflict", "native_later_conflict":
						inputs[count-1]["external_reference"] = reference.String()
						want, committed = validationStatus, false
					case "foreign_label":
						inputs[1]["labels"] = []int64{foreign.ID}
						want, committed = validationStatus, false
					case "late_category", "late_ticket_scope":
						want, committed = 404, false
					case "late_label_scope", "output_error", "bad_returned_keys", "second_batch_error", "link_error", "audit_error", "canceled_audit", "canceled_bulk", "missing_callback", "nil_session", "repeated_callback", "concurrent_callback", "swallowed_callback", "rollback_unknown", "aggregate_output_budget":
						want, committed = 500, false
					case "commit_unknown":
						want = 500
					case "denied_add", "denied_label", "denied_view", "csrf", "duplicate_csrf", "read_only_body":
						want, committed = 403, false
					case "forged_id":
						inputs[1]["id"] = old.ID
						want, committed = 400, false
					case "forged_category":
						inputs[1]["category"] = outside.ID
						want, committed = 400, false
					case "forged_digest":
						inputs[1]["external_payload_digest"] = ""
						want, committed = 400, false
					case "query":
						path += "?category=2"
						want, committed = 400, false
					case "empty":
						inputs = nil
						want, committed = validationStatus, false
					case "too_many":
						inputs = append(make([]map[string]any, 41), inputs[:0]...)
						for index := range inputs {
							inputs[index] = map[string]any{"subject": "overflow"}
						}
						want, committed = 400, false
					case "duplicate_field":
						want, committed = validationStatus, false
					case "scalar_item", "body_limit", "per_item_limit", "many_diagnostics", "diagnostic_overflow":
						want, committed = 400, false
					case "missing_management", "forged_initial", "duplicate_management":
						want, committed = 200, false
					case "unknown_index":
						want, committed = 400, false
					}
					if mode == "aggregate_output_budget" {
						zeros := make([]int, 813)
						for _, input := range inputs {
							input["external_payload"] = map[string]any{"a": zeros, "b": zeros}
							delete(input, "labels")
						}
					}
					if mode == "many_diagnostics" || mode == "diagnostic_overflow" {
						fields := 80
						if mode == "diagnostic_overflow" {
							fields = 256
						}
						for _, input := range inputs {
							for index := 0; index < fields; index++ {
								input[fmt.Sprintf("u%d", index)] = 0
							}
						}
					}
					var body string
					if surface == "api" {
						wire, err := json.Marshal(inputs)
						if err != nil {
							t.Fatal(err)
						}
						body = string(wire)
						switch mode {
						case "empty":
							body = "[]"
						case "duplicate_field":
							body = `[{"subject":"one"},{"subject":"two","subject":"again"}]`
						case "scalar_item":
							body = `[{"subject":"valid"},false]`
						case "body_limit":
							body = strings.Repeat(" ", 163841)
							want = 413
						case "per_item_limit":
							body = `[{"subject":"valid","resolution":"` + strings.Repeat("x", 4096) + `"}]`
						case "read_only_body":
							body = "malformed"
						}
					} else {
						form.Set("tickets-TOTAL_FORMS", strconv.Itoa(len(inputs)))
						form.Set("tickets-INITIAL_FORMS", "0")
						for index, input := range inputs {
							prefix := fmt.Sprintf("tickets-%d-", index)
							for name, value := range input {
								switch typed := value.(type) {
								case []int64:
									form.Del(prefix + name)
									for _, key := range typed {
										form.Add(prefix+name, strconv.FormatInt(key, 10))
									}
								case json.RawMessage:
									form.Set(prefix+name, string(typed))
								case bool:
									if name == "reviewed" {
										form.Set(prefix+name, "false")
									} else if typed {
										form.Set(prefix+name, "on")
									}
								default:
									form.Set(prefix+name, fmt.Sprint(value))
								}
							}
						}
						switch mode {
						case "empty":
							for name := range form {
								if strings.HasPrefix(name, "tickets-0-") || strings.HasPrefix(name, "tickets-1-") {
									form.Del(name)
								}
							}
						case "duplicate_field":
							form.Add("tickets-1-subject", "again")
						case "missing_management":
							form.Del("tickets-TOTAL_FORMS")
						case "forged_initial":
							form.Set("tickets-INITIAL_FORMS", "1")
						case "duplicate_management":
							form.Add("tickets-TOTAL_FORMS", "1")
						case "unknown_index":
							form.Set("tickets-01-subject", "forged")
						case "escaped_redisplay":
							form.Set("tickets-0-subject", "</textarea><script>bulk</script>")
						}
						body = form.Encode()
					}
					if mode == "csrf" {
						client.csrf = ""
						if surface == "admin" {
							form.Del("csrfmiddlewaretoken")
							body = form.Encode()
						}
					}
					if mode == "duplicate_csrf" {
						if surface == "api" {
							client.csrf += "," + client.csrf
						} else {
							form.Add("csrfmiddlewaretoken", form.Get("csrfmiddlewaretoken"))
							body = form.Encode()
						}
					}
					backend.mode = mode
					response := client.requestContext(requestContext, "POST", path, body, surface == "api")
					if response.Code != want {
						t.Fatalf("bulk %s %s status=%d want=%d body=%s", surface, mode, response.Code, want, response.Body.String())
					}
					if strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "<script>bulk</script>") {
						t.Fatal("bulk response disclosed data or unescaped content")
					}
					rows, err := models.TicketObjects.Using(runtime).Filter(relations.ModelsTicket.Category.ID.Exact(category.ID)).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
					if err != nil {
						t.Fatal(err)
					}
					wantRows := 0
					if committed {
						wantRows = count
					}
					if len(rows) != wantRows || backend.singleInserts != 0 {
						t.Fatalf("bulk persistence rows=%d want=%d single inserts=%d", len(rows), wantRows, backend.singleInserts)
					}
					if committed {
						if backend.atomics != 1 || backend.savepoints != 1 || backend.bulks != (count+19)/20 || audits != count {
							t.Fatalf("bulk ownership atomics=%d savepoints=%d batches=%d audits=%d", backend.atomics, backend.savepoints, backend.bulks, audits)
						}
						if count == 40 && !slices.Equal(backend.batchRows, []int{20, 20}) {
							t.Fatal("native multi-row batches were not used", backend.batchRows)
						}
						for index, row := range rows {
							if row.Subject != fmt.Sprintf("Bulk %02d <&>", index) || row.CategoryID != category.ID || row.Closed || len(collectionLinks(t, ctx, runtime, row.ID)) != 1 {
								t.Fatal("bulk order/default/scope/collection")
							}
							history, err := runtime.AuditHistory(ctx, "helpdesk.ticket", row.ID, 10)
							if err != nil || len(history) != 1 || history[0].Action != admin.ActionAdd || history[0].DisplayLabel != row.Subject {
								t.Fatal("bulk audit binding", err)
							}
							if mode == "created" && index == 0 {
								if row.ExternalPayload == nil || row.ExternalPayloadDigest == nil || row.ExpectedCost == nil || row.Reviewed == nil || *row.Reviewed || row.ServiceAt == nil {
									t.Fatal("bulk scalar presence lost")
								}
								sum := sha256.Sum256([]byte(row.ExternalPayload.Text))
								if row.ExternalPayloadDigest.Base64() != base64.StdEncoding.EncodeToString(sum[:]) {
									t.Fatal("stored JSON digest mismatch")
								}
							}
						}
						if surface == "api" && want == 201 {
							var result []struct {
								ID      int64
								Subject string
								Labels  []int64
							}
							if json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result) != count {
								t.Fatal("bulk response envelope")
							}
							for index, row := range result {
								if row.ID != rows[index].ID || row.Subject != rows[index].Subject || !slices.Equal(row.Labels, []int64{label.ID}) {
									t.Fatal("response input order or exact keys")
								}
							}
						}
					} else {
						for _, key := range backend.keys {
							history, err := runtime.AuditHistory(ctx, "helpdesk.ticket", key, 10)
							if err != nil || len(history) != 0 || len(collectionLinks(t, ctx, runtime, key)) != 0 {
								t.Fatal("failed bulk retained link/audit", err)
							}
						}
					}
					if mode == "second_batch_error" || mode == "native_later_conflict" {
						if backend.bulks != 2 || len(backend.keys) != 20 {
							t.Fatal("later failure did not follow a real first batch", backend.bulks, len(backend.keys))
						}
					}
					if mode == "aggregate_output_budget" && (backend.bulks != 2 || len(backend.keys) != 40 || audits != 0) {
						t.Fatal("aggregate response failure was not checked after native storage")
					}
					if mode == "audit_error" && audits != 2 {
						t.Fatal("late audit rollback was not exercised")
					}
					if (mode == "duplicate_batch" || mode == "duplicate_existing" || mode == "foreign_label" || mode == "invalid_second") && backend.bulks != 0 {
						t.Fatal("invalid complete input reached bulk SQL")
					}
					if len(deny) != 0 || mode == "csrf" || mode == "duplicate_csrf" {
						if backend.atomics != 0 || audits != 0 {
							t.Fatal("denied request reached transaction/audit")
						}
					}
					if surface == "api" && (mode == "invalid_second" || mode == "duplicate_batch" || mode == "duplicate_existing" || mode == "foreign_label") {
						if !strings.Contains(response.Body.String(), `"key":"index","value":"1"`) {
							t.Fatal("indexed rejection lost row identity", response.Body.String())
						}
					}
					if mode == "diagnostic_overflow" && !strings.Contains(response.Body.String(), `"code":"too_many_errors"`) {
						t.Fatal("oversized diagnostics did not fail explicitly")
					}
					if backend.retained != nil {
						before := backend.reads
						err := runtime.AtomicRelation(ctx, func(session db.RelationSession) error { return backend.retained(session) })
						if err == nil || backend.reads != before {
							t.Fatal("retained application callback executed work", err)
						}
					}
					if label.ID != 0 {
						current, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(label.ID)).Get(ctx)
						if err != nil || current.CategoryID != category.ID {
							t.Fatal("failed bulk changed label scope", err)
						}
					}
				})
			}
		})
	}
	t.Run("admin_choice_response_budget", func(t *testing.T) {
		category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Bulk large form"))
		if err != nil {
			t.Fatal(err)
		}
		inputs := make([]models.LabelCreate, 256)
		for index := range inputs {
			inputs[index] = models.NewLabelCreate(strings.Repeat("<", 58)+fmt.Sprintf("%06d", index), category.ID)
		}
		if err := runtime.Atomic(ctx, func(session db.Session) error {
			_, err := models.LabelObjects.BulkCreateInputs(ctx, session, orm.CreateInputs(inputs))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		application, err := helpdesk.New(runtime, category.ID)
		if err != nil {
			t.Fatal(err)
		}
		audits := 0
		registry, err := application.AdminRegistry(helpdesk.AdminConfig{AppendAudit: func(context.Context, db.Session, admin.PreparedEvent) error {
			audits++
			return errors.New("invalid large form reached audit")
		}})
		if err != nil {
			t.Fatal(err)
		}
		client := helpdeskHTTPRegistry(t, application, registry, runtime, auth.PrincipalAuthorizer{})
		client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		path := "/admin/tickets/collection-set/create/"
		page := client.request("GET", path, "", false)
		values, _ := adminFormSubmission(t, page, path)
		values.Set("tickets-TOTAL_FORMS", "40")
		for index := 0; index < 40; index++ {
			values.Set(fmt.Sprintf("tickets-%d-subject", index), "Large form row")
		}
		values.Set("tickets-39-subject", strings.Repeat("x", 121))
		response := client.request("POST", path, values.Encode(), false)
		if response.Code != 200 || response.Body.Len() <= 1<<20 || response.Body.Len() > helpdesk.AdminRenderLimits().MaxOutputBytes || !strings.Contains(response.Body.String(), `data-error-code="max_length"`) || audits != 0 {
			t.Fatal("bounded large Admin form cannot redisplay", response.Code, response.Body.Len())
		}
		count, err := models.TicketObjects.Using(runtime).Filter(relations.ModelsTicket.Category.ID.Exact(category.ID)).Count(ctx)
		if err != nil || count != 0 {
			t.Fatal("invalid large form wrote tickets", err)
		}
	})
}
