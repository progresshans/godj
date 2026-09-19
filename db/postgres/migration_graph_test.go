package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/migrationgraphtest"
)

func TestPostgresHistoricalRelationGraphProduct(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	schema := postgresMigrationIntegrationSchema(t, ctx, databaseURL)
	migrationgraphtest.Run(t, ctx, func() migrationgraphtest.Binding {
		backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
		return migrationgraphtest.Binding{Backend: backend, Database: backend.database, Renderer: NewMigrationSQLRenderer(MigrationSQLConfig{Schema: schema}), Close: backend.Close,
			Table: func(name string) string { return `"` + schema + `"."` + name + `"` }}
	})
}

func TestPostgresMigrationFieldPositionProduct(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	schema := postgresMigrationIntegrationSchema(t, ctx, databaseURL)
	migrationgraphtest.RunFieldPositions(t, ctx, func() migrationgraphtest.Binding {
		backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
		return migrationgraphtest.Binding{Backend: backend, Database: backend.database, Renderer: NewMigrationSQLRenderer(MigrationSQLConfig{Schema: schema}), Close: backend.Close, Table: func(name string) string { return `"` + schema + `"."` + name + `"` }}
	})
}

func TestPostgresAutomaticRelationGraphProduct(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	schemas := make(map[string]string)
	migrationgraphtest.RunAutodetection(t, ctx, func(caseName string) migrationgraphtest.Binding {
		schema, exists := schemas[caseName]
		if !exists {
			schema = postgresMigrationIntegrationSchema(t, ctx, databaseURL)
			schemas[caseName] = schema
		}
		backend := openPostgresMigrationIntegrationBackend(t, ctx, databaseURL, schema)
		return migrationgraphtest.Binding{Backend: backend, Database: backend.database, Renderer: NewMigrationSQLRenderer(MigrationSQLConfig{Schema: schema}), Close: backend.Close, Table: func(name string) string { return `"` + schema + `"."` + name + `"` }}
	})
}

func TestPostgresHistoricalRelationGraphSealsTransitiveAuthority(t *testing.T) {
	t.Parallel()
	transition, intent := migrationgraphtest.TransitiveIntent(t)
	schema, err := newPostgresMigrationSchema(transition, intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.Operations[0].RelatedModels[0].Model.Fields[1].Column = "caller_mutation"
	if err := schema.verifySeal(); err != nil {
		t.Fatalf("transitive caller alias: %v", err)
	}
	schema.intent.Operations[0].RelatedModels[0].Model.Fields[1].Column = "private_mutation"
	assertPostgresMigrationIntegrity(t, schema.verifySeal())
	for _, reserved := range []bool{false, true} {
		transition, input := migrationgraphtest.TransitiveIntent(t)
		if reserved {
			input.Operations[0].RelatedModels[0].Model.DBTable = "godj_migrations"
		} else {
			input.Operations[0].RelatedModels = nil
		}
		if _, err := newPostgresMigrationSchema(transition, input); err == nil {
			t.Fatal("accepted missing or reserved transitive authority")
		}
	}
}
