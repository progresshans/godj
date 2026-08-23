package postgres

import (
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestAssertPostgresMigrationModelCatalogAcceptsExactShape(t *testing.T) {
	t.Parallel()

	author := postgresMigrationTestAuthorModel()
	post := postgresMigrationTestPostModel(true)
	target := postgresMigrationTestTarget(post.Fields[len(post.Fields)-1], author)
	catalog := postgresMigrationTestCatalog(t, "product_schema", post, []migrationbackend.MigrationTarget{target})
	if err := assertPostgresMigrationModelCatalog(catalog, "product_schema", post, []migrationbackend.MigrationTarget{target}); err != nil {
		t.Fatalf("assertPostgresMigrationModelCatalog() error = %v", err)
	}
}

func TestAssertPostgresMigrationModelCatalogRejectsPhysicalDriftAsCapability(t *testing.T) {
	t.Parallel()

	model := postgresMigrationTestAuthorModel()
	exact := postgresMigrationTestCatalog(t, "product_schema", model, nil)
	tests := []struct {
		name   string
		mutate func(*postgresMigrationTableCatalog)
	}{
		{name: "temporary table", mutate: func(value *postgresMigrationTableCatalog) { value.persistence = "t" }},
		{name: "custom collation", mutate: func(value *postgresMigrationTableCatalog) { value.columns[1].defaultCollation = false }},
		{name: "persistent default", mutate: func(value *postgresMigrationTableCatalog) { value.columns[1].hasDefault = true }},
		{name: "identity sequence name", mutate: func(value *postgresMigrationTableCatalog) { value.sequences[0].name = "server_default_seq" }},
		{name: "identity sequence increment", mutate: func(value *postgresMigrationTableCatalog) { value.sequences[0].increment = 2 }},
		{name: "identity sequence cycle", mutate: func(value *postgresMigrationTableCatalog) { value.sequences[0].cycle = true }},
		{name: "identity sequence ownership", mutate: func(value *postgresMigrationTableCatalog) { value.sequences[0].ownerAttributeNumber++ }},
		{name: "unvalidated constraint", mutate: func(value *postgresMigrationTableCatalog) { value.constraints[0].validated = false }},
		{name: "renamed primary key", mutate: func(value *postgresMigrationTableCatalog) { value.constraints[0].name = "authors_author_pkey" }},
		{name: "extra index", mutate: func(value *postgresMigrationTableCatalog) { value.indexes = append(value.indexes, value.indexes[0]) }},
		{name: "user trigger", mutate: func(value *postgresMigrationTableCatalog) { value.userTriggers = 1 }},
		{name: "row security", mutate: func(value *postgresMigrationTableCatalog) { value.rowSecurity = true }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			catalog := clonePostgresMigrationTestCatalog(exact)
			test.mutate(&catalog)
			err := assertPostgresMigrationModelCatalog(catalog, "product_schema", model, nil)
			if err == nil || !migrationbackend.IsCapabilityError(err) {
				t.Fatalf("catalog drift error = %T %v, want capability", err, err)
			}
			if migrationbackend.IsRevisionFenceError(err) {
				t.Fatalf("physical drift leaked through integrity taxonomy: %v", err)
			}
		})
	}
}

func TestAssertPostgresMigrationForeignKeyCatalogRequiresExactValidatedAction(t *testing.T) {
	t.Parallel()

	author := postgresMigrationTestAuthorModel()
	post := postgresMigrationTestPostModel(true)
	target := postgresMigrationTestTarget(post.Fields[len(post.Fields)-1], author)
	exact := postgresMigrationTestCatalog(t, "product_schema", post, []migrationbackend.MigrationTarget{target})

	for _, mutation := range []func(*postgresMigrationConstraintCatalog){
		func(value *postgresMigrationConstraintCatalog) { value.validated = false },
		func(value *postgresMigrationConstraintCatalog) { value.deferrable = true },
		func(value *postgresMigrationConstraintCatalog) { value.deleteAction = "c" },
		func(value *postgresMigrationConstraintCatalog) { value.targetSchema = "other_schema" },
		func(value *postgresMigrationConstraintCatalog) { value.targetColumn = "other_id" },
		func(value *postgresMigrationConstraintCatalog) { value.enabledInternal = 2 },
	} {
		catalog := clonePostgresMigrationTestCatalog(exact)
		mutation(&catalog.constraints[1])
		if err := assertPostgresMigrationModelCatalog(catalog, "product_schema", post, []migrationbackend.MigrationTarget{target}); err == nil || !migrationbackend.IsCapabilityError(err) {
			t.Fatalf("foreign key drift error = %T %v, want capability", err, err)
		}
	}
}

func TestAssertPostgresMigrationModelCatalogAllowsDroppedAttributeGaps(t *testing.T) {
	t.Parallel()

	model := postgresMigrationTestPostModel(true)
	author := postgresMigrationTestAuthorModel()
	target := postgresMigrationTestTarget(model.Fields[len(model.Fields)-1], author)
	catalog := postgresMigrationTestCatalog(t, "product_schema", model, []migrationbackend.MigrationTarget{target})
	// A prior native DROP COLUMN at attnum=3 leaves an invisible tombstone.
	// Current logical fields remain ordered but later additions receive new,
	// non-contiguous physical attribute numbers.
	catalog.columns[2].attributeNumber = 4
	catalog.columns[3].attributeNumber = 5
	catalog.attributeSlots = 5
	catalog.constraints[1].sourceAttributeNumber = 5
	if err := assertPostgresMigrationModelCatalog(catalog, "product_schema", model, []migrationbackend.MigrationTarget{target}); err != nil {
		t.Fatalf("catalog with native DROP COLUMN tombstone = %v", err)
	}
}

