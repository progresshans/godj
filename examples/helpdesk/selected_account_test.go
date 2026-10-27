package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	identitymodels "github.com/progresshans/godj/identity/models"
)

func selectedHelpdeskStatus(t *testing.T, result *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if result.Code != expected {
		t.Fatalf("selected Helpdesk status=%d want=%d body=%s", result.Code, expected, result.Body.String())
	}
	if expected >= 400 {
		for _, private := range []string{"selected visible", "private outside", selectedHelpdeskPrivateFault.Error(), selectedHelpdeskPassword} {
			if strings.Contains(result.Body.String(), private) {
				t.Fatal("failure disclosed private data")
			}
		}
	}
}

type selectedHelpdeskOperation struct {
	name, method, path, body string
	permissions              []auth.Permission
}

func selectedHelpdeskOperations(f *selectedHelpdeskFixture) []selectedHelpdeskOperation {
	ticket := fmt.Sprintf("/api/tickets/%d/", f.ticket.ID)
	label := fmt.Sprintf("/api/labels/%d/", f.label.ID)
	report := fmt.Sprintf("/api/service-reports/%d/", f.report.ID)
	link := fmt.Sprintf("/api/ticket-labels/%d/", f.link.ID)
	asOf := "?as_of=2026-10-27T00:00:00Z"
	return []selectedHelpdeskOperation{
		{"tickets", "GET", "/api/tickets/", "", []auth.Permission{helpdesk.ViewTicket}},
		{"ticket", "GET", ticket, "", []auth.Permission{helpdesk.ViewTicket}},
		{"summary", "GET", "/api/tickets/summary/", "", []auth.Permission{helpdesk.ViewTicket}},
		{"cost", "GET", "/api/tickets/cost-preview/", "", []auth.Permission{helpdesk.ViewTicket}},
		{"deadline", "GET", "/api/tickets/deadline-preview/" + asOf, "", []auth.Permission{helpdesk.ViewTicket}},
		{"calendar", "GET", "/api/tickets/deadline-calendar/" + asOf, "", []auth.Permission{helpdesk.ViewTicket}},
		{"service", "GET", "/api/tickets/service-preview/", "", []auth.Permission{helpdesk.ViewTicket}},
		{"labels", "GET", "/api/labels/", "", []auth.Permission{helpdesk.ViewLabel}},
		{"label", "GET", label, "", []auth.Permission{helpdesk.ViewLabel}},
		{"usage", "GET", "/api/labels/usage/", "", []auth.Permission{helpdesk.ViewLabel, helpdesk.ViewTicket}},
		{"reports", "GET", "/api/service-reports/", "", []auth.Permission{helpdesk.ViewServiceReport}},
		{"report", "GET", report, "", []auth.Permission{helpdesk.ViewServiceReport}},
		{"ticket_report", "GET", ticket + "service-report/", "", []auth.Permission{helpdesk.ViewServiceReport}},
		{"report_status", "GET", "/api/service-reports/status/", "", []auth.Permission{helpdesk.ViewServiceReport, helpdesk.ViewTicket}},
		{"links", "GET", "/api/ticket-labels/", "", []auth.Permission{helpdesk.ViewTicketLabel}},
		{"link", "GET", link, "", []auth.Permission{helpdesk.ViewTicketLabel}},
		{"create_ticket", "POST", "/api/tickets/", `{"subject":"denied ticket"}`, []auth.Permission{helpdesk.AddTicket, helpdesk.ViewLabel}},
		{"replace_ticket", "PUT", ticket, `{"subject":"denied ticket"}`, []auth.Permission{helpdesk.ChangeTicket, helpdesk.ViewLabel}},
		{"patch_ticket", "PATCH", ticket, `{"subject":"denied ticket"}`, []auth.Permission{helpdesk.ChangeTicket, helpdesk.ViewLabel}},
		{"delete_ticket", "DELETE", ticket, "", []auth.Permission{helpdesk.DeleteTicket}},
		{"bulk_create", "POST", "/api/tickets/bulk/", fmt.Sprintf(`[{"subject":"denied bulk","labels":[%d]}]`, f.label.ID), []auth.Permission{helpdesk.AddTicket, helpdesk.ViewLabel}},
		{"bulk_update", "PATCH", "/api/tickets/bulk/", fmt.Sprintf(`[{"id":%d,"subject":"denied bulk"}]`, f.ticket.ID), []auth.Permission{helpdesk.ChangeTicket, helpdesk.ViewLabel}},
		{"priority", "POST", "/api/tickets/raise-priority/", fmt.Sprintf(`{"ids":[%d]}`, f.ticket.ID), []auth.Permission{helpdesk.ChangeTicket, helpdesk.ViewLabel}},
		{"create_label", "POST", "/api/labels/", `{"name":"denied label"}`, []auth.Permission{helpdesk.AddLabel}},
		{"replace_label", "PUT", label, `{"name":"denied label"}`, []auth.Permission{helpdesk.ChangeLabel}},
		{"patch_label", "PATCH", label, `{"name":"denied label"}`, []auth.Permission{helpdesk.ChangeLabel}},
		{"delete_label", "DELETE", label, "", []auth.Permission{helpdesk.DeleteLabel}},
		{"ensure", "POST", "/api/labels/ensure/", `{"name":"denied ensure"}`, []auth.Permission{helpdesk.AddLabel, helpdesk.ViewLabel}},
		{"create_report", "POST", "/api/service-reports/", fmt.Sprintf(`{"ticket":%d,"summary":"denied report"}`, f.ticket.ID), []auth.Permission{helpdesk.AddServiceReport}},
		{"replace_report", "PUT", report, fmt.Sprintf(`{"ticket":%d,"summary":"denied report"}`, f.ticket.ID), []auth.Permission{helpdesk.ChangeServiceReport}},
		{"patch_report", "PATCH", report, `{"summary":"denied report"}`, []auth.Permission{helpdesk.ChangeServiceReport}},
		{"delete_report", "DELETE", report, "", []auth.Permission{helpdesk.DeleteServiceReport}},
		{"save_report", "PUT", ticket + "service-report/", `{"summary":"denied report"}`, []auth.Permission{helpdesk.AddServiceReport, helpdesk.ChangeServiceReport, helpdesk.ViewServiceReport}},
		{"create_link", "POST", "/api/ticket-labels/", fmt.Sprintf(`{"ticket":%d,"label":%d}`, f.ticket.ID, f.label.ID), []auth.Permission{helpdesk.AddTicketLabel, helpdesk.ViewTicket, helpdesk.ViewLabel}},
		{"replace_link", "PUT", link, fmt.Sprintf(`{"ticket":%d,"label":%d}`, f.ticket.ID, f.label.ID), []auth.Permission{helpdesk.ChangeTicketLabel, helpdesk.ViewTicket, helpdesk.ViewLabel}},
		{"patch_link", "PATCH", link, `{}`, []auth.Permission{helpdesk.ChangeTicketLabel, helpdesk.ViewTicket, helpdesk.ViewLabel}},
		{"delete_link", "DELETE", link, "", []auth.Permission{helpdesk.DeleteTicketLabel}},
	}
}

