package sqlite

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/uniquetest"
	mb "github.com/progresshans/godj/migrations/backend"
)

func TestSQLiteNamedConstraintSealOwnsNamesAndEveryOrderedMember(t *testing.T) {
	model, _, _ := uniquetest.CompositeModel(t, "char", true)
	intent := mb.MigrationIntent{Operations: []mb.MigrationOperation{{Kind: mb.MigrationCreateModel, After: model}}}
	transition := mb.HistoryTransition{Kind: mb.HistoryTransitionApply, Migration: mb.AppliedMigration{App: "composite", Name: "0001_initial"}}
	seal, err := validateAndSealSQLiteRelationIntent(transition, intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.Operations[0].After.UniqueConstraints[1].Fields[1] = "id"
	if seal.intent.Operations[0].After.UniqueConstraints[1].Fields[1] != "value" {
		t.Fatal("intent retained caller's member slice")
	}
	for _, mode := range []string{"name", "later_member", "order"} {
		changed := seal.intent.Clone()
		constraint := &changed.Operations[0].After.UniqueConstraints[1]
		switch mode {
		case "name":
			constraint.Name = "another"
		case "later_member":
			constraint.Fields[1] = "id"
		case "order":
			constraint.Fields[0], constraint.Fields[1] = constraint.Fields[1], constraint.Fields[0]
		}
		digest, err := hashSQLiteRelationIntent(changed)
		if err != nil || digest == seal.digest {
			t.Fatal("constraint mutation did not change the execution seal", mode, err)
		}
	}
}

func TestSQLiteNamedConstraintNamespaceIsSeparateAndBoundToModel(t *testing.T) {
	column, err := sqliteUniqueIndexName("composite_entry", "value")
	if err != nil {
		t.Fatal(err)
	}
	model, err := sqliteNamedUniqueIndexName("composite_entry", "value")
	if err != nil {
		t.Fatal(err)
	}
	other, err := sqliteNamedUniqueIndexName("other_entry", "value")
	if err != nil {
		t.Fatal(err)
	}
	long, err := sqliteNamedUniqueIndexName("composite_entry", strings.Repeat("a", 1000))
	if err != nil {
		t.Fatal(err)
	}
	if column == model || model == other || len(long) != len(model) {
		t.Fatal("constraint naming lost namespace separation or bounded spelling")
	}
}
