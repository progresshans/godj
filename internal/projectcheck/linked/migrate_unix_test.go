//go:build darwin || linux

package linked

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestRunMigrateAndCheckLoadTheIdenticalStaticAndFileCatalog(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	static := definition.Source{
		SourceID: "framework/system-state.godj.json",
		Document: migrationDocument("alpha", "0001_initial", nil),
	}
	writeFile(
		t,
		filepath.Join(root, "migrations", "0002_article.godj.json"),
		migrationDocument("alpha", "0002_article", [][2]string{{"alpha", "0001_initial"}}),
	)

	checkResponse, checkReport, err := invokeCheckWithSources(t, root, []string{"migrations"}, []definition.Source{static})
	if err != nil || !checkResponse.OK {
		t.Fatalf("check catalog = %+v, %+v, %v", checkResponse, checkReport, err)
	}

	database := newMigrationBackend()
	migrateResponse, migrateReport, output, err := invokeMigrate(
		root,
		[]string{"migrations"},
		[]definition.Source{static},
		func(context.Context) (MigrationBackend, error) { return database, nil },
		migrateprotocol.RequestDocument(),
		new(bytes.Buffer),
	)
	if err != nil || !migrateResponse.OK {
		t.Fatalf("migrate catalog = %+v, %+v, %q, %v", migrateResponse, migrateReport, output, err)
	}
	wantExecute := migrateprotocol.ExecuteResult{
		SourceCount:         checkResponse.Result.SourceCount,
		DefinitionCount:     checkResponse.Result.DefinitionCount,
		DefinitionSetDigest: checkResponse.Result.DefinitionSetDigest,
	}
	if migrateResponse.Result.Mode != migrateprotocol.ModeExecute || migrateResponse.Result.Execute != wantExecute || migrateResponse.Result.Plan != nil {
		t.Fatalf("check/migrate catalog mismatch: check=%+v migrate=%+v", checkResponse.Result, migrateResponse.Result)
	}
	if migrateReport.LoadCalls != 1 || migrateReport.SourceReads != 1 || migrateReport.DocumentsReceived != 2 ||
		migrateReport.BackendOpenCalls != 1 || migrateReport.GoDjDBCalls != 1 || migrateReport.RevisionLifecycleCalls != 1 || migrateReport.BackendCloseCalls != 1 ||
		database.openSessionCalls != 1 || database.session.readCalls != 1 || database.session.beginCalls != 2 ||
		database.session.closeCalls != 1 || database.closeCalls != 1 {
		t.Fatalf("migrate report/backend = %+v, %+v", migrateReport, database)
	}
	if got := database.session.transitions; len(got) != 2 || got[0].Migration.App != "alpha" || got[0].Migration.Name != "0001_initial" || got[1].Migration.Name != "0002_article" {
		t.Fatalf("latest transitions = %+v", got)
	}
}

func TestRunMigrateV2MapsNamedExecuteTarget(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	sources := []definition.Source{
		{SourceID: "alpha/0001.godj.json", Document: migrationDocument("alpha", "0001", nil)},
		{SourceID: "alpha/0002.godj.json", Document: migrationDocument("alpha", "0002", [][2]string{{"alpha", "0001"}})},
	}
	database := newMigrationBackend()
	request := mustMigrateRequest(t, migrateprotocol.Request{
		Mode: migrateprotocol.ModeExecute,
		Target: migrateprotocol.Target{
			Kind: migrateprotocol.TargetNamed,
			App:  "alpha",
			Name: "0001",
		},
	})

	response, report, _, err := invokeMigrate(
		root,
		nil,
		sources,
		func(context.Context) (MigrationBackend, error) { return database, nil },
		request,
		new(bytes.Buffer),
	)
	if err != nil || !response.OK || response.Result.Mode != migrateprotocol.ModeExecute || response.Result.Plan != nil {
		t.Fatalf("named execute response = %+v, report=%+v, err=%v", response, report, err)
	}
	if got := database.session.transitions; len(got) != 1 || got[0] != (backend.HistoryTransition{
		Migration: backend.AppliedMigration{App: "alpha", Name: "0001"},
		Kind:      backend.HistoryTransitionApply,
	}) {
		t.Fatalf("named execute transitions = %+v", got)
	}
	if report.RevisionLifecycleCalls != 1 || report.BackendOpenCalls != 1 || report.BackendCloseCalls != 1 ||
		database.openSessionCalls != 1 || database.session.readCalls != 1 || database.session.beginCalls != 1 ||
		database.session.closeCalls != 1 || database.closeCalls != 1 {
		t.Fatalf("named execute ownership = report %+v backend %+v", report, database)
	}
}

