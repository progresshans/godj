// Package migrationdefs composes the host's account storage migrations without
// introducing runtime imports into the pure model declaration package.
package migrationdefs

import (
	"github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/migrations/definition"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

// MigrationSources installs the explicitly selected identity app and the
// independent domain, audit and session stores. The host separately supplies
// Helpdesk's historical business migrations. Existing account data requires
// an explicit transition; these declarations never adopt or provision it.
func MigrationSources() []definition.Source {
	sources := identity.MigrationSources()
	sources = append(sources, domain.MigrationSources()...)
	sources = append(sources, audit.MigrationSources()...)
	return append(sources, sessiondb.MigrationSources()...)
}
