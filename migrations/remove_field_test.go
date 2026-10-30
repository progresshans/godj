package migrations

import (
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestRemoveFieldStateAndHistoricalOwnership(t *testing.T) {
	schema, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "removal", Models: []ir.Model{{Name: "item", GoName: "Item", Fields: []ir.Field{
		{Name: "legacy", GoName: "Legacy", Kind: ir.FieldChar, MaxLength: 30, Nullable: true, Default: &ir.Scalar{Kind: ir.ScalarString, String: "restored"}},
		{Name: "tail", GoName: "Tail", Kind: ir.FieldText, Nullable: true},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := NewProjectState(schema)
	if err != nil {
		t.Fatal(err)
	}
	model := schema.Models[0]
	legacy := model.Fields[1]
	op := RemoveField{AppLabel: "removal", ModelName: "item", Field: legacy.Clone(), BeforeField: "tail"}
	forward, err := op.stateForward(base)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := forward.Model("removal", "item")
	if len(actual.Fields) != 2 || actual.Fields[0].Name != "id" || actual.Fields[1].Name != "tail" {
		t.Fatal("field removal changed retained order")
	}
	restored, err := op.stateBackward(forward)
	if err != nil || !restored.Equal(base) {
		t.Fatal("schema reverse lost the exact field/position", err)
	}
	for _, bad := range []RemoveField{
		{AppLabel: "removal", ModelName: "item", Field: legacy.Clone()},
		{AppLabel: "removal", ModelName: "item", Field: model.Fields[0].Clone(), BeforeField: "legacy"},
		{AppLabel: "removal", ModelName: "missing", Field: legacy.Clone(), BeforeField: "tail"},
	} {
		if state, err := bad.stateForward(base); err == nil || !state.Equal(base) {
			t.Fatal("invalid removal changed state")
		}
	}
	changed := op
	changed.Field = changed.Field.Clone()
	changed.Field.Default.String = "unreviewed"
	if state, err := changed.stateForward(base); err == nil || !state.Equal(base) {
		t.Fatal("removal ignored its full preimage")
	}
	if state, err := op.stateForward(forward); err == nil || !state.Equal(forward) {
		t.Fatal("repeated removal accepted")
	}
	if state, err := op.stateBackward(base); err == nil || !state.Equal(base) {
		t.Fatal("reverse replaced an existing field")
	}
	definitions := []Migration{
		{App: "removal", Name: "0001_initial", Operations: []Operation{CreateModel{AppLabel: "removal", Model: model}}},
		{App: "removal", Name: "0002_remove", Dependencies: []MigrationKey{{App: "removal", Name: "0001_initial"}}, Operations: []Operation{&op}},
	}
	reconstructor := mustStateReconstructor(t, definitions...)
	op.Field.Default.String = "caller mutation"
	if latest := reconstructState(t, reconstructor, LatestStateRequest()); !latest.Equal(forward) {
		t.Fatal("mutable removal changed published state")
	}
	if before := reconstructState(t, reconstructor, BeforeStateRequest(definitions[1].Key())); !before.Equal(base) {
		t.Fatal("removal lost historical values")
	}
	constrained := schema.Clone()
	constrained.Models[0].UniqueConstraints = []ir.UniqueConstraint{{Name: "legacy_key", Fields: []string{"legacy"}}}
	protected, err := NewProjectState(constrained)
	if err != nil {
		t.Fatal(err)
	}
	remove := RemoveField{AppLabel: "removal", ModelName: "item", Field: legacy.Clone(), BeforeField: "tail"}
	if state, err := remove.stateForward(protected); err == nil || !state.Equal(protected) {
		t.Fatal("retained named constraint lost its field")
	}
}