func TestRunMigrateV2PlansLatestNamedAndZeroWithoutBeginning(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	sources := []definition.Source{
		{SourceID: "alpha/0001.godj.json", Document: migrationDocument("alpha", "0001", nil)},
		{SourceID: "alpha/0002.godj.json", Document: migrationDocument("alpha", "0002", [][2]string{{"alpha", "0001"}})},
		{SourceID: "beta/0001.godj.json", Document: migrationDocument("beta", "0001", [][2]string{{"alpha", "0002"}})},
	}
	allApplied := []backend.AppliedMigration{
		{App: "alpha", Name: "0001"},
		{App: "alpha", Name: "0002"},
		{App: "beta", Name: "0001"},
	}
	tests := []struct {
		name    string
		target  migrateprotocol.Target
		applied []backend.AppliedMigration
		want    []migrateprotocol.PlanRow
	}{
		{
			name:   "latest",
			target: migrateprotocol.Target{Kind: migrateprotocol.TargetLatest},
			want: []migrateprotocol.PlanRow{
				{App: "alpha", Name: "0001", Direction: migrateprotocol.DirectionForward},
				{App: "alpha", Name: "0002", Direction: migrateprotocol.DirectionForward},
				{App: "beta", Name: "0001", Direction: migrateprotocol.DirectionForward},
			},
		},
		{
			name:    "named retains target",
			target:  migrateprotocol.Target{Kind: migrateprotocol.TargetNamed, App: "alpha", Name: "0001"},
			applied: allApplied,
			want: []migrateprotocol.PlanRow{
				{App: "beta", Name: "0001", Direction: migrateprotocol.DirectionBackward},
				{App: "alpha", Name: "0002", Direction: migrateprotocol.DirectionBackward},
			},
		},
		{
			name:    "known app zero",
			target:  migrateprotocol.Target{Kind: migrateprotocol.TargetZero, App: "alpha"},
			applied: allApplied,
			want: []migrateprotocol.PlanRow{
				{App: "beta", Name: "0001", Direction: migrateprotocol.DirectionBackward},
				{App: "alpha", Name: "0002", Direction: migrateprotocol.DirectionBackward},
				{App: "alpha", Name: "0001", Direction: migrateprotocol.DirectionBackward},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := newMigrationBackend()
			database.session.records = append([]backend.AppliedMigration(nil), test.applied...)
			request := mustMigrateRequest(t, migrateprotocol.Request{Mode: migrateprotocol.ModePlan, Target: test.target})

			response, report, _, err := invokeMigrate(
				root,
				nil,
				sources,
				func(context.Context) (MigrationBackend, error) { return database, nil },
				request,
				new(bytes.Buffer),
			)
			if err != nil || !response.OK || response.Result.Mode != migrateprotocol.ModePlan ||
				response.Result.Execute != (migrateprotocol.ExecuteResult{}) || !reflect.DeepEqual(response.Result.Plan, test.want) {
				t.Fatalf("plan response = %+v, want %+v, report=%+v, err=%v", response, test.want, report, err)
			}
			if report.RevisionLifecycleCalls != 1 || report.BackendOpenCalls != 1 || report.BackendCloseCalls != 1 ||
				database.openSessionCalls != 1 || database.session.readCalls != 1 || database.session.beginCalls != 0 ||
				database.session.closeCalls != 1 || database.closeCalls != 1 || len(database.session.transitions) != 0 {
				t.Fatalf("plan ownership = report %+v backend %+v", report, database)
			}
		})
	}
}

