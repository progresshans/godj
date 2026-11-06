package migrationgraphtest

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
)

//go:embed testdata/django61.json
var djangoReference []byte

type graphObservation struct {
	Phase   string                 `json:"phase"`
	Fields  map[string][]string    `json:"fields"`
	Choices map[string][][2]string `json:"choices"`
	Rows    map[string][][]any     `json:"rows"`
	Applied []string               `json:"applied"`
}

// Observe derives every value from returned historical state and actual DB
// rows. It never reads expected outcomes to select a field, row or operation.
func observe(t *testing.T, ctx context.Context, binding Binding, phase string, state migrations.ProjectState) graphObservation {
	t.Helper()
	value := graphObservation{Phase: phase, Fields: map[string][]string{}, Choices: map[string][][2]string{}, Rows: map[string][][]any{}, Applied: []string{}}
	schema, _ := state.Schema("graph")
	for _, model := range schema.Models {
		columns := make([]string, len(model.Fields))
		for index, field := range model.Fields {
			value.Fields[model.Name] = append(value.Fields[model.Name], field.Name)
			columns[index] = `"` + field.Column + `"`
			for _, choice := range field.Choices {
				key := model.Name + "." + field.Name
				value.Choices[key] = append(value.Choices[key], [2]string{choice.Value.String, choice.Label})
			}
		}
		rows, err := binding.Database.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM "+binding.Table(model.DBTable)+" ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		value.Rows[model.Name] = [][]any{}
		for rows.Next() {
			row := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range row {
				pointers[index] = &row[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			for index, cell := range row {
				if text, ok := cell.([]byte); ok {
					row[index] = string(text)
				}
			}
			value.Rows[model.Name] = append(value.Rows[model.Name], row)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := binding.Database.QueryContext(ctx, "SELECT name FROM "+binding.Table("godj_migrations")+" WHERE app='graph' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		value.Applied = append(value.Applied, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertDjangoReference(t *testing.T, observations []graphObservation) {
	t.Helper()
	var reference struct {
		Django       string             `json:"django"`
		Python       string             `json:"python"`
		Observations []graphObservation `json:"observations"`
	}
	if err := json.Unmarshal(djangoReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.7" || len(reference.Observations) != 10 {
		t.Fatal("Django graph reference version or roster is incomplete")
	}
	// DEV-0013: Django SQLite resets the deleted-row sequence high-water on
	// remake. GoDj's existing row/sequence preservation policy forbids reuse.
	// This exact scalar is the only accepted difference; the oracle stays raw.
	reversed := &reference.Observations[6]
	rows := reversed.Rows["a"]
	if reversed.Phase != "reverse_links" || len(rows) != 3 || len(rows[2]) != 3 ||
		rows[2][0] != float64(3) || rows[2][1] != "next" || rows[2][2] != nil {
		t.Fatal("DEV-0013 reference selector changed; re-review the observed difference")
	}
	rows[2][0] = float64(101)
	actual, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(reference.Observations)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatalf("historical graph differs from Django observations plus DEV-0013\nactual: %s\nexpected: %s", actual, expected)
	}
}
