package systemstate

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
)

func TestRuntimeResolvesCurrentCredentialAndRejectsStalePasswordVerification(t *testing.T) {
	ctx, database, policy, runtime, _ := permissionFixture(t)
	previous, err := runtime.Authenticator().Authenticate(ctx, "admin", "permission-change-password")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := readCredentialRows(ctx, database)
	if err != nil || len(rows) != 1 {
		t.Fatal("read initial credential")
	}
	replacement, err := policy.PasswordHasher.Hash(ctx, "replacement-password")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate maintenance outside this runtime. This is a stale-snapshot guard,
	// not a public password-maintenance API or noncooperative-writer guarantee.
	count, err := database.Update(ctx, query.NewUpdatePlan(credentialTableName, []query.Assignment{
		query.NewAssignment(credentialEncodedPasswordRef, query.String(replacement)),
	}, credentialIDRef, query.Integer(rows[0].id)))
	if err != nil || count != 1 {
		t.Fatal("replace stored credential")
	}
	current, err := runtime.Authenticator().Resolve(ctx, "operator")
	if err != nil || !current.Principal().Authenticated() || current.MatchesSessionStamp(previous.SessionStamp()) {
		t.Fatal("resolver restored obsolete credential")
	}
	if result, err := runtime.Authenticator().Authenticate(ctx, "admin", "permission-change-password"); err != auth.ErrInvalidCredentials || result.Principal().Authenticated() {
		t.Fatal("stale verified password adopted current credential")
	}
	fresh, err := OpenExisting(ctx, database, operatorRuntimeConfig(policy))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fresh.Authenticator().Authenticate(ctx, "admin", "replacement-password")
	if err != nil || !result.MatchesSessionStamp(current.SessionStamp()) {
		t.Fatal("reopened runtime did not authenticate current credential")
	}
	_, err = runtime.Authenticator().Authenticate(ctx, "admin", "replacement-password")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatal("immutable login verifier was silently replaced")
	}
}
