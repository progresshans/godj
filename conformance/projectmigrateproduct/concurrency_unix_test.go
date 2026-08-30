//go:build darwin || linux

package projectmigrateproduct_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
)

const (
	fullConcurrencyBarrierEnv         = "GODJ_MIGRATION_CONCURRENCY_BARRIER"
	fullConcurrencyPrivateStderrProbe = "godj-private-stderr-fd2-probe-v1\n"
)

func TestGlobalMigrateArticleSQLiteFullMIG096Concurrency(t *testing.T) {
	repository := repositoryRoot(t)
	globalBinary := buildGlobalGodj(t, repository)
	expected := expectedArticleCatalog(t, repository)
	descriptor := fullConcurrencyProject(t, repository)

	databasePath := filepath.Join(t.TempDir(), "child-vs-child.sqlite3")
	databaseDSN := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc&_busy_timeout=250"
	barrierDirectory := filepath.Join(t.TempDir(), "snapshot-barrier")
	if err := os.Mkdir(barrierDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	workspaceBase := newWorkspaceBase(t)
	values := environmentMap(articleEnvironment(t, databaseDSN, workspaceBase))
	values[fullConcurrencyBarrierEnv] = barrierDirectory
	environment := sortedEnvironment(values)

	start := make(chan struct{})
	completed := make(chan fullConcurrencyExecution, 2)
	for child := 0; child < 2; child++ {
		go func() {
			<-start
			result, err := executeBounded(globalBinary, repository, environment, "migrate", "--project", descriptor)
			completed <- fullConcurrencyExecution{result: result, err: err}
		}()
	}
	close(start)

	executions := make([]fullConcurrencyExecution, 2)
	for index := range executions {
		executions[index] = <-completed
	}
	observed := make([]commandResult, len(executions))
	for index, execution := range executions {
		observed[index] = execution.result
	}
	for _, result := range observed {
		assertOutputSanitized(t, result, databasePath, databaseDSN, barrierDirectory, descriptor, filepath.Dir(descriptor))
	}
	for index, execution := range executions {
		if execution.err != nil {
			t.Fatalf("concurrent global migrate %d: %v", index+1, execution.err)
		}
	}
	assertWorkspaceEmpty(t, workspaceBase)

	markers := fullConcurrencyMarkers(t, barrierDirectory)
	if len(markers) != 2 {
		t.Fatalf("snapshot barrier markers = %+v with command results %+v, want two distinct project children", markers, observed)
	}
	if markers[0].PID == markers[1].PID || markers[0].ParentPID == markers[1].ParentPID {
		t.Fatalf("snapshot barrier ownership = %+v, want two runner PIDs owned by two global command PIDs", markers)
	}
	for index, marker := range markers {
		if marker.PID <= 0 || marker.ParentPID <= 0 || marker.PID == marker.ParentPID || marker.Records != 0 {
			t.Fatalf("snapshot barrier marker = %+v, want a distinct live child with the fresh empty history snapshot", marker)
		}
		beginCount := 1
		if index == 0 {
			beginCount = len(expected.History)
		}
		fullConcurrencyAssertSingleAttempt(t, barrierDirectory, marker.PID, beginCount)
		privateResponse := fullConcurrencyAssertPrivateWire(
			t,
			barrierDirectory,
			marker.PID,
			databasePath,
			databaseDSN,
			barrierDirectory,
			descriptor,
			filepath.Dir(descriptor),
		)
		if index == 0 {
			want := migrateprotocol.Response{
				OK: true,
				Result: migrateprotocol.Result{
					Mode: migrateprotocol.ModeExecute,
					Execute: migrateprotocol.ExecuteResult{
						SourceCount:         expected.Command.SourceCount,
						DefinitionCount:     expected.Command.DefinitionCount,
						DefinitionSetDigest: expected.Command.DefinitionSetDigest,
					},
				},
			}
			if !reflect.DeepEqual(privateResponse, want) {
				t.Fatalf("winner private response = %+v, want exact successful migration result", privateResponse)
			}
			continue
		}
		want := migrateprotocol.Response{Failure: migrateprotocol.Failure{
			Category: migrateprotocol.CategoryTransaction,
			Code:     "history_revision_contended",
		}}
		if !reflect.DeepEqual(privateResponse, want) {
			t.Fatalf("contender private response = %+v, want exact closed revision contention", privateResponse)
		}
	}
	winnerDocument := fullConcurrencyCoordinationMarker(t, barrierDirectory, "winner-lock")
	contenderDocument := fullConcurrencyCoordinationMarker(t, barrierDirectory, "contender-observed")
	if winnerDocument != fmt.Sprintf("pid=%d\n", markers[0].PID) ||
		contenderDocument != fmt.Sprintf("pid=%d\nstatus=contended\n", markers[1].PID) {
		t.Fatalf("transaction barrier ownership = winner:%q contender:%q markers:%+v", winnerDocument, contenderDocument, markers)
	}

	winners := 0
	fenced := 0
	for index, result := range observed {
		switch {
		case result.ExitCode == 0:
			assertMigrateSuccess(t, result, expected, databasePath, databaseDSN, barrierDirectory)
			winners++
		case result.ExitCode == 3 && result.Stdout == "" &&
			result.Stderr == "migration_transaction_error/history_revision_contended\n" &&
			!result.StdoutTruncated && !result.StderrTruncated:
			fenced++
		default:
			t.Fatalf("concurrent migrate %d = exit:%d stdout:%q stderr:%q truncated:%v/%v; all results=%+v, want one success and one closed contention fence", index+1, result.ExitCode, result.Stdout, result.Stderr, result.StdoutTruncated, result.StderrTruncated, observed)
		}
	}
	if winners != 1 || fenced != 1 {
		t.Fatalf("concurrent migrate outcomes = winners:%d fenced:%d, want exactly one of each", winners, fenced)
	}
	assertLatestDatabase(t, databasePath, expected, "")

	beforeReconciliation := digestFile(t, databasePath)
	reconciliationValues := environmentMap(environment)
	delete(reconciliationValues, fullConcurrencyBarrierEnv)
	reconciliationEnvironment := sortedEnvironment(reconciliationValues)
	reconciled := runMigrate(t, globalBinary, repository, descriptor, reconciliationEnvironment)
	assertMigrateSuccess(t, reconciled, expected, databasePath, databaseDSN, barrierDirectory)
	afterReconciliation := digestFile(t, databasePath)
	if beforeReconciliation != afterReconciliation {
		t.Fatalf("fresh reconciliation changed converged SQLite bytes: before=%x after=%x", beforeReconciliation, afterReconciliation)
	}
	assertWorkspaceEmpty(t, workspaceBase)
	assertLatestDatabase(t, databasePath, expected, "")
}

type fullConcurrencyExecution struct {
	result commandResult
	err    error
}

type fullConcurrencyMarker struct {
	PID       int
	ParentPID int
	Records   int
}

func fullConcurrencyProject(t *testing.T, repository string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	for _, directory := range []string{
		filepath.Join(root, "cmd", "projectrunner"),
		filepath.Join(root, "migrations"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	rootModule, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	const moduleDeclaration = "module github.com/progresshans/godj\n"
	if !strings.HasPrefix(string(rootModule), moduleDeclaration) || strings.Count(string(rootModule), moduleDeclaration) != 1 {
		t.Fatal("repository go.mod has an unexpected module declaration")
	}
	goMod := strings.Replace(string(rootModule), moduleDeclaration, "module example.com/godj-full-concurrency-fixture\n", 1)
	goMod += fmt.Sprintf("\nrequire github.com/progresshans/godj v0.0.0\n\nreplace github.com/progresshans/godj => %s\n", strconv.Quote(filepath.ToSlash(repository)))
	fullConcurrencyWrite(t, filepath.Join(root, "go.mod"), []byte(goMod))
	fullConcurrencyCopy(t, filepath.Join(repository, "go.sum"), filepath.Join(root, "go.sum"))
	fullConcurrencyWrite(t, filepath.Join(root, "godj.toml"), []byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\n"))
	fullConcurrencyWrite(t, filepath.Join(root, "cmd", "projectrunner", "main.go"), []byte(fullConcurrencyRunnerSource))
	fullConcurrencyCopy(
		t,
		filepath.Join(repository, "examples", "article", "migrations", "0001_initial.godj.json"),
		filepath.Join(root, "migrations", "0001_initial.godj.json"),
	)
	return filepath.Join(root, "godj.toml")
}

func fullConcurrencyWrite(t *testing.T, path string, document []byte) {
	t.Helper()
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fullConcurrencyCopy(t *testing.T, source, destination string) {
	t.Helper()
	document, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	fullConcurrencyWrite(t, destination, document)
}

func fullConcurrencyMarkers(t *testing.T, directory string) []fullConcurrencyMarker {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	markers := make([]fullConcurrencyMarker, 0, len(entries))
	artifacts := map[string]map[int]struct{}{
		"attempts-":         {},
		"private-request-":  {},
		"private-response-": {},
		"private-stderr-":   {},
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			t.Fatalf("unexpected snapshot barrier entry %q", entry.Name())
		}
		if entry.Name() == "winner-lock" || entry.Name() == "contender-observed" {
			continue
		}
		artifact := false
		for prefix, pids := range artifacts {
			if !strings.HasPrefix(entry.Name(), prefix) {
				continue
			}
			suffix := strings.TrimPrefix(entry.Name(), prefix)
			pid, parseErr := strconv.Atoi(suffix)
			if parseErr != nil || pid <= 0 || suffix != strconv.Itoa(pid) {
				t.Fatalf("snapshot barrier artifact name %q did not contain a canonical positive PID", entry.Name())
			}
			if _, duplicate := pids[pid]; duplicate {
				t.Fatalf("snapshot barrier artifact %q duplicated PID %d", prefix, pid)
			}
			pids[pid] = struct{}{}
			artifact = true
			break
		}
		if artifact {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "ready-") {
			t.Fatalf("unexpected snapshot barrier entry %q", entry.Name())
		}
		document, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(document))
		if len(fields) != 3 {
			t.Fatalf("snapshot barrier marker %q = %q", entry.Name(), document)
		}
		marker := fullConcurrencyMarker{
			PID:       fullConcurrencyMarkerInteger(t, fields[0], "pid"),
			ParentPID: fullConcurrencyMarkerInteger(t, fields[1], "ppid"),
			Records:   fullConcurrencyMarkerInteger(t, fields[2], "records"),
		}
		if entry.Name() != "ready-"+strconv.Itoa(marker.PID) {
			t.Fatalf("snapshot barrier marker filename %q does not bind PID %d", entry.Name(), marker.PID)
		}
		markers = append(markers, marker)
	}
	sort.Slice(markers, func(left, right int) bool { return markers[left].PID < markers[right].PID })
	markerPIDs := make(map[int]struct{}, len(markers))
	for _, marker := range markers {
		markerPIDs[marker.PID] = struct{}{}
	}
	for prefix, pids := range artifacts {
		if len(pids) != len(markerPIDs) {
			t.Fatalf("snapshot barrier artifact %q participant count = %d, want %d", prefix, len(pids), len(markerPIDs))
		}
		for pid := range pids {
			if _, exists := markerPIDs[pid]; !exists {
				t.Fatalf("snapshot barrier artifact %q retained unexpected child PID %d", prefix, pid)
			}
		}
	}
	return markers
}

func fullConcurrencyAssertSingleAttempt(t *testing.T, directory string, pid, beginCount int) {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(directory, "attempts-"+strconv.Itoa(pid)))
	if err != nil {
		t.Fatal("read concurrency attempt marker")
	}
	want := "session\nsnapshot\n" + strings.Repeat("begin\n", beginCount)
	if string(document) != want {
		t.Fatalf("concurrency child %d lifecycle attempts = %q, want one session, one snapshot, and %d migration begins with no retry", pid, document, beginCount)
	}
}

func fullConcurrencyAssertPrivateWire(
	t *testing.T,
	directory string,
	pid int,
	sensitive ...string,
) migrateprotocol.Response {
	t.Helper()
	request := fullConcurrencyReadPrivateCapture(t, directory, "request", pid)
	responseDocument := fullConcurrencyReadPrivateCapture(t, directory, "response", pid)
	stderr := fullConcurrencyReadPrivateCapture(t, directory, "stderr", pid)
	for sensitiveIndex, value := range sensitive {
		if value == "" {
			continue
		}
		for _, document := range [][]byte{request, responseDocument, stderr} {
			if bytes.Contains(document, []byte(value)) {
				t.Fatalf("private migration wire for child %d exposed sensitive value %d", pid, sensitiveIndex+1)
			}
		}
	}
	if !bytes.Equal(request, migrateprotocol.RequestDocument()) {
		t.Fatalf("private migration request for child %d was not the exact canonical request", pid)
	}
	if !bytes.Equal(stderr, []byte(fullConcurrencyPrivateStderrProbe)) {
		t.Fatalf("private migration stderr for child %d did not contain the exact direct-fd2 probe", pid)
	}
	response, failure, failed := migrateprotocol.ParseResponse(responseDocument, true)
	if failed || failure != (migrateprotocol.Failure{}) {
		t.Fatalf(
			"private migration response for child %d failed strict parsing with category=%q code=%q",
			pid,
			failure.Category,
			failure.Code,
		)
	}
	return response
}

func fullConcurrencyReadPrivateCapture(t *testing.T, directory, kind string, pid int) []byte {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(directory, "private-"+kind+"-"+strconv.Itoa(pid)))
	if err != nil {
		t.Fatalf("read private migration %s capture for child %d", kind, pid)
	}
	return document
}

func fullConcurrencyCoordinationMarker(t *testing.T, directory, name string) string {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatalf("read %s coordination marker: %v", name, err)
	}
	return string(document)
}

