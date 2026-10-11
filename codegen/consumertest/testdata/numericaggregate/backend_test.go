package consumer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/records"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
)

type numericBackend interface {
	db.Session
	db.Atomic
	db.QueryUpdater
	migrationbackend.RevisionFencedBackend
	Close() error
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type numericPhysicalWriter func(column, expression string, id int64) error

func withNumericBackends(t *testing.T, run func(*testing.T, numericBackend, string, numericPhysicalWriter)) {
	t.Run("sqlite", func(t *testing.T) {
		backend, err := sqlite.Open(t.Context(), "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "numeric.db")))
		check(t, err)
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateNumeric(t, backend)
		run(t, backend, "sqlite", func(column, expression string, id int64) error {
			_, err := backend.ExecContext(t.Context(), `UPDATE main."gdj_numeric_measure" SET "`+column+`" = `+expression+` WHERE id = ?`, id)
			return err
		})
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
		schema := fmt.Sprintf("godj_numeric_%d_%d", os.Getpid(), time.Now().UnixNano())
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
		migrateNumeric(t, backend)
		run(t, backend, "postgres", func(column, expression string, id int64) error {
			_, err := connection.Exec(t.Context(), `UPDATE `+quoted+`."gdj_numeric_measure" SET `+pgx.Identifier{column}.Sanitize()+` = `+expression+` WHERE id = $1`, id)
			return err
		})
	})
}

func migrateNumeric(t *testing.T, backend numericBackend) {
	t.Helper()
	migration := migrations.Migration{App: "records", Name: "0001_initial"}
	for _, name := range []string{"measure", "required", "link"} {
		found := false
		for _, model := range records.GoDjRelationSchema().Models {
			if model.Name == name {
				migration.Operations = append(migration.Operations, migrations.CreateModel{AppLabel: "records", Model: model})
				found = true
				break
			}
		}
		if !found {
			t.Fatal("missing numeric generated model", name)
		}
	}
	wire, err := definition.Encode(definition.Producer{Name: "numeric-consumer", Version: "1"}, migration)
	check(t, err)
	loaded, _, err := definition.Load(definition.Source{SourceID: "numeric", Document: wire})
	check(t, err)
	_, err = (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest())
	check(t, err)
}

type numericProbe struct {
	numericBackend
	plans []query.Plan
}

func (p *numericProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.plans = append(p.plans, plan)
	return p.numericBackend.Query(ctx, plan)
}
