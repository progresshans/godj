package definitionload_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

const currentCreateDocument = `{"format_version":1,"producer":{"name":"godj-conformance","version":"current"},"migration":{"app":"alpha","name":"0001_initial","dependencies":[],"operations":[{"kind":"create_model","app_label":"alpha","model":{"name":"widget","go_name":"Widget","db_table":"alpha_widget","fields":[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null}]}}]}}`

const currentTailDocument = `{"format_version":1,"producer":{"name":"godj-conformance","version":"current"},"migration":{"app":"alpha","name":"0002_title","dependencies":[{"app":"alpha","name":"0001_initial"}],"operations":[{"kind":"add_field","app_label":"alpha","model_name":"widget","field":{"name":"title","go_name":"Title","column":"title","kind":"char","primary_key":false,"nullable":false,"max_length":200,"default":{"kind":"string","string":"untitled"}}}]}}`

func TestCurrentDefinitionLoadIsCanonicalOwnedAndOrderIndependent(t *testing.T) {
	t.Parallel()

	first, firstReport, err := definition.Load(
		definition.Source{SourceID: "z-tail", Document: []byte(currentTailDocument)},
		definition.Source{SourceID: "a-root", Document: []byte(currentCreateDocument)},
	)
	if err != nil {
		t.Fatalf("Load(current): %v", err)
	}
	second, secondReport, err := definition.Load(
		definition.Source{SourceID: "renamed-root", Document: []byte(currentCreateDocument)},
		definition.Source{SourceID: "renamed-tail", Document: []byte(currentTailDocument)},
	)
	if err != nil {
		t.Fatalf("Load(permuted): %v", err)
	}
	if first.Digest() == "" || first.Digest() != second.Digest() {
		t.Fatalf("canonical digest = %q/%q", first.Digest(), second.Digest())
	}
	if firstReport.DefinitionsPublished != 2 || firstReport.DefinitionSetsPublished != 1 ||
		secondReport.DefinitionsPublished != 2 || secondReport.DefinitionSetsPublished != 1 {
		t.Fatalf("publication reports = first:%+v second:%+v", firstReport, secondReport)
	}

	definitions := first.Definitions()
	sources := first.Sources()
	definitions[0].Name = "mutated"
	sources[0].SourceID = "mutated"
	if first.Definitions()[0].Name == "mutated" || first.Sources()[0].SourceID == "mutated" {
		t.Fatal("loaded definition set retained an accessor mutation")
	}
}

func TestCurrentDefinitionLoadRejectsLegacyAndUnknownFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		document string
		code     definition.ErrorCode
		pointer  string
	}{
		{
			name:     "legacy compatibility envelope",
			document: `{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"producer":{"name":"legacy","version":"1"},"migration":{"app":"alpha","name":"0001_initial","dependencies":[],"operations":[]}}`,
			code:     definition.CodeInvalidDocument,
			pointer:  "/compatibility",
		},
		{
			name:     "unknown current format",
			document: `{"format_version":2,"producer":{"name":"future","version":"1"},"migration":{"app":"alpha","name":"0001_initial","dependencies":[],"operations":[]}}`,
			code:     definition.CodeDefinitionFormatIncompatible,
			pointer:  "/format_version",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, report, err := definition.Load(definition.Source{SourceID: "candidate", Document: []byte(test.document)})
			var sourceError *definition.Error
			if !errors.As(err, &sourceError) || sourceError.Code != test.code {
				t.Fatalf("Load() error = %#v, want %s", err, test.code)
			}
			if sourceError.Context().JSONPointer != test.pointer {
				t.Fatalf("error pointer = %q, want %q", sourceError.Context().JSONPointer, test.pointer)
			}
			if report.DefinitionsPublished != 0 || report.DefinitionSetsPublished != 0 {
				t.Fatalf("failed load published state: %+v", report)
			}
		})
	}
}

func TestCurrentDefinitionLoadIsTheOnlyLifecycleAuthority(t *testing.T) {
	t.Parallel()

	loadType := reflect.TypeOf(definition.Load)
	loadedType := reflect.TypeOf(migrations.LoadedDefinitionSet{})
	if loadType.NumOut() != 3 || loadType.Out(0) != loadedType {
		t.Fatalf("definition.Load result = %v, want %v", loadType.Out(0), loadedType)
	}
	migrateType := reflect.TypeOf(migrations.Executor{}.Migrate)
	if migrateType.NumIn() != 3 || migrateType.In(1) != loadedType {
		t.Fatalf("Executor.Migrate input = %v, want %v", migrateType.In(1), loadedType)
	}

	var zero migrations.LoadedDefinitionSet
	_, err := (migrations.Executor{}).Migrate(context.Background(), zero, migrations.LatestLifecycleRequest())
	var migrationError *migrations.Error
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryState ||
		migrationError.Code != migrations.CodeInvalidState {
		t.Fatalf("zero loaded set error = %#v", err)
	}
}
