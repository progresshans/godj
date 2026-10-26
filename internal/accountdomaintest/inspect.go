package accountdomaintest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/account/domain/modeldef"
	"github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

// Deliberately create damaged storage with a lost uniqueness constraint. The
// source model and its historical migration remain unchanged. A full startup
// inspection must find nonadjacent duplicates across the keyset page boundary.
func inspectLostUniqueness(t *testing.T, open Open) {
	t.Run("inspect_lost_uniqueness", func(t *testing.T) {
		for _, field := range []string{"operation_id", "generation"} {
			t.Run(field, func(t *testing.T) {
				backend, _ := open(t)
				schema, err := modeldef.Schema()
				if err != nil {
					t.Fatal(err)
				}
				model := schema.Models[0]
				for index := range model.Fields {
					if model.Fields[index].Name == field {
						model.Fields[index].Unique = false
					}
				}
				migration := migrations.Migration{App: schema.AppLabel, Name: "0001_damaged_fixture", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: schema.AppLabel, Model: model}}}
				encoded, err := definition.Encode(definition.Producer{Name: "domain-corrupt-storage-test", Version: "1"}, migration)
				if err != nil {
					t.Fatal(err)
				}
				loaded, _, err := definition.Load(definition.Source{SourceID: "deliberately-damaged", Document: encoded})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
					t.Fatal(err)
				}
				var previous models.Transition
				if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
					for sequence := int64(1); sequence <= 257; sequence++ {
						operation, generation := fmt.Sprintf("%064x", sequence), fmt.Sprintf("%064x", sequence+2000)
						if sequence == 1 || sequence == 257 {
							if field == "operation_id" {
								operation = fmt.Sprintf("%064x", 10000)
							} else {
								generation = fmt.Sprintf("%064x", 10000)
							}
						}
						input := models.NewTransitionCreate(operation, sequence, roleA, generation, evidence, instant)
						if sequence > 1 {
							input = input.WithPreviousID(previous.ID)
						}
						var err error
						previous, err = models.TransitionObjects.Create(t.Context(), scope, input)
						if err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				// The normal current lookup's two-link check has a different scope.
				if latest, err := domain.Read(t.Context(), backend, roleA); err != nil || latest.Sequence() != 257 {
					t.Fatal("fixture did not isolate old duplicate", err)
				}
				got, err := db.ReadSnapshotResult(t.Context(), backend, domain.InspectIn)
				if !errors.Is(err, domain.ErrCorrupt) || got.Sequence() != 0 {
					t.Fatal("inspection accepted a resurrected generation/operation", err)
				}
			})
		}
	})
}

func inspectScope(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	first := initialize(t, backend)
	if got, err := domain.InspectIn(t.Context(), backend); !errors.Is(err, domain.ErrInvalid) || got.Sequence() != 0 {
		t.Fatal("inspection accepted a root", err)
	}
	if got, err := domain.InspectIn(nil, backend); !errors.Is(err, domain.ErrInvalid) || got.Sequence() != 0 {
		t.Fatal("inspection accepted a nil context", err)
	}
	var retained db.Queryer
	if err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		retained = scope
		got, err := domain.InspectIn(t.Context(), scope)
		if err != nil {
			return err
		}
		if got.OperationID() != first.OperationID() {
			return errors.New("inspection lost current receipt")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := domain.InspectIn(context.Background(), retained); err == nil || got.Sequence() != 0 {
		t.Fatal("retained scope published receipt")
	}
}
