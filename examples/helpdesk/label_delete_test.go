package helpdesk_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http/httptest"
	"reflect"
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

// Both native lifecycle owners call this with the real Session/CSRF adapter.
// The label and its link must commit together without deleting either endpoint
// of the other link. A prepared 204 cannot hide a failed or uncertain outcome.
func verifyTypedLabelDelete(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient, categoryID, outsideID int64) {
	t.Helper()
	for _, mode := range []string{"success", "ignored_body_query", "outside", "missing", "zero_id", "permission", "csrf", "anonymous", "missing_callback", "swallowed_scope", "scope_rollback_unknown", "validation_rejection", "late_delete", "cancel_delete", "commit_unknown", "post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) {
			category := categoryID
			if mode == "outside" || mode == "swallowed_scope" || mode == "scope_rollback_unknown" {
				category = outsideID
			}
			ticket, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Typed deletion ticket", category))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := models.TicketObjects.Delete(ctx, runtime, &ticket); err != nil {
					t.Error(err)
				}
			}()
			label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Typed deletion target", category))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if found, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(label.ID)).Exists(ctx); err != nil {
					t.Error(err)
				} else if found {
					if _, err := models.LabelObjects.Delete(ctx, runtime, &label); err != nil {
						t.Error(err)
					}
				}
			}()
			other, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Typed deletion retained", category))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := models.LabelObjects.Delete(ctx, runtime, &other); err != nil {
					t.Error(err)
				}
			}()
			for _, id := range []int64{label.ID, other.ID} {
				link, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(ticket.ID, id))
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if found, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.ID.Exact(link.ID)).Exists(ctx); err != nil {
						t.Error(err)
					} else if found {
						if _, err := models.TicketLabelObjects.Delete(ctx, runtime, &link); err != nil {
							t.Error(err)
						}
					}
				}()
			}
			beforeTicket := readUniqueTicket(t, ctx, runtime, ticket.ID)
			requestContext, cancel := context.WithCancel(ctx)
			defer cancel()
			backend := &labelDeleteBackend{Backend: runtime, mode: mode, cancel: cancel}
			application, err := helpdesk.New(backend, categoryID)
			if err != nil {
				t.Fatal(err)
			}
			var authorizer auth.Authorizer = auth.PrincipalAuthorizer{}
			if mode == "permission" {
				authorizer = helpdeskDeniedPermissions{helpdesk.DeleteLabel}
			}
			client := helpdeskHTTP(t, application, runtime, authorizer)
			client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
			// The composed site signs its own CSRF tokens, even with shared sessions.
			if response := client.request("GET", "/admin/", "", false); response.Code != 200 || client.csrf == "" {
				t.Fatal("could not obtain this site's CSRF token", response.Code)
			}
			path := fmt.Sprintf("/api/labels/%d/", label.ID)
			want, transactions := 500, 1
			switch mode {
			case "success", "ignored_body_query", "post_commit_cancel":
				want = 204
			case "outside":
				want = 404
			case "missing":
				path, want = "/api/labels/9223372036854775807/", 404
			case "zero_id":
				path, want, transactions = "/api/labels/0/", 404, 0
			case "permission", "csrf", "anonymous":
				want, transactions = 403, 0
			case "validation_rejection":
				want = 400
			}
			if mode == "ignored_body_query" {
				path += "?ignored=%zz"
			}
			body := &labelObservedBody{Reader: strings.NewReader("{")}
			request := httptest.NewRequest("DELETE", "http://helpdesk.test"+path, nil).WithContext(requestContext)
			request.Body, request.ContentLength = body, -1
			request.Header.Set("Content-Type", "application/json")
			if mode != "anonymous" {
				for _, cookie := range client.cookies {
					request.AddCookie(cookie)
				}
			}
			if mode != "csrf" {
				request.Header.Set(websessionauth.DefaultCSRFHeader, client.csrf)
			}
			response := httptest.NewRecorder()
			client.application.ServeHTTP(response, request)
			if response.Code != want || body.reads != 0 || backend.transactions != transactions || strings.Contains(response.Body.String(), "private") {
				t.Fatal("label deletion boundary", response.Code, response.Body, body.reads, backend.transactions)
			}
			if transactions == 0 && backend.reads+backend.writes != 0 {
				t.Fatal("admission or path rejection reached storage")
			}
			if want == 204 && (response.Body.Len() != 0 || response.Header().Get("Content-Type") != "") {
				t.Fatal("label 204 retained a representation")
			}
			if mode == "validation_rejection" && !strings.Contains(response.Body.String(), `"code":"protected"`) {
				t.Fatal("confirmed deletion rejection lost public diagnostics", response.Body)
			}
			if want == 500 && response.Body.String() != "Internal Server Error\n" {
				t.Fatal("unconfirmed deletion exposed a nonstandard failure body", response.Body)
			}
			assertHelpdeskResponseDocumented(t, client.document, "DELETE", "/api/labels/{id}/", response)
			stored, found, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(label.ID)).OrderBy(models.LabelFields.ID.Asc()).First(ctx)
			deleted := want == 204 || mode == "commit_unknown"
			if err != nil || found == deleted || found && stored != label {
				t.Fatal("label commit disagrees with response", found, err)
			}
			links, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.TicketID.Exact(ticket.ID)).All(ctx)
			wantLinks := 2
			if deleted {
				wantLinks = 1
			}
			if err != nil || len(links) != wantLinks || !reflect.DeepEqual(beforeTicket, readUniqueTicket(t, ctx, runtime, ticket.ID)) {
				t.Fatal("cascade or rollback changed the ticket/link set", len(links), err)
			}
			retained, err := models.LabelObjects.Using(runtime).Filter(models.LabelFields.ID.Exact(other.ID)).Get(ctx)
			if err != nil || retained != other {
				t.Fatal("deletion changed the unrelated label", err)
			}
			for _, link := range links {
				if deleted && link.LabelID != other.ID {
					t.Fatal("deletion retained the wrong link")
				}
			}
			if mode == "late_delete" && backend.writes != 2 || deleted && backend.writes != 2 {
				t.Fatal("cascade failure was not after link DML, or deletion retried", backend.writes)
			}
		})
	}
}

