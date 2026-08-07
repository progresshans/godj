package migrations

import (
	"context"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestProjectStateZeroValueIsImmutableEmptyState(t *testing.T) {
	t.Parallel()

	var state ProjectState
	if state.FormatVersion() != StateFormatVersion {
		t.Fatalf("FormatVersion() = %d, want %d", state.FormatVersion(), StateFormatVersion)
	}
	if apps := state.Apps(); len(apps) != 0 {
		t.Fatalf("Apps() = %v, want empty", apps)
	}
	if _, exists := state.Schema("missing"); exists {
		t.Fatal("Schema(missing) exists")
	}
	if !state.Equal(EmptyProjectState()) {
		t.Fatal("zero state does not equal explicit empty state")
	}
}

func TestProjectStateNormalizesAndDeepClonesSchemaIR(t *testing.T) {
	t.Parallel()

	input := articleSchema()
	input.Models[0].DBTable = ""
	input.Models[0].Fields[0].Column = ""
	input.Models[0].Fields[2].Default = &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false}
	state, err := NewProjectState(input)
	if err != nil {
		t.Fatalf("NewProjectState() error = %v", err)
	}
	model, exists := state.Model("news", "article")
	if !exists {
		t.Fatal("Model(news.article) missing")
	}
	if model.DBTable != "news_article" {
		t.Fatalf("DBTable = %q, want news_article", model.DBTable)
	}
	if model.Fields[0].Column != "id" {
		t.Fatalf("ID column = %q, want id", model.Fields[0].Column)
	}
	input.Models[0].Fields[2].Default.Boolean = true
	again, _ := state.Model("news", "article")
	if again.Fields[2].Default == nil || again.Fields[2].Default.Boolean {
		t.Fatalf("input default mutation aliased state: %#v", again.Fields[2].Default)
	}

	returned, _ := state.Schema("news")
	returned.Models[0].DBTable = "mutated"
	returned.Models[0].Fields[1].Column = "mutated"
	returned.Models[0].Fields[2].Default.Boolean = true
	again, _ = state.Model("news", "article")
	if again.DBTable != "news_article" || again.Fields[1].Column != "title" {
		t.Fatalf("accessor mutated state: %#v", again)
	}
	if again.Fields[2].Default == nil || again.Fields[2].Default.Boolean {
		t.Fatalf("accessor default mutation aliased state: %#v", again.Fields[2].Default)
	}

	clone := state.Clone()
	clone.apps["news"].Models[0].Fields[2].Default.Boolean = true
	again, _ = state.Model("news", "article")
	if again.Fields[2].Default == nil || again.Fields[2].Default.Boolean {
		t.Fatalf("Clone() aliases source default: %#v", again.Fields[2].Default)
	}
}

func TestProjectStateRejectsDuplicateApps(t *testing.T) {
	t.Parallel()

	_, err := NewProjectState(articleSchema(), articleSchema())
	if err == nil {
		t.Fatal("NewProjectState() duplicate app error = nil")
	}
}

func TestExecutorRejectsUnsupportedProjectStateVersionBeforeIO(t *testing.T) {
	t.Parallel()

	state := EmptyProjectState()
	state.formatVersion = StateFormatVersion + 1
	fake := &fakeBackend{transaction: newFakeTransaction()}
	_, err := (Executor{Backend: fake}).Apply(context.Background(), state, articleMigration())
	assertMigrationError(t, err, CategoryState, CodeInvalidState, NoOperation, "")
	if fake.beginCount != 0 {
		t.Fatalf("BeginMigration() calls = %d, want 0", fake.beginCount)
	}
}

func articleSchema() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.FormatVersion,
		AppLabel:      "news",
		Models: []ir.Model{{
			Name:    "article",
			GoName:  "Article",
			DBTable: "news_article",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
				{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean},
			},
		}},
	}
}

func summaryField() ir.Field {
	return ir.Field{
		Name:      "summary",
		GoName:    "Summary",
		Column:    "summary",
		Kind:      ir.FieldChar,
		Nullable:  true,
		MaxLength: 200,
	}
}
