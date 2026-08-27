//go:build darwin || linux

package projectmigrateproduct_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const fullConcurrencyBarrierEnv = "GODJ_MIGRATION_CONCURRENCY_BARRIER"

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
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			t.Fatalf("unexpected snapshot barrier entry %q", entry.Name())
		}
		if entry.Name() == "winner-lock" || entry.Name() == "contender-observed" || strings.HasPrefix(entry.Name(), "attempts-") {
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
)

const barrierEnvironment = "GODJ_MIGRATION_CONCURRENCY_BARRIER"

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

func main() {
	err := godjproject.Run(
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
		os.Stdin,
		os.Stdout,
	)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		os.Exit(1)
	}
}
`
