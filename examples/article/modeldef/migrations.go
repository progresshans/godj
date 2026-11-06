package modeldef

import (
	"github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/migrations/definition"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

// MigrationSources explicitly installs the selected external identity models
// and the independent account-domain, audit and session storage. When selecting
// project-owned account models, replace identity.MigrationSources with their
// reviewed project migrations; keep the three framework storage apps.
// Changing this declaration never adopts or converts an existing account DB.
func MigrationSources() []definition.Source {
	sources := identity.MigrationSources()
	sources = append(sources, domain.MigrationSources()...)
	sources = append(sources, audit.MigrationSources()...)
	return append(sources, sessiondb.MigrationSources()...)
}
