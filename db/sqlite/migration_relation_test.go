package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteMigrationCapabilities(t *testing.T) {
	t.Parallel()
	got := (&Backend{}).MigrationCapabilities()
	want := migrationbackend.MigrationCapabilities{
		CreateModelForeignKeys:            true,
		AddNullableForeignKey:             true,
		AddRequiredForeignKeyToEmptyTable: true,
		RemoveForeignKeyByTableRemake:     true,
	}
	if got != want {
		t.Fatalf("MigrationCapabilities() = %+v, want %+v", got, want)
	}
}

func TestSQLiteRelationTargetAuthorityIndexSelectsExactSameTableSnapshot(t *testing.T) {
	target, created, author := sqliteRelationTestModels()
	editor := author.Clone()
	editor.Name, editor.GoName, editor.Column = "editor", "Editor", "editor_id"
	editor.Nullable = true
	editor.Relation.Reverse.Name = "edited_articles"
	after := created.Clone()
	after.Fields = append(after.Fields, editor)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          created,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: author,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.MigrationAddField,
			Before:         created,
			After:          after,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: editor,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
	}}
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_combined"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err != nil {
		t.Fatalf("validate same-table Create/Add target authority: %v", err)
	}
	candidates := seal.targetOperationByTable[sqliteRelationIdentifierKey(created.DBTable)]
	targets, known := sqliteRelationTargetsForModel(&seal, after)
	if len(candidates) != 2 || !known || len(targets) != 2 ||
		!reflect.DeepEqual(targets[0].SourceField, author) ||
		!reflect.DeepEqual(targets[1].SourceField, editor) {
		t.Fatalf("same-table target candidate selection = candidates:%v known:%t targets:%#v", candidates, known, targets)
	}
}

func TestSQLiteRelationCanonicalTableMatcherAcceptsOnlyExactTableAndMixedForms(t *testing.T) {
	target, before, author := sqliteRelationTestModels()
	editor := author.Clone()
	editor.Name, editor.GoName, editor.Column, editor.Nullable = "editor", "Editor", "editor_id", true
	editor.Relation.Reverse.Name = "edited_articles"
	model := before.Clone()
	model.Fields = append(model.Fields, editor)
	targets := []migrationbackend.MigrationTarget{
		{SourceField: author, TargetModel: target, TargetKey: target.Fields[0]},
		{SourceField: editor, TargetModel: target, TargetKey: target.Fields[0]},
	}
	tableLevel, err := compileSQLiteRelationCreateModel(model, targets)
	if err != nil {
		t.Fatal(err)
	}
	mixed := `CREATE TABLE "news_article" (` +
		`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, ` +
		`"title" VARCHAR(200) NOT NULL, ` +
		`"author_id" INTEGER NOT NULL, ` +
		`"editor_id" INTEGER NULL REFERENCES "news_author" ("id") ON DELETE NO ACTION, ` +
		`FOREIGN KEY ("author_id") REFERENCES "news_author" ("id") ON DELETE NO ACTION)`
	allInline := `CREATE TABLE "news_article" (` +
		`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, ` +
		`"title" VARCHAR(200) NOT NULL, ` +
		`"author_id" INTEGER NOT NULL REFERENCES "news_author" ("id") ON DELETE NO ACTION, ` +
		`"editor_id" INTEGER NULL REFERENCES "news_author" ("id") ON DELETE NO ACTION)`
	for name, statement := range map[string]string{
		"table level": tableLevel,
		"mixed":       mixed,
		"all inline":  allInline,
	} {
		t.Run("accept "+name, func(t *testing.T) {
			matches, err := matchesSQLiteRelationCanonicalTableSQL(statement, model, targets)
			if err != nil || !matches {
				t.Fatalf("canonical matcher(%s) = (%t, %v) for %q", name, matches, err, statement)
			}
		})
	}
	rejections := map[string]string{
		"lowercase":             strings.Replace(mixed, "CREATE TABLE", "create table", 1),
		"wrong source table":    strings.Replace(mixed, `"news_article"`, `"news_articles"`, 1),
		"wrong target table":    strings.Replace(mixed, `"news_author"`, `"news_editor"`, 1),
		"wrong target key":      strings.Replace(mixed, `("id") ON DELETE`, `("other_id") ON DELETE`, 1),
		"cascade":               strings.Replace(mixed, "ON DELETE NO ACTION", "ON DELETE CASCADE", 1),
		"deferrable":            strings.Replace(mixed, "ON DELETE NO ACTION", "ON DELETE NO ACTION DEFERRABLE", 1),
		"check":                 strings.Replace(mixed, `"editor_id" INTEGER NULL`, `"editor_id" INTEGER NULL CHECK ("editor_id" > 0)`, 1),
		"collate":               strings.Replace(mixed, `"editor_id" INTEGER NULL`, `"editor_id" INTEGER NULL COLLATE BINARY`, 1),
		"unique":                strings.Replace(mixed, `"editor_id" INTEGER NULL`, `"editor_id" INTEGER NULL UNIQUE`, 1),
		"constraint order":      strings.Replace(mixed, `"editor_id" INTEGER NULL REFERENCES "news_author" ("id") ON DELETE NO ACTION, FOREIGN KEY ("author_id")`, `FOREIGN KEY ("author_id") REFERENCES "news_author" ("id") ON DELETE NO ACTION, "editor_id" INTEGER NULL`, 1),
		"missing constraint":    strings.Replace(mixed, `, FOREIGN KEY ("author_id") REFERENCES "news_author" ("id") ON DELETE NO ACTION`, "", 1),
		"duplicate constraint":  strings.Replace(mixed, `FOREIGN KEY ("author_id")`, `FOREIGN KEY ("author_id") REFERENCES "news_author" ("id") ON DELETE NO ACTION, FOREIGN KEY ("author_id")`, 1),
		"trailing whitespace":   mixed + " ",
		"trailing statement":    mixed + "; SELECT 1",
		"malformed quote":       strings.Replace(mixed, `"editor_id"`, `"editor_id`, 1),
		"malformed parenthesis": strings.TrimSuffix(mixed, ")"),
		"malformed comma":       strings.Replace(mixed, `, "editor_id"`, `,"editor_id"`, 1),
	}
	for name, statement := range rejections {
		t.Run("reject "+name, func(t *testing.T) {
			matches, err := matchesSQLiteRelationCanonicalTableSQL(statement, model, targets)
			if err != nil {
				t.Fatalf("canonical matcher(%s) unexpected error = %v", name, err)
			}
			if matches {
				t.Fatalf("canonical matcher accepted %s: %q", name, statement)
			}
		})
	}
}

func TestSQLiteRelationCanonicalTableMatcherAcceptsLargeMixedFormInOnePass(t *testing.T) {
	target, _, baseRelation := sqliteRelationTestModels()
	model := ir.Model{
		Name: "wide", GoName: "Wide", DBTable: "wide_relation",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
	targets := make([]migrationbackend.MigrationTarget, 0, sqliteRelationMaxFields-1)
	pending := make([]string, 0, sqliteRelationMaxFields/2)
	var statement strings.Builder
	statement.WriteString(`CREATE TABLE "wide_relation" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT`)
	for index := 1; index < sqliteRelationMaxFields; index++ {
		field := baseRelation.Clone()
		field.Name = fmt.Sprintf("author_%04d", index)
		field.GoName = fmt.Sprintf("Author%04d", index)
		field.Column = fmt.Sprintf("author_%04d_id", index)
		field.Nullable = true
		field.Relation.Reverse.Name = fmt.Sprintf("wide_%04d", index)
		model.Fields = append(model.Fields, field)
		targetMetadata := migrationbackend.MigrationTarget{
			SourceField: field,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}
		targets = append(targets, targetMetadata)
		column, err := compileSQLiteRelationColumn(field)
		if err != nil {
			t.Fatal(err)
		}
		constraint, err := compileSQLiteRelationConstraint(targetMetadata)
		if err != nil {
			t.Fatal(err)
		}
		statement.WriteString(", ")
		statement.WriteString(column)
		if index%2 == 0 {
			statement.WriteString(" REFERENCES ")
			statement.WriteString(constraint)
		} else {
			quoted, err := quoteIdentifier(field.Column)
			if err != nil {
				t.Fatal(err)
			}
			pending = append(pending, "FOREIGN KEY ("+quoted+") REFERENCES "+constraint)
		}
	}
	for _, constraint := range pending {
		statement.WriteString(", ")
		statement.WriteString(constraint)
	}
	statement.WriteByte(')')
	canonical := statement.String()
	matches, err := matchesSQLiteRelationCanonicalTableSQL(canonical, model, targets)
	if err != nil || !matches {
		t.Fatalf("large mixed canonical matcher = (%t, %v), bytes=%d fields=%d", matches, err, len(canonical), len(model.Fields))
	}
	drifted := strings.Replace(canonical, "ON DELETE NO ACTION)", "ON DELETE CASCADE)", 1)
	matches, err = matchesSQLiteRelationCanonicalTableSQL(drifted, model, targets)
	if err != nil || matches {
		t.Fatalf("large mixed canonical matcher accepted tail drift = (%t, %v)", matches, err)
	}
}

func TestSQLiteNullableRelationDerivedTargetExpansionIsBoundedBeforeAllocation(t *testing.T) {
	const width = 600
	target := ir.Model{
		Name: "target", GoName: "Target", DBTable: "wide_target",
		Fields: make([]ir.Field, 0, width+1),
	}
	target.Fields = append(target.Fields, ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true})
	for index := 0; index < width; index++ {
		target.Fields = append(target.Fields, ir.Field{
			Name: fmt.Sprintf("value_%03d", index), GoName: fmt.Sprintf("Value%03d", index),
			Column: fmt.Sprintf("value_%03d", index), Kind: ir.FieldChar, MaxLength: 1,
		})
	}
	before := ir.Model{
		Name: "source", GoName: "Source", DBTable: "wide_source",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
	for index := 0; index < width; index++ {
		before.Fields = append(before.Fields, ir.Field{
			Name: fmt.Sprintf("target_%03d", index), GoName: fmt.Sprintf("Target%03d", index),
			Column: fmt.Sprintf("target_%03d_id", index), Kind: ir.FieldForeignKey, Nullable: true,
			Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "wide", ModelName: "target"}, Cardinality: ir.RelationManyToOne,
				Reverse: ir.ReverseRelation{Name: fmt.Sprintf("sources_%03d", index)}, OnDelete: ir.DeleteProtect,
			},
		})
	}
	added := ir.Field{
		Name: "latest_target", GoName: "LatestTarget", Column: "latest_target_id", Kind: ir.FieldForeignKey, Nullable: true,
		Relation: &ir.ForeignKeyRelation{
			Target: ir.ModelIdentity{AppLabel: "wide", ModelName: "target"}, Cardinality: ir.RelationManyToOne,
			Reverse: ir.ReverseRelation{Name: "latest_sources"}, OnDelete: ir.DeleteProtect,
		},
	}
	after := before.Clone()
	after.Fields = append(after.Fields, added)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0, Kind: migrationbackend.MigrationAddField, Before: before, After: after,
		Targets: []migrationbackend.MigrationTarget{{SourceField: added, TargetModel: target, TargetKey: target.Fields[0]}},
	}}}
	_, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "wide", Name: "0002_latest"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err == nil || !strings.Contains(err.Error(), "derived relation targets") ||
		!strings.Contains(err.Error(), "aggregate relation intent node limit") {
		t.Fatalf("derived expansion resource error = %v", err)
	}
}

func TestSQLiteNullableRelationDerivedTargetExpansionBytesAreBoundedBeforeAllocation(t *testing.T) {
	const relationCount = 40
	target := ir.Model{
		Name: "target", GoName: "Target", DBTable: "wide_byte_target",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{
				Name: "payload", GoName: strings.Repeat("A", 512<<10), Column: "payload",
				Kind: ir.FieldChar, MaxLength: 1,
			},
		},
	}
	before := ir.Model{
		Name: "source", GoName: "Source", DBTable: "wide_byte_source",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
	for index := 0; index < relationCount; index++ {
		before.Fields = append(before.Fields, ir.Field{
			Name: fmt.Sprintf("target_%02d", index), GoName: fmt.Sprintf("Target%02d", index),
			Column: fmt.Sprintf("target_%02d_id", index), Kind: ir.FieldForeignKey, Nullable: true,
			Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "wide", ModelName: "target"}, Cardinality: ir.RelationManyToOne,
				Reverse: ir.ReverseRelation{Name: fmt.Sprintf("byte_sources_%02d", index)}, OnDelete: ir.DeleteProtect,
			},
		})
	}
	added := ir.Field{
		Name: "latest_target", GoName: "LatestTarget", Column: "latest_target_id", Kind: ir.FieldForeignKey, Nullable: true,
		Relation: &ir.ForeignKeyRelation{
			Target: ir.ModelIdentity{AppLabel: "wide", ModelName: "target"}, Cardinality: ir.RelationManyToOne,
			Reverse: ir.ReverseRelation{Name: "latest_byte_sources"}, OnDelete: ir.DeleteProtect,
		},
	}
	after := before.Clone()
	after.Fields = append(after.Fields, added)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0, Kind: migrationbackend.MigrationAddField, Before: before, After: after,
		Targets: []migrationbackend.MigrationTarget{{SourceField: added, TargetModel: target, TargetKey: target.Fields[0]}},
	}}}
	_, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "wide", Name: "0002_latest_bytes"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err == nil || !strings.Contains(err.Error(), "derived relation targets") ||
		!strings.Contains(err.Error(), "aggregate relation intent byte limit") ||
		strings.Contains(err.Error(), "node limit") {
		t.Fatalf("derived expansion byte resource error = %v", err)
	}
}

func TestSQLiteNullableRelationDerivedTargetsDoNotRetainCallerAliases(t *testing.T) {
	target, before, author := sqliteRelationTestModels()
	editor := author.Clone()
	editor.Name, editor.GoName, editor.Column, editor.Nullable = "editor", "Editor", "editor_id", true
	editor.Relation.Reverse.Name = "edited_articles"
	after := before.Clone()
	after.Fields = append(after.Fields, editor)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0, Kind: migrationbackend.MigrationAddField, Before: before, After: after,
		Targets: []migrationbackend.MigrationTarget{{SourceField: editor, TargetModel: target, TargetKey: target.Fields[0]}},
	}}}
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_editor"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err != nil {
		t.Fatalf("validateAndSealSQLiteRelationIntent(): %v", err)
	}
	intent.Operations[0].Before.Fields[2].Column = "mutated_author_id"
	intent.Operations[0].Targets[0].TargetModel.Fields[0].Column = "mutated_id"
	intent.Operations[0].Targets[0].TargetKey.Column = "mutated_id"
	derived := seal.intent.Operations[0].Targets
	if len(derived) != 2 || derived[0].SourceField.Column != "author_id" || derived[1].SourceField.Column != "editor_id" ||
		derived[0].TargetModel.Fields[0].Column != "id" || derived[1].TargetModel.Fields[0].Column != "id" ||
		derived[0].TargetKey.Column != "id" || derived[1].TargetKey.Column != "id" {
		t.Fatalf("sealed derived targets retained caller alias: %+v", derived)
	}
	if err := verifySQLiteRelationIntentSeal(&seal); err != nil {
		t.Fatalf("caller mutation changed sealed intent: %v", err)
	}
}

func TestSQLiteRelationForeignKeysOffUsesSQLiteCapability(t *testing.T) {
	assertSQLiteRelationCapabilityFeature(t, sqliteRelationForeignKeysCapabilityError(0), "sqlite_relation_migration")
}

func TestSQLiteRelationCreateModelRoundTripUsesOneFencedTransaction(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-create-round-trip")
	if err != nil {
		t.Fatalf("OpenMemory(): %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})

	target, source, sourceField := sqliteRelationTestModels()
	targetKey := target.Fields[0]
	applyIntent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          target,
		},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          source,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   targetKey,
			}},
		},
	}}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}

	session := openSQLiteRelationSession(t, backend)
	if records, err := session.ReadAppliedMigrations(ctx); err != nil || len(records) != 0 {
		t.Fatalf("fresh relation snapshot = (%v, %v), want empty", records, err)
	}
	transaction, err := session.BeginMigration(ctx, transition, applyIntent)
	if err != nil {
		t.Fatalf("BeginMigration(apply): %v", err)
	}

	// The backend owns a deep sealed copy after Begin. Mutating every caller
	// alias must not affect the exact operations executed from independent
	// snapshots.
	executeTarget := target.Clone()
	executeSource := source.Clone()
	executeSourceField := executeSource.Fields[2].Clone()
	applyIntent.Operations[0].After.DBTable = "mutated_target"
	applyIntent.Operations[1].After.Fields[2].Column = "mutated_source_id"
	applyIntent.Operations[1].Targets[0].TargetModel.Fields[0].Column = "mutated_key"
	if err := transaction.CreateModel(ctx, executeTarget); err != nil {
		t.Fatalf("CreateModel(target): %v", err)
	}
	if err := transaction.CreateModel(ctx, executeSource); err != nil {
		t.Fatalf("CreateModel(source): %v", err)
	}
	concreteTransaction := transaction.(*sqliteRevisionFencedTransaction)
	if _, err := concreteTransaction.connection.ExecContext(
		ctx,
		`INSERT INTO "news_article" ("title", "author_id") VALUES ('pinned-orphan', 999)`,
	); err == nil {
		t.Fatal("orphan relation insert succeeded on the exact pinned migration connection")
	}
	if err := transaction.RecordApplied(ctx, "news", "0001_relation"); err != nil {
		t.Fatalf("RecordApplied(): %v", err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced(apply) = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(apply session): %v", err)
	}

	var createSQL string
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT "sql" FROM main.sqlite_schema WHERE "type" = 'table' AND "name" = ?`,
		source.DBTable,
	).Scan(&createSQL); err != nil {
		t.Fatalf("read source CREATE TABLE: %v", err)
	}
	wantConstraint := `FOREIGN KEY ("author_id") REFERENCES "news_author" ("id") ON DELETE NO ACTION`
	if !strings.Contains(createSQL, wantConstraint) {
		t.Fatalf("source CREATE TABLE = %q, want constraint %q", createSQL, wantConstraint)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id") VALUES ('orphan', 999)`); err == nil {
		t.Fatal("orphan relation insert succeeded with pinned foreign_keys enforcement")
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada')`); err != nil {
		t.Fatalf("insert target row: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id") VALUES ('valid', 1)`); err != nil {
		t.Fatalf("insert valid relation row: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `DELETE FROM "news_author" WHERE "id" = 1`); err == nil {
		t.Fatal("NO ACTION target delete succeeded while child exists")
	}
	if _, err := backend.ExecContext(ctx, `DELETE FROM "news_article"`); err != nil {
		t.Fatalf("clear child rows: %v", err)
	}

	unapplyIntent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{
			OperationIndex: 1,
			Kind:           migrationbackend.MigrationDeleteModel,
			Before:         executeSource,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: executeSourceField,
				TargetModel: executeTarget,
				TargetKey:   executeTarget.Fields[0],
			}},
		},
		{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationDeleteModel,
			Before:         executeTarget,
		},
	}}
	unapplyTransition := transition
	unapplyTransition.Kind = migrationbackend.HistoryTransitionUnapply
	session = openSQLiteRelationSession(t, backend)
	records, err := session.ReadAppliedMigrations(ctx)
	if err != nil || !reflect.DeepEqual(records, []migrationbackend.AppliedMigration{transition.Migration}) {
		t.Fatalf("unapply snapshot = (%v, %v)", records, err)
	}
	transaction, err = session.BeginMigration(ctx, unapplyTransition, unapplyIntent)
	if err != nil {
		t.Fatalf("BeginMigration(unapply): %v", err)
	}
	if err := transaction.DeleteModel(ctx, executeSource); err != nil {
		t.Fatalf("DeleteModel(child): %v", err)
	}
	if err := transaction.DeleteModel(ctx, executeTarget); err != nil {
		t.Fatalf("DeleteModel(target): %v", err)
	}
	if err := transaction.RecordUnapplied(ctx, "news", "0001_relation"); err != nil {
		t.Fatalf("RecordUnapplied(): %v", err)
	}
	outcome, err = transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced(unapply) = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(unapply session): %v", err)
	}
	for _, table := range []string{executeSource.DBTable, executeTarget.DBTable} {
		var count int
		if err := backend.database.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM main.sqlite_schema WHERE "type" = 'table' AND "name" = ?`,
			table,
		).Scan(&count); err != nil || count != 0 {
			t.Fatalf("final table %q count = (%d, %v), want 0", table, count, err)
		}
	}
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatalf("read final revision snapshot: %v", err)
	}
	if len(snapshot.records) != 0 || snapshot.token.revision != 2 {
		t.Fatalf("final revision snapshot = %+v", snapshot)
	}
}

