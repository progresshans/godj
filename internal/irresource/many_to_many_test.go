package irresource

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestManyToManyIntentResourceLimitsIncludeThroughAndColumnlessFields(t *testing.T) {
	model := ir.Model{Name: "owner", GoName: "Owner", DBTable: "owners", ManyToMany: []ir.ManyToManyField{{Name: "labels", GoName: "Labels", Target: ir.ModelIdentity{AppLabel: "app", ModelName: "label"}, Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "app", ModelName: "link"}, SourceField: "owner", TargetField: "label"}}}}
	limits := Limits{Fields: 1, StringBytes: 32, Nodes: 6, Bytes: 1024}
	budget := New(limits)
	if err := budget.ScanModel("model", model); err != nil {
		t.Fatal(err)
	}
	nodes, used := budget.Counts()
	if nodes != 6 || used == 0 {
		t.Fatal("through not charged", nodes, used)
	}
	limits.Nodes = 5
	budget = New(limits)
	if err := budget.ScanModel("model", model); err == nil {
		t.Fatal("node limit bypass")
	}
	limits.Nodes = 6
	model.ManyToMany[0].Through.SourceField = strings.Repeat("x", 33)
	budget = New(limits)
	if err := budget.ScanModel("model", model); err == nil {
		t.Fatal("through string limit bypass")
	}
	model.ManyToMany[0].Through.SourceField = "owner"
	model.Fields = []ir.Field{{Name: "id"}}
	budget = New(limits)
	if err := budget.ScanModel("model", model); err == nil {
		t.Fatal("columnless field bypassed per-model maximum")
	}
}