func fullConcurrencyMarkerInteger(t *testing.T, field, name string) int {
	t.Helper()
	key, value, ok := strings.Cut(field, "=")
	if !ok || key != name {
		t.Fatalf("snapshot barrier field %q, want %s=<integer>", field, name)
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("snapshot barrier field %q: %v", field, err)
	}
	return parsed
}

const fullConcurrencyRunnerSource = `package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/modeldef"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	godjproject "github.com/progresshans/godj/project"
	"github.com/progresshans/godj/systemstate"
	"golang.org/x/sys/unix"
)

const barrierEnvironment = "GODJ_MIGRATION_CONCURRENCY_BARRIER"
const privateStderrProbe = "godj-private-stderr-fd2-probe-v1\n"

type barrierBackend struct {
	godjproject.MigrationBackend
	directory string
}

func (value *barrierBackend) OpenRevisionFencedSession(ctx context.Context) (backend.RevisionFencedSession, error) {
	if err := appendAttempt(value.directory, os.Getpid(), "session"); err != nil {
		return nil, err
	}
	session, err := value.MigrationBackend.OpenRevisionFencedSession(ctx)
	if err != nil || session == nil {
		return session, err
	}
	return &barrierSession{RevisionFencedSession: session, directory: value.directory}, nil
}

type barrierSession struct {
	backend.RevisionFencedSession
	directory string
	pid       int
	winner    bool
}

func (value *barrierSession) ReadAppliedMigrations(ctx context.Context) ([]backend.AppliedMigration, error) {
	if err := appendAttempt(value.directory, os.Getpid(), "snapshot"); err != nil {
		return nil, err
	}
	records, err := value.RevisionFencedSession.ReadAppliedMigrations(ctx)
	if err != nil {
		return nil, err
	}
	if len(records) != 0 {
		return nil, errors.New("concurrency barrier requires a fresh history snapshot")
	}
	pid := os.Getpid()
	marker := filepath.Join(value.directory, fmt.Sprintf("ready-%d", pid))
	payload := []byte(fmt.Sprintf("pid=%d\nppid=%d\nrecords=%d\n", pid, os.Getppid(), len(records)))
	if err := os.WriteFile(marker, payload, 0o600); err != nil {
		return nil, errors.New("write concurrency barrier marker")
	}

	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		entries, err := os.ReadDir(value.directory)
		if err != nil {
			return nil, errors.New("read concurrency barrier")
		}
		readyPIDs := make([]int, 0, 2)
		for _, entry := range entries {
			if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "ready-") {
				readyPID, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), "ready-"))
				if err != nil || readyPID <= 0 {
					return nil, errors.New("concurrency barrier marker is invalid")
				}
				readyPIDs = append(readyPIDs, readyPID)
			}
		}
		if len(readyPIDs) == 2 {
			value.pid = pid
			value.winner = pid < readyPIDs[0] || pid < readyPIDs[1]
			return records, nil
		}
		if len(readyPIDs) > 2 {
			return nil, errors.New("concurrency barrier has excess participants")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, errors.New("concurrency barrier timed out")
		case <-ticker.C:
		}
	}
}

func (value *barrierSession) BeginMigration(
	ctx context.Context,
	transition backend.HistoryTransition,
	intent backend.MigrationIntent,
) (backend.RevisionFencedTransaction, error) {
	if err := appendAttempt(value.directory, os.Getpid(), "begin"); err != nil {
		return nil, err
	}
	if value.pid <= 0 {
		return nil, errors.New("concurrency snapshot barrier was not crossed")
	}
	winnerMarker := filepath.Join(value.directory, "winner-lock")
	contenderMarker := filepath.Join(value.directory, "contender-observed")
	if value.winner {
		transaction, err := value.RevisionFencedSession.BeginMigration(ctx, transition, intent)
		if err != nil || transaction == nil {
			return transaction, err
		}
		if err := os.WriteFile(winnerMarker, []byte(fmt.Sprintf("pid=%d\n", value.pid)), 0o600); err != nil {
			return transaction, errors.New("write winner lock marker")
		}
		if err := waitForBarrierMarker(ctx, contenderMarker); err != nil {
			return transaction, err
		}
		return transaction, nil
	}
	if err := waitForBarrierMarker(ctx, winnerMarker); err != nil {
		return nil, err
	}
	transaction, err := value.RevisionFencedSession.BeginMigration(ctx, transition, intent)
	status := "unexpected"
	var fence *backend.RevisionFenceError
	if errors.As(err, &fence) && fence != nil && fence.Kind == backend.RevisionFenceFailureContended {
		status = "contended"
	}
	payload := []byte(fmt.Sprintf("pid=%d\nstatus=%s\n", value.pid, status))
	if writeErr := os.WriteFile(contenderMarker, payload, 0o600); writeErr != nil {
		return transaction, errors.New("write contender observation marker")
	}
	if status != "contended" {
		return transaction, errors.New("contender did not observe a revision fence")
	}
	return transaction, err
}

func appendAttempt(directory string, pid int, stage string) error {
	path := filepath.Join(directory, fmt.Sprintf("attempts-%d", pid))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return errors.New("open concurrency attempt marker")
	}
	_, writeErr := file.WriteString(stage + "\n")
	return errors.Join(writeErr, file.Close())
}

func waitForBarrierMarker(ctx context.Context, path string) error {
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := os.Lstat(path)
		if err == nil {
			if !info.Mode().IsRegular() {
				return errors.New("concurrency coordination marker is not regular")
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("inspect concurrency coordination marker")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("concurrency coordination marker timed out")
		case <-ticker.C:
		}
	}
}

type privateProtocolCapture struct {
	request  *os.File
	response *os.File
	stderr   *os.File
}

type privateStderrMirror struct {
	public  io.Writer
	capture io.Writer
	err     error
}

func (value *privateStderrMirror) Write(payload []byte) (int, error) {
	publicWritten, publicErr := value.public.Write(payload)
	if publicWritten != len(payload) && publicErr == nil {
		publicErr = io.ErrShortWrite
	}
	captureWritten, captureErr := value.capture.Write(payload)
	if captureWritten != len(payload) && captureErr == nil {
		captureErr = io.ErrShortWrite
	}
	value.err = errors.Join(value.err, publicErr, captureErr)
	// Always drain fd 2 completely. Individual destination failures are
	// returned after the pipe reaches EOF so the child cannot deadlock while
	// reporting an error.
	return len(payload), nil
}

type privateStderrRedirect struct {
	targetFD int
	original *os.File
	done     <-chan error
}

func startPrivateStderrRedirect(capture *os.File) (*privateStderrRedirect, error) {
	if capture == nil {
		return nil, nil
	}
	targetFD := int(os.Stderr.Fd())
	originalFD, err := unix.Dup(targetFD)
	if err != nil {
		return nil, errors.New("duplicate private stderr")
	}
	original := os.NewFile(uintptr(originalFD), "private-stderr-original")
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = original.Close()
		return nil, errors.New("open private stderr pipe")
	}
	if err := unix.Dup2(int(writer.Fd()), targetFD); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		_ = original.Close()
		return nil, errors.New("redirect private stderr")
	}
	if err := writer.Close(); err != nil {
		restoreErr := unix.Dup2(int(original.Fd()), targetFD)
		if restoreErr != nil {
			restoreErr = errors.Join(restoreErr, unix.Close(targetFD))
		}
		_ = reader.Close()
		_ = original.Close()
		return nil, errors.Join(errors.New("close private stderr pipe writer"), restoreErr)
	}

	done := make(chan error, 1)
	go func() {
		mirror := &privateStderrMirror{public: original, capture: capture}
		_, copyErr := io.Copy(mirror, reader)
		done <- errors.Join(copyErr, mirror.err, reader.Close())
	}()
	return &privateStderrRedirect{targetFD: targetFD, original: original, done: done}, nil
}

func (value *privateStderrRedirect) stop() error {
	if value == nil {
		return nil
	}
	restoreErr := unix.Dup2(int(value.original.Fd()), value.targetFD)
	if restoreErr != nil {
		restoreErr = errors.Join(restoreErr, unix.Close(value.targetFD))
	}
	drainErr := <-value.done
	return errors.Join(restoreErr, drainErr, value.original.Close())
}

func openPrivateProtocolCapture(directory string, pid int) (*privateProtocolCapture, error) {
	if directory == "" || !filepath.IsAbs(directory) {
		return nil, nil
	}
	capture := new(privateProtocolCapture)
	open := func(kind string) (*os.File, error) {
		path := filepath.Join(directory, fmt.Sprintf("private-%s-%d", kind, pid))
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, errors.New("open private protocol capture")
		}
		return file, nil
	}
	var err error
	if capture.request, err = open("request"); err != nil {
		return nil, err
	}
	if capture.response, err = open("response"); err != nil {
		_ = capture.request.Close()
		return nil, err
	}
	if capture.stderr, err = open("stderr"); err != nil {
		_ = capture.request.Close()
		_ = capture.response.Close()
		return nil, err
	}
	return capture, nil
}

func (value *privateProtocolCapture) close() error {
	if value == nil {
		return nil
	}
	return errors.Join(value.request.Close(), value.response.Close(), value.stderr.Close())
}

func runMain() int {
	directory := strings.TrimSpace(os.Getenv(barrierEnvironment))
	capture, err := openPrivateProtocolCapture(directory, os.Getpid())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		return 1
	}
	var stderrCapture *os.File
	if capture != nil {
		stderrCapture = capture.stderr
	}
	stderrRedirect, err := startPrivateStderrRedirect(stderrCapture)
	if err != nil {
		_ = capture.close()
		_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		return 1
	}
	if capture != nil {
		written, writeErr := unix.Write(int(os.Stderr.Fd()), []byte(privateStderrProbe))
		if writeErr != nil || written != len(privateStderrProbe) {
			_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
			stderrErr := stderrRedirect.stop()
			closeErr := capture.close()
			if errors.Join(writeErr, stderrErr, closeErr) != nil {
				return 1
			}
			return 1
		}
	}
	request := io.Reader(os.Stdin)
	response := io.Writer(os.Stdout)
	if capture != nil {
		request = io.TeeReader(os.Stdin, capture.request)
		response = io.MultiWriter(capture.response, os.Stdout)
	}
	err = godjproject.Run(
		context.Background(),
		godjproject.Config{
			MigrationDefinitionRoots:   []string{"migrations"},
			MigrationDefinitionSources: []definition.Source{systemstate.InitialDefinitionSource()},
			LoadProjectSpec:            modeldef.ProjectSpec,
			OpenMigrationBackend: func(ctx context.Context) (godjproject.MigrationBackend, error) {
				directory := strings.TrimSpace(os.Getenv(barrierEnvironment))
				if directory != "" && !filepath.IsAbs(directory) {
					return nil, errors.New("concurrency barrier is invalid")
				}
				opened, err := databaseconfig.FromEnvironment(os.LookupEnv)
				if err != nil {
					return nil, err
				}
				migrationBackend, err := databaseconfig.Open(ctx, opened)
				if err != nil {
					return nil, err
				}
				if directory == "" {
					return migrationBackend, nil
				}
				return &barrierBackend{MigrationBackend: migrationBackend, directory: directory}, nil
			},
		},
		os.Args[1:],
		request,
		response,
	)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
	}
	stderrErr := stderrRedirect.stop()
	if closeErr := capture.close(); errors.Join(stderrErr, closeErr) != nil {
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

func main() {
	os.Exit(runMain())
}
`
