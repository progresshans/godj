package identity

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/temporal"
)

// LoginRecorder uses an already coordinated transaction. The owner must couple
// this observation with authenticated session persistence and roll both back on
// failure. It is never called by Authenticate, Resolve, session Access or logout.
type LoginRecorder struct{ directory *Directory }

func (*LoginRecorder) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.LoginRecorder{redacted}"))
}

func NewLoginRecorder(directory *Directory) (*LoginRecorder, error) {
	if directory == nil || directory.state == nil {
		return nil, &auth.Error{Code: auth.CodeInvalidConfig, Field: "login", Detail: "identity directory is uninitialized"}
	}
	return &LoginRecorder{directory: directory}, nil
}

// RecordIn rechecks current credential and admission before updating only the
// observational last_login field. Management revision, password, roles, grants
// and audit history are unchanged. The returned credential is provisional until
// the transaction owner confirms commit.
func (recorder *LoginRecorder) RecordIn(ctx context.Context, session db.Session, login auth.SessionLogin) (auth.Credential, error) {
	if ctx == nil || nilIdentityValue(session) || recorder == nil || recorder.directory == nil {
		return auth.Credential{}, &auth.Error{Code: auth.CodeInvalidInput, Field: "login", Detail: "login transaction is incomplete"}
	}
	if err := ctx.Err(); err != nil {
		return auth.Credential{}, err
	}
	if err := login.Validate(); err != nil {
		return auth.Credential{}, err
	}
	row, found, err := models.UserObjects.Using(session).Filter(models.UserFields.PrincipalID.Exact(login.Credential.Principal().ID())).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil {
		return auth.Credential{}, identityReadFailure(err)
	}
	if !found {
		return auth.Credential{}, auth.ErrInvalidCredentials
	}
	account, err := recorder.directory.accountFromRow(ctx, session, row)
	if err != nil {
		return auth.Credential{}, identityReadFailure(err)
	}
	current := account.value().credential
	if !current.Principal().Authenticated() || !current.MatchesSessionStamp(login.Credential.SessionStamp()) {
		return auth.Credential{}, auth.ErrInvalidCredentials
	}
	if err := login.Admit(ctx, current.Principal()); err != nil {
		return auth.Credential{}, err
	}
	if err := ctx.Err(); err != nil {
		return auth.Credential{}, err
	}
	instant, err := temporal.Canonical(login.At)
	if err != nil {
		return auth.Credential{}, &auth.Error{Code: auth.CodeInvalidInput, Field: "login", Detail: "login timestamp is outside the supported range"}
	}
	if _, err := models.UserObjects.Patch(ctx, session, row, models.UserPatch{}.WithLastLogin(instant)); err != nil {
		return auth.Credential{}, identityReadFailure(err)
	}
	return current, nil
}
