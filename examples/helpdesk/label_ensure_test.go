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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type labelEnsureResult struct {
	Label   labelConsumerValue `json:"label"`
	Created bool               `json:"created"`
}

type ensureLabelBackend struct {
	helpdesk.Backend
	mode                                            string
	category, outside                               int64
	atomics, reads, labelReads, inserts, savepoints int
	insertedID                                      int64
	cancel                                          context.CancelFunc
	retained                                        func(db.Session) error
}

func (b *ensureLabelBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	b.atomics++
	if b.mode == "missing_callback" {
		b.retained = callback
		return nil
	}
	err := b.Backend.Atomic(ctx, func(session db.Session) error {
		if b.mode == "late_category" {
			category := models.NewCategoryWithID(b.category)
			if _, err := models.CategoryObjects.Delete(ctx, session, &category); err != nil {
				return err
			}
		}
		wrapped := &ensureLabelSession{Session: session, owner: b}
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

type ensureLabelSession struct {
	db.Session
	owner *ensureLabelBackend
}

func (s *ensureLabelSession) ValidateSession(ctx context.Context) error {
	validator, ok := s.Session.(db.SessionValidator)
	if !ok {
		return errors.New("ensure test lost native session validator")
	}
	return validator.ValidateSession(ctx)
}

func (s *ensureLabelSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	s.owner.savepoints++
	err := db.WithSavepoint(ctx, s.Session, func(child db.Session) error { return callback(&ensureLabelSession{Session: child, owner: s.owner}) })
	if err == nil && s.owner.mode == "late_scope" {
		value, err := models.LabelObjects.Using(s.Session).Filter(models.LabelFields.ID.Exact(s.owner.insertedID)).Get(ctx)
		if err != nil {
			return err
		}
		_, err = models.LabelObjects.Update(ctx, s.Session, value, models.LabelPatch{}.WithCategoryID(s.owner.outside))
		return err
	}
	return err
}

func (s *ensureLabelSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	b := s.owner
	b.reads++
	if plan.Table() == "helpdesk_label" {
		b.labelReads++
		if b.labelReads == 1 && b.mode == "native_unique" {
			return uniqueEmptyRows{}, nil
		}
		if b.mode == "query_error" || b.mode == "reload_error" && b.insertedID != 0 {
			return nil, errors.New("private label read failure")
		}
	}
	rows, err := s.Session.Query(ctx, plan)
	if err == nil && plan.Table() == "helpdesk_label" && b.insertedID != 0 && b.mode == "output_error" {
		return ensureInvalidOutputRows{rows}, nil
	}
	return rows, err
}

type ensureInvalidOutputRows struct{ db.Rows }

func (rows ensureInvalidOutputRows) Scan(values ...any) error {
	if err := rows.Rows.Scan(values...); err != nil {
		return err
	}
	if len(values) < 2 {
		return errors.New("unexpected label row shape")
	}
	name, ok := values[1].(*string)
	if !ok {
		return errors.New("unexpected label name scanner")
	}
	*name = strings.Repeat("x", 65)
	return nil
}

func (s *ensureLabelSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if plan.Table() != "helpdesk_label" {
		return s.Session.Insert(ctx, plan)
	}
	s.owner.inserts++
	if s.owner.mode == "insert_error" {
		return 0, errors.New("private label insert failure")
	}
	key, err := s.Session.Insert(ctx, plan)
	if err == nil {
		s.owner.insertedID = key
	}
	return key, err
}

func assertEnsureAudit(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, id int64, count int, name string) {
	t.Helper()
	if id <= 0 {
		if count != 0 {
			t.Fatal("missing audited identity")
		}
		return
	}
	history, err := runtime.AuditHistory(ctx, "helpdesk.label", id, 100)
	if err != nil || len(history) != count {
		t.Fatalf("label audit count=%d want=%d: %v", len(history), count, err)
	}
	if count != 0 && (history[0].Action != admin.ActionAdd || history[0].DisplayLabel != name || history[0].ActorID == "" || len(history[0].ChangedFields) != 0) {
		t.Fatal("label add audit differs from committed object")
	}
}

func verifyHelpdeskLabelEnsure(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	t.Helper()
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	const apiPath = "/api/labels/ensure/"
	const formPath = "/admin/labels/collection/ensure/"
	for _, surface := range []string{"api", "admin"} {
		t.Run(surface, func(t *testing.T) {
			for _, mode := range []string{"created", "existing", "other_category", "native_unique", "late_category", "late_scope", "query_error", "insert_error", "reload_error", "output_error", "audit_error", "canceled_audit", "rollback_unknown", "commit_unknown", "post_commit_cancel", "missing_callback", "repeated_callback", "concurrent_callback", "swallowed_callback", "denied_add", "denied_view", "csrf", "duplicate_csrf", "empty", "duplicate", "forged_category", "forged_id", "query", "too_long"} {
				t.Run(mode, func(t *testing.T) {
					category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Ensure "+surface+" "+mode))
					if err != nil {
						t.Fatal(err)
					}
					outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Outside ensure "+surface+" "+mode))
					if err != nil {
						t.Fatal(err)
					}
					const name = "Label <&>"
					var existing models.Label
					if mode == "existing" || mode == "native_unique" || mode == "other_category" {
						owner := category.ID
						if mode == "other_category" {
							owner = outside.ID
						}
						existing, err = models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate(name, owner))
						if err != nil {
							t.Fatal(err)
						}
					}
					backend := &ensureLabelBackend{Backend: runtime, category: category.ID, outside: outside.ID}
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
						if mode == "canceled_audit" {
							cancel()
							return ctx.Err()
						}
						if mode == "audit_error" || mode == "rollback_unknown" || mode == "swallowed_callback" {
							return errors.New("private audit failure")
						}
						return nil
					}
					registry, err := application.AdminRegistry(helpdesk.AdminConfig{AppendAudit: appendAudit})
					if err != nil {
						t.Fatal(err)
					}
					var deny helpdeskDeniedPermissions
					if mode == "denied_add" {
						deny = []auth.Permission{helpdesk.AddLabel}
					}
					if mode == "denied_view" {
						deny = []auth.Permission{helpdesk.ViewLabel}
					}
					client := helpdeskHTTPRegistry(t, application, registry, runtime, deny, appendAudit)
					client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
					// Each composed authentication runtime signs its own masked
					// tokens; acquire one from this site's actual safe response.
					if response := client.request("GET", "/admin/", "", false); response.Code != 200 || client.csrf == "" {
						t.Fatal("could not obtain this site's CSRF token", response.Code)
					}
					path := apiPath
					values := url.Values{"name": {"  " + name + "  "}}
					if surface == "admin" {
						path = formPath
						list := client.request("GET", "/admin/labels/", "", false)
						allowed := len(deny) == 0
						if list.Code != 200 || strings.Contains(list.Body.String(), formPath) != allowed {
							t.Fatal("collection command discovery/admission", list.Code)
						}
						get := client.request("GET", formPath, "", false)
						if allowed {
							var action string
							values, action = adminFormSubmission(t, get, formPath)
							if action != formPath || len(values) != 2 || values.Has("category") || values.Has("id") || values.Has("expected_revision") {
								t.Fatal("ensure form leaked model scope or condition")
							}
							values.Set("name", "  "+name+"  ")
						} else {
							if get.Code != 403 {
								t.Fatal("denied collection GET", get.Code)
							}
							values.Set("csrfmiddlewaretoken", client.csrf)
						}
					}
					backend.mode = mode
					body := labelInput("  " + name + "  ")
					want, rowCount, auditCount, attempts := 201, 1, 1, 1
					if surface == "admin" {
						want = 302
					}
					switch mode {
					case "existing":
						if surface == "api" {
							want = 200
						}
						auditCount, attempts = 0, 0
					case "native_unique":
						if surface == "api" {
							want = 200
						}
						auditCount = 0
					case "late_category":
						want, rowCount, auditCount, attempts = 404, 0, 0, 0
					case "late_scope":
						want, rowCount, auditCount = 404, 0, 0
					case "query_error":
						want, rowCount, auditCount, attempts = 500, 0, 0, 0
					case "insert_error", "reload_error", "output_error", "audit_error", "canceled_audit", "repeated_callback", "concurrent_callback", "swallowed_callback":
						want, rowCount, auditCount = 500, 0, 0
					case "missing_callback":
						want, rowCount, auditCount, attempts = 500, 0, 0, 0
					case "rollback_unknown":
						want, rowCount, auditCount = 500, 0, 0
					case "commit_unknown":
						want = 500
					case "denied_add", "denied_view", "csrf", "duplicate_csrf":
						want, rowCount, auditCount, attempts = 403, 0, 0, 0
					case "empty", "duplicate", "forged_category", "forged_id", "query", "too_long":
						want, rowCount, auditCount, attempts = 400, 0, 0, 0
						if surface == "admin" && (mode == "empty" || mode == "duplicate" || mode == "too_long") {
							want = 200
						}
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
						body = labelInput(" ")
						values.Set("name", " ")
					case "duplicate":
						body = `{"name":"one","name":"two"}`
						values.Add("name", "two")
					case "forged_category":
						body = fmt.Sprintf(`{"name":"forged","category":%d}`, outside.ID)
						values.Set("category", fmt.Sprint(outside.ID))
					case "forged_id":
						body = `{"name":"forged","id":1}`
						values.Set("id", "1")
					case "query":
						path += "?category=1"
					case "too_long":
						body = labelInput(strings.Repeat("라", 65))
						values.Set("name", strings.Repeat("라", 65))
					}
					if surface == "admin" {
						body = values.Encode()
					}
					response := client.requestContext(requestContext, "POST", path, body, surface == "api")
					if response.Code != want || backend.inserts != attempts || backend.atomics > 1 || strings.Contains(response.Body.String(), "private ") {
						t.Fatalf("ensure: status=%d want=%d inserts=%d want=%d transactions=%d", response.Code, want, backend.inserts, attempts, backend.atomics)
					}
					if backend.savepoints != attempts {
						t.Fatal("ensure creation did not use exactly one borrowed savepoint", backend.savepoints, attempts)
					}
					if backend.retained != nil && backend.retained(nil) == nil {
						t.Fatal("expired parent callback accepted a late invocation")
					}
					if attempts == 0 && rowCount == 0 && mode != "missing_callback" && mode != "late_category" && mode != "query_error" && (backend.atomics != 0 || backend.reads != 0 || audits != 0) {
						t.Fatal("denied or invalid input reached database")
					}
					if response.Header().Get("Retry-After") != "" || response.Code != 302 && response.Header().Get("Location") != "" {
						t.Fatal("failure invited retry or published success redirect")
					}
					rows, err := models.LabelObjects.Using(runtime).Filter(relations.ModelsLabel.Category.ID.Exact(category.ID)).All(ctx)
					if err != nil || len(rows) != rowCount {
						t.Fatalf("ensure row rollback: count=%d want=%d: %v", len(rows), rowCount, err)
					}
					id := backend.insertedID
					if rowCount != 0 {
						id = rows[0].ID
						if rows[0].Name != name {
							t.Fatal("ensure normalization changed durable label")
						}
					}
					assertEnsureAudit(t, ctx, runtime, id, auditCount, name)
					if category, err := models.CategoryObjects.Using(runtime).Filter(models.CategoryFields.ID.Exact(category.ID)).Get(ctx); err != nil || category.ID == 0 {
						t.Fatal("failed ensure committed late category removal", err)
					}
					if mode == "other_category" {
						row, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(existing.ID)).Get(ctx)
						if err != nil || row.CategoryID != outside.ID || row.Name != name || row.ID == id {
							t.Fatal("ensure crossed category boundary", err)
						}
					}
					if (mode == "existing" || mode == "native_unique") && id != existing.ID {
						t.Fatal("ensure did not reuse original identity")
					}
					if want == 201 || surface == "api" && want == 200 {
						var result labelEnsureResult
						if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Label.ID != id || result.Label.Category != category.ID || result.Label.Name != name || result.Created != (auditCount == 1) {
							t.Fatal("ensure response differs from commit", err)
						}
						assertHelpdeskResponseDocumented(t, client.document, "POST", apiPath, response)
					}
					if want == 302 {
						notice := "added"
						if auditCount == 0 {
							notice = "existing"
						}
						target, err := url.Parse(response.Header().Get("Location"))
						if err != nil || target.Path != "/admin/labels/" || target.Query().Get("notice") != notice {
							t.Fatal("ensure Admin result differs from commit")
						}
					}
					if mode == "created" {
						// Repeating this operation reuses the committed identity and
						// never duplicates its semantic event. Ordinary create keeps
						// its separate duplicate-rejection policy.
						repeat := client.request("POST", apiPath, labelInput(name), true)
						var result labelEnsureResult
						if repeat.Code != 200 || json.Unmarshal(repeat.Body.Bytes(), &result) != nil || result.Created || result.Label.ID != id {
							t.Fatal("ensure repeat created another identity")
						}
						ordinary := client.request("POST", "/api/labels/", labelInput(name), true)
						if ordinary.Code != 400 {
							t.Fatal("ordinary create stopped rejecting duplicates")
						}
						assertEnsureAudit(t, ctx, runtime, id, 1, name)
					}
				})
			}
		})
	}
}

