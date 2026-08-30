//go:build darwin || linux

package projectmigratetargetproduct_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectLinkedTargetedMigrateSQLite(t *testing.T) {
	project := newTargetExternalProject(t)

	t.Run("MIG-119_exact_argv_and_exact_name_miss", func(t *testing.T) {
		targetExerciseAcceptedPublicFamilies(t, project)
		targetAssertRejectedArgumentsPrecedeProjectIO(t, project)
		targetAssertPrefixLookingNameIsExactMiss(t, project)
	})

	t.Run("MIG-120_named_forward_closure", func(t *testing.T) {
		targetAssertNamedForwardClosure(t, project)
	})

	t.Run("MIG-121_named_reverse_descendants", func(t *testing.T) {
		targetAssertNamedReverseDescendants(t, project)
	})

	t.Run("MIG-122_app_zero_DEV-0002_order", func(t *testing.T) {
		targetAssertAppZeroOrder(t, project)
	})

	t.Run("MIG-123_noop_and_known_zero_unknown", func(t *testing.T) {
		targetAssertNoopAndKnownZeroUnknown(t, project)
	})

	t.Run("MIG-124_plan_is_exact_and_read_only", func(t *testing.T) {
		targetAssertPlanIsReadOnly(t, project)
	})

	t.Run("MIG-125_preview_drift_fresh_execute", func(t *testing.T) {
		targetAssertPreviewDriftUsesFreshHistory(t, project)
	})

	t.Run("MIG-126_reverse_middle_failure_fresh_resume", func(t *testing.T) {
		targetAssertReverseMiddleFailureResume(t, project)
	})

	t.Run("MIG-128_phase_C_public_ownership_subset", func(t *testing.T) {
		targetAssertPhaseCOwnershipSubset(t, project)
	})

	t.Run("exact_public_family_coverage", func(t *testing.T) {
		project.assertAllPublicFamilies(t)
		project.assertWorkspaceEmpty(t)
		project.assertApplicationUnchanged(t)
		project.assertArtifactsRedacted(t, project.secret)
	})
}

func targetExerciseAcceptedPublicFamilies(t *testing.T, project *targetExternalProject) {
	t.Helper()
	latestPlan := targetPlanOutput(t,
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha1, Direction: "forward"},
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha2, Direction: "forward"},
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha3, Direction: "forward"},
		targetPlanRow{App: targetBetaApp, Name: targetBeta1, Direction: "forward"},
		targetPlanRow{App: targetCharlieApp, Name: targetCharlie1, Direction: "forward"},
		targetPlanRow{App: targetGammaApp, Name: targetGamma1, Direction: "forward"},
	)
	targetPlan := targetPlanOutput(t,
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha1, Direction: "forward"},
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha2, Direction: "forward"},
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha3, Direction: "forward"},
	)
	tests := []struct {
		name     string
		argv     []string
		execute  bool
		wantPlan string
	}{
		{name: "execute_latest_implicit", argv: []string{"migrate"}, execute: true},
		{name: "execute_latest_explicit", argv: []string{"migrate", "--project", project.descriptor}, execute: true},
		{name: "plan_latest_implicit", argv: []string{"migrate", "--plan"}, wantPlan: latestPlan},
		{name: "plan_latest_explicit", argv: []string{"migrate", "--plan", "--project", project.descriptor}, wantPlan: latestPlan},
		{name: "execute_target_implicit", argv: []string{"migrate", targetAlphaApp, targetAlpha3}, execute: true},
		{name: "execute_target_explicit", argv: []string{"migrate", targetAlphaApp, targetAlpha3, "--project", project.descriptor}, execute: true},
		{name: "plan_target_implicit", argv: []string{"migrate", targetAlphaApp, targetAlpha3, "--plan"}, wantPlan: targetPlan},
		{name: "plan_target_explicit", argv: []string{"migrate", targetAlphaApp, targetAlpha3, "--plan", "--project", project.descriptor}, wantPlan: targetPlan},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, marker := project.paths(t, "accepted-"+test.name)
			if !test.execute {
				targetInitializeSQLite(t, database)
			}
			result := project.run(t, project.environment(database, marker, targetCatalogBranch), test.argv...)
			if test.execute {
				targetAssertExecuteSuccess(t, result, 6, project.sensitive(database)...)
				return
			}
			targetAssertSuccess(t, result, test.wantPlan, project.sensitive(database)...)
		})
	}
}

