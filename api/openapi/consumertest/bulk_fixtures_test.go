package consumertest_test

import (
	"crypto/sha256"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
)

// The parent reads the real database independently of the generated client's
// success receipt. Both rows, selected links (in the fixture owner), exact
// scalar values and one durable audit each must survive the same commit.
func verifyGeneratedBulkTicketEffects(t *testing.T, runtime *systemstate.Runtime, category int64, actor string, rows map[string]models.Ticket) []int64 {
	t.Helper()
	if len(rows) != 2 {
		t.Fatal("generated client did not persist both bulk rows")
	}
	ids := make([]int64, 2)
	for index, name := range []string{"Client bulk first", "Client bulk second"} {
		row, found := rows[name]
		if !found || row.ID <= 0 || row.CategoryID != category || row.Closed || row.Details != nil || row.Priority != nil || row.Resolution != nil || row.DueAt != nil || row.ServiceOn != nil || row.Elapsed != nil || row.Effort != nil || row.ExternalReference != nil || row.ExternalURL != nil {
			t.Fatal("generated bulk row default/scope fidelity")
		}
		ids[index] = row.ID
		if index == 0 {
			if row.ExternalPayload == nil || row.ExternalPayload.Text != `{"":9007199254740993}` || row.ExternalPayloadDigest == nil || row.Reviewed == nil || *row.Reviewed || row.ServiceAt == nil || row.ServiceAt.String() != "00:00:00" || row.ExpectedCost == nil {
				t.Fatal("generated bulk exact scalar presence")
			}
			sum := sha256.Sum256([]byte(row.ExternalPayload.Text))
			cost, err := row.ExpectedCost.Fixed(2)
			if err != nil || cost != "12.30" || row.ExternalPayloadDigest.Data != string(sum[:]) {
				t.Fatal("generated bulk decimal/digest persistence", err)
			}
		} else if row.ExternalPayload != nil || row.ExternalPayloadDigest != nil || row.Reviewed != nil || row.ServiceAt != nil || row.ExpectedCost != nil {
			t.Fatal("generated bulk omitted scalar values")
		}
		history, err := runtime.AuditHistory(t.Context(), "helpdesk.ticket", row.ID, 10)
		if err != nil || len(history) != 1 || history[0].ActorID != actor || history[0].Action != admin.ActionAdd || history[0].DisplayLabel != name {
			t.Fatal("generated bulk durable audit", err)
		}
	}
	if ids[1] <= ids[0] {
		t.Fatal("generated bulk order lost")
	}
	return ids
}
