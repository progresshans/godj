package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/godj-slug/models"
	"example.com/godj-slug/project"
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
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

//go:embed sqlite_reference.json
var sqliteReference []byte

//go:embed postgres_reference.json
var postgresReference []byte

type slugBackend interface {
	db.Queryer
	db.Mutator
	db.Atomic
	migrationbackend.RevisionFencedBackend
	Close() error
}

func TestGeneratedSlugInputProjection(t *testing.T) {
	metadata := models.ContactDescriptor{}.Metadata()
	if metadata.Fields[2].Kind != ir.FieldSlug || metadata.Fields[2].MaxLength != 50 || !metadata.Fields[2].DBIndex || !metadata.Fields[2].AllowUnicode {
		t.Fatal("generated descriptor lost slug semantics")
	}
	spec, err := formmodel.NewSpecForFields(metadata, []string{"address"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Fields()[0].Kind() != forms.FieldSlug || spec.Fields()[0].Widget() != forms.TextInput || !spec.Fields()[0].AllowUnicode() {
		t.Fatal("model form lost slug input")
	}
	bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"address": {"invalid slug!"}}), nil)
	if err != nil || bound.Valid() || bound.Errors().ByField("address").Empty() {
		t.Fatal("generated slug form accepted invalid syntax")
	}
	jsonSpec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "address", AllowEmpty: true})
	if err != nil {
		t.Fatal(err)
	}
	object, err := serializers.NewObject(serializers.MemberOf("address", serializers.String("invalid slug!")))
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
	legacy := "invalid slug!"
	if _, err := encoder.Encode(models.Contact{Address: &legacy}); err != nil {
		t.Fatal("response re-ran slug input grammar")
	}
}

func TestGeneratedSlugStorageAndHistory(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "slug.sqlite3")) + "?mode=rwc"
		runSlugStorage(t, sqliteReference, func(ctx context.Context) (slugBackend, error) { return sqlite.Open(ctx, dsn) })
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
			t.Fatal("connect generated slug PostgreSQL consumer")
		}
		name := fmt.Sprintf("godj_slug_%d_%d", os.Getpid(), time.Now().UnixNano())
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
		runSlugStorage(t, postgresReference, func(ctx context.Context) (slugBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: name})
		})
	})
}