func targetAssertRejectedArgumentsPrecedeProjectIO(t *testing.T, project *targetExternalProject) {
	t.Helper()
	tests := []struct {
		name string
		argv []string
	}{
		{name: "app_only", argv: []string{"migrate", targetAlphaApp}},
		{name: "permuted_project_plan", argv: []string{"migrate", "--project", filepath.Join(project.universe, "missing-poison-descriptor.toml"), "--plan"}},
		{name: "repeated_plan", argv: []string{"migrate", "--plan", "--plan"}},
		{name: "double_dash", argv: []string{"migrate", "--", targetAlphaApp, targetAlpha1}},
		{name: "unknown_option", argv: []string{"migrate", "--database", "other"}},
		{name: "leading_dash_app", argv: []string{"migrate", "--alpha", targetAlpha1}},
		{name: "leading_dash_name", argv: []string{"migrate", targetAlphaApp, "--0001"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, marker := project.paths(t, "rejected-"+test.name)
			result := project.runAt(t, project.unselected, project.environment(database, marker, targetCatalogBranch), test.argv...)
			targetAssertFailure(t, result, 2, "migration_project_command_error/invalid_arguments\n", project.sensitive(database)...)
			targetAssertPathAbsent(t, marker)
			targetAssertPathAbsent(t, database)
		})
	}
}

func targetAssertPrefixLookingNameIsExactMiss(t *testing.T, project *targetExternalProject) {
	t.Helper()
	database, marker := project.paths(t, "prefix-looking-exact-miss")
	targetInitializeSQLite(t, database)
	before := targetCaptureSQLite(t, database)
	result := project.run(t, project.environment(database, marker, targetCatalogBranch),
		"migrate", targetAlphaApp, "0001", "--plan", "--project", project.descriptor)
	targetAssertFailure(t, result, 1, "migration_plan_error/target_not_found\n", project.sensitive(database)...)
	targetAssertSQLiteUnchanged(t, database, before)
	targetAssertMarkerEvents(t, marker,
		targetEventBackendOpen,
		targetEventSessionOpen,
		targetEventHistoryRead,
		targetEventSessionClose,
		targetEventBackendClose,
	)
}

func targetAssertNamedForwardClosure(t *testing.T, project *targetExternalProject) {
	t.Helper()
	database, marker := project.paths(t, "named-forward")
	targetInitializeSQLite(t, database)
	before := targetCaptureSQLite(t, database)
	plan := project.run(t, project.environment(database, marker, targetCatalogBranch),
		"migrate", targetAlphaApp, targetAlpha3, "--plan", "--project", project.descriptor)
	targetAssertSuccess(t, plan, targetPlanOutput(t,
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha1, Direction: "forward"},
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha2, Direction: "forward"},
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha3, Direction: "forward"},
	), project.sensitive(database)...)
	targetAssertSQLiteUnchanged(t, database, before)
	targetAssertMarkerEvents(t, marker,
		targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
		targetEventSessionClose, targetEventBackendClose,
	)

	targetResetMarker(t, marker)
	execute := project.run(t, project.environment(database, marker, targetCatalogBranch),
		"migrate", targetAlphaApp, targetAlpha3, "--project", project.descriptor)
	targetAssertExecuteSuccess(t, execute, 6, project.sensitive(database)...)
	targetAssertSQLiteHistory(t, database,
		targetHistory(targetAlphaApp, targetAlpha1),
		targetHistory(targetAlphaApp, targetAlpha2),
		targetHistory(targetAlphaApp, targetAlpha3),
	)
	targetAssertSQLiteRevision(t, database, 3)
	targetAssertSQLiteTables(t, database, targetAlpha1Table, targetAlpha2Table, targetAlpha3Table)
	targetAssertMarkerEvents(t, marker,
		targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
		targetBeginEvent(targetAlphaApp, targetAlpha1, "forward"), targetCreateEvent(targetAlpha1Table), targetRecordAppliedEvent(targetAlphaApp, targetAlpha1), targetCommitEvent(targetAlphaApp, targetAlpha1, "forward"),
		targetBeginEvent(targetAlphaApp, targetAlpha2, "forward"), targetCreateEvent(targetAlpha2Table), targetRecordAppliedEvent(targetAlphaApp, targetAlpha2), targetCommitEvent(targetAlphaApp, targetAlpha2, "forward"),
		targetBeginEvent(targetAlphaApp, targetAlpha3, "forward"), targetCreateEvent(targetAlpha3Table), targetRecordAppliedEvent(targetAlphaApp, targetAlpha3), targetCommitEvent(targetAlphaApp, targetAlpha3, "forward"),
		targetEventSessionClose, targetEventBackendClose,
	)
}

