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
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

type collectionFaultBackend struct {
	helpdesk.Backend
	mode                                   string
	before                                 func() error
	afterScalar                            func()
	onEnter                                func()
	cancel                                 context.CancelFunc
	reads, atomics, writes, partialDeletes int
}

func (b *collectionFaultBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	b.reads++
	return b.Backend.Query(ctx, plan)
}

func (b *collectionFaultBackend) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	b.atomics++
	if before := b.before; before != nil {
		b.before = nil
		if err := before(); err != nil {
			return err
		}
	}
	err := b.Backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		if b.onEnter != nil {
			b.onEnter()
		}
		validator, valid := session.(db.SessionValidator)
		inserter, capable := session.(db.ConflictInserter)
		if !valid || !capable {
			return errors.New("collection test backend lacks native session capabilities")
		}
		return callback(&collectionFaultSession{RelationSession: session, validator: validator, inserter: inserter, owner: b})
	})
	if err == nil && b.mode == "commit_unknown" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	if err != nil && (b.mode == "rollback_unknown" || b.mode == "notfound_unknown") {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
	}
	return err
}

type collectionFaultSession struct {
	db.RelationSession
	validator db.SessionValidator
	inserter  db.ConflictInserter
	owner     *collectionFaultBackend
	written   bool
}

func (s *collectionFaultSession) ValidateSession(ctx context.Context) error {
	return s.validator.ValidateSession(ctx)
}
func (s *collectionFaultSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	s.owner.reads++
	if s.owner.mode == "candidate_error" && plan.Table() == "helpdesk_label" {
		return nil, errors.New("candidate query failed")
	}
	if s.owner.mode == "response_error" && s.written && plan.Table() == "helpdesk_ticket" {
		return nil, errors.New("response query failed after writes")
	}
	return s.RelationSession.Query(ctx, plan)
}
func (s *collectionFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	s.owner.writes++
	rows, err := s.RelationSession.Update(ctx, plan)
	if err == nil {
		s.written = true
		if s.owner.afterScalar != nil && plan.Table() == "helpdesk_ticket" {
			s.owner.afterScalar()
		}
	}
	return rows, err
}
func (s *collectionFaultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	s.owner.writes++
	key, err := s.RelationSession.Insert(ctx, plan)
	s.written = s.written || err == nil
	return key, err
}
func (s *collectionFaultSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	s.owner.writes++
	if s.owner.mode == "late_delete" && s.owner.partialDeletes > 0 {
		return 0, errors.New("second link delete failed")
	}
	rows, err := s.RelationSession.Delete(ctx, plan)
	if err == nil {
		s.written = true
		s.owner.partialDeletes++
	}
	return rows, err
}
func (s *collectionFaultSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	s.owner.writes++
	if s.owner.mode == "link_error" {
		return false, errors.New("link insert failed after scalar write")
	}
	if s.owner.mode == "rollback_unknown" {
		return false, validation.Reject(validation.NewErrors(validation.New("labels", "invalid_choice")), nil)
	}
	inserted, err := s.inserter.InsertOnConflict(ctx, plan)
	if err == nil {
		s.written = true
		if s.owner.cancel != nil {
			s.owner.cancel()
		}
	}
	return inserted, err
}

func collectionBody(subject string, keys []int64) string {
	encoded, _ := json.Marshal(struct {
		Subject string  `json:"subject"`
		Labels  []int64 `json:"labels"`
	}{subject, keys})
	return string(encoded)
}

