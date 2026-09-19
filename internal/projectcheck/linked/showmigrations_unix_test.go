//go:build darwin || linux

package linked

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
)

func TestRunShowMigrationsLoadsBeforeOneReadOnlySessionAndListsUnknownApps(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	sources := []definition.Source{
		{SourceID: "alpha-0002", Document: migrationDocument("alpha", "0002", [][2]string{{"alpha", "0001"}})},
		{SourceID: "beta-0001", Document: migrationDocument("beta", "0001", [][2]string{{"alpha", "0001"}})},
		{SourceID: "alpha-0001", Document: migrationDocument("alpha", "0001", nil)},
	}
	database := newShowMigrationsBackend([]backend.AppliedMigration{
		{App: "legacy", Name: "0009"},
		{App: "alpha", Name: "0001"},
	})
	response, report, document, err := invokeShowMigrations(
		root,
		nil,
		sources,
		func(context.Context) (MigrationBackend, error) { return database, nil },
		showmigrationsprotocol.RequestDocument(),
		new(bytes.Buffer),
	)
	if err != nil || !response.OK {
		t.Fatalf("showmigrations = %+v report=%+v wire=%q err=%v", response, report, document, err)
	}
	want := []showmigrationsprotocol.Row{
		{App: "alpha", Name: "0001", Status: showmigrationsprotocol.StatusApplied},
		{App: "alpha", Name: "0002", Status: showmigrationsprotocol.StatusUnapplied},
		{App: "beta", Name: "0001", Status: showmigrationsprotocol.StatusUnapplied},
		{App: "legacy", Name: "0009", Status: showmigrationsprotocol.StatusUnknown},
	}
	if !reflect.DeepEqual(response.Result.Rows, want) {
		t.Fatalf("rows = %+v, want %+v", response.Result.Rows, want)
	}
	if report.LoadCalls != 1 || report.DocumentsReceived != 3 || report.BackendOpenCalls != 1 ||
		report.RevisionSessionOpens != 1 || report.AppliedHistoryReads != 1 || report.DirectPlannerCalls != 1 ||
		report.RevisionSessionCloses != 1 || report.BackendCloseCalls != 1 || report.RevisionLifecycleCalls != 0 ||
		report.GoDjDBCalls != 0 || database.openSessionCalls != 1 || database.session.readCalls != 1 ||
		database.session.beginCalls != 0 || database.session.closeCalls != 1 || database.closeCalls != 1 {
		t.Fatalf("report/backend = %+v / %+v", report, database)
	}
}

func TestRunShowMigrationsRequestAndDefinitionFailuresPrecedeBackendOpen(t *testing.T) {
	t.Parallel()
	openCalls := 0
	opener := func(context.Context) (MigrationBackend, error) {
		openCalls++
		return newShowMigrationsBackend(nil), nil
	}
	response, report, _, err := invokeShowMigrations(
		filepath.Join(t.TempDir(), "missing"),
		[]string{"missing"},
		[]definition.Source{{SourceID: "secret", Document: []byte(`{"password":"do-not-publish"}`)}},
		opener,
		[]byte(`{"protocol_version":2,"command":"migrations.show"}`),
		new(bytes.Buffer),
	)
	if err != nil || response.Failure != (showmigrationsprotocol.Failure{
		Category: showmigrationsprotocol.CategoryProtocol,
		Code:     showmigrationsprotocol.CodeProtocolIncompatible,
	}) || report.CommandDispatches != 0 || report.LoadCalls != 0 || report.BackendOpenCalls != 0 || openCalls != 0 {
		t.Fatalf("request precedence = %+v report=%+v open=%d err=%v", response, report, openCalls, err)
	}

	response, report, document, err := invokeShowMigrations(
		newProjectRoot(t),
		nil,
		[]definition.Source{{SourceID: "secret", Document: []byte(`{"password":"do-not-publish"}`)}},
		opener,
		showmigrationsprotocol.RequestDocument(),
		new(bytes.Buffer),
	)
	if err != nil || response.Failure.Category != showmigrationsprotocol.CategorySource ||
		report.LoadCalls != 1 || report.BackendOpenCalls != 0 || openCalls != 0 ||
		bytes.Contains(document, []byte("password")) || bytes.Contains(document, []byte("do-not-publish")) {
		t.Fatalf("definition precedence = %+v report=%+v wire=%q open=%d err=%v", response, report, document, openCalls, err)
	}
}