func targetAssertNamedReverseDescendants(t *testing.T, project *targetExternalProject) {
	t.Helper()
	database, marker := project.paths(t, "named-reverse")
	environment := project.environment(database, marker, targetCatalogBranch)
	seed := project.run(t, environment, "migrate", "--project", project.descriptor)
	targetAssertExecuteSuccess(t, seed, 6, project.sensitive(database)...)
	targetInsertSQLiteValue(t, database, targetAlpha1Table, "named reverse sentinel")
	targetResetMarker(t, marker)
	before := targetCaptureSQLite(t, database)

	plan := project.run(t, environment, "migrate", targetAlphaApp, targetAlpha1, "--plan", "--project", project.descriptor)
	targetAssertSuccess(t, plan, targetPlanOutput(t,
		targetPlanRow{App: targetCharlieApp, Name: targetCharlie1, Direction: "backward"},
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha3, Direction: "backward"},
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha2, Direction: "backward"},
	), project.sensitive(database)...)
	targetAssertSQLiteUnchanged(t, database, before)
	targetAssertMarkerEvents(t, marker, targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead, targetEventSessionClose, targetEventBackendClose)

	targetResetMarker(t, marker)
	execute := project.run(t, environment, "migrate", targetAlphaApp, targetAlpha1, "--project", project.descriptor)
	targetAssertExecuteSuccess(t, execute, 6, project.sensitive(database)...)
	targetAssertSQLiteHistory(t, database,
		targetHistory(targetAlphaApp, targetAlpha1),
		targetHistory(targetBetaApp, targetBeta1),
		targetHistory(targetGammaApp, targetGamma1),
	)
	targetAssertSQLiteRevision(t, database, 9)
	targetAssertSQLiteTables(t, database, targetAlpha1Table, targetBeta1Table, targetGamma1Table)
	if got := targetSQLiteValues(t, database, targetAlpha1Table); !reflect.DeepEqual(got, []string{"named reverse sentinel"}) {
		t.Fatalf("named reverse sentinel values = %v", got)
	}
	targetAssertMarkerEvents(t, marker,
		targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
		targetBeginEvent(targetCharlieApp, targetCharlie1, "backward"), targetDeleteEvent(targetCharlie1Table), targetRecordUnappliedEvent(targetCharlieApp, targetCharlie1), targetCommitEvent(targetCharlieApp, targetCharlie1, "backward"),
		targetBeginEvent(targetAlphaApp, targetAlpha3, "backward"), targetDeleteEvent(targetAlpha3Table), targetRecordUnappliedEvent(targetAlphaApp, targetAlpha3), targetCommitEvent(targetAlphaApp, targetAlpha3, "backward"),
		targetBeginEvent(targetAlphaApp, targetAlpha2, "backward"), targetDeleteEvent(targetAlpha2Table), targetRecordUnappliedEvent(targetAlphaApp, targetAlpha2), targetCommitEvent(targetAlphaApp, targetAlpha2, "backward"),
		targetEventSessionClose, targetEventBackendClose,
	)
}