func TestValidatePostgresMigrationAttributeCapacityCountsDroppedSlots(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		physical      int
		pendingAdds   int
		wantError     bool
		wantIntegrity bool
	}{
		{name: "last_slot", physical: postgresMigrationMaxAttributeSlots - 1, pendingAdds: 1},
		{name: "tombstone_exhausted", physical: postgresMigrationMaxAttributeSlots, pendingAdds: 1, wantError: true},
		{name: "multiple_adds_overflow", physical: postgresMigrationMaxAttributeSlots - 1, pendingAdds: 2, wantError: true},
		{name: "negative", physical: -1, pendingAdds: 1, wantError: true, wantIntegrity: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validatePostgresMigrationAttributeCapacity("entries", test.physical, test.pendingAdds)
			if !test.wantError {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if test.wantIntegrity {
				if !migrationbackend.IsRevisionFenceError(err) {
					t.Fatalf("negative capacity error = %T %v, want integrity", err, err)
				}
				return
			}
			if !migrationbackend.IsCapabilityError(err) {
				t.Fatalf("attribute capacity error = %T %v, want capability", err, err)
			}
		})
	}
}

func postgresMigrationTestCatalog(
	t *testing.T,
	namespace string,
	model ir.Model,
	targets []migrationbackend.MigrationTarget,
) postgresMigrationTableCatalog {
	t.Helper()
	catalog := postgresMigrationTableCatalog{
		oid: 100, attributeSlots: len(model.Fields),
		name: model.DBTable, kind: "r", persistence: "p", accessMethod: "heap", replicaIdentity: "d",
	}
	for index := range model.Fields {
		field := model.Fields[index]
		column := postgresMigrationColumnCatalog{
			attributeNumber:  index + 1,
			name:             field.Column,
			typeSchema:       "pg_catalog",
			typeModifier:     -1,
			defaultCollation: true,
		}
		switch field.Kind {
		case ir.FieldAuto:
			column.typeName = "int8"
			column.notNull = true
			column.identity = "d"
		case ir.FieldChar:
			column.typeName = "varchar"
			column.typeModifier = field.MaxLength + 4
			column.notNull = !field.Nullable
		case ir.FieldBoolean:
			column.typeName = "bool"
			column.notNull = true
		case ir.FieldForeignKey:
			column.typeName = "int8"
			column.notNull = !field.Nullable
		}
		catalog.columns = append(catalog.columns, column)
	}
	primaryKey, err := postgresMigrationPrimaryKey(model)
	if err != nil {
		t.Fatal(err)
	}
	primaryName, err := postgresPrimaryKeyConstraintName(model.DBTable)
	if err != nil {
		t.Fatal(err)
	}
	primaryAttribute := postgresMigrationCatalogAttributeNumber(catalog, primaryKey.Column)
	sequenceName, err := postgresIdentitySequenceName(model.DBTable, primaryKey.Column)
	if err != nil {
		t.Fatal(err)
	}
	catalog.sequences = append(catalog.sequences, postgresMigrationSequenceCatalog{
		oid: 150, schema: namespace, name: sequenceName, kind: "S", persistence: "p",
		typeSchema: "pg_catalog", typeName: "int8", start: 1, increment: 1,
		maximum: 9223372036854775807, minimum: 1, cache: 1,
		ownerTableOID: catalog.oid, ownerAttributeNumber: primaryAttribute,
		dependencyType: "i", tableDependencyCount: 1,
	})
	catalog.constraints = append(catalog.constraints, postgresMigrationConstraintCatalog{
		oid: 200, name: primaryName, kind: "p", validated: true,
		sourceKeyCount: 1, sourceAttributeNumber: primaryAttribute, indexOID: 300,
	})
	catalog.indexes = append(catalog.indexes, postgresMigrationIndexCatalog{
		oid: 300, name: primaryName, primary: true, unique: true, valid: true, ready: true, live: true,
		keyCount: 1, totalCount: 1, firstAttributeNumber: primaryAttribute,
	})
	for index := range targets {
		target := targets[index]
		name, err := postgresForeignKeyConstraintName(model.DBTable, target.SourceField.Column)
		if err != nil {
			t.Fatal(err)
		}
		catalog.constraints = append(catalog.constraints, postgresMigrationConstraintCatalog{
			oid: int64(201 + index), name: name, kind: "f", validated: true,
			sourceKeyCount: 1, sourceAttributeNumber: postgresMigrationFieldAttributeNumber(model, target.SourceField.Column),
			targetOID: 400, targetSchema: namespace, targetTable: target.TargetModel.DBTable,
			targetColumn: target.TargetKey.Column, targetKeyCount: 1,
			targetAttributeNumber: postgresMigrationFieldAttributeNumber(target.TargetModel, target.TargetKey.Column),
			updateAction:          "a", deleteAction: "a", matchType: "s", indexOID: 500,
			internalTriggers: 4, enabledInternal: 4,
		})
	}
	return catalog
}

func clonePostgresMigrationTestCatalog(value postgresMigrationTableCatalog) postgresMigrationTableCatalog {
	value.columns = append([]postgresMigrationColumnCatalog(nil), value.columns...)
	value.constraints = append([]postgresMigrationConstraintCatalog(nil), value.constraints...)
	value.indexes = append([]postgresMigrationIndexCatalog(nil), value.indexes...)
	value.sequences = append([]postgresMigrationSequenceCatalog(nil), value.sequences...)
	return value
}
