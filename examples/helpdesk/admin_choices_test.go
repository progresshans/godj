package helpdesk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
)

func verifyHelpdeskAdminChoices(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	t.Helper()
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Bounded Admin choices"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Outside choice scope"))
	if err != nil {
		t.Fatal(err)
	}
	var labels []models.Label
	if err := runtime.CoordinatedAtomic(ctx, func(session db.Session) error {
		for index := range 125 {
			label, err := models.LabelObjects.Create(ctx, session, models.NewLabelCreate(fmt.Sprintf("Bounded label %03d <safe>", index), category.ID))
			if err != nil {
				return err
			}
			labels = append(labels, label)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ticket, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Choice parent", category.ID).WithResolution(""))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Private choice parent", outside.ID))
	if err != nil {
		t.Fatal(err)
	}
	selected := labels[len(labels)-1]
	link, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(ticket.ID, selected.ID))
	if err != nil {
		t.Fatal(err)
	}
	counter := &helpdeskReadCounter{Backend: runtime}
	app, err := helpdesk.New(counter, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := app.AdminRegistry(helpdesk.AdminConfig{AppendAudit: runtime.AppendAudit})
	if err != nil {
		t.Fatal(err)
	}
	client := helpdeskHTTPRegistry(t, app, registry, runtime, auth.PrincipalAuthorizer{})
	client.cookies = maps.Clone(authenticated.cookies)
	searchPath := "/admin/tickets/add/choices/labels/"
	changePath := fmt.Sprintf("/admin/tickets/change/?id=%d", ticket.ID)
	type choicePage struct {
		Items  []struct{ Value, Label string } `json:"items"`
		Offset int                             `json:"offset"`
		Limit  int                             `json:"limit"`
		More   bool                            `json:"more"`
	}
	t.Run("bounded_pages_and_search_use_current_category", func(t *testing.T) {
		for _, item := range []struct {
			query         string
			offset, count int
			more          bool
		}{
			{"?limit=100", 0, 100, true}, {"?limit=100&offset=100", 100, 25, false}, {"?q=124&limit=100", 0, 1, false},
		} {
			counter.queries, counter.plans = 0, nil
			response := client.request("GET", searchPath+item.query, "", false)
			var page choicePage
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != item.count || page.Offset != item.offset || page.Limit != 100 || page.More != item.more {
				t.Fatal("bounded choice page mismatch", response.Code, response.Body.String())
			}
			if counter.queries != 1 || counter.last.Table() != "helpdesk_label" {
				t.Fatal("search read unrelated or repeated data", counter.queries)
			}
			if limit, present := counter.last.Limit(); !present || limit != 101 {
				t.Fatal("search omitted bounded lookahead", limit, present)
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("choice response lost cache/content boundary")
			}
			for index, choice := range page.Items {
				want := labels[item.offset+index]
				if item.count == 1 {
					want = selected
				}
				if choice.Value != strconv.FormatInt(want.ID, 10) || choice.Label != want.Name {
					t.Fatal("target order, string key or label changed", choice)
				}
			}
		}
	})
	t.Run("admission_rejects_before_target_reads", func(t *testing.T) {
		for _, query := range []string{"?limit=101", "?offset=-1", "?q=a&q=b", "?source=other", "?id=1", "?limit=01"} {
			counter.queries, counter.plans = 0, nil
			if response := client.request("GET", searchPath+query, "", false); response.Code != http.StatusBadRequest || counter.queries != 0 {
				t.Fatal("malformed choice search performed target I/O", query, response.Code, counter.queries)
			}
		}
		for _, permission := range []auth.Permission{helpdesk.ViewLabel, helpdesk.AddTicket} {
			denied := helpdeskHTTPRegistry(t, app, registry, runtime, helpdeskDeniedPermissions{permission})
			denied.cookies = maps.Clone(authenticated.cookies)
			counter.queries = 0
			if response := denied.request("GET", searchPath, "", false); response.Code != http.StatusForbidden || counter.queries != 0 {
				t.Fatal("denied choice search read targets", permission, response.Code, counter.queries)
			}
		}
		viewOnly := helpdeskHTTPRegistry(t, app, registry, runtime, helpdeskDeniedPermissions{helpdesk.ChangeTicket})
		viewOnly.cookies = maps.Clone(authenticated.cookies)
		counter.queries = 0
		if response := viewOnly.request("GET", fmt.Sprintf("/admin/tickets/change/choices/labels/?id=%d", ticket.ID), "", false); response.Code != http.StatusForbidden || counter.queries != 0 {
			t.Fatal("view-only source searched writable choices", response.Code, counter.queries)
		}
		counter.plans = nil
		response := client.request("GET", fmt.Sprintf("/admin/tickets/change/choices/labels/?id=%d", foreign.ID), "", false)
		if response.Code != http.StatusNotFound {
			t.Fatal("foreign source entered target search", response.Code)
		}
		for _, plan := range counter.plans {
			if plan.Table() == "helpdesk_label" {
				t.Fatal("foreign source read target labels")
			}
		}
	})
	t.Run("outside_page_selection_survives_then_stale_scope_is_rejected", func(t *testing.T) {
		response := client.request("GET", changePath, "", false)
		values, action := adminFormSubmission(t, response, changePath)
		if values.Get("labels") != strconv.FormatInt(selected.ID, 10) || !strings.Contains(response.Body.String(), "Bounded label 124 &lt;safe&gt;") || strings.Contains(response.Body.String(), "Bounded label 100 &lt;safe&gt;") {
			t.Fatal("initial rendering lost selected key or rendered entire catalog")
		}
		values.Set("subject", "Valid off-page selection")
		if result := client.request("POST", action, values.Encode(), false); result.Code != http.StatusFound {
			t.Fatal("off-page valid key was rejected", result.Code, result.Body.String())
		}
		response = client.request("GET", changePath, "", false)
		values, action = adminFormSubmission(t, response, changePath)
		values.Set("subject", "Must not persist")
		if _, err := models.LabelObjects.Patch(ctx, runtime, selected, models.LabelPatch{}.WithCategoryID(outside.ID)); err != nil {
			t.Fatal(err)
		}
		result := client.request("POST", action, values.Encode(), false)
		if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `data-error-field="labels" data-error-code="invalid_choice"`) || strings.Contains(result.Body.String(), "Bounded label 124 &lt;safe&gt;") {
			t.Fatal("stale scope leaked a label or bypassed validation", result.Code, result.Body.String())
		}
		stored, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(ticket.ID)).First(ctx)
		if err != nil || !found || stored.Subject != "Valid off-page selection" {
			t.Fatal("rejected selection changed parent", err)
		}
		storedLink, found, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.ID.Exact(link.ID)).First(ctx)
		if err != nil || !found || storedLink != link {
			t.Fatal("rejected selection changed membership", err)
		}
	})
}