func TestSQLiteNullableRelationAddUsesSealedSameTargetAndPreservesPopulatedRows(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "nullable-relation-add")
	if err != nil {
		t.Fatalf("OpenMemory(): %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	target, before, authorField := sqliteRelationTestModels()
	initial := migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}
	seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, backend, initial, target, before, authorField)
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada'), ('Grace')`); err != nil {
		t.Fatalf("insert authors: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id") VALUES ('first', 1), ('second', 1)`); err != nil {
		t.Fatalf("insert populated articles: %v", err)
	}
	var sequenceBefore int64
	if err := backend.database.QueryRowContext(ctx, `SELECT "seq" FROM main.sqlite_sequence WHERE "name" = 'news_article'`).Scan(&sequenceBefore); err != nil {
		t.Fatalf("read source sequence before Add: %v", err)
	}

	editor := authorField.Clone()
	editor.Name = "editor"
	editor.GoName = "Editor"
	editor.Column = "editor_id"
	editor.Nullable = true
	editor.Relation.Reverse.Name = "edited_articles"
	after := before.Clone()
	after.Fields = append(after.Fields, editor)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationAddField,
		Before:         before,
		After:          after,
		Targets: []migrationbackend.MigrationTarget{{
			SourceField: editor,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}}}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_editor"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	session := openSQLiteRelationSession(t, backend)
	if records, err := session.ReadAppliedMigrations(ctx); err != nil || !reflect.DeepEqual(records, []migrationbackend.AppliedMigration{initial}) {
		t.Fatalf("pre-Add history = (%v, %v)", records, err)
	}
	transaction, err := session.BeginMigration(ctx, transition, intent)
	if err != nil {
		t.Fatalf("BeginMigration(nullable Add): %v", err)
	}
	// The caller-visible contract remains the changed-field target only. The
	// backend derives its full private source target list without retaining
	// these aliases.
	intent.Operations[0].Targets[0].TargetModel.DBTable = "mutated_target"
	intent.Operations[0].After.Fields[len(intent.Operations[0].After.Fields)-1].Column = "mutated_editor_id"
	if err := transaction.AddField(ctx, before.Clone(), editor.Clone()); err != nil {
		t.Fatalf("AddField(nullable relation): %v", err)
	}
	if err := transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name); err != nil {
		t.Fatalf("RecordApplied(nullable relation): %v", err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced(nullable relation) = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(nullable Add session): %v", err)
	}

	var createSQL string
	if err := backend.database.QueryRowContext(ctx, `SELECT "sql" FROM main.sqlite_schema WHERE "type"='table' AND "name"='news_article'`).Scan(&createSQL); err != nil {
		t.Fatalf("read post-Add CREATE SQL: %v", err)
	}
	wantSQL := `CREATE TABLE "news_article" (` +
		`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, ` +
		`"title" VARCHAR(200) NOT NULL, ` +
		`"author_id" INTEGER NOT NULL, ` +
		`"editor_id" INTEGER NULL REFERENCES "news_author" ("id") ON DELETE NO ACTION, ` +
		`FOREIGN KEY ("author_id") REFERENCES "news_author" ("id") ON DELETE NO ACTION)`
	if createSQL != wantSQL {
		t.Fatalf("post-Add CREATE SQL = %q, want exact %q", createSQL, wantSQL)
	}
	foreignKeys, err := readSQLiteRelationForeignKeys(ctx, backend.database, before.DBTable, 2)
	if err != nil {
		t.Fatalf("read post-Add foreign keys: %v", err)
	}
	bySource := make(map[string]sqliteRelationPhysicalForeignKey, len(foreignKeys))
	for _, foreignKey := range foreignKeys {
		bySource[foreignKey.from] = foreignKey
	}
	for _, column := range []string{"author_id", "editor_id"} {
		foreignKey, exists := bySource[column]
		if !exists || foreignKey.table != "news_author" || foreignKey.to != "id" ||
			foreignKey.onUpdate != "NO ACTION" || foreignKey.onDelete != "NO ACTION" ||
			foreignKey.match != "NONE" || foreignKey.sequence != 0 {
			t.Fatalf("post-Add foreign key %q = %+v", column, foreignKey)
		}
	}
	rows, err := backend.database.QueryContext(ctx, `SELECT "id", "title", "author_id", "editor_id" FROM "news_article" ORDER BY "id"`)
	if err != nil {
		t.Fatalf("query populated rows after Add: %v", err)
	}
	defer rows.Close()
	wantRows := []struct {
		id     int64
		title  string
		author int64
	}{{1, "first", 1}, {2, "second", 1}}
	for index := range wantRows {
		if !rows.Next() {
			t.Fatalf("populated rows ended at %d", index)
		}
		var id, author int64
		var title string
		var editorID sql.NullInt64
		if err := rows.Scan(&id, &title, &author, &editorID); err != nil {
			t.Fatalf("scan populated row %d: %v", index, err)
		}
		if id != wantRows[index].id || title != wantRows[index].title || author != wantRows[index].author || editorID.Valid {
			t.Fatalf("populated row %d = (%d,%q,%d,%v)", index, id, title, author, editorID)
		}
	}
	if extra := rows.Next(); extra || rows.Err() != nil {
		t.Fatalf("unexpected populated row tail: next=%t err=%v", extra, rows.Err())
	}
	var sequenceAfter int64
	if err := backend.database.QueryRowContext(ctx, `SELECT "seq" FROM main.sqlite_sequence WHERE "name" = 'news_article'`).Scan(&sequenceAfter); err != nil || sequenceAfter != sequenceBefore {
		t.Fatalf("source sequence after Add = (%d, %v), want %d", sequenceAfter, err, sequenceBefore)
	}
	if _, err := backend.ExecContext(ctx, `UPDATE "news_article" SET "editor_id"=2 WHERE "id"=1`); err != nil {
		t.Fatalf("set valid editor relation: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `UPDATE "news_article" SET "editor_id"=999 WHERE "id"=2`); err == nil {
		t.Fatal("orphan editor relation update succeeded")
	}

	// A fresh session revalidates the durable history. Reverse Remove receives
	// only the changed-field public target and uses its sealed private authority.
	session = openSQLiteRelationSession(t, backend)
	if records, err := session.ReadAppliedMigrations(ctx); err != nil || !reflect.DeepEqual(records, []migrationbackend.AppliedMigration{initial, transition.Migration}) {
		t.Fatalf("reopened Add history = (%v, %v)", records, err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	connectionCalls := 0
	concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
		connectionCalls++
		return connection
	}
	removeBefore := before.Clone()
	removeBefore.Fields = append(removeBefore.Fields, editor.Clone())
	remove := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationRemoveField,
		Before:         removeBefore,
		After:          before,
		Targets: []migrationbackend.MigrationTarget{{
			SourceField: editor,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}}}
	removeTransition := transition
	removeTransition.Kind = migrationbackend.HistoryTransitionUnapply
	transaction, err = session.BeginMigration(ctx, removeTransition, remove)
	if err != nil {
		t.Fatalf("BeginMigration(reverse Remove): %v", err)
	}
	if err := transaction.RemoveField(ctx, removeBefore.Clone(), editor.Clone()); err != nil {
		t.Fatalf("RemoveField(reverse relation remake): %v", err)
	}
	if err := transaction.RecordUnapplied(ctx, removeTransition.Migration.App, removeTransition.Migration.Name); err != nil {
		t.Fatalf("RecordUnapplied(reverse relation remake): %v", err)
	}
	outcome, err = transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced(reverse relation remake) = (%+v, %v)", outcome, err)
	}
	if connectionCalls != 1 || concrete.active != nil || concrete.state != revisionSessionReady {
		t.Fatalf("reverse Remove session state: calls=%d active=%v state=%d", connectionCalls, concrete.active, concrete.state)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(reopened Remove session): %v", err)
	}
	var retained int64
	if err := backend.database.QueryRowContext(ctx, `SELECT "author_id" FROM "news_article" WHERE "id"=1`).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("row 1 author after relation remake = (%d, %v)", retained, err)
	}
	var removedColumns int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_xinfo('news_article') WHERE "name"='editor_id'`).Scan(&removedColumns); err != nil || removedColumns != 0 {
		t.Fatalf("removed editor column count = (%d, %v)", removedColumns, err)
	}
}

func TestSQLiteRequiredRelationAddToEmptyTableUsesNativeNotNullForeignKey(t *testing.T) {
	ctx := context.Background()
	database, err := OpenMemory(ctx, "required-relation-add-empty")
	if err != nil {
		t.Fatalf("OpenMemory(): %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	target, before, author := sqliteRelationTestModels()
	initial := migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}
	seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, database, initial, target, before, author)
	if _, err := database.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada'), ('Grace')`); err != nil {
		t.Fatalf("seed target rows: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id") VALUES ('deleted', 1)`); err != nil {
		t.Fatalf("seed source sequence: %v", err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM "news_article"`); err != nil {
		t.Fatalf("empty required Add source: %v", err)
	}
	var sourceSequenceBefore int64
	if err := database.database.QueryRowContext(ctx,
		`SELECT "seq" FROM main.sqlite_sequence WHERE "name"='news_article'`,
	).Scan(&sourceSequenceBefore); err != nil {
		t.Fatalf("read empty source sequence: %v", err)
	}
	reviewer := author.Clone()
	reviewer.Name, reviewer.GoName, reviewer.Column = "reviewer", "Reviewer", "reviewer_id"
	reviewer.Relation.Reverse.Name = "reviewed_articles"
	after := before.Clone()
	after.Fields = append(after.Fields, reviewer)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0, Kind: migrationbackend.MigrationAddField,
		Before: before, After: after,
		Targets: []migrationbackend.MigrationTarget{{
			SourceField: reviewer, TargetModel: target, TargetKey: target.Fields[0],
		}},
	}}}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_reviewer"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	session := openSQLiteRelationSession(t, database)
	if records, err := session.ReadAppliedMigrations(ctx); err != nil ||
		!reflect.DeepEqual(records, []migrationbackend.AppliedMigration{initial}) {
		t.Fatalf("pre-Add history = (%v, %v)", records, err)
	}
	transaction, err := session.BeginMigration(ctx, transition, intent)
	if err != nil {
		t.Fatalf("BeginMigration(required Add): %v", err)
	}
	if err := transaction.AddField(ctx, before.Clone(), reviewer.Clone()); err != nil {
		t.Fatalf("AddField(required relation): %v", err)
	}
	if err := transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name); err != nil {
		t.Fatalf("RecordApplied(required relation): %v", err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced(required relation) = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(required Add session): %v", err)
	}

	var createSQL string
	if err := database.database.QueryRowContext(ctx,
		`SELECT "sql" FROM main.sqlite_schema WHERE "type"='table' AND "name"='news_article'`,
	).Scan(&createSQL); err != nil {
		t.Fatalf("read post-Add CREATE SQL: %v", err)
	}
	wantSQL := `CREATE TABLE "news_article" (` +
		`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, ` +
		`"title" VARCHAR(200) NOT NULL, ` +
		`"author_id" INTEGER NOT NULL, ` +
		`"reviewer_id" INTEGER NOT NULL REFERENCES "news_author" ("id") ON DELETE NO ACTION, ` +
		`FOREIGN KEY ("author_id") REFERENCES "news_author" ("id") ON DELETE NO ACTION)`
	if createSQL != wantSQL {
		t.Fatalf("required Add CREATE SQL = %q, want exact %q", createSQL, wantSQL)
	}
	foreignKeys, err := readSQLiteRelationForeignKeys(ctx, database.database, before.DBTable, 2)
	if err != nil {
		t.Fatalf("read required Add foreign keys: %v", err)
	}
	bySource := make(map[string]sqliteRelationPhysicalForeignKey, len(foreignKeys))
	for _, foreignKey := range foreignKeys {
		bySource[foreignKey.from] = foreignKey
	}
	for _, column := range []string{"author_id", "reviewer_id"} {
		foreignKey, exists := bySource[column]
		if !exists || foreignKey.table != "news_author" || foreignKey.to != "id" ||
			foreignKey.sequence != 0 || foreignKey.onUpdate != "NO ACTION" ||
			foreignKey.onDelete != "NO ACTION" || foreignKey.match != "NONE" {
			t.Fatalf("required Add foreign key %q = %+v", column, foreignKey)
		}
	}
	var sourceSequenceAfter int64
	if err := database.database.QueryRowContext(ctx,
		`SELECT "seq" FROM main.sqlite_sequence WHERE "name"='news_article'`,
	).Scan(&sourceSequenceAfter); err != nil || sourceSequenceAfter != sourceSequenceBefore {
		t.Fatalf("required Add source sequence = (%d, %v), want %d", sourceSequenceAfter, err, sourceSequenceBefore)
	}
	var notNull int
	rows, err := database.database.QueryContext(ctx, `PRAGMA main.table_xinfo("news_article")`)
	if err != nil {
		t.Fatalf("table_xinfo: %v", err)
	}
	for rows.Next() {
		var cid, columnNotNull, primaryKey, hidden int
		var name, fieldType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &fieldType, &columnNotNull, &defaultValue, &primaryKey, &hidden); err != nil {
			_ = rows.Close()
			t.Fatalf("scan table_xinfo: %v", err)
		}
		if name == "reviewer_id" {
			notNull = columnNotNull
		}
	}
	if err := rows.Close(); err != nil || notNull != 1 {
		t.Fatalf("required reviewer notnull = %d, close=%v", notNull, err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO "news_article" ("title", "author_id", "reviewer_id") VALUES ('valid', 1, 2)`,
	); err != nil {
		t.Fatalf("insert valid required relation: %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO "news_article" ("title", "author_id", "reviewer_id") VALUES ('null', 1, NULL)`,
	); err == nil || !strings.Contains(strings.ToLower(err.Error()), "not null") {
		t.Fatalf("NULL required relation insert error = %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO "news_article" ("title", "author_id", "reviewer_id") VALUES ('orphan', 1, 999)`,
	); err == nil || !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("orphan required relation insert error = %v", err)
	}
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, database)
	if err != nil || snapshot.token.revision != 2 ||
		!reflect.DeepEqual(snapshot.records, []migrationbackend.AppliedMigration{initial, transition.Migration}) {
		t.Fatalf("required Add revision snapshot = (%+v, %v)", snapshot, err)
	}
}

func TestSQLiteRequiredRelationAddRejectsPopulatedSourceBeforeRevisionClaim(t *testing.T) {
	ctx := context.Background()
	database, err := OpenMemory(ctx, "required-relation-add-populated")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	target, before, author := sqliteRelationTestModels()
	initial := migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}
	seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, database, initial, target, before, author)
	if _, err := database.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id") VALUES ('existing', 1)`); err != nil {
		t.Fatal(err)
	}
	beforeSnapshot, err := readAtomicMigrationRevisionSnapshot(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := author.Clone()
	reviewer.Name, reviewer.GoName, reviewer.Column = "reviewer", "Reviewer", "reviewer_id"
	reviewer.Relation.Reverse.Name = "reviewed_articles"
	after := before.Clone()
	after.Fields = append(after.Fields, reviewer)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0, Kind: migrationbackend.MigrationAddField, Before: before, After: after,
		Targets: []migrationbackend.MigrationTarget{{SourceField: reviewer, TargetModel: target, TargetKey: target.Fields[0]}},
	}}}
	session := openSQLiteRelationSession(t, database)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_reviewer"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if transaction != nil || err == nil {
		t.Fatalf("Begin required Add on populated source = (%v, %v)", transaction, err)
	}
	var capability *migrationbackend.CapabilityError
	if !errors.As(err, &capability) || capability.Feature != "sqlite_relation_migration" ||
		!strings.Contains(capability.Detail, "contains rows") {
		t.Fatalf("populated required Add error = %#v (%v)", capability, err)
	}
	wantCheckpoints := []sqliteRelationBeginCheckpoint{
		sqliteRelationCheckpointForeignKeysSet,
		sqliteRelationCheckpointForeignKeysRead,
		sqliteRelationCheckpointTransactionBegun,
	}
	if !reflect.DeepEqual(checkpoints, wantCheckpoints) {
		t.Fatalf("populated required Add checkpoints = %v, want %v", checkpoints, wantCheckpoints)
	}
	afterSnapshot, snapshotErr := readAtomicMigrationRevisionSnapshot(ctx, database)
	if snapshotErr != nil || !reflect.DeepEqual(afterSnapshot, beforeSnapshot) {
		t.Fatalf("populated required Add changed durable snapshot: before=%+v after=%+v err=%v", beforeSnapshot, afterSnapshot, snapshotErr)
	}
	if sqliteRelationTestColumnExists(t, database, before.DBTable, reviewer.Column) {
		t.Fatal("populated required Add created reviewer column")
	}
	if closeErr := session.Close(ctx); closeErr != nil {
		t.Fatal(closeErr)
	}
}

func TestSQLiteRequiredRelationAddTreatsSameIntentCreatedSourceAsStaticallyEmpty(t *testing.T) {
	ctx := context.Background()
	database, err := OpenMemory(ctx, "required-relation-add-same-intent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	target, _, author := sqliteRelationTestModels()
	before := ir.Model{
		Name: "article", GoName: "Article", DBTable: "news_article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
		},
	}
	reviewer := author.Clone()
	reviewer.Name, reviewer.GoName, reviewer.Column = "reviewer", "Reviewer", "reviewer_id"
	reviewer.Relation.Reverse.Name = "reviewed_articles"
	after := before.Clone()
	after.Fields = append(after.Fields, reviewer)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{OperationIndex: 0, Kind: migrationbackend.MigrationCreateModel, After: target},
		{OperationIndex: 1, Kind: migrationbackend.MigrationCreateModel, After: before},
		{
			OperationIndex: 2, Kind: migrationbackend.MigrationAddField, Before: before, After: after,
			Targets: []migrationbackend.MigrationTarget{{SourceField: reviewer, TargetModel: target, TargetKey: target.Fields[0]}},
		},
	}}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_required"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	session := openSQLiteRelationSession(t, database)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	emptyProbe := &sqliteRelationBeginFaultConnection{
		method: "query_row", contains: "SELECT EXISTS", remaining: 1,
	}
	concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
		emptyProbe.migrationPinnedConnection = connection
		return emptyProbe
	}
	transaction, err := session.BeginMigration(ctx, transition, intent)
	if err != nil {
		t.Fatalf("BeginMigration(same-intent required Add): %v", err)
	}
	if emptyProbe.remaining != 1 {
		t.Fatal("same-intent created source performed a physical emptiness query")
	}
	if err := transaction.CreateModel(ctx, target.Clone()); err != nil {
		t.Fatal(err)
	}
	if err := transaction.CreateModel(ctx, before.Clone()); err != nil {
		t.Fatal(err)
	}
	if err := transaction.AddField(ctx, before.Clone(), reviewer.Clone()); err != nil {
		t.Fatal(err)
	}
	if err := transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name); err != nil {
		t.Fatal(err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced(same-intent required Add) = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !sqliteRelationTestColumnExists(t, database, before.DBTable, reviewer.Column) {
		t.Fatal("same-intent required Add did not create reviewer column")
	}
}

func TestSQLiteNullableRelationAddFaultsRollbackExactDurableSnapshot(t *testing.T) {
	tests := []struct {
		name             string
		method           string
		contains         string
		corruptCanonical bool
		owner            string
		wantDetail       string
		wantAlterCalls   int
		wantFKChecks     int
		wantRecorder     int
	}{
		{
			name:           "alter",
			method:         "exec",
			contains:       `ALTER TABLE "main"."news_article"`,
			owner:          "AddField",
			wantAlterCalls: 1,
		},
		{
			name:             "final canonical",
			corruptCanonical: true,
			owner:            "AddField",
			wantDetail:       "canonical declaration",
			wantAlterCalls:   1,
		},
		{
			name:           "final foreign key check",
			method:         "query",
			contains:       "foreign_key_check",
			owner:          "AddField",
			wantAlterCalls: 1,
			wantFKChecks:   1,
		},
		{
			name:           "recorder",
			method:         "exec",
			contains:       `INSERT INTO "godj_migrations"`,
			owner:          "RecordApplied",
			wantAlterCalls: 1,
			wantFKChecks:   1,
			wantRecorder:   1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "nullable-add-fault.sqlite")
			open := func() *Backend {
				t.Helper()
				backend, err := Open(ctx, "file:"+filepath.ToSlash(path)+"?mode=rwc")
				if err != nil {
					t.Fatalf("Open(nullable Add fault database): %v", err)
				}
				return backend
			}

			target, before, authorField := sqliteRelationTestModels()
			initial := migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}
			backend := open()
			seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, backend, initial, target, before, authorField)
			if _, err := backend.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada'), ('Grace')`); err != nil {
				t.Fatalf("seed nullable Add fault authors: %v", err)
			}
			if _, err := backend.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id") VALUES ('first', 1), ('second', 2)`); err != nil {
				t.Fatalf("seed nullable Add fault articles: %v", err)
			}
			if err := backend.Close(); err != nil {
				t.Fatalf("close nullable Add fault seed: %v", err)
			}
			beforeSnapshot := readSQLiteNullableRelationAddSnapshot(t, path)
			assertSQLiteNullableRelationAddSeedSnapshot(t, beforeSnapshot, initial)

			editor := authorField.Clone()
			editor.Name, editor.GoName, editor.Column, editor.Nullable = "editor", "Editor", "editor_id", true
			editor.Relation.Reverse.Name = "edited_articles"
			after := before.Clone()
			after.Fields = append(after.Fields, editor)
			intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
				OperationIndex: 0,
				Kind:           migrationbackend.MigrationAddField,
				Before:         before,
				After:          after,
				Targets: []migrationbackend.MigrationTarget{{
					SourceField: editor,
					TargetModel: target,
					TargetKey:   target.Fields[0],
				}},
			}}}
			transition := migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_editor"},
				Kind:      migrationbackend.HistoryTransitionApply,
			}

			backend = open()
			backendClosed := false
			defer func() {
				if !backendClosed {
					_ = backend.Close()
				}
			}()
			session := openSQLiteRelationSession(t, backend)
			if records, err := session.ReadAppliedMigrations(ctx); err != nil ||
				!reflect.DeepEqual(records, []migrationbackend.AppliedMigration{initial}) {
				t.Fatalf("read nullable Add fault history = (%v, %v)", records, err)
			}
			concrete := session.(*sqliteRevisionFencedSession)
			var faultErr error
			if test.method != "" {
				faultErr = fmt.Errorf("nullable Add %s raw fault", test.name)
			}
			var boundary *sqliteNullableRelationAddFaultConnection
			connectionHooks := 0
			var checkpoints []sqliteRelationBeginCheckpoint
			concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
				checkpoints = append(checkpoints, checkpoint)
			}
			concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
				connectionHooks++
				boundary = &sqliteNullableRelationAddFaultConnection{
					migrationPinnedConnection: connection,
					method:                    test.method,
					contains:                  test.contains,
					faultErr:                  faultErr,
					remaining:                 1,
					corruptCanonical:          test.corruptCanonical,
				}
				return boundary
			}
			transaction, err := session.BeginMigration(ctx, transition, intent)
			if err != nil {
				t.Fatalf("BeginMigration(nullable Add %s): %v", test.name, err)
			}
			if connectionHooks != 1 || boundary == nil {
				t.Fatalf("nullable Add %s connection hooks = %d boundary=%v", test.name, connectionHooks, boundary)
			}
			wantCheckpoints := []sqliteRelationBeginCheckpoint{
				sqliteRelationCheckpointForeignKeysSet,
				sqliteRelationCheckpointForeignKeysRead,
				sqliteRelationCheckpointTransactionBegun,
				sqliteRelationCheckpointPhysicalPreflightComplete,
				sqliteRelationCheckpointRevisionClaimStarting,
				sqliteRelationCheckpointRevisionClaimed,
			}
			if !reflect.DeepEqual(checkpoints, wantCheckpoints) {
				t.Fatalf("nullable Add %s Begin checkpoints = %v, want %v", test.name, checkpoints, wantCheckpoints)
			}

			addErr := transaction.AddField(ctx, before.Clone(), editor.Clone())
			var ownedErr error
			switch test.owner {
			case "AddField":
				if addErr == nil {
					t.Fatalf("AddField(nullable Add %s) succeeded", test.name)
				}
				ownedErr = addErr
			case "RecordApplied":
				if addErr != nil {
					t.Fatalf("AddField(nullable Add recorder fault): %v", addErr)
				}
				ownedErr = transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name)
				if ownedErr == nil {
					t.Fatal("RecordApplied(nullable Add recorder fault) succeeded")
				}
			default:
				t.Fatalf("unknown nullable Add fault owner %q", test.owner)
			}
			wantDetail := test.wantDetail
			if faultErr != nil {
				wantDetail = faultErr.Error()
			}
			if !strings.Contains(ownedErr.Error(), wantDetail) {
				t.Fatalf("%s(nullable Add %s) error = %v, want detail %q", test.owner, test.name, ownedErr, wantDetail)
			}
			if faultErr != nil && (!errors.Is(ownedErr, faultErr) || boundary.remaining != 0) {
				t.Fatalf("nullable Add %s raw fault = errors.Is:%t remaining:%d", test.name, errors.Is(ownedErr, faultErr), boundary.remaining)
			}
			var fenceError *migrationbackend.RevisionFenceError
			if errors.As(ownedErr, &fenceError) {
				t.Fatalf("nullable Add %s raw fault was reclassified as revision fence error: %#v", test.name, fenceError)
			}
			wantCanonicalCorruptions := 0
			if test.corruptCanonical {
				wantCanonicalCorruptions = 1
			}
			wantCounts := [4]int{test.wantAlterCalls, test.wantFKChecks, test.wantRecorder, wantCanonicalCorruptions}
			if got := boundary.operationCounts(); got != wantCounts {
				t.Fatalf("nullable Add %s operation counts = %v, want %v", test.name, got, wantCounts)
			}

			// A failed owner poisons the transaction. Repeating that exact API call
			// must return the sticky failure without executing SQL or consuming a
			// second fault.
			var retryErr error
			if test.owner == "AddField" {
				retryErr = transaction.AddField(ctx, before.Clone(), editor.Clone())
			} else {
				retryErr = transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name)
			}
			if retryErr == nil || retryErr.Error() != ownedErr.Error() {
				t.Fatalf("%s(nullable Add %s retry) = %v, want sticky %v", test.owner, test.name, retryErr, ownedErr)
			}
			if got := boundary.operationCounts(); got != wantCounts {
				t.Fatalf("nullable Add %s retry executed SQL: counts=%v want=%v", test.name, got, wantCounts)
			}
			if err := transaction.Rollback(ctx); err != nil {
				t.Fatalf("Rollback(nullable Add %s): %v", test.name, err)
			}
			if boundary.rollbackCalls != 1 {
				t.Fatalf("nullable Add %s rollback calls = %d, want 1", test.name, boundary.rollbackCalls)
			}
			if err := session.Close(ctx); err != nil {
				t.Fatalf("Close(nullable Add %s session): %v", test.name, err)
			}
			if err := backend.Close(); err != nil {
				t.Fatalf("Close(nullable Add %s backend): %v", test.name, err)
			}
			backendClosed = true

			afterSnapshot := readSQLiteNullableRelationAddSnapshot(t, path)
			if !reflect.DeepEqual(afterSnapshot, beforeSnapshot) {
				t.Fatalf("nullable Add %s changed reopened durable snapshot:\nbefore=%+v\nafter=%+v", test.name, beforeSnapshot, afterSnapshot)
			}
		})
	}
}

