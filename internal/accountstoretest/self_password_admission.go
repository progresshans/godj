package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
)

func selfPasswordRefusals(t *testing.T, open Open) {
	for _, mode := range []string{"wrong_old", "untrimmed_old", "empty_new", "weak", "canceled", "nil_context"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			previous := f.targetSessions[0]
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			old, next := password, selfPassword
			switch mode {
			case "wrong_old":
				old = "wrong"
			case "untrimmed_old":
				old = " " + password
			case "empty_new":
				next = ""
			case "weak":
				next = "short"
			case "canceled":
				cancel()
			case "nil_context":
				ctx = nil
			}
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			f.owner.hasher.hashes.Store(0)
			f.owner.hasher.verifies.Store(0)
			f.owner.boundary.calls = 0
			result, err := f.runtime.PasswordChangePersistence().ChangePassword(ctx, previous.ID(), old, next)
			passwordNoResult(t, result, err)
			diagnostics, rejected := validation.Rejected(err)
			input := mode != "canceled" && mode != "nil_context"
			if input {
				field := validation.Field("password")
				if mode == "wrong_old" || mode == "untrimmed_old" {
					field = "old_password"
				}
				if !rejected || diagnostics.ByField(field).Empty() || errors.Unwrap(err) != nil {
					t.Fatal("self password input refusal lost its direct diagnostics", mode, err)
				}
			} else if rejected || err == auth.ErrInvalidCredentials {
				t.Fatal("self password execution error became ordinary refusal", mode, err)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("self password cancellation cause lost", err)
			}
			verifies := int64(0)
			if input {
				verifies = 1
			}
			if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != verifies || f.owner.boundary.calls != 0 || f.entropy.count() != entropy || !reflect.DeepEqual(before, userReferenceSnapshot(t, reopen(t))) {
				t.Fatal("self password preflight failure changed native/host state or started new work")
			}
			for _, value := range []any{result, err} {
				selfPasswordPrivate(t, value, old, next, f.target.EncodedPassword, previous.ID().Encoded(), f.credential.SessionStamp())
			}
		})
	}
}
