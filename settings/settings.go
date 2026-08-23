// Package settings turns a mutable startup definition into an immutable
// project settings snapshot.
package settings

import (
	"strings"

	"github.com/progresshans/godj/apps"
)

// Definition is the startup input for project settings.
type Definition struct {
	ProjectName   string
	InstalledApps []apps.Config
}

// Settings is an immutable project settings snapshot.
type Settings struct {
	projectName string
	apps        apps.Registry
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
	return Settings{projectName: projectName, apps: registry}, nil
}

// ProjectName returns the stable project name.
func (s Settings) ProjectName() string {
	return s.projectName
}

// Apps returns the immutable installed-app registry.
func (s Settings) Apps() apps.Registry {
	return s.apps
}
