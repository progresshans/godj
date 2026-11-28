package migrationgraphtest

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/progresshans/godj/migrations"
)

//go:embed testdata/django-autodetect61.json
var autodetectionReference []byte

type autodetectionReferenceField struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Column     string `json:"column"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primary_key"`
	HasDefault bool   `json:"has_default"`
	Target     string `json:"target,omitempty"`
	TargetKey  string `json:"target_key,omitempty"`
	OnDelete   string `json:"on_delete,omitempty"`
}
type autodetectionReferenceModel struct {
	App    string                        `json:"app"`
	Model  string                        `json:"model"`
	Fields []autodetectionReferenceField `json:"fields"`
}

func assertAutodetectionReference(t *testing.T, ctx context.Context, binding Binding, state migrations.ProjectState, appA, appB string, cross bool) {
	t.Helper()
	var reference struct {
		Django       string `json:"django"`
		Observations []struct {
			Case               string                        `json:"case"`
			Graph              []autodetectionReferenceModel `json:"graph"`
			DeclaredFieldOrder map[string][]string           `json:"declared_field_order"`
			Rows               map[string][][]string         `json:"rows"`
		} `json:"observations"`
	}
	if err := json.Unmarshal(autodetectionReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 2 {
		t.Fatal("unexpected independent autodetector reference")
	}
	index := 0
	if cross {
		index = 1
	}
	want := reference.Observations[index]
	if want.Case != []string{"same_app", "cross_app"}[index] {
		t.Fatal("reference case order changed")
	}
	var graph []autodetectionReferenceModel
	order := make(map[string][]string)
	for _, app := range state.Apps() {
		schema, _ := state.Schema(app)
		for _, model := range schema.Models {
			value := autodetectionReferenceModel{App: app, Model: model.Name}
			for _, field := range model.Fields {
				order[app+"."+model.Name] = append(order[app+"."+model.Name], field.Name)
				observed := autodetectionReferenceField{Name: field.Name, Kind: string(field.Kind), Column: field.Column, Nullable: field.Nullable, PrimaryKey: field.PrimaryKey, HasDefault: field.Default != nil}
				if field.Relation != nil {
					target := field.Relation.Target
					observed.Target = target.AppLabel + "." + target.ModelName
					observed.OnDelete = string(field.Relation.OnDelete)
					model, ok := state.Model(target.AppLabel, target.ModelName)
					if !ok {
						t.Fatal("missing observed target")
					}
					for _, key := range model.Fields {
						if key.PrimaryKey {
							observed.TargetKey = key.Column
						}
					}
				}
				value.Fields = append(value.Fields, observed)
			}
			sort.Slice(value.Fields, func(i, j int) bool { return value.Fields[i].Name < value.Fields[j].Name })
			graph = append(graph, value)
		}
	}
	sort.Slice(graph, func(i, j int) bool {
		if graph[i].App != graph[j].App {
			return graph[i].App < graph[j].App
		}
		return graph[i].Model < graph[j].Model
	})
	if !reflect.DeepEqual(graph, want.Graph) {
		t.Fatalf("named field/constraint graph differs from Django: got=%+v want=%+v", graph, want.Graph)
	}
	// GoDj preserves declaration order. Django's different historical field
	// order and file/operation names remain in the raw reference, not rewritten.
	if !reflect.DeepEqual(order, want.DeclaredFieldOrder) {
		t.Fatal("automatic migration lost the independently declared field order")
	}
	tableA, tableB := binding.Table(appA+"_a"), binding.Table(appB+"_b")
	statements := map[string]string{
		appA + ".a": fmt.Sprintf(`SELECT source."label", peer."label", parent."label" FROM %s AS source JOIN %s AS peer ON source."peer_id"=peer."key" JOIN %s AS parent ON source."parent_id"=parent."key" ORDER BY source."key"`, tableA, tableB, tableA),
		appB + ".b": fmt.Sprintf(`SELECT source."label", owner."label" FROM %s AS source JOIN %s AS owner ON source."owner_id"=owner."key" ORDER BY source."key"`, tableB, tableA),
	}
	for identity, statement := range statements {
		rows, err := binding.Database.QueryContext(ctx, statement)
		if err != nil {
			t.Fatal(err)
		}
		var actual [][]string
		for rows.Next() {
			values := make([]string, 2)
			if identity == appA+".a" {
				values = make([]string, 3)
			}
			dest := make([]any, len(values))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			actual = append(actual, values)
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("read observed rows: %v %v", err, closeErr)
		}
		if !reflect.DeepEqual(actual, want.Rows[identity]) {
			t.Fatalf("actual relation values for %s differ from Django: %v", identity, actual)
		}
	}
}
