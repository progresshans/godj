package main

import (
	"context"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	fixtureaccounts "github.com/progresshans/godj/conformance/identityfixture/accounts"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	identityapi "github.com/progresshans/godj/identity/api"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

// The real selected runtime performs bounded dummy-hash setup. It shares the
// caller-owned isolated backend, without migrations, rows, credential checks
// or permission callbacks. NewManagement only describes these real owners.
func identityDocuments(ctx context.Context, backend sessiondb.Backend, guard *authenticationGuard, session, bearer api.PolicyAuthentication) ([]schemaFile, error) {
	config, err := fixtureaccounts.Policy()
	if err != nil {
		return nil, err
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		return nil, err
	}
	runtime, err := hostproject.NewAccountRuntime(ctx, backend, hasher, guard, config)
	if err != nil {
		return nil, err
	}
	var files []schemaFile
	for _, profile := range []struct {
		name           string
		authentication api.PolicyAuthentication
	}{{"identitysession", session}, {"identitybearer", bearer}} {
		application, err := identityapi.NewManagement(runtime.Accounts(), runtime.Catalog(), identityapi.ManagementConfig{Namespace: "godj_identity", Authentication: profile.authentication})
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
