// Package commands owns explicit local operator actions. The generated-aware
// command program is separate from the declaration-only migration runner.
package commands

import (
	"context"
	"errors"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/internal/accounts"
	"github.com/progresshans/godj/examples/article/project"
	"github.com/progresshans/godj/project/command"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
)

func Registry(environment settings.Environment) (command.Registry, error) {
	installed, err := apps.New(project.InstalledApps())
	if err != nil {
		return command.Registry{}, err
	}
	selected, selectionErr := databaseconfig.FromEnvironment(environment.Lookup)
	first, err := accounts.FirstAccount(func(ctx context.Context) (firstaccount.Backend, error) {
		if selectionErr != nil {
			return nil, selectionErr
		}
		return database.Open(ctx, selected)
	})
	if err != nil {
		return command.Registry{}, err
	}
	return command.New(command.Config{
		Apps: installed, FirstAccount: first,
		Check: func(context.Context) error { return configurationError(selectionErr) },
		ResourceChecks: []command.ResourceCheck{{Name: "database", Summary: "Read the existing database connection and schema catalog", Run: func(ctx context.Context) error {
			if selectionErr != nil {
				return configurationError(selectionErr)
			}
			return configurationError(database.Check(ctx, selected))
		}}},
	})
}

func configurationError(err error) error {
	var invalid *database.Error
	if errors.As(err, &invalid) && invalid != nil {
		return &command.CheckError{Field: invalid.Field, Cause: err}
	}
	return err
}