func TestSQLiteLoadedNullableRelationAddFaultTaxonomyAndRollback(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		contains  string
		category  migrations.ErrorCategory
		code      migrations.ErrorCode
		operation int
		kind      string
	}{
		{
			name:      "alter",
			method:    "exec",
			contains:  `ALTER TABLE "main"."news_article"`,
			category:  migrations.CategoryExecution,
			code:      migrations.CodeOperationFailed,
			operation: 0,
			kind:      "AddField",
		},
		{
			name:      "final foreign key check",
			method:    "query",
			contains:  "foreign_key_check",
			category:  migrations.CategoryExecution,
			code:      migrations.CodeOperationFailed,
			operation: 0,
			kind:      "AddField",
		},
		{
			name:      "recorder",
			method:    "exec",
			contains:  `INSERT INTO "godj_migrations"`,
			category:  migrations.CategoryRecorder,
			code:      migrations.CodeRecordFailed,
			operation: migrations.NoOperation,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "loaded-nullable-add-fault.sqlite")
			loaded := loadSQLiteLoadedNullableRelationAddSet(t, test.name)
			database := openSQLiteLoadedRelationTaxonomyBackend(t, path)
			seedState, err := (migrations.Executor{Backend: database}).Migrate(
				ctx,
				loaded,
				migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{
					App: "news", Name: "0002_article",
				})),
			)

			if err != nil {
				t.Fatalf("Migrate(nullable Add taxonomy seed): %v", err)
			}
			assertSQLiteLoadedNullableRelationAddSeedState(t, seedState)
			if _, err := database.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada'), ('Grace')`); err != nil {
				t.Fatalf("insert nullable Add taxonomy authors: %v", err)
			}
			if _, err := database.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id") VALUES ('first', 1), ('second', 2)`); err != nil {
				t.Fatalf("insert nullable Add taxonomy articles: %v", err)
			}
			if err := database.Close(); err != nil {
				t.Fatalf("close nullable Add taxonomy seed: %v", err)
			}
			before := readSQLiteNullableRelationAddSnapshot(t, path)
			assertSQLiteLoadedNullableRelationAddSeedSnapshot(t, before)

			database = openSQLiteLoadedRelationTaxonomyBackend(t, path)
			cause := fmt.Errorf("loaded nullable Add %s raw fault", test.name)
			connectionFault := &sqliteRelationBeginFaultConnection{
				method: test.method, contains: test.contains, remaining: 1, faultErr: cause,
			}
			probe := &sqliteLoadedRelationTaxonomyBackend{Backend: database, fault: connectionFault}
			state, err := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
			var migrationError *migrations.Error
			if !errors.As(err, &migrationError) || migrationError == nil ||
				migrationError.Category != test.category || migrationError.Code != test.code ||
				migrationError.Direction != migrations.DirectionForward || migrationError.App != "news" ||
				migrationError.Migration != "0003_editor" || migrationError.OperationIndex != test.operation ||
				migrationError.Operation != test.kind || migrationError.RollbackCause != nil ||
				!errors.Is(err, cause) {
				t.Fatalf(
					"%s nullable Add taxonomy = %#v (%v), want %s/%s forward news.0003_editor operation[%d]=%q raw cause",
					test.name,
					migrationError,
					err,
					test.category,
					test.code,
					test.operation,
					test.kind,
				)
			}
			var fenceError *migrationbackend.RevisionFenceError
			if errors.As(migrationError.Cause, &fenceError) {
				t.Fatalf("%s nullable Add fault was reclassified as revision fence error: %#v", test.name, fenceError)
			}
			if !state.Equal(seedState) {
				t.Fatalf("%s nullable Add returned state differs from exact pre-step state", test.name)
			}
			assertSQLiteLoadedNullableRelationAddIntent(t, test.name, probe.transition, probe.intent)
			wantCheckpoints := []sqliteRelationBeginCheckpoint{
				sqliteRelationCheckpointForeignKeysSet,
				sqliteRelationCheckpointForeignKeysRead,
				sqliteRelationCheckpointTransactionBegun,
				sqliteRelationCheckpointPhysicalPreflightComplete,
				sqliteRelationCheckpointRevisionClaimStarting,
				sqliteRelationCheckpointRevisionClaimed,
			}
			if !reflect.DeepEqual(probe.checkpoints, wantCheckpoints) {
				t.Fatalf("%s nullable Add checkpoints = %v, want %v", test.name, probe.checkpoints, wantCheckpoints)
			}
			if connectionFault.remaining != 0 || connectionFault.closeCalls != 1 ||
				connectionFault.rawCalls != 0 || connectionFault.rollbackCalls != 1 {
				t.Fatalf(
					"%s nullable Add connection = remaining:%d close:%d raw:%d rollback:%d, want 0/1/0/1",
					test.name,
					connectionFault.remaining,
					connectionFault.closeCalls,
					connectionFault.rawCalls,
					connectionFault.rollbackCalls,
				)
			}
			if probe.capabilityCalls != 1 || probe.openCalls != 1 || probe.readCalls != 1 ||
				probe.beginCalls != 1 || probe.closeCalls != 1 || probe.connectionHookCalls != 1 ||
				probe.transactionRollbackCalls != 1 {
				t.Fatalf(
					"%s nullable Add lifecycle = capability:%d open:%d read:%d begin:%d close:%d hook:%d rollback:%d, want all 1",
					test.name,
					probe.capabilityCalls,
					probe.openCalls,
					probe.readCalls,
					probe.beginCalls,
					probe.closeCalls,
					probe.connectionHookCalls,
					probe.transactionRollbackCalls,
				)
			}
			if stats := database.database.Stats(); stats.InUse != 0 {
				t.Fatalf("%s nullable Add database in-use connections = %d, want 0", test.name, stats.InUse)
			}
			if err := database.Close(); err != nil {
				t.Fatalf("close nullable Add taxonomy fault database: %v", err)
			}
			after := readSQLiteNullableRelationAddSnapshot(t, path)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("%s nullable Add changed reopened durable snapshot:\nbefore=%+v\nafter=%+v", test.name, before, after)
			}
		})
	}
}

func TestSQLiteLoadedRequiredRelationAddFaultTaxonomyAndRollback(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		contains    string
		category    migrations.ErrorCategory
		code        migrations.ErrorCode
		operation   int
		kind        string
		preflight   bool
		wantRawText string
	}{
		{
			name: "empty source query", method: "query_row", contains: "SELECT EXISTS",
			category: migrations.CategoryTransaction, code: migrations.CodeBeginFailed,
			operation: migrations.NoOperation, preflight: true, wantRawText: "__godj_relation_begin_fault__",
		},
		{
			name: "alter", method: "exec", contains: `ALTER TABLE "main"."news_article"`,
			category: migrations.CategoryExecution, code: migrations.CodeOperationFailed,
			operation: 0, kind: "AddField",
		},
		{
			name: "final foreign key check", method: "query", contains: "foreign_key_check",
			category: migrations.CategoryExecution, code: migrations.CodeOperationFailed,
			operation: 0, kind: "AddField",
		},
		{
			name: "recorder", method: "exec", contains: `INSERT INTO "godj_migrations"`,
			category: migrations.CategoryRecorder, code: migrations.CodeRecordFailed,
			operation: migrations.NoOperation,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "loaded-required-add-fault.sqlite")
			loaded := loadSQLiteLoadedRequiredRelationAddSet(t, test.name)
			database := openSQLiteLoadedRelationTaxonomyBackend(t, path)
			seedState, err := (migrations.Executor{Backend: database}).Migrate(
				ctx,
				loaded,
				migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{
					App: "news", Name: "0002_article",
				})),
			)

			if err != nil {
				t.Fatalf("Migrate(required Add taxonomy seed): %v", err)
			}
			assertSQLiteLoadedNullableRelationAddSeedState(t, seedState)
			if _, err := database.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada')`); err != nil {
				t.Fatalf("insert required Add taxonomy target: %v", err)
			}
			if err := database.Close(); err != nil {
				t.Fatalf("close required Add taxonomy seed: %v", err)
			}
			before := readSQLiteNullableRelationAddSnapshot(t, path)

			database = openSQLiteLoadedRelationTaxonomyBackend(t, path)
			cause := fmt.Errorf("loaded required Add %s raw fault", test.name)
			connectionFault := &sqliteRelationBeginFaultConnection{
				method: test.method, contains: test.contains, remaining: 1, faultErr: cause,
			}
			probe := &sqliteLoadedRelationTaxonomyBackend{Backend: database, fault: connectionFault}
			state, err := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
			var migrationError *migrations.Error
			if !errors.As(err, &migrationError) || migrationError == nil ||
				migrationError.Category != test.category || migrationError.Code != test.code ||
				migrationError.Direction != migrations.DirectionForward || migrationError.App != "news" ||
				migrationError.Migration != "0003_editor" || migrationError.OperationIndex != test.operation ||
				migrationError.Operation != test.kind || migrationError.RollbackCause != nil {
				t.Fatalf("%s required Add taxonomy = %#v (%v)", test.name, migrationError, err)
			}
			if test.wantRawText != "" {
				if !strings.Contains(err.Error(), test.wantRawText) {
					t.Fatalf("%s required Add preflight error = %v, want %q", test.name, err, test.wantRawText)
				}
			} else if !errors.Is(err, cause) {
				t.Fatalf("%s required Add lost raw cause: %v", test.name, err)
			}
			var fenceError *migrationbackend.RevisionFenceError
			if errors.As(migrationError.Cause, &fenceError) {
				t.Fatalf("%s required Add fault was reclassified as revision fence error: %#v", test.name, fenceError)
			}
			if !state.Equal(seedState) {
				t.Fatalf("%s required Add returned state differs from exact pre-step state", test.name)
			}
			assertSQLiteLoadedRequiredRelationAddIntent(t, test.name, probe.transition, probe.intent)
			wantCheckpoints := []sqliteRelationBeginCheckpoint{
				sqliteRelationCheckpointForeignKeysSet,
				sqliteRelationCheckpointForeignKeysRead,
				sqliteRelationCheckpointTransactionBegun,
			}
			wantTransactionRollbacks := 0
			if !test.preflight {
				wantCheckpoints = append(wantCheckpoints,
					sqliteRelationCheckpointPhysicalPreflightComplete,
					sqliteRelationCheckpointRevisionClaimStarting,
					sqliteRelationCheckpointRevisionClaimed,
				)
				wantTransactionRollbacks = 1
			}
			if !reflect.DeepEqual(probe.checkpoints, wantCheckpoints) {
				t.Fatalf("%s required Add checkpoints = %v, want %v", test.name, probe.checkpoints, wantCheckpoints)
			}
			if connectionFault.remaining != 0 || connectionFault.closeCalls != 1 ||
				connectionFault.rawCalls != 0 || connectionFault.rollbackCalls != 1 {
				t.Fatalf("%s required Add connection = remaining:%d close:%d raw:%d rollback:%d",
					test.name, connectionFault.remaining, connectionFault.closeCalls,
					connectionFault.rawCalls, connectionFault.rollbackCalls)
			}
			if probe.capabilityCalls != 1 || probe.openCalls != 1 || probe.readCalls != 1 ||
				probe.beginCalls != 1 || probe.closeCalls != 1 || probe.connectionHookCalls != 1 ||
				probe.transactionRollbackCalls != wantTransactionRollbacks {
				t.Fatalf("%s required Add lifecycle = capability:%d open:%d read:%d begin:%d close:%d hook:%d rollback:%d",
					test.name, probe.capabilityCalls, probe.openCalls, probe.readCalls, probe.beginCalls,
					probe.closeCalls, probe.connectionHookCalls, probe.transactionRollbackCalls)
			}
			if err := database.Close(); err != nil {
				t.Fatalf("close required Add taxonomy fault database: %v", err)
			}
			after := readSQLiteNullableRelationAddSnapshot(t, path)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("%s required Add changed durable snapshot:\nbefore=%+v\nafter=%+v", test.name, before, after)
			}
		})
	}
}

func TestSQLiteNullableRelationAddRejectsUnsealedAuthorityBeforeClaim(t *testing.T) {
	target, before, authorField := sqliteRelationTestModels()
	baseEditor := authorField.Clone()
	baseEditor.Name = "editor"
	baseEditor.GoName = "Editor"
	baseEditor.Column = "editor_id"
	baseEditor.Nullable = true
	baseEditor.Relation.Reverse.Name = "edited_articles"
	addIntent := func(field ir.Field, targetModel ir.Model, targetKey ir.Field) migrationbackend.MigrationIntent {
		after := before.Clone()
		after.Fields = append(after.Fields, field)
		return migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationAddField,
			Before:         before,
			After:          after,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: field,
				TargetModel: targetModel,
				TargetKey:   targetKey,
			}},
		}}}
	}

	defaultedRequired := baseEditor.Clone()
	defaultedRequired.Nullable = false
	defaultedRequired.Default = &ir.ScalarDefault{Kind: ir.ScalarInteger, Integer: 1}
	differentTarget := target.Clone()
	differentTarget.Name = "editor"
	differentTarget.GoName = "Editor"
	differentTarget.DBTable = "news_editor"
	different := baseEditor.Clone()
	different.Relation.Target.ModelName = "editor"
	nestedTarget := target.Clone()
	nestedTarget.Fields = append(nestedTarget.Fields, ir.Field{
		Name: "publisher", GoName: "Publisher", Column: "publisher_id", Kind: ir.FieldForeignKey, Nullable: true,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "news", ModelName: "publisher"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "authors"},
			OnDelete:    ir.DeleteProtect,
		},
	})
	reverseConflict := baseEditor.Clone()
	reverseConflict.Relation.Reverse.Name = authorField.Relation.Reverse.Name
	forged := addIntent(baseEditor, target, target.Fields[0])
	forged.Operations[0].Targets[0].SourceField.Column = "forged_editor_id"
	selfBefore := ir.Model{
		Name: "node", GoName: "Node", DBTable: "news_node",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
	selfField := baseEditor.Clone()
	selfField.Name, selfField.GoName, selfField.Column = "parent", "Parent", "parent_id"
	selfField.Relation.Target.ModelName = selfBefore.Name
	selfField.Relation.Reverse.Name = "children"
	selfAfter := selfBefore.Clone()
	selfAfter.Fields = append(selfAfter.Fields, selfField)
	selfIntent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0, Kind: migrationbackend.MigrationAddField, Before: selfBefore, After: selfAfter,
		Targets: []migrationbackend.MigrationTarget{{SourceField: selfField, TargetModel: selfBefore, TargetKey: selfBefore.Fields[0]}},
	}}}
	firstAfter := before.Clone()
	firstAfter.Fields = append(firstAfter.Fields, baseEditor)
	reviewer := baseEditor.Clone()
	reviewer.Name = "reviewer"
	reviewer.GoName = "Reviewer"
	reviewer.Column = "reviewer_id"
	reviewer.Nullable = false
	reviewer.Relation.Reverse.Name = "reviewed_articles"
	secondAfter := firstAfter.Clone()
	secondAfter.Fields = append(secondAfter.Fields, reviewer)
	multiple := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{
			OperationIndex: 0, Kind: migrationbackend.MigrationAddField,
			Before: before, After: firstAfter,
			Targets: []migrationbackend.MigrationTarget{{SourceField: baseEditor, TargetModel: target, TargetKey: target.Fields[0]}},
		},
		{
			OperationIndex: 1, Kind: migrationbackend.MigrationAddField,
			Before: firstAfter, After: secondAfter,
			Targets: []migrationbackend.MigrationTarget{{SourceField: reviewer, TargetModel: target, TargetKey: target.Fields[0]}},
		},
	}}
	tests := []struct {
		name   string
		intent migrationbackend.MigrationIntent
		detail string
	}{
		{name: "defaulted required", intent: addIntent(defaultedRequired, target, target.Fields[0]), detail: "ForeignKey defaults are not supported"},
		{name: "different target", intent: addIntent(different, differentTarget, differentTarget.Fields[0]), detail: "different symbolic target"},
		{name: "nested target", intent: addIntent(baseEditor, nestedTarget, nestedTarget.Fields[0]), detail: "nested relation fields"},
		{name: "reverse conflict", intent: addIntent(reverseConflict, target, target.Fields[0]), detail: "duplicated by"},
		{name: "self relation", intent: selfIntent, detail: "self relation"},
		{name: "forged source", intent: forged, detail: "changed-field target snapshot"},
		{name: "multiple Adds", intent: multiple, detail: "at most one relation mutation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			backend, err := OpenMemory(ctx, "nullable-add-reject-"+strings.ReplaceAll(test.name, " ", "-"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Close() })
			session := openSQLiteRelationSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			concrete := session.(*sqliteRevisionFencedSession)
			connectionCalls := 0
			var checkpoints []sqliteRelationBeginCheckpoint
			concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
				connectionCalls++
				return connection
			}
			concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
				checkpoints = append(checkpoints, checkpoint)
			}
			transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_editor"},
				Kind:      migrationbackend.HistoryTransitionApply,
			}, test.intent)
			if transaction != nil || err == nil || !strings.Contains(err.Error(), test.detail) {
				t.Fatalf("BeginMigration(%s) = (%v, %v), want detail %q", test.name, transaction, err, test.detail)
			}
			if connectionCalls != 0 || len(checkpoints) != 0 || concrete.active != nil || concrete.state != revisionSessionReady {
				t.Fatalf("%s crossed static boundary: connections=%d checkpoints=%v active=%v state=%d", test.name, connectionCalls, checkpoints, concrete.active, concrete.state)
			}
			if err := session.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSQLiteNullableRelationAddCanonicalDriftFailsBeforeRevisionClaim(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "nullable-add-canonical-drift")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, before, authorField := sqliteRelationTestModels()
	targetSQL, err := compileMigrationCreateModel(target)
	if err != nil {
		t.Fatal(err)
	}
	// Exact columns and FK semantics, but deliberately noncanonical keyword
	// casing. Shape PRAGMAs pass; the strict one-pass SQL matcher must reject it.
	driftSQL := `create table "news_article"(` +
		`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, ` +
		`"title" VARCHAR(200) NOT NULL, "author_id" INTEGER NOT NULL, ` +
		`FOREIGN KEY ("author_id") REFERENCES "news_author" ("id") ON DELETE NO ACTION)`
	for _, statement := range []string{targetSQL, driftSQL} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed drift schema %q: %v", statement, err)
		}
	}
	initial := migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}
	seedSQLiteMigrationHistory(t, ctx, backend, initial)
	editor := authorField.Clone()
	editor.Name, editor.GoName, editor.Column, editor.Nullable = "editor", "Editor", "editor_id", true
	editor.Relation.Reverse.Name = "edited_articles"
	after := before.Clone()
	after.Fields = append(after.Fields, editor)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0, Kind: migrationbackend.MigrationAddField, Before: before, After: after,
		Targets: []migrationbackend.MigrationTarget{{SourceField: editor, TargetModel: target, TargetKey: target.Fields[0]}},
	}}}
	beforeSnapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	var sourceSQLBefore string
	if err := backend.database.QueryRowContext(ctx, `SELECT "sql" FROM main.sqlite_schema WHERE "name"='news_article'`).Scan(&sourceSQLBefore); err != nil {
		t.Fatal(err)
	}
	var columnsBefore int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_xinfo('news_article')`).Scan(&columnsBefore); err != nil {
		t.Fatal(err)
	}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	connectionCalls := 0
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
		connectionCalls++
		return connection
	}
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_editor"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if transaction != nil || err == nil || !strings.Contains(err.Error(), "canonical declaration") {
		t.Fatalf("BeginMigration(canonical drift) = (%v, %v)", transaction, err)
	}
	if connectionCalls != 1 {
		t.Fatalf("canonical drift connection calls = %d, want 1", connectionCalls)
	}
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
	if err := session.Close(ctx); err != nil {
		t.Fatalf("Close(canonical drift): %v", err)
	}
	afterSnapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || !reflect.DeepEqual(afterSnapshot, beforeSnapshot) {
		t.Fatalf("canonical drift changed history snapshot: before=%+v after=%+v err=%v", beforeSnapshot, afterSnapshot, err)
	}
	var sourceSQLAfter string
	var columnsAfter int
	if err := backend.database.QueryRowContext(ctx, `SELECT "sql" FROM main.sqlite_schema WHERE "name"='news_article'`).Scan(&sourceSQLAfter); err != nil {
		t.Fatal(err)
	}
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_xinfo('news_article')`).Scan(&columnsAfter); err != nil {
		t.Fatal(err)
	}
	if sourceSQLAfter != sourceSQLBefore || columnsAfter != columnsBefore || strings.Contains(sourceSQLAfter, "editor_id") {
		t.Fatalf("canonical drift changed source schema: SQL %q -> %q columns %d -> %d", sourceSQLBefore, sourceSQLAfter, columnsBefore, columnsAfter)
	}
}

func TestSQLiteRelationDirectPortSurvivesCloseReopenApplyUnapplyReapply(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relation-direct-port.sqlite")
	open := func() *Backend {
		t.Helper()
		backend, err := Open(ctx, "file:"+filepath.ToSlash(path)+"?mode=rwc")
		if err != nil {
			t.Fatalf("Open(%s): %v", path, err)
		}
		return backend
	}
	target, source, sourceField := sqliteRelationTestModels()
	migration := migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"}

	apply := func(backend *Backend) {
		t.Helper()
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migration,
			Kind:      migrationbackend.HistoryTransitionApply,
		}, sqliteRelationApplyIntent(target, source, sourceField))
		if err != nil {
			t.Fatal(err)
		}
		if err := transaction.CreateModel(ctx, target); err != nil {
			t.Fatal(err)
		}
		if err := transaction.CreateModel(ctx, source); err != nil {
			t.Fatal(err)
		}
		if err := transaction.RecordApplied(ctx, migration.App, migration.Name); err != nil {
			t.Fatal(err)
		}
		if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("CommitFenced(apply) = (%+v, %v)", outcome, err)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	unapply := func(backend *Backend) {
		t.Helper()
		session := openSQLiteRelationSession(t, backend)
		records, err := session.ReadAppliedMigrations(ctx)
		if err != nil || !reflect.DeepEqual(records, []migrationbackend.AppliedMigration{migration}) {
			t.Fatalf("ReadAppliedMigrations(unapply) = (%v, %v)", records, err)
		}
		transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migration,
			Kind:      migrationbackend.HistoryTransitionUnapply,
		}, sqliteRelationUnapplyIntent(target, source, sourceField))
		if err != nil {
			t.Fatal(err)
		}
		if err := transaction.DeleteModel(ctx, source); err != nil {
			t.Fatal(err)
		}
		if err := transaction.DeleteModel(ctx, target); err != nil {
			t.Fatal(err)
		}
		if err := transaction.RecordUnapplied(ctx, migration.App, migration.Name); err != nil {
			t.Fatal(err)
		}
		if outcome, err := transaction.CommitFenced(ctx); err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("CommitFenced(unapply) = (%+v, %v)", outcome, err)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}

	backend := open()
	apply(backend)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend = open()
	unapply(backend)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend = open()
	apply(backend)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend = open()
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("final Close(): %v", err)
		}
	}()
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || snapshot.token.revision != 3 ||
		!reflect.DeepEqual(snapshot.records, []migrationbackend.AppliedMigration{migration}) {
		t.Fatalf("reopened final snapshot = (%+v, %v)", snapshot, err)
	}
	if !sqliteRelationTestTableExists(t, backend, target.DBTable) || !sqliteRelationTestTableExists(t, backend, source.DBTable) {
		t.Fatal("reopened reapply did not preserve both relation tables")
	}
}

