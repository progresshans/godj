package systemstate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestInitialDefinitionSourceIsFreshStableAndPure(t *testing.T) {
	wantKey := migrations.MigrationKey{App: "godj_system", Name: "0001_initial"}
	if got := InitialMigrationKey(); got != wantKey {
		t.Fatalf("InitialMigrationKey() = %+v, want %+v", got, wantKey)
	}

	first := InitialDefinitionSource()
	second := InitialDefinitionSource()
	if first.SourceID != "systemstate/godj_system.0001_initial" || second.SourceID != first.SourceID {
		t.Fatalf("InitialDefinitionSource() IDs = %q/%q", first.SourceID, second.SourceID)
	}
	if len(first.Document) == 0 || !bytes.Equal(first.Document, second.Document) {
		t.Fatalf("InitialDefinitionSource() documents are empty or unstable")
	}
	first.Document[0] ^= 0xff
	third := InitialDefinitionSource()
	if bytes.Equal(first.Document, second.Document) || !bytes.Equal(second.Document, third.Document) {
		t.Fatalf("InitialDefinitionSource() retained a caller document alias")
	}

	databasePath := filepath.Join(t.TempDir(), "definition-construction-must-not-create.sqlite3")
	loaded, report, err := migrationdefinition.Load(second)
	if err != nil {
		t.Fatalf("definition.Load(InitialDefinitionSource()): %v", err)
	}
	if loaded.Digest() == "" || report.DocumentsReceived != 1 || report.DefinitionsPublished != 1 {
		t.Fatalf("definition load result = digest %q/report %+v", loaded.Digest(), report)
	}
	if _, err := os.Stat(databasePath); !os.IsNotExist(err) {
		t.Fatalf("constructing/loading the embedded definition touched database path %q: %v", databasePath, err)
	}

	backend, err := sqlite.OpenMemory(context.Background(), "systemstate-definition-purity-"+t.Name())
	if err != nil {
		t.Fatalf("sqlite.OpenMemory(): %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close SQLite purity backend: %v", err)
		}
	})
	before := backend.QueryCount()
	if _, _, err := migrationdefinition.Load(InitialDefinitionSource()); err != nil {
		t.Fatalf("definition.Load() with idle backend present: %v", err)
	}
	if after := backend.QueryCount(); after != before {
		t.Fatalf("constructing/loading definition database query count = %d -> %d, want unchanged", before, after)
	}
	history, err := backend.ReadAppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("ReadAppliedMigrations() after pure load: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("applied history after pure definition load = %+v, want empty", history)
	}
	rows, queryErr := backend.Query(
		context.Background(),
		query.NewPlan(credentialTableName, []query.FieldRef{
			query.NewFieldRef("id", "id", query.FieldInteger, false),
		}),
	)
	if rows != nil {
		_ = rows.Close()
	}
	var classified *query.Error
	if !errors.As(queryErr, &classified) || classified.Code != query.CodeMissingTable {
		t.Fatalf("credential table after pure definition load error = %v, want missing_table", queryErr)
	}
}

