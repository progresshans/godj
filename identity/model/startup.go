package model

import (
	"context"
	"fmt"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

// StartupInspection is a detached read observation, never cached authority or
// permission to provision. A fresh result means installed storage with no
// ownership history, account rows or account audit. FirstAccount still rechecks
// eligibility in its write fence. A ready result can have no surviving accounts.
type StartupInspection struct{ state *startupInspection }
type startupInspection struct {
	ownership domain.Receipt
	fresh     bool
	sessions  int
	audits    int64
}

func (StartupInspection) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.StartupInspection{redacted}"))
}
func (inspection StartupInspection) Fresh() bool {
	return inspection.state != nil && inspection.state.fresh
}
func (inspection StartupInspection) Ready() bool {
	return inspection.state != nil && !inspection.state.fresh
}
func (inspection StartupInspection) Ownership() domain.Receipt {
	if inspection.state == nil {
		return domain.Receipt{}
	}
	return inspection.state.ownership
}
func (inspection StartupInspection) SessionRecords() int {
	if inspection.state == nil {
		return 0
	}
	return inspection.state.sessions
}
func (inspection StartupInspection) AuditEvents() int64 {
	if inspection.state == nil {
		return 0
	}
	return inspection.state.audits
}

// InspectStartup verifies the exact framework-owned migration identities, then
// reads ownership history, selected account/catalog columns and collections,
// account audit and every session namespace in ONE native data snapshot.
// Migration history is a separate read: this is not a schema migration lock.
// The host owns applying/checking its project migration graph before startup.
// Column probes are not an all-user validation or an index/constraint audit.
// No initialization, migration, expiry cleanup, adoption or repair occurs here.
// Every request continues to check current domain and credential authority.
func (store *SessionStore[M]) InspectStartup(ctx context.Context) (StartupInspection, error) {
	if err := store.valid(ctx); err != nil {
		return StartupInspection{}, err
	}
	credentials := store.state.credentials
	if err := credentials.valid(); err != nil {
		return StartupInspection{}, managementError(CodeInvalidConfig, err)
	}
	if credentials.state.probeSchema == nil {
		return StartupInspection{}, managementError(CodeInvalidConfig, nil)
	}
	fingerprint, err := credentials.state.binding.RoleFingerprint()
	if err != nil {
		return StartupInspection{}, managementError(CodeInvalidConfig, err)
	}
	history, ok := store.state.backend.(migrationbackend.AppliedMigrationReader)
	if !ok || nilModelValue(history) {
		return StartupInspection{}, managementError(CodeInvalidConfig, nil)
	}
	if err := requireAccountMigrations(ctx, history); err != nil {
		return StartupInspection{}, err
	}
	result, err := db.ReadSnapshotResult(ctx, store.state.backend, func(work context.Context, scope db.Queryer) (StartupInspection, error) {
		ownership, err := domain.InspectIn(work, scope)
		// Only the exact empty-history sentinel permits a fresh observation;
		// a backend/cleanup failure wrapping it remains a failed inspection.
		fresh := err == domain.ErrUninitialized
		if err != nil && !fresh {
			return StartupInspection{}, err
		}
		if !fresh && ownership.RoleFingerprint() != fingerprint {
			return StartupInspection{}, domain.ErrMismatch
		}
		present, err := credentials.state.probeSchema(work, scope)
		if err != nil {
			return StartupInspection{}, err
		}
		if fresh && present {
			return StartupInspection{}, managementError(CodeNotFresh, nil)
		}
		audits, err := accountaudit.InspectIn(work, scope)
		if err != nil {
			return StartupInspection{}, err
		}
		if fresh && audits != 0 {
			return StartupInspection{}, managementError(CodeNotFresh, nil)
		}
		records, err := store.state.database.InspectIn(work, scope)
		if err != nil {
			return StartupInspection{}, err
		}
		return StartupInspection{state: &startupInspection{ownership: ownership, fresh: fresh, sessions: records, audits: audits}}, nil
	})
	if err != nil {
		return StartupInspection{}, managementError(CodePersistence, err)
	}
	return result, nil
}

func requireAccountMigrations(ctx context.Context, history migrationbackend.AppliedMigrationReader) error {
	sources := append(domain.MigrationSources(), accountaudit.MigrationSources()...)
	sources = append(sources, sessiondb.MigrationSources()...)
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		return managementError(CodeInvalidConfig, err)
	}
	required := make(map[migrations.MigrationKey]bool)
	owned := make(map[string]bool)
	for _, migration := range loaded.Definitions() {
		required[migration.Key()], owned[migration.App] = true, true
	}
	rows, err := history.ReadAppliedMigrations(ctx)
	if err != nil {
		return managementError(CodePersistence, err)
	}
	if err := ctx.Err(); err != nil {
		return managementError(CodePersistence, err)
	}
	for _, row := range rows {
		if !owned[row.App] {
			continue
		}
		key := migrations.MigrationKey{App: row.App, Name: row.Name}
		if !required[key] {
			return managementError(CodeSchemaUnavailable, nil)
		}
		delete(required, key)
	}
	if len(required) != 0 {
		return managementError(CodeSchemaUnavailable, nil)
	}
	return nil
}
