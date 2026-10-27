package main

import (
	"context"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/accounts"
	"github.com/progresshans/godj/examples/helpdesk/project"
)

func helpdeskDocument(ctx context.Context, backend helpdesk.SelectedBackend, guard *authenticationGuard, session api.Authentication) (schemaFile, error) {
	config, err := accounts.Policy()
	if err != nil {
		return schemaFile{}, err
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		return schemaFile{}, err
	}
	runtime, err := project.NewAccountRuntime(ctx, backend, hasher, guard, config)
	if err != nil {
		return schemaFile{}, err
	}
	application, err := helpdesk.NewSelected(backend, runtime.Credentials(), guard, 1)
	if err != nil {
		return schemaFile{}, err
	}
	adapter, err := application.API(helpdesk.APIConfig{Authentication: session})
	if err != nil {
		return schemaFile{}, err
	}
	document, err := adapter.OpenAPI()
	if err != nil {
		return schemaFile{}, err
	}
	return schemaFile{name: "helpdesksession.json", data: document.Bytes()}, nil
}