func TestRunMigrateV2KnownAppZeroRejectsUnknownApp(t *testing.T) {
	t.Parallel()
	database := newMigrationBackend()
	request := mustMigrateRequest(t, migrateprotocol.Request{
		Mode:   migrateprotocol.ModePlan,
		Target: migrateprotocol.Target{Kind: migrateprotocol.TargetZero, App: "unknown"},
	})
	response, report, _, err := invokeMigrate(
		newProjectRoot(t),
		nil,
		[]definition.Source{{SourceID: "alpha/0001.godj.json", Document: migrationDocument("alpha", "0001", nil)}},
		func(context.Context) (MigrationBackend, error) { return database, nil },
		request,
		new(bytes.Buffer),
	)
	if err != nil || response.OK || response.Failure != (migrateprotocol.Failure{
		Category: migrateprotocol.CategoryPlan,
		Code:     string(migrations.CodeTargetNotFound),
	}) || response.Result.Mode != "" || response.Result.Plan != nil || response.Result.Execute != (migrateprotocol.ExecuteResult{}) {
		t.Fatalf("unknown zero response = %+v, report=%+v, err=%v", response, report, err)
	}
	if report.RevisionLifecycleCalls != 1 || report.BackendOpenCalls != 1 || report.BackendCloseCalls != 1 ||
		database.openSessionCalls != 1 || database.session.readCalls != 1 || database.session.beginCalls != 0 ||
		database.session.closeCalls != 1 || database.closeCalls != 1 {
		t.Fatalf("unknown zero ownership = report %+v backend %+v", report, database)
	}
}

func TestRunMigrateV2PlanCloseFailuresDiscardResult(t *testing.T) {
	t.Parallel()
	sessionClose := errors.New("session close secret")
	outerClose := errors.New("outer close secret")
	tests := []struct {
		name            string
		sessionCloseErr error
		outerCloseErr   error
		want            migrateprotocol.Failure
	}{
		{
			name:            "session close",
			sessionCloseErr: sessionClose,
			want: migrateprotocol.Failure{
				Category: migrateprotocol.CategoryTransaction,
				Code:     string(migrations.CodeSessionCloseFailed),
			},
		},
		{
			name:          "outer close",
			outerCloseErr: outerClose,
			want: migrateprotocol.Failure{
				Category:      migrateprotocol.CategoryBackend,
				Code:          migrateprotocol.CodeBackendCloseFailed,
				CleanupFailed: true,
			},
		},
		{
			name:            "session and outer close",
			sessionCloseErr: sessionClose,
			outerCloseErr:   outerClose,
			want: migrateprotocol.Failure{
				Category:      migrateprotocol.CategoryTransaction,
				Code:          string(migrations.CodeSessionCloseFailed),
				CleanupFailed: true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := newMigrationBackend()
			database.session.closeErr = test.sessionCloseErr
			database.closeErr = test.outerCloseErr
			request := mustMigrateRequest(t, migrateprotocol.Request{
				Mode:   migrateprotocol.ModePlan,
				Target: migrateprotocol.Target{Kind: migrateprotocol.TargetLatest},
			})
			response, report, output, err := invokeMigrate(
				newProjectRoot(t),
				nil,
				[]definition.Source{{SourceID: "alpha/0001.godj.json", Document: migrationDocument("alpha", "0001", nil)}},
				func(context.Context) (MigrationBackend, error) { return database, nil },
				request,
				new(bytes.Buffer),
			)
			if err != nil || response.OK || response.Failure != test.want || response.Result.Mode != "" ||
				response.Result.Plan != nil || response.Result.Execute != (migrateprotocol.ExecuteResult{}) {
				t.Fatalf("plan close response = %+v, want %+v, report=%+v, err=%v", response, test.want, report, err)
			}
			if report.RevisionLifecycleCalls != 1 || report.BackendOpenCalls != 1 || report.BackendCloseCalls != 1 ||
				database.openSessionCalls != 1 || database.session.readCalls != 1 || database.session.beginCalls != 0 ||
				database.session.closeCalls != 1 || database.closeCalls != 1 {
				t.Fatalf("plan close ownership = report %+v backend %+v", report, database)
			}
			if bytes.Contains(output, []byte("secret")) {
				t.Fatalf("plan close cause leaked in %q", output)
			}
		})
	}
}