func runSlugStorage(t *testing.T, document []byte, open func(context.Context) (slugBackend, error)) {
	t.Helper()
	ctx := t.Context()
	type snapshot struct {
		Rows [][]*string `json:"rows"`
	}
	var reference struct {
		Django  string `json:"django"`
		Storage struct {
			Before, Reverse snapshot
			Lifecycle       []struct {
				snapshot
				Stage string `json:"stage"`
			} `json:"lifecycle"`
			Unvalidated            string `json:"unvalidated_save"`
			FormSaved              string `json:"form_saved"`
			Queries                map[string][]string
			UniqueRejectsDuplicate bool `json:"unique_rejects_duplicate"`
			RollbackAbsent         bool `json:"rollback_absent"`
			Prepared               struct {
				Address   string
				Persisted bool
			} `json:"prepared"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(document, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Storage.Before.Rows) != 6 || len(reference.Storage.Reverse.Rows) != 8 || len(reference.Storage.Lifecycle) != 6 || len(reference.Storage.Queries) != 4 || !reference.Storage.UniqueRejectsDuplicate || !reference.Storage.RollbackAbsent || reference.Storage.Prepared.Persisted || reference.Storage.Prepared.Address != reference.Storage.FormSaved {
		t.Fatal("incomplete native slug storage reference")
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
	before.Fields[2].Kind, before.Fields[2].DBIndex, before.Fields[2].AllowUnicode = ir.FieldChar, false, false
	initial := migrations.Migration{App: "slug_reference", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "slug_reference", Model: before},
		migrations.CreateModel{AppLabel: "slug_reference", Model: models.MessageDescriptor{}.Metadata()},
	}}
	history := []migrations.Migration{initial}
	previousField := before.Fields[2].Clone()
	stages := []struct {
		name                   string
		index, unicode, unique bool
		statements             int
	}{
		{"add_index", true, false, false, 1}, {"allow_unicode", true, true, false, 0},
		{"remove_index", false, true, false, 1}, {"readd_index", true, true, false, 1},
		{"unique", true, true, true, 2}, {"nonunique", true, true, false, 2},
	}
	for i, stage := range stages {
		after := previousField.Clone()
		after.Kind, after.DBIndex, after.AllowUnicode, after.Unique = ir.FieldSlug, stage.index, stage.unicode, stage.unique
		history = append(history, migrations.Migration{App: initial.App, Name: fmt.Sprintf("%04d_%s", i+2, stage.name), Dependencies: []migrations.MigrationKey{history[len(history)-1].Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: initial.App, ModelName: "contact", Before: previousField, After: after}}})
		previousField = after
	}
	var sources []definition.Source
	for _, migration := range history {
		wire, err := definition.Encode(definition.Producer{Name: "slug-consumer", Version: "1"}, migration)
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
		for i, stage := range stages {
			statements, err := migrations.RenderMigrationSQL(ctx, loaded, history[i+1].Key(), renderer)
			if err != nil || len(statements) != stage.statements {
				t.Fatal("slug history lost required index DDL or added metadata DDL", stage.name, statements, err)
			}
		}
	}
	migrate := func(key migrations.MigrationKey) migrations.ProjectState {
		t.Helper()
		state, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(key)))
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	originalState := migrate(initial.Key())
	for _, row := range reference.Storage.Before.Rows {
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
			t.Fatalf("slug migration/storage differs from native: %#v", actual)
		}
	}
	assertRows(reference.Storage.Before.Rows)
	for i, stage := range stages {
		observed := reference.Storage.Lifecycle[i]
		if observed.Stage != stage.name {
			t.Fatal("native lifecycle order changed")
		}
		if state := migrate(history[i+1].Key()); state.Equal(originalState) {
			t.Fatal("slug transition lost historical policy")
		}
		assertRows(observed.Rows)
		if stage.unique {
			_, err := models.ContactObjects.Create(ctx, backend, models.NewContactCreate("duplicate").WithAddress("Old_Name"))
			if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) {
				t.Fatal("unique index transition did not enforce native duplicate rejection", err)
			}
			assertRows(observed.Rows)
		}
	}
	created, err := models.ContactObjects.Create(ctx, backend, models.NewContactCreate("new_invalid").WithAddress(reference.Storage.Unvalidated))
	if err != nil || created.Address == nil || *created.Address != reference.Storage.Unvalidated {
		t.Fatal("ordinary ORM rewrote or validated slug storage", err)
	}
	queries := map[string]struct {
		predicate orm.Predicate[models.Contact]
		key       string
		value     any
	}{
		"exact":    {models.ContactFields.Address.Exact("Old_Name"), "address", "Old_Name"},
		"iexact":   {models.ContactFields.Address.IExact("OLD_NAME"), "address__iexact", "OLD_NAME"},
		"contains": {models.ContactFields.Address.IContains("old"), "address__icontains", "old"},
		"null":     {models.ContactFields.Address.IsNull(true), "address__isnull", true},
	}
	for name, probe := range queries {
		typed := models.ContactObjects.Using(backend).Filter(probe.predicate)
		dynamic, err := orm.ParseDynamic(models.ContactDescriptor{}, nil, []orm.LookupInput{{Key: probe.key, Value: probe.value}})
		if err != nil {
			t.Fatal(err)
		}
		if !typed.Plan().Equal(models.ContactObjects.Using(backend).Filter(dynamic...).Plan()) {
			t.Fatal("typed/dynamic slug AST differs")
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
			t.Fatal("slug query differs from native", name, labels)
		}
	}
	related, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := models.MessageObjects.Using(backend).Filter(related.ModelsMessage.Contact.Address.IExact("OLD_NAME")).OrderBy(models.MessageFields.ID.Asc()).All(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatal("slug relation lookup lost native string meaning", err)
	}
	t.Run("model_form_save", func(t *testing.T) {
		spec, err := formmodel.NewSpecForFields(current, []string{"label", "address"})
		if err != nil {
			t.Fatal(err)
		}
		instance, err := formmodel.BindInstance(ctx, models.ContactObjects, spec, forms.NewData(map[string][]string{"label": {"form_saved"}, "address": {"  읽기-쉬운_주소  "}}), nil, formmodel.PostClean{})
		if err != nil || !instance.BoundForm().Form().Valid() {
			t.Fatal("generated slug ModelForm bind", err)
		}
		prepared, err := formmodel.PrepareInstance(models.ContactObjects, instance.BoundForm(), nil)
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := prepared.Model()
		if err != nil || candidate.ID != 0 || candidate.Label != "form_saved" || candidate.Address == nil || *candidate.Address != reference.Storage.Prepared.Address {
			t.Fatal("commit-false slug candidate differs from native", err)
		}
		assertRows(reference.Storage.Reverse.Rows[:7])
		rollback := errors.New("abort slug publication")
		err = backend.Atomic(ctx, func(session db.Session) error {
			copy := candidate
			if err := prepared.Save(ctx, session, &copy); err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal("slug save lost rollback cause", err)
		}
		assertRows(reference.Storage.Reverse.Rows[:7])
		if err := backend.Atomic(ctx, func(session db.Session) error { return prepared.Save(ctx, session, &candidate) }); err != nil {
			t.Fatal(err)
		}
		if candidate.ID <= 0 || candidate.Address == nil || *candidate.Address != reference.Storage.FormSaved {
			t.Fatal("slug form publication differs from native")
		}
		assertRows(reference.Storage.Reverse.Rows)
	})
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend = nil
	backend, err = open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertRows(reference.Storage.Reverse.Rows)
	if restored := migrate(initial.Key()); !restored.Equal(originalState) {
		t.Fatal("reverse did not restore exact Char history")
	}
	assertRows(reference.Storage.Reverse.Rows)
	if restored := migrate(history[len(history)-1].Key()); restored.Equal(originalState) {
		t.Fatal("reapply lost slug policy")
	}
	assertRows(reference.Storage.Reverse.Rows)
}
