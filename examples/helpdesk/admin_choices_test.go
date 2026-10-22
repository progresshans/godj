package helpdesk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
	xhtml "golang.org/x/net/html"
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
		stored, found, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.Exact(ticket.ID)).OrderBy(models.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found || stored.Subject != "Valid off-page selection" {
			t.Fatal("rejected selection changed parent", err)
		}
		storedLink, found, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.ID.Exact(link.ID)).OrderBy(models.TicketLabelFields.ID.Asc()).First(ctx)
		if err != nil || !found || storedLink != link {
			t.Fatal("rejected selection changed membership", err)
		}
	})
}

// Inspect the successful form's DOM semantics without depending on attribute
// order. Inert prototypes do not count as submitted controls or rendered rows.
type adminChoiceOption struct {
	Value, Label             string
	Selected, Page, Disabled bool
}
type adminChoiceControl struct {
	Name, SearchURL                    string
	Required, Multiple, Disabled, More bool
	Limit                              int
	Options                            []adminChoiceOption
}

func (control adminChoiceControl) selected() []string {
	var selected []string
	for _, option := range control.Options {
		if option.Selected {
			selected = append(selected, option.Value)
		}
	}
	slices.Sort(selected)
	return selected
}
func (control adminChoiceControl) pageCount() int {
	count := 0
	for _, option := range control.Options {
		if option.Page {
			count++
		}
	}
	return count
}
func (control adminChoiceControl) option(value string) (adminChoiceOption, bool) {
	for _, option := range control.Options {
		if option.Value == value {
			return option, true
		}
	}
	return adminChoiceOption{}, false
}
func adminChoiceControls(t *testing.T, response *httptest.ResponseRecorder, expectedAction string) map[string]adminChoiceControl {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("Admin choice form: %d %s", response.Code, response.Body.String())
	}
	document, err := xhtml.Parse(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]adminChoiceControl)
	var content func(*xhtml.Node) string
	content = func(node *xhtml.Node) string {
		if node.Type == xhtml.TextNode {
			return node.Data
		}
		var value string
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			value += content(child)
		}
		return value
	}
	var controls func(*xhtml.Node, bool)
	controls = func(node *xhtml.Node, disabled bool) {
		if node.Type == xhtml.ElementNode && node.Data == "template" {
			return
		}
		_, off := adminInlineAttribute(node, "disabled")
		disabled = disabled || off
		if node.Type == xhtml.ElementNode && node.Data == "select" {
			name, named := adminInlineAttribute(node, "name")
			if !named || name == "" {
				t.Fatal("select lacks its submitted field name")
			}
			if _, exists := result[name]; exists {
				t.Fatalf("duplicate select %s", name)
			}
			control := adminChoiceControl{Name: name, Disabled: disabled}
			_, control.Required = adminInlineAttribute(node, "required")
			_, control.Multiple = adminInlineAttribute(node, "multiple")
			for ancestor := node.Parent; ancestor != nil && ancestor.Data != "form"; ancestor = ancestor.Parent {
				if address, found := adminInlineAttribute(ancestor, "data-choice-url"); found {
					control.SearchURL = address
					limit, _ := adminInlineAttribute(ancestor, "data-choice-limit")
					control.Limit, err = strconv.Atoi(limit)
					if err != nil || control.Limit < 1 || control.Limit > admin.MaximumListLimit {
						t.Fatal("invalid choice page limit", limit)
					}
					more, _ := adminInlineAttribute(ancestor, "data-choice-more")
					if more != "true" && more != "false" {
						t.Fatal("invalid choice page boundary", more)
					}
					control.More = more == "true"
					break
				}
			}
			for option := node.FirstChild; option != nil; option = option.NextSibling {
				if option.Type != xhtml.ElementNode || option.Data != "option" {
					continue
				}
				value, _ := adminInlineAttribute(option, "value")
				_, selected := adminInlineAttribute(option, "selected")
				_, page := adminInlineAttribute(option, "data-choice-page")
				_, off := adminInlineAttribute(option, "disabled")
				control.Options = append(control.Options, adminChoiceOption{Value: value, Label: content(option), Selected: selected, Page: page, Disabled: disabled || off})
			}
			result[name] = control
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			controls(child, disabled)
		}
	}
	forms := 0
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && node.Data == "form" {
			if action, _ := adminInlineAttribute(node, "action"); action == expectedAction {
				forms++
				controls(node, false)
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if forms != 1 {
		t.Fatal("expected exactly one submitted Admin form", forms)
	}
	return result
}

type helpdeskAdminChoicePage struct {
	Items         []struct{ Value, Label string } `json:"items"`
	Offset, Limit int
	More          bool
}

func readHelpdeskAdminChoicePage(t *testing.T, response *httptest.ResponseRecorder) helpdeskAdminChoicePage {
	t.Helper()
	var page helpdeskAdminChoicePage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Items == nil || page.Limit < 1 || len(page.Items) > page.Limit {
		t.Fatal("invalid bounded choice response", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json; charset=utf-8" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("choice endpoint lost its content or cache boundary")
	}
	return page
}
