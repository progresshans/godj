package definition_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestCurrentDefinitionPublicBoundary(t *testing.T) {
	if definition.DefinitionFormatVersion != 1 || ir.CurrentFormatVersion != 1 {
		t.Fatalf("current formats = definition:%d schema:%d", definition.DefinitionFormatVersion, ir.CurrentFormatVersion)
	}
	if definition.EmptySetDigest != "sha256:1412c48d7da2299b6f2be7a614c5bb9ce510027328f6baed72ae05cbecc9b494" {
		t.Fatalf("empty digest = %q", definition.EmptySetDigest)
	}

	loaded, report, err := definition.Load(definition.Source{
		SourceID: "external-current",
		Document: []byte(`{"format_version":1,"producer":{"name":"external-test","version":"1"},"migration":{"app":"alpha","name":"0001_initial","dependencies":[],"operations":[{"kind":"create_model","app_label":"alpha","model":{"name":"entry","go_name":"Entry","db_table":"alpha_entry","fields":[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null}]}}]}}`),
	})
	if err != nil {
		t.Fatalf("Load(current): %v", err)
	}
	var exactType migrations.LoadedDefinitionSet = loaded
	if exactType.Digest() == "" || report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("publication = digest:%q report:%+v", exactType.Digest(), report)
	}
	definitions := exactType.Definitions()
	definitions[0].Name = "mutated"
	if exactType.Definitions()[0].Name != "0001_initial" {
		t.Fatal("public definition snapshot retained caller mutation")
	}
}

func TestUnknownDefinitionFormatAndZeroLoadedSetFailClosed(t *testing.T) {
	_, report, err := definition.Load(definition.Source{
		SourceID: "unknown-format",
		Document: []byte(`{"format_version":2,"producer":{"name":"external-test","version":"1"},"migration":{"app":"alpha","name":"0001_initial","dependencies":[],"operations":[]}}`),
	})
	var sourceError *definition.Error
	if !errors.As(err, &sourceError) || sourceError.Code != definition.CodeDefinitionFormatIncompatible {
		t.Fatalf("unknown format error = %#v", err)
	}
	failureContext := sourceError.Context()
	if failureContext.Stage != "format" || failureContext.JSONPointer != "/format_version" || failureContext.Reason != "format_version" {
		t.Fatalf("unknown format context = %+v", failureContext)
	}
	if report.DefinitionsPublished != 0 || report.DefinitionSetsPublished != 0 {
		t.Fatalf("unknown format published state: %+v", report)
	}

	var zero migrations.LoadedDefinitionSet
	_, err = (migrations.Executor{}).Migrate(context.Background(), zero, migrations.LatestLifecycleRequest())
	var migrationError *migrations.Error
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryState || migrationError.Code != migrations.CodeInvalidState {
		t.Fatalf("zero loaded set error = %#v", err)
	}
}
