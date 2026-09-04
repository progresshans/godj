//go:build darwin || linux

package projectshowmigrationsproduct_test

import (
	"testing"
)

const (
	externalStatusEmptyOutput = "(no migrations)\n"
	externalStatusFreshOutput = "authors\n" +
		" [ ] 0001_author\n" +
		"blog\n" +
		" [ ] 0001_article\n" +
		" [ ] 0002_publish\n"
	externalStatusPrefixOutput = "authors\n" +
		" [X] 0001_author\n" +
		"blog\n" +
		" [X] 0001_article\n" +
		" [ ] 0002_publish\n"
	externalStatusFullOutput = "authors\n" +
		" [X] 0001_author\n" +
		"blog\n" +
		" [X] 0001_article\n" +
		" [X] 0002_publish\n"
	externalStatusBranchOutput = "alpha\n" +
		" [ ] 0099_parent\n" +
		" [ ] 0001_child\n" +
		"zeta\n" +
		" [ ] 0001_root\n"
	externalStatusUnknownOutput = "authors\n" +
		" [X] 0001_author\n" +
		"blog\n" +
		" [X] 0001_article\n" +
		" [ ] 0002_publish\n" +
		" [?] 0000_removed\n" +
		" [?] 9999_removed\n" +
		"legacy\n" +
		" [?] 0001_gone\n"
)