type labelDeleteBackend struct {
	helpdesk.Backend
	mode                        string
	cancel                      context.CancelFunc
	transactions, reads, writes int
}

func (b *labelDeleteBackend) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	b.transactions++
	if b.mode == "missing_callback" {
		return nil
	}
	if b.mode == "validation_rejection" {
		return validation.Reject(validation.NewErrors(validation.New(validation.NonField, "protected")), nil)
	}
	err := b.Backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		return callback(&labelDeleteSession{RelationSession: session, owner: b})
	})
	if b.mode == "swallowed_scope" {
		return nil
	}
	if err != nil && b.mode == "scope_rollback_unknown" {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
	}
	if err == nil && b.mode == "commit_unknown" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	if err == nil && b.mode == "post_commit_cancel" {
		b.cancel()
	}
	return err
}

type labelDeleteSession struct {
	db.RelationSession
	owner *labelDeleteBackend
}

func (s *labelDeleteSession) ValidateSession(ctx context.Context) error {
	validator, ok := s.RelationSession.(db.SessionValidator)
	if !ok {
		return errors.New("native deletion session validator missing")
	}
	return validator.ValidateSession(ctx)
}

func (s *labelDeleteSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	s.owner.reads++
	return s.RelationSession.Query(ctx, plan)
}

func (s *labelDeleteSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	s.owner.writes++
	if s.owner.mode == "late_delete" && s.owner.writes == 2 {
		return 0, errors.New("private failure after link deletion")
	}
	if s.owner.mode == "cancel_delete" {
		s.owner.cancel()
		return 0, context.Canceled
	}
	return s.RelationSession.Delete(ctx, plan)
}
