package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type reportSaveResult struct {
	Report  reportConsumerValue `json:"report"`
	Created bool                `json:"created"`
}

type reportSaveBackend struct {
	helpdesk.Backend
	mode                                                           string
	ticket, outsideCategory, outsideTicket, savedID                int64
	transactions, reads, reportReads, inserts, updates, savepoints int
	moved                                                          bool
	cancel                                                         context.CancelFunc
	retained                                                       func(db.Session) error
}

func (b *reportSaveBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	b.transactions++
	if b.mode == "missing_callback" {
		b.retained = callback
		return nil
	}
	err := b.Backend.Atomic(ctx, func(session db.Session) error {
		if b.mode == "late_ticket" {
			value, err := models.TicketObjects.Using(session).Filter(models.TicketFields.ID.Exact(b.ticket)).Get(ctx)
			if err != nil {
				return err
			}
			_, err = models.TicketObjects.Patch(ctx, session, value, models.TicketPatch{}.WithCategoryID(b.outsideCategory))
			if err != nil {
				return err
			}
		}
		if b.mode == "missing_policy" {
			return callback(struct{ db.Session }{session})
		}
		wrapped := reportSaveSession{Session: session, owner: b}
		if b.mode == "concurrent_callback" {
			failures := make(chan error, 2)
			var workers sync.WaitGroup
			for range 2 {
				workers.Go(func() { failures <- callback(wrapped) })
			}
			workers.Wait()
			return errors.Join(<-failures, <-failures)
		}
		err := callback(wrapped)
		if err == nil && b.mode == "repeated_callback" {
			return callback(wrapped)
		}
		return err
	})
	if b.mode == "swallowed_callback" {
		return nil
	}
	if err == nil && b.mode == "commit_unknown" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	if err != nil && b.mode == "rollback_unknown" {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
	}
	if err == nil && b.mode == "post_commit_cancel" {
		b.cancel()
	}
	return err
}

type reportSaveSession struct {
	db.Session
	owner *reportSaveBackend
}

