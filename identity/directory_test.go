package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type snapshotFunc func(context.Context, func(db.Queryer) error) error

func (function snapshotFunc) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	return function(ctx, callback)
}

type queryFailure struct{ err error }

func (value queryFailure) Query(context.Context, query.Plan) (db.Rows, error) { return nil, value.err }

func TestDirectoryRejectsInvalidCallsAndSnapshotContractFailure(t *testing.T) {
	var typedNil snapshotFunc
	if _, err := NewDirectory(typedNil); !errors.Is(err, &auth.Error{Code: auth.CodeInvalidConfig}) {
		t.Fatal("typed nil snapshot backend accepted", err)
	}
	for _, mode := range []string{"zero_callbacks", "nil_reader", "twice", "ignored_error", "canceled_after_callback"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("private driver failure")
			directory, err := NewDirectory(snapshotFunc(func(ctx context.Context, callback func(db.Queryer) error) error {
				switch mode {
				case "zero_callbacks":
					return nil
				case "nil_reader":
					return callback(nil)
				case "twice":
					_ = callback(queryFailure{failure})
					return callback(queryFailure{failure})
				case "canceled_after_callback":
					_ = callback(queryFailure{failure})
					cancel()
					return nil
				default:
					_ = callback(queryFailure{failure})
					return nil
				}
			}))
			if err != nil {
				t.Fatal(err)
			}
			account, found, err := directory.ByUsername(ctx, "member")
			if err == nil || found || account.Profile().ID != 0 {
				t.Fatal("broken snapshot published identity", found, err)
			}
			if mode == "ignored_error" && !errors.Is(err, failure) || mode == "canceled_after_callback" && !errors.Is(err, context.Canceled) {
				t.Fatal("snapshot failure lost its cause", err)
			}
		})
	}
	calls := 0
	directory, err := NewDirectory(snapshotFunc(func(context.Context, func(db.Queryer) error) error { calls++; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := directory.ByUsername(t.Context(), "bad\x00name"); err == nil {
		t.Fatal("invalid username was accepted")
	}
	if _, _, err := directory.ByPrincipalID(t.Context(), " invalid-id"); err == nil {
		t.Fatal("invalid principal was accepted")
	}
	if _, _, err := directory.ByUsername(nil, "member"); err == nil {
		t.Fatal("nil context was accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := directory.ByUsername(canceled, "member"); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("invalid call reached snapshot backend", calls, err)
	}
}

func TestDirectoryFailuresDoNotRenderDriverSecrets(t *testing.T) {
	const secret = "must-not-appear-in-error-or-json"
	cause := errors.New(secret)
	directory, err := NewDirectory(snapshotFunc(func(context.Context, func(db.Queryer) error) error { return cause }))
	if err != nil {
		t.Fatal(err)
	}
	_, _, failure := directory.ByPrincipalID(t.Context(), "member")
	if !errors.Is(failure, cause) {
		t.Fatal("failure cause was not preserved")
	}
	encoded, err := json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{fmt.Sprint(failure), fmt.Sprintf("%+v", failure), fmt.Sprintf("%#v", failure), string(encoded)} {
		if strings.Contains(output, secret) {
			t.Fatal("driver secret entered a default diagnostic")
		}
	}
}
