package consumertest_test

import (
	"crypto/sha256"
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
)

// The parent reads the real database independently of the generated client's
// success receipt. Both rows, selected links (in the fixture owner), exact
// scalar values and exact creation/change audits must survive. Id-only requests add no audit.
func verifyGeneratedBulkTicketEffects(t *testing.T, runtime *systemstate.Runtime, category int64, actor string, rows map[string]models.Ticket) []int64 {
	t.Helper()
	if len(rows) != 2 {
		t.Fatal("generated client did not persist both bulk rows")
	}
	ids := make([]int64, 2)
	for index, name := range []string{"Client bulk first", "Client bulk second edited"} {
		row, found := rows[name]
		if !found || row.ID <= 0 || row.CategoryID != category || row.Closed != (index == 1) || row.Priority != nil || row.Resolution != nil || row.DueAt != nil || row.ServiceOn != nil || row.Elapsed != nil || row.Effort != nil || row.ExternalReference != nil || row.ExternalURL != nil {
			t.Fatal("generated bulk row default/scope fidelity")
		}
		ids[index] = row.ID
		if index == 0 {
			if row.ExternalPayload == nil || row.ExternalPayload.Text != `{"":9007199254740993,"updated":true}` || row.ExternalPayloadDigest == nil || row.Reviewed == nil || *row.Reviewed || row.ServiceAt == nil || row.ServiceAt.String() != "00:00:00" || row.ExpectedCost == nil || row.Details == nil || *row.Details != "Bulk update details" {
				t.Fatal("generated bulk exact scalar presence")
			}
			sum := sha256.Sum256([]byte(row.ExternalPayload.Text))
			cost, err := row.ExpectedCost.Fixed(2)
			if err != nil || cost != "12.30" || row.ExternalPayloadDigest.Data != string(sum[:]) {
				t.Fatal("generated bulk decimal/digest persistence", err)
			}
		} else if row.Details != nil || row.ExternalPayload != nil || row.ExternalPayloadDigest != nil || row.Reviewed != nil || row.ServiceAt != nil || row.ExpectedCost != nil {
			t.Fatal("generated bulk omitted scalar values")
		}
		history, err := runtime.AuditHistory(t.Context(), "helpdesk.ticket", row.ID, 10)
		if err != nil || len(history) != 2 {
			t.Fatal("generated bulk creation/update audit count", err)
		}
		adds, changes := 0, 0
		for _, event := range history {
			if event.ActorID != actor {
				t.Fatal("generated bulk audit actor")
			}
			switch event.Action {
			case admin.ActionAdd:
				adds++
				originalName := []string{"Client bulk first", "Client bulk second"}[index]
				if event.DisplayLabel != originalName || len(event.ChangedFields) != 0 {
					t.Fatal("bulk creation audit was rewritten")
				}
			case admin.ActionChange:
				changes++
				fields := []string{"details", "external_payload", "external_payload_digest"}
				if index == 1 {
					fields = []string{"subject", "closed", "labels"}
				}
				if event.DisplayLabel != name || !slices.Equal(event.ChangedFields, fields) {
					t.Fatal("bulk update durable audit fields", event.ChangedFields)
				}
			default:
				t.Fatal("unexpected bulk audit action", event.Action)
			}
		}
		if adds != 1 || changes != 1 {
			t.Fatal("id-only bulk request appended another audit")
		}
	}
	if ids[1] <= ids[0] {
		t.Fatal("generated bulk order lost")
	}
	return ids
}
