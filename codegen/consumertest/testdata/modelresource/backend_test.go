package consumer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/directory"
	"example.com/godj-project-bundle/inventory"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
)

type resourceBackend interface {
	db.Session
	db.Atomic
	db.RelationAtomic
	migrationbackend.RevisionFencedBackend
	Close() error
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func withResourceBackends(t *testing.T, run func(*testing.T, resourceBackend)) {
	t.Run("sqlite", func(t *testing.T) {
		backend, err := sqlite.Open(t.Context(), "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "resources.db")))
		check(t, err)
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateResources(t, backend)
		run(t, backend)
	})
	url := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if url == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), url)
		check(t, err)
		schema := fmt.Sprintf("godj_resource_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
		_, err = connection.Exec(t.Context(), "CREATE SCHEMA "+quoted)
		check(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
			check(t, err)
			check(t, connection.Close(ctx))
		})
		backend, err := postgres.Open(t.Context(), postgres.Config{URL: url, Schema: schema})
		check(t, err)
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateResources(t, backend)
		run(t, backend)
	})
}

func migrateResources(t *testing.T, backend resourceBackend) {
	t.Helper()
	first := migrations.Migration{App: "directory", Name: "0001_initial"}
	for _, model := range directory.GoDjRelationSchema().Models {
		first.Operations = append(first.Operations, migrations.CreateModel{AppLabel: first.App, Model: model})
	}
	second := migrations.Migration{App: "inventory", Name: "0001_initial", Dependencies: []migrations.MigrationKey{first.Key()}}
	for _, model := range inventory.GoDjRelationSchema().Models {
		second.Operations = append(second.Operations, migrations.CreateModel{AppLabel: second.App, Model: model})
	}
	var sources []definition.Source
	for _, migration := range []migrations.Migration{first, second} {
		wire, err := definition.Encode(definition.Producer{Name: "resource-consumer", Version: "1"}, migration)
		check(t, err)
		sources = append(sources, definition.Source{SourceID: migration.App, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	check(t, err)
	_, err = (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest())
	check(t, err)
}
