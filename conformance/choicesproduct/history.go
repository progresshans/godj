// Package choicesproduct supplies the same historical choices fixture to real
// backend consumers. It owns no driver and produces no expectations from SQL.
package choicesproduct

import (
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

// Sources includes a relation target whose metadata changes before its source
// model in the same step, plus a later mixed physical/metadata step.
func Sources() ([]definition.Source, error) {
	s, err := schema.Build(schema.Definition{AppLabel: "choices", Models: []schema.Model{
		{Name: "category", GoName: "Category", Fields: []schema.Field{schema.CharField("name", "Name", 32)}},
		{Name: "entry", GoName: "Entry", Fields: []schema.Field{
			schema.CharField("status", "Status", 12, schema.Default("open")),
			schema.IntegerField("priority", "Priority", schema.Nullable()),
			schema.ForeignKey("category", "CategoryID", schema.Target("choices", "category"), schema.RelatedName("entries"), schema.Protect),
		}},
	}})
	if err != nil {
		return nil, err
	}
	category, entry := s.Models[0], s.Models[1]
	name, status, priority := category.Fields[1], entry.Fields[1], entry.Fields[2]
	chosenName, chosenStatus, chosenPriority := name.Clone(), status.Clone(), priority.Clone()
	chosenName.Choices = []ir.Choice{schema.Choice("ops", "Operations")}
	chosenStatus.Choices = []ir.Choice{schema.Choice("open", "Open"), schema.Choice("closed", "Closed")}
	chosenPriority.Choices = []ir.Choice{schema.Choice(int64(-1), "Low"), schema.Choice(int64(0), "Normal"), schema.Choice(int64(1), "High")}
	alter := func(model string, before, after ir.Field) migrations.Operation {
		return migrations.AlterField{AppLabel: "choices", ModelName: model, Before: before, After: after}
	}
	changedName, changedPriority := chosenName.Clone(), chosenPriority.Clone()
	changedName.Choices[0].Label = "Operations & support"
	changedPriority.Choices[0], changedPriority.Choices[2] = changedPriority.Choices[2], changedPriority.Choices[0]
	changedPriority.Choices[0].Label = "Urgent"
	removedName := changedName.Clone()
	removedName.Choices = nil
	changes := []migrations.Migration{
		{App: "choices", Name: "0001_initial", Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "choices", Model: category}, migrations.CreateModel{AppLabel: "choices", Model: entry},
		}},
		{App: "choices", Name: "0002_choices", Dependencies: []migrations.MigrationKey{{App: "choices", Name: "0001_initial"}}, Operations: []migrations.Operation{
			alter("category", name, chosenName), alter("entry", status, chosenStatus), alter("entry", priority, chosenPriority),
		}},
		{App: "choices", Name: "0003_labels", Dependencies: []migrations.MigrationKey{{App: "choices", Name: "0002_choices"}}, Operations: []migrations.Operation{
			alter("category", chosenName, changedName), alter("entry", chosenPriority, changedPriority),
		}},
		{App: "choices", Name: "0004_mixed", Dependencies: []migrations.MigrationKey{{App: "choices", Name: "0003_labels"}}, Operations: []migrations.Operation{
			alter("category", changedName, removedName),
			migrations.AddField{AppLabel: "choices", ModelName: "entry", Field: ir.Field{Name: "notes", GoName: "Notes", Column: "notes", Kind: ir.FieldText, Nullable: true, Choices: []ir.Choice{schema.Choice("done", "Done")}}},
		}},
	}
	sources := make([]definition.Source, len(changes))
	for index, change := range changes {
		wire, err := definition.Encode(definition.Producer{Name: "choices-product", Version: "1"}, change)
		if err != nil {
			return nil, err
		}
		sources[index] = definition.Source{SourceID: "choices/" + change.Name, Document: wire}
	}
	return sources, nil
}