func TestRunMigrateDefinitionFailurePrecedesBackendOpen(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	openCalls := 0
	response, report, output, err := invokeMigrate(
		root,
		nil,
		[]definition.Source{{SourceID: "broken", Document: []byte(`{"secret":"do-not-publish"}`)}},
		func(context.Context) (MigrationBackend, error) {
			openCalls++
			return newMigrationBackend(), nil
		},
		migrateprotocol.RequestDocument(),
		new(bytes.Buffer),
	)
	if err != nil || response.Failure.Category != migrateprotocol.CategorySource || openCalls != 0 || report.BackendOpenCalls != 0 || report.LoadCalls != 1 {
		t.Fatalf("load-before-open = %+v, %+v, open=%d, %v", response, report, openCalls, err)
	}
	if bytes.Contains(output, []byte("do-not-publish")) || bytes.Contains(output, []byte("secret")) {
		t.Fatalf("definition secret leaked in %q", output)
	}
}

func TestRunMigrateRequestFailurePrecedesCatalogAndBackend(t *testing.T) {
	t.Parallel()
	openCalls := 0
	response, report, _, err := invokeMigrate(
		filepath.Join(t.TempDir(), "missing"),
		[]string{"missing"},
		[]definition.Source{{SourceID: "broken", Document: []byte(`{"broken":true}`)}},
		func(context.Context) (MigrationBackend, error) {
			openCalls++
			return newMigrationBackend(), nil
		},
		[]byte(`{"protocol_version":1,"command":"migrations.migrate"}`),
		new(bytes.Buffer),
	)
	if err != nil || response.Failure != (migrateprotocol.Failure{
		Category: migrateprotocol.CategoryProtocol,
		Code:     migrateprotocol.CodeProtocolIncompatible,
	}) || report.CommandDispatches != 0 || report.RootsOpened != 0 || report.LoadCalls != 0 || report.BackendOpenCalls != 0 || openCalls != 0 {
		t.Fatalf("request precedence = %+v, %+v, open=%d, %v", response, report, openCalls, err)
	}
}

func TestRunMigrateOpenerAndOuterCloseOwnership(t *testing.T) {
	t.Parallel()
	openSecret := errors.New("postgres://user:password@example.invalid/private")
	closeSecret := errors.New("close secret password")
	typedNil := (*migrationTestBackend)(nil)
	tests := []struct {
		name          string
		opener        func(context.Context) (MigrationBackend, error)
		wantCode      string
		wantCleanup   bool
		wantOpenCalls int
		backend       *migrationTestBackend
	}{
		{name: "nil opener", wantCode: migrateprotocol.CodeInvalidBackend},
		{
			name: "typed nil",
			opener: func(context.Context) (MigrationBackend, error) {
				return typedNil, nil
			},
			wantCode:      migrateprotocol.CodeInvalidBackend,
			wantOpenCalls: 1,
		},
		{
			name: "nil with error",
			opener: func(context.Context) (MigrationBackend, error) {
				return nil, openSecret
			},
			wantCode:      migrateprotocol.CodeBackendOpenFailed,
			wantOpenCalls: 1,
		},
		{
			name:          "partial acquisition",
			backend:       newMigrationBackend(),
			wantCode:      migrateprotocol.CodeBackendOpenFailed,
			wantOpenCalls: 1,
		},
		{
			name:          "partial acquisition close failure",
			backend:       &migrationTestBackend{session: newMigrationSession(), closeErr: closeSecret},
			wantCode:      migrateprotocol.CodeBackendOpenFailed,
			wantCleanup:   true,
			wantOpenCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opener := test.opener
			if test.backend != nil {
				opener = func(context.Context) (MigrationBackend, error) { return test.backend, openSecret }
			}
			response, report, output, err := invokeMigrate(
				newProjectRoot(t), nil, nil, opener, migrateprotocol.RequestDocument(), new(bytes.Buffer),
			)
			if err != nil || response.Failure != (migrateprotocol.Failure{
				Category:      migrateprotocol.CategoryBackend,
				Code:          test.wantCode,
				CleanupFailed: test.wantCleanup,
			}) {
				t.Fatalf("opener response = %+v report=%+v err=%v", response, report, err)
			}
			if report.BackendOpenCalls != test.wantOpenCalls || report.RevisionLifecycleCalls != 0 {
				t.Fatalf("opener report = %+v", report)
			}
			wantClose := 0
			if test.backend != nil {
				wantClose = 1
			}
			if report.BackendCloseCalls != wantClose || test.backend != nil && test.backend.closeCalls != 1 {
				t.Fatalf("close ownership = report %+v backend %+v", report, test.backend)
			}
			if bytes.Contains(output, []byte("password")) || bytes.Contains(output, []byte("postgres://")) {
				t.Fatalf("opener/close cause leaked in %q", output)
			}
		})
	}
}

