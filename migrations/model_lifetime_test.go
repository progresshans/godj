package migrations

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestDeletedModelIdentityRequiresTheCausalCreationAndExactPriorShape(t *testing.T) {
	old := stateModel("entry", "archive_entry")
	keep := stateModel("keeper", "archive_keeper")
	old.GoName, keep.GoName = "Entry", "Keeper"
	create := Migration{App: "archive", Name: "0001_base", Operations: []Operation{CreateModel{AppLabel: "archive", Model: old}, CreateModel{AppLabel: "archive", Model: keep}}}
	drop := Migration{App: "archive", Name: "0002_drop", Dependencies: []MigrationKey{create.Key()}, Operations: []Operation{DeleteModel{AppLabel: "archive", Model: old, BeforeModel: "keeper"}}}
	fresh := old.Clone()
	fresh.Fields = append(fresh.Fields, ir.Field{Name: "new_value", GoName: "NewValue", Column: "new_value", Kind: ir.FieldText})
	again := Migration{App: "archive", Name: "0003_new", Dependencies: []MigrationKey{drop.Key()}, Operations: []Operation{CreateModel{AppLabel: "archive", Model: fresh}}}
	r, err := NewStateReconstructor(again, drop, create)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		target            MigrationKey
		present, newField bool
	}{{create.Key(), true, false}, {drop.Key(), false, false}, {again.Key(), true, true}} {
		state, err := r.Reconstruct(AfterStateRequest(sample.target))
		if err != nil {
			t.Fatal(err)
		}
		model, ok := state.Model("archive", "entry")
		hasNew := false
		for _, field := range model.Fields {
			hasNew = hasNew || field.Name == "new_value"
		}
		if ok != sample.present || hasNew != sample.newField {
			t.Fatal("borrowed model from a different lifetime", sample, model)
		}
	}
	for _, bad := range []DeleteModel{
		{AppLabel: "archive", Model: old}, {AppLabel: "archive", Model: old, BeforeModel: "absent"}, {AppLabel: "archive", Model: fresh, BeforeModel: "keeper"},
	} {
		edit := drop
		edit.Operations = []Operation{bad}
		if _, err := NewStateReconstructor(create, edit); err == nil {
			t.Fatal("accepted stale deletion", bad)
		}
	}
	unordered := again
	unordered.Dependencies = []MigrationKey{create.Key()}
	if _, err := NewStateReconstructor(create, drop, unordered); err == nil {
		t.Fatal("unordered recreation")
	}
	audit := Migration{App: "audit", Name: "0001_read", Dependencies: []MigrationKey{create.Key()}, Operations: []Operation{RunData{
		Key: DataKey{App: "audit", Name: "review", Version: 1}, Forward: DataNoop, Reverse: DataNoop, Models: []ir.ModelIdentity{{AppLabel: "archive", ModelName: "entry"}},
	}}}
	var detail *Error
	if _, err := NewStateReconstructor(create, drop, again, audit); !errors.As(err, &detail) || detail.Code != CodeInvalidState || detail.App != "audit" {
		t.Fatal("unordered model access", err)
	}
	audit.Dependencies = []MigrationKey{again.Key()}
	if _, err := NewStateReconstructor(create, drop, again, audit); err != nil {
		t.Fatal("new model not visible to its dependent", err)
	}
}