func verifyTicketCollectionBoundaries(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, otherCategoryID int64, keys map[int64]int64, policy systemstate.CredentialPolicy) {
	t.Helper()
	b := &collectionFaultBackend{Backend: runtime}
	application, err := helpdesk.New(b, categoryID)
	if err != nil {
		t.Fatal(err)
	}
	client := helpdeskHTTP(t, application, runtime, auth.PrincipalAuthorizer{})
	client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
	if response := client.request("GET", "/admin/", "", false); response.Code != 200 {
		t.Fatal("collection CSRF bootstrap", response.Code)
	}
	owner := decodeCollectionTicket(t, client.request("POST", "/api/tickets/", collectionBody("Collection boundary", []int64{keys[7], keys[9]}), true), 201)
	path := fmt.Sprintf("/api/tickets/%d/", owner.ID)
	reset := func() {
		t.Helper()
		b.mode = ""
		b.cancel = nil
		b.afterScalar = nil
		b.before = nil
		b.partialDeletes = 0
		decodeCollectionTicket(t, client.request("PATCH", path, collectionBody("Collection boundary", []int64{keys[7], keys[9]}), true), 200)
	}
	checkOriginal := func(subject string, before map[int64]int64) {
		t.Helper()
		current := readUniqueTicket(t, ctx, runtime, owner.ID)
		if current.Subject != subject || !reflect.DeepEqual(collectionLinks(t, ctx, runtime, owner.ID), before) {
			t.Fatal("failed replacement changed scalar or link identity", current.Subject)
		}
	}
	t.Run("permission_and_csrf_before_validation_or_data", func(t *testing.T) {
		denied := helpdeskHTTP(t, application, runtime, helpdeskDeniedPermission{permission: helpdesk.ViewLabel})
		denied.cookies, denied.csrf = maps.Clone(client.cookies), client.csrf
		if response := denied.request("GET", "/admin/", "", false); response.Code != 200 {
			t.Fatal("denied collection CSRF bootstrap", response.Code)
		}
		for _, test := range []struct{ method, path string }{{"POST", "/api/tickets/"}, {"PUT", path}, {"PATCH", path}} {
			reads, atomics, writes := b.reads, b.atomics, b.writes
			response := denied.request(test.method, test.path, "not a valid body", true)
			if response.Code != 403 || strings.HasPrefix(test.path, "/api/") && !strings.Contains(response.Body.String(), "permission_denied") || b.reads != reads || b.atomics != atomics || b.writes != writes {
				t.Fatalf("%s %s: missing target permission reached input or storage: %d %s", test.method, test.path, response.Code, response.Body)
			}
		}
		// HTML carries CSRF in its bounded form body. Transport parsing precedes
		// CSRF/authentication; field validation and all model/choice I/O follow it.
		for _, target := range []string{"/admin/tickets/add/", fmt.Sprintf("/admin/tickets/change/?id=%d", owner.ID)} {
			reads, atomics, writes := b.reads, b.atomics, b.writes
			input := url.Values{"csrfmiddlewaretoken": {denied.csrf}, "subject": {""}, "labels": {"not an integer"}}
			response := denied.request("POST", target, input.Encode(), false)
			if response.Code != 403 || b.reads != reads || b.atomics != atomics || b.writes != writes {
				t.Fatalf("POST %s: missing target permission reached field validation or storage: %d %s", target, response.Code, response.Body)
			}
			response = denied.request("POST", target, "not a form body", true)
			if response.Code != 400 || b.reads != reads || b.atomics != atomics || b.writes != writes {
				t.Fatalf("POST %s: invalid transport reached storage: %d %s", target, response.Code, response.Body)
			}
			input.Set("csrfmiddlewaretoken", "invalid")
			response = client.request("POST", target, input.Encode(), false)
			if response.Code != 403 || b.reads != reads || b.atomics != atomics || b.writes != writes {
				t.Fatalf("POST %s: invalid CSRF reached field validation or storage: %d %s", target, response.Code, response.Body)
			}
		}
		for _, test := range []struct{ method, path string }{{"POST", "/api/tickets/"}, {"PUT", path}, {"PATCH", path}} {
			reads, atomics := b.reads, b.atomics
			old := client.csrf
			client.csrf = "invalid"
			response := client.request(test.method, test.path, collectionBody("must not write", []int64{keys[11]}), true)
			client.csrf = old
			if response.Code != 403 || b.reads != reads || b.atomics != atomics {
				t.Fatal("CSRF rejection entered collection transaction", response.Code)
			}
		}
	})
	t.Run("admin_complete_candidates_and_empty_replacement", func(t *testing.T) {
		var candidates []int64
		for i := range 25 {
			label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate(fmt.Sprintf("Collection candidate %02d", i), categoryID))
			if err != nil {
				t.Fatal(err)
			}
			candidates = append(candidates, label.ID)
		}
		changePath := fmt.Sprintf("/admin/tickets/change/?id=%d", owner.ID)
		form := client.request("GET", changePath, "", false)
		if form.Code != 200 || !strings.Contains(form.Body.String(), `<select name="labels" multiple>`) || !strings.Contains(form.Body.String(), fmt.Sprintf(`<option value="%d">Collection candidate 24</option>`, candidates[24])) || strings.Contains(form.Body.String(), "Collection reference 12") {
			t.Fatal("candidate scope or completeness", form.Code, form.Body)
		}
		input := url.Values{"subject": {"Admin replacement"}, "csrfmiddlewaretoken": {client.csrf}, "labels": {fmt.Sprint(keys[7]), fmt.Sprint(candidates[24]), fmt.Sprint(keys[7])}}
		if response := client.request("POST", changePath, input.Encode(), false); response.Code != 302 {
			t.Fatal("Admin multiple replacement", response.Code, response.Body)
		}
		if links := collectionLinks(t, ctx, runtime, owner.ID); len(links) != 2 || links[keys[7]] == 0 || links[candidates[24]] == 0 {
			t.Fatal("Admin truncated or duplicated selection", links)
		}
		input.Del("labels")
		if response := client.request("POST", changePath, input.Encode(), false); response.Code != 302 || len(collectionLinks(t, ctx, runtime, owner.ID)) != 0 {
			t.Fatal("missing optional HTML selection did not clear", response.Code, response.Body)
		}
		reset()
	})
	t.Run("scope_changes_after_binding", func(t *testing.T) {
		for _, adminForm := range []bool{false, true} {
			reset()
			before := collectionLinks(t, ctx, runtime, owner.ID)
			label, found, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(keys[11])).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
			if err != nil || !found {
				t.Fatal(err)
			}
			b.before = func() error {
				_, err := models.LabelObjects.Update(ctx, runtime, label, models.LabelPatch{}.WithCategoryID(otherCategoryID))
				return err
			}
			var response *httptest.ResponseRecorder
			if adminForm {
				input := url.Values{"subject": {"Must roll back"}, "labels": {fmt.Sprint(keys[7]), fmt.Sprint(keys[11])}, "csrfmiddlewaretoken": {client.csrf}}
				response = client.request("POST", fmt.Sprintf("/admin/tickets/change/?id=%d", owner.ID), input.Encode(), false)
			} else {
				response = client.request("PATCH", path, collectionBody("Must roll back", []int64{keys[7], keys[11]}), true)
			}
			want := 400
			if adminForm {
				want = 200
			}
			if response.Code != want || !strings.Contains(response.Body.String(), "invalid_choice") || strings.Contains(response.Body.String(), "Collection reference 12") {
				t.Fatal("stale label scope accepted", response.Code, response.Body)
			}
			checkOriginal("Collection boundary", before)
			label.CategoryID = otherCategoryID
			if _, err := models.LabelObjects.Update(ctx, runtime, label, models.LabelPatch{}.WithCategoryID(categoryID)); err != nil {
				t.Fatal(err)
			}
		}
		reset()
		before := collectionLinks(t, ctx, runtime, owner.ID)
		stored := readUniqueTicket(t, ctx, runtime, owner.ID)
		b.before = func() error {
			_, err := models.TicketObjects.Update(ctx, runtime, stored, models.TicketPatch{}.WithCategoryID(otherCategoryID))
			return err
		}
		if response := client.request("PATCH", path, collectionBody("Must not update moved owner", []int64{keys[11]}), true); response.Code != 404 {
			t.Fatal("stale owner scope accepted", response.Code, response.Body)
		}
		checkOriginal("Collection boundary", before)
		stored.CategoryID = otherCategoryID
		if _, err := models.TicketObjects.Update(ctx, runtime, stored, models.TicketPatch{}.WithCategoryID(categoryID)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("transaction_failures_never_publish_partial_sets", func(t *testing.T) {
		for _, mode := range []string{"candidate_error", "link_error", "late_delete", "response_error", "rollback_unknown"} {
			t.Run(mode, func(t *testing.T) {
				reset()
				before := collectionLinks(t, ctx, runtime, owner.ID)
				b.mode = mode
				calls := b.atomics
				wanted := []int64{keys[7], keys[11]}
				if mode == "late_delete" {
					wanted = []int64{}
				}
				response := client.request("PATCH", path, collectionBody("Must roll back", wanted), true)
				if response.Code != 500 || b.atomics != calls+1 || strings.Contains(response.Body.String(), "Must roll back") {
					t.Fatal("failed transaction reported success or retried", response.Code, response.Body)
				}
				if mode == "late_delete" && b.partialDeletes != 1 {
					t.Fatal("late failure did not follow a real delete")
				}
				checkOriginal("Collection boundary", before)
				b.mode = ""
			})
		}
	})
	t.Run("uncertain_not_found_is_execution_failure", func(t *testing.T) {
		reset()
		before := collectionLinks(t, ctx, runtime, owner.ID)
		b.mode = "notfound_unknown"
		response := client.request("PATCH", "/api/tickets/9223372036854775807/", collectionBody("Must not create", []int64{keys[7]}), true)
		b.mode = ""
		if response.Code != 500 {
			t.Fatal("uncertain rollback became confirmed 404", response.Code, response.Body)
		}
		checkOriginal("Collection boundary", before)
	})
	t.Run("cancellation_after_native_link_write", func(t *testing.T) {
		reset()
		before := collectionLinks(t, ctx, runtime, owner.ID)
		requestCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		b.cancel = cancel
		request := httptest.NewRequest(http.MethodPatch, "http://helpdesk.test"+path, strings.NewReader(collectionBody("Canceled", []int64{keys[11]}))).WithContext(requestCtx)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(websessionauth.DefaultCSRFHeader, client.csrf)
		for _, cookie := range client.cookies {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		client.application.ServeHTTP(response, request)
		b.cancel = nil
		if requestCtx.Err() != context.Canceled || response.Code != 500 {
			t.Fatal("late cancellation did not reject publication", requestCtx.Err(), response.Code)
		}
		checkOriginal("Collection boundary", before)
	})
	t.Run("unknown_commit_is_not_success_or_retry", func(t *testing.T) {
		reset()
		b.mode = "commit_unknown"
		calls := b.atomics
		response := client.request("PATCH", path, collectionBody("Unknown committed", []int64{keys[11]}), true)
		b.mode = ""
		if response.Code != 500 || b.atomics != calls+1 || strings.Contains(response.Body.String(), "Unknown committed") {
			t.Fatal("unknown commit published or retried", response.Code, response.Body)
		}
		if stored := readUniqueTicket(t, ctx, runtime, owner.ID); stored.Subject != "Unknown committed" {
			t.Fatal("fault did not follow a real commit")
		}
		if links := collectionLinks(t, ctx, runtime, owner.ID); len(links) != 1 || links[keys[11]] == 0 {
			t.Fatal("faulted commit lost complete set", links)
		}
	})
	t.Run("reopen_preserves_labels_and_scalar", func(t *testing.T) {
		reset()
		result := decodeCollectionTicket(t, client.request("PATCH", path, collectionBody("Durable collection", []int64{keys[7], keys[11]}), true), 200)
		reopened, err := open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		stored := readUniqueTicket(t, ctx, reopened, owner.ID)
		if stored.Subject != result.Subject {
			t.Fatal("scalar not durable")
		}
		links, err := models.TicketLabelObjects.Using(reopened).OrderBy(models.TicketLabelFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var actual []int64
		for _, link := range links {
			if link.TicketID == owner.ID {
				actual = append(actual, link.LabelID)
			}
		}
		slices.Sort(actual)
		if !slices.Equal(actual, result.Labels) {
			t.Fatal("response set not durable", actual, result.Labels)
		}
	})
	t.Run("two_runtime_replacements_are_serial", func(t *testing.T) {
		verifyCollectionConcurrency(t, ctx, runtime, open, authenticated, categoryID, owner.ID, keys, policy)
	})
	b.mode = ""
	if response := client.request("DELETE", path, "", true); response.Code != 204 {
		t.Fatal("boundary fixture cleanup", response.Code, response.Body)
	}
}
