package accountdomaintest

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/db"
)

func lookupContract(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	first := initialize(t, backend)
	second, err := transition(t.Context(), backend, first, prepare(t, roleB))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Follows(first) || first.Follows(second) || second.Follows(second) || second.Follows(domain.Receipt{}) || (domain.Receipt{}).Follows(first) {
		t.Fatal("receipt linkage used sequence or zero state")
	}
	missing := fingerprint("not applied")
	if got, err := domain.Lookup(t.Context(), reopen(t), missing); err != domain.ErrUninitialized || got.Sequence() != 0 {
		t.Fatal("confirmed absence lost its exact sentinel", err)
	}
	var retained db.Queryer
	if err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		retained = scope
		old, found, err := domain.LookupIn(t.Context(), scope, first.OperationID())
		if err != nil || !found || !second.Follows(old) {
			return errors.New("borrowed lookup lost actual predecessor")
		}
		got, present, err := domain.LookupIn(t.Context(), scope, missing)
		if err != nil || present || got.Sequence() != 0 {
			return errors.New("borrowed absence was not data")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, found, err := domain.LookupIn(t.Context(), retained, first.OperationID()); err == nil || found || got.Sequence() != 0 {
		t.Fatal("expired lookup scope accepted")
	}
	if _, found, err := domain.LookupIn(t.Context(), backend, first.OperationID()); !errors.Is(err, domain.ErrInvalid) || found {
		t.Fatal("root lookup scope accepted", err)
	}
	fault := errors.New("private operation lookup cleanup")
	for _, operation := range []string{first.OperationID(), missing} {
		reader := snapshotFunc(func(ctx context.Context, read func(db.Queryer) error) error {
			if err := backend.ReadSnapshot(ctx, read); err != nil {
				return err
			}
			return fault
		})
		if got, err := domain.Lookup(t.Context(), reader, operation); !errors.Is(err, fault) || err == domain.ErrUninitialized || got.Sequence() != 0 {
			t.Fatal("cleanup failed but lookup published presence or absence", err)
		}
	}
	for _, mode := range []string{"zero", "nil", "twice", "ignored_callback"} {
		reader := snapshotFunc(func(ctx context.Context, read func(db.Queryer) error) error {
			switch mode {
			case "zero":
				return nil
			case "nil":
				_ = read(nil)
				return nil
			case "twice":
				if err := backend.ReadSnapshot(ctx, read); err != nil {
					return err
				}
				_ = backend.ReadSnapshot(ctx, read)
				return nil
			default:
				_ = read(backend)
				return nil
			}
		})
		if got, err := domain.Lookup(t.Context(), reader, first.OperationID()); err == nil || got.Sequence() != 0 {
			t.Fatal(mode, "lookup published invalid callback")
		}
	}
}