func targetAssertAppZeroOrder(t *testing.T, project *targetExternalProject) {
	t.Helper()
	database, marker := project.paths(t, "app-zero")
	environment := project.environment(database, marker, targetCatalogZero)
	for _, target := range [][2]string{{targetAlphaApp, targetAlpha3}, {targetBetaApp, targetBeta1}, {targetGammaApp, targetGamma1}} {
		result := project.run(t, environment, "migrate", target[0], target[1], "--project", project.descriptor)
		targetAssertExecuteSuccess(t, result, 5, project.sensitive(database)...)
	}
	targetInsertSQLiteValue(t, database, targetGamma1Table, "unrelated survives zero")
	targetAssertSQLiteRevision(t, database, 5)
	targetResetMarker(t, marker)
	before := targetCaptureSQLite(t, database)

	plan := project.run(t, environment, "migrate", targetAlphaApp, "zero", "--plan", "--project", project.descriptor)
	targetAssertSuccess(t, plan, targetPlanOutput(t,
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha3, Direction: "backward"},
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha2, Direction: "backward"},
		targetPlanRow{App: targetBetaApp, Name: targetBeta1, Direction: "backward"},
		targetPlanRow{App: targetAlphaApp, Name: targetAlpha1, Direction: "backward"},
	), project.sensitive(database)...)
	targetAssertSQLiteUnchanged(t, database, before)

	targetResetMarker(t, marker)
	execute := project.run(t, environment, "migrate", targetAlphaApp, "zero", "--project", project.descriptor)
	targetAssertExecuteSuccess(t, execute, 5, project.sensitive(database)...)
	targetAssertSQLiteHistory(t, database, targetHistory(targetGammaApp, targetGamma1))
	targetAssertSQLiteRevision(t, database, 9)
	targetAssertSQLiteTables(t, database, targetGamma1Table)
	if got := targetSQLiteValues(t, database, targetGamma1Table); !reflect.DeepEqual(got, []string{"unrelated survives zero"}) {
		t.Fatalf("app-zero unrelated values = %v", got)
	}
	wantSteps := []string{
		targetBeginEvent(targetAlphaApp, targetAlpha3, "backward"),
		targetBeginEvent(targetAlphaApp, targetAlpha2, "backward"),
		targetBeginEvent(targetBetaApp, targetBeta1, "backward"),
		targetBeginEvent(targetAlphaApp, targetAlpha1, "backward"),
	}
	markers := targetAssertMarkerEvents(t, marker,
		targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
		targetBeginEvent(targetAlphaApp, targetAlpha3, "backward"), targetDeleteEvent(targetAlpha3Table), targetRecordUnappliedEvent(targetAlphaApp, targetAlpha3), targetCommitEvent(targetAlphaApp, targetAlpha3, "backward"),
		targetBeginEvent(targetAlphaApp, targetAlpha2, "backward"), targetDeleteEvent(targetAlpha2Table), targetRecordUnappliedEvent(targetAlphaApp, targetAlpha2), targetCommitEvent(targetAlphaApp, targetAlpha2, "backward"),
		targetBeginEvent(targetBetaApp, targetBeta1, "backward"), targetDeleteEvent(targetBeta1Table), targetRecordUnappliedEvent(targetBetaApp, targetBeta1), targetCommitEvent(targetBetaApp, targetBeta1, "backward"),
		targetBeginEvent(targetAlphaApp, targetAlpha1, "backward"), targetDeleteEvent(targetAlpha1Table), targetRecordUnappliedEvent(targetAlphaApp, targetAlpha1), targetCommitEvent(targetAlphaApp, targetAlpha1, "backward"),
		targetEventSessionClose, targetEventBackendClose,
	)
	if got := targetBeginEvents(targetMarkerEventNames(markers)); !reflect.DeepEqual(got, wantSteps) {
		t.Fatalf("app-zero begin order = %v, want %v", got, wantSteps)
	}
}

