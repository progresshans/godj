package consumertest_test

import (
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
)

func verifyGeneratedPriorityEffects(t *testing.T, runtime *systemstate.Runtime, category int64, actor string, rows map[string]models.Ticket) {
	t.Helper()
	if len(rows) != 4 {
		t.Fatal("priority generated consumer omitted retained rows")
	}
	for index, label := range []string{"Low", "Normal", "Unset", "Urgent"} {
		row, found := rows["Client priority "+label]
		if !found || row.ID <= 0 || row.CategoryID != category || row.Priority == nil || *row.Priority != 1 || row.Closed != (index == 1) || row.ExternalPayload != nil || row.ExternalPayloadDigest != nil || row.Details != nil {
			t.Fatal("priority consumer final database values", label)
		}
		links, err := models.TicketLabelObjects.Using(runtime).Filter(models.TicketLabelFields.TicketID.Exact(row.ID)).All(t.Context())
		if err != nil || len(links) != 0 {
			t.Fatal("priority consumer created a label link", err)
		}
		history, err := runtime.AuditHistory(t.Context(), "helpdesk.ticket", row.ID, 10)
		changes := []int{2, 1, 2, 0}[index]
		if err != nil || len(history) != changes+1 {
			t.Fatal("priority consumer durable event count", label, len(history), err)
		}
		adds, updates := 0, 0
		for _, event := range history {
			if event.ActorID != actor || event.DisplayLabel != row.Subject {
				t.Fatal("priority audit actor/label")
			}
			switch event.Action {
			case admin.ActionAdd:
				adds++
				if len(event.ChangedFields) != 0 {
					t.Fatal("priority fixture creation event changed")
				}
			case admin.ActionChange:
				updates++
				if !slices.Equal(event.ChangedFields, []string{"priority"}) {
					t.Fatal("priority event includes unrelated fields")
				}
			default:
				t.Fatal("unexpected priority event action")
			}
		}
		if adds != 1 || updates != changes {
			t.Fatal("priority no-op or rejected selection appended audit")
		}
	}
}
