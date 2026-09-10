package migrations

import (
	"context"
	"sync"
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
	left := ProjectState{apps: map[string]ir.Schema{"empty": {AppLabel: "empty"}}}
	right := ProjectState{formatVersion: StateFormatVersion, apps: map[string]ir.Schema{"empty": {AppLabel: "empty", Models: []ir.Model{}}}}
	if !left.Equal(right) {
		t.Fatal("nil and empty models differ after clone normalization")
	}
	left.apps["empty"] = ir.Schema{Models: []ir.Model{{Fields: nil}}}
	right.apps["empty"] = ir.Schema{Models: []ir.Model{{Fields: []ir.Field{}}}}
	if !left.Equal(right) {
		t.Fatal("nil and empty fields differ after clone normalization")
	}
	right.formatVersion++
	if left.Equal(right) {
		t.Fatal("different state versions compare equal")
	}
}

func TestProjectStateDerivedSnapshotsKeepSchemaOwnership(t *testing.T) {
	base, err := NewProjectState(articleSchema(), relationMigrationSchema())
	if err != nil {
		t.Fatal(err)
	}
	want := base.Clone()
	schema, _ := base.Schema("news")
	schema.Models[0].Fields[1].MaxLength = 100
	changed := base.withSchema(schema)
	schema.Models[0].Fields[1].MaxLength = 1
	removed := changed.withoutApp("blog")
	if _, found := removed.Schema("blog"); found {
		t.Fatal("removed app remains")
	}
	if _, found := changed.Schema("blog"); !found {
		t.Fatal("removing an app changed an earlier state")
	}
	if base.Equal(changed) || changed.Equal(removed) {
		t.Fatal("different schema snapshots compare equal")
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for _, state := range []ProjectState{base, changed, removed} {
				model, _ := state.Model("news", "article")
				model.Fields[1].Column = "changed"
			}
			model, _ := changed.Model("news", "article")
			if model.Fields[1].MaxLength != 100 || !base.Equal(want) {
				t.Error("input or getter mutation changed a published state")
			}
		})
	}
	workers.Wait()
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

func TestProjectStateAndOperationWrappersUseOneCurrentRelationFormat(t *testing.T) {
	t.Parallel()

	relationSchema := relationMigrationSchema()
	state, err := NewProjectState(relationSchema)
	if err != nil || state.FormatVersion() != StateFormatVersion {
		t.Fatalf("NewProjectState(current relation) = state:%#v err:%v", state, err)
	}
	if model, exists := state.Model("blog", "post"); !exists || len(model.Fields) != 2 || model.Fields[1].Relation == nil {
		t.Fatalf("current relation state = %#v/%t", model, exists)
	}

	create := CreateModel{AppLabel: "blog", Model: relationSchema.Models[0]}
	afterCreate, err := create.stateForward(EmptyProjectState())
	if err != nil || !afterCreate.Equal(state) {
		t.Fatalf("CreateModel current relation = state:%#v err:%v", afterCreate, err)
	}

	before, err := NewProjectState(articleSchema())
	if err != nil {
		t.Fatalf("NewProjectState(current scalar) error = %v", err)
	}
	add := AddField{AppLabel: "news", ModelName: "article", Field: relationMigrationField()}
	afterAdd, err := add.stateForward(before)
	model, exists := afterAdd.Model("news", "article")
	if err != nil || !exists || len(model.Fields) != 4 || model.Fields[3].Relation == nil {
		t.Fatalf("AddField current relation = model:%#v exists:%t err:%v", model, exists, err)
	}
}

func TestDirectExecutorRelationStateUsesCapabilityBoundaryBeforeIO(t *testing.T) {
	t.Parallel()

	state := EmptyProjectState()
	state.apps["blog"] = relationMigrationSchema()
	fake := &fakeBackend{transaction: newFakeTransaction()}
	_, err := (DirectExecutor{Backend: fake}).Apply(context.Background(), state, articleMigration())
	assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
	if fake.beginCount != 0 {
		t.Fatalf("Apply(current relation state) = err:%v begin:%d", err, fake.beginCount)
	}
}

func TestExecutorRejectsUnsupportedProjectStateVersionBeforeIO(t *testing.T) {
	t.Parallel()

	state := EmptyProjectState()
	state.formatVersion = StateFormatVersion + 1
	fake := &fakeBackend{transaction: newFakeTransaction()}
	_, err := (DirectExecutor{Backend: fake}).Apply(context.Background(), state, articleMigration())
	assertMigrationError(t, err, CategoryState, CodeInvalidState, NoOperation, "")
	if fake.beginCount != 0 {
		t.Fatalf("BeginMigration() calls = %d, want 0", fake.beginCount)
	}
}

func articleSchema() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
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

func relationMigrationSchema() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:    "post",
			GoName:  "Post",
			DBTable: "blog_post",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				relationMigrationField(),
			},
		}},
	}
}

func relationMigrationField() ir.Field {
	return ir.Field{
		Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "posts"},
			OnDelete:    ir.DeleteProtect,
		},
	}
}