func TestInitialDefinitionIsDeterministicPortableCurrentScalarIR(t *testing.T) {
	first, firstReport := loadInitialDefinition(t)
	second, secondReport := loadInitialDefinition(t)
	if first.Digest() != second.Digest() || first.Digest() != initialDefinitionDigest {
		t.Fatalf("initial definition digests = %q/%q, want %q", first.Digest(), second.Digest(), initialDefinitionDigest)
	}
	if !reflect.DeepEqual(firstReport, secondReport) {
		t.Fatalf("initial definition reports differ: %+v/%+v", firstReport, secondReport)
	}
	if firstReport.DocumentsReceived != 1 || firstReport.HeadersValidated != 1 || firstReport.OperationsDecoded != 3 ||
		firstReport.PlannerConstruction != 1 || firstReport.DefinitionsPublished != 1 ||
		firstReport.DefinitionSetsPublished != 1 {
		t.Fatalf("initial definition report = %+v", firstReport)
	}

	definitions := first.Definitions()
	if len(definitions) != 1 || definitions[0].Key() != InitialMigrationKey() || len(definitions[0].Dependencies) != 0 {
		t.Fatalf("initial definitions = %+v", definitions)
	}
	wantModels := initialDefinitionModels()
	if len(definitions[0].Operations) != len(wantModels) {
		t.Fatalf("initial operation count = %d, want %d", len(definitions[0].Operations), len(wantModels))
	}
	gotModels := make([]ir.Model, len(definitions[0].Operations))
	for index, operation := range definitions[0].Operations {
		create, ok := operation.(migrations.CreateModel)
		if !ok {
			t.Fatalf("initial operation %d type = %T, want migrations.CreateModel", index, operation)
		}
		if create.AppLabel != initialMigrationApp {
			t.Fatalf("initial operation %d app = %q, want %q", index, create.AppLabel, initialMigrationApp)
		}
		gotModels[index] = create.Model
		for fieldIndex, field := range create.Model.Fields {
			if field.Kind != ir.FieldAuto && field.Kind != ir.FieldChar && field.Kind != ir.FieldBoolean {
				t.Fatalf("model %s field %d kind = %q, want current scalar IR", create.Model.Name, fieldIndex, field.Kind)
			}
			if field.Relation != nil || field.Default != nil {
				t.Fatalf("model %s field %s has backend-specific relation/default = %+v/%+v", create.Model.Name, field.Name, field.Relation, field.Default)
			}
		}
	}
	if !reflect.DeepEqual(gotModels, wantModels) {
		t.Fatalf("initial models =\n%#v\nwant\n%#v", gotModels, wantModels)
	}

	normalized, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      initialMigrationApp,
		Models:        gotModels,
	})
	if err != nil {
		t.Fatalf("normalize initial system schema: %v", err)
	}
	if !reflect.DeepEqual(normalized.Models, gotModels) {
		t.Fatalf("initial models are not already normalized: got %#v", normalized.Models)
	}

	gotModels[0].Fields[1].Name = "caller_mutation"
	fresh := first.Definitions()
	freshCreate := fresh[0].Operations[0].(migrations.CreateModel)
	if freshCreate.Model.Fields[1].Name != "principal_id" {
		t.Fatalf("LoadedDefinitionSet.Definitions() retained caller alias: %+v", freshCreate.Model.Fields[1])
	}
}

func loadInitialDefinition(t *testing.T) (migrations.LoadedDefinitionSet, migrationdefinition.LoadReport) {
	t.Helper()
	loaded, report, err := migrationdefinition.Load(InitialDefinitionSource())
	if err != nil {
		t.Fatalf("definition.Load(InitialDefinitionSource()): %v", err)
	}
	return loaded, report
}

func initialDefinitionModels() []ir.Model {
	return []ir.Model{
		{
			Name: credentialModelName, GoName: "Credential", DBTable: credentialTableName,
			Fields: []ir.Field{
				initialAutoField(),
				initialCharField(credentialPrincipalIDColumn, "PrincipalID", credentialPrincipalIDMaxLength),
				initialCharField(credentialUsernameColumn, "Username", credentialUsernameMaxLength),
				initialCharField(credentialEncodedPasswordColumn, "EncodedPassword", credentialEncodedPasswordMaxLength),
				{Name: credentialActiveColumn, GoName: "Active", Column: credentialActiveColumn, Kind: ir.FieldBoolean},
				initialCharField(credentialPermissionsColumn, "Permissions", credentialPermissionsMaxLength),
				initialCharField(credentialDefinitionDigestColumn, "DefinitionDigest", credentialDefinitionDigestMaxLength),
			},
		},
		{
			Name: sessionModelName, GoName: "Session", DBTable: sessionTableName,
			Fields: []ir.Field{
				initialAutoField(),
				initialCharField(sessionDigestColumn, "Digest", sessionDigestMaxLength),
				initialCharField(sessionPayloadColumn, "Payload", sessionPayloadMaxLength),
			},
		},
		{
			Name: auditModelName, GoName: "Audit", DBTable: auditTableName,
			Fields: []ir.Field{
				initialAutoField(),
				initialCharField(auditActorIDColumn, "ActorID", auditActorIDMaxLength),
				initialCharField(auditModelColumn, "Model", auditModelMaxLength),
				initialCharField(auditObjectIDColumn, "ObjectID", auditObjectIDMaxLength),
				initialCharField(auditActionColumn, "Action", auditActionMaxLength),
				initialCharField(auditChangedFieldsColumn, "ChangedFields", auditChangedFieldsMaxLength),
				initialCharField(auditDisplayLabelColumn, "DisplayLabel", auditDisplayLabelMaxLength),
			},
		},
	}
}

func initialAutoField() ir.Field {
	return ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}
}

func initialCharField(name, goName string, maxLength int) ir.Field {
	return ir.Field{Name: name, GoName: goName, Column: name, Kind: ir.FieldChar, MaxLength: maxLength}
}
