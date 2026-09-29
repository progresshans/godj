package helpdesk_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/systemstate"
	"golang.org/x/net/html"
)

// Consume the actual browser-successful controls, including disabled ancestor
// fieldsets, default single-select options, escaped text and hidden identities.
func adminInlineSubmission(t *testing.T, response *httptest.ResponseRecorder) (url.Values, string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("Admin GET: %d %s", response.Code, response.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	attr := func(n *html.Node, key string) (string, bool) {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val, true
			}
		}
		return "", false
	}
	values := url.Values{}
	action := ""
	var content func(*html.Node) string
	content = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var s string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			s += content(c)
		}
		return s
	}
	var controls func(*html.Node, bool)
	controls = func(n *html.Node, disabled bool) {
		_, off := attr(n, "disabled")
		disabled = disabled || off
		name, exists := attr(n, "name")
		if exists && !disabled {
			value, _ := attr(n, "value")
			switch n.Data {
			case "input":
				typ, _ := attr(n, "type")
				_, checked := attr(n, "checked")
				if typ != "checkbox" && typ != "radio" || checked {
					values.Add(name, value)
				}
			case "textarea":
				values.Add(name, content(n))
			case "select":
				_, multiple := attr(n, "multiple")
				selected := false
				first := ""
				seen := false
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && c.Data == "option" {
						v, _ := attr(c, "value")
						if !seen {
							first = v
							seen = true
						}
						if _, ok := attr(c, "selected"); ok {
							values.Add(name, v)
							selected = true
						}
					}
				}
				if !multiple && !selected && seen {
					values.Add(name, first)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			controls(c, disabled)
		}
	}
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			a, _ := attr(n, "action")
			if strings.HasPrefix(a, "/admin/tickets/") {
				action = a
				controls(n, false)
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if action == "" || values.Get("csrfmiddlewaretoken") == "" {
		t.Fatal("missing actual Admin form")
	}
	return values, action
}

func verifyHelpdeskAdminInlines(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	t.Helper()
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	modes := []string{"new_parent", "new_invalid", "new_audit_failure", "change", "child_only", "long_summary", "unchanged", "invalid_parent", "invalid_child", "delete", "delete_audit_failure", "readonly", "readonly_delete", "add_only", "invisible", "denied_forged", "denied_add", "denied_delete", "forged_parent", "forged_identity", "missing_management", "duplicate_management", "excess_rows", "unknown_field", "replace_deleted_unique", "late_parent_scope", "late_cohort", "late_child_error", "rollback_unknown", "commit_unknown", "view_parent"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Admin inline "+mode))
			if err != nil {
				t.Fatal(err)
			}
			fresh := strings.HasPrefix(mode, "new_") || mode == "add_only"
			var parent models.Ticket
			var report models.ServiceReport
			if !strings.HasPrefix(mode, "new_") {
				parent, err = models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Before parent", category.ID).WithResolution(""))
				if err != nil {
					t.Fatal(err)
				}
				if !fresh {
					report, err = models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(parent.ID, "Before <report>"))
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			backend := &collectionFaultBackend{Backend: runtime}
			app, err := helpdesk.New(backend, category.ID)
			if err != nil {
				t.Fatal(err)
			}
			audits := 0
			registry, err := app.AdminRegistry(helpdesk.AdminConfig{AppendAudit: func(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
				audits++
				if err := runtime.AppendAudit(ctx, session, event); err != nil {
					return err
				}
				if strings.Contains(mode, "audit_failure") && audits == 2 {
					return errors.New("late parent audit failure")
				}
				if mode == "rollback_unknown" {
					return errors.New("child audit failed with uncertain rollback")
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			var deny helpdeskDeniedPermissions
			switch mode {
			case "readonly", "readonly_delete":
				deny = []auth.Permission{helpdesk.AddServiceReport, helpdesk.ChangeServiceReport}
			case "add_only":
				deny = []auth.Permission{helpdesk.ViewServiceReport, helpdesk.ChangeServiceReport, helpdesk.DeleteServiceReport}
			case "invisible", "denied_forged":
				deny = []auth.Permission{helpdesk.ViewServiceReport, helpdesk.AddServiceReport, helpdesk.ChangeServiceReport, helpdesk.DeleteServiceReport}
			case "denied_add":
				deny = []auth.Permission{helpdesk.AddServiceReport}
			case "denied_delete":
				deny = []auth.Permission{helpdesk.DeleteServiceReport}
			case "view_parent":
				deny = []auth.Permission{helpdesk.ChangeTicket}
			}
			client := helpdeskHTTPRegistry(t, app, registry, runtime, deny)
			client.cookies = maps.Clone(authenticated.cookies)
			path := "/admin/tickets/add/"
			if parent.ID != 0 {
				path = fmt.Sprintf("/admin/tickets/change/?id=%d", parent.ID)
			}
			response := client.request("GET", path, "", false)
			if mode == "view_parent" {
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Before &lt;report&gt;") || !strings.Contains(response.Body.String(), "<fieldset disabled>") {
					t.Fatal("read-only parent detail lost inline", response.Code, response.Body.String())
				}
				return
			}
			values, action := adminInlineSubmission(t, response)
			if mode == "invisible" || mode == "denied_forged" {
				if strings.Contains(response.Body.String(), "Before &lt;report&gt;") {
					t.Fatal("unauthorized child rendered")
				}
			} else if values.Get("reports-TOTAL_FORMS") != "1" {
				t.Fatal("wrong OneToOne controls", values.Get("reports-TOTAL_FORMS"))
			}
			if mode == "readonly" || mode == "readonly_delete" {
				if _, exists := values["reports-0-summary"]; exists {
					t.Fatal("disabled row submitted ordinary fields")
				}
				if values.Get("reports-0-id") != strconv.FormatInt(report.ID, 10) {
					t.Fatal("read-only identity was disabled")
				}
			}
			if mode != "unchanged" && mode != "child_only" {
				values.Set("subject", "After <parent>")
			}
			status, code := http.StatusFound, ""
			switch mode {
			case "new_parent", "new_audit_failure", "add_only", "change", "child_only", "late_child_error", "rollback_unknown", "commit_unknown":
				values.Set("reports-0-summary", "After <report>")
			case "new_invalid", "invalid_child":
				values["reports-0-summary"] = []string{"first", "second"}
				status, code = http.StatusOK, "multiple"
			case "long_summary":
				values.Set("reports-0-summary", strings.Repeat("x", 2048))
			case "invalid_parent":
				values.Set("subject", "")
				values.Set("reports-0-summary", "After <report>")
				status, code = http.StatusOK, "required"
			case "delete", "delete_audit_failure", "readonly_delete":
				values.Set("reports-0-DELETE", "on")
				values.Set("reports-0-summary", "")
			case "readonly":
				values.Set("reports-0-summary", "forged readonly")
			case "denied_forged":
				values.Set("reports-TOTAL_FORMS", "1")
				values.Set("reports-INITIAL_FORMS", "0")
				status = http.StatusForbidden
			case "denied_add":
				values.Set("reports-TOTAL_FORMS", "2")
				values.Set("reports-1-summary", "new forbidden child")
				status = http.StatusForbidden
			case "denied_delete":
				values.Set("reports-0-DELETE", "on")
				status = http.StatusForbidden
			case "forged_parent":
				values.Set("reports-0-ticket", strconv.FormatInt(parent.ID+999, 10))
				values.Set("reports-0-DELETE", "on")
				status, code = http.StatusOK, "invalid_parent"
			case "forged_identity":
				values.Set("reports-0-id", strconv.FormatInt(report.ID+999, 10))
				values.Set("reports-0-DELETE", "on")
				status, code = http.StatusOK, "invalid_identity"
			case "missing_management":
				values.Del("reports-INITIAL_FORMS")
				status, code = http.StatusOK, "missing_management_form"
			case "duplicate_management":
				values.Add("reports-TOTAL_FORMS", "1")
				status, code = http.StatusOK, "multiple"
			case "excess_rows":
				values.Set("reports-TOTAL_FORMS", "999")
				status, code = http.StatusOK, "too_many_forms"
			case "unknown_field":
				values.Set("reports-0-admin", "true")
				status = http.StatusBadRequest
			case "replace_deleted_unique":
				values.Set("reports-0-DELETE", "on")
				values.Set("reports-TOTAL_FORMS", "2")
				values.Set("reports-1-summary", "Replacement")
				status, code = http.StatusOK, "unique"
			case "late_parent_scope":
				other, createErr := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Moved scope"))
				if createErr != nil {
					t.Fatal(createErr)
				}
				backend.before = func() error {
					moved := parent
					moved.CategoryID = other.ID
					return models.TicketObjects.Save(ctx, runtime, &moved)
				}
				status = http.StatusNotFound
			case "late_cohort":
				backend.before = func() error { _, err := models.ServiceReportObjects.Delete(ctx, runtime, &report); return err }
				status, code = http.StatusOK, "stale_inline"
			}
			if strings.Contains(mode, "audit_failure") {
				status = http.StatusInternalServerError
			}
			if mode == "late_child_error" {
				backend.mode = "report_error"
				status = http.StatusInternalServerError
			}
			if mode == "rollback_unknown" {
				backend.mode = "rollback_unknown"
				status = http.StatusInternalServerError
			}
			if mode == "commit_unknown" {
				backend.mode = "commit_unknown"
				status = http.StatusInternalServerError
			}
			backend.atomics, backend.writes = 0, 0
			response = client.request("POST", action, values.Encode(), false)
			if response.Code != status {
				t.Fatalf("%s POST=%d want %d: %s", mode, response.Code, status, response.Body.String())
			}
			if code != "" && !strings.Contains(response.Body.String(), `data-error-code="`+code+`"`) {
				t.Fatal("missing inline/parent diagnostic", code, response.Body.String())
			}
			if response.Code == http.StatusOK && (strings.Contains(response.Body.String(), "After <parent>") || strings.Contains(response.Body.String(), "After <report>")) {
				t.Fatal("redisplay escaped incorrectly")
			}
			if backend.atomics > 1 {
				t.Fatal("composed mutation retried", backend.atomics)
			}
			tickets, err := models.TicketObjects.Using(runtime).Filter(relations.ModelsTicket.Category.ID.Exact(category.ID)).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "late_parent_scope" {
				if len(tickets) != 0 || backend.writes != 0 || audits != 0 {
					t.Fatal("moved parent allowed writes")
				}
				return
			}
			committed := status == http.StatusFound || mode == "commit_unknown"
			if parent.ID == 0 && !committed {
				if len(tickets) != 0 {
					t.Fatal("new parent survived failed child/audit")
				}
				return
			}
			if len(tickets) != 1 {
				t.Fatal("parent count", len(tickets))
			}
			saved := tickets[0]
			expectedSubject := "Before parent"
			if committed && mode != "unchanged" && mode != "child_only" {
				expectedSubject = "After <parent>"
			}
			if saved.Subject != expectedSubject {
				t.Fatal("parent rollback/content", saved.Subject, expectedSubject)
			}
			reports, err := models.ServiceReportObjects.Using(runtime).Filter(relations.ModelsServiceReport.Ticket.ID.Exact(saved.ID)).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			deleted := mode == "late_cohort" || committed && (mode == "delete" || mode == "readonly_delete")
			if deleted {
				if len(reports) != 0 {
					t.Fatal("child deletion failed")
				}
			} else {
				if len(reports) != 1 {
					t.Fatal("child count", len(reports))
				}
				want := "Before <report>"
				if committed && (fresh || mode == "change" || mode == "child_only" || mode == "commit_unknown") {
					want = "After <report>"
				}
				if mode == "long_summary" {
					want = strings.Repeat("x", 2048)
				}
				if reports[0].Summary != want {
					t.Fatal("child content/rollback", reports[0].Summary, want)
				}
			}
			history, err := runtime.AuditHistory(ctx, "helpdesk.ticket", saved.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			expectedAudits := 0
			if committed && mode != "unchanged" {
				expectedAudits = 1
			}
			if mode == "child_only" && (len(history) != 1 || strings.Join(history[0].ChangedFields, ",") != "reports") {
				t.Fatal("child-only mutation lost composite audit field")
			}
			if len(history) != expectedAudits {
				var fields []string
				if len(history) > 0 {
					fields = history[0].ChangedFields
				}
				t.Fatalf("parent audit count=%d want=%d changed=%v", len(history), expectedAudits, fields)
			}
		})
	}
}
