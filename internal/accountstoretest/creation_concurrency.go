package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

func creationConcurrentWrites(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	for _, mode := range []string{"same_login", "same_candidate", "case_variants"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			creators := make([]*accountmodel.Creator[models.User], 2)
			hashers := make([]*passwordChangeHasher, 2)
			encoded := make([]atomic.Value, 2)
			principals := make([]string, 2)
			logins := []string{"concurrent-" + mode, "concurrent-" + mode}
			if mode == "case_variants" {
				logins = []string{"case-variant", "CASE-VARIANT"}
			}
			for i, storage := range []Backend{backend, reopen(t)} {
				tracked := &creationOwner{Backend: storage}
				principals[i] = fmt.Sprintf("creation-race-%s-%d", mode, i)
				principal := principals[i]
				config := creationConfig()
				config.CaseInsensitiveLogin = mode == "case_variants"
				config.PrincipalID = func(context.Context) (string, error) { return principal, nil }
				hashers[i] = &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes, encode: func(ctx context.Context, raw string) (string, error) {
					value, err := f.hasher.Hash(ctx, raw)
					if err == nil {
						encoded[i].Store(value)
					}
					return value, err
				}}
				creators[i] = accountCreator(t, tracked, hashers[i], config)
			}
			before := accountNativeSnapshot(t, reopen(t))
			candidates := make([]accountmodel.PreparedCreation[models.User], 2)
			wantHashes := int64(1)
			inputs := []orm.ModelInput[models.User]{creationInput(t, logins[0]), creationInput(t, logins[1])}
			if mode == "same_candidate" {
				first, err := creators[0].Prepare(ctx, actor, inputs[0], password)
				if err != nil {
					t.Fatal(err)
				}
				candidates[0], candidates[1], creators[1] = first, first, creators[0]
			} else {
				wantHashes = 2
				arrived, release := make(chan struct{}, 2), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				var workers sync.WaitGroup
				defer func() { cancel(); unblock(); workers.Wait() }()
				type preparation struct {
					index     int
					candidate accountmodel.PreparedCreation[models.User]
					err       error
				}
				done := make(chan preparation, 2)
				for i := range 2 {
					hashers[i].beforeHash = func(ctx context.Context) error {
						arrived <- struct{}{}
						select {
						case <-release:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					workers.Add(1)
					go func() {
						defer workers.Done()
						candidate, err := creators[i].Prepare(ctx, actor, inputs[i], password)
						done <- preparation{i, candidate, err}
					}()
				}
				for range 2 {
					select {
					case <-arrived:
					case early := <-done:
						t.Fatal("creation did not reach both native-scope-free hashes", early.err)
					case <-ctx.Done():
						t.Fatal("concurrent preparation retained a scope or failed to reach hashing", ctx.Err())
					}
				}
				unblock()
				for range 2 {
					select {
					case out := <-done:
						if out.err != nil {
							t.Fatal(out.err)
						}
						candidates[out.index] = out.candidate
					case <-ctx.Done():
						t.Fatal("concurrent creation preparation did not finish", ctx.Err())
					}
				}
				workers.Wait()
				for _, hasher := range hashers {
					hasher.beforeHash = nil
				}
			}
			assertCatalogRows(t, before, accountNativeSnapshot(t, reopen(t)), actor)
			type outcome struct {
				index  int
				result accountmodel.Creation
				err    error
			}
			start, done := make(chan struct{}), make(chan outcome, 2)
			var commits sync.WaitGroup
			defer func() { cancel(); commits.Wait() }()
			for i := range 2 {
				commits.Add(1)
				go func() {
					defer commits.Done()
					<-start
					result, err := creators[i].Commit(ctx, actor, candidates[i])
					done <- outcome{i, result, err}
				}()
			}
			close(start)
			successes, rejections := 0, 0
			var winner outcome
			for range 2 {
				var out outcome
				select {
				case out = <-done:
				case <-ctx.Done():
					t.Fatal("concurrent creation did not finish inside its deadline", ctx.Err())
				}
				if out.err == nil {
					successes++
					winner = out
					if out.result.ID <= 0 || out.result.Revision != 1 || out.result.RevokedSessions != 0 || !slices.Equal(out.result.ChangedFields, userReferenceCreationFields) {
						t.Fatal("creation winner metadata differs")
					}
				} else {
					if !reflect.DeepEqual(out.result, accountmodel.Creation{}) {
						t.Fatal("creation loser exposed metadata")
					}
					if mode == "same_candidate" {
						if !errors.Is(out.err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
							t.Fatal(out.err)
						}
					} else {
						usernameUnique(t, out.err)
					}
					rejections++
				}
			}
			commits.Wait()
			if successes != 1 || rejections != 1 || hashers[0].hashes.Load()+hashers[1].hashes.Load() != wantHashes {
				t.Fatal("creation race did not have one winner or repeated hashing")
			}
			if slices.ContainsFunc(before.users, func(row models.User) bool { return row.ID == winner.result.ID }) {
				t.Fatal("creation overwrote an existing user")
			}
			origin := winner.index
			if mode == "same_candidate" {
				origin = 0
			}
			expected := userReferenceExpectedRow(winner.result.ID, logins[origin], "", encoded[origin].Load().(string))
			expected.PrincipalID = principals[origin]
			userReferenceReplace(&before, expected)
			assertCatalogRows(t, before, accountNativeSnapshot(t, reopen(t)), actor, catalogExpectedEvent{"user", expected.ID, admin.ActionAdd, userReferenceCreationFields})
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
