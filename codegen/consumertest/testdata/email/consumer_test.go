package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/godj-email/models"
	"example.com/godj-email/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

//go:embed sqlite_reference.json
var sqliteReference []byte

//go:embed postgres_reference.json
var postgresReference []byte

type emailBackend interface {
	db.Queryer
	db.Mutator
	migrationbackend.RevisionFencedBackend
	Close() error
}

func TestGeneratedEmailInputProjection(t *testing.T) {
	metadata := models.ContactDescriptor{}.Metadata()
	if metadata.Fields[2].Kind != ir.FieldEmail || metadata.Fields[2].MaxLength != 254 {
		t.Fatal("generated descriptor lost email semantics")
	}
	spec, err := formmodel.NewSpecForFields(metadata, []string{"address"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Fields()[0].Kind() != forms.FieldEmail || spec.Fields()[0].Widget() != forms.EmailInput {
		t.Fatal("model form lost email input")
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{"address": {"not-an-email"}}), nil)
	if err != nil || bound.Valid() || bound.Errors().ByField("address").Empty() {
		t.Fatal("generated email form accepted invalid syntax")
	}
	jsonSpec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "address", AllowEmpty: true})
	if err != nil {
		t.Fatal(err)
	}
	object, err := serializers.NewObject(serializers.MemberOf("address", serializers.String("invalid")))
	if err != nil {
		t.Fatal(err)
	}
	result, err := jsonSpec.Bind(object, serializers.ModeFull)
	if err != nil || result.Valid() {
		t.Fatal("generated model JSON input accepted invalid syntax")
	}
	encoder, err := serializers.NewModelEncoder[models.Contact](jsonSpec, metadata, models.ContactDescriptor{}.WriteFieldValue)
	if err != nil {
		t.Fatal(err)
	}
	legacy := "not-an-email"
	if _, err := encoder.Encode(models.Contact{Address: &legacy}); err != nil {
		t.Fatal("response re-ran email input grammar")
	}
}

func TestGeneratedEmailStorageAndHistory(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "email.sqlite3")) + "?mode=rwc"
		runEmailStorage(t, sqliteReference, func(ctx context.Context) (emailBackend, error) { return sqlite.Open(ctx, dsn) })
	})
	databaseURL := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL connection is absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), databaseURL)
		if err != nil {
			t.Fatal("connect generated email PostgreSQL consumer")
		}
		name := fmt.Sprintf("godj_email_%d_%d", os.Getpid(), time.Now().UnixNano())
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
		runEmailStorage(t, postgresReference, func(ctx context.Context) (emailBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: name})
		})
	})
}

func runEmailStorage(t *testing.T, document []byte, open func(context.Context) (emailBackend, error)) {
	t.Helper()
	ctx := t.Context()
	var reference struct {
		Django  string `json:"django"`
		Storage struct {
			Before, After, Reverse [][]*string
			Unvalidated            string `json:"unvalidated_save"`
			Queries                map[string][]string
		} `json:"storage"`
	}
	if err := json.Unmarshal(document, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Storage.Before) != 5 || len(reference.Storage.Reverse) != 6 || len(reference.Storage.Queries) != 4 {
		t.Fatal("incomplete native email storage reference")
	}
	backend, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if backend != nil {
			if err := backend.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	current := models.ContactDescriptor{}.Metadata()
	before := current.Clone()
	before.Fields[2].Kind = ir.FieldChar
	initial := migrations.Migration{App: "email_reference", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "email_reference", Model: before}, migrations.CreateModel{AppLabel: "email_reference", Model: models.MessageDescriptor{}.Metadata()}}}
	change := migrations.Migration{App: "email_reference", Name: "0002_email", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "email_reference", ModelName: "contact", Before: before.Fields[2], After: current.Fields[2]}}}
	var sources []definition.Source
	for _, migration := range []migrations.Migration{initial, change} {
		wire, err := definition.Encode(definition.Producer{Name: "email-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	for _, renderer := range []migrationbackend.MigrationSQLRenderer{
		sqlite.NewMigrationSQLRenderer(),
		postgres.NewMigrationSQLRenderer(postgres.MigrationSQLConfig{Schema: "public"}),
	} {
		statements, err := migrations.RenderMigrationSQL(ctx, loaded, change.Key(), renderer)
		if err != nil || len(statements) != 0 {
			t.Fatal("email metadata transition attempted physical DDL", statements, err)
		}
	}
	migrate := func(name string) migrations.ProjectState {
		t.Helper()
		state, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "email_reference", Name: name})))
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	previous := migrate(initial.Name)
	for _, row := range reference.Storage.Before {
		input := models.NewContactCreate(*row[0])
		if row[1] != nil {
			input = input.WithAddress(*row[1])
		}
		created, err := models.ContactObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := models.MessageObjects.Create(ctx, backend, models.NewMessageCreate(created.ID)); err != nil {
			t.Fatal(err)
		}
	}
	assertRows := func(expected [][]*string) {
		t.Helper()
		rows, err := models.ContactObjects.Using(backend).OrderBy(models.ContactFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		actual := [][]*string{}
		for _, row := range rows {
			label := row.Label
			actual = append(actual, []*string{&label, row.Address})
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatal("email metadata transition changed stored rows")
		}
	}
	assertRows(reference.Storage.Before)
	state := migrate(change.Name)
	if state.Equal(previous) {
		t.Fatal("email transition lost historical kind")
	}
	assertRows(reference.Storage.After)
	created, err := models.ContactObjects.Create(ctx, backend, models.NewContactCreate("new_invalid").WithAddress(reference.Storage.Unvalidated))
	if err != nil {
		t.Fatal("email storage implicitly rejected legacy string", err)
	}
	if created.Address == nil || *created.Address != reference.Storage.Unvalidated {
		t.Fatal("email storage rewrote input")
	}
	queries := map[string]struct {
		predicate orm.Predicate[models.Contact]
		key       string
		value     any
	}{
		"exact":    {models.ContactFields.Address.Exact("Person@Example.com"), "address", "Person@Example.com"},
		"iexact":   {models.ContactFields.Address.IExact("PERSON@example.COM"), "address__iexact", "PERSON@example.COM"},
		"contains": {models.ContactFields.Address.IContains("example"), "address__icontains", "example"},
		"null":     {models.ContactFields.Address.IsNull(true), "address__isnull", true},
	}
	for name, probe := range queries {
		typed := models.ContactObjects.Using(backend).Filter(probe.predicate)
		dynamic, err := orm.ParseDynamic(models.ContactDescriptor{}, nil, []orm.LookupInput{{Key: probe.key, Value: probe.value}})
		if err != nil {
			t.Fatal(err)
		}
		if !typed.Plan().Equal(models.ContactObjects.Using(backend).Filter(dynamic...).Plan()) {
			t.Fatal("typed and dynamic email queries diverged")
		}
		rows, err := typed.OrderBy(models.ContactFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		labels := []string{}
		for _, row := range rows {
			labels = append(labels, row.Label)
		}
		if !reflect.DeepEqual(labels, reference.Storage.Queries[name]) {
			t.Fatalf("email query %s differs from native: %v", name, labels)
		}
	}
	related, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := models.MessageObjects.Using(backend).Filter(related.ModelsMessage.Contact.Address.IExact("PERSON@example.COM")).OrderBy(models.MessageFields.ID.Asc()).All(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatal("email forward relation lookup lost native string meaning", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend = nil
	backend, err = open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertRows(reference.Storage.Reverse)
	if restored := migrate(initial.Name); !restored.Equal(previous) {
		t.Fatal("email reverse did not restore Char history")
	}
	assertRows(reference.Storage.Reverse)
}