func runSelectedHelpdeskScope(t *testing.T, f *selectedHelpdeskFixture) {
	t.Helper()
	actor := f.credential.Principal()
	t.Run("native_reads_and_audited_writes", func(t *testing.T) {
		probe := &selectedHelpdeskProbe{SelectedBackend: f.backend}
		client := f.client(t, probe, auth.PrincipalAuthorizer{})
		if probe.snapshots+probe.scalarScopes+probe.relationScopes+probe.rootIO != 0 {
			t.Fatal("constructing selected Helpdesk performed I/O")
		}
		before := f.state(t)
		for _, operation := range selectedHelpdeskOperations(f) {
			if operation.method != "GET" {
				continue
			}
			response := client.requestContext(t.Context(), "GET", operation.path, "", true)
			selectedHelpdeskStatus(t, response, http.StatusOK)
			if strings.Contains(response.Body.String(), "private outside ticket") {
				t.Fatal("API crossed assigned category", operation.name)
			}
		}
		for _, path := range []string{"/admin/tickets/", fmt.Sprintf("/admin/tickets/change/?id=%d", f.ticket.ID), "/admin/tickets/add/choices/labels/", "/admin/tickets/collection-set/edit/", helpdesk.TicketEditorPath, helpdesk.TicketSummaryPath, helpdesk.ServiceReportStatusPath, helpdesk.LabelUsagePath, helpdesk.CostPreviewPath, helpdesk.DeadlinePreviewPath + "?as_of=2026-10-27T00:00:00Z", helpdesk.DeadlineCalendarPath + "?as_of=2026-10-27T00:00:00Z", helpdesk.ServicePreviewPath} {
			response := client.requestContext(t.Context(), "GET", path, "", false)
			selectedHelpdeskStatus(t, response, http.StatusOK)
			if strings.Contains(response.Body.String(), "private outside") {
				t.Fatal("HTML crossed assigned category", path)
			}
		}
		f.unchanged(t, before)
		for _, reader := range probe.readHandles {
			if err := reader.ValidateSession(t.Context()); err == nil {
				t.Fatal("HTML/API retained a live read scope")
			}
		}
		selectedHelpdeskStatus(t, client.requestContext(t.Context(), "POST", "/api/labels/ensure/", `{"name":"selected ensured label"}`, true), http.StatusCreated)
		label, err := models.LabelObjects.Using(f.backend).Filter(models.LabelFields.Name.Exact("selected ensured label")).Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if events := f.history(t, actor, "helpdesk.label", label.ID); len(events) != 1 || events[0].Action() != admin.ActionAdd || len(events[0].ChangedFields()) != 0 {
			t.Fatal("ensure did not commit exact selected audit")
		}
		before = f.state(t)
		selectedHelpdeskStatus(t, client.requestContext(t.Context(), "POST", "/api/labels/ensure/", `{"name":"selected ensured label"}`, true), http.StatusOK)
		f.unchanged(t, before)
		selectedHelpdeskStatus(t, client.requestContext(t.Context(), "POST", "/api/tickets/bulk/", fmt.Sprintf(`[{"subject":"selected bulk ticket","labels":[%d]}]`, f.label.ID), true), http.StatusCreated)
		created, err := models.TicketObjects.Using(f.backend).Filter(models.TicketFields.Subject.Exact("selected bulk ticket")).Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if count, err := models.TicketLabelObjects.Using(f.backend).Filter(models.TicketLabelFields.TicketID.Exact(created.ID), models.TicketLabelFields.LabelID.Exact(f.label.ID)).Count(t.Context()); err != nil || count != 1 {
			t.Fatal("selected bulk lost its label", err)
		}
		selectedHelpdeskStatus(t, client.requestContext(t.Context(), "POST", "/api/tickets/raise-priority/", fmt.Sprintf(`{"ids":[%d]}`, created.ID), true), http.StatusOK)
		stored, err := models.TicketObjects.Using(f.backend).Filter(models.TicketFields.ID.Exact(created.ID)).Get(t.Context())
		if err != nil || stored.Priority == nil || *stored.Priority != 0 || stored.CategoryID != f.category.ID {
			t.Fatal("selected native priority result", err)
		}
		events := f.history(t, actor, "helpdesk.ticket", created.ID)
		if len(events) != 2 || events[0].Action() != admin.ActionChange || !slices.Equal(events[0].ChangedFields(), []string{"priority"}) || events[1].Action() != admin.ActionAdd {
			t.Fatal("bulk and priority audit sequence differs")
		}
		selectedHelpdeskStatus(t, client.requestContext(t.Context(), "PUT", fmt.Sprintf("/api/tickets/%d/service-report/", f.ticket.ID), `{"summary":"selected updated report"}`, true), http.StatusOK)
		if events := f.history(t, actor, "helpdesk.service_report", f.report.ID); len(events) != 1 || events[0].Action() != admin.ActionChange || !slices.Equal(events[0].ChangedFields(), []string{"summary"}) {
			t.Fatal("selected report audit differs")
		}
		if probe.rootIO != 0 || probe.plainTransactions != 0 || probe.scalarScopes == 0 || probe.relationScopes == 0 || probe.auditAttempts != 4 {
			t.Fatalf("native selected owners root=%d ordinary=%d scalar=%d relation=%d audits=%d", probe.rootIO, probe.plainTransactions, probe.scalarScopes, probe.relationScopes, probe.auditAttempts)
		}
	})

	t.Run("current_api_permissions", func(t *testing.T) {
		probe := &selectedHelpdeskProbe{SelectedBackend: f.backend}
		client := f.client(t, probe, auth.PrincipalAuthorizer{})
		for _, operation := range selectedHelpdeskOperations(f) {
			// Prove each input reaches its native operation before testing denial.
			// Abort before opening the write owner, avoiding fixture mutation.
			if operation.method != "GET" {
				entered := 0
				probe.beforeWrite = func(context.Context) error { entered++; return selectedHelpdeskPrivateFault }
				selectedHelpdeskStatus(t, client.requestContext(t.Context(), operation.method, operation.path, operation.body, true), http.StatusInternalServerError)
				if entered != 1 {
					t.Fatal("negative input never reached native write", operation.name)
				}
			}
			for _, permission := range operation.permissions {
				func() {
					restore := f.revoke(t, permission)
					defer restore()
					before := f.state(t)
					reads, writes := probe.businessReads, probe.businessWrites
					response := client.requestContext(t.Context(), operation.method, operation.path, operation.body, true)
					if response.Code != http.StatusForbidden {
						t.Fatalf("%s retained %s: %d %s", operation.name, permission, response.Code, response.Body.String())
					}
					selectedHelpdeskStatus(t, response, http.StatusForbidden)
					if reads != probe.businessReads || writes != probe.businessWrites {
						t.Fatal("denied operation touched business data", operation.name, permission)
					}
					f.unchanged(t, before)
				}()
			}
		}
		if probe.rootIO != 0 || probe.plainTransactions != 0 {
			t.Fatal("permission test escaped native owner")
		}
	})

	t.Run("current_admin_forms_choices_and_roles", func(t *testing.T) { selectedHelpdeskAdminAuthority(t, f) })
	t.Run("read_ownership_and_cleanup", func(t *testing.T) { selectedHelpdeskReadFailures(t, f) })
	t.Run("write_rollback_and_unknown", func(t *testing.T) { selectedHelpdeskWriteFailures(t, f) })
	t.Run("authority_after_admission_before_fence", func(t *testing.T) {
		for _, kind := range []string{"scalar", "relation"} {
			probe := &selectedHelpdeskProbe{SelectedBackend: f.backend}
			client := f.client(t, probe, auth.PrincipalAuthorizer{})
			permission, path, body := helpdesk.AddLabel, "/api/labels/ensure/", `{"name":"fence denied"}`
			if kind == "relation" {
				permission, path, body = helpdesk.AddTicket, "/api/tickets/bulk/", `[{"subject":"fence denied"}]`
			}
			before := f.state(t)
			var restore func()
			probe.beforeWrite = func(context.Context) error { restore = f.revoke(t, permission); return nil }
			response := client.requestContext(t.Context(), "POST", path, body, true)
			if restore == nil {
				t.Fatal("write never reached the fence")
			}
			restore()
			selectedHelpdeskStatus(t, response, http.StatusForbidden)
			if probe.businessReads != 0 || probe.businessWrites != 0 || probe.scalarScopes+probe.relationScopes != 1 {
				t.Fatal("fence did not recheck authority before data")
			}
			f.unchanged(t, before)
		}
	})
	t.Run("coherent_authority_and_data_snapshot", func(t *testing.T) { selectedHelpdeskCoherentRead(t, f) })
	t.Run("old_domain_authority_and_history", func(t *testing.T) {
		probe := &selectedHelpdeskProbe{SelectedBackend: f.backend}
		client := f.client(t, probe, auth.PrincipalAuthorizer{})
		before := f.state(t)
		oldHistoryCount := len(f.history(t, actor, "helpdesk.service_report", f.report.ID))
		if oldHistoryCount == 0 {
			t.Fatal("domain isolation has no prior business audit")
		}
		change, err := domain.Prepare(t.Context(), f.ownership.RoleFingerprint(), strings.Repeat("e", 64))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
			_, err := domain.TransitionIn(t.Context(), scope, f.ownership, change, time.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for _, operation := range []selectedHelpdeskOperation{{method: "GET", path: "/api/tickets/"}, {method: "POST", path: "/api/labels/ensure/", body: `{"name":"old domain"}`}, {method: "GET", path: "/admin/tickets/"}} {
			selectedHelpdeskStatus(t, client.requestContext(t.Context(), operation.method, operation.path, operation.body, true), http.StatusForbidden)
		}
		if probe.businessReads+probe.businessWrites != 0 {
			t.Fatal("old domain touched business state")
		}
		current, err := f.runtime.Authenticator().Authenticate(t.Context(), "scope-operator", selectedHelpdeskPassword)
		if err != nil {
			t.Fatal(err)
		}
		if current.Principal().ID() != actor.ID() {
			t.Fatal("domain test changed the account identity")
		}
		old, _ := actor.CredentialDomain()
		next, bound := current.Principal().CredentialDomain()
		if !bound || old == next {
			t.Fatal("credential inherited previous domain")
		}
		if len(f.history(t, current.Principal(), "helpdesk.service_report", f.report.ID)) != 0 || len(f.history(t, actor, "helpdesk.service_report", f.report.ID)) != oldHistoryCount {
			t.Fatal("audit history crossed credential domain")
		}
		f.unchanged(t, before)
	})
}

func selectedHelpdeskReadFailures(t *testing.T, f *selectedHelpdeskFixture) {
	for _, mode := range []string{"nil", "root", "no_validator", "writable", "expired", "skip", "repeat", "swallow", "late", "cleanup", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			probe := &selectedHelpdeskProbe{SelectedBackend: f.backend, readMode: mode}
			if mode == "cancel" {
				probe.cancel = cancel
			}
			client := f.client(t, probe, auth.PrincipalAuthorizer{})
			before := f.state(t)
			selectedHelpdeskStatus(t, client.requestContext(ctx, "GET", fmt.Sprintf("/api/tickets/%d/", f.ticket.ID), "", true), http.StatusInternalServerError)
			if probe.rootIO != 0 || probe.plainTransactions != 0 || probe.snapshots != 1 {
				t.Fatal("read failure escaped or retried its owner")
			}
			if mode == "late" {
				if probe.lateRead == nil {
					t.Fatal("missing delayed read callback")
				}
				err := f.backend.ReadSnapshot(t.Context(), probe.lateRead)
				if err == nil || probe.businessReads != 0 {
					t.Fatal("sealed callback resumed database work")
				}
			}
			f.unchanged(t, before)
		})
	}
	// A denied current actor is still provisional until read cleanup succeeds.
	for _, path := range []string{"/api/tickets/", "/admin/tickets/"} {
		func() {
			restore := f.revoke(t, helpdesk.ViewTicket, helpdesk.ChangeTicket)
			defer restore()
			probe := &selectedHelpdeskProbe{SelectedBackend: f.backend, readMode: "cleanup"}
			client := f.client(t, probe, auth.PrincipalAuthorizer{})
			selectedHelpdeskStatus(t, client.requestContext(t.Context(), "GET", path, "", true), http.StatusInternalServerError)
			if probe.businessReads != 0 {
				t.Fatal("denied cleanup read business data")
			}
		}()
	}
}

