package accountdomaintest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/db"
)

func legacyOriginInventory() domain.MappingInventory {
	origin := &domain.LegacyIdentityOrigin{SchemaSHA256: fingerprint("legacy schema"), ProjectSHA256: fingerprint("host graph"), OwnerSHA256: fingerprint("legacy owner"), AccountsSHA256: fingerprint("full accounts"), CatalogSHA256: fingerprint("full catalog"), SessionsSHA256: fingerprint("full sessions"), SessionRecords: 3, AuditSHA256: fingerprint("full audit"), AuditEvents: 257}
	return domain.MappingInventory{Accounts: []domain.Mapping{{SourceID: 0, TargetID: 0, SourceRevision: 2, TargetRevision: 2}}, Catalog: &domain.CatalogMappings{Groups: []domain.Mapping{{SourceID: -7, TargetID: -7, SourceRevision: 3, TargetRevision: 3}}, Permissions: []domain.Mapping{}}, Legacy: origin}
}

func legacyOriginHistory(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	input := legacyOriginInventory()
	change := prepare(t, roleA)
	first, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		return domain.InitializeAdoptedIn(ctx, scope, change, instant, input)
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Legacy.AuditEvents = 0
	input.Accounts[0].TargetID = 7
	next := mappedTransition(t, backend, first, mappingRows())
	next = catalogMappedTransition(t, backend, next, catalogMappingRows())
	collections := collectionMappingInventory()
	change = prepare(t, roleB)
	next, err = db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		return domain.TransitionMappedIn(ctx, scope, next, change, instant, collections)
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 257 {
		next, err = transition(t.Context(), backend, next, prepare(t, roleB))
		if err != nil {
			t.Fatal(err)
		}
	}
	reader := reopen(t)
	latest, err := db.ReadSnapshotResult(t.Context(), reader, domain.InspectIn)
	if err != nil || latest.OperationID() != next.OperationID() {
		t.Fatal("mixed v4/v1/v2/v3 history lost its initial origin", err)
	}
	var retained db.Queryer
	if err := reader.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		retained = scope
		actual, origin, found, err := domain.InitialLegacyOriginIn(t.Context(), scope)
		if err != nil || !found || actual.OperationID() != first.OperationID() || origin != *legacyOriginInventory().Legacy {
			t.Fatal("later transitions replaced historical origin", err)
		}
		inventory, err := domain.LookupMappingInventoryIn(t.Context(), scope, first)
		if err != nil || !reflect.DeepEqual(inventory, legacyOriginInventory()) {
			t.Fatal("origin correspondence lost canonical identity values", err)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if receipt, _, found, err := domain.InitialLegacyOriginIn(t.Context(), retained); err == nil || found || receipt.Sequence() != 0 {
		t.Fatal("expired origin lookup published evidence")
	}
	if receipt, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		return domain.InitializeAdoptedIn(ctx, scope, prepare(t, roleB), instant, legacyOriginInventory())
	}); !errors.Is(err, domain.ErrStale) || receipt.Sequence() != 0 {
		t.Fatal("existing ownership was adopted twice", err)
	}
	if receipt, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
		return domain.TransitionMappedIn(ctx, scope, next, prepare(t, roleA), instant, legacyOriginInventory())
	}); !errors.Is(err, domain.ErrInvalid) || receipt.Sequence() != 0 {
		t.Fatal("later transition accepted legacy origin", err)
	}
}

func legacyOriginFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	for _, mode := range []string{"missing_origin", "insert", "outer_rollback", "rewritten_payload"} {
		t.Run(mode, func(t *testing.T) {
			input := legacyOriginInventory()
			change := prepare(t, roleA)
			faults := 0
			if mode == "missing_origin" {
				input.Legacy = nil
			}
			receipt, err := db.CoordinatedAtomicResult(t.Context(), backend, func(ctx context.Context, scope db.Session) (domain.Receipt, error) {
				var writer db.Session = scope
				if mode == "insert" {
					writer = mappingInsertFailure{scope, &faults}
				}
				if mode == "rewritten_payload" {
					writer = mappingRewrite{scope, &faults}
				}
				value, err := domain.InitializeAdoptedIn(ctx, writer, change, instant, input)
				if err == nil && mode == "outer_rollback" {
					err = errors.New("after complete adopted receipt")
				}
				return value, err
			})
			if err == nil || receipt.Sequence() != 0 {
				t.Fatal("incomplete adoption published ownership")
			}
			if (mode == "insert" || mode == "rewritten_payload") && faults != 1 {
				t.Fatal("native payload fault was not reached", faults, err)
			}
			if current, err := db.ReadSnapshotResult(t.Context(), reopen(t), domain.InspectIn); err != domain.ErrUninitialized || current.Sequence() != 0 {
				t.Fatal("failed adoption left partial history", err)
			}
		})
	}
}
