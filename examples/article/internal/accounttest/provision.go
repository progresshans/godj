// Package accounttest supplies explicit setup for Article's selected-account
// integration tests. It uses the host's real command policy and typed owner;
// the test remains responsible for the already-open backend's lifetime.
package accounttest

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/progresshans/godj/examples/article/internal/accounts"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/project/firstaccount"
)

func Provision(ctx context.Context, backend firstaccount.Backend, username, password string) (accountmodel.FirstAccountCreation, error) {
	command, err := accounts.FirstAccount(func(context.Context) (firstaccount.Backend, error) { return backend, nil })
	if err != nil {
		return accountmodel.FirstAccountCreation{}, err
	}
	description, err := command.Description()
	if err != nil {
		return accountmodel.FirstAccountCreation{}, err
	}
	input := []firstaccount.Value{{Name: "username", Present: true, Text: []byte(username)}, {Name: "email"}}
	defer clear(input[0].Text)
	prepared, err := command.Prepare(ctx, description.Fingerprint, input)
	if err != nil {
		return accountmodel.FirstAccountCreation{}, err
	}
	defer prepared.Clear()
	opened, err := command.Open(ctx)
	if err != nil {
		return accountmodel.FirstAccountCreation{}, err
	}
	var operation [32]byte
	if _, err := rand.Read(operation[:]); err != nil {
		return accountmodel.FirstAccountCreation{}, err
	}
	return command.Provision(ctx, opened, hex.EncodeToString(operation[:]), prepared, password)
}
