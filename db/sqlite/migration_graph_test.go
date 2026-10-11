package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/migrationgraphtest"
)

func TestSQLiteHistoricalRelationGraphProduct(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "graph.sqlite3")
	migrationgraphtest.Run(t, ctx, func() migrationgraphtest.Binding {
		backend, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		backend.database.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = backend.Close() })
		return migrationgraphtest.Binding{Backend: backend, Database: backend.database, Renderer: NewMigrationSQLRenderer(), Close: backend.Close,
			Table: func(name string) string { return `"main"."` + name + `"` }}
	})
}

func TestSQLiteMigrationFieldPositionProduct(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "positions.sqlite3")
	migrationgraphtest.RunFieldPositions(t, ctx, func() migrationgraphtest.Binding {
		backend, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		backend.database.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = backend.Close() })
		return migrationgraphtest.Binding{Backend: backend, Database: backend.database, Renderer: NewMigrationSQLRenderer(), Close: backend.Close, Table: func(name string) string { return `"main"."` + name + `"` }}
	})
}

func TestSQLiteAutomaticRelationGraphProduct(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	migrationgraphtest.RunAutodetection(t, ctx, func(caseName string) migrationgraphtest.Binding {
		backend, err := Open(ctx, filepath.Join(root, caseName+".sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		backend.database.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = backend.Close() })
		return migrationgraphtest.Binding{Backend: backend, Database: backend.database, Renderer: NewMigrationSQLRenderer(), Close: backend.Close, Table: func(name string) string { return `"main"."` + name + `"` }}
	})
}

func TestSQLiteHistoricalRelationGraphSealsTransitiveAuthority(t *testing.T) {
	t.Parallel()
	transition, intent := migrationgraphtest.TransitiveIntent(t)
	seal, err := validateAndSealSQLiteRelationIntent(transition, intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.Operations[0].RelatedModels[0].Model.Fields[1].Column = "caller_mutation"
	if err := verifySQLiteRelationIntentSeal(&seal); err != nil {
		t.Fatalf("transitive caller alias: %v", err)
	}
	seal.intent.Operations[0].RelatedModels[0].Model.Fields[1].Column = "private_mutation"
	if err := verifySQLiteRelationIntentSeal(&seal); err == nil {
		t.Fatal("seal omitted transitive model metadata")
	}
	for _, reserved := range []bool{false, true} {
		transition, input := migrationgraphtest.TransitiveIntent(t)
		if reserved {
			input.Operations[0].RelatedModels[0].Model.DBTable = "godj_migrations"
		} else {
			input.Operations[0].RelatedModels = nil
		}
		if _, err := validateAndSealSQLiteRelationIntent(transition, input); err == nil {
			t.Fatal("accepted missing or reserved transitive authority")
		}
	}
}