func TestRunShowMigrationsCancellationBoundaries(t *testing.T) {
	t.Run("pre-canceled before request and backend", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		openCalls := 0
		var output bytes.Buffer
		report, err := RunShowMigrations(
			ctx,
			ShowMigrationsConfig{
				ProjectRoot: newProjectRoot(t),
				OpenMigrationBackend: func(context.Context) (MigrationBackend, error) {
					openCalls++
					return newShowMigrationsBackend(nil), nil
				},
			},
			[]string{showmigrationsprotocol.PrivateArgument},
			bytes.NewReader(showmigrationsprotocol.RequestDocument()),
			&output,
		)
		if !errors.Is(err, context.Canceled) || report.CommandDispatches != 0 || report.LoadCalls != 0 ||
			report.BackendOpenCalls != 0 || report.RunnerResponseWrites != 0 || openCalls != 0 || output.Len() != 0 {
			t.Fatalf("pre-canceled report=%+v open=%d output=%q err=%v", report, openCalls, output.String(), err)
		}
	})

	t.Run("closed snapshot precedes response-boundary cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		database := newShowMigrationsBackend(nil)
		var output bytes.Buffer
		report, err := runShowMigrations(
			ctx,
			ShowMigrationsConfig{
				ProjectRoot:          newProjectRoot(t),
				OpenMigrationBackend: func(context.Context) (MigrationBackend, error) { return database, nil },
			},
			[]string{showmigrationsprotocol.PrivateArgument},
			bytes.NewReader(showmigrationsprotocol.RequestDocument()),
			&output,
			systemDependencies{beforeResponseWrite: cancel},
		)
		response, failure, failed := showmigrationsprotocol.ParseResponse(output.Bytes(), true)
		if err != nil || !errors.Is(ctx.Err(), context.Canceled) || failed || failure != (showmigrationsprotocol.Failure{}) ||
			!response.OK || len(response.Result.Rows) != 0 || report.RunnerResponseWrites != 1 ||
			report.RevisionSessionOpens != 1 || report.AppliedHistoryReads != 1 || report.RevisionSessionCloses != 1 ||
			report.BackendCloseCalls != 1 || database.session.closeCalls != 1 || database.closeCalls != 1 {
			t.Fatalf("completed snapshot response=%+v failure=%+v failed=%v report=%+v backend=%+v err=%v ctx=%v", response, failure, failed, report, database, err, ctx.Err())
		}
	})
}

