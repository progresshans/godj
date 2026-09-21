package irresource_test

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/internal/irresource"
	"github.com/progresshans/godj/schema/ir"
)

func TestNamedConstraintMembersConsumeIndependentIntentResources(t *testing.T) {
	model := ir.Model{Fields: []ir.Field{{Name: "first"}, {Name: "second"}}, UniqueConstraints: []ir.UniqueConstraint{{Name: "unique", Fields: []string{"first", "second"}}}}
	before := model.Clone()
	for _, limit := range []struct {
		limits irresource.Limits
		valid  bool
	}{
		{irresource.Limits{Fields: 2, StringBytes: 6, Nodes: 8, Bytes: 28}, true},
		{irresource.Limits{Fields: 2, StringBytes: 6, Nodes: 7, Bytes: 28}, false},
		{irresource.Limits{Fields: 2, StringBytes: 6, Nodes: 8, Bytes: 27}, false},
		{irresource.Limits{Fields: 2, StringBytes: 5, Nodes: 8, Bytes: 28}, false},
	} {
		budget := irresource.New(limit.limits)
		if err := budget.ScanModel("model", model); (err == nil) != limit.valid {
			t.Fatalf("constraint resource boundary %+v: %v", limit, err)
		}
	}
	if !reflect.DeepEqual(before, model) {
		t.Fatal("resource admission mutated caller input")
	}
	model.Fields = nil
	model.UniqueConstraints[0].Fields = []string{"first", "second", "third"}
	budget := irresource.New(irresource.Limits{Fields: 2, StringBytes: 16, Nodes: 100, Bytes: 1000})
	if err := budget.ScanModel("model", model); err == nil {
		t.Fatal("constraint member limit bypassed field count budget")
	}
}
