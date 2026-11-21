package consumer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

type observedRankedInput struct {
	amount int64
	calls  *int
}

func (input observedRankedInput) BuildManyToManyCreate(source, target ir.Field, sourceKey, targetKey int64) orm.Mutation[owners.RankedLink] {
	*input.calls++
	return owners.NewRankedLinkCreate(input.amount).BuildManyToManyCreate(source, target, sourceKey, targetKey)
}

// This external generated module checks the intended two-phase use: prepare a
// typed scalar placeholder in a closed snapshot, then supply its final value
// without changing any nullable FK or the requested collection topology.
func TestCollectionObservations(t *testing.T) {
	withCollectionBackends(t, func(t *testing.T, backend collectionBackend, reopen func() (collectionBackend, error), _ func(string) error, _ bool) {
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateCollections(t, backend)
		collections, err := project.BindCollections()
		check(t, err)
		target, err := labels.LabelObjects.Create(t.Context(), backend, labels.NewLabelCreate("observation target"))
		check(t, err)
		other, err := reopen()
		check(t, err)
		t.Cleanup(func() { check(t, other.Close()) })
		observe := func(t *testing.T, change orm.CollectionChange) orm.ObservedCollectionChanges {
			t.Helper()
			result, err := db.ReadSnapshotResult(t.Context(), backend.(db.SnapshotReader), func(ctx context.Context, reader db.Queryer) (orm.ObservedCollectionChanges, error) {
				return orm.ObserveCollectionChangesInReadScope(ctx, reader, change)
			})
			check(t, err)
			return result
		}
		for index, mode := range []string{"ranked_scalar", "retained", "unique_failure", "outer_failure"} {
			t.Run(mode, func(t *testing.T) {
				owner, err := owners.OwnerObjects.Create(t.Context(), backend, owners.NewOwnerCreate(mode))
				check(t, err)
				amount := int64(1000 + index*10)
				var retained owners.RankedLink
				if mode == "retained" {
					retained, err = owners.RankedLinkObjects.Create(t.Context(), backend, owners.NewRankedLinkCreate(amount).WithOwnerID(owner.ID).WithLabelID(target.ID))
					check(t, err)
				}
				if mode == "unique_failure" {
					// Only the payload is unique here. A different physical pair
					// must not turn this violation into a pair-conflict no-op.
					_, err = owners.RankedLinkObjects.Create(t.Context(), backend, owners.NewRankedLinkCreate(amount+1))
					check(t, err)
				}
				before, err := owners.RankedLinkObjects.Using(other).OrderBy(owners.RankedLinkFields.ID.Asc()).All(t.Context())
				check(t, err)
				readCalls, writeCalls := 0, 0
				change, err := collections.OwnersOwnerRanked.SetKeysChange(owner, []int64{target.ID}, orm.ManyToManySetOptions[owners.RankedLink]{ThroughDefaults: observedRankedInput{amount, &readCalls}})
				check(t, err)
				observed := observe(t, change)
				wantedCalls := 1
				if mode == "retained" {
					wantedCalls = 0
				}
				if readCalls != wantedCalls || len(observed.Effects().Insertions()) != wantedCalls {
					t.Fatal("observation evaluated a retained pair or omitted its typed candidate")
				}
				final, err := collections.OwnersOwnerRanked.SetKeysChange(owner, []int64{target.ID}, orm.ManyToManySetOptions[owners.RankedLink]{ThroughDefaults: observedRankedInput{amount + 1, &writeCalls}})
				check(t, err)
				fault := errors.New("outer collection observation failure")
				var applied orm.AppliedCollectionChanges
				err = backend.CoordinatedAtomicRelation(t.Context(), func(scope db.RelationSession) error {
					prepared, err := observed.PrepareInSession(t.Context(), scope, final)
					if err != nil {
						return err
					}
					if mode != "retained" {
						old, ok := observed.Effects().Insertions()[0].Values["amount"].Integer()
						value, valid := prepared.Effects().Insertions()[0].Values["amount"].Integer()
						if !ok || !valid || old != amount || value != amount+1 {
							return errors.New("final scalar preparation changed its observation or lost its final value")
						}
					}
					applied, err = prepared.Apply(t.Context(), nil)
					if err != nil {
						return err
					}
					if mode == "outer_failure" {
						return fault
					}
					return applied.Verify(t.Context())
				})
				after, readErr := owners.RankedLinkObjects.Using(other).OrderBy(owners.RankedLinkFields.ID.Asc()).All(t.Context())
				check(t, readErr)
				if readCalls != wantedCalls || writeCalls != wantedCalls {
					t.Fatal("phases repeated inputs or evaluated a retained pair")
				}
				if mode == "unique_failure" || mode == "outer_failure" {
					if err == nil || !reflect.DeepEqual(before, after) || mode == "outer_failure" && !errors.Is(err, fault) {
						t.Fatal("failed final scalar write escaped rollback", err)
					}
					if mode == "unique_failure" && len(applied.Insertions()) != 0 {
						t.Fatal("failed unique insertion published a row")
					}
					return
				}
				check(t, err)
				if mode == "retained" {
					if !reflect.DeepEqual(before, after) || len(applied.Insertions()) != 0 || retained.Amount != amount {
						t.Fatal("retained pair lost its identity or scalar payload")
					}
					return
				}
				if len(after) != len(before)+1 || len(applied.Insertions()) != 1 {
					t.Fatal("final scalar insertion cardinality")
				}
				insert := applied.Insertions()[0]
				row, err := owners.RankedLinkObjects.Using(other).Filter(owners.RankedLinkFields.ID.Exact(insert.Key)).Get(t.Context())
				check(t, err)
				actual, ok := insert.Values["amount"].Integer()
				if !ok || actual != amount+1 || row.Amount != amount+1 || row.OwnerID == nil || *row.OwnerID != owner.ID || row.LabelID == nil || *row.LabelID != target.ID {
					t.Fatal("committed typed scalar or endpoint differs from its actual native row")
				}
			})
		}
		t.Run("loose_scalar", func(t *testing.T) {
			owner, err := owners.OwnerObjects.Create(t.Context(), backend, owners.NewOwnerCreate("plain observation"))
			check(t, err)
			change, err := collections.OwnersOwnerLoose.SetKeysChange(owner, []int64{target.ID}, orm.ManyToManySetOptions[owners.LooseLink]{ThroughDefaults: owners.NewLooseLinkCreate(5)})
			check(t, err)
			observed := observe(t, change)
			final, err := collections.OwnersOwnerLoose.SetKeysChange(owner, []int64{target.ID}, orm.ManyToManySetOptions[owners.LooseLink]{ThroughDefaults: owners.NewLooseLinkCreate(17)})
			check(t, err)
			var applied orm.AppliedCollectionChanges
			check(t, backend.CoordinatedAtomicRelation(t.Context(), func(scope db.RelationSession) error {
				prepared, err := observed.PrepareInSession(t.Context(), scope, final)
				if err != nil {
					return err
				}
				applied, err = prepared.Apply(t.Context(), nil)
				return err
			}))
			if len(applied.Insertions()) != 1 {
				t.Fatal("ordinary through lost its actual inserted row")
			}
			row, err := owners.LooseLinkObjects.Using(other).Filter(owners.LooseLinkFields.ID.Exact(applied.Insertions()[0].Key)).Get(t.Context())
			check(t, err)
			if row.Amount != 17 || row.OwnerID == nil || *row.OwnerID != owner.ID || row.LabelID == nil || *row.LabelID != target.ID {
				t.Fatal("ordinary through did not commit the final typed scalar")
			}
		})
	})
}
