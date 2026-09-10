// Package systemstate owns GoDj's explicitly migrated framework system state.
package systemstate

import (
	_ "embed"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

const (
	initialMigrationApp       = "godj_system"
	initialMigrationName      = "0001_initial"
	initialDefinitionSourceID = "systemstate/godj_system.0001_initial"
	initialDefinitionDigest   = "sha256:a1112e7f570164ec50c3b9748185c4e7e54e60d5cbf5972490aa87c64bbbd9e2"

	credentialModelName                 = "credential"
	credentialTableName                 = "godj_system_credential"
	credentialPrincipalIDColumn         = "principal_id"
	credentialPrincipalIDMaxLength      = 128
	credentialUsernameColumn            = "username"
	credentialUsernameMaxLength         = 256
	credentialEncodedPasswordColumn     = "encoded_password"
	credentialEncodedPasswordMaxLength  = 2048
	credentialActiveColumn              = "active"
	credentialPermissionsColumn         = "permissions"
	credentialPermissionsMaxLength      = 65536
	credentialDefinitionDigestColumn    = "definition_digest"
	credentialDefinitionDigestMaxLength = 71
	sessionModelName                    = "session"
	sessionTableName                    = "godj_system_session"
	sessionDigestColumn                 = "digest"
	sessionDigestMaxLength              = 64
	sessionPayloadColumn                = "payload"
	sessionPayloadMaxLength             = 32768
	auditModelName                      = "audit"
	auditTableName                      = "godj_system_audit"
	auditActorIDColumn                  = "actor_id"
	auditActorIDMaxLength               = 128
	auditModelColumn                    = "model"
	auditModelMaxLength                 = 128
	auditObjectIDColumn                 = "object_id"
	auditObjectIDMaxLength              = 64
	auditActionColumn                   = "action"
	auditActionMaxLength                = 16
	auditChangedFieldsColumn            = "changed_fields"
	auditChangedFieldsMaxLength         = 32768
	auditDisplayLabelColumn             = "display_label"
	auditDisplayLabelMaxLength          = 1024
)

//go:embed testdata/0001_initial.godj.json
var initialDefinitionDocument []byte

// InitialMigrationKey returns the sole current system-schema migration
// identity. Applying it remains the caller's explicit responsibility.
func InitialMigrationKey() migrations.MigrationKey {
	return migrations.MigrationKey{App: initialMigrationApp, Name: initialMigrationName}
}

// InitialDefinitionSource returns a fresh caller-owned copy of the current
// system-schema definition. Constructing this value performs no filesystem or
// database I/O and never applies, adopts, or repairs a schema.
func InitialDefinitionSource() definition.Source {
	return definition.Source{
		SourceID: initialDefinitionSourceID,
		Document: append([]byte(nil), initialDefinitionDocument...),
	}
}
