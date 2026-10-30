package accountdomaintest

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/account/domain/modeldef"
	"github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func mappingUpgrade(t *testing.T, open Open) {
	t.Run("mapping_populated_upgrade", func(t *testing.T) {
		backend, reopen := open(t)
		sources := domain.MigrationSources()
		historical, _, err := definition.Load(sources[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), historical, migrations.LatestLifecycleRequest()); err != nil {
			t.Fatal(err)
		}
		model := historical.Definitions()[0].Operations[0].(migrations.CreateModel).Model
		var previous int64
		var originals []map[string]query.Value
		var operation string
		for sequence := int64(1); sequence <= 2; sequence++ {
			change := prepare(t, roleA)
			generation := change.Generation()
			operation = change.OperationID()
			values := map[string]query.Value{"operation_id": query.String(operation), "sequence": query.Integer(sequence), "previous": query.Null(), "role_fingerprint": query.String(roleA), "generation": query.String(hex.EncodeToString(generation[:])), "evidence_sha256": query.String(evidence), "transitioned_at": query.DateTime(instant)}
			if sequence > 1 {
				values["previous"] = query.Integer(previous)
			}
			var assignments []query.Assignment
			var key query.FieldRef
			for _, field := range model.Fields {
				kind := query.FieldString
				switch field.Kind {
				case ir.FieldAuto, ir.FieldInteger, ir.FieldForeignKey:
					kind = query.FieldInteger
				case ir.FieldDateTime:
					kind = query.FieldDateTime
				}
				ref := query.NewFieldRef(field.Name, field.Column, kind, field.Nullable)
				if field.PrimaryKey {
					key = ref
					continue
				}
				assignments = append(assignments, query.NewAssignment(ref, values[field.Name]))
			}
			previous, err = backend.Insert(t.Context(), query.NewInsertPlanReturningKey(model.DBTable, assignments, key))
			if err != nil {
				t.Fatal(err)
			}
			values["id"] = query.Integer(previous)
			originals = append(originals, values)
		}
		current, _, err := definition.Load(sources...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), current, migrations.LatestLifecycleRequest()); err != nil {
			t.Fatal(err)
		}
		reader := reopen(t)
		rows, err := models.TransitionObjects.Using(reader).OrderBy(models.TransitionFields.Sequence.Asc()).All(t.Context())
		if err != nil || len(rows) != len(originals) {
			t.Fatal("filled history changed cardinality", err)
		}
		for i, row := range rows {
			values, err := models.TransitionObjects.ModelValues(row)
			if err != nil {
				t.Fatal(err)
			}
			for field, want := range originals[i] {
				if !values[field].Equal(want) {
					t.Fatal("migration changed original history field", field)
				}
			}
			if row.MappingCount != 0 || row.MappingSHA256 != "" {
				t.Fatal("upgrade fabricated a mapping")
			}
		}
		last, err := domain.Lookup(t.Context(), reader, operation)
		if err != nil || last.Sequence() != 2 {
			t.Fatal("upgraded ownership did not reopen", err)
		}
		next := mappedTransition(t, reader, last, mappingRows())
		if got, err := db.ReadSnapshotResult(t.Context(), reader, domain.InspectIn); err != nil || got.OperationID() != next.OperationID() {
			t.Fatal("new mapping did not coexist with old history", err)
		}
	})
}

func mappingLostConstraint(t *testing.T, open Open) {
	t.Run("mapping_lost_unique_constraint", func(t *testing.T) {
		backend, _ := open(t)
		schema, err := modeldef.Schema()
		if err != nil {
			t.Fatal(err)
		}
		var parent, child ir.Model
		for _, model := range schema.Models {
			if model.Name == "transition" {
				parent = model
			} else if model.Name == "mapping" {
				child = model
			}
		}
		for i := range child.Fields {
			if child.Fields[i].Name == "transition" {
				child.Fields[i].Unique = false
				child.Fields[i].Relation.Cardinality = ir.RelationManyToOne
			}
		}
		migration := migrations.Migration{App: schema.AppLabel, Name: "0001_damaged_mapping", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: schema.AppLabel, Model: parent}, migrations.CreateModel{AppLabel: schema.AppLabel, Model: child}}}
		encoded, err := definition.Encode(definition.Producer{Name: "deliberately-damaged-mapping", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(definition.Source{SourceID: "damaged-mapping", Document: encoded})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
			t.Fatal(err)
		}
		first := initialize(t, backend)
		receipt := mappedTransition(t, backend, first, mappingRows())
		header, err := models.TransitionObjects.Using(backend).Filter(models.TransitionFields.OperationID.Exact(receipt.OperationID())).Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		row, err := models.MappingObjects.Using(backend).Filter(models.MappingFields.TransitionID.Exact(header.ID)).Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := models.MappingObjects.Create(t.Context(), backend, models.NewMappingCreate(header.ID, row.Payload)); err != nil {
			t.Fatal(err)
		}
		if rows, err := db.ReadSnapshotResult(t.Context(), backend, func(ctx context.Context, scope db.Queryer) ([]domain.Mapping, error) {
			return domain.LookupMappingsIn(ctx, scope, receipt)
		}); err == nil || rows != nil {
			t.Fatal("duplicate mapping link published rows")
		}
		if got, err := db.ReadSnapshotResult(t.Context(), backend, domain.InspectIn); err == nil || got.Sequence() != 0 {
			t.Fatal("lost mapping uniqueness escaped full inspection")
		}
	})
}
