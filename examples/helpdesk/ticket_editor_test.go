package helpdesk_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
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
	"github.com/progresshans/godj/uuid"
	"github.com/progresshans/godj/web/sessionauth"
	"golang.org/x/net/html"
)

func TestTicketEditorRejectsUnusableConfiguration(t *testing.T) {
	application, err := helpdesk.New(helpdeskConstructionBackend{t: t}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.TicketEditor(helpdesk.TicketEditorConfig{Auth: &sessionauth.Runtime{}, AppendAudit: func(context.Context, db.Session, admin.PreparedEvent) error { return nil }}); err == nil {
		t.Fatal("editor accepted a runtime without cookie coverage and login destination")
	}
}

// Submit the successful HTML controls, so the test consumes actual management,
// identity, checkbox and multiple-select output instead of inventing a form.
func editorSubmission(t *testing.T, response *httptest.ResponseRecorder) (url.Values, string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("editor GET/redisplay: %d %s", response.Code, response.Body.String())
	}
	document, err := html.Parse(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{}
	action := ""
	attribute := func(node *html.Node, name string) (string, bool) {
		for _, attr := range node.Attr {
			if attr.Key == name {
				return attr.Val, true
			}
		}
		return "", false
	}
	var controls func(*html.Node)
	controls = func(node *html.Node) {
		if node.Type == html.ElementNode {
			name, hasName := attribute(node, "name")
			_, disabled := attribute(node, "disabled")
			if hasName && !disabled && node.Data == "input" {
				typ, _ := attribute(node, "type")
				_, checked := attribute(node, "checked")
				if typ != "checkbox" || checked {
					value, _ := attribute(node, "value")
					values.Add(name, value)
				}
			}
			if hasName && !disabled && node.Data == "select" {
				for option := node.FirstChild; option != nil; option = option.NextSibling {
					if option.Type == html.ElementNode && option.Data == "option" {
						if _, selected := attribute(option, "selected"); selected {
							value, _ := attribute(option, "value")
							values.Add(name, value)
						}
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			controls(child)
		}
	}
	var find func(*html.Node)
	find = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "form" {
			value, _ := attribute(node, "action")
			if strings.HasPrefix(value, helpdesk.TicketEditorPath) {
				action = value
				controls(node)
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(document)
	if action == "" || values.Get("csrfmiddlewaretoken") == "" || values.Get("tickets-TOTAL_FORMS") == "" {
		t.Fatal("editor omitted successful form controls")
	}
	return values, action
}

func verifyHelpdeskTicketEditor(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, policy systemstate.CredentialPolicy) {
	t.Helper()
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Editor outside"))
	if err != nil {
		t.Fatal(err)
	}
	secretLabel, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("private outside label", outside.ID))
	if err != nil {
		t.Fatal(err)
	}
	unique, err := uuid.Parse("4e9c8627-2643-4dd5-99b5-b932742b8989")
	if err != nil {
		t.Fatal(err)
	}
	outsideTicket, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("private outside ticket", outside.ID).WithExternalReference(unique))
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{"mixed", "unchanged", "invalid_row", "duplicate_identity", "deleted_outside_identity", "empty_extra_identity", "forbidden_label", "protected_delete", "duplicate_tuple", "late_unique", "audit_failure", "collection_failure", "read_failure", "rollback_unknown", "commit_unknown", "cancellation", "late_scope_change", "forged_initial", "duplicate_management", "forged_parent", "deleted_forged_parent", "empty_extra_forged_parent", "repeated_parent"}
	for _, mode := range cases {
		t.Run(mode, func(t *testing.T) {
			category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Editor "+mode))
			if err != nil {
				t.Fatal(err)
			}
			first, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Old <script>", category.ID).WithResolution("preserved excluded text"))
			if err != nil {
				t.Fatal(err)
			}
			second, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Second", category.ID))
			if err != nil {
				t.Fatal(err)
			}
			label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Owned & label", category.ID))
			if err != nil {
				t.Fatal(err)
			}
			otherLabel, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Second label", category.ID))
			if err != nil {
				t.Fatal(err)
			}
			_, err = models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(first.ID, label.ID))
			if err != nil {
				t.Fatal(err)
			}
			_, err = models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(second.ID, label.ID))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "protected_delete" {
				if _, err = models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(second.ID, "Keep ticket")); err != nil {
					t.Fatal(err)
				}
			}
			backend := &collectionFaultBackend{Backend: runtime}
			app, err := helpdesk.New(backend, category.ID)
			if err != nil {
				t.Fatal(err)
			}
			audits := 0
			client := helpdeskHTTP(t, app, runtime, auth.PrincipalAuthorizer{}, func(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
				audits++
				if err := runtime.AppendAudit(ctx, session, event); err != nil {
					return err
				}
				if mode == "audit_failure" && audits == 2 {
					return errors.New("second audit append failed")
				}
				return nil
			})
			client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
			page := client.request("GET", helpdesk.TicketEditorPath, "", false)
			values, path := editorSubmission(t, page)
			if values.Get("tickets-INITIAL_FORMS") != "2" || values.Get("tickets-TOTAL_FORMS") != "4" || values.Get("tickets-0-id") != strconv.FormatInt(first.ID, 10) || values.Get("tickets-0-category") != strconv.FormatInt(category.ID, 10) {
				t.Fatal("wrong server cohort/blank rows")
			}
			if strings.Contains(page.Body.String(), "private outside") || strings.Contains(page.Body.String(), "Old <script>") || !strings.Contains(page.Body.String(), "Old &lt;script&gt;") {
				t.Fatal("editor leaked scope or failed escaping")
			}
			beforeFirstLinks, beforeSecondLinks := collectionLinks(t, ctx, runtime, first.ID), collectionLinks(t, ctx, runtime, second.ID)
			if mode != "unchanged" {
				values.Set("tickets-0-subject", "Updated <img src=x>")
			}
			wantStatus, wantCode := http.StatusOK, ""
			requestCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			switch mode {
			case "mixed":
				values.Set("tickets-0-closed", "on")
				values.Set("tickets-0-labels", strconv.FormatInt(otherLabel.ID, 10))
				values.Set("tickets-1-DELETE", "on")
				values.Set("tickets-1-subject", "")
				values.Set("tickets-2-subject", "Created together")
				values.Set("tickets-2-labels", strconv.FormatInt(label.ID, 10))
				wantStatus = http.StatusSeeOther
			case "unchanged":
				wantStatus = http.StatusSeeOther
			case "invalid_row":
				values.Set("tickets-2-subject", strings.Repeat("x", 121))
				wantCode = "max_length"
			case "duplicate_identity":
				values.Set("tickets-1-id", strconv.FormatInt(first.ID, 10))
				values.Set("tickets-1-DELETE", "on")
				wantCode = "invalid_identity"
			case "deleted_outside_identity":
				values.Set("tickets-1-id", strconv.FormatInt(outsideTicket.ID, 10))
				values.Set("tickets-1-DELETE", "on")
				wantCode = "invalid_identity"
			case "empty_extra_identity":
				values.Set("tickets-2-id", strconv.FormatInt(first.ID, 10))
				wantCode = "invalid_identity"
			case "forbidden_label":
				values.Set("tickets-0-labels", strconv.FormatInt(secretLabel.ID, 10))
				wantCode = "invalid_choice"
			case "protected_delete":
				values.Set("tickets-1-DELETE", "on")
				wantCode = "protected"
			case "late_unique":
				values.Set("tickets-2-subject", "Duplicate reference")
				values.Set("tickets-2-external_reference", unique.String())
				wantCode = "unique"
			case "duplicate_tuple":
				values.Set("tickets-0-external_reference", "12345678-1234-4234-8234-123456789001")
				values.Set("tickets-1-external_reference", "12345678123442348234123456789001")
				wantCode = "unique"
			case "forged_parent", "deleted_forged_parent", "empty_extra_forged_parent", "repeated_parent":
				index := 0
				if mode == "empty_extra_forged_parent" {
					index = 2
				}
				name := fmt.Sprintf("tickets-%d-category", index)
				if mode == "repeated_parent" {
					values.Add(name, values.Get(name))
				} else {
					values.Set(name, strconv.FormatInt(outside.ID, 10))
				}
				if mode == "deleted_forged_parent" {
					values.Set("tickets-0-DELETE", "on")
				}
				wantCode = "invalid_parent"
			case "audit_failure":
				values.Set("tickets-1-subject", "Second changed")
				wantStatus = http.StatusInternalServerError
			case "collection_failure", "rollback_unknown":
				values.Set("tickets-0-labels", strconv.FormatInt(otherLabel.ID, 10))
				backend.mode = map[string]string{"collection_failure": "link_error", "rollback_unknown": "rollback_unknown"}[mode]
				wantStatus = http.StatusInternalServerError
			case "read_failure":
				backend.mode = "candidate_error"
				wantStatus = http.StatusInternalServerError
			case "commit_unknown":
				backend.mode = "commit_unknown"
				wantStatus = http.StatusInternalServerError
			case "cancellation":
				backend.afterScalar = cancel
				wantStatus = http.StatusInternalServerError
			case "late_scope_change":
				backend.before = func() error {
					moved := second
					moved.CategoryID = outside.ID
					return models.TicketObjects.Save(ctx, runtime, &moved)
				}
				wantCode = "missing_management_form"
			case "forged_initial":
				values.Set("tickets-INITIAL_FORMS", "0")
				wantCode = "missing_management_form"
			case "duplicate_management":
				values.Add("tickets-TOTAL_FORMS", "4")
				wantCode = "multiple"
			}
			backend.atomics, backend.writes = 0, 0
			response := client.requestContext(requestCtx, "POST", path, values.Encode(), false)
			if response.Code != wantStatus {
				t.Fatalf("%s status %d, want %d: %s", mode, response.Code, wantStatus, response.Body.String())
			}
			if backend.atomics != 1 {
				t.Fatal("batch transaction was missing or retried", backend.atomics)
			}
			if wantCode != "" && !strings.Contains(response.Body.String(), `data-error-code="`+wantCode+`"`) {
				t.Fatal("stable whole/row diagnostic missing", wantCode, response.Body.String())
			}
			if response.Code == http.StatusOK && strings.Contains(response.Body.String(), "Updated <img src=x>") {
				t.Fatal("invalid redisplay is unescaped")
			}
			stored, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(first.ID)).All(ctx)
			if err != nil || len(stored) != 1 {
				t.Fatal("read first after submit", err)
			}
			firstHistory, err := runtime.AuditHistory(ctx, "helpdesk.ticket", first.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			secondHistory, err := runtime.AuditHistory(ctx, "helpdesk.ticket", second.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			wantCount := int64(2)
			if mode == "late_scope_change" {
				wantCount = 1
			}
			count, err := models.TicketObjects.Using(runtime).Filter(relations.ModelsTicket.Category.ID.Exact(category.ID)).Count(ctx)
			if err != nil || count != wantCount {
				t.Fatal("batch retained unexpected inserted/deleted rows", count, wantCount, err)
			}
			if mode == "mixed" {
				if stored[0].Subject != "Updated <img src=x>" || !stored[0].Closed || stored[0].Resolution == nil || *stored[0].Resolution != "preserved excluded text" {
					t.Fatal("mixed edit lost scalar/excluded state")
				}
				if links := collectionLinks(t, ctx, runtime, first.ID); len(links) != 1 || links[otherLabel.ID] == 0 {
					t.Fatal("selected collection not saved")
				}
				found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(second.ID)).Exists(ctx)
				if err != nil || found || len(collectionLinks(t, ctx, runtime, second.ID)) != 0 {
					t.Fatal("delete/cascade did not commit", err)
				}
				created, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.Subject.Exact("Created together")).All(ctx)
				if err != nil || len(created) != 1 || created[0].CategoryID != category.ID {
					t.Fatal("new row lost server category", err)
				}
				newHistory, err := runtime.AuditHistory(ctx, "helpdesk.ticket", created[0].ID, 100)
				if err != nil || len(newHistory) != 1 || len(firstHistory) != 1 || len(secondHistory) != 1 || audits != 3 {
					t.Fatal("batch audit did not commit with all operations", err)
				}
				if response.Header().Get("Location") != helpdesk.TicketEditorPath+"?p=1" {
					t.Fatal("unexpected post/redirect/get target")
				}
			} else if mode == "commit_unknown" {
				if stored[0].Subject != "Updated <img src=x>" || len(firstHistory) != 1 {
					t.Fatal("commit-unknown fixture did not commit before reporting uncertainty")
				}
			} else {
				if !reflect.DeepEqual(stored[0], first) || !reflect.DeepEqual(collectionLinks(t, ctx, runtime, first.ID), beforeFirstLinks) || !reflect.DeepEqual(collectionLinks(t, ctx, runtime, second.ID), beforeSecondLinks) || len(firstHistory) != 0 || len(secondHistory) != 0 {
					t.Fatal("rejected/no-op batch changed scalar, collections or audit")
				}
				secondAfter := readUniqueTicket(t, ctx, runtime, second.ID)
				if mode == "late_scope_change" {
					second.CategoryID = outside.ID
				}
				if !reflect.DeepEqual(secondAfter, second) {
					t.Fatal("second row changed after failure")
				}
			}
			if mode == "late_unique" && backend.writes == 0 || mode == "audit_failure" && audits != 2 || mode == "collection_failure" && backend.writes == 0 {
				t.Fatal("failure fixture did not reach preceding real writes")
			}
			if mode == "duplicate_tuple" && (backend.writes != 0 || audits != 0) {
				t.Fatal("cross-row duplicate reached writes or audit")
			}
			if strings.Contains(mode, "parent") && (backend.writes != 0 || audits != 0) {
				t.Fatal("parent mismatch reached writes or audit")
			}
			if strings.Contains(mode, "parent") {
				redisplayed, _ := editorSubmission(t, response)
				if redisplayed.Get("tickets-0-category") != strconv.FormatInt(category.ID, 10) || redisplayed.Get("tickets-2-category") != strconv.FormatInt(category.ID, 10) {
					t.Fatal("hidden parent replayed attacker input")
				}
			}
			if mode == "mixed" {
				connection, err := open(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				reopened, err := systemstate.OpenIdentity(ctx, connection, systemstate.IdentityRuntimeConfig{PasswordHasher: policy.PasswordHasher})
				if err != nil {
					t.Fatal(err)
				}
				if readUniqueTicket(t, ctx, reopened, first.ID).Subject != stored[0].Subject {
					t.Fatal("batch scalar did not survive independent runtime")
				}
				history, err := reopened.AuditHistory(ctx, "helpdesk.ticket", first.ID, 100)
				if err != nil || len(history) != 1 {
					t.Fatal("batch audit did not survive reopen", err)
				}
			}
		})
	}
	verifyTicketEditorAdmission(t, ctx, runtime, authenticated)
}

func verifyTicketEditorAdmission(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	t.Helper()
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Editor admission"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Existing", category.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend := &collectionFaultBackend{Backend: runtime}
	app, err := helpdesk.New(backend, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	client := helpdeskHTTP(t, app, runtime, auth.PrincipalAuthorizer{})
	client.cookies = maps.Clone(authenticated.cookies)
	values, path := editorSubmission(t, client.request("GET", helpdesk.TicketEditorPath, "", false))
	for _, permission := range []auth.Permission{helpdesk.ViewTicket, helpdesk.ChangeTicket, helpdesk.ViewLabel} {
		t.Run("denied_"+string(permission), func(t *testing.T) {
			denied := helpdeskHTTP(t, app, runtime, helpdeskDeniedPermissions{permission})
			denied.cookies, denied.csrf = maps.Clone(client.cookies), client.csrf
			backend.atomics, backend.reads = 0, 0
			if response := denied.request("POST", path, values.Encode(), false); response.Code != http.StatusForbidden || backend.atomics != 0 || backend.reads != 0 {
				t.Fatal("denied batch reached application data", response.Code, backend.atomics, backend.reads)
			}
		})
	}
	for _, permission := range []auth.Permission{helpdesk.AddTicket, helpdesk.DeleteTicket} {
		t.Run("operation_"+string(permission), func(t *testing.T) {
			denied := helpdeskHTTP(t, app, runtime, helpdeskDeniedPermissions{permission})
			denied.cookies = maps.Clone(client.cookies)
			input, action := editorSubmission(t, denied.request("GET", helpdesk.TicketEditorPath, "", false))
			if permission == helpdesk.AddTicket {
				input.Set("tickets-TOTAL_FORMS", "2")
				input.Set("tickets-1-subject", "Forged new")
			} else {
				input.Set("tickets-0-DELETE", "on")
			}
			backend.writes = 0
			if response := denied.request("POST", action, input.Encode(), false); response.Code != http.StatusForbidden || backend.writes != 0 {
				t.Fatal("operation permission lost", response.Code, backend.writes)
			}
		})
	}
	t.Run("csrf_and_bounded_parser", func(t *testing.T) {
		for _, mode := range []string{"missing_csrf", "duplicate_csrf", "unknown_scope", "row_index", "body_limit", "duplicate_query"} {
			t.Run(mode, func(t *testing.T) {
				input := url.Values{}
				for name, entries := range values {
					input[name] = append([]string{}, entries...)
				}
				action := path
				want := http.StatusBadRequest
				switch mode {
				case "missing_csrf":
					input.Del("csrfmiddlewaretoken")
					want = http.StatusForbidden
				case "duplicate_csrf":
					input.Add("csrfmiddlewaretoken", input.Get("csrfmiddlewaretoken"))
					want = http.StatusForbidden
				case "unknown_scope":
					input.Set("tickets-0-details", "excluded scalar")
				case "row_index":
					input.Set("tickets-01-subject", "alias")
				case "body_limit":
					input.Set("tickets-0-subject", strings.Repeat("x", 70<<10))
				case "duplicate_query":
					action += "&p=2"
				}
				backend.atomics, backend.reads = 0, 0
				if response := client.request("POST", action, input.Encode(), false); response.Code != want || backend.atomics != 0 || backend.reads != 0 {
					t.Fatal("invalid batch reached application data", response.Code, want, backend.atomics, backend.reads)
				}
			})
		}
	})
	t.Run("pagination", func(t *testing.T) {
		for index := 0; index < 20; index++ {
			if _, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate(fmt.Sprintf("Page %d", index), category.ID)); err != nil {
				t.Fatal(err)
			}
		}
		firstPage, _ := editorSubmission(t, client.request("GET", helpdesk.TicketEditorPath, "", false))
		secondPage, _ := editorSubmission(t, client.request("GET", helpdesk.TicketEditorPath+"?p=2", "", false))
		if firstPage.Get("tickets-INITIAL_FORMS") != "20" || secondPage.Get("tickets-INITIAL_FORMS") != "1" || firstPage.Get("tickets-0-id") == secondPage.Get("tickets-0-id") {
			t.Fatal("pagination lost server cohort")
		}
	})
	t.Run("render_budget", func(t *testing.T) {
		// Bound the real worst-case escaping expansion in a single seed
		// transaction instead of doing a separate durable commit per label.
		if err := runtime.Atomic(ctx, func(session db.Session) error {
			// This case must also work when selected without the pagination case.
			for index := 0; index < 19; index++ {
				if _, err := models.TicketObjects.Create(ctx, session, models.NewTicketCreate("Render budget row", category.ID)); err != nil {
					return err
				}
			}
			for index := 0; index < 256; index++ {
				name := strings.Repeat("\"", 61) + fmt.Sprintf("%03d", index)
				if _, err := models.LabelObjects.Create(ctx, session, models.NewLabelCreate(name, category.ID)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		page := client.request("GET", helpdesk.TicketEditorPath, "", false)
		if page.Code != http.StatusOK || page.Body.Len() <= 1<<20 || page.Body.Len() > helpdesk.TicketEditorMaxResponseBytes {
			t.Fatal("valid scoped choices exceeded composed response budget", page.Code, page.Body.Len())
		}
		input, action := editorSubmission(t, page)
		input.Set("tickets-TOTAL_FORMS", "40")
		input.Set("tickets-39-subject", strings.Repeat("x", 121))
		rejected := client.request("POST", action, input.Encode(), false)
		if rejected.Code != http.StatusOK || rejected.Body.Len() > helpdesk.TicketEditorMaxResponseBytes || !strings.Contains(rejected.Body.String(), `data-error-code="max_length"`) {
			t.Fatal("bounded rejected rows could not be redisplayed", rejected.Code, rejected.Body.Len())
		}
	})
}
