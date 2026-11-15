package systemstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
)

type identitySnapshotFunc func(context.Context, func(db.Queryer) error) error

func (function identitySnapshotFunc) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	return function(ctx, callback)
}

func TestHistoricalProfileCannotUseTheByteEnvelopeToEscapeIR(t *testing.T) {
	for _, row := range []models.User{
		{ID: 1, Revision: 1, Username: strings.Repeat("x", 257)},
		{ID: 1, Revision: 1, Username: "valid", FirstName: strings.Repeat("한", 151)},
		{ID: 1, Revision: 1, Username: "valid", Email: strings.Repeat("x", 255)},
	} {
		directory := &identityDirectory{}
		// An invalid stored profile must be rejected before permissions or any
		// other lookup can run, including for a caller with an empty reader.
		if account, err := directory.credentialFromRow(t.Context(), nil, row); err == nil || account.SessionStamp() != "" {
			t.Fatal("invalid stored profile published", err)
		}
	}
}

type identityQueryFailure struct{ err error }

func (value identityQueryFailure) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, value.err
}

func TestHistoricalDirectoryRejectsInvalidCallsAndSnapshotContractFailure(t *testing.T) {
	var typedNil identitySnapshotFunc
	if _, err := newIdentityDirectory(typedNil); !errors.Is(err, &auth.Error{Code: auth.CodeInvalidConfig}) {
		t.Fatal("typed nil snapshot backend accepted", err)
	}
	for _, mode := range []string{"zero_callbacks", "nil_reader", "twice", "ignored_error", "canceled_after_callback"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("private driver failure")
			directory, err := newIdentityDirectory(identitySnapshotFunc(func(ctx context.Context, callback func(db.Queryer) error) error {
				switch mode {
				case "zero_callbacks":
					return nil
				case "nil_reader":
					return callback(nil)
				case "twice":
					_ = callback(identityQueryFailure{failure})
					return callback(identityQueryFailure{failure})
				case "canceled_after_callback":
					_ = callback(identityQueryFailure{failure})
					cancel()
					return nil
				default:
					_ = callback(identityQueryFailure{failure})
					return nil
				}
			}))
			if err != nil {
				t.Fatal(err)
			}
			account, found, err := directory.CredentialByUsername(ctx, "member")
			if err == nil || found || account.SessionStamp() != "" {
				t.Fatal("broken snapshot published identity", found, err)
			}
			if mode == "ignored_error" && !errors.Is(err, failure) || mode == "canceled_after_callback" && !errors.Is(err, context.Canceled) {
				t.Fatal("snapshot failure lost its cause", err)
			}
		})
	}
	calls := 0
	directory, err := newIdentityDirectory(identitySnapshotFunc(func(context.Context, func(db.Queryer) error) error { calls++; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := directory.CredentialByUsername(t.Context(), "bad\x00name"); err == nil {
		t.Fatal("invalid username was accepted")
	}
	if _, _, err := directory.CredentialByID(t.Context(), " invalid-id"); err == nil {
		t.Fatal("invalid principal was accepted")
	}
	if _, _, err := directory.CredentialByUsername(nil, "member"); err == nil {
		t.Fatal("nil context was accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := directory.CredentialByUsername(canceled, "member"); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("invalid call reached snapshot backend", calls, err)
	}
}

func TestHistoricalDirectoryFailuresDoNotRenderDriverSecrets(t *testing.T) {
	const secret = "must-not-appear-in-error-or-json"
	cause := errors.New(secret)
	directory, err := newIdentityDirectory(identitySnapshotFunc(func(context.Context, func(db.Queryer) error) error { return cause }))
	if err != nil {
		t.Fatal(err)
	}
	_, _, failure := directory.CredentialByID(t.Context(), "member")
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