func (s reportSaveSession) ValidateSession(ctx context.Context) error {
	return s.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (s reportSaveSession) ReadModifyWritePolicy(ctx context.Context) (db.ReadModifyWritePolicy, error) {
	if s.owner.mode == "unknown_policy" {
		return "unknown", nil
	}
	return s.Session.(db.ReadModifyWriteSession).ReadModifyWritePolicy(ctx)
}
func (s reportSaveSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	s.owner.savepoints++
	err := db.WithSavepoint(ctx, s.Session, func(child db.Session) error { return callback(reportSaveSession{Session: child, owner: s.owner}) })
	if err == nil && s.owner.mode == "late_report_scope" && !s.owner.moved && s.owner.savedID != 0 {
		s.owner.moved = true
		value, err := models.ServiceReportObjects.Using(s.Session).Filter(models.ServiceReportFields.ID.Exact(s.owner.savedID)).Get(ctx)
		if err != nil {
			return err
		}
		_, err = models.ServiceReportObjects.Patch(ctx, s.Session, value, models.ServiceReportPatch{}.WithTicketID(s.owner.outsideTicket))
		return err
	}
	return err
}
func (s reportSaveSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	b := s.owner
	b.reads++
	if b.mode == "query_error" {
		return nil, errors.New("private report query failure")
	}
	if plan.Table() == "helpdesk_service_report" {
		b.reportReads++
		if b.mode == "native_unique" && b.reportReads == 1 {
			return uniqueEmptyRows{}, nil
		}
		if b.mode == "reload_error" && (b.inserts+b.updates) > 0 {
			return nil, errors.New("private report reload failure")
		}
	}
	rows, err := s.Session.Query(ctx, plan)
	if err == nil && plan.Table() == "helpdesk_service_report" && (b.inserts+b.updates) > 0 && (b.mode == "output_error" || b.mode == "update_output_error") {
		return reportSaveInvalidOutput{rows}, nil
	}
	return rows, err
}
func (s reportSaveSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if plan.Table() != "helpdesk_service_report" {
		return s.Session.Insert(ctx, plan)
	}
	s.owner.inserts++
	if s.owner.mode == "insert_error" {
		return 0, errors.New("private report insert failure")
	}
	id, err := s.Session.Insert(ctx, plan)
	if err == nil {
		s.owner.savedID = id
	}
	return id, err
}
func (s reportSaveSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if plan.Table() != "helpdesk_service_report" {
		return s.Session.Update(ctx, plan)
	}
	s.owner.updates++
	if s.owner.mode == "update_error" {
		return 0, errors.New("private report update failure")
	}
	return s.Session.Update(ctx, plan)
}

type reportSaveInvalidOutput struct{ db.Rows }

func (rows reportSaveInvalidOutput) Scan(values ...any) error {
	if err := rows.Rows.Scan(values...); err != nil {
		return err
	}
	if len(values) != 4 {
		return errors.New("unexpected report scanner width")
	}
	summary, ok := values[2].(*string)
	if !ok {
		return errors.New("unexpected report summary scanner")
	}
	*summary = "\xff"
	return nil
}

func checkReportSave(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertReportSaveAudit(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, id int64, count int, created bool) {
	t.Helper()
	if id <= 0 {
		if count != 0 {
			t.Fatal("missing audit identity")
		}
		return
	}
	history, err := runtime.AuditHistory(ctx, "helpdesk.service_report", id, 100)
	checkReportSave(t, err)
	if len(history) != count {
		t.Fatalf("report audit count=%d/%d", len(history), count)
	}
	if count == 0 {
		return
	}
	last := history[len(history)-1]
	action := admin.ActionChange
	fields := []string{"summary", "completed"}
	if created {
		action = admin.ActionAdd
		fields = nil
	}
	if last.Action != action || last.DisplayLabel != fmt.Sprintf("Report #%d", id) || last.ActorID == "" || !slices.Equal(last.ChangedFields, fields) {
		t.Fatal("report audit does not describe the saved branch", last)
	}
}

func verifyHelpdeskReportSave(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	t.Helper()
	relations, err := project.BindRelations()
	checkReportSave(t, err)
	for _, surface := range []string{"api", "admin"} {
		t.Run(surface, func(t *testing.T) {
			modes := []string{"created", "updated", "unchanged", "native_unique", "other_category", "missing_ticket", "late_ticket", "late_report_scope", "query_error", "insert_error", "update_error", "reload_error", "output_error", "update_output_error", "audit_error", "update_audit_error", "canceled_audit", "rollback_unknown", "commit_unknown", "post_commit_cancel", "missing_callback", "repeated_callback", "concurrent_callback", "swallowed_callback", "missing_policy", "unknown_policy", "denied_add", "denied_change", "denied_view", "without_ticket_view", "csrf", "duplicate_csrf", "empty", "duplicate", "forged_id", "forged_ticket", "forged_category", "query"}
			if surface == "api" {
				modes = append(modes, "body_limit")
			}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Report save "+surface+" "+mode))
					checkReportSave(t, err)
					outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Outside report save"))
					checkReportSave(t, err)
					ticket, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Ticket <&>", category.ID))
					checkReportSave(t, err)
					foreign, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Hidden report ticket", outside.ID))
					checkReportSave(t, err)
					const normalized = "Saved <&>\nSecond line"
					summary := "  " + normalized + "  "
					completed := false
					var existing models.ServiceReport
					updating := slices.Contains([]string{"updated", "unchanged", "native_unique", "update_error", "update_output_error", "update_audit_error"}, mode)
					if updating || mode == "other_category" {
						key := ticket.ID
						if mode == "other_category" {
							key = foreign.ID
						}
						existing, err = models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(key, "Original report").WithCompleted(true))
						checkReportSave(t, err)
					}
					if mode == "unchanged" {
						summary = "Original report"
						completed = true
					}
					backend := &reportSaveBackend{Backend: runtime, ticket: ticket.ID, outsideCategory: outside.ID, outsideTicket: foreign.ID, savedID: existing.ID}
					application, err := helpdesk.New(backend, category.ID)
					checkReportSave(t, err)
					requestContext, cancel := context.WithCancel(ctx)
					defer cancel()
					backend.cancel = cancel
					audits := 0
					appendAudit := func(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
						audits++
						if err := runtime.AppendAudit(ctx, session, event); err != nil {
							return err
						}
						if mode == "canceled_audit" {
							cancel()
							return ctx.Err()
						}
						if slices.Contains([]string{"audit_error", "update_audit_error", "rollback_unknown", "swallowed_callback"}, mode) {
							return errors.New("private report audit failure")
						}
						return nil
					}
					registry, err := application.AdminRegistry(helpdesk.AdminConfig{AppendAudit: appendAudit})
					checkReportSave(t, err)
					var denied helpdeskDeniedPermissions
					switch mode {
					case "denied_add":
						denied = []auth.Permission{helpdesk.AddServiceReport}
					case "denied_change":
						denied = []auth.Permission{helpdesk.ChangeServiceReport}
					case "denied_view":
						denied = []auth.Permission{helpdesk.ViewServiceReport}
					case "without_ticket_view":
						denied = []auth.Permission{helpdesk.ViewTicket}
					}
					client := helpdeskHTTPRegistry(t, application, registry, runtime, denied, appendAudit)
					client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
					if response := client.request("GET", "/admin/", "", false); response.Code != 200 || client.csrf == "" {
						t.Fatal("report client admission setup", response.Code)
					}
					addressed := ticket.ID
					if mode == "other_category" {
						addressed = foreign.ID
					}
					if mode == "missing_ticket" {
						addressed = 9223372036854775807
					}
					path := fmt.Sprintf("/api/tickets/%d/service-report/", addressed)
					method := "PUT"
					values := url.Values{"ticket": {fmt.Sprint(addressed)}, "summary": {summary}}
					if completed {
						values.Set("completed", "on")
					}
					if surface == "admin" {
						path = "/admin/service-reports/collection/save/"
						method = "POST"
						get := client.request("GET", path, "", false)
						if len(denied) == 0 {
							if get.Code != 200 || strings.Contains(get.Body.String(), "Hidden report ticket") || !strings.Contains(get.Body.String(), "Ticket &lt;&amp;&gt;") {
								t.Fatal("report chooser scope/escaping", get.Code)
							}
							var action string
							values, action = adminFormSubmission(t, get, path)
							if action != path || values.Has("id") || values.Has("category") || values.Has("expected_revision") {
								t.Fatal("report save form exposed an unowned identity")
							}
							values.Set("ticket", fmt.Sprint(addressed))
							values.Set("summary", summary)
							values.Del("completed")
							if completed {
								values.Set("completed", "on")
							}
						} else {
							if get.Code != 403 {
								t.Fatal("denied report chooser read rows", get.Code)
							}
							values.Set("csrfmiddlewaretoken", client.csrf)
						}
					}
					bodyBytes, err := json.Marshal(map[string]any{"summary": summary, "completed": completed})
					checkReportSave(t, err)
					body := string(bodyBytes)
					want, rowCount, auditCount, insertCount, updateCount := 201, 1, 1, 1, 0
					if surface == "admin" {
						want = 302
					}
					if updating {
						if surface == "api" {
							want = 200
						}
						insertCount, updateCount = 0, 1
					}
					if mode == "native_unique" {
						insertCount = 1
					}
					if mode == "unchanged" {
						auditCount, updateCount = 0, 0
					}
					switch mode {
					case "other_category", "missing_ticket":
						want, rowCount, auditCount, insertCount = 404, 0, 0, 0
						if surface == "admin" {
							want = 200
						}
					case "late_ticket":
						want, rowCount, auditCount, insertCount = 404, 0, 0, 0
					case "late_report_scope":
						want, rowCount, auditCount = 404, 0, 0
					case "query_error", "missing_callback", "missing_policy", "unknown_policy":
						want, rowCount, auditCount, insertCount = 500, 0, 0, 0
					case "insert_error", "reload_error", "output_error", "audit_error", "canceled_audit", "rollback_unknown", "repeated_callback", "concurrent_callback", "swallowed_callback":
						want, rowCount, auditCount = 500, 0, 0
					case "update_error", "update_output_error", "update_audit_error":
						want, auditCount = 500, 0
					case "commit_unknown":
						want = 500
					case "denied_add", "denied_change", "denied_view", "csrf", "duplicate_csrf":
						want, rowCount, auditCount, insertCount = 403, 0, 0, 0
					case "without_ticket_view":
						if surface == "admin" {
							want, rowCount, auditCount, insertCount = 403, 0, 0, 0
						}
					case "empty", "duplicate", "forged_id", "forged_ticket", "forged_category", "query":
						want, rowCount, auditCount, insertCount = 400, 0, 0, 0
						if surface == "admin" && (mode == "empty" || mode == "duplicate" || mode == "forged_ticket") {
							want = 200
						}
					case "body_limit":
						want, rowCount, auditCount, insertCount = 413, 0, 0, 0
					}
					switch mode {
					case "csrf":
						client.csrf = ""
						values.Del("csrfmiddlewaretoken")
					case "duplicate_csrf":
						if surface == "api" {
							client.csrf += "," + client.csrf
						} else {
							values.Add("csrfmiddlewaretoken", values.Get("csrfmiddlewaretoken"))
						}
					case "empty":
						body = `{"summary":" "}`
						values.Set("summary", " ")
					case "duplicate":
						body = `{"summary":"one","summary":"two"}`
						values.Add("summary", "second")
					case "forged_id":
						body = `{"summary":"forged","id":1}`
						values.Set("id", "1")
					case "forged_ticket":
						body = fmt.Sprintf(`{"summary":"forged","ticket":%d}`, foreign.ID)
						values.Set("ticket", fmt.Sprint(foreign.ID))
					case "forged_category":
						body = fmt.Sprintf(`{"summary":"forged","category":%d}`, outside.ID)
						values.Set("category", fmt.Sprint(outside.ID))
					case "query":
						path += "?ticket=1"
					case "body_limit":
						body = `{"summary":"` + strings.Repeat("x", 4096) + `"}`
					}
					if surface == "admin" {
						body = values.Encode()
					}
					backend.mode = mode
					response := client.requestContext(requestContext, method, path, body, surface == "api")
					if response.Code != want || backend.inserts != insertCount || backend.updates != updateCount || backend.transactions > 1 || strings.Contains(response.Body.String(), "private ") {
						t.Fatalf("report save status=%d/%d inserts=%d/%d updates=%d/%d scopes=%d", response.Code, want, backend.inserts, insertCount, backend.updates, updateCount, backend.transactions)
					}
					if backend.retained != nil && backend.retained(nil) == nil {
						t.Fatal("late report callback was admitted")
					}
					if response.Header().Get("Retry-After") != "" || response.Code != 302 && response.Header().Get("Location") != "" {
						t.Fatal("report failure advertised success or retry")
					}
					if want == 400 || want == 403 || want == 413 || surface == "admin" && want == 200 {
						if backend.transactions != 0 || backend.reads != 0 || audits != 0 {
							t.Fatal("rejected report input entered the write scope")
						}
					}
					rows, err := models.ServiceReportObjects.Using(runtime).Filter(relations.ModelsServiceReport.Ticket.ID.Exact(ticket.ID)).All(ctx)
					checkReportSave(t, err)
					if len(rows) != rowCount {
						t.Fatalf("report durability count=%d/%d", len(rows), rowCount)
					}
					id := backend.savedID
					if rowCount == 1 {
						id = rows[0].ID
						expectedSummary, expectedCompleted := normalized, false
						if mode == "unchanged" || updating && want == 500 {
							expectedSummary, expectedCompleted = "Original report", true
						}
						if rows[0].TicketID != ticket.ID || rows[0].Summary != expectedSummary || rows[0].Completed != expectedCompleted || updating && rows[0].ID != existing.ID {
							t.Fatal("saved report crossed identity, scope or rollback boundary", rows[0])
						}
					}
					assertReportSaveAudit(t, ctx, runtime, id, auditCount, !updating)
					storedTicket, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(ticket.ID)).Get(ctx)
					checkReportSave(t, err)
					if storedTicket.CategoryID != category.ID {
						t.Fatal("failed report save retained a staged ticket move")
					}
					if mode == "other_category" {
						row, err := models.ServiceReportObjects.Using(runtime).Filter(models.ServiceReportFields.ID.Exact(existing.ID)).Get(ctx)
						checkReportSave(t, err)
						if row != existing {
							t.Fatal("foreign report changed")
						}
					}
					if surface == "api" && (want == 200 || want == 201) {
						var result reportSaveResult
						checkReportSave(t, json.Unmarshal(response.Body.Bytes(), &result))
						if result.Report.ID != id || result.Report.Ticket != ticket.ID || result.Report.Summary != rows[0].Summary || result.Report.Completed != rows[0].Completed || result.Created == updating {
							t.Fatal("published report differs from commit", result)
						}
						assertHelpdeskResponseDocumented(t, client.document, "PUT", "/api/tickets/{id}/service-report/", response)
					}
					if want == 302 {
						notice := "added"
						if updating {
							notice = "changed"
						}
						if mode == "unchanged" {
							notice = "existing"
						}
						target, err := url.Parse(response.Header().Get("Location"))
						checkReportSave(t, err)
						if target.Path != "/admin/service-reports/" || target.Query().Get("notice") != notice {
							t.Fatal("Admin report outcome did not describe the commit")
						}
					}
					if mode == "created" {
						beforeInserts, beforeUpdates, beforeAudit := backend.inserts, backend.updates, audits
						repeat := client.request("PUT", fmt.Sprintf("/api/tickets/%d/service-report/", ticket.ID), `{"summary":"Saved <&>\nSecond line"}`, true)
						var result reportSaveResult
						if repeat.Code != http.StatusOK || json.Unmarshal(repeat.Body.Bytes(), &result) != nil || result.Created || result.Report.ID != id || backend.inserts != beforeInserts || backend.updates != beforeUpdates || audits != beforeAudit {
							t.Fatal("unchanged repeat wrote a row or audit", repeat.Code)
						}
						backend.mode = ""
						if ordinary := client.request("POST", "/api/service-reports/", reportInput(ticket.ID, "duplicate"), true); ordinary.Code != 400 {
							t.Fatal("ordinary creation stopped rejecting duplicate reports", ordinary.Code)
						}
						assertReportSaveAudit(t, ctx, runtime, id, 1, true)
					}
				})
			}
		})
	}
}

