package choicesproduct_test

import (
	"context"
	"testing"

	"github.com/progresshans/godj/conformance/choicesproduct"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
)

func TestChoicesSQLRendersZeroStatementsAndMixedStepOnlyPhysicalSQL(t *testing.T) {
	sources, err := choicesproduct.Sources()
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	for name, renderer := range map[string]backend.MigrationSQLRenderer{"sqlite": sqlite.NewMigrationSQLRenderer(), "postgres": postgres.NewMigrationSQLRenderer(postgres.MigrationSQLConfig{Schema: "public"})} {
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				migration  string
				statements int
			}{{"0001_initial", 2}, {"0002_choices", 0}, {"0003_labels", 0}, {"0004_mixed", 1}} {
				sql, err := migrations.RenderMigrationSQL(context.Background(), loaded, migrations.MigrationKey{App: "choices", Name: test.migration}, renderer)
				if err != nil || len(sql) != test.statements {
					t.Fatalf("%s: %v %v", test.migration, sql, err)
				}
			}
			if _, err := migrations.RenderMigrationSQL(context.Background(), loaded, migrations.MigrationKey{App: "choices", Name: "0002_choices"}, forgedChoiceTarget{renderer}); err == nil {
				t.Fatal("metadata-only target forgery retained SQL-rendering authority")
			}
		})
	}
}

type forgedChoiceTarget struct{ backend.MigrationSQLRenderer }

func (renderer forgedChoiceTarget) RenderForwardMigrationSQL(ctx context.Context, request backend.ForwardMigrationSQLRequest) ([]string, error) {
	request.Intent.Operations[1].Targets[0].TargetModel.Fields[1].Choices[0].Label = "Forged label"
	return renderer.MigrationSQLRenderer.RenderForwardMigrationSQL(ctx, request)
}
