package main

import (
	"context"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	identityapi "github.com/progresshans/godj/identity/api"
)

// These guards make the identity schema export independent of database rows,
// password work and authentication. The real adapter still builds every route.
func identityDocuments(guard *authenticationGuard, session, bearer api.AlternativeAuthentication) ([]schemaFile, error) {
	policies, err := hostproject.BindRelationDeleters()
	if err != nil {
		return nil, err
	}
	var files []schemaFile
	for _, profile := range []struct {
		name           string
		authentication api.AlternativeAuthentication
	}{{"identitysession", session}, {"identitybearer", bearer}} {
		application, err := identityapi.New(identityapi.Config{Namespace: "godj_identity", Backend: identityExportBackend{guard}, PasswordHasher: identityExportHasher{guard}, Authorizer: guard, Authentication: profile.authentication, Users: policies.AccountsUser, Groups: policies.AccountsGroup, Permissions: policies.AccountsPermission})
		if err != nil {
			return nil, err
		}
		document, err := application.OpenAPI()
		if err != nil {
			return nil, err
		}
		files = append(files, schemaFile{name: profile.name + ".json", data: document.Bytes()})
	}
	return files, nil
}

type identityExportBackend struct{ guard *authenticationGuard }

func (b identityExportBackend) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	return b.guard.reject("identity read")
}
func (b identityExportBackend) CoordinatedAtomic(context.Context, func(db.Session) error) error {
	return b.guard.reject("identity write")
}
func (b identityExportBackend) CoordinatedAtomicRelation(context.Context, func(db.RelationSession) error) error {
	return b.guard.reject("identity relation write")
}
func (b identityExportBackend) AppendAudit(context.Context, db.Session, admin.PreparedEvent) error {
	return b.guard.reject("identity audit")
}
func (b identityExportBackend) RevokePrincipalSessions(context.Context, db.Session, string) (int, error) {
	return 0, b.guard.reject("identity session revocation")
}

type identityExportHasher struct{ guard *authenticationGuard }

func (h identityExportHasher) Hash(context.Context, string) (string, error) {
	return "", h.guard.reject("password hash")
}
func (h identityExportHasher) Verify(context.Context, string, string) (bool, error) {
	return false, h.guard.reject("password verification")
}
func (h identityExportHasher) ValidateEncoded(string) error {
	return h.guard.reject("password validation")
}