func TestGlobalShowMigrationsExternalProjectSQLiteProduct(t *testing.T) {
	project := newExternalStatusProject(t)

	t.Run("MIG-111_empty_catalog", func(t *testing.T) {
		database, marker := project.paths(t, "mig-111-empty")
		externalStatusInitializeSQLite(t, database)
		before := externalStatusCaptureSQLite(t, database)
		externalStatusAssertInitializedEmpty(t, before)

		result := project.runShow(t, project.environment(database, marker, "empty"))
		externalStatusAssertSuccess(t, result, externalStatusEmptyOutput, project.sensitive(database)...)
		externalStatusAssertReadLifecycle(t, marker)
		after := externalStatusCaptureSQLite(t, database)
		externalStatusAssertSQLiteUnchanged(t, before, after)
	})

	t.Run("MIG-112_fresh_unapplied", func(t *testing.T) {
		database, marker := project.paths(t, "mig-112-fresh")
		externalStatusInitializeSQLite(t, database)
		before := externalStatusCaptureSQLite(t, database)
		externalStatusAssertInitializedEmpty(t, before)

		result := project.runShow(t, project.environment(database, marker, "full"))
		externalStatusAssertSuccess(t, result, externalStatusFreshOutput, project.sensitive(database)...)
		externalStatusAssertReadLifecycle(t, marker)
		after := externalStatusCaptureSQLite(t, database)
		externalStatusAssertSQLiteUnchanged(t, before, after)
	})

	t.Run("MIG-113_applied_prefix", func(t *testing.T) {
		database, marker := project.paths(t, "mig-113-prefix")
		externalStatusMigrateSetup(t, project, project.environment(database, marker, "prefix"), database, marker)
		externalStatusSeedApplicationRows(t, database)
		before := externalStatusCaptureSQLite(t, database)
		externalStatusAssertExpectedHistory(t, before,
			externalSQLiteHistoryRow{app: "authors", name: "0001_author"},
			externalSQLiteHistoryRow{app: "blog", name: "0001_article"},
		)
		externalStatusAssertRevisionCount(t, before, 2)

		result := project.runShow(t, project.environment(database, marker, "full"))
		externalStatusAssertSuccess(t, result, externalStatusPrefixOutput, project.sensitive(database)...)
		externalStatusAssertReadLifecycle(t, marker)
		after := externalStatusCaptureSQLite(t, database)
		externalStatusAssertSQLiteUnchanged(t, before, after)
	})

	t.Run("MIG-114_fully_applied_restart", func(t *testing.T) {
		database, marker := project.paths(t, "mig-114-full-restart")
		environment := project.environment(database, marker, "full")
		externalStatusMigrateSetup(t, project, environment, database, marker)
		externalStatusSeedApplicationRows(t, database)
		before := externalStatusCaptureSQLite(t, database)
		externalStatusAssertExpectedHistory(t, before,
			externalSQLiteHistoryRow{app: "authors", name: "0001_author"},
			externalSQLiteHistoryRow{app: "blog", name: "0001_article"},
			externalSQLiteHistoryRow{app: "blog", name: "0002_publish"},
		)
		externalStatusAssertRevisionCount(t, before, 3)

		first := project.runShow(t, environment)
		externalStatusAssertSuccess(t, first, externalStatusFullOutput, project.sensitive(database)...)
		firstPID := externalStatusAssertReadLifecycle(t, marker)
		afterFirst := externalStatusCaptureSQLite(t, database)
		externalStatusAssertSQLiteUnchanged(t, before, afterFirst)

		externalStatusResetMarker(t, marker)
		second := project.runShow(t, environment)
		externalStatusAssertSuccess(t, second, externalStatusFullOutput, project.sensitive(database)...)
		secondPID := externalStatusAssertReadLifecycle(t, marker)
		if first.stdout != second.stdout || firstPID == secondPID {
			t.Fatalf("fresh-process status was not byte-identical and process-distinct: first_pid=%d second_pid=%d first=%q second=%q", firstPID, secondPID, first.stdout, second.stdout)
		}
		afterSecond := externalStatusCaptureSQLite(t, database)
		externalStatusAssertSQLiteUnchanged(t, before, afterSecond)
	})

	t.Run("MIG-115_cross_app_branch_order", func(t *testing.T) {
		database, marker := project.paths(t, "mig-115-branch")
		externalStatusInitializeSQLite(t, database)
		before := externalStatusCaptureSQLite(t, database)
		externalStatusAssertInitializedEmpty(t, before)

		result := project.runShow(t, project.environment(database, marker, "branch"))
		externalStatusAssertSuccess(t, result, externalStatusBranchOutput, project.sensitive(database)...)
		externalStatusAssertReadLifecycle(t, marker)
		after := externalStatusCaptureSQLite(t, database)
		externalStatusAssertSQLiteUnchanged(t, before, after)
	})

	t.Run("MIG-116_unknown_record_visible", func(t *testing.T) {
		database, marker := project.paths(t, "mig-116-unknown")
		seedEnvironment := project.environment(database, marker, "unknown_seed")
		externalStatusMigrateSetup(t, project, seedEnvironment, database, marker)
		externalStatusSeedApplicationRows(t, database)
		before := externalStatusCaptureSQLite(t, database)
		externalStatusAssertExpectedHistory(t, before,
			externalSQLiteHistoryRow{app: "authors", name: "0001_author"},
			externalSQLiteHistoryRow{app: "blog", name: "0000_removed"},
			externalSQLiteHistoryRow{app: "blog", name: "0001_article"},
			externalSQLiteHistoryRow{app: "blog", name: "9999_removed"},
			externalSQLiteHistoryRow{app: "legacy", name: "0001_gone"},
		)
		externalStatusAssertRevisionCount(t, before, 5)

		result := project.runShow(t, project.environment(database, marker, "full"))
		externalStatusAssertSuccess(t, result, externalStatusUnknownOutput, project.sensitive(database)...)
		externalStatusAssertReadLifecycle(t, marker)
		after := externalStatusCaptureSQLite(t, database)
		externalStatusAssertSQLiteUnchanged(t, before, after)
	})

	t.Run("MIG-117_inconsistent_known_history", func(t *testing.T) {
		database, marker := project.paths(t, "mig-117-inconsistent")
		environment := project.environment(database, marker, "full")
		externalStatusMigrateSetup(t, project, environment, database, marker)
		externalStatusSeedApplicationRows(t, database)
		externalStatusInstallInconsistentHistory(t, database)
		before := externalStatusCaptureSQLite(t, database)
		externalStatusAssertExpectedHistory(t, before,
			externalSQLiteHistoryRow{app: "blog", name: "0001_article"},
			externalSQLiteHistoryRow{app: "blog", name: "0002_publish"},
		)

		result := project.runShow(t, environment)
		externalStatusAssertFailure(t, result, 1, "migration_history_error/inconsistent_applied_history\n", project.sensitive(database)...)
		externalStatusAssertReadLifecycle(t, marker)
		after := externalStatusCaptureSQLite(t, database)
		externalStatusAssertSQLiteUnchanged(t, before, after)
	})

	t.Run("MIG-118_project_boundary", func(t *testing.T) {
		database, marker := project.paths(t, "mig-118-boundary")
		fullEnvironment := project.environment(database, marker, "full")

		invalidArguments := project.run(t, fullEnvironment, "showmigrations", "--project")
		externalStatusAssertFailure(t, invalidArguments, 2, "migration_project_command_error/invalid_arguments\n", project.sensitive(database)...)
		externalStatusAssertMarkerAbsent(t, marker)

		invalidDefinition := project.runShow(t, project.environment(database, marker, "invalid"))
		externalStatusAssertFailure(t, invalidDefinition, 1, "migration_definition_source_error/invalid_definition_document\n", project.sensitive(database)...)
		externalStatusAssertMarkerAbsent(t, marker)

		externalStatusInitializeSQLite(t, database)
		before := externalStatusCaptureSQLite(t, database)
		success := project.runShow(t, project.environment(database, marker, "empty"))
		externalStatusAssertSuccess(t, success, externalStatusEmptyOutput, project.sensitive(database)...)
		externalStatusAssertReadLifecycle(t, marker)
		after := externalStatusCaptureSQLite(t, database)
		externalStatusAssertSQLiteUnchanged(t, before, after)

		externalStatusAuditApplicationSources(t, project.repository, project.root)
		externalStatusAssertArtifactsRedacted(t, project.root, project.secret, "sqlite-secret-path-8c813d")
		project.assertWorkspaceEmpty(t)
	})
}
