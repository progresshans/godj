package helpdesk_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/query"
)

// Read the persisted columns shared by the 0015..0021 historical schemas.
// Comparing their raw storage detects changes without asking a current model
// scanner to select later columns before they exist. URL/Binary additions and
// reversals have independent current-model lifecycle checks. This allowlist is
// the historical projection, independent of future current-model growth.
func readPreURLTicketStorage(t *testing.T, ctx context.Context, backend db.Queryer, id int64) []any {
	t.Helper()
	byName := make(map[string]query.FieldRef)
	for _, field := range models.TicketObjects.Using(backend).Plan().SourceFields() {
		byName[field.Name()] = field
	}
	var fields []query.FieldRef
	for _, name := range []string{"id", "subject", "details", "closed", "category", "priority", "resolution", "due_at", "reviewed", "service_on", "service_at", "elapsed", "effort", "expected_cost", "external_reference", "external_payload"} {
		field, found := byName[name]
		if !found {
			t.Fatal("historical projection field disappeared", name)
		}
		fields = append(fields, field)
	}
	key := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan, err := query.NewPlan("helpdesk_ticket", fields).WithConditions(query.NewCondition(key, query.LookupExact, query.Integer(id)))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("historical ticket is absent", rows.Err())
	}
	values := make([]any, len(fields))
	targets := make([]any, len(fields))
	for index := range values {
		targets[index] = &values[index]
	}
	if err := rows.Scan(targets...); err != nil {
		t.Fatal(err)
	}
	for index, value := range values {
		if data, ok := value.([]byte); ok {
			values[index] = bytes.Clone(data)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("historical ticket cardinality/read failed", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return values
}