type coordinatedPeerBackend struct {
	helpdeskBackend
	entered chan struct{}
	once    sync.Once
}

func (b *coordinatedPeerBackend) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	if b.entered != nil {
		b.once.Do(func() { close(b.entered) })
	}
	return b.helpdeskBackend.CoordinatedAtomic(ctx, callback)
}

// Two independent runtimes share the database coordination domain. Hold the
// leader after its actual audit INSERT, admit a peer HTTP request into that
// domain, and observe the uncommitted label through the other connection.
// Native unique-conflict overlap is independently owned by the ORM consumer;
// this is the Helpdesk transaction/admission composition boundary.
func verifyHelpdeskLabelEnsureConcurrentRequests(t *testing.T, rootContext context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), hasher auth.PasswordHasher, authenticated *helpdeskClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(rootContext, 15*time.Second)
	defer cancel()
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Concurrent label ensure"))
	if err != nil {
		t.Fatal(err)
	}
	peerBackend, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peerBackend.Close()
	observer, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	peer := &coordinatedPeerBackend{helpdeskBackend: peerBackend}
	peerRuntime, err := systemstate.OpenIdentity(ctx, peer, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher})
	if err != nil {
		t.Fatal(err)
	}
	leaderApp, err := helpdesk.New(runtime, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	peerApp, err := helpdesk.New(peerRuntime, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	audited, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	var workers sync.WaitGroup
	defer func() { unlock(); workers.Wait() }()
	leader := helpdeskHTTP(t, leaderApp, runtime, auth.PrincipalAuthorizer{}, func(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
		if err := runtime.AppendAudit(ctx, session, event); err != nil {
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
	follower := helpdeskHTTP(t, peerApp, peerRuntime, auth.PrincipalAuthorizer{})
	for _, client := range []*helpdeskClient{leader, follower} {
		client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		if response := client.request("GET", "/admin/", "", false); response.Code != 200 || client.csrf == "" {
			t.Fatal("concurrent client CSRF setup", response.Code)
		}
	}
	peer.entered = make(chan struct{})
	results := []chan *httptest.ResponseRecorder{make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)}
	start := func(index int, client *helpdeskClient) {
		workers.Go(func() {
			results[index] <- client.requestContext(ctx, "POST", "/api/labels/ensure/", labelInput("Shared concurrent label"), true)
		})
	}
	start(0, leader)
	select {
	case <-audited:
	case <-ctx.Done():
		t.Fatal("leader did not reach provisional audit")
	}
	start(1, follower)
	select {
	case <-peer.entered:
	case <-ctx.Done():
		t.Fatal("peer did not enter database coordination")
	}
	// Query directly; re-entering the leader Runtime would deadlock on its gate.
	count, err := models.LabelObjects.Using(observer).Filter(models.LabelFields.Name.Exact("Shared concurrent label")).Count(ctx)
	if err != nil || count != 0 {
		t.Fatal("uncommitted ensure label leaked to a peer", count, err)
	}
	unlock()
	var outcomes [2]labelEnsureResult
	for index := range results {
		select {
		case response := <-results[index]:
			want := http.StatusOK
			if index == 0 {
				want = http.StatusCreated
			}
			if response.Code != want || json.Unmarshal(response.Body.Bytes(), &outcomes[index]) != nil {
				t.Fatal("concurrent ensure request failed", index, response.Code)
			}
		case <-ctx.Done():
			t.Fatal("concurrent ensure did not finish")
		}
	}
	workers.Wait()
	if !outcomes[0].Created || outcomes[1].Created || outcomes[0].Label.ID != outcomes[1].Label.ID || outcomes[0].Label.Category != category.ID || outcomes[0].Label.Name != "Shared concurrent label" {
		t.Fatal("concurrent ensure results lost creation ownership")
	}
	if count, err := models.LabelObjects.Using(peerBackend).Filter(models.LabelFields.Name.Exact("Shared concurrent label")).Count(ctx); err != nil || count != 1 {
		t.Fatal("concurrent ensure committed duplicate labels", count, err)
	}
	assertEnsureAudit(t, ctx, peerRuntime, outcomes[0].Label.ID, 1, "Shared concurrent label")
}