func TestRunMigratePreservesPrimaryAndMarksOuterCleanupFailure(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	database := newMigrationBackend()
	database.session.readErr = errors.New("reader contains database secret")
	database.closeErr = errors.New("closer contains database secret")
	response, report, output, err := invokeMigrate(
		root, nil, nil,
		func(context.Context) (MigrationBackend, error) { return database, nil },
		migrateprotocol.RequestDocument(),
		new(bytes.Buffer),
	)
	if err != nil || response.Failure != (migrateprotocol.Failure{
		Category:      migrateprotocol.CategoryRecorder,
		Code:          string(migrations.CodeReadFailed),
		CleanupFailed: true,
	}) {
		t.Fatalf("primary+cleanup = %+v, %+v, %v", response, report, err)
	}
	if report.RevisionLifecycleCalls != 1 || report.BackendCloseCalls != 1 || database.session.closeCalls != 1 || database.closeCalls != 1 {
		t.Fatalf("primary+cleanup ownership = %+v, %+v", report, database)
	}
	if bytes.Contains(output, []byte("secret")) {
		t.Fatalf("core/close cause leaked in %q", output)
	}

	database = newMigrationBackend()
	database.closeErr = errors.New("outer close")
	response, report, _, err = invokeMigrate(
		root, nil, nil,
		func(context.Context) (MigrationBackend, error) { return database, nil },
		migrateprotocol.RequestDocument(),
		new(bytes.Buffer),
	)
	if err != nil || response.Failure != (migrateprotocol.Failure{
		Category:      migrateprotocol.CategoryBackend,
		Code:          migrateprotocol.CodeBackendCloseFailed,
		CleanupFailed: true,
	}) || report.BackendCloseCalls != 1 {
		t.Fatalf("close-only failure = %+v, %+v, %v", response, report, err)
	}
}

func TestRunMigrateCompletedDatabaseOutcomeWinsResponseBarrierCancellation(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	database := newMigrationBackend()
	var output bytes.Buffer
	report, err := runMigrate(
		ctx,
		MigrateConfig{
			ProjectRoot: root,
			OpenMigrationBackend: func(context.Context) (MigrationBackend, error) {
				return database, nil
			},
		},
		[]string{migrateprotocol.PrivateArgument},
		bytes.NewReader(migrateprotocol.RequestDocument()),
		&output,
		systemDependencies{beforeResponseWrite: cancel},
	)
	if err != nil || !errors.Is(ctx.Err(), context.Canceled) || report.RunnerResponseWrites != 1 || report.BackendCloseCalls != 1 {
		t.Fatalf("completed outcome cancellation = report %+v, ctx %v, err %v", report, ctx.Err(), err)
	}
	response, parseFailure, parseFailed := migrateprotocol.ParseResponse(output.Bytes(), true)
	if parseFailed || parseFailure != (migrateprotocol.Failure{}) || !response.OK {
		t.Fatalf("completed outcome response = %+v, %+v, %v", response, parseFailure, parseFailed)
	}

	ctx, cancel = context.WithCancel(context.Background())
	output.Reset()
	report, err = runMigrate(
		ctx,
		MigrateConfig{ProjectRoot: root},
		[]string{migrateprotocol.PrivateArgument},
		bytes.NewReader([]byte(`{"protocol_version":2,"command":"migrations.migrate"}`)),
		&output,
		systemDependencies{beforeResponseWrite: cancel},
	)
	if !errors.Is(err, context.Canceled) || output.Len() != 0 || report.RunnerResponseWrites != 0 || report.BackendOpenCalls != 0 {
		t.Fatalf("pre-acquisition cancellation = report %+v, output %q, err %v", report, output.Bytes(), err)
	}
}

