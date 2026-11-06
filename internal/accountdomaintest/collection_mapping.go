package accountdomaintest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/db"
)

func collectionMappingInventory() domain.MappingInventory {
	catalog := catalogMappingRows()
	return domain.MappingInventory{Accounts: mappingRows(), Catalog: &catalog, Collections: []domain.CollectionIdentity{
		{Kind: "account", Key: -71, Revision: 1, Additional: true}, {Kind: "group", Key: -72, Revision: 1, Additional: true}, {Kind: "permission", Key: -73, Revision: 1, Additional: true}, {Kind: "account", Key: 0, Revision: 4},
	}}
}

func collectionMappingHistory(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	change := prepare(t, roleB)
	input := collectionMappingInventory()
	receipt, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		return domain.TransitionMappedIn(ctx, scope, first, change, instant, input)
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Collections[0].Key = 999
	input.Catalog.Groups[0].TargetID = 999
	next, err := transition(t.Context(), backend, receipt, prepare(t, roleA))
	if err != nil {
		t.Fatal(err)
	}
	reader := reopen(t)
	actual, err := db.ReadSnapshotResult(t.Context(), reader, func(ctx context.Context, scope db.Queryer) (domain.MappingInventory, error) {
		return domain.LookupMappingInventoryIn(ctx, scope, receipt)
	})
	if err != nil || !reflect.DeepEqual(actual, collectionMappingInventory()) {
		t.Fatal("reopened collection identities lost independent native evidence", err)
	}
	header, err := db.ReadSnapshotResult(t.Context(), reader, domain.InspectIn)
	if err != nil || header.OperationID() != next.OperationID() {
		t.Fatal("full history rejected collection payload", err)
	}
	var borrowed db.Queryer
	if err := reader.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		borrowed = scope
		_, err := domain.LookupMappingInventoryIn(t.Context(), scope, receipt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if value, err := domain.LookupMappingInventoryIn(t.Context(), borrowed, receipt); err == nil || !reflect.DeepEqual(value, domain.MappingInventory{}) {
		t.Fatal("expired collection lookup published state", err)
	}
}

func collectionMappingFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	for _, mode := range []string{"insert", "outer_rollback", "rewritten_payload"} {
		t.Run(mode, func(t *testing.T) {
			change := prepare(t, roleB)
			faults := 0
			receipt, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
				var selected db.Session = scope
				if mode == "insert" {
					selected = mappingInsertFailure{scope, &faults}
				}
				if mode == "rewritten_payload" {
					selected = mappingRewrite{scope, &faults}
				}
				actual, err := domain.TransitionMappedIn(ctx, selected, first, change, instant, collectionMappingInventory())
				if err == nil && mode == "outer_rollback" {
					faults++
					return actual, errors.New("after collection mapping write")
				}
				return actual, err
			})
			if err == nil || receipt.OperationID() != "" || faults != 1 {
				t.Fatal("collection fault did not reach atomic boundary", err)
			}
			if got, err := domain.Lookup(t.Context(), reopen(t), change.OperationID()); err != domain.ErrUninitialized || got.OperationID() != "" {
				t.Fatal("failed collection mapping left a receipt", err)
			}
			current, err := db.ReadSnapshotResult(t.Context(), reopen(t), domain.InspectIn)
			if err != nil || current.OperationID() != first.OperationID() {
				t.Fatal("failed collection write changed prior ownership", err)
			}
		})
	}
}