func TestSQLiteRelationBeginFaultsCleanUpBeforePublication(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		contains    string
		setup       func(*testing.T, context.Context, *Backend, *ir.Model, *ir.Model, *ir.Field) migrationbackend.MigrationIntent
		checkpoints []sqliteRelationBeginCheckpoint
		begun       bool
	}{
		{name: "pragma_set", method: "exec", contains: "PRAGMA foreign_keys = ON"},
		{name: "pragma_read", method: "query_row", contains: "PRAGMA foreign_keys", checkpoints: []sqliteRelationBeginCheckpoint{sqliteRelationCheckpointForeignKeysSet}},
		{name: "begin_immediate", method: "exec", contains: "BEGIN IMMEDIATE", checkpoints: []sqliteRelationBeginCheckpoint{sqliteRelationCheckpointForeignKeysSet, sqliteRelationCheckpointForeignKeysRead}},
		{name: "catalog", method: "query", contains: "FROM main.sqlite_schema", begun: true, checkpoints: []sqliteRelationBeginCheckpoint{sqliteRelationCheckpointForeignKeysSet, sqliteRelationCheckpointForeignKeysRead, sqliteRelationCheckpointTransactionBegun}},
		{
			name:     "physical_preflight",
			method:   "query",
			contains: `PRAGMA main.table_xinfo("news_author")`,
			begun:    true,
			checkpoints: []sqliteRelationBeginCheckpoint{
				sqliteRelationCheckpointForeignKeysSet,
				sqliteRelationCheckpointForeignKeysRead,
				sqliteRelationCheckpointTransactionBegun,
			},
			setup: func(t *testing.T, ctx context.Context, backend *Backend, target, source *ir.Model, sourceField *ir.Field) migrationbackend.MigrationIntent {
				t.Helper()
				statement, err := compileMigrationCreateModel(*target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
				sourceField.Relation.Target.AppLabel = "accounts"
				source.Fields[2] = sourceField.Clone()
				return migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
					OperationIndex: 0,
					Kind:           migrationbackend.MigrationCreateModel,
					After:          source.Clone(),
					Targets: []migrationbackend.MigrationTarget{{
						SourceField: sourceField.Clone(),
						TargetModel: target.Clone(),
						TargetKey:   target.Fields[0].Clone(),
					}},
				}}}
			},
		},
		{
			name:     "revision_claim",
			method:   "exec",
			contains: `CREATE TABLE "godj_migration_revision"`,
			begun:    true,
			checkpoints: []sqliteRelationBeginCheckpoint{
				sqliteRelationCheckpointForeignKeysSet,
				sqliteRelationCheckpointForeignKeysRead,
				sqliteRelationCheckpointTransactionBegun,
				sqliteRelationCheckpointPhysicalPreflightComplete,
				sqliteRelationCheckpointRevisionClaimStarting,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			backend, err := OpenMemory(ctx, "relation-begin-fault-"+test.name)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Close() })
			target, source, sourceField := sqliteRelationTestModels()
			intent := sqliteRelationApplyIntent(target, source, sourceField)
			if test.setup != nil {
				intent = test.setup(t, ctx, backend, &target, &source, &sourceField)
			}
			session := openSQLiteRelationSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			concrete := session.(*sqliteRevisionFencedSession)
			var checkpoints []sqliteRelationBeginCheckpoint
			concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
				checkpoints = append(checkpoints, checkpoint)
			}
			fault := &sqliteRelationBeginFaultConnection{
				method:    test.method,
				contains:  test.contains,
				remaining: 1,
			}
			concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
				fault.migrationPinnedConnection = connection
				return fault
			}
			transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
				Kind:      migrationbackend.HistoryTransitionApply,
			}, intent)
			if transaction != nil || err == nil || fault.remaining != 0 {
				t.Fatalf("BeginMigration() = (%v, %v), remaining fault=%d", transaction, err, fault.remaining)
			}
			if !reflect.DeepEqual(checkpoints, test.checkpoints) {
				t.Fatalf("begin checkpoints = %v, want %v", checkpoints, test.checkpoints)
			}
			wantClose, wantRaw := 1, 0
			if test.name == "begin_immediate" {
				wantClose, wantRaw = 0, 1
			}
			if fault.closeCalls != wantClose || fault.rawCalls != wantRaw ||
				test.begun && fault.rollbackCalls != 1 || !test.begun && fault.rollbackCalls != 0 {
				t.Fatalf("cleanup calls = close:%d raw:%d rollback:%d, want close:%d raw:%d begun=%t", fault.closeCalls, fault.rawCalls, fault.rollbackCalls, wantClose, wantRaw, test.begun)
			}
			if concrete.active != nil || concrete.state != revisionSessionPoisoned {
				t.Fatalf("failed session = active:%v state:%d", concrete.active, concrete.state)
			}
			if err := session.Close(ctx); err != nil {
				t.Fatalf("Close(poisoned session): %v", err)
			}
			if sqliteRelationTestTableExists(t, backend, source.DBTable) || test.setup == nil && sqliteRelationTestTableExists(t, backend, target.DBTable) {
				t.Fatal("Begin fault published relation DDL")
			}
			snapshot, snapshotErr := readAtomicMigrationRevisionSnapshot(ctx, backend)
			if snapshotErr != nil || snapshot.token.initialized || len(snapshot.records) != 0 {
				t.Fatalf("history after Begin fault = (%+v, %v)", snapshot, snapshotErr)
			}
		})
	}

	t.Run("acquire_connection", func(t *testing.T) {
		ctx := context.Background()
		backend, err := OpenMemory(ctx, "relation-begin-fault-acquire")
		if err != nil {
			t.Fatal(err)
		}
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		target, source, sourceField := sqliteRelationTestModels()
		transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}, sqliteRelationApplyIntent(target, source, sourceField))
		concrete := session.(*sqliteRevisionFencedSession)
		if transaction != nil || err == nil || concrete.active != nil || concrete.state != revisionSessionPoisoned {
			t.Fatalf("closed-backend Begin = transaction:%v error:%v active:%v state:%d", transaction, err, concrete.active, concrete.state)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatalf("Close(acquire-failed session): %v", err)
		}
	})

	t.Run("pre_begin_close_failure_joins_primary", func(t *testing.T) {
		ctx := context.Background()
		backend, err := OpenMemory(ctx, "relation-begin-close-cleanup-fault")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = backend.Close() }()
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		primary := errors.New("relation pragma primary fault")
		cleanup := errors.New("relation connection close cleanup fault")
		fault := &sqliteRelationBeginFaultConnection{
			method:    "exec",
			contains:  "PRAGMA foreign_keys = ON",
			remaining: 1,
			faultErr:  primary,
			closeErr:  cleanup,
		}
		concrete := session.(*sqliteRevisionFencedSession)
		concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
			fault.migrationPinnedConnection = connection
			return fault
		}
		target, source, sourceField := sqliteRelationTestModels()
		transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}, sqliteRelationApplyIntent(target, source, sourceField))
		if transaction != nil || !errors.Is(err, primary) || !errors.Is(err, cleanup) ||
			fault.closeCalls != 1 || fault.rawCalls != 1 {
			t.Fatalf("close-cleanup Begin = transaction:%v error:%v close:%d raw:%d", transaction, err, fault.closeCalls, fault.rawCalls)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("post_begin_rollback_failure_joins_primary", func(t *testing.T) {
		ctx := context.Background()
		backend, err := OpenMemory(ctx, "relation-begin-rollback-cleanup-fault")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = backend.Close() }()
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		primary := errors.New("relation catalog primary fault")
		cleanup := errors.New("relation rollback cleanup fault")
		fault := &sqliteRelationBeginFaultConnection{
			method:      "query",
			contains:    "FROM main.sqlite_schema",
			remaining:   1,
			faultErr:    primary,
			rollbackErr: cleanup,
		}
		concrete := session.(*sqliteRevisionFencedSession)
		concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
			fault.migrationPinnedConnection = connection
			return fault
		}
		target, source, sourceField := sqliteRelationTestModels()
		transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
			Kind:      migrationbackend.HistoryTransitionApply,
		}, sqliteRelationApplyIntent(target, source, sourceField))
		if transaction != nil || !errors.Is(err, primary) || !errors.Is(err, cleanup) ||
			fault.rollbackCalls != 1 || fault.rawCalls != 1 {
			t.Fatalf("rollback-cleanup Begin = transaction:%v error:%v rollback:%d raw:%d", transaction, err, fault.rollbackCalls, fault.rawCalls)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSQLiteRelationCreateModelRequiresExactCursorAndRecorderExhaustion(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-cursor")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	intent := sqliteRelationApplyIntent(target, source, sourceField)
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginMigration(ctx, transition, intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.CreateModel(ctx, source); err == nil {
		t.Fatal("reordered CreateModel succeeded")
	}
	if err := transaction.CreateModel(ctx, target); err == nil {
		t.Fatal("relation transaction retried after mismatch")
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err == nil || outcome.Durability != migrationbackend.CommitRolledBack {
		t.Fatalf("failed cursor CommitFenced() = (%+v, %v)", outcome, err)
	}
	if sqliteRelationTestTableExists(t, backend, target.DBTable) || sqliteRelationTestTableExists(t, backend, source.DBTable) {
		t.Fatal("cursor mismatch published relation tables")
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}

	backend2, err := OpenMemory(ctx, "relation-recorder-exhaustion")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend2.Close() })
	session = openSQLiteRelationSession(t, backend2)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err = session.BeginMigration(ctx, transition, intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.CreateModel(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := transaction.RecordApplied(ctx, "news", "0001_relation"); err == nil {
		t.Fatal("RecordApplied before cursor exhaustion succeeded")
	}
	outcome, err = transaction.CommitFenced(ctx)
	if err == nil || outcome.Durability != migrationbackend.CommitRolledBack {
		t.Fatalf("unexhausted CommitFenced() = (%+v, %v)", outcome, err)
	}
	if sqliteRelationTestTableExists(t, backend2, target.DBTable) {
		t.Fatal("unexhausted relation transaction preserved partial DDL")
	}
}

func TestSQLiteRelationMixedScalarFieldRoundTripAndReapply(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-mixed-scalar-round-trip")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	target, source, sourceField := sqliteRelationTestModels()
	extra := ir.Field{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean}
	sourceAfterAdd := source.Clone()
	sourceAfterAdd.Fields = append(sourceAfterAdd.Fields, extra)
	applyIntent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{OperationIndex: 0, Kind: migrationbackend.MigrationCreateModel, After: target},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          source,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
		{
			OperationIndex: 2,
			Kind:           migrationbackend.MigrationAddField,
			Before:         source,
			After:          sourceAfterAdd,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
	}}
	unapplyIntent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{
			OperationIndex: 2,
			Kind:           migrationbackend.MigrationRemoveField,
			Before:         sourceAfterAdd,
			After:          source,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.MigrationDeleteModel,
			Before:         source,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
		{OperationIndex: 0, Kind: migrationbackend.MigrationDeleteModel, Before: target},
	}}
	migration := migrationbackend.AppliedMigration{App: "news", Name: "0001_mixed_relation"}

	apply := func() {
		t.Helper()
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migration,
			Kind:      migrationbackend.HistoryTransitionApply,
		}, applyIntent)
		if err != nil {
			t.Fatalf("BeginMigration(apply): %v", err)
		}
		if err := transaction.CreateModel(ctx, target); err != nil {
			t.Fatal(err)
		}
		if err := transaction.CreateModel(ctx, source); err != nil {
			t.Fatal(err)
		}
		if err := transaction.AddField(ctx, source, extra); err != nil {
			t.Fatalf("AddField(created relation table): %v", err)
		}
		if err := transaction.RecordApplied(ctx, migration.App, migration.Name); err != nil {
			t.Fatal(err)
		}
		outcome, err := transaction.CommitFenced(ctx)
		if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("CommitFenced(apply) = (%+v, %v)", outcome, err)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	unapply := func() {
		t.Helper()
		session := openSQLiteRelationSession(t, backend)
		if _, err := session.ReadAppliedMigrations(ctx); err != nil {
			t.Fatal(err)
		}
		transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
			Migration: migration,
			Kind:      migrationbackend.HistoryTransitionUnapply,
		}, unapplyIntent)
		if err != nil {
			t.Fatalf("BeginMigration(unapply): %v", err)
		}
		if err := transaction.RemoveField(ctx, sourceAfterAdd, extra); err != nil {
			t.Fatalf("RemoveField(relation table): %v", err)
		}
		if err := transaction.DeleteModel(ctx, source); err != nil {
			t.Fatal(err)
		}
		if err := transaction.DeleteModel(ctx, target); err != nil {
			t.Fatal(err)
		}
		if err := transaction.RecordUnapplied(ctx, migration.App, migration.Name); err != nil {
			t.Fatal(err)
		}
		outcome, err := transaction.CommitFenced(ctx)
		if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("CommitFenced(unapply) = (%+v, %v)", outcome, err)
		}
		if err := session.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}

	apply()
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_author" ("name") VALUES ('Ada')`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_article" ("title", "author_id", "published") VALUES ('sealed', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `DELETE FROM "news_article"`); err != nil {
		t.Fatal(err)
	}
	unapply()
	apply()
	if !sqliteRelationTestTableExists(t, backend, target.DBTable) || !sqliteRelationTestTableExists(t, backend, source.DBTable) {
		t.Fatal("reapply did not restore the complete mixed relation step")
	}
}

func TestSQLiteRelationIntentStaticBoundaryIsClosedAndDeterministic(t *testing.T) {
	target, source, sourceField := sqliteRelationTestModels()
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	valid := sqliteRelationApplyIntent(target, source, sourceField)

	t.Run("scalar_only_is_a_valid_unified_intent", func(t *testing.T) {
		seal, err := validateAndSealSQLiteRelationIntent(transition, migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          target,
		}}})
		if err != nil || len(seal.intent.Operations) != 1 || len(seal.intent.Operations[0].Targets) != 0 {
			t.Fatalf("scalar unified intent = (%+v, %v)", seal.intent, err)
		}
	})

	t.Run("self_target_bearing_add_is_integrity", func(t *testing.T) {
		before := target.Clone()
		relationField := sourceField.Clone()
		relationField.Relation.Target.ModelName = target.Name
		after := before.Clone()
		after.Fields = append(after.Fields, relationField)
		_, err := validateAndSealSQLiteRelationIntent(transition, migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationAddField,
			Before:         before,
			After:          after,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: relationField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		}}})
		if err == nil || migrationbackend.IsCapabilityError(err) || !strings.Contains(err.Error(), "self relation") {
			t.Fatalf("self relation Add validation error = %v", err)
		}
	})

	t.Run("wrong_direction_is_integrity_before_session", func(t *testing.T) {
		single := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{valid.Operations[1]}}
		single.Operations[0].OperationIndex = 0
		wrongDirection := transition
		wrongDirection.Kind = migrationbackend.HistoryTransitionUnapply
		_, err := validateAndSealSQLiteRelationIntent(wrongDirection, single)
		if err == nil || migrationbackend.IsCapabilityError(err) || !strings.Contains(err.Error(), "does not match history transition") {
			t.Fatalf("wrong-direction validation error = %v", err)
		}
	})

	t.Run("duplicate_target_metadata_is_integrity", func(t *testing.T) {
		forged := cloneSQLiteRelationIntent(valid)
		forged.Operations[1].Targets = append(forged.Operations[1].Targets, forged.Operations[1].Targets[0])
		_, err := validateAndSealSQLiteRelationIntent(transition, forged)
		if err == nil || !strings.Contains(err.Error(), "exact field order") {
			t.Fatalf("duplicate target validation error = %v", err)
		}
	})

	t.Run("non_nil_empty_zero_sentinel_is_rejected_before_clone", func(t *testing.T) {
		forged := cloneSQLiteRelationIntent(valid)
		forged.Operations[0].Before = ir.Model{Fields: []ir.Field{}}
		_, err := validateAndSealSQLiteRelationIntent(transition, forged)
		if err == nil || !strings.Contains(err.Error(), "non-zero Before") {
			t.Fatalf("non-nil empty zero-sentinel validation error = %v", err)
		}
	})

	t.Run("discontinuous_scalar_delta_is_integrity", func(t *testing.T) {
		extra := ir.Field{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean}
		before := source.Clone()
		before.Fields[1].MaxLength++
		after := before.Clone()
		after.Fields = append(after.Fields, extra)
		forged := cloneSQLiteRelationIntent(valid)
		forged.Operations = append(forged.Operations, migrationbackend.MigrationOperation{
			OperationIndex: 2,
			Kind:           migrationbackend.MigrationAddField,
			Before:         before,
			After:          after,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		})
		_, err := validateAndSealSQLiteRelationIntent(transition, forged)
		if err == nil || !strings.Contains(err.Error(), "discontinuous") {
			t.Fatalf("discontinuous validation error = %v", err)
		}
	})

	t.Run("external_relation_target_requires_nested_authority", func(t *testing.T) {
		external := source.Clone()
		field := sourceField.Clone()
		field.Relation.Target = ir.ModelIdentity{AppLabel: "accounts", ModelName: external.Name}
		child := ir.Model{
			Name: "comment", GoName: "Comment", DBTable: "news_comment",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				field,
			},
		}
		_, err := validateAndSealSQLiteRelationIntent(transition, migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          child,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: field,
				TargetModel: external,
				TargetKey:   external.Fields[0],
			}},
		}}})
		assertSQLiteRelationCapabilityFeature(t, err, "sqlite_relation_migration")
	})

	t.Run("cross_app_same_model_name_uses_full_identity", func(t *testing.T) {
		localTarget := target.Clone()
		localTarget.DBTable = "news_local_author"
		externalTarget := target.Clone()
		externalTarget.DBTable = "accounts_author"
		field := sourceField.Clone()
		field.Relation.Target.AppLabel = "accounts"
		crossAppSource := source.Clone()
		crossAppSource.Fields[2] = field
		intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
			{OperationIndex: 0, Kind: migrationbackend.MigrationCreateModel, After: localTarget},
			{
				OperationIndex: 1,
				Kind:           migrationbackend.MigrationCreateModel,
				After:          crossAppSource,
				Targets: []migrationbackend.MigrationTarget{{
					SourceField: field,
					TargetModel: externalTarget,
					TargetKey:   externalTarget.Fields[0],
				}},
			},
		}}
		if _, err := validateAndSealSQLiteRelationIntent(transition, intent); err != nil {
			t.Fatalf("cross-app same-name relation rejected: %v", err)
		}
		externalTarget.DBTable = localTarget.DBTable
		intent.Operations[1].Targets[0].TargetModel = externalTarget
		_, err := validateAndSealSQLiteRelationIntent(transition, intent)
		if err == nil || !strings.Contains(err.Error(), "collides with local model") {
			t.Fatalf("cross-app table alias validation error = %v", err)
		}
	})

	t.Run("resource_limit_precedes_clone", func(t *testing.T) {
		forged := migrationbackend.MigrationIntent{Operations: make([]migrationbackend.MigrationOperation, sqliteRelationMaxOperations+1)}
		_, err := validateAndSealSQLiteRelationIntent(transition, forged)
		if err == nil || !strings.Contains(err.Error(), "maximum") {
			t.Fatalf("resource validation error = %v", err)
		}
	})
}

func TestSQLiteRelationLaterTargetFieldCannotCollideWithRegisteredReverseBeforeClaim(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-later-reverse-collision")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	targetAfter := target.Clone()
	targetAfter.Fields = append(targetAfter.Fields, ir.Field{
		Name: "articles", GoName: "Articles", Column: "articles", Kind: ir.FieldBoolean,
	})
	intent := sqliteRelationApplyIntent(target, source, sourceField)
	intent.Operations = append(intent.Operations, migrationbackend.MigrationOperation{
		OperationIndex: 2,
		Kind:           migrationbackend.MigrationAddField,
		Before:         target,
		After:          targetAfter,
	})

	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_reverse_collision"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if transaction != nil || err == nil || !strings.Contains(err.Error(), "collides with reverse name") {
		t.Fatalf("BeginMigration() = (%v, %v), want ordered reverse collision", transaction, err)
	}
	if len(checkpoints) != 0 || concrete.active != nil || concrete.state != revisionSessionReady {
		t.Fatalf("reverse collision crossed connection boundary: checkpoints=%v active=%v state=%d", checkpoints, concrete.active, concrete.state)
	}
	if sqliteRelationTestTableExists(t, backend, target.DBTable) || sqliteRelationTestTableExists(t, backend, source.DBTable) {
		t.Fatal("reverse collision published relation tables")
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationInitialReverseCollisionCannotBeRemovedBeforeDelete(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-initial-reverse-collision")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	migration := migrationbackend.AppliedMigration{App: "news", Name: "0001_initial_reverse_collision"}
	seedSQLiteMigrationHistory(t, ctx, backend, migration)
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "reverse_collision_marker" ("id" INTEGER)`); err != nil {
		t.Fatal(err)
	}

	targetAfter, source, sourceField := sqliteRelationTestModels()
	targetBefore := targetAfter.Clone()
	targetBefore.Fields = append(targetBefore.Fields, ir.Field{
		Name: "articles", GoName: "Articles", Column: "articles", Kind: ir.FieldBoolean,
	})
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{
			OperationIndex: 1,
			Kind:           migrationbackend.MigrationRemoveField,
			Before:         targetBefore,
			After:          targetAfter,
		},
		{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationDeleteModel,
			Before:         source,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: sourceField,
				TargetModel: targetAfter,
				TargetKey:   targetAfter.Fields[0],
			}},
		},
	}}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	connectionCalls := 0
	concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
		connectionCalls++
		return connection
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migration,
		Kind:      migrationbackend.HistoryTransitionUnapply,
	}, intent)
	if transaction != nil || err == nil || !strings.Contains(err.Error(), "collides with reverse name") {
		t.Fatalf("BeginMigration() = (%v, %v), want initial reverse collision", transaction, err)
	}
	if connectionCalls != 0 || len(checkpoints) != 0 || concrete.active != nil || concrete.state != revisionSessionReady {
		t.Fatalf(
			"initial reverse collision crossed connection boundary: connections=%d checkpoints=%v active=%v state=%d",
			connectionCalls,
			checkpoints,
			concrete.active,
			concrete.state,
		)
	}
	if !sqliteRelationTestTableExists(t, backend, "reverse_collision_marker") {
		t.Fatal("initial reverse collision changed the marker schema")
	}
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || !reflect.DeepEqual(snapshot.records, []migrationbackend.AppliedMigration{migration}) {
		t.Fatalf("history after initial reverse collision = (%+v, %v)", snapshot, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationIdentifierFoldingMatchesSQLiteASCIIOnly(t *testing.T) {
	if sqliteRelationIdentifierKey("News_Article") != sqliteRelationIdentifierKey("news_article") {
		t.Fatal("ASCII identifier case was not folded")
	}
	if sqliteRelationIdentifierKey("K") == sqliteRelationIdentifierKey("k") {
		t.Fatal("non-ASCII Kelvin sign was incorrectly folded to ASCII k")
	}
}

func TestSQLiteRelationNonASCIITempDecoyDoesNotShadowASCIIIdentifier(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-nonascii-decoy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := backend.ExecContext(ctx, `CREATE TEMP TABLE "K" ("id" INTEGER)`); err != nil {
		t.Fatal(err)
	}
	target, source, sourceField := sqliteRelationTestModels()
	source.DBTable = "k"
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_nonascii"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, sqliteRelationApplyIntent(target, source, sourceField))
	if err != nil {
		t.Fatalf("non-ASCII TEMP decoy falsely shadowed ASCII table: %v", err)
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationUnrelatedLegacyForeignKeyCycleDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-unrelated-cycle")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	for _, statement := range []string{
		`CREATE TABLE "legacy_a" ("id" INTEGER PRIMARY KEY, "b_id" INTEGER, FOREIGN KEY ("b_id") REFERENCES "legacy_b" ("id"))`,
		`CREATE TABLE "legacy_b" ("id" INTEGER PRIMARY KEY, "a_id" INTEGER, FOREIGN KEY ("a_id") REFERENCES "legacy_a" ("id"))`,
		`CREATE TABLE "reference_decoy" ("references" "news_article", "note" TEXT DEFAULT 'REFERENCES news_article' /* REFERENCES news_article */)`,
		`CREATE VIEW "literal_decoy" AS SELECT 'unrelated_literal' AS "value"`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	target, source, sourceField := sqliteRelationTestModels()
	initial := migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}
	seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, backend, initial, target, source, sourceField)
	editor := sourceField.Clone()
	editor.Name, editor.GoName, editor.Column, editor.Nullable = "editor", "Editor", "editor_id", true
	editor.Relation.Reverse.Name = "edited_articles"
	after := source.Clone()
	after.Fields = append(after.Fields, editor)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationAddField,
		Before:         source,
		After:          after,
		Targets: []migrationbackend.MigrationTarget{{
			SourceField: editor,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}}}
	session := openSQLiteRelationSession(t, backend)
	if records, err := session.ReadAppliedMigrations(ctx); err != nil ||
		!reflect.DeepEqual(records, []migrationbackend.AppliedMigration{initial}) {
		t.Fatalf("unrelated cycle history = (%v, %v)", records, err)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_editor"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err != nil {
		t.Fatalf("unrelated legacy cycle blocked nullable relation Add Begin: %v", err)
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteNullableRelationAddTargetOutgoingCycleFailsBeforeClaim(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "nullable-add-target-cycle")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, author := sqliteRelationTestModels()
	initial := migrationbackend.AppliedMigration{App: "news", Name: "0001_initial"}
	seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, backend, initial, target, source, author)
	if _, err := backend.ExecContext(
		ctx,
		`ALTER TABLE "news_author" ADD COLUMN "featured_article_id" INTEGER NULL REFERENCES "news_article" ("id") ON DELETE NO ACTION`,
	); err != nil {
		t.Fatalf("seed target outgoing cycle: %v", err)
	}
	beforeSnapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}

	editor := author.Clone()
	editor.Name, editor.GoName, editor.Column, editor.Nullable = "editor", "Editor", "editor_id", true
	editor.Relation.Reverse.Name = "edited_articles"
	after := source.Clone()
	after.Fields = append(after.Fields, editor)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationAddField,
		Before:         source,
		After:          after,
		Targets: []migrationbackend.MigrationTarget{{
			SourceField: editor,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}}}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0002_editor"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if transaction != nil || err == nil || !errors.Is(err, errSQLiteRelationPhysicalDrift) {
		t.Fatalf("BeginMigration(target outgoing cycle) = (%v, %v)", transaction, err)
	}
	assertSQLiteRelationCapabilityFeature(t, err, "sqlite_relation_migration")
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	afterSnapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || !reflect.DeepEqual(afterSnapshot, beforeSnapshot) {
		t.Fatalf("target cycle changed history snapshot: before=%+v after=%+v err=%v", beforeSnapshot, afterSnapshot, err)
	}
	var editorColumns int
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM pragma_table_xinfo('news_article') WHERE "name" = 'editor_id'`,
	).Scan(&editorColumns); err != nil || editorColumns != 0 {
		t.Fatalf("target cycle editor columns = (%d, %v), want 0", editorColumns, err)
	}
}