func TestMigrationFailureClassificationPrecedence(t *testing.T) {
	t.Parallel()
	primary := &migrations.Error{Category: migrations.CategoryExecution, Code: migrations.CodeOperationFailed}
	spoofedCause := &migrations.Error{
		Category: migrations.CategoryExecution,
		Code:     migrations.CodeOperationFailed,
		Cause:    &migrations.Error{Category: migrations.CategoryTransaction, Code: migrations.CodeCommitOutcomeUnknown},
	}
	sessionClose := &migrations.Error{Category: migrations.CategoryTransaction, Code: migrations.CodeSessionCloseFailed}
	commitCleanup := &migrations.Error{Category: migrations.CategoryTransaction, Code: migrations.CodeCommitCleanupFailed}
	rollback := &migrations.Error{Category: migrations.CategoryExecution, Code: migrations.CodeOperationFailed, RollbackCause: errors.New("rollback")}
	unknown := &migrations.Error{Category: migrations.CategoryTransaction, Code: migrations.CodeCommitOutcomeUnknown}
	tests := []struct {
		name string
		err  error
		want migrateprotocol.Failure
	}{
		{name: "commit unknown", err: errors.Join(primary, sessionClose, commitCleanup, rollback, unknown), want: migrateprotocol.Failure{Category: migrateprotocol.CategoryTransaction, Code: string(migrations.CodeCommitOutcomeUnknown)}},
		{name: "rollback", err: errors.Join(primary, sessionClose, commitCleanup, rollback), want: migrateprotocol.Failure{Category: migrateprotocol.CategoryTransaction, Code: migrateprotocol.CodeRollbackFailed}},
		{name: "commit cleanup", err: errors.Join(primary, sessionClose, commitCleanup), want: migrateprotocol.Failure{Category: migrateprotocol.CategoryTransaction, Code: string(migrations.CodeCommitCleanupFailed)}},
		{name: "session close", err: errors.Join(primary, sessionClose), want: migrateprotocol.Failure{Category: migrateprotocol.CategoryTransaction, Code: string(migrations.CodeSessionCloseFailed)}},
		{name: "primary", err: primary, want: migrateprotocol.Failure{Category: string(primary.Category), Code: string(primary.Code)}},
		{name: "raw cause cannot spoof priority", err: spoofedCause, want: migrateprotocol.Failure{Category: string(spoofedCause.Category), Code: string(spoofedCause.Code)}},
		{name: "planning", err: &migrations.PlanningError{Category: migrations.CategoryGraph, Code: migrations.CodeDependencyCycle}, want: migrateprotocol.Failure{Category: migrateprotocol.CategoryGraph, Code: string(migrations.CodeDependencyCycle)}},
		{name: "recorder", err: &migrations.RecorderError{Category: migrations.CategoryRecorder, Code: migrations.CodeReadFailed}, want: migrateprotocol.Failure{Category: migrateprotocol.CategoryRecorder, Code: string(migrations.CodeReadFailed)}},
		{name: "unknown", err: errors.New("raw secret"), want: migrateprotocol.Failure{Category: migrateprotocol.CategoryInternal, Code: migrateprotocol.CodeProjectInternalError}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyMigrationFailure(test.err)
			if got != test.want || !migrateprotocol.IsLinkedFailure(got) {
				t.Fatalf("classify = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestRunMigrateOwnsInputsAndRejectsTransportEdges(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	sources := []definition.Source{{SourceID: "static.godj.json", Document: migrationDocument("alpha", "0001", nil)}}
	argv := []string{migrateprotocol.PrivateArgument}
	reader := newBlockingReader(migrateprotocol.RequestDocument())
	database := newMigrationBackend()
	var output bytes.Buffer
	done := make(chan struct{})
	var report Report
	var runErr error
	go func() {
		report, runErr = RunMigrate(context.Background(), MigrateConfig{
			ProjectRoot:                root,
			MigrationDefinitionSources: sources,
			OpenMigrationBackend: func(context.Context) (MigrationBackend, error) {
				return database, nil
			},
		}, argv, reader, &output)
		close(done)
	}()
	<-reader.started
	sources[0].SourceID = "mutated"
	sources[0].Document[0] = 'x'
	argv[0] = "mutated"
	close(reader.release)
	<-done
	response, parseFailure, parseFailed := migrateprotocol.ParseResponse(output.Bytes(), true)
	if runErr != nil || parseFailed || parseFailure != (migrateprotocol.Failure{}) || !response.OK || response.Result.Mode != migrateprotocol.ModeExecute || response.Result.Execute.SourceCount != 1 || report.LoadCalls != 1 {
		t.Fatalf("owned inputs = %+v, %+v, %+v, %v", response, parseFailure, report, runErr)
	}

	if _, err := RunMigrate(nil, MigrateConfig{}, []string{migrateprotocol.PrivateArgument}, bytes.NewReader(migrateprotocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("nil context succeeded")
	}
	if _, err := RunMigrate(context.Background(), MigrateConfig{}, []string{migrateprotocol.PrivateArgument}, nil, io.Discard); err == nil {
		t.Fatal("nil stdin succeeded")
	}
	if _, err := RunMigrate(context.Background(), MigrateConfig{}, []string{migrateprotocol.PrivateArgument}, bytes.NewReader(migrateprotocol.RequestDocument()), nil); err == nil {
		t.Fatal("nil stdout succeeded")
	}
	if _, err := RunMigrate(context.Background(), MigrateConfig{}, []string{"wrong"}, bytes.NewReader(migrateprotocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("wrong argv succeeded")
	}
	shortReport, err := RunMigrate(context.Background(), MigrateConfig{}, []string{migrateprotocol.PrivateArgument}, bytes.NewReader(migrateprotocol.RequestDocument()), shortWriter{})
	if !errors.Is(err, io.ErrShortWrite) || shortReport.RunnerResponseWrites != 1 || shortReport.BackendOpenCalls != 0 {
		t.Fatalf("short write = %+v, %v", shortReport, err)
	}
}

func TestOldCheckProtocolBytesAndDatabaseFreeBoundaryRemainExact(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	openCalls := 0
	var output bytes.Buffer
	report, err := Run(
		context.Background(),
		Config{
			ProjectRoot: root,
			MigrationDefinitionSources: []definition.Source{{
				SourceID: "static.godj.json",
				Document: migrationDocument("alpha", "0001", nil),
			}},
		},
		[]string{protocol.PrivateArgument},
		bytes.NewReader(protocol.RequestDocument()),
		&output,
	)
	if err != nil || openCalls != 0 || report.BackendOpenCalls != 0 || report.RevisionLifecycleCalls != 0 {
		t.Fatalf("check database boundary = %+v open=%d err=%v", report, openCalls, err)
	}
	want, err := protocol.EncodeResponse(protocol.Response{OK: true, Result: protocol.Result{
		SourceCount:         1,
		DefinitionCount:     1,
		DefinitionSetDigest: mustLoadDigest(t, []definition.Source{{SourceID: "static.godj.json", Document: migrationDocument("alpha", "0001", nil)}}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("check bytes = %q, want %q", output.Bytes(), want)
	}
}

func invokeCheckWithSources(t *testing.T, root string, roots []string, sources []definition.Source) (protocol.Response, Report, error) {
	t.Helper()
	var output bytes.Buffer
	report, err := Run(context.Background(), Config{
		ProjectRoot:                root,
		MigrationDefinitionRoots:   roots,
		MigrationDefinitionSources: sources,
	}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), &output)
	if err != nil {
		return protocol.Response{}, report, err
	}
	response, parseFailure, parseFailed := protocol.ParseResponse(output.Bytes(), true)
	if parseFailed {
		t.Fatalf("invalid check response %q: %+v", output.Bytes(), parseFailure)
	}
	return response, report, nil
}

func invokeMigrate(
	root string,
	roots []string,
	sources []definition.Source,
	opener func(context.Context) (MigrationBackend, error),
	request []byte,
	writer io.Writer,
) (migrateprotocol.Response, Report, []byte, error) {
	buffer, _ := writer.(*bytes.Buffer)
	report, err := RunMigrate(context.Background(), MigrateConfig{
		ProjectRoot:                root,
		MigrationDefinitionRoots:   roots,
		MigrationDefinitionSources: sources,
		OpenMigrationBackend:       opener,
	}, []string{migrateprotocol.PrivateArgument}, bytes.NewReader(request), writer)
	if err != nil {
		return migrateprotocol.Response{}, report, nil, err
	}
	if buffer == nil {
		return migrateprotocol.Response{}, report, nil, nil
	}
	document := append([]byte(nil), buffer.Bytes()...)
	response, _, failed := migrateprotocol.ParseResponse(document, true)
	if failed {
		return migrateprotocol.Response{}, report, document, errors.New("linked wrote an invalid migrate response")
	}
	return response, report, document, nil
}

func mustMigrateRequest(t *testing.T, request migrateprotocol.Request) []byte {
	t.Helper()
	document, err := migrateprotocol.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func mustLoadDigest(t *testing.T, sources []definition.Source) string {
	t.Helper()
	set, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return set.Digest()
}

type migrationTestBackend struct {
	session          *migrationTestSession
	openSessionCalls int
	closeCalls       int
	openSessionErr   error
	closeErr         error
}

func newMigrationBackend() *migrationTestBackend {
	return &migrationTestBackend{session: newMigrationSession()}
}

func (*migrationTestBackend) MigrationCapabilities() backend.MigrationCapabilities {
	return backend.MigrationCapabilities{}
}

func (value *migrationTestBackend) OpenRevisionFencedSession(context.Context) (backend.RevisionFencedSession, error) {
	value.openSessionCalls++
	return value.session, value.openSessionErr
}

func (value *migrationTestBackend) Close() error {
	value.closeCalls++
	return value.closeErr
}

type migrationTestSession struct {
	readCalls   int
	beginCalls  int
	closeCalls  int
	readErr     error
	closeErr    error
	records     []backend.AppliedMigration
	transitions []backend.HistoryTransition
}

func newMigrationSession() *migrationTestSession { return new(migrationTestSession) }

func (value *migrationTestSession) ReadAppliedMigrations(context.Context) ([]backend.AppliedMigration, error) {
	value.readCalls++
	return append([]backend.AppliedMigration(nil), value.records...), value.readErr
}

func (value *migrationTestSession) BeginMigration(
	_ context.Context,
	transition backend.HistoryTransition,
	_ backend.MigrationIntent,
) (backend.RevisionFencedTransaction, error) {
	value.beginCalls++
	value.transitions = append(value.transitions, transition)
	return &migrationTestTransaction{transition: transition}, nil
}

func (value *migrationTestSession) Close(context.Context) error {
	value.closeCalls++
	return value.closeErr
}

type migrationTestTransaction struct {
	transition backend.HistoryTransition
}

func (*migrationTestTransaction) CreateModel(context.Context, ir.Model) error        { return nil }
func (*migrationTestTransaction) DeleteModel(context.Context, ir.Model) error        { return nil }
func (*migrationTestTransaction) AddField(context.Context, ir.Model, ir.Field) error { return nil }
func (*migrationTestTransaction) RemoveField(context.Context, ir.Model, ir.Field) error {
	return nil
}
func (*migrationTestTransaction) RecordApplied(context.Context, string, string) error   { return nil }
func (*migrationTestTransaction) RecordUnapplied(context.Context, string, string) error { return nil }
func (*migrationTestTransaction) CommitFenced(context.Context) (backend.CommitOutcome, error) {
	return backend.CommitOutcome{Durability: backend.CommitCommitted}, nil
}
func (*migrationTestTransaction) Rollback(context.Context) error { return nil }

var _ MigrationBackend = (*migrationTestBackend)(nil)
var _ backend.RevisionFencedSession = (*migrationTestSession)(nil)
var _ backend.RevisionFencedTransaction = (*migrationTestTransaction)(nil)
