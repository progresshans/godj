package migrationgraph

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestTableOriginsFollowRenamesAndResetOnRecreation(t *testing.T) {
	entry := graphTestModel(t, "graph", "entry").Model
	publication := graphTestModel(t, "graph", "publication").Model
	archive := graphTestModel(t, "graph", "archive").Model
	intent := MigrationIntent{Operations: []MigrationOperation{
		{OperationIndex: 0, Kind: MigrationRenameModel, Before: entry, After: publication},
		{OperationIndex: 1, Kind: MigrationRenameModel, Before: publication, After: archive},
		{OperationIndex: 2, Kind: MigrationDeleteModel, Before: archive},
		{OperationIndex: 3, Kind: MigrationCreateModel, After: entry},
		{OperationIndex: 4, Kind: MigrationRenameModel, Before: entry, After: publication},
		{OperationIndex: 5, Kind: MigrationDeleteModel, Before: publication},
	}}
	plan, err := ResolveMigrationGraphPlan("graph", "0002", false, intent)
	if err != nil {
		t.Fatal(err)
	}
	initial := TableOrigin{Name: entry.DBTable, CreatedAt: -1}
	created := TableOrigin{Name: entry.DBTable, CreatedAt: 3, InitialFields: len(entry.Fields)}
	want := []TableOrigin{initial, initial, initial, {CreatedAt: -1}, created, created}
	got := plan.InputTables()
	if !slices.Equal(got, want) {
		t.Fatalf("physical input tables = %+v, want %+v", got, want)
	}
	got[0] = created
	if !slices.Equal(plan.InputTables(), want) {
		t.Fatal("changing a returned origin altered a subsequent preflight")
	}
}

func TestModelRenameBoundsExpandedIntermediaryIdentifiersBeforeAllocation(t *testing.T) {
	before := ir.Model{Name: "item", GoName: "Item", DBTable: "items"}
	for i := 0; i < 64; i++ {
		suffix := strconv.Itoa(i)
		before.ManyToMany = append(before.ManyToMany, ir.ManyToManyField{Name: "labels" + suffix, GoName: "Labels" + suffix, Target: ir.ModelIdentity{AppLabel: "app", ModelName: "tag"}})
	}
	after := before.Clone()
	after.Name = "next"
	after.DBTable = strings.Repeat("x", migrationGraphMaxStringBytes/2)
	if _, err := NewModelRename("app", before, after); err == nil || !strings.Contains(err.Error(), "aggregate relation intent byte limit") {
		t.Fatal("expanded table names did not hit the aggregate budget before normalization", err)
	}
}
