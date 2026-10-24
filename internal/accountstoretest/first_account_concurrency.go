package accountstoretest

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/account/domain"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
)

func firstAccountConcurrent(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	hasher := &creationHasher{PasswordHasher: firstAccountHasher(t)}
	owners := []*accountmodel.FirstAccount[models.User]{firstAccountOwner(t, backend, hasher, firstAccountConfig()), firstAccountOwner(t, reopen(t), hasher, firstAccountConfig())}
	pending := make([]accountmodel.PreparedFirstAccount[models.User], 2)
	for i, name := range []string{"first-left", "first-right"} {
		var err error
		pending[i], err = owners[i].Prepare(t.Context(), creationInput(t, name), password)
		if err != nil {
			t.Fatal(err)
		}
	}
	type outcome struct {
		result accountmodel.FirstAccountCreation
		err    error
		index  int
	}
	start := make(chan struct{})
	done := make(chan outcome, 2)
	for i := range 2 {
		go func() {
			<-start
			result, err := owners[i].Commit(t.Context(), pending[i])
			done <- outcome{result, err, i}
		}()
	}
	close(start)
	successes, refusals := 0, 0
	for range 2 {
		value := <-done
		receipt, err := domain.Lookup(t.Context(), reopen(t), pending[value.index].OperationID())
		if value.err == nil {
			successes++
			if err != nil || receipt.Sequence() != 1 || receipt.OperationID() != value.result.Ownership.OperationID() {
				t.Fatal("winning first receipt missing", err)
			}
		} else {
			refusals++
			if !errors.Is(value.err, &accountmodel.Error{Code: accountmodel.CodeNotFresh}) || !errors.Is(err, domain.ErrUninitialized) || value.result.Account.Revision != 0 {
				t.Fatal("losing first operation adopted or published", value.err)
			}
		}
	}
	counts := firstAccountCounts(t, reopen(t))
	if successes != 1 || refusals != 1 || hasher.calls.Load() != 2 || counts[0] != 1 || counts[1] != 0 || counts[2] != 0 || counts[3] != 1 || counts[4] != 1 {
		t.Fatal("first-account fence admitted multiple domains", counts)
	}
}
