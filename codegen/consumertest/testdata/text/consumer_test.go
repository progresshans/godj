package consumer_test

import (
	"strings"
	"testing"

	"example.com/godj-text/models"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
)

func TestTextGeneratedDefaults(t *testing.T) {
	if err := (models.NoteCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("required Text body was silently defaulted")
	}
	create := models.NewNoteCreate("").BuildCreate()
	if err := create.Err(); err != nil {
		t.Fatal(err)
	}
	for _, assignment := range create.Assignments() {
		switch assignment.Field().Name() {
		case "body", "empty":
			if value, ok := assignment.Value().String(); !ok || value != "" {
				t.Fatal("explicit/default empty text became null")
			}
		case "abstract":
			if !assignment.Value().IsNull() {
				t.Fatal("omitted nullable Text became empty")
			}
		case "seed":
			if value, ok := assignment.Value().String(); !ok || value != strings.Repeat("line\n日本語 ", 1000) {
				t.Fatal("long multiline default changed")
			}
		}
	}
}

func TestTextStorageAndQuery(t *testing.T) {
	ctx := t.Context()
	backend, err := sqlite.OpenMemory(ctx, "text-consumer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	document, err := definition.Encode(definition.Producer{Name: "text-consumer", Version: "1"}, migrations.Migration{
		App: "notes", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "notes", Model: (models.NoteDescriptor{}).Metadata()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "text-consumer", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	body := "first line\n" + strings.Repeat("長い本文 ", 3000) + "</textarea><script>untrusted</script>"
	first, err := models.NoteObjects.Create(ctx, backend, models.NewNoteCreate(body))
	if err != nil {
		t.Fatal(err)
	}
	second, err := models.NoteObjects.Create(ctx, backend, models.NewNoteCreate("short").WithAbstract(""))
	if err != nil {
		t.Fatal(err)
	}
	if first.Body != body || first.Abstract != nil || first.Empty == nil || *first.Empty != "" || first.Seed != strings.Repeat("line\n日本語 ", 1000) {
		t.Fatal("stored Text defaults or presence changed")
	}
	all := models.NoteObjects.Using(backend).OrderBy(models.NoteFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil || len(rows) != 2 || rows[0].Body != body || rows[1].Abstract == nil || *rows[1].Abstract != "" {
		t.Fatalf("long Text roundtrip: %v", err)
	}
	*rows[1].Abstract = "mutated"
	again, err := all.All(ctx)
	if err != nil || *again[1].Abstract != "" {
		t.Fatal("Text pointer escaped cached result")
	}
	typed := models.NoteObjects.Using(backend).Filter(models.NoteFields.Body.IContains("FIRST"))
	dynamic, err := orm.ParseDynamic(models.NoteDescriptor{}, nil, []orm.LookupInput{{Key: "body__icontains", Value: "FIRST"}})
	if err != nil || !typed.Plan().Equal(models.NoteObjects.Using(backend).Filter(dynamic...).Plan()) {
		t.Fatal("typed and dynamic Text queries diverged")
	}
	if count, err := typed.Count(ctx); err != nil || count != 1 {
		t.Fatalf("Text predicate: %v", err)
	}
	second, err = models.NoteObjects.Update(ctx, backend, second, models.NotePatch{}.WithBody("matched").WithAbstract("matched"))
	if err != nil {
		t.Fatal(err)
	}
	if count, err := models.NoteObjects.Using(backend).Filter(models.NoteFields.Body.ExactField(orm.F[models.Note, string](models.NoteFields.Abstract))).Count(ctx); err != nil || count != 1 {
		t.Fatal("nullable Text F comparison diverged")
	}
	values, err := orm.SelectInto(ctx, models.NoteObjects.Using(backend).OrderBy(models.NoteFields.ID.Asc()), orm.Project1(models.NoteFields.Abstract, func(value *string) *string { return value }))
	if err != nil || len(values) != 2 || values[0] != nil || values[1] == nil || *values[1] != "matched" {
		t.Fatal("nullable Text projection lost presence")
	}
	maximum, err := orm.AggregateInto(ctx, models.NoteObjects.Using(backend), orm.Aggregate1(orm.Max(models.NoteFields.Body), func(value orm.Optional[string]) orm.Optional[string] { return value }))
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := maximum.Get(); !ok || value != "matched" {
		t.Fatal("Text aggregate changed")
	}
	second.Body = "saved"
	*second.Abstract = "not saved"
	if err := models.NoteObjects.Save(ctx, backend, &second, models.NoteUpdateFields(models.NoteFields.Body)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.NoteObjects.Using(backend).Filter(models.NoteFields.ID.Exact(second.ID)).OrderBy(models.NoteFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Body != "saved" || stored.Abstract == nil || *stored.Abstract != "matched" {
		t.Fatal("Text update mask saved an omitted field")
	}
	if _, err := models.NoteObjects.Create(ctx, backend, models.NewNoteCreate("").WithAbstractNull()); err != nil {
		t.Fatal(err)
	}
	if count, err := models.NoteObjects.Using(backend).Filter(models.NoteFields.Body.Exact("")).Count(ctx); err != nil || count != 1 {
		t.Fatal("empty Text was confused with null")
	}
}
