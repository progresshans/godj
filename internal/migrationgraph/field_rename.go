package migrationgraph

import (
	"errors"

	"github.com/progresshans/godj/internal/irresource"
	"github.com/progresshans/godj/schema/ir"
)

// RenamedField verifies every field and every reference in the sealed source
// model. Exactly one field changes name at its original ordinal.
func (operation MigrationOperation) RenamedField(app string) (ir.Field, ir.Field, error) {
	before, after := operation.Before, operation.After
	if operation.Kind != MigrationRenameField || len(before.Fields) != len(after.Fields) {
		return ir.Field{}, ir.Field{}, errors.New("expected a field rename with the same field count")
	}
	budget := irresource.New(irresource.Limits{Fields: migrationGraphMaxFields, StringBytes: migrationGraphMaxStringBytes, Nodes: migrationGraphMaxNodes, Bytes: migrationGraphMaxBytes})
	if err := budget.ScanModel("rename.before", before); err != nil {
		return ir.Field{}, ir.Field{}, err
	}
	if err := budget.ScanModel("rename.after", after); err != nil {
		return ir.Field{}, ir.Field{}, err
	}
	for i, field := range before.Fields {
		next := after.Fields[i]
		if field.Name == next.Name {
			continue
		}
		model, err := ir.RenameModelField(app, before, field, next)
		if err != nil {
			return ir.Field{}, ir.Field{}, err
		}
		if !model.Equal(after) {
			return ir.Field{}, ir.Field{}, errors.New("field rename changes other model metadata or field order")
		}
		return field.Clone(), next.Clone(), nil
	}
	return ir.Field{}, ir.Field{}, errors.New("field rename does not change a logical name")
}

// RenameReferences lists the incoming FK columns authorized by the sealed
// operation. Physical catalogs contain the pre-rename column names.
func (operation MigrationOperation) RenameReferences(app string, before ir.Field) map[string]map[string]bool {
	allowed := make(map[string]map[string]bool)
	if !before.PrimaryKey {
		return allowed
	}
	root := ir.ModelIdentity{AppLabel: app, ModelName: operation.Before.Name}
	add := func(model ir.Model) {
		if _, visited := allowed[model.DBTable]; visited {
			return
		}
		fields := make(map[string]bool)
		for _, field := range model.Fields {
			if field.Relation != nil && field.Relation.Target == root {
				fields[field.Column] = true
			}
		}
		allowed[model.DBTable] = fields
	}
	add(operation.Before)
	for _, target := range operation.Targets {
		add(target.TargetModel)
	}
	for _, related := range operation.RelatedModels {
		add(related.Model)
	}
	return allowed
}
