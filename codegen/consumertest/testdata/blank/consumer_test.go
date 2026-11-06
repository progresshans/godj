package consumer_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/godj-blank/models"
	"example.com/godj-blank/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
)

type blankBackend interface {
	db.Session
	db.RelationAtomic
	db.CoordinatedRelationAtomic
	migrationbackend.RevisionFencedBackend
	Close() error
}

func TestGeneratedBlankPolicyStorageAndHistory(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "blank.sqlite3")) + "?mode=rwc"
		runBlankPolicy(t, func(ctx context.Context) (blankBackend, error) { return sqlite.Open(ctx, dsn) })
	})
	url := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if url == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL is absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), url)
		if err != nil {
			t.Fatal("connect generated blank-policy PostgreSQL consumer")
		}
		name := fmt.Sprintf("godj_blank_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{name}.Sanitize()
		if _, err := connection.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
			_ = connection.Close(context.Background())
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
				t.Error(err)
			}
			if err := connection.Close(ctx); err != nil {
				t.Error(err)
			}
		})
		runBlankPolicy(t, func(ctx context.Context) (blankBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: url, Schema: name})
		})
	})
}

func runBlankPolicy(t *testing.T, open func(context.Context) (blankBackend, error)) {
	t.Helper()
	ctx := t.Context()
	b, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if b != nil {
			if err := b.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	current := models.OwnerDescriptor{}.Metadata()
	previous := current.Clone()
	var changes []migrations.Operation
	for i, field := range current.Fields {
		if field.PrimaryKey {
			continue
		}
		if !field.Blank {
			t.Fatal("generated descriptor lost blank policy", field.Name)
		}
		previous.Fields[i].Blank = false
		changes = append(changes, migrations.AlterField{AppLabel: "blank_reference", ModelName: current.Name, Before: previous.Fields[i], After: field})
	}
	if len(current.ManyToMany) != 1 || !current.ManyToMany[0].Blank {
		t.Fatal("generated collection lost blank policy")
	}
	previous.ManyToMany[0].Blank = false
	changes = append(changes, migrations.AlterManyToMany{AppLabel: "blank_reference", ModelName: current.Name, Before: previous.ManyToMany[0], After: current.ManyToMany[0]})
	initial := migrations.Migration{App: "blank_reference", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "blank_reference", Model: models.LabelDescriptor{}.Metadata()},
		migrations.CreateModel{AppLabel: "blank_reference", Model: previous},
	}}
	changed := migrations.Migration{App: "blank_reference", Name: "0002_blank", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: changes}
	var sources []definition.Source
	for _, migration := range []migrations.Migration{initial, changed} {
		wire, err := definition.Encode(definition.Producer{Name: "blank-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	for _, renderer := range []migrationbackend.MigrationSQLRenderer{sqlite.NewMigrationSQLRenderer(), postgres.NewMigrationSQLRenderer(postgres.MigrationSQLConfig{Schema: "public"})} {
		statements, err := migrations.RenderMigrationSQL(ctx, loaded, changed.Key(), renderer)
		if err != nil || len(statements) != 0 {
			t.Fatal("blank-only change attempted physical DDL", statements, err)
		}
	}
	migrate := func(name string) migrations.ProjectState {
		t.Helper()
		state, err := (migrations.Executor{Backend: b}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "blank_reference", Name: name})))
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := migrate(initial.Name)
	owner, err := models.OwnerObjects.Create(ctx, b, models.NewOwnerCreate("").WithAddress("legacy-invalid"))
	if err != nil {
		t.Fatal(err)
	}
	label, err := models.LabelObjects.Create(ctx, b, models.NewLabelCreate(""))
	if err != nil {
		t.Fatal("ORM storage implicitly enforced blank validation", err)
	}
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	links, err := collections.ModelsOwnerLabels.From(b, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := links.Add(ctx, []models.Label{label}); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		row, found, err := models.OwnerObjects.Using(b).Filter(models.OwnerFields.ID.Exact(owner.ID)).OrderBy(models.OwnerFields.ID.Asc()).First(ctx)
		if err != nil || !found || row.Name != "" || row.Address == nil || *row.Address != "legacy-invalid" || row.Score != 3 {
			t.Fatal("blank migration changed stored scalar/default", err)
		}
		links, err := collections.ModelsOwnerLabels.From(b, row)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := links.All(ctx)
		if err != nil || len(rows) != 1 || rows[0].ID != label.ID || rows[0].Name != label.Name {
			t.Fatal("blank migration changed retained links", err)
		}
	}
	check()
	if after := migrate(changed.Name); after.Equal(before) {
		t.Fatal("blank migration lost historical input policy")
	}
	check()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b = nil
	b, err = open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check()
	if restored := migrate(initial.Name); !restored.Equal(before) {
		t.Fatal("blank reverse did not restore original metadata")
	}
	check()
	newOwner, err := models.OwnerObjects.Create(ctx, b, models.NewOwnerCreate(""))
	if err != nil || newOwner.ID <= owner.ID {
		t.Fatal("blank reverse damaged identity allocation", err)
	}
}