// This checks two independently admitted HTTP requests and their durable audit
// history through the database coordination domain. Native overlapping upsert
// row/unique conflicts are owned by the generated ORM reference consumer.
func verifyHelpdeskReportSaveConcurrentRequests(t *testing.T, rootContext context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), hasher auth.PasswordHasher, authenticated *helpdeskClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(rootContext, 20*time.Second)
	defer cancel()
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Concurrent report saves"))
	checkReportSave(t, err)
	ticket, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Concurrent report ticket", category.ID))
	checkReportSave(t, err)
	peerBackend, err := open(ctx)
	checkReportSave(t, err)
	defer peerBackend.Close()
	observer, err := open(ctx)
	checkReportSave(t, err)
	defer observer.Close()
	peer := &coordinatedPeerBackend{helpdeskBackend: peerBackend}
	peerRuntime, err := systemstate.OpenIdentity(ctx, peer, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher})
	checkReportSave(t, err)
	firstApp, err := helpdesk.New(runtime, category.ID)
	checkReportSave(t, err)
	secondApp, err := helpdesk.New(peerRuntime, category.ID)
	checkReportSave(t, err)
	audited, release := make(chan struct{}), make(chan struct{})
	var nativePolicy db.ReadModifyWritePolicy
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	var workers sync.WaitGroup
	defer func() { cancel(); unlock(); workers.Wait() }()
	leader := helpdeskHTTP(t, firstApp, runtime, auth.PrincipalAuthorizer{}, func(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
		if err := runtime.AppendAudit(ctx, session, event); err != nil {
			return err
		}
		var err error
		nativePolicy, err = session.(db.ReadModifyWriteSession).ReadModifyWritePolicy(ctx)
		if err != nil {
			return err
		}
		close(audited)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	follower := helpdeskHTTP(t, secondApp, peerRuntime, auth.PrincipalAuthorizer{})
	for _, client := range []*helpdeskClient{leader, follower} {
		client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		if response := client.request("GET", "/admin/", "", false); response.Code != 200 || client.csrf == "" {
			t.Fatal("concurrent report admission", response.Code)
		}
	}
	peer.entered = make(chan struct{})
	results := []chan *httptest.ResponseRecorder{make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)}
	path := fmt.Sprintf("/api/tickets/%d/service-report/", ticket.ID)
	workers.Go(func() { results[0] <- leader.requestContext(ctx, "PUT", path, `{"summary":"First report"}`, true) })
	select {
	case <-audited:
	case <-ctx.Done():
		t.Fatal("report leader did not reach its provisional audit")
	}
	workers.Go(func() {
		results[1] <- follower.requestContext(ctx, "PUT", path, `{"summary":"Second report","completed":true}`, true)
	})
	select {
	case <-peer.entered:
	case <-ctx.Done():
		t.Fatal("report peer did not enter database coordination")
	}
	relations, err := project.BindRelations()
	checkReportSave(t, err)
	count, err := models.ServiceReportObjects.Using(observer).Filter(relations.ModelsServiceReport.Ticket.ID.Exact(ticket.ID)).Count(ctx)
	if err != nil || count != 0 {
		t.Fatal("provisional report became externally visible", count, err)
	}
	if nativePolicy == db.ReadModifyWriteRowLock {
		err := observer.(db.Atomic).Atomic(ctx, func(session db.Session) error {
			_, err := models.TicketObjects.Using(session).Filter(models.TicketFields.ID.Exact(ticket.ID)).SelectForUpdate(orm.RowLockOptions{NoWait: true}).Get(ctx)
			return err
		})
		var native *pgconn.PgError
		if !errors.As(err, &native) || native.Code != "55P03" {
			t.Fatal("report save did not protect the addressed ticket through commit", err)
		}
	} else if nativePolicy != db.ReadModifyWriteConflict {
		t.Fatal("unknown native report scope")
	}
	unlock()
	var outcomes [2]reportSaveResult
	for index := range results {
		select {
		case response := <-results[index]:
			want := 200
			if index == 0 {
				want = 201
			}
			if response.Code != want || json.Unmarshal(response.Body.Bytes(), &outcomes[index]) != nil {
				t.Fatal("concurrent report response", index, response.Code)
			}
		case <-ctx.Done():
			t.Fatal("concurrent report request did not finish")
		}
	}
	workers.Wait()
	if !outcomes[0].Created || outcomes[1].Created || outcomes[0].Report.ID != outcomes[1].Report.ID || outcomes[0].Report.Ticket != ticket.ID || outcomes[1].Report.Ticket != ticket.ID || outcomes[0].Report.Summary != "First report" || outcomes[0].Report.Completed || outcomes[1].Report.Summary != "Second report" || !outcomes[1].Report.Completed {
		t.Fatal("concurrent report branches or snapshots changed")
	}
	rows, err := models.ServiceReportObjects.Using(peerBackend).Filter(relations.ModelsServiceReport.Ticket.ID.Exact(ticket.ID)).All(ctx)
	checkReportSave(t, err)
	if len(rows) != 1 || rows[0].ID != outcomes[0].Report.ID || rows[0].Summary != "Second report" || !rows[0].Completed {
		t.Fatal("concurrent report final storage differs")
	}
	assertReportSaveAudit(t, ctx, peerRuntime, rows[0].ID, 2, false)
	history, err := peerRuntime.AuditHistory(ctx, "helpdesk.service_report", rows[0].ID, 100)
	checkReportSave(t, err)
	if history[0].Action != admin.ActionAdd || len(history[0].ChangedFields) != 0 {
		t.Fatal("concurrent report lost its original creation audit")
	}
}