func TestRunShowMigrationsFailsClosedForHistoryAndCleanup(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	sources := []definition.Source{
		{SourceID: "alpha-0001", Document: migrationDocument("alpha", "0001", nil)},
		{SourceID: "alpha-0002", Document: migrationDocument("alpha", "0002", [][2]string{{"alpha", "0001"}})},
	}

	t.Run("inconsistent known history", func(t *testing.T) {
		database := newShowMigrationsBackend([]backend.AppliedMigration{{App: "alpha", Name: "0002"}})
		response, report, _, err := invokeShowMigrations(
			root, nil, sources,
			func(context.Context) (MigrationBackend, error) { return database, nil },
			showmigrationsprotocol.RequestDocument(), new(bytes.Buffer),
		)
		if err != nil || response.Failure.Category != showmigrationsprotocol.CategoryHistory ||
			response.Failure.Code != "inconsistent_applied_history" || response.Failure.CleanupFailed ||
			report.AppliedHistoryReads != 1 || database.session.beginCalls != 0 ||
			database.session.closeCalls != 1 || database.closeCalls != 1 {
			t.Fatalf("inconsistent history = %+v report=%+v backend=%+v err=%v", response, report, database, err)
		}
	})

	t.Run("bounded history", func(t *testing.T) {
		records := make([]backend.AppliedMigration, showMigrationsHistoryRecordLimit+1)
		for index := range records {
			records[index] = backend.AppliedMigration{App: "legacy", Name: string(rune(index + 1))}
		}
		database := newShowMigrationsBackend(records)
		response, _, _, err := invokeShowMigrations(
			root, nil, sources,
			func(context.Context) (MigrationBackend, error) { return database, nil },
			showmigrationsprotocol.RequestDocument(), new(bytes.Buffer),
		)
		if err != nil || response.Failure.Category != showmigrationsprotocol.CategoryHistory ||
			response.Failure.Code != "history_revision_integrity" || database.session.beginCalls != 0 ||
			database.session.closeCalls != 1 || database.closeCalls != 1 {
			t.Fatalf("bounded history = %+v backend=%+v err=%v", response, database, err)
		}
	})

	t.Run("successful read plus close failure", func(t *testing.T) {
		database := newShowMigrationsBackend(nil)
		database.session.closeErr = errors.New("private session close secret")
		response, _, document, err := invokeShowMigrations(
			root, nil, nil,
			func(context.Context) (MigrationBackend, error) { return database, nil },
			showmigrationsprotocol.RequestDocument(), new(bytes.Buffer),
		)
		if err != nil || response.Failure != (showmigrationsprotocol.Failure{
			Category:      showmigrationsprotocol.CategoryBackend,
			Code:          showmigrationsprotocol.CodeBackendCloseFailed,
			CleanupFailed: true,
		}) || bytes.Contains(document, []byte("secret")) || database.session.closeCalls != 1 || database.closeCalls != 1 {
			t.Fatalf("close failure = %+v wire=%q backend=%+v err=%v", response, document, database, err)
		}
	})
}

func TestRunShowMigrationsPartialAcquisitionAndShortWire(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	private := errors.New("postgres://user:password@example.invalid/private")

	database := newShowMigrationsBackend(nil)
	response, report, document, err := invokeShowMigrations(
		root, nil, nil,
		func(context.Context) (MigrationBackend, error) { return database, private },
		showmigrationsprotocol.RequestDocument(), new(bytes.Buffer),
	)
	if err != nil || response.Failure.Category != showmigrationsprotocol.CategoryBackend ||
		response.Failure.Code != showmigrationsprotocol.CodeBackendOpenFailed || report.BackendCloseCalls != 1 ||
		database.closeCalls != 1 || bytes.Contains(document, []byte("password")) {
		t.Fatalf("partial backend = %+v report=%+v wire=%q backend=%+v err=%v", response, report, document, database, err)
	}

	database = newShowMigrationsBackend(nil)
	database.openSessionErr = private
	response, report, document, err = invokeShowMigrations(
		root, nil, nil,
		func(context.Context) (MigrationBackend, error) { return database, nil },
		showmigrationsprotocol.RequestDocument(), new(bytes.Buffer),
	)
	if err != nil || response.Failure.Category != showmigrationsprotocol.CategoryRecorder ||
		response.Failure.Code != "read_failed" || report.RevisionSessionCloses != 1 || report.BackendCloseCalls != 1 ||
		database.session.closeCalls != 1 || database.closeCalls != 1 || bytes.Contains(document, []byte("password")) {
		t.Fatalf("partial session = %+v report=%+v wire=%q backend=%+v err=%v", response, report, document, database, err)
	}

	short := &linkedShortWriter{}
	shortReport, shortErr := RunShowMigrations(
		context.Background(),
		ShowMigrationsConfig{ProjectRoot: root},
		[]string{showmigrationsprotocol.PrivateArgument},
		bytes.NewReader(showmigrationsprotocol.RequestDocument()),
		short,
	)
	if !errors.Is(shortErr, io.ErrShortWrite) || shortReport.RunnerResponseWrites != 1 || shortReport.BackendOpenCalls != 0 {
		t.Fatalf("short response = report=%+v err=%v", shortReport, shortErr)
	}
}