func selectedHelpdeskWriteFailures(t *testing.T, f *selectedHelpdeskFixture) {
	for _, mode := range []string{"audit_failure", "rollback_unknown", "commit_unknown", "skip", "repeat"} {
		t.Run(mode, func(t *testing.T) {
			for _, kind := range []string{"scalar", "relation"} {
				probe := &selectedHelpdeskProbe{SelectedBackend: f.backend, writeMode: mode}
				client := f.client(t, probe, auth.PrincipalAuthorizer{})
				name := mode + " " + kind
				path, body := "/api/labels/ensure/", fmt.Sprintf(`{"name":%q}`, name)
				if kind == "relation" {
					path, body = "/api/tickets/bulk/", fmt.Sprintf(`[{"subject":%q,"labels":[%d]}]`, name, f.label.ID)
				}
				before := f.state(t)
				selectedHelpdeskStatus(t, client.requestContext(t.Context(), "POST", path, body, true), http.StatusInternalServerError)
				if probe.rootIO != 0 || probe.plainTransactions != 0 || probe.scalarScopes+probe.relationScopes != 1 {
					t.Fatal("failed write escaped or retried its owner")
				}
				if mode == "skip" {
					if probe.businessWrites+probe.auditAttempts != 0 {
						t.Fatal("skipped callback wrote data")
					}
				} else if probe.businessWrites == 0 || probe.auditAttempts != 1 {
					t.Fatal("fault did not exercise a real write and exactly one domain audit", mode, kind, probe.businessWrites, probe.auditAttempts)
				}
				if mode != "commit_unknown" {
					f.unchanged(t, before)
					continue
				}
				after := f.state(t)
				if len(after.audits) != len(before.audits)+1 || !reflect.DeepEqual(after.audits[:len(before.audits)], before.audits) {
					t.Fatal("uncertain commit lost or repeated audit")
				}
				after.audits = after.audits[:len(before.audits)]
				if kind == "scalar" {
					if len(after.labels) != len(before.labels)+1 {
						t.Fatal("unknown scalar did not follow one native commit")
					}
					added := after.labels[len(before.labels)]
					if added.Name != name || added.CategoryID != f.category.ID || len(f.history(t, f.credential.Principal(), "helpdesk.label", added.ID)) != 1 {
						t.Fatal("unknown scalar committed wrong row or domain")
					}
					after.labels = after.labels[:len(before.labels)]
				} else {
					if len(after.tickets) != len(before.tickets)+1 || len(after.links) != len(before.links)+1 {
						t.Fatal("unknown relation did not follow one native commit")
					}
					added, link := after.tickets[len(before.tickets)], after.links[len(before.links)]
					if added.Subject != name || added.CategoryID != f.category.ID || link.TicketID != added.ID || link.LabelID != f.label.ID || len(f.history(t, f.credential.Principal(), "helpdesk.ticket", added.ID)) != 1 {
						t.Fatal("unknown relation committed wrong row, links or domain")
					}
					after.tickets, after.links = after.tickets[:len(before.tickets)], after.links[:len(before.links)]
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatal("unknown commit changed unrelated rows")
				}
			}
		})
	}
}

