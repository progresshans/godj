package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
)

type singleTicketInvalidOutputRows struct{ db.Rows }

func (rows singleTicketInvalidOutputRows) Scan(values ...any) error {
	if err := rows.Rows.Scan(values...); err != nil {
		return err
	}
	if len(values) < 2 {
		return errors.New("unexpected Ticket row shape")
	}
	subject, ok := values[1].(*string)
	if !ok {
		return errors.New("unexpected Ticket subject scanner")
	}
	*subject = strings.Repeat("x", 121)
	return nil
}

// Single and collection creation share one Body and one commit/prepared-output
// policy. The two native Helpdesk owners run this through real session HTTP.
func verifyHelpdeskTypedTicketCreate(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	t.Helper()
	for _, mode := range []string{"created", "single_reload_error", "single_output_error", "single_cancel", "missing_callback", "nil_session", "repeated_callback", "concurrent_callback", "swallowed_callback", "rollback_unknown", "commit_unknown", "post_commit_cancel", "denied_add", "denied_label", "csrf", "invalid_subject", "nested_label_index"} {
		t.Run(mode, func(t *testing.T) {
			category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Single prepared "+mode))
			if err != nil {
				t.Fatal(err)
			}
			backend := &bulkTicketBackend{Backend: runtime, category: category.ID}
			app, err := helpdesk.New(backend, category.ID)
			if err != nil {
				t.Fatal(err)
			}
			registry, err := app.AdminRegistry(helpdesk.AdminConfig{AppendAudit: runtime.AppendAudit})
			if err != nil {
				t.Fatal(err)
			}
			var deny helpdeskDeniedPermissions
			if mode == "denied_add" {
				deny = []auth.Permission{helpdesk.AddTicket}
			}
			if mode == "denied_label" {
				deny = []auth.Permission{helpdesk.ViewLabel}
			}
			client := helpdeskHTTPRegistry(t, app, registry, runtime, deny, runtime.AppendAudit)
			client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
			if result := client.request("GET", "/admin/", "", false); result.Code != 200 {
				t.Fatal("admitted single session", result.Code)
			}
			body := `{"subject":"  Prepared ticket  ","reviewed":false,"labels":[]}`
			status := 500
			switch mode {
			case "created", "post_commit_cancel":
				status = 201
			case "denied_add", "denied_label":
				status = 403
				body = "{"
			case "csrf":
				status = 403
				client.csrf = ""
				body = "{"
			case "invalid_subject":
				status = 400
				body = `{"subject":true}`
			case "nested_label_index":
				status = 400
				body = `{"subject":"Prepared ticket","labels":[1,1,"invalid"]}`
			}
			requestContext, cancel := context.WithCancel(ctx)
			defer cancel()
			backend.cancel, backend.mode = cancel, mode
			response := client.requestContext(requestContext, "POST", "/api/tickets/", body, true)
			if response.Code != status || strings.Contains(response.Body.String(), "private") {
				t.Fatal("prepared single create outcome", response.Code, response.Body)
			}
			rows, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.CategoryID.Exact(category.ID)).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			committed := mode == "created" || mode == "post_commit_cancel" || mode == "commit_unknown"
			if len(rows) != 0 && !committed || committed && len(rows) != 1 {
				t.Fatal("failed single preparation committed a row", len(rows))
			}
			if committed {
				row := rows[0]
				if row.Subject != "Prepared ticket" || row.Closed || row.Details != nil || row.Priority != nil || row.Reviewed == nil || *row.Reviewed || row.CategoryID != category.ID {
					t.Fatal("typed single defaults/null/false changed", row)
				}
				if backend.atomics != 1 || backend.singleInserts != 1 || backend.bulks != 0 {
					t.Fatal("single completion was repeated", backend.atomics, backend.singleInserts)
				}
				if status == 201 {
					var decoded struct {
						ID       int64
						Subject  string
						Reviewed *bool
						Labels   []int64
					}
					if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded.ID != row.ID || decoded.Subject != row.Subject || decoded.Reviewed == nil || *decoded.Reviewed || decoded.Labels == nil || len(decoded.Labels) != 0 {
						t.Fatal("prepared output differs from committed data", err)
					}
				}
			}
			if mode == "post_commit_cancel" && requestContext.Err() == nil {
				t.Fatal("late-cancel case did not cancel")
			}
			if mode == "single_reload_error" || mode == "single_output_error" || mode == "single_cancel" || mode == "swallowed_callback" || mode == "rollback_unknown" {
				if backend.singleInserts != 1 {
					t.Fatal("failure did not follow insertion")
				}
			}
			if status == 400 || status == 403 {
				if backend.atomics != 0 || backend.singleInserts != 0 {
					t.Fatal("invalid/denied single reached I/O")
				}
			}
			if mode == "nested_label_index" && (!strings.Contains(response.Body.String(), `"key":"index","value":"2"`) || strings.Contains(response.Body.String(), `"key":"item_index"`)) {
				t.Fatal("single field index was changed to an outer row index", response.Body)
			}
			if backend.retained != nil {
				reads := backend.reads
				if err := runtime.AtomicRelation(ctx, backend.retained); err == nil || backend.reads != reads {
					t.Fatal("retained callback performed work", err)
				}
			}
		})
	}
}