func TestRunShowMigrationsInvalidSessionRetainsOuterCloseFailure(t *testing.T) {
	t.Parallel()
	database := newShowMigrationsBackend(nil)
	database.session = nil
	database.closeErr = errors.New("private outer close failure")

	response, report, document, err := invokeShowMigrations(
		newProjectRoot(t), nil, nil,
		func(context.Context) (MigrationBackend, error) { return database, nil },
		showmigrationsprotocol.RequestDocument(), new(bytes.Buffer),
	)
	want := showmigrationsprotocol.Failure{
		Category:      showmigrationsprotocol.CategoryBackend,
		Code:          showmigrationsprotocol.CodeInvalidBackend,
		CleanupFailed: true,
	}
	if err != nil || response.Failure != want || report.RunnerResponseWrites != 1 ||
		report.RevisionSessionOpens != 1 || report.RevisionSessionCloses != 0 ||
		report.BackendCloseCalls != 1 || database.openSessionCalls != 1 || database.closeCalls != 1 ||
		bytes.Contains(document, []byte("private")) {
		t.Fatalf("response=%+v report=%+v wire=%q backend=%+v err=%v", response, report, document, database, err)
	}
}

func TestRunShowMigrationsPreservesRevisionFenceFailureKinds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		kind     backend.RevisionFenceFailureKind
		category string
		code     string
	}{
		{name: "adoption", kind: backend.RevisionFenceFailureAdoptionRequired, category: showmigrationsprotocol.CategoryCapability, code: "revision_fence_adoption_required"},
		{name: "stale", kind: backend.RevisionFenceFailureStale, category: showmigrationsprotocol.CategoryConflict, code: "stale_history_revision"},
		{name: "contended", kind: backend.RevisionFenceFailureContended, category: showmigrationsprotocol.CategoryTransaction, code: "history_revision_contended"},
		{name: "integrity", kind: backend.RevisionFenceFailureIntegrity, category: showmigrationsprotocol.CategoryHistory, code: "history_revision_integrity"},
		{name: "unknown", kind: backend.RevisionFenceFailureKind(255), category: showmigrationsprotocol.CategoryHistory, code: "history_revision_integrity"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			database := newShowMigrationsBackend(nil)
			database.session.readErr = &backend.RevisionFenceError{Kind: test.kind, Cause: errors.New("private")}
			response, _, _, err := invokeShowMigrations(
				newProjectRoot(t), nil, nil,
				func(context.Context) (MigrationBackend, error) { return database, nil },
				showmigrationsprotocol.RequestDocument(), new(bytes.Buffer),
			)
			if err != nil || response.Failure.Category != test.category || response.Failure.Code != test.code ||
				database.session.closeCalls != 1 || database.closeCalls != 1 {
				t.Fatalf("failure = %+v backend=%+v err=%v", response.Failure, database, err)
			}
		})
	}
}

