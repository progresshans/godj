package domain_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/account/domain/modeldef"
	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

func TestAccountDomainDefinitionAndGeneratedBinding(t *testing.T) {
	selected, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	sources := domain.MigrationSources()
	loaded, _, err := definition.Load(sources...)
	if err != nil || len(loaded.Definitions()) != 1 {
		t.Fatal("domain migration graph", err)
	}
	initial := loaded.Definitions()[0]
	if initial.App != modeldef.AppLabel || initial.Name != domain.InitialMigrationKey().Name || len(initial.Dependencies) != 0 || len(initial.Operations) != 1 {
		t.Fatal("domain depends on an account implementation")
	}
	create, ok := initial.Operations[0].(migrations.CreateModel)
	if !ok || !reflect.DeepEqual(create.Model, selected.Models[0]) {
		t.Fatal("history does not implement the declared domain model")
	}
	sources[0].Document[0] = '!'
	if domain.MigrationSources()[0].Document[0] != '{' {
		t.Fatal("historical migration is mutable")
	}
	spec, err := modeldef.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if spec.Account != nil || len(spec.Apps) != 1 {
		t.Fatal("domain selected a hidden account")
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	report, err := projectgenerate.Check(t.Context(), ".", bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("domain generated drift: %+v, %v", report, err)
	}
}

func TestAccountDomainPreparationRejectsInvalidInputs(t *testing.T) {
	valid := strings.Repeat("a", 64)
	for _, bad := range []string{"", strings.Repeat("0", 64), strings.Repeat("A", 64), strings.Repeat("z", 64), valid[:63], valid + "a"} {
		if got, err := domain.Prepare(t.Context(), bad, valid); !errors.Is(err, domain.ErrInvalid) || got.OperationID() != "" {
			t.Fatal("bad role accepted", err)
		}
		if got, err := domain.Prepare(t.Context(), valid, bad); !errors.Is(err, domain.ErrInvalid) || got.OperationID() != "" {
			t.Fatal("bad evidence accepted", err)
		}
	}
	if _, err := domain.Prepare(nil, valid, valid); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("nil context accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := domain.Prepare(ctx, valid, valid); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled preparation accepted", err)
	}
	first, err := domain.Prepare(t.Context(), valid, valid)
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.Prepare(t.Context(), valid, valid)
	if err != nil || first.OperationID() == second.OperationID() || len(first.OperationID()) != 64 {
		t.Fatal("fresh preparation reused operation identity", err)
	}
	if got, err := domain.Read(t.Context(), nil, valid); !errors.Is(err, domain.ErrInvalid) || got.Sequence() != 0 {
		t.Fatal("nil snapshot backend accepted", err)
	}
	if got, err := domain.Lookup(t.Context(), nil, valid); !errors.Is(err, domain.ErrInvalid) || got.Sequence() != 0 {
		t.Fatal("nil lookup backend accepted", err)
	}
}
