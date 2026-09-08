package postgres

import (
	"context"
	"fmt"
	"testing"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

func TestPostgresOperationSealBindsEveryExecutionAuthorityBeforeSQL(t *testing.T) {
	model := postgresMigrationTestPostModel(true)
	intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{
		OperationIndex: 0, Kind: migrationbackend.MigrationCreateModel, After: model,
		Targets: []migrationbackend.MigrationTarget{postgresMigrationTestTarget(model.Fields[3], postgresMigrationTestAuthorModel())},
	}}}
	for _, test := range []struct {
		name   string
		mutate func(*postgresMigrationSchema)
	}{
		{"transition", func(s *postgresMigrationSchema) { s.transition.Migration.Name = "different" }},
		{"before", func(s *postgresMigrationSchema) { s.intent.Operations[0].Before.DBTable = "forged" }},
		{"after", func(s *postgresMigrationSchema) { s.intent.Operations[0].After.Fields[1].Column = "forged" }},
		{"source field", func(s *postgresMigrationSchema) { s.intent.Operations[0].Targets[0].SourceField.Column = "forged" }},
		{"target model", func(s *postgresMigrationSchema) { s.intent.Operations[0].Targets[0].TargetModel.DBTable = "forged" }},
		{"target key", func(s *postgresMigrationSchema) { s.intent.Operations[0].Targets[0].TargetKey.Column = "forged" }},
		{"operation index", func(s *postgresMigrationSchema) { s.intent.Operations[0].OperationIndex++ }},
		{"operation kind", func(s *postgresMigrationSchema) { s.intent.Operations[0].Kind = migrationbackend.MigrationDeleteModel }},
		{"operation inventory", func(s *postgresMigrationSchema) {
			s.intent.Operations = append(s.intent.Operations, s.intent.Operations[0])
		}},
		{"negative cursor", func(s *postgresMigrationSchema) { s.cursor = -1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema, err := newPostgresMigrationSchema(postgresMigrationTestTransition(), intent)
			if err != nil {
				t.Fatal(err)
			}
			schema.preflight, schema.namespace = true, "product_schema"
			test.mutate(schema)
			executor := &postgresMigrationRecordingExecutor{}
			assertPostgresMigrationIntegrity(t, schema.CreateModel(context.Background(), executor, model))
			if executor.execCalls != 0 {
				t.Fatal("changed operation authority reached SQL")
			}
		})
	}
}

func TestPostgresIntentSealRejectsFutureUseAndPastMutationBeforeRecorder(t *testing.T) {
	ctx := context.Background()
	intent := postgresSealTestIntent(2)
	t.Run("future operation", func(t *testing.T) {
		schema, err := newPostgresMigrationSchema(postgresMigrationTestTransition(), intent)
		if err != nil {
			t.Fatal(err)
		}
		schema.preflight, schema.namespace = true, "product_schema"
		// Only this package's test can alter the private snapshot. A future
		// operation is checked before its use, while the current operation
		// remains bound to its independent seal.
		schema.intent.Operations[1].After.Fields[1].Column = "forged"
		executor := &postgresMigrationRecordingExecutor{}
		if err := schema.CreateModel(ctx, executor, intent.Operations[0].After); err != nil {
			t.Fatal(err)
		}
		assertPostgresMigrationIntegrity(t, schema.CreateModel(ctx, executor, intent.Operations[1].After))
		if executor.execCalls != 1 || schema.cursor != 1 {
			t.Fatal("changed future operation reached SQL")
		}
		assertPostgresMigrationIntegrity(t, schema.VerifyComplete(ctx, executor))
	})
	t.Run("already consumed operation", func(t *testing.T) {
		schema, err := newPostgresMigrationSchema(postgresMigrationTestTransition(), intent)
		if err != nil {
			t.Fatal(err)
		}
		schema.preflight, schema.namespace = true, "product_schema"
		executor := &postgresMigrationRecordingExecutor{}
		for _, operation := range intent.Operations {
			if err := schema.CreateModel(ctx, executor, operation.After); err != nil {
				t.Fatal(err)
			}
		}
		schema.intent.Operations[0].After.Fields[1].Column = "forged"
		// Recording calls VerifyComplete first. The full seal fails before
		// even a final catalog query, so no success can be recorded.
		assertPostgresMigrationIntegrity(t, schema.VerifyComplete(ctx, executor))
		if schema.verified {
			t.Fatal("changed consumed operation was marked complete")
		}
	})
}

func BenchmarkPostgresMigrationSealValidation(b *testing.B) {
	for _, count := range []int{1, 64, 1024} {
		intent := postgresSealTestIntent(count)
		schema, err := newPostgresMigrationSchema(postgresMigrationTestTransition(), intent)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("operations_%d/current_operation", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := schema.verifyOperationSeal(schema.intent.Operations[0]); err != nil {
					b.Fatal(err)
				}
			}
		})
		// The full check remains necessary at the preflight/final boundaries;
		// this reference makes its cost per operation directly comparable.
		b.Run(fmt.Sprintf("operations_%d/whole_intent", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := schema.verifySeal(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func postgresSealTestIntent(count int) migrationbackend.MigrationIntent {
	intent := migrationbackend.MigrationIntent{Operations: make([]migrationbackend.MigrationOperation, count)}
	for index := range intent.Operations {
		model := postgresMigrationTestPostModel(false)
		model.Name = fmt.Sprintf("entry_%04d", index)
		model.GoName = fmt.Sprintf("Entry%d", index)
		model.DBTable = "blog_" + model.Name
		intent.Operations[index] = migrationbackend.MigrationOperation{
			OperationIndex: index, Kind: migrationbackend.MigrationCreateModel, After: model,
			Targets: []migrationbackend.MigrationTarget{},
		}
	}
	return intent
}