func TestRunShowMigrationsPrevalidatesIdentityAndSuccessWireBeforeClose(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		records []backend.AppliedMigration
	}{
		{name: "invalid UTF-8", records: []backend.AppliedMigration{{App: "legacy", Name: string([]byte{0xff})}}},
		{name: "escaped wire expansion", records: []backend.AppliedMigration{
			{App: "legacy", Name: strings.Repeat("\x01", 800_000) + "a"},
			{App: "legacy", Name: strings.Repeat("\x01", 800_000) + "b"},
			{App: "legacy", Name: strings.Repeat("\x01", 800_000) + "c"},
			{App: "legacy", Name: strings.Repeat("\x01", 800_000) + "d"},
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			database := newShowMigrationsBackend(test.records)
			response, report, _, err := invokeShowMigrations(
				newProjectRoot(t), nil, nil,
				func(context.Context) (MigrationBackend, error) { return database, nil },
				showmigrationsprotocol.RequestDocument(), new(bytes.Buffer),
			)
			if err != nil || response.Failure.Category != showmigrationsprotocol.CategoryHistory ||
				response.Failure.Code != "history_revision_integrity" || report.RunnerResponseWrites != 1 ||
				database.session.closeCalls != 1 || database.closeCalls != 1 {
				t.Fatalf("response=%+v report=%+v backend=%+v err=%v", response, report, database, err)
			}
		})
	}
}

func invokeShowMigrations(
	root string,
	roots []string,
	sources []definition.Source,
	opener func(context.Context) (MigrationBackend, error),
	request []byte,
	writer io.Writer,
) (showmigrationsprotocol.Response, Report, []byte, error) {
	buffer, _ := writer.(*bytes.Buffer)
	report, err := RunShowMigrations(
		context.Background(),
		ShowMigrationsConfig{
			ProjectRoot: root, MigrationDefinitionRoots: roots,
			MigrationDefinitionSources: sources, OpenMigrationBackend: opener,
		},
		[]string{showmigrationsprotocol.PrivateArgument},
		bytes.NewReader(request), writer,
	)
	if err != nil {
		return showmigrationsprotocol.Response{}, report, nil, err
	}
	if buffer == nil {
		return showmigrationsprotocol.Response{}, report, nil, nil
	}
	document := append([]byte(nil), buffer.Bytes()...)
	response, _, failed := showmigrationsprotocol.ParseResponse(document, true)
	if failed {
		return showmigrationsprotocol.Response{}, report, document, errors.New("linked wrote an invalid showmigrations response")
	}
	return response, report, document, nil
}

type showMigrationsTestBackend struct {
	session          *showMigrationsTestSession
	openSessionCalls int
	closeCalls       int
	openSessionErr   error
	closeErr         error
}

func newShowMigrationsBackend(records []backend.AppliedMigration) *showMigrationsTestBackend {
	return &showMigrationsTestBackend{session: &showMigrationsTestSession{records: append([]backend.AppliedMigration(nil), records...)}}
}

func (*showMigrationsTestBackend) MigrationCapabilities() backend.MigrationCapabilities {
	return backend.MigrationCapabilities{}
}

func (value *showMigrationsTestBackend) OpenRevisionFencedSession(context.Context) (backend.RevisionFencedSession, error) {
	value.openSessionCalls++
	return value.session, value.openSessionErr
}

func (value *showMigrationsTestBackend) Close() error {
	value.closeCalls++
	return value.closeErr
}

type showMigrationsTestSession struct {
	records    []backend.AppliedMigration
	readCalls  int
	beginCalls int
	closeCalls int
	readErr    error
	closeErr   error
}

func (value *showMigrationsTestSession) ReadAppliedMigrations(context.Context) ([]backend.AppliedMigration, error) {
	value.readCalls++
	return append([]backend.AppliedMigration(nil), value.records...), value.readErr
}

func (value *showMigrationsTestSession) BeginMigration(context.Context, backend.HistoryTransition, backend.MigrationIntent) (backend.RevisionFencedTransaction, error) {
	value.beginCalls++
	return nil, errors.New("showmigrations must not begin a migration transaction")
}

func (value *showMigrationsTestSession) Close(context.Context) error {
	value.closeCalls++
	return value.closeErr
}

type linkedShortWriter struct{}

func (*linkedShortWriter) Write(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	return len(payload) - 1, nil
}

var _ MigrationBackend = (*showMigrationsTestBackend)(nil)
var _ backend.RevisionFencedSession = (*showMigrationsTestSession)(nil)