func TestSQLiteRelationExternalTargetWithHarmlessSchemaObjects(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-external-target")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	target, source, sourceField := sqliteRelationTestModels()
	sourceField.Relation.Target.AppLabel = "accounts"
	source.Fields[2] = sourceField
	targetSQL, err := compileMigrationCreateModel(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		targetSQL,
		`CREATE TABLE "external_audit" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT)`,
		`CREATE INDEX "external_author_name" ON "news_author" ("name")`,
		`CREATE TRIGGER "news_article" AFTER INSERT ON "news_author" BEGIN INSERT INTO "external_audit" ("id") VALUES (NULL); END`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationCreateModel,
		After:          source,
		Targets: []migrationbackend.MigrationTarget{{
			SourceField: sourceField,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}}}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_external_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginMigration(ctx, transition, intent)
	if err != nil {
		t.Fatalf("BeginMigration(): %v", err)
	}
	if err := transaction.CreateModel(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name); err != nil {
		t.Fatal(err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("CommitFenced() = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationCompilerAlwaysUsesNoAction(t *testing.T) {
	target, source, sourceField := sqliteRelationTestModels()
	sourceField.Nullable = true
	sourceField.Relation.OnDelete = ir.DeleteSetNull
	source.Fields[2] = sourceField
	statement, err := compileSQLiteRelationCreateModel(source, []migrationbackend.MigrationTarget{{
		SourceField: sourceField,
		TargetModel: target,
		TargetKey:   target.Fields[0],
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement, `ON DELETE NO ACTION`) || strings.Contains(statement, `ON DELETE SET NULL`) {
		t.Fatalf("relation CREATE SQL = %q, want exact NO ACTION enforcement", statement)
	}
}

func TestSQLiteRelationBeginCheckpointOrderAndPostClaimCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend, err := OpenMemory(ctx, "relation-begin-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
		if checkpoint == sqliteRelationCheckpointRevisionClaimed {
			cancel()
		}
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_cancel_after_claim"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, sqliteRelationApplyIntent(target, source, sourceField))
	if transaction != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("BeginMigration() = (%v, %v), want nil/context.Canceled", transaction, err)
	}
	want := []sqliteRelationBeginCheckpoint{
		sqliteRelationCheckpointForeignKeysSet,
		sqliteRelationCheckpointForeignKeysRead,
		sqliteRelationCheckpointTransactionBegun,
		sqliteRelationCheckpointPhysicalPreflightComplete,
		sqliteRelationCheckpointRevisionClaimStarting,
		sqliteRelationCheckpointRevisionClaimed,
	}
	if !reflect.DeepEqual(checkpoints, want) {
		t.Fatalf("begin checkpoints = %v, want %v", checkpoints, want)
	}
	if concrete.active != nil || concrete.state != revisionSessionPoisoned {
		t.Fatalf("canceled session = active:%v state:%d, want nil/poisoned", concrete.active, concrete.state)
	}
	if sqliteRelationTestTableExists(t, backend, migrationRevisionTable) {
		t.Fatal("post-claim cancellation published revision metadata")
	}
}

func TestSQLiteMigrationStaticInvalidDoesNotConsumeReadySession(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-static-zero-io")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	transaction, err := session.BeginMigration(ctx, transition, migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationCreateModel,
		After:          target,
		Targets: []migrationbackend.MigrationTarget{{
			SourceField: sourceField,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}}})
	if transaction != nil {
		t.Fatalf("invalid Begin returned transaction %v", transaction)
	}
	if err == nil || !strings.Contains(err.Error(), "has 1 targets, want 0") {
		t.Fatalf("invalid Begin error = %v", err)
	}
	if len(checkpoints) != 0 || concrete.state != revisionSessionReady || concrete.active != nil {
		t.Fatalf("static rejection checkpoints/state/active = %v/%d/%v, want empty/ready/nil", checkpoints, concrete.state, concrete.active)
	}
	transaction, err = session.BeginMigration(ctx, transition, sqliteRelationApplyIntent(target, source, sourceField))
	if err != nil {
		t.Fatalf("valid Begin after static rejection: %v", err)
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationPhysicalPreflightStopsBeforeRevisionClaim(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, context.Context, *Backend)
	}{
		{
			name: "mixed_case_temp_control_shadow",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TEMP TABLE "GODJ_MIGRATION_REVISION" ("revision" INTEGER)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mixed_case_temp_recorder_shadow",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TEMP TABLE "GODJ_MIGRATIONS" ("app" TEXT, "name" TEXT)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mixed_case_temp_source_shadow",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TEMP TABLE "NEWS_ARTICLE" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mixed_case_main_control_table_alias",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "GoDj_Migrations" ("app" TEXT)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mixed_case_main_control_view_alias",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "GoDj_Migration_Revision" AS SELECT 1 AS "revision"`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mixed_case_main_control_index_alias",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "index_owner" ("value" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE INDEX "GoDj_Migrations" ON "index_owner" ("value")`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dangling_child_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "dangling_child" ("article_id" INTEGER, FOREIGN KEY ("article_id") REFERENCES "news_article" ("id"))`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "commented_dangling_child_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "commented_dangling_child" (`+
					`"article_id" INTEGER, FOREIGN KEY ("article_id") `+
					`REFERENCES /* bounded decoy */ "news_article" ("id"))`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "future_article_view" AS SELECT * FROM "news_article"`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "single_quoted_view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "single_quoted_future_article_view" AS SELECT * FROM 'news_article'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "comma_join_single_quoted_view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "view_other" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "comma_future_article_view" AS `+
					`SELECT 1 FROM "view_other", 'news_article'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "single_quoted_schema_view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "schema_future_article_view" AS `+
					`SELECT * FROM 'main'.'news_article'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "join_on_then_comma_single_quoted_view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				for _, statement := range []string{
					`CREATE TABLE "join_left" ("id" INTEGER)`,
					`CREATE TABLE "join_right" ("id" INTEGER)`,
				} {
					if _, err := backend.ExecContext(ctx, statement); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "join_comma_future_article_view" AS `+
					`SELECT 1 FROM "join_left" JOIN "join_right" ON 1 = 1, 'news_article'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "parenthesized_single_quoted_view_references_future_source",
			setup: func(t *testing.T, ctx context.Context, backend *Backend) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "parenthesized_future_article_view" AS `+
					`SELECT * FROM ('news_article')`); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			backend, err := OpenMemory(ctx, "relation-preflight-"+test.name)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Close() })
			target, source, sourceField := sqliteRelationTestModels()
			session := openSQLiteRelationSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			test.setup(t, ctx, backend)
			concrete := session.(*sqliteRevisionFencedSession)
			var checkpoints []sqliteRelationBeginCheckpoint
			concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
				checkpoints = append(checkpoints, checkpoint)
			}
			transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
				Kind:      migrationbackend.HistoryTransitionApply,
			}, sqliteRelationApplyIntent(target, source, sourceField))
			if transaction != nil || err == nil {
				t.Fatalf("BeginMigration() = (%v, %v), want physical preflight failure", transaction, err)
			}
			for _, checkpoint := range checkpoints {
				if checkpoint == sqliteRelationCheckpointRevisionClaimStarting || checkpoint == sqliteRelationCheckpointRevisionClaimed {
					t.Fatalf("physical preflight reached revision claim: checkpoints=%v", checkpoints)
				}
			}
			if concrete.active != nil || concrete.state != revisionSessionPoisoned {
				t.Fatalf("failed session = active:%v state:%d, want nil/poisoned", concrete.active, concrete.state)
			}
		})
	}
}

func TestSQLiteRelationExternalTargetRequiresExactAutoIncrementShape(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-external-target-autoincrement")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	sourceField.Relation.Target.AppLabel = "accounts"
	source.Fields[2] = sourceField
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "news_author" ("id" INTEGER NOT NULL PRIMARY KEY, "name" VARCHAR(120) NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_external"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationCreateModel,
		After:          source,
		Targets: []migrationbackend.MigrationTarget{{
			SourceField: sourceField,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}}})
	if transaction != nil || err == nil || !strings.Contains(err.Error(), "canonical declaration") {
		t.Fatalf("BeginMigration() = (%v, %v), want AUTOINCREMENT drift failure", transaction, err)
	}
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
}

func TestSQLiteRelationCreateRejectsOrphanSequenceBeforeClaim(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-orphan-sequence")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "sequence_carrier" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO main.sqlite_sequence ("name", "seq") VALUES ('news_author', 7)`); err != nil {
		t.Fatal(err)
	}
	target, source, sourceField := sqliteRelationTestModels()
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, sqliteRelationApplyIntent(target, source, sourceField))
	if transaction != nil || err == nil || !strings.Contains(err.Error(), "orphan row") {
		t.Fatalf("BeginMigration() = (%v, %v), want orphan sequence failure", transaction, err)
	}
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
}

func TestSQLiteRelationNonEmptyScalarAddFailsBeforeClaim(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-nonempty-scalar-add")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	profile := ir.Model{
		Name: "profile", GoName: "Profile", DBTable: "news_profile",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 80},
		},
	}
	profileSQL, err := compileMigrationCreateModel(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, profileSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "news_profile" ("name") VALUES ('occupied')`); err != nil {
		t.Fatal(err)
	}
	extra := ir.Field{Name: "active", GoName: "Active", Column: "active", Kind: ir.FieldBoolean}
	profileAfter := profile.Clone()
	profileAfter.Fields = append(profileAfter.Fields, extra)
	target, source, sourceField := sqliteRelationTestModels()
	intent := sqliteRelationApplyIntent(target, source, sourceField)
	intent.Operations = append([]migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationAddField,
		Before:         profile,
		After:          profileAfter,
	}}, intent.Operations...)
	intent.Operations[1].OperationIndex = 1
	intent.Operations[2].OperationIndex = 2

	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if transaction != nil || err == nil {
		t.Fatalf("BeginMigration() = (%v, %v), want nonempty AddField failure", transaction, err)
	}
	assertSQLiteRelationCapabilityFeature(t, err, "sqlite_add_field")
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
	var columns int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('news_profile') WHERE "name" = 'active'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("active column count = (%d, %v), want 0", columns, err)
	}
}

func TestSQLiteRelationDeletePreflightRejectsUnsealedSchemaHazards(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, context.Context, *Backend, ir.Model, ir.Model)
	}{
		{
			name: "inbound_foreign_key",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, target, _ ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "outside_child" ("author_id" INTEGER, FOREIGN KEY ("author_id") REFERENCES "news_author" ("id"))`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "user_index",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE INDEX "article_title_custom" ON "news_article" ("title")`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "user_trigger",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "delete_audit" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "article_delete_audit" AFTER DELETE ON "news_article" BEGIN INSERT INTO "delete_audit" ("id") VALUES (1); END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dependent_view",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE VIEW "article_view" AS SELECT "title" FROM "news_article"`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trigger_when_subquery",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "trigger_owner" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "article_when_dependency" AFTER INSERT ON "trigger_owner" `+
					`WHEN EXISTS (SELECT 1 FROM "news_article") BEGIN SELECT 1; END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trigger_single_quoted_dml_target",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "trigger_owner_literal" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "article_literal_dependency" AFTER INSERT ON "trigger_owner_literal" `+
					`BEGIN INSERT INTO 'news_article' ("title", "author_id") VALUES ('literal', 1); END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trigger_when_comma_join_single_quoted_target",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				for _, statement := range []string{
					`CREATE TABLE "trigger_owner_comma" ("id" INTEGER)`,
					`CREATE TABLE "trigger_other" ("id" INTEGER)`,
				} {
					if _, err := backend.ExecContext(ctx, statement); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "article_comma_dependency" `+
					`AFTER INSERT ON "trigger_owner_comma" `+
					`WHEN EXISTS (SELECT 1 FROM "trigger_other", 'news_article') BEGIN SELECT 1; END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trigger_update_or_ignore_single_quoted_target",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE "trigger_owner_update" ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "article_update_dependency" `+
					`AFTER INSERT ON "trigger_owner_update" BEGIN `+
					`UPDATE OR IGNORE 'news_article' SET "title" = "title"; END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trigger_unquoted_unicode_owner_with_touched_body",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE TABLE café ("id" INTEGER)`); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "unicode_owner_dependency" `+
					`AFTER INSERT ON café BEGIN `+
					`SELECT * FROM "news_article"; SELECT * FROM "café"; END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "virtual_table_content_dependency",
			setup: func(t *testing.T, ctx context.Context, backend *Backend, _, source ir.Model) {
				if _, err := backend.ExecContext(ctx, `CREATE  VIRTUAL TABLE "article_search" USING fts5("title", content='news_article')`); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			backend, err := OpenMemory(ctx, "relation-delete-hazard-"+test.name)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Close() })
			target, source, sourceField := sqliteRelationTestModels()
			migration := migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"}
			seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, backend, migration, target, source, sourceField)
			test.setup(t, ctx, backend, target, source)

			session := openSQLiteRelationSession(t, backend)
			if _, err := session.ReadAppliedMigrations(ctx); err != nil {
				t.Fatal(err)
			}
			concrete := session.(*sqliteRevisionFencedSession)
			var checkpoints []sqliteRelationBeginCheckpoint
			concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
				checkpoints = append(checkpoints, checkpoint)
			}
			transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
				Migration: migration,
				Kind:      migrationbackend.HistoryTransitionUnapply,
			}, sqliteRelationUnapplyIntent(target, source, sourceField))
			if transaction != nil || err == nil {
				t.Fatalf("BeginMigration() = (%v, %v), want closed preflight failure", transaction, err)
			}
			assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
			if !sqliteRelationTestTableExists(t, backend, target.DBTable) || !sqliteRelationTestTableExists(t, backend, source.DBTable) {
				t.Fatal("failed destructive preflight changed relation tables")
			}
		})
	}
}

func TestSQLiteRelationControlInboundForeignKeyFailsBeforeClaimWithoutCascade(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-control-inbound")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	bootstrap := migrationbackend.AppliedMigration{App: "bootstrap", Name: "0001"}
	seedSQLiteMigrationHistory(t, ctx, backend, bootstrap)
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "control_child" (`+
		`"app" VARCHAR(255) NOT NULL, "name" VARCHAR(255) NOT NULL, `+
		`FOREIGN KEY ("app", "name") REFERENCES "godj_migrations" ("app", "name") ON DELETE CASCADE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "control_child" ("app", "name") VALUES (?, ?)`, bootstrap.App, bootstrap.Name); err != nil {
		t.Fatal(err)
	}

	target, source, sourceField := sqliteRelationTestModels()
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, sqliteRelationApplyIntent(target, source, sourceField))
	if transaction != nil || err == nil {
		t.Fatalf("BeginMigration() = (%v, %v), want control-inbound failure", transaction, err)
	}
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
	var rows int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "control_child"`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("control child rows = (%d, %v), want unchanged 1", rows, err)
	}
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || !reflect.DeepEqual(snapshot.records, []migrationbackend.AppliedMigration{bootstrap}) {
		t.Fatalf("history after control-inbound failure = (%+v, %v)", snapshot, err)
	}
}

