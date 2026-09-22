package definition_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/internal/cascadetest"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestCascadeHistoricalWirePreservesCreateAddAlterAndPolicyDigest(t *testing.T) {
	changes, _, _, after := cascadetest.History(t, true, true)
	cycle, _ := cascadetest.RequiredCycle(t)
	created := changes[0]
	created.Operations = append([]migrations.Operation(nil), created.Operations...)
	created.Operations[1] = migrations.CreateModel{AppLabel: created.App, Model: after}
	producer := definition.Producer{Name: "cascade-test", Version: "1"}
	for _, history := range [][]migrations.Migration{{created}, {cycle}, changes} {
		var sources []definition.Source
		for _, migration := range history {
			wire, err := definition.Encode(producer, migration)
			if err != nil {
				t.Fatal(err)
			}
			sources = append(sources, definition.Source{SourceID: migration.App + "/" + migration.Name, Document: wire})
		}
		loaded, _, err := definition.Load(sources...)
		if err != nil {
			t.Fatal(err)
		}
		for index, migration := range loaded.Definitions() {
			again, err := definition.Encode(producer, migration)
			if err != nil || !bytes.Equal(again, sources[index].Document) {
				t.Fatal("canonical history round trip lost the policy", err)
			}
		}
		reconstructor, err := loaded.Reconstructor(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
		if err != nil {
			t.Fatal(err)
		}
		modelName := "child"
		if history[0].App == "cascadecycle" {
			modelName = "first"
		}
		model, found := state.Model(history[0].App, modelName)
		if !found || model.Fields[1].Relation.OnDelete != ir.DeleteCascade {
			t.Fatal("Create/Add/Alter reconstruction erased CASCADE")
		}
		for _, replacement := range []string{"protect", "restrict", "CASCADE", ""} {
			changed := make([]definition.Source, len(sources))
			modified := false
			for index, source := range sources {
				changed[index] = definition.Source{SourceID: source.SourceID, Document: bytes.ReplaceAll(source.Document, []byte(`"on_delete":"cascade"`), []byte(`"on_delete":"`+replacement+`"`))}
				modified = modified || !bytes.Equal(changed[index].Document, source.Document)
			}
			if !modified {
				t.Fatal("negative control did not modify a CASCADE declaration")
			}
			other, _, err := definition.Load(changed...)
			if replacement == "protect" {
				// The mixed Alter still changes cardinality/uniqueness, so it
				// remains a real supported delta after replacing its policy.
				if err != nil || other.Digest() == loaded.Digest() {
					t.Fatal("historical digest erased a valid policy change", err)
				}
			} else if err == nil || other.Digest() != "" {
				t.Fatal("unsupported policy published a normalized or partial history")
			}
		}
	}
}