func selectedHelpdeskCoherentRead(t *testing.T, f *selectedHelpdeskFixture) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	second, err := f.open(ctx)
	if err != nil {
		t.Fatal("open concurrent selected backend failed")
	}
	defer second.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	probe := &selectedHelpdeskProbe{SelectedBackend: f.backend}
	probe.beforeQuery = func(ctx context.Context, table string) error {
		if table != "helpdesk_ticket" {
			return nil
		}
		probe.beforeQuery = nil
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	client := f.client(t, probe, auth.PrincipalAuthorizer{})
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- client.requestContext(ctx, "GET", fmt.Sprintf("/api/tickets/%d/", f.ticket.ID), "", true)
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-result
		}
	}()
	select {
	case <-entered:
	case response := <-result:
		joined = true
		t.Fatalf("coherent read never reached gated business query: %d", response.Code)
	case <-ctx.Done():
		t.Fatal("coherent read did not reach business scope")
	}
	old, err := models.TicketObjects.Using(second).Filter(models.TicketFields.ID.Exact(f.ticket.ID)).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var removed identitymodels.UserPermissionsLink
	err = second.CoordinatedAtomic(ctx, func(scope db.Session) error {
		var err error
		removed, err = identitymodels.UserPermissionsLinkObjects.Using(scope).Filter(identitymodels.UserPermissionsLinkFields.SourceID.Exact(f.account.ID), identitymodels.UserPermissionsLinkFields.TargetID.Exact(f.permissions[helpdesk.ViewTicket])).Get(ctx)
		if err != nil {
			return err
		}
		if count, err := identitymodels.UserPermissionsLinkObjects.Delete(ctx, scope, &removed); err != nil || count != 1 {
			return errors.Join(err, errors.New("concurrent grant was not removed"))
		}
		_, err = models.TicketObjects.Patch(ctx, scope, old, (models.TicketPatch{}).WithSubject("changed after authorization"))
		return err
	})
	close(release)
	response := <-result
	joined = true
	if err != nil {
		t.Fatal("concurrent selected authority/data commit", err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := identitymodels.UserPermissionsLinkObjects.Create(cleanup, f.backend, identitymodels.NewUserPermissionsLinkCreate(f.account.ID, f.permissions[helpdesk.ViewTicket])); err != nil {
			t.Error(err)
		}
		if _, err := models.TicketObjects.Patch(cleanup, f.backend, old, (models.TicketPatch{}).WithSubject(old.Subject)); err != nil {
			t.Error(err)
		}
	})
	selectedHelpdeskStatus(t, response, http.StatusOK)
	var decoded struct {
		Ticket struct {
			Subject string `json:"subject"`
		} `json:"ticket"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded.Ticket.Subject != old.Subject {
		t.Fatal("authorization and business data did not share one snapshot", err)
	}
	reads := probe.businessReads
	selectedHelpdeskStatus(t, client.requestContext(t.Context(), "GET", "/api/tickets/", "", true), http.StatusForbidden)
	if probe.businessReads != reads {
		t.Fatal("next snapshot retained revoked authority")
	}
}

func selectedHelpdeskAdminAuthority(t *testing.T, f *selectedHelpdeskFixture) {
	probe := &selectedHelpdeskProbe{SelectedBackend: f.backend}
	client := f.client(t, probe, auth.PrincipalAuthorizer{})
	change := fmt.Sprintf("/admin/tickets/change/?id=%d", f.ticket.ID)
	// Save the real parent/inline controls through the selected Admin owner.
	values, action := adminFormSubmission(t, client.requestContext(t.Context(), "GET", change, "", false), change)
	values.Set("subject", "selected Admin ticket")
	values.Set("reports-0-summary", "selected Admin report")
	beforeSave := f.state(t)
	selectedHelpdeskStatus(t, client.requestContext(t.Context(), "POST", action, values.Encode(), false), http.StatusFound)
	afterSave := f.state(t)
	if len(afterSave.audits) != len(beforeSave.audits)+2 || !reflect.DeepEqual(afterSave.audits[:len(beforeSave.audits)], beforeSave.audits) {
		t.Fatal("Admin parent/inline did not commit exactly two audits")
	}
	afterSave.audits = afterSave.audits[:len(beforeSave.audits)]
	for index := range beforeSave.tickets {
		if beforeSave.tickets[index].ID == f.ticket.ID {
			beforeSave.tickets[index].Subject = "selected Admin ticket"
		}
	}
	for index := range beforeSave.reports {
		if beforeSave.reports[index].ID == f.report.ID {
			beforeSave.reports[index].Summary = "selected Admin report"
		}
	}
	if !reflect.DeepEqual(afterSave, beforeSave) {
		t.Fatal("selected Admin lost parent/inline state or changed unrelated values")
	}
	if events := f.history(t, f.credential.Principal(), "helpdesk.ticket", f.ticket.ID); len(events) != 1 || events[0].Action() != admin.ActionChange || !slices.Equal(events[0].ChangedFields(), []string{"subject", "reports"}) {
		t.Fatal("selected Admin parent audit differs")
	}
	if events := f.history(t, f.credential.Principal(), "helpdesk.service_report", f.report.ID); len(events) != 2 || events[0].Action() != admin.ActionChange || !slices.Equal(events[0].ChangedFields(), []string{"summary"}) {
		t.Fatal("selected Admin inline audit differs")
	}
	// Read-or-change Admin admission remains distinct from API ViewTicket.
	func() {
		restore := f.revoke(t, helpdesk.ViewTicket)
		defer restore()
		selectedHelpdeskStatus(t, client.requestContext(t.Context(), "GET", "/admin/tickets/", "", false), http.StatusOK)
		selectedHelpdeskStatus(t, client.requestContext(t.Context(), "GET", "/api/tickets/", "", true), http.StatusForbidden)
		selectedHelpdeskStatus(t, client.requestContext(t.Context(), "GET", helpdesk.TicketEditorPath, "", false), http.StatusForbidden)
	}()
	for _, mode := range []string{"inactive", "not_staff"} {
		func() {
			current, err := identitymodels.UserObjects.Using(f.backend).Filter(identitymodels.UserFields.ID.Exact(f.account.ID)).Get(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			patch := (identitymodels.UserPatch{}).WithStaff(false)
			if mode == "inactive" {
				patch = (identitymodels.UserPatch{}).WithActive(false)
			}
			changed, err := identitymodels.UserObjects.Patch(t.Context(), f.backend, current, patch)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := identitymodels.UserObjects.Patch(t.Context(), f.backend, changed, (identitymodels.UserPatch{}).WithStaff(current.Staff).WithActive(current.Active)); err != nil {
					t.Error(err)
				}
			}()
			before := f.state(t)
			selectedHelpdeskStatus(t, client.requestContext(t.Context(), "GET", "/admin/tickets/", "", false), http.StatusForbidden)
			status := http.StatusOK
			if mode == "inactive" {
				status = http.StatusForbidden
			}
			selectedHelpdeskStatus(t, client.requestContext(t.Context(), "GET", "/api/tickets/", "", true), status)
			f.unchanged(t, before)
		}()
	}
	for _, permission := range []auth.Permission{helpdesk.AddTicket, helpdesk.ViewLabel} {
		func() {
			restore := f.revoke(t, permission)
			defer restore()
			selectedHelpdeskStatus(t, client.requestContext(t.Context(), "GET", "/admin/tickets/add/choices/labels/", "", false), http.StatusForbidden)
		}()
	}
	func() {
		restore := f.revoke(t, helpdesk.ViewCategory)
		defer restore()
		selectedHelpdeskStatus(t, client.requestContext(t.Context(), "GET", "/admin/categories/", "", false), http.StatusForbidden)
	}()
	// A form rendered with the old grants cannot validate/save changed inline
	// values after the child permission has been revoked.
	values, action = adminFormSubmission(t, client.requestContext(t.Context(), "GET", change, "", false), change)
	if values.Get("reports-0-id") != strconv.FormatInt(f.report.ID, 10) {
		t.Fatal("selected inline omitted its current identity")
	}
	values.Set("reports-0-summary", "denied inline update")
	func() {
		restore := f.revoke(t, helpdesk.ChangeServiceReport)
		defer restore()
		before := f.state(t)
		selectedHelpdeskStatus(t, client.requestContext(t.Context(), "POST", action, values.Encode(), false), http.StatusForbidden)
		f.unchanged(t, before)
	}()
	// Current collection-set binding must still handle a grant change between
	// the displayed controls and the write fence; it may reject as form data.
	setPath := "/admin/tickets/collection-set/edit/"
	values, action = adminFormSubmission(t, client.requestContext(t.Context(), "GET", setPath, "", false), setPath)
	values.Set("tickets-0-subject", "denied collection update")
	func() {
		restore := f.revoke(t, helpdesk.ChangeTicket)
		defer restore()
		before := f.state(t)
		response := client.requestContext(t.Context(), "POST", action, values.Encode(), false)
		if response.Code != http.StatusForbidden && response.Code != http.StatusBadRequest {
			t.Fatal("revoked collection change was not rejected", response.Code)
		}
		f.unchanged(t, before)
	}()
	for _, permission := range []auth.Permission{helpdesk.AddTicket, helpdesk.DeleteTicket} {
		values, action := editorSubmission(t, client.requestContext(t.Context(), "GET", helpdesk.TicketEditorPath, "", false))
		if permission == helpdesk.DeleteTicket {
			values.Set("tickets-0-DELETE", "on")
		} else {
			values.Set("tickets-"+values.Get("tickets-INITIAL_FORMS")+"-subject", "denied editor addition")
		}
		func() {
			restore := f.revoke(t, permission)
			defer restore()
			before := f.state(t)
			selectedHelpdeskStatus(t, client.requestContext(t.Context(), "POST", action, values.Encode(), false), http.StatusForbidden)
			f.unchanged(t, before)
		}()
	}
	denied := f.client(t, probe, helpdeskDeniedPermissions{helpdesk.ViewTicket})
	before := f.state(t)
	selectedHelpdeskStatus(t, denied.requestContext(t.Context(), "GET", "/api/tickets/", "", true), http.StatusForbidden)
	f.unchanged(t, before)
	if probe.rootIO != 0 || probe.plainTransactions != 0 {
		t.Fatal("Admin fell back to the root backend")
	}
}
