package accountstoretest

import (
	"errors"
	"testing"

	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func creationConcurrentWrites(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	for _, mode := range []string{"same_login", "same_candidate"} {
		t.Run(mode, func(t *testing.T) {
			hasher := &creationHasher{PasswordHasher: f.hasher}
			creators := []*accountmodel.Creator[models.User]{accountCreator(t, backend, hasher, creationConfig()), accountCreator(t, reopen(t), hasher, creationConfig())}
			first, err := creators[0].Prepare(t.Context(), actor, creationInput(t, "concurrent-"+mode), password)
			if err != nil {
				t.Fatal(err)
			}
			candidates := []accountmodel.PreparedCreation[models.User]{first, first}
			wantHash := int64(1)
			if mode == "same_candidate" {
				creators[1] = creators[0]
			} else {
				candidates[1], err = creators[1].Prepare(t.Context(), actor, creationInput(t, "concurrent-"+mode), password)
				if err != nil {
					t.Fatal(err)
				}
				wantHash = 2
			}
			before := creationCounts(t, backend)
			type outcome struct {
				result accountmodel.Creation
				err    error
			}
			start := make(chan struct{})
			done := make(chan outcome, 2)
			for i := range 2 {
				go func() {
					<-start
					result, err := creators[i].Commit(t.Context(), actor, candidates[i])
					done <- outcome{result, err}
				}()
			}
			close(start)
			successes, rejections := 0, 0
			for range 2 {
				out := <-done
				if out.err == nil {
					successes++
					if out.result.Revision != 1 {
						t.Fatal("winner has no revision")
					}
				} else {
					if out.result.Revision != 0 {
						t.Fatal("loser exposed row")
					}
					if mode == "same_candidate" {
						if !errors.Is(out.err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
							t.Fatal(out.err)
						}
					} else if diagnostics, rejected := validation.Rejected(out.err); !rejected || diagnostics.ByField("username").Empty() {
						t.Fatal("duplicate race bypassed final validation", out.err)
					}
					rejections++
				}
			}
			after := creationCounts(t, reopen(t))
			if successes != 1 || rejections != 1 || hasher.calls.Load() != wantHash || after[0] != before[0]+1 || after[3] != before[3]+1 || after[1] != before[1] || after[2] != before[2] {
				t.Fatal("creation race did not produce exactly one whole account", before, after)
			}
		})
	}
}

func creationDomainBinding(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	hasher := &creationHasher{PasswordHasher: f.hasher}
	creator := accountCreator(t, backend, hasher, creationConfig())
	pending, err := creator.Prepare(t.Context(), actor, creationInput(t, "domain-pending"), password)
	if err != nil {
		t.Fatal(err)
	}
	transitionSessionDomain(t, backend, f.binding, f.ownership)
	fresh := profileActor(t, f).Principal()
	if result, err := creator.Commit(t.Context(), fresh, pending); result.Revision != 0 || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
		t.Fatal("new domain adopted old preparation", err)
	}
	if result, err := creator.Commit(t.Context(), actor, pending); result.Revision != 0 || err == nil {
		t.Fatal("old domain created current account")
	}
	if creationExists(t, reopen(t), "domain-pending") {
		t.Fatal("domain refusal left account")
	}
	pending, err = creator.Prepare(t.Context(), fresh, creationInput(t, "fresh-domain"), password)
	if err != nil {
		t.Fatal(err)
	}
	result, err := creator.Commit(t.Context(), fresh, pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(profileHistory(t, reopen(t), actor, result.ID)) != 0 || len(profileHistory(t, backend, fresh, result.ID)) != 1 {
		t.Fatal("creation audit crossed domains")
	}
	if hasher.calls.Load() != 2 {
		t.Fatal("domain checks rehashed")
	}
}
