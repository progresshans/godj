// Package settings turns a mutable startup definition into an immutable
// project settings snapshot.
package settings

import (
	"fmt"
	"strings"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/storage"
)

// Definition is the startup input for project settings.
type Definition struct {
	ProjectName   string
	InstalledApps []apps.Config
	// Storages is an explicit immutable alias snapshot. Resource lifetimes
	// remain with the application that opened the registered backends.
	Storages storage.Registry
}

// Settings is an immutable project settings snapshot.
type Settings struct {
	projectName string
	apps        apps.Registry
	storages    storage.Registry
}

// New validates and snapshots one settings definition.
func New(definition Definition) (Settings, error) {
	projectName := strings.TrimSpace(definition.ProjectName)
	if projectName == "" {
		return Settings{}, &Error{Field: "project_name", Detail: "project name is empty"}
	}
	registry, err := apps.New(definition.InstalledApps)
	if err != nil {
		return Settings{}, &Error{Field: "installed_apps", Detail: "invalid app registry", Cause: err}
	}
	return Settings{projectName: projectName, apps: registry, storages: definition.Storages}, nil
}

// ProjectName returns the stable project name.
func (s Settings) ProjectName() string {
	return s.projectName
}

// Apps returns the immutable installed-app registry.
func (s Settings) Apps() apps.Registry {
	return s.apps
}

func (s Settings) Storages() storage.Registry { return s.storages }

func (Settings) Format(s fmt.State, _ rune) { fmt.Fprint(s, "settings.Settings{redacted}") }