func targetAssertNoopAndKnownZeroUnknown(t *testing.T, project *targetExternalProject) {
	t.Helper()
	t.Run("applied_named_leaf", func(t *testing.T) {
		database, marker := project.paths(t, "named-noop")
		environment := project.environment(database, marker, targetCatalogBlog)
		seed := project.run(t, environment, "migrate", targetBlogApp, targetBlog2, "--project", project.descriptor)
		targetAssertExecuteSuccess(t, seed, 2, project.sensitive(database)...)
		targetResetMarker(t, marker)
		before := targetCaptureSQLite(t, database)
		plan := project.run(t, environment, "migrate", targetBlogApp, targetBlog2, "--plan", "--project", project.descriptor)
		targetAssertSuccess(t, plan, targetPlanOutput(t), project.sensitive(database)...)
		targetAssertSQLiteUnchanged(t, database, before)
		targetAssertMarkerEvents(t, marker, targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead, targetEventSessionClose, targetEventBackendClose)
		targetResetMarker(t, marker)
		execute := project.run(t, environment, "migrate", targetBlogApp, targetBlog2, "--project", project.descriptor)
		targetAssertExecuteSuccess(t, execute, 2, project.sensitive(database)...)
		targetAssertSQLiteUnchanged(t, database, before)
		targetAssertMarkerEvents(t, marker, targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead, targetEventSessionClose, targetEventBackendClose)
	})

	t.Run("public_known_zero_unknown", func(t *testing.T) {
		database, marker := project.paths(t, "unknown-zero")
		targetInitializeSQLite(t, database)
		before := targetCaptureSQLite(t, database)
		result := project.run(t, project.environment(database, marker, targetCatalogBlog),
			"migrate", "unknown", "zero", "--plan", "--project", project.descriptor)
		targetAssertFailure(t, result, 1, "migration_plan_error/target_not_found\n", project.sensitive(database)...)
		targetAssertSQLiteUnchanged(t, database, before)
		targetAssertMarkerEvents(t, marker, targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead, targetEventSessionClose, targetEventBackendClose)
	})
}

func targetAssertPlanIsReadOnly(t *testing.T, project *targetExternalProject) {
	t.Helper()
	tests := []struct {
		name       string
		planTarget string
		want       []targetPlanRow
	}{
		{name: "nonempty", planTarget: targetBlog1, want: []targetPlanRow{{App: targetBlogApp, Name: targetBlog2, Direction: "backward"}}},
		{name: "empty", planTarget: targetBlog2, want: []targetPlanRow{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, marker := project.paths(t, "plan-"+test.name)
			environment := project.environment(database, marker, targetCatalogBlog)
			seed := project.run(t, environment, "migrate", targetBlogApp, targetBlog2, "--project", project.descriptor)
			targetAssertExecuteSuccess(t, seed, 2, project.sensitive(database)...)
			targetInsertSQLiteValue(t, database, targetBlog1Table, "plan sentinel")
			targetResetMarker(t, marker)
			before := targetCaptureSQLite(t, database)
			result := project.run(t, environment, "migrate", targetBlogApp, test.planTarget, "--plan", "--project", project.descriptor)
			targetAssertSuccess(t, result, targetPlanOutput(t, test.want...), project.sensitive(database)...)
			targetAssertSQLiteUnchanged(t, database, before)
			if got := targetSQLiteValues(t, database, targetBlog1Table); !reflect.DeepEqual(got, []string{"plan sentinel"}) {
				t.Fatalf("plan sentinel values = %v", got)
			}
			targetAssertMarkerEvents(t, marker, targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead, targetEventSessionClose, targetEventBackendClose)
		})
	}
}

func targetAssertPreviewDriftUsesFreshHistory(t *testing.T, project *targetExternalProject) {
	t.Helper()
	database, marker := project.paths(t, "preview-drift")
	environment := project.environment(database, marker, targetCatalogBlog)
	seed := project.run(t, environment, "migrate", targetBlogApp, targetBlog1, "--project", project.descriptor)
	targetAssertExecuteSuccess(t, seed, 2, project.sensitive(database)...)
	targetInsertSQLiteValue(t, database, targetBlog1Table, "preview drift sentinel")
	targetResetMarker(t, marker)
	previewBefore := targetCaptureSQLite(t, database)
	preview := project.run(t, environment, "migrate", targetBlogApp, targetBlog2, "--plan", "--project", project.descriptor)
	targetAssertSuccess(t, preview, targetPlanOutput(t, targetPlanRow{App: targetBlogApp, Name: targetBlog2, Direction: "forward"}), project.sensitive(database)...)
	targetAssertSQLiteUnchanged(t, database, previewBefore)
	targetAssertMarkerEvents(t, marker,
		targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
		targetEventSessionClose, targetEventBackendClose,
	)

	targetResetMarker(t, marker)
	drift := project.run(t, environment, "migrate", targetBlogApp, targetBlog2, "--project", project.descriptor)
	targetAssertExecuteSuccess(t, drift, 2, project.sensitive(database)...)
	targetAssertSQLiteHistory(t, database,
		targetHistory(targetBlogApp, targetBlog1),
		targetHistory(targetBlogApp, targetBlog2),
	)
	targetAssertSQLiteRevision(t, database, 2)
	targetAssertSQLiteTables(t, database, targetBlog1Table, targetBlog2Table)
	if got := targetSQLiteValues(t, database, targetBlog1Table); !reflect.DeepEqual(got, []string{"preview drift sentinel"}) {
		t.Fatalf("preview drift sentinel values = %v", got)
	}
	targetAssertMarkerEvents(t, marker,
		targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
		targetBeginEvent(targetBlogApp, targetBlog2, "forward"), targetCreateEvent(targetBlog2Table), targetRecordAppliedEvent(targetBlogApp, targetBlog2), targetCommitEvent(targetBlogApp, targetBlog2, "forward"),
		targetEventSessionClose, targetEventBackendClose,
	)
	afterDrift := targetCaptureSQLite(t, database)

	targetResetMarker(t, marker)
	freshExecute := project.run(t, environment, "migrate", targetBlogApp, targetBlog2, "--project", project.descriptor)
	targetAssertExecuteSuccess(t, freshExecute, 2, project.sensitive(database)...)
	targetAssertSQLiteUnchanged(t, database, afterDrift)
	targetAssertMarkerEvents(t, marker, targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead, targetEventSessionClose, targetEventBackendClose)
}