func TestSQLiteRelationForeignKeyCheckRunsBeforeRecorder(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-fk-check-before-recorder")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	target, source, sourceField := sqliteRelationTestModels()
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginMigration(ctx, transition, sqliteRelationApplyIntent(target, source, sourceField))
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.CreateModel(ctx, target); err != nil {
		t.Fatal(err)
	}
	fault := &migrationSQLFault{method: "query", contains: "foreign_key_check", code: 5, remaining: 1}
	installMigrationTransactionFault(transaction, fault)
	if err := transaction.CreateModel(ctx, source); err == nil {
		t.Fatal("last operation succeeded despite foreign_key_check fault")
	}
	if fault.remainingCount() != 0 {
		t.Fatal("foreign_key_check fault was not reached by the last operation")
	}
	if err := transaction.RecordApplied(ctx, transition.Migration.App, transition.Migration.Name); err == nil {
		t.Fatal("recorder ran after last-operation foreign_key_check failure")
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err == nil || outcome.Durability != migrationbackend.CommitRolledBack {
		t.Fatalf("CommitFenced() = (%+v, %v), want rolled back failure", outcome, err)
	}
	if sqliteRelationTestTableExists(t, backend, target.DBTable) || sqliteRelationTestTableExists(t, backend, source.DBTable) {
		t.Fatal("foreign_key_check failure published relation DDL")
	}

	assertSQLiteLoadedRelationErrorTaxonomy(t)
}

func TestSQLiteRelationPhysicalValidationCachesRepeatedQueries(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-physical-cache")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	var columns strings.Builder
	columns.WriteString(`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT`)
	for index := 0; index < 64; index++ {
		fmt.Fprintf(&columns, `, "field_%d" BOOLEAN NOT NULL`, index)
	}
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "cache_parent" (`+columns.String()+`)`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 64; index++ {
		statement := fmt.Sprintf(`CREATE TABLE "cache_child_%d" (`+
			`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "parent_id" INTEGER, `+
			`FOREIGN KEY ("parent_id") REFERENCES "cache_parent" ("id"))`, index)
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	counting := &countingRelationSQLExecutor{migrationSQLExecutor: backend.database}
	cache := newSQLiteRelationPhysicalValidationCache()
	for index := 0; index < 128; index++ {
		if err := cache.assertAutoKey(ctx, counting, "cache_parent", "id"); err != nil {
			t.Fatal(err)
		}
	}
	if counting.queryCalls != 1 {
		t.Fatalf("shared AutoKey validation QueryContext calls = %d, want 1", counting.queryCalls)
	}

	catalog, err := loadSQLiteRelationCatalog(ctx, backend.database)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := buildSQLiteRelationPhysicalGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	counting.queryCalls = 0
	dependencyIndex, err := buildSQLiteRelationRemoveDependencyIndex(
		ctx,
		counting,
		catalog,
		graph,
		map[string]struct{}{sqliteRelationIdentifierKey("cache_parent"): {}},
	)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 64; index++ {
		if owner, referenced := dependencyIndex.owner("cache_parent", fmt.Sprintf("field_%d", index)); referenced {
			t.Fatalf("unrelated removed field_%d reported inbound owner %q", index, owner)
		}
	}
	if owner, referenced := dependencyIndex.owner("cache_parent", "id"); !referenced || owner != "cache_child_0" {
		t.Fatalf("referenced parent id owner = (%q, %t), want cache_child_0", owner, referenced)
	}
	if counting.queryCalls != 64 || dependencyIndex.ownerVisits != 64 || dependencyIndex.foreignKeyVisits != 64 {
		t.Fatalf(
			"64x64 Remove dependency work = queries:%d owners:%d foreign-keys:%d, want 64/64/64",
			counting.queryCalls,
			dependencyIndex.ownerVisits,
			dependencyIndex.foreignKeyVisits,
		)
	}
}

func TestSQLiteRelationScalarRemoveRejectsExternalInboundColumnBeforeClaim(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-remove-inbound-column")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	target, source, sourceField := sqliteRelationTestModels()
	migration := migrationbackend.AppliedMigration{App: "news", Name: "0001_relation_with_profile_field"}
	seedSQLiteRelationPhysicalSchemaAndHistory(t, ctx, backend, migration, target, source, sourceField)
	profileAfter := ir.Model{
		Name: "profile", GoName: "Profile", DBTable: "news_profile",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
	profileBefore := profileAfter.Clone()
	profileBefore.Fields = append(profileBefore.Fields, ir.Field{
		Name: "code", GoName: "Code", Column: "code", Kind: ir.FieldChar, MaxLength: 40,
	})
	profileSQL, err := compileMigrationCreateModel(profileBefore)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		profileSQL,
		`CREATE TABLE "outside_profile_child" (` +
			`"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, ` +
			`"profile_code" VARCHAR(40), ` +
			`FOREIGN KEY ("profile_code") REFERENCES "news_profile" ("code"))`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed scalar RemoveField dependency %q: %v", statement, err)
		}
	}

	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{
			OperationIndex: 2,
			Kind:           migrationbackend.MigrationDeleteModel,
			Before:         source,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
		{OperationIndex: 1, Kind: migrationbackend.MigrationDeleteModel, Before: target},
		{OperationIndex: 0, Kind: migrationbackend.MigrationRemoveField, Before: profileBefore, After: profileAfter},
	}}
	session := openSQLiteRelationSession(t, backend)
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	concrete := session.(*sqliteRevisionFencedSession)
	var checkpoints []sqliteRelationBeginCheckpoint
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		checkpoints = append(checkpoints, checkpoint)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migration,
		Kind:      migrationbackend.HistoryTransitionUnapply,
	}, intent)
	if transaction != nil || err == nil {
		t.Fatalf("BeginMigration() = (%v, %v), want inbound removed-column failure", transaction, err)
	}
	assertSQLiteRelationCapabilityFeature(t, err, "sqlite_drop_column")
	assertSQLiteRelationNoClaimCheckpoint(t, checkpoints)
	if concrete.state != revisionSessionPoisoned || concrete.active != nil {
		t.Fatalf("session after removed-column preflight = (state:%d active:%v), want poisoned without active transaction", concrete.state, concrete.active)
	}
	if !sqliteRelationTestTableExists(t, backend, target.DBTable) ||
		!sqliteRelationTestTableExists(t, backend, source.DBTable) ||
		!sqliteRelationTestTableExists(t, backend, profileBefore.DBTable) {
		t.Fatal("removed-column preflight failure changed a sealed table")
	}
	var codeColumns int
	if err := backend.database.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM pragma_table_info('news_profile') WHERE "name" = 'code'`,
	).Scan(&codeColumns); err != nil || codeColumns != 1 {
		t.Fatalf("profile code columns after failure = (%d, %v), want unchanged 1", codeColumns, err)
	}
	snapshot, err := readAtomicMigrationRevisionSnapshot(ctx, backend)
	if err != nil || !reflect.DeepEqual(snapshot.records, []migrationbackend.AppliedMigration{migration}) {
		t.Fatalf("history after removed-column preflight = (%+v, %v)", snapshot, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRelationTargetLookupUsesSealedTableIndexAtOperationLimit(t *testing.T) {
	target, _, sourceField := sqliteRelationTestModels()
	sourceField.Relation.Target.AppLabel = "accounts"
	intent := migrationbackend.MigrationIntent{
		Operations: make([]migrationbackend.MigrationOperation, sqliteRelationMaxOperations),
	}
	models := make([]ir.Model, sqliteRelationMaxOperations)
	longSuffix := strings.Repeat("x", 1_024)
	for index := range intent.Operations {
		field := sourceField.Clone()
		field.Relation.Reverse.Name = fmt.Sprintf("sources_%04d", index)
		model := ir.Model{
			Name:    fmt.Sprintf("source_%04d", index),
			GoName:  fmt.Sprintf("Source%d", index),
			DBTable: fmt.Sprintf("news_source_%04d_%s", index, longSuffix),
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				field,
			},
		}
		models[index] = model
		intent.Operations[index] = migrationbackend.MigrationOperation{
			OperationIndex: index,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          model,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: field,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		}
	}
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_large_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err != nil {
		t.Fatalf("validateAndSealSQLiteRelationIntent(max operations): %v", err)
	}
	if len(seal.targetOperationByTable) != sqliteRelationMaxOperations {
		t.Fatalf("sealed target table index size = %d, want %d", len(seal.targetOperationByTable), sqliteRelationMaxOperations)
	}
	for index := range models {
		targets, known := sqliteRelationTargetsForModel(&seal, models[index])
		if !known || len(targets) != 1 || !reflect.DeepEqual(targets[0].TargetModel, target) {
			t.Fatalf("indexed targets[%d] = (%#v, %t)", index, targets, known)
		}
	}
}

func TestSQLiteRelationReverseCollisionCheckBoundsLongModelNameAtFieldLimit(t *testing.T) {
	fields := make([]ir.Field, sqliteRelationMaxFields)
	fields[0] = ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}
	for index := 1; index < len(fields); index++ {
		fields[index] = ir.Field{
			Name:   fmt.Sprintf("field_%04d", index),
			GoName: fmt.Sprintf("Field%d", index),
			Column: fmt.Sprintf("field_%04d", index),
			Kind:   ir.FieldBoolean,
		}
	}
	wide := ir.Model{
		Name:    strings.Repeat("a", sqliteRelationMaxStringBytes-1),
		GoName:  "WideModel",
		DBTable: "wide_model",
		Fields:  fields,
	}
	reverseIndex := newSQLiteRelationReverseOwnerIndex()
	reverseApp := reverseIndex.app("news")
	if _, exists := reverseApp.register(
		wide.Name,
		"reserved_reverse",
		sqliteRelationReverseOwner{model: "source", field: "field"},
	); exists {
		t.Fatal("fresh reverse owner index reported duplicate registration")
	}
	reverseApp.modelLookups = 0
	if field, owner, collision := reverseApp.firstFieldCollision(wide.Name, wide.Fields); collision {
		t.Fatalf("unexpected long-model reverse collision = field:%#v owner:%#v", field, owner)
	}
	if reverseApp.modelLookups != 1 {
		t.Fatalf("long model reverse outer-map lookups = %d, want exactly 1 for all %d fields", reverseApp.modelLookups, len(wide.Fields))
	}
	target, source, sourceField := sqliteRelationTestModels()
	intent := sqliteRelationApplyIntent(target, source, sourceField)
	intent.Operations = append([]migrationbackend.MigrationOperation{{
		OperationIndex: 0,
		Kind:           migrationbackend.MigrationCreateModel,
		After:          wide,
	}}, intent.Operations...)
	intent.Operations[1].OperationIndex = 1
	intent.Operations[2].OperationIndex = 2
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_wide_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err != nil {
		t.Fatalf("validateAndSealSQLiteRelationIntent(long model at field limit): %v", err)
	}
	if len(seal.intent.Operations) != 3 || seal.intent.Operations[0].After.Name != wide.Name {
		t.Fatal("long-name boundary intent was not sealed exactly")
	}
}

func TestSQLiteRelationReverseCollisionIndexSelectsLongTransitionAppOnce(t *testing.T) {
	longApp := strings.Repeat("a", sqliteRelationMaxStringBytes-1)
	reverseIndex := newSQLiteRelationReverseOwnerIndex()
	transitionOwners := reverseIndex.app(longApp)
	for index := 0; index < sqliteRelationMaxOperations; index++ {
		model := fmt.Sprintf("model_%04d", index)
		if _, _, collision := transitionOwners.firstFieldCollision(model, nil); collision {
			t.Fatalf("unexpected reverse collision for %q", model)
		}
	}
	if reverseIndex.appLookups != 1 {
		t.Fatalf("long transition app outer-map lookups = %d, want exactly 1 for %d operations", reverseIndex.appLookups, sqliteRelationMaxOperations)
	}
	if transitionOwners.modelLookups != sqliteRelationMaxOperations {
		t.Fatalf("short model lookups = %d, want %d", transitionOwners.modelLookups, sqliteRelationMaxOperations)
	}

	operations := make([]migrationbackend.MigrationOperation, sqliteRelationMaxOperations)
	for index := 0; index < len(operations)-2; index++ {
		operations[index] = migrationbackend.MigrationOperation{
			OperationIndex: index,
			Kind:           migrationbackend.MigrationCreateModel,
			After: ir.Model{
				Name:    fmt.Sprintf("scalar_%04d", index),
				GoName:  fmt.Sprintf("Scalar%d", index),
				DBTable: fmt.Sprintf("scalar_%04d", index),
				Fields: []ir.Field{{
					Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true,
				}},
			},
		}
	}
	target, source, sourceField := sqliteRelationTestModels()
	sourceField.Relation.Target.AppLabel = longApp
	source.Fields[2] = sourceField
	operations[len(operations)-2] = migrationbackend.MigrationOperation{
		OperationIndex: len(operations) - 2,
		Kind:           migrationbackend.MigrationCreateModel,
		After:          target,
	}
	operations[len(operations)-1] = migrationbackend.MigrationOperation{
		OperationIndex: len(operations) - 1,
		Kind:           migrationbackend.MigrationCreateModel,
		After:          source,
		Targets: []migrationbackend.MigrationTarget{{
			SourceField: sourceField,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}},
	}
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: longApp, Name: "0001_relation"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, migrationbackend.MigrationIntent{Operations: operations})
	if err != nil {
		t.Fatalf("validateAndSealSQLiteRelationIntent(long transition app at operation limit): %v", err)
	}
	if len(seal.intent.Operations) != sqliteRelationMaxOperations {
		t.Fatalf("sealed operation count = %d, want %d", len(seal.intent.Operations), sqliteRelationMaxOperations)
	}
}

func TestSQLiteRelationResourceScanBoundsTransitionIdentityBeforeClone(t *testing.T) {
	target, source, sourceField := sqliteRelationTestModels()
	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{
			App:  strings.Repeat("a", sqliteRelationMaxStringBytes+1),
			Name: "0001_relation",
		},
		Kind: migrationbackend.HistoryTransitionApply,
	}
	_, err := validateAndSealSQLiteRelationIntent(transition, sqliteRelationApplyIntent(target, source, sourceField))
	if err == nil || !strings.Contains(err.Error(), "transition.migration.app") || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("oversized transition app error = %v", err)
	}
}

func TestSQLiteRelationReverseOwnersRetainLongSourceStructurallyAtTargetLimit(t *testing.T) {
	target, _, baseField := sqliteRelationTestModels()
	fields := make([]ir.Field, sqliteRelationMaxFields)
	targets := make([]migrationbackend.MigrationTarget, sqliteRelationMaxFields-1)
	fields[0] = ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}
	for index := 1; index < len(fields); index++ {
		field := baseField.Clone()
		field.Name = fmt.Sprintf("target_%04d", index)
		field.GoName = fmt.Sprintf("Target%d", index)
		field.Column = fmt.Sprintf("target_%04d_id", index)
		field.Relation.Reverse.Name = fmt.Sprintf("sources_%04d", index)
		fields[index] = field
		targets[index-1] = migrationbackend.MigrationTarget{
			SourceField: field,
			TargetModel: target,
			TargetKey:   target.Fields[0],
		}
	}
	longSourceName := strings.Repeat("s", sqliteRelationMaxStringBytes-1)
	source := ir.Model{
		Name:    longSourceName,
		GoName:  "WideRelationSource",
		DBTable: "wide_relation_source",
		Fields:  fields,
	}
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{
			OperationIndex: 0,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          target,
		},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          source,
			Targets:        targets,
		},
	}}
	seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0001_wide_relation_source"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}, intent)
	if err != nil {
		t.Fatalf("validateAndSealSQLiteRelationIntent(long source at target limit): %v", err)
	}
	if len(seal.intent.Operations[1].Targets) != sqliteRelationMaxFields-1 || seal.intent.Operations[1].After.Name != longSourceName {
		t.Fatal("long-source relation owners were not sealed at the target limit")
	}
}

func TestSQLiteRelationCatalogJoinsRowsCloseFailure(t *testing.T) {
	closeErr := errors.New("catalog rows close failure")
	backend := openMigrationHistoryFaultBackend(t, historyFault{
		rows:     [][]driver.Value{{"too", "few"}},
		closeErr: closeErr,
	})
	_, err := loadSQLiteRelationCatalog(context.Background(), backend.database)
	if err == nil || !errors.Is(err, closeErr) || !strings.Contains(err.Error(), "expected 2 destination arguments") {
		t.Fatalf("loadSQLiteRelationCatalog() error = %v, want scan primary joined with rows.Close cause", err)
	}
}

func sqliteRelationTestModels() (ir.Model, ir.Model, ir.Field) {
	target := ir.Model{
		Name: "author", GoName: "Author", DBTable: "news_author",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 120},
		},
	}
	sourceField := ir.Field{
		Name: "author", GoName: "Author", Column: "author_id", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "news", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "articles"},
			OnDelete:    ir.DeleteProtect,
		},
	}
	source := ir.Model{
		Name: "article", GoName: "Article", DBTable: "news_article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
			sourceField,
		},
	}
	return target, source, sourceField
}

func sqliteRelationApplyIntent(target, source ir.Model, sourceField ir.Field) migrationbackend.MigrationIntent {
	return migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{OperationIndex: 0, Kind: migrationbackend.MigrationCreateModel, After: target},
		{
			OperationIndex: 1,
			Kind:           migrationbackend.MigrationCreateModel,
			After:          source,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
	}}
}

func sqliteRelationUnapplyIntent(target, source ir.Model, sourceField ir.Field) migrationbackend.MigrationIntent {
	return migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{
		{
			OperationIndex: 1,
			Kind:           migrationbackend.MigrationDeleteModel,
			Before:         source,
			Targets: []migrationbackend.MigrationTarget{{
				SourceField: sourceField,
				TargetModel: target,
				TargetKey:   target.Fields[0],
			}},
		},
		{OperationIndex: 0, Kind: migrationbackend.MigrationDeleteModel, Before: target},
	}}
}

func seedSQLiteRelationPhysicalSchemaAndHistory(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
	migration migrationbackend.AppliedMigration,
	target,
	source ir.Model,
	sourceField ir.Field,
) {
	t.Helper()
	targetSQL, err := compileMigrationCreateModel(target)
	if err != nil {
		t.Fatal(err)
	}
	sourceSQL, err := compileSQLiteRelationCreateModel(source, []migrationbackend.MigrationTarget{{
		SourceField: sourceField,
		TargetModel: target,
		TargetKey:   target.Fields[0],
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{targetSQL, sourceSQL} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed relation schema %q: %v", statement, err)
		}
	}
	seedSQLiteMigrationHistory(t, ctx, backend, migration)
}

func seedSQLiteMigrationHistory(
	t *testing.T,
	ctx context.Context,
	backend *Backend,
	migration migrationbackend.AppliedMigration,
) {
	t.Helper()
	session, err := backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	transaction, err := session.BeginMigration(ctx, migrationbackend.HistoryTransition{
		Migration: migration,
		Kind:      migrationbackend.HistoryTransitionApply,
	}, emptySQLiteMigrationIntent())

	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.RecordApplied(ctx, migration.App, migration.Name); err != nil {
		t.Fatal(err)
	}
	outcome, err := transaction.CommitFenced(ctx)
	if err != nil || outcome.Durability != migrationbackend.CommitCommitted {
		t.Fatalf("seed history CommitFenced() = (%+v, %v)", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

type sqliteNullableRelationAddSnapshot struct {
	FormatVersion        int64
	Epoch                [16]byte
	Revision             int64
	Fingerprint          [32]byte
	History              []migrationbackend.AppliedMigration
	Schema               []sqliteNullableRelationAddSchemaObject
	Columns              []sqliteNullableRelationAddColumn
	ForeignKeys          []sqliteNullableRelationAddForeignKey
	Sequences            []sqliteNullableRelationAddSequence
	Authors              []sqliteNullableRelationAddAuthor
	Articles             []sqliteNullableRelationAddArticle
	ForeignKeyViolations int
}

type sqliteNullableRelationAddSchemaObject struct {
	Type       string
	Name       string
	Table      string
	Definition string
}

type sqliteNullableRelationAddColumn struct {
	Table        string
	Position     int
	Name         string
	DeclaredType string
	NotNull      int
	DefaultValue sql.NullString
	PrimaryKey   int
	Hidden       int
}

type sqliteNullableRelationAddForeignKey struct {
	SourceTable string
	ID          int
	Sequence    int
	TargetTable string
	FromColumn  string
	ToColumn    string
	OnUpdate    string
	OnDelete    string
	Match       string
}

type sqliteNullableRelationAddSequence struct {
	Table string
	Value int64
}

type sqliteNullableRelationAddAuthor struct {
	ID   int64
	Name string
}

type sqliteNullableRelationAddArticle struct {
	ID       int64
	Title    string
	AuthorID int64
}

func readSQLiteNullableRelationAddSnapshot(t *testing.T, path string) sqliteNullableRelationAddSnapshot {
	t.Helper()
	ctx := context.Background()
	reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatalf("open read-only nullable Add snapshot: %v", err)
	}
	reader.SetMaxOpenConns(1)
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close read-only nullable Add snapshot: %v", err)
		}
	}()
	tx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read-only nullable Add snapshot: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	snapshot := sqliteNullableRelationAddSnapshot{
		History:     make([]migrationbackend.AppliedMigration, 0),
		Schema:      make([]sqliteNullableRelationAddSchemaObject, 0),
		Columns:     make([]sqliteNullableRelationAddColumn, 0),
		ForeignKeys: make([]sqliteNullableRelationAddForeignKey, 0),
		Sequences:   make([]sqliteNullableRelationAddSequence, 0),
		Authors:     make([]sqliteNullableRelationAddAuthor, 0),
		Articles:    make([]sqliteNullableRelationAddArticle, 0),
	}
	var epoch, fingerprint []byte
	if err := tx.QueryRowContext(
		ctx,
		`SELECT "format_version", "epoch", "revision", "history_fingerprint" `+
			`FROM "godj_migration_revision" WHERE "singleton" = 1`,
	).Scan(&snapshot.FormatVersion, &epoch, &snapshot.Revision, &fingerprint); err != nil {
		t.Fatalf("read nullable Add revision token: %v", err)
	}
	if len(epoch) != len(snapshot.Epoch) || len(fingerprint) != len(snapshot.Fingerprint) {
		t.Fatalf("nullable Add token bytes = epoch:%d fingerprint:%d", len(epoch), len(fingerprint))
	}
	copy(snapshot.Epoch[:], epoch)
	copy(snapshot.Fingerprint[:], fingerprint)

	historyRows, err := tx.QueryContext(ctx, `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
	if err != nil {
		t.Fatalf("read nullable Add history: %v", err)
	}
	for historyRows.Next() {
		var record migrationbackend.AppliedMigration
		if err := historyRows.Scan(&record.App, &record.Name); err != nil {
			_ = historyRows.Close()
			t.Fatalf("scan nullable Add history: %v", err)
		}
		snapshot.History = append(snapshot.History, record)
	}
	if err := historyRows.Err(); err != nil {
		_ = historyRows.Close()
		t.Fatalf("iterate nullable Add history: %v", err)
	}
	if err := historyRows.Close(); err != nil {
		t.Fatalf("close nullable Add history: %v", err)
	}

	schemaRows, err := tx.QueryContext(
		ctx,
		`SELECT "type", "name", "tbl_name", COALESCE("sql", '') FROM main.sqlite_schema `+
			`WHERE "name" NOT LIKE 'sqlite_%' ORDER BY "type", "name", "tbl_name", "sql"`,
	)
	if err != nil {
		t.Fatalf("read nullable Add schema: %v", err)
	}
	for schemaRows.Next() {
		var object sqliteNullableRelationAddSchemaObject
		if err := schemaRows.Scan(&object.Type, &object.Name, &object.Table, &object.Definition); err != nil {
			_ = schemaRows.Close()
			t.Fatalf("scan nullable Add schema: %v", err)
		}
		snapshot.Schema = append(snapshot.Schema, object)
	}
	if err := schemaRows.Err(); err != nil {
		_ = schemaRows.Close()
		t.Fatalf("iterate nullable Add schema: %v", err)
	}
	if err := schemaRows.Close(); err != nil {
		t.Fatalf("close nullable Add schema: %v", err)
	}

	for _, table := range []string{"news_author", "news_article"} {
		columnRows, err := tx.QueryContext(ctx, `PRAGMA main.table_xinfo("`+table+`")`)
		if err != nil {
			t.Fatalf("read nullable Add columns for %s: %v", table, err)
		}
		for columnRows.Next() {
			column := sqliteNullableRelationAddColumn{Table: table}
			if err := columnRows.Scan(
				&column.Position,
				&column.Name,
				&column.DeclaredType,
				&column.NotNull,
				&column.DefaultValue,
				&column.PrimaryKey,
				&column.Hidden,
			); err != nil {
				_ = columnRows.Close()
				t.Fatalf("scan nullable Add columns for %s: %v", table, err)
			}
			snapshot.Columns = append(snapshot.Columns, column)
		}
		if err := columnRows.Err(); err != nil {
			_ = columnRows.Close()
			t.Fatalf("iterate nullable Add columns for %s: %v", table, err)
		}
		if err := columnRows.Close(); err != nil {
			t.Fatalf("close nullable Add columns for %s: %v", table, err)
		}

		foreignKeyRows, err := tx.QueryContext(ctx, `PRAGMA main.foreign_key_list("`+table+`")`)
		if err != nil {
			t.Fatalf("read nullable Add foreign keys for %s: %v", table, err)
		}
		for foreignKeyRows.Next() {
			foreignKey := sqliteNullableRelationAddForeignKey{SourceTable: table}
			if err := foreignKeyRows.Scan(
				&foreignKey.ID,
				&foreignKey.Sequence,
				&foreignKey.TargetTable,
				&foreignKey.FromColumn,
				&foreignKey.ToColumn,
				&foreignKey.OnUpdate,
				&foreignKey.OnDelete,
				&foreignKey.Match,
			); err != nil {
				_ = foreignKeyRows.Close()
				t.Fatalf("scan nullable Add foreign keys for %s: %v", table, err)
			}
			snapshot.ForeignKeys = append(snapshot.ForeignKeys, foreignKey)
		}
		if err := foreignKeyRows.Err(); err != nil {
			_ = foreignKeyRows.Close()
			t.Fatalf("iterate nullable Add foreign keys for %s: %v", table, err)
		}
		if err := foreignKeyRows.Close(); err != nil {
			t.Fatalf("close nullable Add foreign keys for %s: %v", table, err)
		}
	}

	sequenceRows, err := tx.QueryContext(ctx, `SELECT "name", "seq" FROM main.sqlite_sequence ORDER BY "name"`)
	if err != nil {
		t.Fatalf("read nullable Add sequences: %v", err)
	}
	for sequenceRows.Next() {
		var sequence sqliteNullableRelationAddSequence
		if err := sequenceRows.Scan(&sequence.Table, &sequence.Value); err != nil {
			_ = sequenceRows.Close()
			t.Fatalf("scan nullable Add sequences: %v", err)
		}
		snapshot.Sequences = append(snapshot.Sequences, sequence)
	}
	if err := sequenceRows.Err(); err != nil {
		_ = sequenceRows.Close()
		t.Fatalf("iterate nullable Add sequences: %v", err)
	}
	if err := sequenceRows.Close(); err != nil {
		t.Fatalf("close nullable Add sequences: %v", err)
	}

	authorRows, err := tx.QueryContext(ctx, `SELECT "id", "name" FROM "news_author" ORDER BY "id"`)
	if err != nil {
		t.Fatalf("read nullable Add authors: %v", err)
	}
	for authorRows.Next() {
		var author sqliteNullableRelationAddAuthor
		if err := authorRows.Scan(&author.ID, &author.Name); err != nil {
			_ = authorRows.Close()
			t.Fatalf("scan nullable Add authors: %v", err)
		}
		snapshot.Authors = append(snapshot.Authors, author)
	}
	if err := authorRows.Err(); err != nil {
		_ = authorRows.Close()
		t.Fatalf("iterate nullable Add authors: %v", err)
	}
	if err := authorRows.Close(); err != nil {
		t.Fatalf("close nullable Add authors: %v", err)
	}

	articleRows, err := tx.QueryContext(ctx, `SELECT "id", "title", "author_id" FROM "news_article" ORDER BY "id"`)
	if err != nil {
		t.Fatalf("read nullable Add articles: %v", err)
	}
	for articleRows.Next() {
		var article sqliteNullableRelationAddArticle
		if err := articleRows.Scan(&article.ID, &article.Title, &article.AuthorID); err != nil {
			_ = articleRows.Close()
			t.Fatalf("scan nullable Add articles: %v", err)
		}
		snapshot.Articles = append(snapshot.Articles, article)
	}
	if err := articleRows.Err(); err != nil {
		_ = articleRows.Close()
		t.Fatalf("iterate nullable Add articles: %v", err)
	}
	if err := articleRows.Close(); err != nil {
		t.Fatalf("close nullable Add articles: %v", err)
	}

	violationRows, err := tx.QueryContext(ctx, `PRAGMA main.foreign_key_check`)
	if err != nil {
		t.Fatalf("read nullable Add foreign-key violations: %v", err)
	}
	for violationRows.Next() {
		snapshot.ForeignKeyViolations++
		var table, parent string
		var rowID sql.NullInt64
		var foreignKeyID int
		if err := violationRows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			_ = violationRows.Close()
			t.Fatalf("scan nullable Add foreign-key violation: %v", err)
		}
	}
	if err := violationRows.Err(); err != nil {
		_ = violationRows.Close()
		t.Fatalf("iterate nullable Add foreign-key violations: %v", err)
	}
	if err := violationRows.Close(); err != nil {
		t.Fatalf("close nullable Add foreign-key violations: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit read-only nullable Add snapshot: %v", err)
	}
	committed = true
	return snapshot
}

