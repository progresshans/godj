//go:build darwin || linux

package projectmigratetargetproduct_test

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestGlobalTargetedMigratePostgresLifecycle(t *testing.T) {
	databaseURL := targetPostgresTestURL(t)
	project := newTargetExternalProject(t)

	t.Run("MIG-120_named_forward_plan_and_exact_state", func(t *testing.T) {
		schema := targetCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "named-forward")
		environment := project.postgresEnvironment(t, databaseURL, schema, marker, targetCatalogBranch)
		sensitive := targetPostgresSensitive(t, project, databaseURL, schema)
		before := targetCapturePostgres(t, databaseURL, schema)
		targetAssertPostgresEmpty(t, before)

		plan := project.run(t, environment,
			"migrate", targetAlphaApp, targetAlpha3, "--plan", "--project", project.descriptor)
		targetAssertSuccess(t, plan, targetPlanOutput(t,
			targetPlanRow{App: targetAlphaApp, Name: targetAlpha1, Direction: "forward"},
			targetPlanRow{App: targetAlphaApp, Name: targetAlpha2, Direction: "forward"},
			targetPlanRow{App: targetAlphaApp, Name: targetAlpha3, Direction: "forward"},
		), sensitive...)
		targetAssertPostgresUnchanged(t, databaseURL, schema, before)
		targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetEventSessionClose, targetEventBackendClose,
		)

		targetResetMarker(t, marker)
		execute := project.run(t, environment,
			"migrate", targetAlphaApp, targetAlpha3, "--project", project.descriptor)
		targetAssertExecuteSuccess(t, execute, 6, sensitive...)
		targetAssertPostgresState(t, databaseURL, schema, 3,
			[]string{targetAlpha1Table, targetAlpha2Table, targetAlpha3Table},
			[]targetSQLiteHistoryRow{
				targetHistory(targetAlphaApp, targetAlpha1),
				targetHistory(targetAlphaApp, targetAlpha2),
				targetHistory(targetAlphaApp, targetAlpha3),
			},
			nil,
		)
		targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetBeginEvent(targetAlphaApp, targetAlpha1, "forward"), targetCreateEvent(targetAlpha1Table), targetRecordAppliedEvent(targetAlphaApp, targetAlpha1), targetCommitEvent(targetAlphaApp, targetAlpha1, "forward"),
			targetBeginEvent(targetAlphaApp, targetAlpha2, "forward"), targetCreateEvent(targetAlpha2Table), targetRecordAppliedEvent(targetAlphaApp, targetAlpha2), targetCommitEvent(targetAlphaApp, targetAlpha2, "forward"),
			targetBeginEvent(targetAlphaApp, targetAlpha3, "forward"), targetCreateEvent(targetAlpha3Table), targetRecordAppliedEvent(targetAlphaApp, targetAlpha3), targetCommitEvent(targetAlphaApp, targetAlpha3, "forward"),
			targetEventSessionClose, targetEventBackendClose,
		)
	})

	t.Run("MIG-121_named_reverse_descendants_and_sentinel", func(t *testing.T) {
		schema := targetCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "named-reverse")
		environment := project.postgresEnvironment(t, databaseURL, schema, marker, targetCatalogBranch)
		sensitive := targetPostgresSensitive(t, project, databaseURL, schema)
		seed := project.run(t, environment, "migrate", "--project", project.descriptor)
		targetAssertExecuteSuccess(t, seed, 6, sensitive...)
		identifier := targetInsertPostgresValue(t, databaseURL, schema, targetAlpha1Table, "named reverse PostgreSQL sentinel")
		seedState := targetAssertPostgresState(t, databaseURL, schema, 6,
			[]string{targetAlpha1Table, targetAlpha2Table, targetAlpha3Table, targetBeta1Table, targetCharlie1Table, targetGamma1Table},
			[]targetSQLiteHistoryRow{
				targetHistory(targetAlphaApp, targetAlpha1), targetHistory(targetAlphaApp, targetAlpha2), targetHistory(targetAlphaApp, targetAlpha3),
				targetHistory(targetBetaApp, targetBeta1), targetHistory(targetCharlieApp, targetCharlie1), targetHistory(targetGammaApp, targetGamma1),
			},
			map[string][]targetPostgresValue{targetAlpha1Table: {{id: identifier, value: "named reverse PostgreSQL sentinel"}}},
		)
		seedEpoch := targetPostgresEpoch(t, seedState)

		targetResetMarker(t, marker)
		before := targetCapturePostgres(t, databaseURL, schema)
		plan := project.run(t, environment,
			"migrate", targetAlphaApp, targetAlpha1, "--plan", "--project", project.descriptor)
		targetAssertSuccess(t, plan, targetPlanOutput(t,
			targetPlanRow{App: targetCharlieApp, Name: targetCharlie1, Direction: "backward"},
			targetPlanRow{App: targetAlphaApp, Name: targetAlpha3, Direction: "backward"},
			targetPlanRow{App: targetAlphaApp, Name: targetAlpha2, Direction: "backward"},
		), sensitive...)
		targetAssertPostgresUnchanged(t, databaseURL, schema, before)
		targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetEventSessionClose, targetEventBackendClose,
		)

		targetResetMarker(t, marker)
		execute := project.run(t, environment,
			"migrate", targetAlphaApp, targetAlpha1, "--project", project.descriptor)
		targetAssertExecuteSuccess(t, execute, 6, sensitive...)
		after := targetAssertPostgresState(t, databaseURL, schema, 9,
			[]string{targetAlpha1Table, targetBeta1Table, targetGamma1Table},
			[]targetSQLiteHistoryRow{
				targetHistory(targetAlphaApp, targetAlpha1),
				targetHistory(targetBetaApp, targetBeta1),
				targetHistory(targetGammaApp, targetGamma1),
			},
			map[string][]targetPostgresValue{targetAlpha1Table: {{id: identifier, value: "named reverse PostgreSQL sentinel"}}},
		)
		if got := targetPostgresEpoch(t, after); got != seedEpoch {
			t.Fatalf("PostgreSQL migration epoch changed across named reverse: got %q want %q", got, seedEpoch)
		}
		targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetBeginEvent(targetCharlieApp, targetCharlie1, "backward"), targetDeleteEvent(targetCharlie1Table), targetRecordUnappliedEvent(targetCharlieApp, targetCharlie1), targetCommitEvent(targetCharlieApp, targetCharlie1, "backward"),
			targetBeginEvent(targetAlphaApp, targetAlpha3, "backward"), targetDeleteEvent(targetAlpha3Table), targetRecordUnappliedEvent(targetAlphaApp, targetAlpha3), targetCommitEvent(targetAlphaApp, targetAlpha3, "backward"),
			targetBeginEvent(targetAlphaApp, targetAlpha2, "backward"), targetDeleteEvent(targetAlpha2Table), targetRecordUnappliedEvent(targetAlphaApp, targetAlpha2), targetCommitEvent(targetAlphaApp, targetAlpha2, "backward"),
			targetEventSessionClose, targetEventBackendClose,
		)
	})

	t.Run("MIG-122_app_zero_DEV-0002_order_and_unrelated_state", func(t *testing.T) {
		schema := targetCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "app-zero")
		environment := project.postgresEnvironment(t, databaseURL, schema, marker, targetCatalogZero)
		sensitive := targetPostgresSensitive(t, project, databaseURL, schema)
		seed := project.run(t, environment, "migrate", "--project", project.descriptor)
		targetAssertExecuteSuccess(t, seed, 5, sensitive...)
		identifier := targetInsertPostgresValue(t, databaseURL, schema, targetGamma1Table, "unrelated PostgreSQL zero sentinel")
		seedState := targetAssertPostgresState(t, databaseURL, schema, 5,
			[]string{targetAlpha1Table, targetAlpha2Table, targetAlpha3Table, targetBeta1Table, targetGamma1Table},
			[]targetSQLiteHistoryRow{
				targetHistory(targetAlphaApp, targetAlpha1), targetHistory(targetAlphaApp, targetAlpha2), targetHistory(targetAlphaApp, targetAlpha3),
				targetHistory(targetBetaApp, targetBeta1), targetHistory(targetGammaApp, targetGamma1),
			},
			map[string][]targetPostgresValue{targetGamma1Table: {{id: identifier, value: "unrelated PostgreSQL zero sentinel"}}},
		)
		seedEpoch := targetPostgresEpoch(t, seedState)

		targetResetMarker(t, marker)
		before := targetCapturePostgres(t, databaseURL, schema)
		plan := project.run(t, environment,
			"migrate", targetAlphaApp, "zero", "--plan", "--project", project.descriptor)
		targetAssertSuccess(t, plan, targetPlanOutput(t,
			targetPlanRow{App: targetAlphaApp, Name: targetAlpha3, Direction: "backward"},
			targetPlanRow{App: targetAlphaApp, Name: targetAlpha2, Direction: "backward"},
			targetPlanRow{App: targetBetaApp, Name: targetBeta1, Direction: "backward"},
			targetPlanRow{App: targetAlphaApp, Name: targetAlpha1, Direction: "backward"},
		), sensitive...)
		targetAssertPostgresUnchanged(t, databaseURL, schema, before)
		targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetEventSessionClose, targetEventBackendClose,
		)

		targetResetMarker(t, marker)
		execute := project.run(t, environment,
			"migrate", targetAlphaApp, "zero", "--project", project.descriptor)
		targetAssertExecuteSuccess(t, execute, 5, sensitive...)
		after := targetAssertPostgresState(t, databaseURL, schema, 9,
			[]string{targetGamma1Table},
			[]targetSQLiteHistoryRow{targetHistory(targetGammaApp, targetGamma1)},
			map[string][]targetPostgresValue{targetGamma1Table: {{id: identifier, value: "unrelated PostgreSQL zero sentinel"}}},
		)
		if got := targetPostgresEpoch(t, after); got != seedEpoch {
			t.Fatalf("PostgreSQL migration epoch changed across app zero: got %q want %q", got, seedEpoch)
		}
		markers := targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetBeginEvent(targetAlphaApp, targetAlpha3, "backward"), targetDeleteEvent(targetAlpha3Table), targetRecordUnappliedEvent(targetAlphaApp, targetAlpha3), targetCommitEvent(targetAlphaApp, targetAlpha3, "backward"),
			targetBeginEvent(targetAlphaApp, targetAlpha2, "backward"), targetDeleteEvent(targetAlpha2Table), targetRecordUnappliedEvent(targetAlphaApp, targetAlpha2), targetCommitEvent(targetAlphaApp, targetAlpha2, "backward"),
			targetBeginEvent(targetBetaApp, targetBeta1, "backward"), targetDeleteEvent(targetBeta1Table), targetRecordUnappliedEvent(targetBetaApp, targetBeta1), targetCommitEvent(targetBetaApp, targetBeta1, "backward"),
			targetBeginEvent(targetAlphaApp, targetAlpha1, "backward"), targetDeleteEvent(targetAlpha1Table), targetRecordUnappliedEvent(targetAlphaApp, targetAlpha1), targetCommitEvent(targetAlphaApp, targetAlpha1, "backward"),
			targetEventSessionClose, targetEventBackendClose,
		)
		wantOrder := []string{
			targetBeginEvent(targetAlphaApp, targetAlpha3, "backward"),
			targetBeginEvent(targetAlphaApp, targetAlpha2, "backward"),
			targetBeginEvent(targetBetaApp, targetBeta1, "backward"),
			targetBeginEvent(targetAlphaApp, targetAlpha1, "backward"),
		}
		if got := targetBeginEvents(targetMarkerEventNames(markers)); !reflect.DeepEqual(got, wantOrder) {
			t.Fatalf("PostgreSQL app-zero begin order = %v, want DEV-0002 %v", got, wantOrder)
		}
	})

	t.Run("MIG-124_125_plan_read_only_and_fresh_history_replan", func(t *testing.T) {
		schema := targetCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "preview-drift")
		environment := project.postgresEnvironment(t, databaseURL, schema, marker, targetCatalogBlog)
		sensitive := targetPostgresSensitive(t, project, databaseURL, schema)
		seed := project.run(t, environment,
			"migrate", targetBlogApp, targetBlog1, "--project", project.descriptor)
		targetAssertExecuteSuccess(t, seed, 2, sensitive...)
		identifier := targetInsertPostgresValue(t, databaseURL, schema, targetBlog1Table, "PostgreSQL preview drift sentinel")
		seedState := targetAssertPostgresState(t, databaseURL, schema, 1,
			[]string{targetBlog1Table},
			[]targetSQLiteHistoryRow{targetHistory(targetBlogApp, targetBlog1)},
			map[string][]targetPostgresValue{targetBlog1Table: {{id: identifier, value: "PostgreSQL preview drift sentinel"}}},
		)
		seedEpoch := targetPostgresEpoch(t, seedState)

		targetResetMarker(t, marker)
		preview := project.run(t, environment,
			"migrate", targetBlogApp, targetBlog2, "--plan", "--project", project.descriptor)
		targetAssertSuccess(t, preview, targetPlanOutput(t,
			targetPlanRow{App: targetBlogApp, Name: targetBlog2, Direction: "forward"},
		), sensitive...)
		targetAssertPostgresUnchanged(t, databaseURL, schema, seedState)
		previewMarkers := targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetEventSessionClose, targetEventBackendClose,
		)

		targetResetMarker(t, marker)
		drift := project.run(t, environment,
			"migrate", targetBlogApp, targetBlog2, "--project", project.descriptor)
		targetAssertExecuteSuccess(t, drift, 2, sensitive...)
		afterDrift := targetAssertPostgresState(t, databaseURL, schema, 2,
			[]string{targetBlog1Table, targetBlog2Table},
			[]targetSQLiteHistoryRow{targetHistory(targetBlogApp, targetBlog1), targetHistory(targetBlogApp, targetBlog2)},
			map[string][]targetPostgresValue{targetBlog1Table: {{id: identifier, value: "PostgreSQL preview drift sentinel"}}},
		)
		if got := targetPostgresEpoch(t, afterDrift); got != seedEpoch {
			t.Fatalf("PostgreSQL migration epoch changed across preview drift: got %q want %q", got, seedEpoch)
		}

		targetResetMarker(t, marker)
		fresh := project.run(t, environment,
			"migrate", targetBlogApp, targetBlog2, "--project", project.descriptor)
		targetAssertExecuteSuccess(t, fresh, 2, sensitive...)
		targetAssertPostgresUnchanged(t, databaseURL, schema, afterDrift)
		freshMarkers := targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetEventSessionClose, targetEventBackendClose,
		)
		if targetSingleMarkerPID(t, previewMarkers) == targetSingleMarkerPID(t, freshMarkers) {
			t.Fatal("PostgreSQL preview and fresh execute reused the linked child process")
		}
	})

	t.Run("MIG-126_reverse_middle_failure_and_fresh_resume", func(t *testing.T) {
		schema := targetCreatePostgresSchema(t, databaseURL)
		marker := project.postgresMarker(t, "reverse-middle-failure")
		environment := project.postgresEnvironment(t, databaseURL, schema, marker, targetCatalogReverseFailure)
		sensitive := targetPostgresSensitive(t, project, databaseURL, schema)
		seed := project.run(t, environment, "migrate", "--project", project.descriptor)
		targetAssertExecuteSuccess(t, seed, 4, sensitive...)
		identifier := targetInsertPostgresValue(t, databaseURL, schema, targetFailure1Table, "PostgreSQL reverse resume sentinel")
		seedState := targetAssertPostgresState(t, databaseURL, schema, 4,
			[]string{targetFailure1Table, targetFailure2Table, targetFailure3FirstTable, targetFailure3SecondTable, targetFailure4Table},
			[]targetSQLiteHistoryRow{
				targetHistory(targetFailureApp, targetFailure1), targetHistory(targetFailureApp, targetFailure2),
				targetHistory(targetFailureApp, targetFailure3), targetHistory(targetFailureApp, targetFailure4),
			},
			map[string][]targetPostgresValue{targetFailure1Table: {{id: identifier, value: "PostgreSQL reverse resume sentinel"}}},
		)
		seedEpoch := targetPostgresEpoch(t, seedState)

		targetResetMarker(t, marker)
		preview := project.run(t, environment,
			"migrate", targetFailureApp, targetFailure1, "--plan", "--project", project.descriptor)
		targetAssertSuccess(t, preview, targetPlanOutput(t,
			targetPlanRow{App: targetFailureApp, Name: targetFailure4, Direction: "backward"},
			targetPlanRow{App: targetFailureApp, Name: targetFailure3, Direction: "backward"},
			targetPlanRow{App: targetFailureApp, Name: targetFailure2, Direction: "backward"},
		), sensitive...)
		targetAssertPostgresUnchanged(t, databaseURL, schema, seedState)
		targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetEventSessionClose, targetEventBackendClose,
		)

		targetResetMarker(t, marker)
		failingEnvironment := project.postgresEnvironmentWith(t, databaseURL, schema, marker, targetCatalogReverseFailure, map[string]string{
			targetFailDeleteTableEnvironment: targetFailure3FirstTable,
		})
		failed := project.run(t, failingEnvironment,
			"migrate", targetFailureApp, targetFailure1, "--project", project.descriptor)
		targetAssertFailure(t, failed, 3, "migration_execution_error/operation_failed\n", sensitive...)
		failedMarkers := targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetBeginEvent(targetFailureApp, targetFailure4, "backward"), targetDeleteEvent(targetFailure4Table), targetRecordUnappliedEvent(targetFailureApp, targetFailure4), targetCommitEvent(targetFailureApp, targetFailure4, "backward"),
			targetBeginEvent(targetFailureApp, targetFailure3, "backward"), targetDeleteEvent(targetFailure3SecondTable), targetDeleteEvent(targetFailure3FirstTable), targetRollbackEvent(targetFailureApp, targetFailure3, "backward"),
			targetEventSessionClose, targetEventBackendClose,
		)
		failedState := targetAssertPostgresState(t, databaseURL, schema, 5,
			[]string{targetFailure1Table, targetFailure2Table, targetFailure3FirstTable, targetFailure3SecondTable},
			[]targetSQLiteHistoryRow{
				targetHistory(targetFailureApp, targetFailure1), targetHistory(targetFailureApp, targetFailure2), targetHistory(targetFailureApp, targetFailure3),
			},
			map[string][]targetPostgresValue{targetFailure1Table: {{id: identifier, value: "PostgreSQL reverse resume sentinel"}}},
		)
		if got := targetPostgresEpoch(t, failedState); got != seedEpoch {
			t.Fatalf("PostgreSQL migration epoch changed across failed reverse: got %q want %q", got, seedEpoch)
		}

		targetResetMarker(t, marker)
		resumed := project.run(t, environment,
			"migrate", targetFailureApp, targetFailure1, "--project", project.descriptor)
		targetAssertExecuteSuccess(t, resumed, 4, sensitive...)
		resumeMarkers := targetAssertMarkerEvents(t, marker,
			targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
			targetBeginEvent(targetFailureApp, targetFailure3, "backward"), targetDeleteEvent(targetFailure3SecondTable), targetDeleteEvent(targetFailure3FirstTable), targetRecordUnappliedEvent(targetFailureApp, targetFailure3), targetCommitEvent(targetFailureApp, targetFailure3, "backward"),
			targetBeginEvent(targetFailureApp, targetFailure2, "backward"), targetDeleteEvent(targetFailure2Table), targetRecordUnappliedEvent(targetFailureApp, targetFailure2), targetCommitEvent(targetFailureApp, targetFailure2, "backward"),
			targetEventSessionClose, targetEventBackendClose,
		)
		after := targetAssertPostgresState(t, databaseURL, schema, 7,
			[]string{targetFailure1Table},
			[]targetSQLiteHistoryRow{targetHistory(targetFailureApp, targetFailure1)},
			map[string][]targetPostgresValue{targetFailure1Table: {{id: identifier, value: "PostgreSQL reverse resume sentinel"}}},
		)
		if got := targetPostgresEpoch(t, after); got != seedEpoch {
			t.Fatalf("PostgreSQL migration epoch changed across reverse resume: got %q want %q", got, seedEpoch)
		}
		if targetSingleMarkerPID(t, failedMarkers) == targetSingleMarkerPID(t, resumeMarkers) {
			t.Fatal("PostgreSQL reverse resume reused the failed linked child process")
		}
	})

	t.Run("MIG-128_external_ownership_cleanup_and_redaction", func(t *testing.T) {
		t.Run("load_before_open", func(t *testing.T) {
			schema := targetCreatePostgresSchema(t, databaseURL)
			marker := project.postgresMarker(t, "invalid-catalog")
			environment := project.postgresEnvironment(t, databaseURL, schema, marker, targetCatalogInvalid)
			sensitive := targetPostgresSensitive(t, project, databaseURL, schema)
			before := targetCapturePostgres(t, databaseURL, schema)
			result := project.run(t, environment, "migrate", "--plan", "--project", project.descriptor)
			targetAssertFailure(t, result, 1, "migration_definition_source_error/invalid_definition_document\n", sensitive...)
			targetAssertPostgresUnchanged(t, databaseURL, schema, before)
			if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("PostgreSQL backend marker exists before definition load: %v", err)
			}
		})

		t.Run("outer_close_discards_plan", func(t *testing.T) {
			schema := targetCreatePostgresSchema(t, databaseURL)
			marker := project.postgresMarker(t, "outer-close")
			environment := project.postgresEnvironmentWith(t, databaseURL, schema, marker, targetCatalogBlog, map[string]string{
				targetFailBackendCloseEnvironment: "1",
			})
			sensitive := targetPostgresSensitive(t, project, databaseURL, schema)
			before := targetCapturePostgres(t, databaseURL, schema)
			result := project.run(t, environment, "migrate", "--plan", "--project", project.descriptor)
			targetAssertFailure(t, result, 3, "migration_backend_error/backend_close_failed\n", sensitive...)
			targetAssertPostgresUnchanged(t, databaseURL, schema, before)
			targetAssertMarkerEvents(t, marker,
				targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
				targetEventSessionClose, targetEventBackendClose,
			)
		})

		t.Run("backend_open_raw_cause_redacted", func(t *testing.T) {
			schema := targetCreatePostgresSchema(t, databaseURL)
			marker := project.postgresMarker(t, "open-redaction")
			environment := project.postgresEnvironmentWith(t, databaseURL, schema, marker, targetCatalogBlog, map[string]string{
				targetFailBackendOpenEnvironment: "1",
			})
			sensitive := targetPostgresSensitive(t, project, databaseURL, schema)
			before := targetCapturePostgres(t, databaseURL, schema)
			result := project.run(t, environment, "migrate", "--plan", "--project", project.descriptor)
			targetAssertFailure(t, result, 3, "migration_backend_error/backend_open_failed\n", sensitive...)
			targetAssertPostgresUnchanged(t, databaseURL, schema, before)
			targetAssertMarkerEvents(t, marker, targetEventBackendOpen)
		})
	})
}