func targetAssertPhaseCOwnershipSubset(t *testing.T, project *targetExternalProject) {
	t.Helper()
	t.Run("load_before_open", func(t *testing.T) {
		database, marker := project.paths(t, "invalid-catalog")
		result := project.run(t, project.environment(database, marker, targetCatalogInvalid), "migrate", "--plan", "--project", project.descriptor)
		targetAssertFailure(t, result, 1, "migration_definition_source_error/invalid_definition_document\n", project.sensitive(database)...)
		targetAssertPathAbsent(t, marker)
		targetAssertPathAbsent(t, database)
	})

	t.Run("outer_close_discards_plan", func(t *testing.T) {
		database, marker := project.paths(t, "outer-close")
		targetInitializeSQLite(t, database)
		before := targetCaptureSQLite(t, database)
		environment := project.environmentWith(database, marker, targetCatalogBlog, map[string]string{targetFailBackendCloseEnvironment: "1"})
		result := project.run(t, environment, "migrate", "--plan", "--project", project.descriptor)
		targetAssertFailure(t, result, 3, "migration_backend_error/backend_close_failed\n", project.sensitive(database)...)
		targetAssertSQLiteUnchanged(t, database, before)
		targetAssertMarkerEvents(t, marker, targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead, targetEventSessionClose, targetEventBackendClose)
	})

	t.Run("backend_open_raw_cause_redacted", func(t *testing.T) {
		database, marker := project.paths(t, "open-redaction")
		environment := project.environmentWith(database, marker, targetCatalogBlog, map[string]string{targetFailBackendOpenEnvironment: "1"})
		result := project.run(t, environment, "migrate", "--plan", "--project", project.descriptor)
		targetAssertFailure(t, result, 3, "migration_backend_error/backend_open_failed\n", project.sensitive(database)...)
		targetAssertMarkerEvents(t, marker, targetEventBackendOpen)
		targetAssertPathAbsent(t, database)
	})
}

func targetAssertExecuteSuccess(t *testing.T, result targetCommandResult, definitions int, sensitive ...string) targetExecuteResult {
	t.Helper()
	targetAssertRedacted(t, result, sensitive...)
	if result.exitCode != 0 || result.stderr != "" || result.stdout == "" {
		t.Fatalf("target migrate execute = exit:%d stdout:%q stderr:%q", result.exitCode, result.stdout, result.stderr)
	}
	decoded := targetDecodeExecuteResult(t, result)
	if decoded.SourceCount != definitions || decoded.DefinitionCount != definitions || decoded.DefinitionSetDigest == "" {
		t.Fatalf("target migrate execute summary = %+v, want %d sources/definitions and digest", decoded, definitions)
	}
	return decoded
}

func targetAssertPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path %s unexpectedly exists: %v", path, err)
	}
}

func targetBeginEvents(events []string) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		if len(event) >= len("begin ") && event[:len("begin ")] == "begin " {
			result = append(result, event)
		}
	}
	return result
}