func assertSQLiteNullableRelationAddSeedSnapshot(
	t *testing.T,
	snapshot sqliteNullableRelationAddSnapshot,
	initial migrationbackend.AppliedMigration,
) {
	t.Helper()
	wantHistory := []migrationbackend.AppliedMigration{initial}
	wantAuthors := []sqliteNullableRelationAddAuthor{{ID: 1, Name: "Ada"}, {ID: 2, Name: "Grace"}}
	wantArticles := []sqliteNullableRelationAddArticle{
		{ID: 1, Title: "first", AuthorID: 1},
		{ID: 2, Title: "second", AuthorID: 2},
	}
	wantSequences := []sqliteNullableRelationAddSequence{{Table: "news_article", Value: 2}, {Table: "news_author", Value: 2}}
	if snapshot.FormatVersion != migrationRevisionFormatVersion || snapshot.Revision != 1 ||
		snapshot.Epoch == ([16]byte{}) || snapshot.Fingerprint != fingerprintMigrationHistory(snapshot.History) ||
		!reflect.DeepEqual(snapshot.History, wantHistory) ||
		!reflect.DeepEqual(snapshot.Authors, wantAuthors) ||
		!reflect.DeepEqual(snapshot.Articles, wantArticles) ||
		!reflect.DeepEqual(snapshot.Sequences, wantSequences) ||
		snapshot.ForeignKeyViolations != 0 {
		t.Fatalf("nullable Add fault seed snapshot = %+v", snapshot)
	}
	if len(snapshot.Columns) != 5 || len(snapshot.ForeignKeys) != 1 {
		t.Fatalf("nullable Add fault seed shape = columns:%+v foreignKeys:%+v", snapshot.Columns, snapshot.ForeignKeys)
	}
	foreignKey := snapshot.ForeignKeys[0]
	if foreignKey.SourceTable != "news_article" || foreignKey.TargetTable != "news_author" ||
		foreignKey.FromColumn != "author_id" || foreignKey.ToColumn != "id" ||
		foreignKey.Sequence != 0 || foreignKey.OnUpdate != "NO ACTION" ||
		foreignKey.OnDelete != "NO ACTION" || foreignKey.Match != "NONE" {
		t.Fatalf("nullable Add fault seed foreign key = %+v", foreignKey)
	}
	for _, object := range snapshot.Schema {
		if object.Name == "news_article" && strings.Contains(object.Definition, "editor_id") {
			t.Fatalf("nullable Add fault seed already contains editor column: %+v", object)
		}
	}
}

func loadSQLiteLoadedNullableRelationAddSet(t *testing.T, label string) migrations.LoadedDefinitionSet {
	t.Helper()
	suffix := strings.ReplaceAll(label, " ", "-")
	loaded, report, err := migrationdefinition.Load(
		migrationdefinition.Source{
			SourceID: "loaded-nullable-add-author-" + suffix,
			Document: sqliteLoadedNullableRelationAddAuthorDocument(),
		},
		migrationdefinition.Source{
			SourceID: "loaded-nullable-add-article-" + suffix,
			Document: sqliteLoadedNullableRelationAddArticleDocument(),
		},
		migrationdefinition.Source{
			SourceID: "loaded-nullable-add-editor-" + suffix,
			Document: sqliteLoadedNullableRelationAddEditorDocument(),
		},
	)
	if err != nil {
		t.Fatalf("Load(nullable Add taxonomy %s): %v", label, err)
	}
	if report.DocumentsReceived != 3 || report.HeadersValidated != 3 || report.OperationsDecoded != 3 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 3 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("Load(nullable Add taxonomy %s) report = %+v", label, report)
	}
	return loaded
}

func loadSQLiteLoadedRequiredRelationAddSet(t *testing.T, label string) migrations.LoadedDefinitionSet {
	t.Helper()
	suffix := strings.ReplaceAll(label, " ", "-")
	loaded, report, err := migrationdefinition.Load(
		migrationdefinition.Source{
			SourceID: "loaded-required-add-author-" + suffix,
			Document: sqliteLoadedNullableRelationAddAuthorDocument(),
		},
		migrationdefinition.Source{
			SourceID: "loaded-required-add-article-" + suffix,
			Document: sqliteLoadedNullableRelationAddArticleDocument(),
		},
		migrationdefinition.Source{
			SourceID: "loaded-required-add-editor-" + suffix,
			Document: sqliteLoadedRequiredRelationAddEditorDocument(),
		},
	)
	if err != nil {
		t.Fatalf("Load(required Add taxonomy %s): %v", label, err)
	}
	if report.DocumentsReceived != 3 || report.HeadersValidated != 3 || report.OperationsDecoded != 3 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 3 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("Load(required Add taxonomy %s) report = %+v", label, report)
	}
	return loaded
}

func sqliteLoadedNullableRelationAddAuthorDocument() []byte {
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"loaded-nullable-add","version":"1"},` +
		`"migration":{"app":"news","name":"0001_author","dependencies":[],"operations":[` +
		`{"kind":"create_model","app_label":"news","model":{` +
		`"name":"author","go_name":"Author","db_table":"news_author","fields":[` +
		`{"name":"id","go_name":"ID","column":"id","kind":"auto",` +
		`"primary_key":true,"nullable":false,"max_length":0,"default":null},` +
		`{"name":"name","go_name":"Name","column":"name","kind":"char",` +
		`"primary_key":false,"nullable":false,"max_length":120,"default":null}]}}]}}`)
}

func sqliteLoadedNullableRelationAddArticleDocument() []byte {
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"loaded-nullable-add","version":"1"},` +
		`"migration":{"app":"news","name":"0002_article",` +
		`"dependencies":[{"app":"news","name":"0001_author"}],"operations":[` +
		`{"kind":"create_model","app_label":"news","model":{` +
		`"name":"article","go_name":"Article","db_table":"news_article","fields":[` +
		`{"name":"id","go_name":"ID","column":"id","kind":"auto",` +
		`"primary_key":true,"nullable":false,"max_length":0,"default":null},` +
		`{"name":"title","go_name":"Title","column":"title","kind":"char",` +
		`"primary_key":false,"nullable":false,"max_length":200,"default":null},` +
		`{"name":"author","go_name":"Author","column":"author_id","kind":"foreign_key",` +
		`"primary_key":false,"nullable":false,"max_length":0,"default":null,` +
		`"relation":{"target":{"app_label":"news","model_name":"author"},` +
		`"cardinality":"many_to_one","reverse":{"name":"articles","disabled":false},` +
		`"on_delete":"protect"}}]}}]}}`)
}

func sqliteLoadedNullableRelationAddEditorDocument() []byte {
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"loaded-nullable-add","version":"1"},` +
		`"migration":{"app":"news","name":"0003_editor",` +
		`"dependencies":[{"app":"news","name":"0002_article"}],"operations":[` +
		`{"kind":"add_field","app_label":"news","model_name":"article","field":{` +
		`"name":"editor","go_name":"Editor","column":"editor_id","kind":"foreign_key",` +
		`"primary_key":false,"nullable":true,"max_length":0,"default":null,` +
		`"relation":{"target":{"app_label":"news","model_name":"author"},` +
		`"cardinality":"many_to_one","reverse":{"name":"edited_articles","disabled":false},` +
		`"on_delete":"protect"}}}]}}`)
}

func sqliteLoadedRequiredRelationAddEditorDocument() []byte {
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"loaded-required-add","version":"1"},` +
		`"migration":{"app":"news","name":"0003_editor",` +
		`"dependencies":[{"app":"news","name":"0002_article"}],"operations":[` +
		`{"kind":"add_field","app_label":"news","model_name":"article","field":{` +
		`"name":"editor","go_name":"Editor","column":"editor_id","kind":"foreign_key",` +
		`"primary_key":false,"nullable":false,"max_length":0,"default":null,` +
		`"relation":{"target":{"app_label":"news","model_name":"author"},` +
		`"cardinality":"many_to_one","reverse":{"name":"edited_articles","disabled":false},` +
		`"on_delete":"protect"}}}]}}`)
}

func assertSQLiteLoadedNullableRelationAddSeedState(t *testing.T, state migrations.ProjectState) {
	t.Helper()
	if state.FormatVersion() != migrations.StateFormatVersion ||
		!reflect.DeepEqual(state.Apps(), []string{"news"}) {
		t.Fatalf("nullable Add taxonomy seed state = format:%d apps:%v", state.FormatVersion(), state.Apps())
	}
	author, authorExists := state.Model("news", "author")
	article, articleExists := state.Model("news", "article")
	if !authorExists || len(author.Fields) != 2 || !articleExists || len(article.Fields) != 3 ||
		article.Fields[2].Relation == nil || article.Fields[2].Relation.Target.ModelName != "author" {
		t.Fatalf("nullable Add taxonomy seed models = author:%+v/%t article:%+v/%t", author, authorExists, article, articleExists)
	}
}

func assertSQLiteLoadedNullableRelationAddSeedSnapshot(t *testing.T, snapshot sqliteNullableRelationAddSnapshot) {
	t.Helper()
	wantHistory := []migrationbackend.AppliedMigration{
		{App: "news", Name: "0001_author"},
		{App: "news", Name: "0002_article"},
	}
	wantAuthors := []sqliteNullableRelationAddAuthor{{ID: 1, Name: "Ada"}, {ID: 2, Name: "Grace"}}
	wantArticles := []sqliteNullableRelationAddArticle{
		{ID: 1, Title: "first", AuthorID: 1},
		{ID: 2, Title: "second", AuthorID: 2},
	}
	wantSequences := []sqliteNullableRelationAddSequence{{Table: "news_article", Value: 2}, {Table: "news_author", Value: 2}}
	if snapshot.FormatVersion != migrationRevisionFormatVersion || snapshot.Revision != 2 ||
		snapshot.Epoch == ([16]byte{}) || snapshot.Fingerprint != fingerprintMigrationHistory(snapshot.History) ||
		!reflect.DeepEqual(snapshot.History, wantHistory) ||
		!reflect.DeepEqual(snapshot.Authors, wantAuthors) ||
		!reflect.DeepEqual(snapshot.Articles, wantArticles) ||
		!reflect.DeepEqual(snapshot.Sequences, wantSequences) ||
		snapshot.ForeignKeyViolations != 0 || len(snapshot.Columns) != 5 || len(snapshot.ForeignKeys) != 1 {
		t.Fatalf("loaded nullable Add seed snapshot = %+v", snapshot)
	}
	foreignKey := snapshot.ForeignKeys[0]
	if foreignKey.SourceTable != "news_article" || foreignKey.TargetTable != "news_author" ||
		foreignKey.FromColumn != "author_id" || foreignKey.ToColumn != "id" ||
		foreignKey.Sequence != 0 || foreignKey.OnUpdate != "NO ACTION" ||
		foreignKey.OnDelete != "NO ACTION" || foreignKey.Match != "NONE" {
		t.Fatalf("loaded nullable Add seed foreign key = %+v", foreignKey)
	}
	for _, object := range snapshot.Schema {
		if object.Name == "news_article" && strings.Contains(object.Definition, "editor_id") {
			t.Fatalf("loaded nullable Add seed already contains editor column: %+v", object)
		}
	}
}

func assertSQLiteLoadedNullableRelationAddIntent(
	t *testing.T,
	label string,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) {
	t.Helper()
	if transition != (migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0003_editor"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}) || len(intent.Operations) != 1 {
		t.Fatalf("%s loaded nullable Add begin payload = transition:%+v intent:%+v", label, transition, intent)
	}
	operation := intent.Operations[0]
	if operation.OperationIndex != 0 || operation.Kind != migrationbackend.MigrationAddField ||
		operation.Before.DBTable != "news_article" || len(operation.Before.Fields) != 3 ||
		operation.After.DBTable != "news_article" || len(operation.After.Fields) != 4 ||
		operation.After.Fields[3].Name != "editor" || !operation.After.Fields[3].Nullable ||
		len(operation.Targets) != 1 || !reflect.DeepEqual(operation.Targets[0].SourceField, operation.After.Fields[3]) ||
		operation.Targets[0].TargetModel.DBTable != "news_author" ||
		len(operation.Targets[0].TargetModel.Fields) != 2 || operation.Targets[0].TargetKey.Column != "id" {
		t.Fatalf("%s loaded nullable Add operation payload = %+v", label, operation)
	}
}

func assertSQLiteLoadedRequiredRelationAddIntent(
	t *testing.T,
	label string,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) {
	t.Helper()
	if transition != (migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "news", Name: "0003_editor"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}) || len(intent.Operations) != 1 {
		t.Fatalf("%s loaded required Add begin payload = transition:%+v intent:%+v", label, transition, intent)
	}
	operation := intent.Operations[0]
	if operation.OperationIndex != 0 || operation.Kind != migrationbackend.MigrationAddField ||
		operation.Before.DBTable != "news_article" || len(operation.Before.Fields) != 3 ||
		operation.After.DBTable != "news_article" || len(operation.After.Fields) != 4 ||
		operation.After.Fields[3].Name != "editor" || operation.After.Fields[3].Nullable ||
		operation.After.Fields[3].Default != nil || operation.After.Fields[3].Relation == nil ||
		operation.After.Fields[3].Relation.OnDelete != ir.DeleteProtect ||
		len(operation.Targets) != 1 || !reflect.DeepEqual(operation.Targets[0].SourceField, operation.After.Fields[3]) ||
		operation.Targets[0].TargetModel.DBTable != "news_author" ||
		len(operation.Targets[0].TargetModel.Fields) != 2 || operation.Targets[0].TargetKey.Column != "id" {
		t.Fatalf("%s loaded required Add operation payload = %+v", label, operation)
	}
}

type sqliteLoadedRelationTaxonomyCase struct {
	name        string
	beginErr    error
	method      string
	contains    string
	cause       error
	category    migrations.ErrorCategory
	code        migrations.ErrorCode
	operation   int
	kind        string
	fenceKind   migrationbackend.RevisionFenceFailureKind
	checkpoints []sqliteRelationBeginCheckpoint
	rollbacks   int
}

func assertSQLiteLoadedRelationErrorTaxonomy(t *testing.T) {
	t.Helper()
	beginCause := errors.New("loaded relation Begin fault")
	pragmaCause := errors.New("loaded relation PRAGMA-set fault")
	catalogCause := errors.New("loaded relation catalog fault")
	claimCause := &codedRevisionSQLiteError{code: 5}
	foreignKeyCause := errors.New("loaded relation final foreign-key-check fault")
	recorderCause := errors.New("loaded relation recorder fault")
	fullCheckpoints := []sqliteRelationBeginCheckpoint{
		sqliteRelationCheckpointForeignKeysSet,
		sqliteRelationCheckpointForeignKeysRead,
		sqliteRelationCheckpointTransactionBegun,
		sqliteRelationCheckpointPhysicalPreflightComplete,
		sqliteRelationCheckpointRevisionClaimStarting,
		sqliteRelationCheckpointRevisionClaimed,
	}
	tests := []sqliteLoadedRelationTaxonomyCase{
		{
			name:      "begin",
			beginErr:  beginCause,
			cause:     beginCause,
			category:  migrations.CategoryTransaction,
			code:      migrations.CodeBeginFailed,
			operation: migrations.NoOperation,
		},
		{
			name:      "pragma_set",
			method:    "exec",
			contains:  "PRAGMA foreign_keys = ON",
			cause:     pragmaCause,
			category:  migrations.CategoryTransaction,
			code:      migrations.CodeBeginFailed,
			operation: migrations.NoOperation,
		},
		{
			name:        "catalog",
			method:      "query",
			contains:    "FROM main.sqlite_schema",
			cause:       catalogCause,
			category:    migrations.CategoryTransaction,
			code:        migrations.CodeBeginFailed,
			operation:   migrations.NoOperation,
			checkpoints: fullCheckpoints[:3],
		},
		{
			name:        "claim_busy",
			method:      "exec",
			contains:    `UPDATE "godj_migration_revision"`,
			cause:       claimCause,
			category:    migrations.CategoryTransaction,
			code:        migrations.CodeHistoryRevisionContended,
			operation:   migrations.NoOperation,
			fenceKind:   migrationbackend.RevisionFenceFailureContended,
			checkpoints: fullCheckpoints[:5],
		},
		{
			name:        "final_foreign_key_check",
			method:      "query",
			contains:    "foreign_key_check",
			cause:       foreignKeyCause,
			category:    migrations.CategoryExecution,
			code:        migrations.CodeOperationFailed,
			operation:   1,
			kind:        "AddField",
			checkpoints: fullCheckpoints,
			rollbacks:   1,
		},
		{
			name:        "recorder",
			method:      "exec",
			contains:    `INSERT INTO "godj_migrations"`,
			cause:       recorderCause,
			category:    migrations.CategoryRecorder,
			code:        migrations.CodeRecordFailed,
			operation:   migrations.NoOperation,
			checkpoints: fullCheckpoints,
			rollbacks:   1,
		},
	}

	for _, test := range tests {
		path := filepath.Join(t.TempDir(), "loaded-relation-taxonomy-"+test.name+".sqlite")
		database := openSQLiteLoadedRelationTaxonomyBackend(t, path)
		seedState := seedSQLiteLoadedRelationTaxonomyAuthor(t, database)
		if err := database.Close(); err != nil {
			t.Fatalf("%s seed Close(): %v", test.name, err)
		}
		before := readSQLiteLoadedRelationTaxonomySnapshot(t, path)
		assertSQLiteLoadedRelationTaxonomySeed(t, test.name, before)

		database = openSQLiteLoadedRelationTaxonomyBackend(t, path)
		var connectionFault *sqliteRelationBeginFaultConnection
		if test.method != "" {
			connectionFault = &sqliteRelationBeginFaultConnection{
				method:    test.method,
				contains:  test.contains,
				remaining: 1,
				faultErr:  test.cause,
			}
		}
		probe := &sqliteLoadedRelationTaxonomyBackend{
			Backend:  database,
			beginErr: test.beginErr,
			fault:    connectionFault,
		}
		loaded := loadSQLiteLoadedRelationTaxonomySet(t, test.name)
		state, err := (migrations.Executor{Backend: probe}).Migrate(
			context.Background(),
			loaded,
			migrations.LatestLifecycleRequest(),
		)

		assertSQLiteLoadedRelationTaxonomyError(t, test, err)
		assertSQLiteLoadedRelationTaxonomyState(t, test.name, state, seedState)
		assertSQLiteLoadedRelationTaxonomyIntent(t, test.name, probe.transition, probe.intent)
		if connectionFault != nil && connectionFault.remaining != 0 {
			t.Fatalf("%s fault remaining = %d, want 0", test.name, connectionFault.remaining)
		}
		if connectionFault != nil {
			wantRollbacks := 1
			if test.name == "pragma_set" {
				wantRollbacks = 0
			}
			if connectionFault.closeCalls != 1 || connectionFault.rawCalls != 0 ||
				connectionFault.rollbackCalls != wantRollbacks {
				t.Fatalf(
					"%s connection cleanup = close:%d raw:%d rollback:%d, want 1/0/%d",
					test.name,
					connectionFault.closeCalls,
					connectionFault.rawCalls,
					connectionFault.rollbackCalls,
					wantRollbacks,
				)
			}
		}
		wantHooks := 1
		if test.beginErr != nil {
			wantHooks = 0
		}
		if probe.capabilityCalls != 1 || probe.openCalls != 1 || probe.readCalls != 1 || probe.beginCalls != 1 ||
			probe.closeCalls != 1 || probe.connectionHookCalls != wantHooks ||
			probe.transactionRollbackCalls != test.rollbacks {
			t.Fatalf(
				"%s lifecycle calls = capability:%d open:%d read:%d begin:%d close:%d hook:%d rollback:%d, want 1/1/1/1/1/%d/%d",
				test.name,
				probe.capabilityCalls,
				probe.openCalls,
				probe.readCalls,
				probe.beginCalls,
				probe.closeCalls,
				probe.connectionHookCalls,
				probe.transactionRollbackCalls,
				wantHooks,
				test.rollbacks,
			)
		}
		if !reflect.DeepEqual(probe.checkpoints, test.checkpoints) {
			t.Fatalf("%s checkpoints = %v, want %v", test.name, probe.checkpoints, test.checkpoints)
		}
		if stats := database.database.Stats(); stats.InUse != 0 {
			t.Fatalf("%s database in-use connections = %d, want 0", test.name, stats.InUse)
		}
		if err := database.Close(); err != nil {
			t.Fatalf("%s fault Close(): %v", test.name, err)
		}
		after := readSQLiteLoadedRelationTaxonomySnapshot(t, path)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("%s changed reopened durable snapshot:\nbefore=%+v\nafter=%+v", test.name, before, after)
		}
	}
}

func assertSQLiteLoadedRelationTaxonomyError(
	t *testing.T,
	test sqliteLoadedRelationTaxonomyCase,
	err error,
) {
	t.Helper()
	var migrationError *migrations.Error
	if !errors.As(err, &migrationError) || migrationError == nil ||
		migrationError.Category != test.category || migrationError.Code != test.code ||
		migrationError.Direction != migrations.DirectionForward || migrationError.App != "blog" ||
		migrationError.Migration != "0001_article" || migrationError.OperationIndex != test.operation ||
		migrationError.Operation != test.kind || migrationError.RollbackCause != nil ||
		!errors.Is(err, test.cause) {
		t.Fatalf(
			"%s taxonomy error = %#v (%v), want %s/%s forward blog.0001_article operation[%d]=%q cause %v",
			test.name,
			migrationError,
			err,
			test.category,
			test.code,
			test.operation,
			test.kind,
			test.cause,
		)
	}
	var fenceError *migrationbackend.RevisionFenceError
	if test.fenceKind == 0 {
		if errors.As(migrationError.Cause, &fenceError) {
			t.Fatalf("%s raw fault was reclassified as revision fence error: %#v", test.name, fenceError)
		}
	} else if !errors.As(migrationError.Cause, &fenceError) || fenceError == nil || fenceError.Kind != test.fenceKind {
		t.Fatalf("%s revision fence error = %#v, want kind %d", test.name, fenceError, test.fenceKind)
	}
}

func assertSQLiteLoadedRelationTaxonomyState(
	t *testing.T,
	label string,
	state migrations.ProjectState,
	seed migrations.ProjectState,
) {
	t.Helper()
	if !state.Equal(seed) {
		t.Fatalf(
			"%s rollback state = format:%d apps:%v, want exact seed format:%d apps:%v",
			label,
			state.FormatVersion(),
			state.Apps(),
			seed.FormatVersion(),
			seed.Apps(),
		)
	}
}

