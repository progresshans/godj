package migrationgraph_test

import (
	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/internal/migrationgraph"
	"github.com/progresshans/godj/schema/ir"
	"strings"
	"testing"
)

func TestAutomaticStorageGraphSealsCompleteLifecycleAndRejectsForgedAuthority(t *testing.T) {
	_, models, field := manytomanytest.AutomaticHistory(t, false, false)
	before := models[0]
	with := before.Clone()
	with.ManyToMany = []ir.ManyToManyField{field}
	renamed := with.Clone()
	renamed.ManyToMany[0].Name, renamed.ManyToMany[0].GoName = "tags", "Tags"
	related := func(owner ir.Model) []migrationgraph.MigrationModel {
		return []migrationgraph.MigrationModel{{AppLabel: "manyhistory", Model: models[1]}, {AppLabel: "manyhistory", Model: manytomanytest.AutomaticModel(t, owner, owner.ManyToMany[0])}}
	}
	intent := migrationgraph.MigrationIntent{Operations: []migrationgraph.MigrationOperation{
		{OperationIndex: 0, Kind: migrationgraph.MigrationAlterManyToMany, Before: before, After: with, RelatedModels: related(with)},
		{OperationIndex: 1, Kind: migrationgraph.MigrationAlterManyToMany, Before: with, After: renamed, RelatedModels: related(renamed)},
		{OperationIndex: 2, Kind: migrationgraph.MigrationAlterManyToMany, Before: renamed, After: before, RelatedModels: related(renamed)},
	}}
	plan, err := migrationgraph.ResolveMigrationGraphPlan("manyhistory", "step", false, intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.InitialModels()) != 2 || len(plan.FinalModels()) != 2 || len(plan.StorageChanges()) != 3 {
		t.Fatal("transient ownership missing")
	}
	changes := plan.StorageChanges()
	changes[1].Before.Fields[1].Column = "mutated"
	if plan.StorageChanges()[1].Before.Fields[1].Column != "source_id" {
		t.Fatal("plan leaked auxiliary aliases")
	}
	create := migrationgraph.MigrationIntent{Operations: []migrationgraph.MigrationOperation{{Kind: migrationgraph.MigrationCreateModel, After: with.Clone(), RelatedModels: related(with)}}}
	created, err := migrationgraph.ResolveMigrationGraphPlan("manyhistory", "create", false, create)
	if err != nil {
		t.Fatal(err)
	}
	create.Operations[0].After.Fields[0].GoName = "Forged"
	create.Operations[0].After.ManyToMany[0].GoName = "Forged"
	owned := created.StorageChanges()[0].After
	if owned.Fields[0].GoName != with.Fields[0].GoName || owned.ManyToMany[0].GoName != with.ManyToMany[0].GoName {
		t.Fatal("plan retained caller-owned CreateModel metadata")
	}
	for _, mode := range []string{"missing", "forged", "discontinuous", "future", "unowned"} {
		t.Run(mode, func(t *testing.T) {
			bad := intent.Clone()
			switch mode {
			case "missing":
				bad.Operations[0].RelatedModels = bad.Operations[0].RelatedModels[:1]
			case "forged":
				bad.Operations[0].RelatedModels[1].Model.Fields[1].Nullable = true
			case "discontinuous":
				bad.Operations[1].Before = before
			case "future":
				bad.Operations[0].RelatedModels[1] = bad.Operations[1].RelatedModels[1]
			case "unowned":
				bad.Operations[1].After.ManyToMany[0].Through = &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "manyhistory", ModelName: "owner_labels"}, SourceField: "source", TargetField: "target"}
			}
			if _, err := migrationgraph.ResolveMigrationGraphPlan("manyhistory", "step", false, bad); err == nil {
				t.Fatal("invalid storage authority accepted")
			}
		})
	}
}

func TestAutomaticStorageDerivationBoundsNamesAndFanout(t *testing.T) {
	_, models, field := manytomanytest.AutomaticHistory(t, false, false)
	for _, fanout := range []bool{false, true} {
		owner := models[0].Clone()
		owner.DBTable = strings.Repeat("x", (1<<20)-1)
		owner.ManyToMany = []ir.ManyToManyField{field}
		if fanout {
			owner.DBTable = strings.Repeat("x", 1<<18)
			owner.ManyToMany = make([]ir.ManyToManyField, 100)
			for i := range owner.ManyToMany {
				owner.ManyToMany[i] = field
			}
		}
		operation := migrationgraph.MigrationOperation{Kind: migrationgraph.MigrationCreateModel, After: owner}
		if _, err := operation.AutomaticStorageChanges("manyhistory"); err == nil {
			t.Fatal("derived allocation escaped budget", fanout)
		}
	}
}
