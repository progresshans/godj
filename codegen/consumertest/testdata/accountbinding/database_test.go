package consumer

import (
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/settings/database"
)

func accountDatabase(t *testing.T, schemas ...ir.Schema) database.Backend {
	t.Helper()
	selected, err := database.SQLite(filepath.Join(t.TempDir(), "accounts.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	backend, err := database.Open(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	var sources []definition.Source
	var previous *migrations.MigrationKey
	for _, app := range schemas {
		migration := migrations.Migration{App: app.AppLabel, Name: "0001_initial"}
		if previous != nil {
			migration.Dependencies = []migrations.MigrationKey{*previous}
		}
		for _, model := range app.Models {
			migration.Operations = append(migration.Operations, migrations.CreateModel{AppLabel: app.AppLabel, Model: model})
		}
		encoded, err := definition.Encode(definition.Producer{Name: "account-binding-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: app.AppLabel + "/0001_initial", Document: encoded})
		key := migrations.MigrationKey{App: app.AppLabel, Name: "0001_initial"}
		previous = &key
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	return backend
}
