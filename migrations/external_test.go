package migrations_test

import (
	"context"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestExternalConsumerCanConstructBuiltInMigration(t *testing.T) {
	t.Parallel()

	model := ir.Model{
		Name:    "article",
		GoName:  "Article",
		DBTable: "news_article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
		},
	}
	migration := migrations.Migration{
		App:  "news",
		Name: "0001_article",
		Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "news", Model: model},
			migrations.AddField{
				AppLabel:  "news",
				ModelName: "article",
				Field: ir.Field{
					Name:      "summary",
					GoName:    "Summary",
					Column:    "summary",
					Kind:      ir.FieldChar,
					Nullable:  true,
					MaxLength: 200,
				},
			},
		},
	}
	if len(migration.Operations) != 2 {
		t.Fatalf("operations = %d, want 2", len(migration.Operations))
	}
	external := &externalBackend{transaction: &externalTransaction{}}
	state, err := (migrations.Executor{Backend: external}).Apply(
		context.Background(),
		migrations.EmptyProjectState(),
		migration,
	)
	if err != nil {
		t.Fatalf("external Executor.Apply() error = %v", err)
	}
	if _, exists := state.Model("news", "article"); !exists {
		t.Fatal("external Executor.Apply() did not return the applied model state")
	}
}

type externalBackend struct {
	transaction *externalTransaction
}

func (b *externalBackend) BeginMigration(context.Context) (backend.Transaction, error) {
	return b.transaction, nil
}

type externalTransaction struct{}

func (*externalTransaction) CreateModel(context.Context, ir.Model) error           { return nil }
func (*externalTransaction) DeleteModel(context.Context, ir.Model) error           { return nil }
func (*externalTransaction) AddField(context.Context, ir.Model, ir.Field) error    { return nil }
func (*externalTransaction) RemoveField(context.Context, ir.Model, ir.Field) error { return nil }
func (*externalTransaction) RecordApplied(context.Context, string, string) error   { return nil }
func (*externalTransaction) RecordUnapplied(context.Context, string, string) error { return nil }
func (*externalTransaction) Commit(context.Context) error                          { return nil }
func (*externalTransaction) Rollback(context.Context) error                        { return nil }