func assertSQLiteLoadedRelationTaxonomyIntent(
	t *testing.T,
	label string,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) {
	t.Helper()
	if transition != (migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "blog", Name: "0001_article"},
		Kind:      migrationbackend.HistoryTransitionApply,
	}) || len(intent.Operations) != 2 {
		t.Fatalf("%s relation begin payload = transition:%+v intent:%+v", label, transition, intent)
	}
	create := intent.Operations[0]
	add := intent.Operations[1]
	if create.OperationIndex != 0 || create.Kind != migrationbackend.MigrationCreateModel ||
		create.After.DBTable != "blog_article" || len(create.Targets) != 1 ||
		create.Targets[0].TargetModel.DBTable != "authors_author" ||
		create.Targets[0].SourceField.Column != "author_id" ||
		add.OperationIndex != 1 || add.Kind != migrationbackend.MigrationAddField ||
		len(add.Targets) != 1 || add.Targets[0].SourceField.Name != "author" || len(add.After.Fields) != 3 ||
		add.After.Fields[2].Name != "summary" || !add.After.Fields[2].Nullable {
		t.Fatalf("%s relation operation payload = create:%+v add:%+v", label, create, add)
	}
}

func openSQLiteLoadedRelationTaxonomyBackend(t *testing.T, path string) *Backend {
	t.Helper()
	database, err := Open(context.Background(), "file:"+filepath.ToSlash(path)+"?mode=rwc")
	if err != nil {
		t.Fatalf("Open(file-backed loaded relation taxonomy): %v", err)
	}
	return database
}

func seedSQLiteLoadedRelationTaxonomyAuthor(t *testing.T, database *Backend) migrations.ProjectState {
	t.Helper()
	ctx := context.Background()
	loaded, report, err := migrationdefinition.Load(migrationdefinition.Source{
		SourceID: "loaded-taxonomy-authors",
		Document: sqliteLoadedRelationTaxonomyAuthorDocument(),
	})
	if err != nil {
		t.Fatalf("Load(loaded taxonomy author): %v", err)
	}
	if report.DocumentsReceived != 1 || report.HeadersValidated != 1 || report.OperationsDecoded != 1 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("Load(loaded taxonomy author) report = %+v", report)
	}
	state, err := (migrations.Executor{Backend: database}).Migrate(
		ctx,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{
			App: "authors", Name: "0001_author",
		})),
	)

	if err != nil {
		t.Fatalf("Migrate(loaded taxonomy author): %v", err)
	}
	if state.FormatVersion() != migrations.StateFormatVersion || !reflect.DeepEqual(state.Apps(), []string{"authors"}) {
		t.Fatalf("loaded taxonomy seed state = format:%d apps:%v", state.FormatVersion(), state.Apps())
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO "authors_author" ("id") VALUES (41)`); err != nil {
		t.Fatalf("insert loaded taxonomy author: %v", err)
	}
	return state.Clone()
}

func loadSQLiteLoadedRelationTaxonomySet(t *testing.T, label string) migrations.LoadedDefinitionSet {
	t.Helper()
	loaded, report, err := migrationdefinition.Load(
		migrationdefinition.Source{
			SourceID: "loaded-taxonomy-blog-" + label,
			Document: sqliteLoadedRelationTaxonomyBlogDocument(),
		},
		migrationdefinition.Source{
			SourceID: "loaded-taxonomy-authors-" + label,
			Document: sqliteLoadedRelationTaxonomyAuthorDocument(),
		},
	)
	if err != nil {
		t.Fatalf("%s Load(loaded relation taxonomy): %v", label, err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 3 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("%s Load(loaded relation taxonomy) report = %+v", label, report)
	}
	return loaded
}

func sqliteLoadedRelationTaxonomyAuthorDocument() []byte {
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"loaded-taxonomy","version":"1"},` +
		`"migration":{"app":"authors","name":"0001_author","dependencies":[],"operations":[` +
		`{"kind":"create_model","app_label":"authors","model":{` +
		`"name":"author","go_name":"Author","db_table":"authors_author","fields":[` +
		`{"name":"id","go_name":"ID","column":"id","kind":"auto",` +
		`"primary_key":true,"nullable":false,"max_length":0,"default":null}]}}]}}`)
}

func sqliteLoadedRelationTaxonomyBlogDocument() []byte {
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"loaded-taxonomy","version":"1"},` +
		`"migration":{"app":"blog","name":"0001_article",` +
		`"dependencies":[{"app":"authors","name":"0001_author"}],"operations":[` +
		`{"kind":"create_model","app_label":"blog","model":{` +
		`"name":"article","go_name":"Article","db_table":"blog_article","fields":[` +
		`{"name":"id","go_name":"ID","column":"id","kind":"auto",` +
		`"primary_key":true,"nullable":false,"max_length":0,"default":null},` +
		`{"name":"author","go_name":"Author","column":"author_id","kind":"foreign_key",` +
		`"primary_key":false,"nullable":false,"max_length":0,"default":null,` +
		`"relation":{"target":{"app_label":"authors","model_name":"author"},` +
		`"cardinality":"many_to_one","reverse":{"name":"articles","disabled":false},` +
		`"on_delete":"protect"}}]}},` +
		`{"kind":"add_field","app_label":"blog","model_name":"article","field":{` +
		`"name":"summary","go_name":"Summary","column":"summary","kind":"char",` +
		`"primary_key":false,"nullable":true,"max_length":64,"default":null}}]}}`)
}

const (
	sqliteLoadedRelationTaxonomyEpochBytes       = 16
	sqliteLoadedRelationTaxonomyFingerprintBytes = 32
)

type sqliteLoadedRelationTaxonomySnapshot struct {
	FormatVersion int64
	Epoch         [sqliteLoadedRelationTaxonomyEpochBytes]byte
	Revision      int64
	Fingerprint   [sqliteLoadedRelationTaxonomyFingerprintBytes]byte
	History       []migrationbackend.AppliedMigration
	Schema        []sqliteLoadedRelationTaxonomySchemaObject
	AuthorIDs     []int64
	ForeignKeys   []sqliteLoadedRelationTaxonomyForeignKey
}

type sqliteLoadedRelationTaxonomySchemaObject struct {
	Type       string
	Name       string
	Table      string
	Definition string
}

type sqliteLoadedRelationTaxonomyForeignKey struct {
	SourceTable string
	ID          int64
	Sequence    int64
	TargetTable string
	FromColumn  string
	ToColumn    string
	OnUpdate    string
	OnDelete    string
	Match       string
}

func readSQLiteLoadedRelationTaxonomySnapshot(t *testing.T, path string) sqliteLoadedRelationTaxonomySnapshot {
	t.Helper()
	ctx := context.Background()
	reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatalf("open read-only loaded relation taxonomy snapshot: %v", err)
	}
	reader.SetMaxOpenConns(1)
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close read-only loaded relation taxonomy snapshot: %v", err)
		}
	}()
	tx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read-only loaded relation taxonomy snapshot: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	snapshot := sqliteLoadedRelationTaxonomySnapshot{
		History:     make([]migrationbackend.AppliedMigration, 0),
		Schema:      make([]sqliteLoadedRelationTaxonomySchemaObject, 0),
		AuthorIDs:   make([]int64, 0),
		ForeignKeys: make([]sqliteLoadedRelationTaxonomyForeignKey, 0),
	}
	var epoch, fingerprint []byte
	if err := tx.QueryRowContext(
		ctx,
		`SELECT "format_version", "epoch", "revision", "history_fingerprint" `+
			`FROM "godj_migration_revision" WHERE "singleton" = 1`,
	).Scan(&snapshot.FormatVersion, &epoch, &snapshot.Revision, &fingerprint); err != nil {
		t.Fatalf("read loaded relation taxonomy revision token: %v", err)
	}
	if len(epoch) != len(snapshot.Epoch) || len(fingerprint) != len(snapshot.Fingerprint) {
		t.Fatalf("loaded relation taxonomy token bytes = epoch:%d fingerprint:%d", len(epoch), len(fingerprint))
	}
	copy(snapshot.Epoch[:], epoch)
	copy(snapshot.Fingerprint[:], fingerprint)

	historyRows, err := tx.QueryContext(ctx, `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
	if err != nil {
		t.Fatalf("read loaded relation taxonomy history: %v", err)
	}
	for historyRows.Next() {
		var record migrationbackend.AppliedMigration
		if err := historyRows.Scan(&record.App, &record.Name); err != nil {
			_ = historyRows.Close()
			t.Fatalf("scan loaded relation taxonomy history: %v", err)
		}
		snapshot.History = append(snapshot.History, record)
	}
	if err := historyRows.Err(); err != nil {
		_ = historyRows.Close()
		t.Fatalf("iterate loaded relation taxonomy history: %v", err)
	}
	if err := historyRows.Close(); err != nil {
		t.Fatalf("close loaded relation taxonomy history: %v", err)
	}

	schemaRows, err := tx.QueryContext(
		ctx,
		`SELECT "type", "name", "tbl_name", COALESCE("sql", '') FROM main.sqlite_schema `+
			`WHERE "name" NOT LIKE 'sqlite_%' ORDER BY "type", "name", "tbl_name", "sql"`,
	)
	if err != nil {
		t.Fatalf("read loaded relation taxonomy schema: %v", err)
	}
	for schemaRows.Next() {
		var object sqliteLoadedRelationTaxonomySchemaObject
		if err := schemaRows.Scan(&object.Type, &object.Name, &object.Table, &object.Definition); err != nil {
			_ = schemaRows.Close()
			t.Fatalf("scan loaded relation taxonomy schema: %v", err)
		}
		snapshot.Schema = append(snapshot.Schema, object)
	}
	if err := schemaRows.Err(); err != nil {
		_ = schemaRows.Close()
		t.Fatalf("iterate loaded relation taxonomy schema: %v", err)
	}
	if err := schemaRows.Close(); err != nil {
		t.Fatalf("close loaded relation taxonomy schema: %v", err)
	}

	authorRows, err := tx.QueryContext(ctx, `SELECT "id" FROM "authors_author" ORDER BY "id"`)
	if err != nil {
		t.Fatalf("read loaded relation taxonomy author rows: %v", err)
	}
	for authorRows.Next() {
		var id int64
		if err := authorRows.Scan(&id); err != nil {
			_ = authorRows.Close()
			t.Fatalf("scan loaded relation taxonomy author rows: %v", err)
		}
		snapshot.AuthorIDs = append(snapshot.AuthorIDs, id)
	}
	if err := authorRows.Err(); err != nil {
		_ = authorRows.Close()
		t.Fatalf("iterate loaded relation taxonomy author rows: %v", err)
	}
	if err := authorRows.Close(); err != nil {
		t.Fatalf("close loaded relation taxonomy author rows: %v", err)
	}

	for _, table := range []string{"authors_author", "blog_article"} {
		rows, err := tx.QueryContext(ctx, `PRAGMA main.foreign_key_list("`+table+`")`)
		if err != nil {
			t.Fatalf("read loaded relation taxonomy foreign keys for %s: %v", table, err)
		}
		for rows.Next() {
			foreignKey := sqliteLoadedRelationTaxonomyForeignKey{SourceTable: table}
			if err := rows.Scan(
				&foreignKey.ID,
				&foreignKey.Sequence,
				&foreignKey.TargetTable,
				&foreignKey.FromColumn,
				&foreignKey.ToColumn,
				&foreignKey.OnUpdate,
				&foreignKey.OnDelete,
				&foreignKey.Match,
			); err != nil {
				_ = rows.Close()
				t.Fatalf("scan loaded relation taxonomy foreign keys for %s: %v", table, err)
			}
			snapshot.ForeignKeys = append(snapshot.ForeignKeys, foreignKey)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatalf("iterate loaded relation taxonomy foreign keys for %s: %v", table, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close loaded relation taxonomy foreign keys for %s: %v", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit read-only loaded relation taxonomy snapshot: %v", err)
	}
	committed = true
	return snapshot
}

func assertSQLiteLoadedRelationTaxonomySeed(t *testing.T, label string, snapshot sqliteLoadedRelationTaxonomySnapshot) {
	t.Helper()
	wantHistory := []migrationbackend.AppliedMigration{{App: "authors", Name: "0001_author"}}
	if snapshot.FormatVersion != migrationRevisionFormatVersion || snapshot.Revision != 1 ||
		snapshot.Epoch == ([sqliteLoadedRelationTaxonomyEpochBytes]byte{}) ||
		snapshot.Fingerprint != fingerprintMigrationHistory(snapshot.History) ||
		!reflect.DeepEqual(snapshot.History, wantHistory) || !reflect.DeepEqual(snapshot.AuthorIDs, []int64{41}) ||
		len(snapshot.ForeignKeys) != 0 {
		t.Fatalf("%s loaded relation taxonomy seed snapshot = %+v", label, snapshot)
	}
	for _, object := range snapshot.Schema {
		if object.Name == "blog_article" {
			t.Fatalf("%s seed snapshot already contains blog_article: %+v", label, object)
		}
	}
}

type sqliteLoadedRelationTaxonomyBackend struct {
	*Backend
	beginErr                 error
	fault                    *sqliteRelationBeginFaultConnection
	transition               migrationbackend.HistoryTransition
	intent                   migrationbackend.MigrationIntent
	checkpoints              []sqliteRelationBeginCheckpoint
	capabilityCalls          int
	openCalls                int
	readCalls                int
	beginCalls               int
	closeCalls               int
	connectionHookCalls      int
	transactionRollbackCalls int
}

type sqliteLoadedRelationTaxonomySession struct {
	migrationbackend.RevisionFencedSession
	owner *sqliteLoadedRelationTaxonomyBackend
}

type sqliteLoadedRelationTaxonomyTransaction struct {
	migrationbackend.RevisionFencedTransaction
	owner *sqliteLoadedRelationTaxonomyBackend
}

var _ migrationbackend.AtomicBackend = (*sqliteLoadedRelationTaxonomyBackend)(nil)
var _ migrationbackend.RevisionFencedBackend = (*sqliteLoadedRelationTaxonomyBackend)(nil)
var _ migrationbackend.RevisionFencedSession = (*sqliteLoadedRelationTaxonomySession)(nil)
var _ migrationbackend.RevisionFencedTransaction = (*sqliteLoadedRelationTaxonomyTransaction)(nil)

func (backend *sqliteLoadedRelationTaxonomyBackend) MigrationCapabilities() migrationbackend.MigrationCapabilities {
	backend.capabilityCalls++
	return backend.Backend.MigrationCapabilities()
}

func (backend *sqliteLoadedRelationTaxonomyBackend) OpenRevisionFencedSession(
	ctx context.Context,
) (migrationbackend.RevisionFencedSession, error) {
	backend.openCalls++
	raw, err := backend.Backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		return nil, err
	}
	concrete, ok := raw.(*sqliteRevisionFencedSession)
	if !ok {
		_ = raw.Close(context.Background())
		return nil, fmt.Errorf("loaded relation taxonomy SQLite session has type %T", raw)
	}
	concrete.relationBeginCheckpoint = func(checkpoint sqliteRelationBeginCheckpoint) {
		backend.checkpoints = append(backend.checkpoints, checkpoint)
	}
	if backend.fault != nil {
		concrete.relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
			backend.connectionHookCalls++
			backend.fault.migrationPinnedConnection = connection
			return backend.fault
		}
	}
	return &sqliteLoadedRelationTaxonomySession{RevisionFencedSession: raw, owner: backend}, nil
}

func (session *sqliteLoadedRelationTaxonomySession) ReadAppliedMigrations(
	ctx context.Context,
) ([]migrationbackend.AppliedMigration, error) {
	session.owner.readCalls++
	return session.RevisionFencedSession.ReadAppliedMigrations(ctx)
}

func (session *sqliteLoadedRelationTaxonomySession) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.owner.beginCalls++
	session.owner.transition = transition
	session.owner.intent = cloneSQLiteRelationIntent(intent)
	if session.owner.beginErr != nil {
		return nil, session.owner.beginErr
	}
	transaction, err := session.RevisionFencedSession.BeginMigration(ctx, transition, intent)
	if err != nil || transaction == nil {
		return transaction, err
	}
	return &sqliteLoadedRelationTaxonomyTransaction{
		RevisionFencedTransaction: transaction,
		owner:                     session.owner,
	}, nil
}

func (session *sqliteLoadedRelationTaxonomySession) Close(ctx context.Context) error {
	session.owner.closeCalls++
	return session.RevisionFencedSession.Close(ctx)
}

func (transaction *sqliteLoadedRelationTaxonomyTransaction) Rollback(ctx context.Context) error {
	transaction.owner.transactionRollbackCalls++
	return transaction.RevisionFencedTransaction.Rollback(ctx)
}

func openSQLiteRelationSession(t *testing.T, backend *Backend) migrationbackend.RevisionFencedSession {
	t.Helper()
	raw, err := backend.OpenRevisionFencedSession(context.Background())
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(): %v", err)
	}
	return raw
}

func sqliteRelationTestTableExists(t *testing.T, backend *Backend, table string) bool {
	t.Helper()
	var count int
	if err := backend.database.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM main.sqlite_schema WHERE "type" = 'table' AND "name" = ?`,
		table,
	).Scan(&count); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("inspect table %q: %v", table, err)
	}
	return count == 1
}

func sqliteRelationTestColumnExists(t *testing.T, backend *Backend, table, column string) bool {
	t.Helper()
	quotedTable, err := quoteIdentifier(table)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := backend.database.QueryContext(context.Background(), `PRAGMA main.table_xinfo(`+quotedTable+`)`)
	if err != nil {
		t.Fatalf("inspect columns for %q: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, fieldType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &fieldType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			t.Fatalf("scan columns for %q: %v", table, err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns for %q: %v", table, err)
	}
	return false
}

func assertSQLiteRelationCapabilityFeature(t *testing.T, err error, feature string) {
	t.Helper()
	var capability *migrationbackend.CapabilityError
	if !errors.As(err, &capability) || capability.Feature != feature {
		t.Fatalf("capability error = %#v (%v), want feature %q", capability, err, feature)
	}
}

func assertSQLiteRelationNoClaimCheckpoint(t *testing.T, checkpoints []sqliteRelationBeginCheckpoint) {
	t.Helper()
	for _, checkpoint := range checkpoints {
		if checkpoint == sqliteRelationCheckpointRevisionClaimStarting || checkpoint == sqliteRelationCheckpointRevisionClaimed {
			t.Fatalf("preflight reached revision claim: checkpoints=%v", checkpoints)
		}
	}
}

type countingRelationSQLExecutor struct {
	migrationSQLExecutor
	queryCalls int
}

func (executor *countingRelationSQLExecutor) QueryContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) (*sql.Rows, error) {
	executor.queryCalls++
	return executor.migrationSQLExecutor.QueryContext(ctx, statement, arguments...)
}

type sqliteNullableRelationAddFaultConnection struct {
	migrationPinnedConnection
	method               string
	contains             string
	faultErr             error
	remaining            int
	corruptCanonical     bool
	alterCalls           int
	foreignKeyCheckCalls int
	recorderCalls        int
	canonicalCorruptions int
	rollbackCalls        int
}

func (connection *sqliteNullableRelationAddFaultConnection) ExecContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) (sql.Result, error) {
	if statement == "ROLLBACK" {
		connection.rollbackCalls++
	}
	isAlter := strings.Contains(statement, `ALTER TABLE "main"."news_article"`)
	if isAlter {
		connection.alterCalls++
	}
	if strings.Contains(statement, `INSERT INTO "godj_migrations"`) {
		connection.recorderCalls++
	}
	if err := connection.inject("exec", statement); err != nil {
		return nil, err
	}
	result, err := connection.migrationPinnedConnection.ExecContext(ctx, statement, arguments...)
	if err != nil || !isAlter || !connection.corruptCanonical {
		return result, err
	}
	if _, err := connection.migrationPinnedConnection.ExecContext(ctx, `PRAGMA writable_schema = ON`); err != nil {
		return result, fmt.Errorf("enable writable_schema for canonical fault: %w", err)
	}
	update, updateErr := connection.migrationPinnedConnection.ExecContext(
		ctx,
		`UPDATE main.sqlite_schema SET "sql" = "sql" || ' ' WHERE "type" = 'table' AND "name" = 'news_article'`,
	)
	_, disableErr := connection.migrationPinnedConnection.ExecContext(ctx, `PRAGMA writable_schema = OFF`)
	if updateErr != nil || disableErr != nil {
		return result, errors.Join(
			fmt.Errorf("corrupt nullable Add canonical SQL: %w", updateErr),
			fmt.Errorf("disable writable_schema after canonical fault: %w", disableErr),
		)
	}
	rows, err := update.RowsAffected()
	if err != nil {
		return result, fmt.Errorf("count nullable Add canonical SQL corruption: %w", err)
	}
	if rows != 1 {
		return result, fmt.Errorf("corrupt nullable Add canonical SQL changed %d rows, want 1", rows)
	}
	connection.canonicalCorruptions++
	return result, nil
}

func (connection *sqliteNullableRelationAddFaultConnection) QueryContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) (*sql.Rows, error) {
	if strings.Contains(statement, "foreign_key_check") {
		connection.foreignKeyCheckCalls++
	}
	if err := connection.inject("query", statement); err != nil {
		return nil, err
	}
	return connection.migrationPinnedConnection.QueryContext(ctx, statement, arguments...)
}

func (connection *sqliteNullableRelationAddFaultConnection) inject(method, statement string) error {
	if connection.remaining == 0 || connection.method != method ||
		!strings.Contains(statement, connection.contains) {
		return nil
	}
	connection.remaining--
	return connection.faultErr
}

func (connection *sqliteNullableRelationAddFaultConnection) operationCounts() [4]int {
	return [4]int{
		connection.alterCalls,
		connection.foreignKeyCheckCalls,
		connection.recorderCalls,
		connection.canonicalCorruptions,
	}
}

type sqliteRelationBeginFaultConnection struct {
	migrationPinnedConnection
	method        string
	contains      string
	remaining     int
	closeCalls    int
	rawCalls      int
	rollbackCalls int
	faultErr      error
	rollbackErr   error
	closeErr      error
}

func (connection *sqliteRelationBeginFaultConnection) inject(method, statement string) error {
	if connection.remaining == 0 || connection.method != method || !strings.Contains(statement, connection.contains) {
		return nil
	}
	connection.remaining--
	if connection.faultErr != nil {
		return connection.faultErr
	}
	return errors.New("injected relation Begin fault")
}

func (connection *sqliteRelationBeginFaultConnection) ExecContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) (sql.Result, error) {
	if statement == "ROLLBACK" {
		connection.rollbackCalls++
		if connection.rollbackErr != nil {
			return nil, connection.rollbackErr
		}
	}
	if err := connection.inject("exec", statement); err != nil {
		return nil, err
	}
	return connection.migrationPinnedConnection.ExecContext(ctx, statement, arguments...)
}

func (connection *sqliteRelationBeginFaultConnection) QueryContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) (*sql.Rows, error) {
	if err := connection.inject("query", statement); err != nil {
		return nil, err
	}
	return connection.migrationPinnedConnection.QueryContext(ctx, statement, arguments...)
}

func (connection *sqliteRelationBeginFaultConnection) QueryRowContext(
	ctx context.Context,
	statement string,
	arguments ...any,
) *sql.Row {
	if err := connection.inject("query_row", statement); err != nil {
		return connection.migrationPinnedConnection.QueryRowContext(ctx, `SELECT * FROM "__godj_relation_begin_fault__"`)
	}
	return connection.migrationPinnedConnection.QueryRowContext(ctx, statement, arguments...)
}

func (connection *sqliteRelationBeginFaultConnection) Close() error {
	connection.closeCalls++
	if connection.closeErr != nil {
		return connection.closeErr
	}
	return connection.migrationPinnedConnection.Close()
}

func (connection *sqliteRelationBeginFaultConnection) Raw(callback func(any) error) error {
	connection.rawCalls++
	return connection.migrationPinnedConnection.Raw(callback)
}
