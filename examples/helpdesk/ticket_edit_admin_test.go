package helpdesk_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
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
)

// Exercise the registered public CollectionFormSet route, including its actual
// rendered controls, typed current binding, application writer and native DB.
// The same function is required by both SQLite and PostgreSQL product owners.
func verifyHelpdeskAdminCollectionEdit(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	t.Helper()
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Admin collection outside"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Private outside ticket", outside.ID))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Private outside label", outside.ID))
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{"mixed", "unchanged", "order_only", "collection_only", "deleted_extra", "readonly_add", "delete_only", "change_no_add", "invalid_row", "duplicate_identity", "deleted_outside_identity", "extra_identity", "forged_initial", "duplicate_management", "missing_management", "foreign_label", "duplicate_tuple", "forged_category", "denied_add", "denied_delete", "denied_order", "protected_delete", "late_cohort_gain", "late_cohort_loss", "stale_label", "audit_failure", "collection_failure", "response_failure", "rollback_unknown", "commit_unknown", "cancellation"}
	for _, mode := range cases {
		t.Run(mode, func(t *testing.T) {
			category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Admin collection "+mode))
			if err != nil {
				t.Fatal(err)
			}
			first, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("First <script>", category.ID).WithResolution("Preserve this value"))
			if err != nil {
				t.Fatal(err)
			}
			second, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Second", category.ID))
			if err != nil {
				t.Fatal(err)
			}
			label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Owned <&>", category.ID))
			if err != nil {
				t.Fatal(err)
			}
			other, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate("Other", category.ID))
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []int64{first.ID, second.ID} {
				if _, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(key, label.ID)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "protected_delete" {
				if _, err := models.ServiceReportObjects.Create(ctx, runtime, models.NewServiceReportCreate(second.ID, "Protect this ticket")); err != nil {
					t.Fatal(err)
				}
			}
			backend := &collectionFaultBackend{Backend: runtime}
			application, err := helpdesk.New(backend, category.ID)
			if err != nil {
				t.Fatal(err)
			}
			audits := 0
			appendAudit := func(work context.Context, session db.Session, event admin.PreparedEvent) error {
				audits++
				if err := runtime.AppendAudit(work, session, event); err != nil {
					return err
				}
				if mode == "audit_failure" && audits == 2 {
					return errors.New("private second collection audit failure")
				}
				return nil
			}
			registry, err := application.AdminRegistry(helpdesk.AdminConfig{AppendAudit: appendAudit})
			if err != nil {
				t.Fatal(err)
			}
			var denied helpdeskDeniedPermissions
			switch mode {
			case "readonly_add", "denied_order":
				denied = []auth.Permission{helpdesk.ChangeTicket}
			case "delete_only":
				denied = []auth.Permission{helpdesk.AddTicket, helpdesk.ChangeTicket, helpdesk.ViewLabel}
			case "change_no_add", "denied_add":
				denied = []auth.Permission{helpdesk.AddTicket}
			case "denied_delete":
				denied = []auth.Permission{helpdesk.DeleteTicket}
			}
			client := helpdeskHTTPRegistry(t, application, registry, runtime, denied)
			client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
			path := "/admin/tickets/collection-set/edit/"
			page := client.request("GET", path, "", false)
			values, action := adminFormSubmission(t, page, path)
			extra := !strings.Contains(mode, "no_add") && mode != "denied_add" && mode != "delete_only"
			wantTotal := "2"
			if extra {
				wantTotal = "3"
			}
			if values.Get("tickets-INITIAL_FORMS") != "2" || values.Get("tickets-TOTAL_FORMS") != wantTotal || values.Get("tickets-0-id") != strconv.FormatInt(first.ID, 10) || values.Get("tickets-1-id") != strconv.FormatInt(second.ID, 10) || values.Has("tickets-0-category") {
				t.Fatal("collection did not render its own complete cohort and hidden identities", values)
			}
			if strings.Contains(page.Body.String(), "First <script>") || !strings.Contains(page.Body.String(), "First &lt;script&gt;") || strings.Contains(page.Body.String(), "Private outside") {
				t.Fatal("collection escaped/scope display contract failed")
			}
			if !extra && strings.Contains(page.Body.String(), "data-inline-empty") {
				t.Fatal("denied Add exposed a prototype")
			}
			if mode == "delete_only" && strings.Contains(page.Body.String(), "Owned &lt;&amp;&gt;") {
				t.Fatal("denied target permission exposed readonly labels")
			}
			beforeFirstLinks, beforeSecondLinks := collectionLinks(t, ctx, runtime, first.ID), collectionLinks(t, ctx, runtime, second.ID)
			wantFirst, wantSecond := first, second
			wantLinks := beforeFirstLinks
			wantCount, wantAudits, wantFirstAudit, wantSecondAudit := int64(2), 0, 0, 0
			wantStatus, wantCode, wantAtomics := http.StatusOK, "", 0
			created, removed := false, false
			requestCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if mode != "unchanged" && mode != "order_only" && mode != "collection_only" && mode != "deleted_extra" && mode != "readonly_add" && mode != "delete_only" && mode != "denied_order" {
				values.Set("tickets-0-subject", "Updated <img src=x>")
			}
			switch mode {
			case "mixed":
				values.Set("tickets-0-closed", "on")
				values.Set("tickets-0-labels", strconv.FormatInt(other.ID, 10))
				values.Set("tickets-1-DELETE", "on")
				values.Set("tickets-1-subject", "")
				values.Set("tickets-2-subject", "Created together")
				values.Set("tickets-2-labels", strconv.FormatInt(label.ID, 10))
				wantFirst.Subject, wantFirst.Closed = "Updated <img src=x>", true
				wantLinks, created, removed = map[int64]int64{other.ID: 0}, true, true
				wantStatus, wantAtomics, wantAudits, wantFirstAudit, wantSecondAudit = 302, 1, 3, 1, 1
			case "unchanged", "order_only", "deleted_extra":
				if mode == "order_only" {
					values.Set("tickets-0-ORDER", "2")
					values.Set("tickets-1-ORDER", "1")
				} else if mode == "deleted_extra" {
					values.Set("tickets-2-DELETE", "on")
					values.Set("tickets-2-subject", strings.Repeat("x", 121))
				}
				wantStatus, wantAtomics = 302, 1
			case "collection_only":
				values.Set("tickets-0-labels", strconv.FormatInt(other.ID, 10))
				wantLinks = map[int64]int64{other.ID: 0}
				wantStatus, wantAtomics, wantAudits, wantFirstAudit = 302, 1, 1, 1
			case "readonly_add":
				if values.Has("tickets-0-subject") || values.Has("tickets-0-labels") || values.Has("tickets-0-ORDER") {
					t.Fatal("readonly initial row submitted editable controls")
				}
				values.Set("tickets-0-subject", "forged ignored")
				values.Set("tickets-0-labels", strconv.FormatInt(secret.ID, 10))
				values.Set("tickets-2-subject", "Created together")
				values.Set("tickets-2-labels", strconv.FormatInt(label.ID, 10))
				created, wantCount = true, 3
				wantStatus, wantAtomics, wantAudits = 302, 1, 1
			case "delete_only":
				values.Set("tickets-1-DELETE", "on")
				removed, wantCount = true, 1
				wantStatus, wantAtomics, wantAudits, wantSecondAudit = 302, 1, 1, 1
			case "change_no_add":
				wantFirst.Subject = "Updated <img src=x>"
				wantStatus, wantAtomics, wantAudits, wantFirstAudit = 302, 1, 1, 1
			case "invalid_row":
				values.Set("tickets-2-subject", strings.Repeat("x", 121))
				wantCode = "max_length"
			case "duplicate_identity", "deleted_outside_identity", "extra_identity":
				key, index := first.ID, 1
				if mode == "deleted_outside_identity" {
					key = foreign.ID
				}
				if mode == "extra_identity" {
					index = 2
				}
				values.Set(fmt.Sprintf("tickets-%d-id", index), strconv.FormatInt(key, 10))
				values.Set(fmt.Sprintf("tickets-%d-DELETE", index), "on")
				wantCode = "invalid_identity"
			case "forged_initial", "missing_management":
				if mode == "forged_initial" {
					values.Set("tickets-INITIAL_FORMS", "0")
				} else {
					values.Del("tickets-TOTAL_FORMS")
				}
				wantCode = "missing_management_form"
			case "duplicate_management":
				values.Add("tickets-TOTAL_FORMS", "3")
				wantCode = "multiple"
			case "foreign_label":
				values.Set("tickets-0-labels", strconv.FormatInt(secret.ID, 10))
				wantCode = "invalid_choice"
			case "duplicate_tuple":
				values.Set("tickets-0-external_reference", "ec3f1530-c1ce-4e26-a0b0-4e4a24e37b20")
				values.Set("tickets-1-external_reference", "ec3f1530c1ce4e26a0b04e4a24e37b20")
				wantCode = "unique"
			case "forged_category":
				values.Set("tickets-0-category", strconv.FormatInt(outside.ID, 10))
				wantStatus = 400
			case "denied_add":
				values.Set("tickets-TOTAL_FORMS", "3")
				values.Set("tickets-2-subject", "forged new row")
				wantStatus = 403
			case "denied_delete":
				values.Set("tickets-1-DELETE", "on")
				wantStatus = 403
			case "denied_order":
				values.Set("tickets-0-ORDER", "2")
				wantStatus = 403
			case "protected_delete":
				values.Set("tickets-1-DELETE", "on")
				wantCode, wantAtomics = "protected", 1
			case "late_cohort_gain":
				backend.before = func() error {
					_, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Concurrent new member", category.ID))
					return err
				}
				wantCode, wantAtomics, wantCount = "missing_management_form", 1, 3
			case "late_cohort_loss":
				wantSecond.CategoryID, wantCount = outside.ID, 1
				backend.before = func() error { return models.TicketObjects.Save(ctx, runtime, &wantSecond) }
				wantCode, wantAtomics = "missing_management_form", 1
			case "stale_label":
				backend.before = func() error {
					moved := label
					moved.CategoryID = outside.ID
					return models.LabelObjects.Save(ctx, runtime, &moved)
				}
				wantCode, wantAtomics = "invalid_choice", 1
			case "audit_failure":
				values.Set("tickets-1-subject", "Second changed")
				wantStatus, wantAtomics, wantAudits = 500, 1, 2
			case "collection_failure", "rollback_unknown":
				values.Set("tickets-0-labels", strconv.FormatInt(other.ID, 10))
				backend.mode = map[string]string{"collection_failure": "link_error", "rollback_unknown": "rollback_unknown"}[mode]
				wantStatus, wantAtomics = 500, 1
			case "response_failure":
				backend.mode = "response_error"
				wantStatus, wantAtomics = 500, 1
			case "commit_unknown":
				backend.mode = "commit_unknown"
				wantFirst.Subject = "Updated <img src=x>"
				wantStatus, wantAtomics, wantAudits, wantFirstAudit = 500, 1, 1, 1
			case "cancellation":
				backend.afterScalar = cancel
				wantStatus, wantAtomics = 500, 1
			}
			backend.atomics, backend.writes = 0, 0
			response := client.requestContext(requestCtx, "POST", action, values.Encode(), false)
			if response.Code != wantStatus || backend.atomics != wantAtomics {
				t.Fatalf("response %d, atomics %d; want %d, %d: %s", response.Code, backend.atomics, wantStatus, wantAtomics, response.Body.String())
			}
			if wantCode != "" && !strings.Contains(response.Body.String(), `data-error-code="`+wantCode+`"`) {
				t.Fatal("missing owned row/set diagnostic", wantCode, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "Updated <img src=x>") || strings.Contains(response.Body.String(), "private second collection") || strings.Contains(response.Body.String(), "Private outside") {
				t.Fatal("unsafe or private redisplay")
			}
			if mode == "stale_label" && strings.Contains(response.Body.String(), "Owned &lt;&amp;&gt;") {
				t.Fatal("writer-rejected selection retained its earlier label authority")
			}
			if response.Code == 302 && !strings.HasPrefix(response.Header().Get("Location"), "/admin/tickets/?") || response.Code != 302 && response.Header().Get("Location") != "" {
				t.Fatal("unconfirmed result redirected or confirmed result lost redirect")
			}
			if got := readUniqueTicket(t, ctx, runtime, first.ID); !reflect.DeepEqual(got, wantFirst) {
				t.Fatal("first scalar readback differs", mode)
			}
			firstLinks := collectionLinks(t, ctx, runtime, first.ID)
			if mode == "mixed" || mode == "collection_only" {
				if len(firstLinks) != 1 || firstLinks[other.ID] == 0 {
					t.Fatal("collection update did not commit")
				}
			} else if !reflect.DeepEqual(firstLinks, wantLinks) {
				t.Fatal("unchanged/rolled-back collection was modified")
			}
			if removed {
				present, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(second.ID)).Exists(ctx)
				if err != nil || present || len(collectionLinks(t, ctx, runtime, second.ID)) != 0 {
					t.Fatal("delete/cascade did not commit", err)
				}
			} else if !reflect.DeepEqual(readUniqueTicket(t, ctx, runtime, second.ID), wantSecond) || !reflect.DeepEqual(collectionLinks(t, ctx, runtime, second.ID), beforeSecondLinks) {
				t.Fatal("second scalar/collection changed after rejection")
			}
			for key, want := range map[int64]int{first.ID: wantFirstAudit, second.ID: wantSecondAudit} {
				history, err := runtime.AuditHistory(ctx, "helpdesk.ticket", key, 100)
				if err != nil || len(history) != want {
					t.Fatal("stored audit diverged from native outcome", err, len(history), want)
				}
			}
			count, err := models.TicketObjects.Using(runtime).Filter(relations.ModelsTicket.Category.ID.Exact(category.ID)).Count(ctx)
			if err != nil || count != wantCount {
				t.Fatal("complete cohort readback", err, count, wantCount)
			}
			if created {
				row, err := models.TicketObjects.Using(runtime).Filter(relations.ModelsTicket.Category.ID.Exact(category.ID), models.TicketFields.Subject.Exact("Created together")).Get(ctx)
				if err != nil || row.ID == first.ID || row.ID == second.ID || len(collectionLinks(t, ctx, runtime, row.ID)) != 1 || collectionLinks(t, ctx, runtime, row.ID)[label.ID] == 0 {
					t.Fatal("new typed row/category/collection lost", err)
				}
				history, err := runtime.AuditHistory(ctx, "helpdesk.ticket", row.ID, 100)
				if err != nil || len(history) != 1 {
					t.Fatal("new row audit missing", err)
				}
			}
			// A protected deletion follows a real first-row write, whose audit
			// must roll back. Model formset uniqueness rejects before the writer.
			if mode == "protected_delete" {
				wantAudits = 1
			}
			if audits != wantAudits {
				t.Fatal("unexpected audit attempts", audits, wantAudits)
			}
			if (mode == "audit_failure" || mode == "collection_failure" || mode == "response_failure" || mode == "cancellation") && backend.writes == 0 {
				t.Fatal("late failure never reached native writes")
			}
			if wantAtomics == 0 && backend.writes != 0 {
				t.Fatal("initial rejection reached writes")
			}
		})
	}
}
