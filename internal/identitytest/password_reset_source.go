package identitytest

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
)

type resetMailScopeOwner struct {
	identity.ManagementBackend
	root  db.Queryer
	mode  string
	reads int
}

type resetQueryOnly struct{ db.Queryer }

func (owner *resetMailScopeOwner) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	owner.reads++
	return owner.ManagementBackend.ReadSnapshot(ctx, func(scope db.Queryer) error {
		switch owner.mode {
		case "root":
			return read(owner.root)
		case "typed_nil":
			return read((*resetQueryOnly)(nil))
		case "without_validator":
			return read(&resetQueryOnly{scope})
		default:
			return read(scope)
		}
	})
}

func passwordResetSourceScopes(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"root", "typed_nil", "without_validator"} {
		t.Run(mode, func(t *testing.T) {
			backend, other := open(t)
			fixture := newManagementFixture(t, backend, 2)
			owner := &resetMailScopeOwner{ManagementBackend: fixture.runtime, root: backend, mode: mode}
			now := loginInstant
			resetter := resetService(t, owner, fixture.hasher, resetConfig(t, &now))
			before, audit := snapshotIdentitySystemRows(t, backend)
			memory := resetMemory(t)
			err := resetMailer(t, resetter, memory, resetMailConfig(t)).Request(t.Context(), fixture.user.Email)
			if err == nil || owner.reads != 1 || fixture.hasher.calls.Load() != 0 {
				t.Fatal("reset source accepted an invalid read scope or retried", err)
			}
			if messages, err := memory.Drain(); err != nil || len(messages) != 0 {
				t.Fatal("invalid read scope published mail", err)
			}
			after, afterAudit := snapshotIdentitySystemRows(t, other)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(audit, afterAudit) {
				t.Fatal("invalid reset read scope changed durable state")
			}
		})
	}
}
