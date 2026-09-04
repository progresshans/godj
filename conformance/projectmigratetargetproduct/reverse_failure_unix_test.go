//go:build darwin || linux

package projectmigratetargetproduct_test

import (
	"reflect"
	"testing"
)

func targetAssertReverseMiddleFailureResume(t *testing.T, project *targetExternalProject) {
	t.Helper()
	database, marker := project.paths(t, "reverse-middle-failure")
	environment := project.environment(database, marker, targetCatalogReverseFailure)
	seed := project.run(t, environment, "migrate", "--project", project.descriptor)
	targetAssertExecuteSuccess(t, seed, 4, project.sensitive(database)...)
	targetInsertSQLiteValue(t, database, targetFailure1Table, "reverse resume sentinel")
	targetAssertSQLiteHistory(t, database,
		targetHistory(targetFailureApp, targetFailure1),
		targetHistory(targetFailureApp, targetFailure2),
		targetHistory(targetFailureApp, targetFailure3),
		targetHistory(targetFailureApp, targetFailure4),
	)
	targetAssertSQLiteRevision(t, database, 4)
	seedEpoch := targetSQLiteEpoch(t, database)

	targetResetMarker(t, marker)
	beforePlan := targetCaptureSQLite(t, database)
	plan := project.run(t, environment,
		"migrate", targetFailureApp, targetFailure1, "--plan", "--project", project.descriptor)
	targetAssertSuccess(t, plan, targetPlanOutput(t,
		targetPlanRow{App: targetFailureApp, Name: targetFailure4, Direction: "backward"},
		targetPlanRow{App: targetFailureApp, Name: targetFailure3, Direction: "backward"},
		targetPlanRow{App: targetFailureApp, Name: targetFailure2, Direction: "backward"},
	), project.sensitive(database)...)
	targetAssertSQLiteUnchanged(t, database, beforePlan)
	targetAssertMarkerEvents(t, marker,
		targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
		targetEventSessionClose, targetEventBackendClose,
	)

	targetResetMarker(t, marker)
	failingEnvironment := project.environmentWith(database, marker, targetCatalogReverseFailure, map[string]string{
		targetFailDeleteTableEnvironment: targetFailure3FirstTable,
	})
	failed := project.run(t, failingEnvironment,
		"migrate", targetFailureApp, targetFailure1, "--project", project.descriptor)
	targetAssertFailure(t, failed, 3, "migration_execution_error/operation_failed\n", project.sensitive(database)...)
	firstMarkers := targetAssertMarkerEvents(t, marker,
		targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
		targetBeginEvent(targetFailureApp, targetFailure4, "backward"),
		targetDeleteEvent(targetFailure4Table),
		targetRecordUnappliedEvent(targetFailureApp, targetFailure4),
		targetCommitEvent(targetFailureApp, targetFailure4, "backward"),
		targetBeginEvent(targetFailureApp, targetFailure3, "backward"),
		targetDeleteEvent(targetFailure3SecondTable),
		targetDeleteEvent(targetFailure3FirstTable),
		targetRollbackEvent(targetFailureApp, targetFailure3, "backward"),
		targetEventSessionClose, targetEventBackendClose,
	)
	targetAssertSQLiteHistory(t, database,
		targetHistory(targetFailureApp, targetFailure1),
		targetHistory(targetFailureApp, targetFailure2),
		targetHistory(targetFailureApp, targetFailure3),
	)
	targetAssertSQLiteRevision(t, database, 5)
	targetAssertSQLiteEpoch(t, database, seedEpoch)
	targetAssertSQLiteTables(t, database,
		targetFailure1Table,
		targetFailure2Table,
		targetFailure3FirstTable,
		targetFailure3SecondTable,
	)
	if got := targetSQLiteValues(t, database, targetFailure1Table); !reflect.DeepEqual(got, []string{"reverse resume sentinel"}) {
		t.Fatalf("failed reverse sentinel values = %v", got)
	}
	firstPID := targetSingleMarkerPID(t, firstMarkers)

	targetResetMarker(t, marker)
	resumed := project.run(t, environment,
		"migrate", targetFailureApp, targetFailure1, "--project", project.descriptor)
	targetAssertExecuteSuccess(t, resumed, 4, project.sensitive(database)...)
	resumeMarkers := targetAssertMarkerEvents(t, marker,
		targetEventBackendOpen, targetEventSessionOpen, targetEventHistoryRead,
		targetBeginEvent(targetFailureApp, targetFailure3, "backward"),
		targetDeleteEvent(targetFailure3SecondTable),
		targetDeleteEvent(targetFailure3FirstTable),
		targetRecordUnappliedEvent(targetFailureApp, targetFailure3),
		targetCommitEvent(targetFailureApp, targetFailure3, "backward"),
		targetBeginEvent(targetFailureApp, targetFailure2, "backward"),
		targetDeleteEvent(targetFailure2Table),
		targetRecordUnappliedEvent(targetFailureApp, targetFailure2),
		targetCommitEvent(targetFailureApp, targetFailure2, "backward"),
		targetEventSessionClose, targetEventBackendClose,
	)
	resumePID := targetSingleMarkerPID(t, resumeMarkers)
	if firstPID == resumePID {
		t.Fatalf("reverse resume reused child pid %d; want fresh project-linked process", firstPID)
	}
	targetAssertSQLiteHistory(t, database, targetHistory(targetFailureApp, targetFailure1))
	targetAssertSQLiteRevision(t, database, 7)
	targetAssertSQLiteEpoch(t, database, seedEpoch)
	targetAssertSQLiteTables(t, database, targetFailure1Table)
	if got := targetSQLiteValues(t, database, targetFailure1Table); !reflect.DeepEqual(got, []string{"reverse resume sentinel"}) {
		t.Fatalf("resumed reverse sentinel values = %v", got)
	}
}

func targetSingleMarkerPID(t *testing.T, markers []targetMarker) int {
	t.Helper()
	if len(markers) == 0 {
		t.Fatal("migration marker set is empty")
	}
	pid := markers[0].pid
	if pid <= 0 {
		t.Fatalf("invalid migration marker pid %d", pid)
	}
	for _, marker := range markers[1:] {
		if marker.pid != pid {
			t.Fatalf("one global command used multiple project child pids: %d and %d", pid, marker.pid)
		}
	}
	return pid
}
