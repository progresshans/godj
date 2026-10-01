package main

import (
	"encoding/json"
	"fmt"

	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
)

const (
	productApp   = "postgresproduct"
	productTable = "postgresproduct_entry"
)

var (
	initialMigration = migrations.MigrationKey{App: productApp, Name: "0001_initial"}
	summaryMigration = migrations.MigrationKey{App: productApp, Name: "0002_summary"}
)

func loadProductDefinitions() (migrations.LoadedDefinitionSet, error) {
	initial, err := productDefinitionSource(
		initialMigration,
		nil,
		map[string]any{
			"kind":      "create_model",
			"app_label": productApp,
			"model": map[string]any{
				"name":     "entry",
				"go_name":  "Entry",
				"db_table": productTable,
				"fields": []map[string]any{
					productAutoField(),
					productCharField("title", "Title", "title", 120, false),
				},
			},
		},
	)
	if err != nil {
		return migrations.LoadedDefinitionSet{}, err
	}
	summary, err := productDefinitionSource(
		summaryMigration,
		[]migrations.MigrationKey{initialMigration},
		map[string]any{
			"kind":       "add_field",
			"app_label":  productApp,
			"model_name": "entry",
			"field":      productCharField("summary", "Summary", "summary", 200, true),
		},
	)
	if err != nil {
		return migrations.LoadedDefinitionSet{}, err
	}
	loaded, report, err := migrationdefinition.Load(initial, summary)
	if err != nil {
		return migrations.LoadedDefinitionSet{}, fmt.Errorf("load PostgreSQL product definitions: %w", err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 2 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		return migrations.LoadedDefinitionSet{}, fmt.Errorf("unexpected PostgreSQL product definition report: %+v", report)
	}
	return loaded, nil
}

func productDefinitionSource(
	key migrations.MigrationKey,
	dependencies []migrations.MigrationKey,
	operations ...map[string]any,
) (migrationdefinition.Source, error) {
	encodedDependencies := make([]map[string]string, len(dependencies))
	for index := range dependencies {
		encodedDependencies[index] = map[string]string{
			"app":  dependencies[index].App,
			"name": dependencies[index].Name,
		}
	}
	document, err := json.Marshal(map[string]any{
		"format_version": migrationdefinition.DefinitionFormatVersion,
		"producer": map[string]string{
			"name":    "postgres-product-runner",
			"version": "1",
		},
		"migration": map[string]any{
			"app":          key.App,
			"name":         key.Name,
			"dependencies": encodedDependencies,
			"operations":   operations,
		},
	})
	if err != nil {
		return migrationdefinition.Source{}, fmt.Errorf("encode definition %s.%s: %w", key.App, key.Name, err)
	}
	return migrationdefinition.Source{
		SourceID: key.App + "/" + key.Name + ".godj.json",
		Document: document,
	}, nil
}

func productAutoField() map[string]any {
	return map[string]any{
		"name":        "id",
		"go_name":     "ID",
		"column":      "id",
		"kind":        "auto",
		"primary_key": true,
		"nullable":    false,
		"max_length":  0,
		"default":     nil,
	}
}

func productCharField(name, goName, column string, maximumLength int, nullable bool) map[string]any {
	return map[string]any{
		"name":        name,
		"go_name":     goName,
		"column":      column,
		"kind":        "char",
		"primary_key": false,
		"nullable":    nullable,
		"max_length":  maximumLength,
		"default":     nil,
	}
}
