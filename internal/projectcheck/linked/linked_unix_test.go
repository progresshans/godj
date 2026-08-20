//go:build darwin || linux

package linked

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/migrations/definition"
)

func TestRunLoadsEmptyAndNonemptyCatalogs(t *testing.T) {
	t.Parallel()
	zeroRoot := newProjectRoot(t)
	response, report, err := invoke(t, zeroRoot, nil, protocol.RequestDocument(), nil)
	if err != nil || !response.OK || response.Result != (protocol.Result{DefinitionSetDigest: protocol.EmptySetDigest}) || report.LoadCalls != 1 || report.RootsOpened != 0 || report.PlannerConstruction != 1 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("zero-root Run = %+v, %+v, %v", response, report, err)
	}

	emptyRoot := newProjectRoot(t, "migrations")
	response, report, err = invoke(t, emptyRoot, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || !response.OK || response.Result != (protocol.Result{DefinitionSetDigest: protocol.EmptySetDigest}) {
		t.Fatalf("empty Run = %+v, %+v, %v", response, report, err)
	}
	wantEmpty := Report{
		RunnerResponseWrites:    1,
		LoadCalls:               1,
		PlannerConstruction:     1,
		DefinitionSetsPublished: 1,
		CommandDispatches:       1,
		RootsOpened:             1,
	}
	if !reflect.DeepEqual(report, wantEmpty) {
		t.Fatalf("empty report = %+v, want %+v", report, wantEmpty)
	}

	nonemptyRoot := newProjectRoot(t, "migrations")
	writeFile(t, filepath.Join(nonemptyRoot, "migrations", "0001_initial.godj.json"), migrationDocument("alpha", "0001_initial", nil))
	writeFile(t, filepath.Join(nonemptyRoot, "migrations", "notes.txt"), []byte("ignored"))
	response, report, err = invoke(t, nonemptyRoot, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || !response.OK || response.Result.SourceCount != 1 || response.Result.DefinitionCount != 1 || response.Result.DefinitionSetDigest == protocol.EmptySetDigest {
		t.Fatalf("nonempty Run = %+v, %+v, %v", response, report, err)
	}
	if report.RunnerResponseWrites != 1 || report.SourceReads != 1 || report.LoadCalls != 1 ||
		report.DocumentsReceived != 1 || report.HeadersValidated != 1 || report.OperationsDecoded != 0 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 ||
		report.CommandDispatches != 1 || report.RootsOpened != 1 || report.DirectoryEntriesSeen != 2 ||
		report.DirectPlannerCalls != 0 || report.GoDjDBCalls != 0 || report.RevisionLifecycleCalls != 0 || report.HasLoadFailure {
		t.Fatalf("nonempty report = %+v", report)
	}
}

func TestRunMapsProtocolDiscoveryAndDefinitionFailures(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")

	response, report, err := invoke(t, root, []string{"migrations"}, []byte(`{"protocol_version":2,"command":"migrations.check"}`), nil)
	if err != nil || response.Failure != (protocol.Failure{Category: protocol.CategoryProtocol, Code: protocol.CodeProjectProtocolIncompatible}) || report.RunnerResponseWrites != 1 || report.CommandDispatches != 0 || report.LoadCalls != 0 {
		t.Fatalf("protocol failure = %+v, %+v, %v", response, report, err)
	}

	response, report, err = invoke(t, root, []string{"missing"}, protocol.RequestDocument(), nil)
	if err != nil || response.Failure != (protocol.Failure{Category: protocol.CategoryDiscovery, Code: protocol.CodeInvalidSourceRoot}) || report.CommandDispatches != 1 || report.LoadCalls != 0 {
		t.Fatalf("root failure = %+v, %+v, %v", response, report, err)
	}

	broken := []byte(`{"format_version":1,"producer":{"name":"test","version":"1"},"migration":{"app":"alpha","name":"0001","name":"duplicate","dependencies":[],"operations":[]}}`)
	writeFile(t, filepath.Join(root, "migrations", "broken.godj.json"), broken)
	response, report, err = invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || response.Failure != (protocol.Failure{Category: protocol.CategorySource, Code: "invalid_definition_document"}) {
		t.Fatalf("definition failure = %+v, %+v, %v", response, report, err)
	}
	if report.LoadCalls != 1 || report.SourceReads != 1 || report.DocumentsReceived != 1 || report.HeadersValidated != 0 || !report.HasLoadFailure {
		t.Fatalf("definition report = %+v", report)
	}
	if report.LoadFailure.Stage != "document" || report.LoadFailure.SourceID != "migrations/broken.godj.json" || report.LoadFailure.JSONPointer != "/migration/name" || report.LoadFailure.Reason != "duplicate_key" {
		t.Fatalf("definition failure context = %+v", report.LoadFailure)
	}
}

func TestRequestAndConfigPrecedeProjectRootFilesystem(t *testing.T) {
	t.Parallel()
	missingRoot := filepath.Join(t.TempDir(), "missing")

	response, report, err := invoke(t, missingRoot, []string{"migrations"}, []byte(`{"protocol_version":2,"command":"migrations.check"}`), nil)
	if err != nil || response.Failure.Code != protocol.CodeProjectProtocolIncompatible || report.CommandDispatches != 0 {
		t.Fatalf("request precedence = %+v, %+v, %v", response, report, err)
	}
	response, report, err = invoke(t, missingRoot, []string{"bad/../root"}, protocol.RequestDocument(), nil)
	if err != nil || response.Failure.Code != protocol.CodeInvalidProjectSourceConfig || report.CommandDispatches != 1 || report.RootsOpened != 0 {
		t.Fatalf("config precedence = %+v, %+v, %v", response, report, err)
	}
	response, report, err = invoke(t, missingRoot, nil, protocol.RequestDocument(), nil)
	if err != nil || response.Failure.Code != protocol.CodeSourceDiscoveryFailed || report.CommandDispatches != 1 || report.LoadCalls != 0 {
		t.Fatalf("filesystem failure = %+v, %+v, %v", response, report, err)
	}
}

func TestCanonicalRootsAndSourceOrdering(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations/z", "migrations/a")
	writeFile(t, filepath.Join(root, "migrations", "z", "0001_initial.godj.json"), migrationDocument("alpha", "0001_initial", nil))
	writeFile(t, filepath.Join(root, "migrations", "z", "notes.txt"), []byte("ignored"))
	writeFile(t, filepath.Join(root, "migrations", "a", "0002_fields.godj.json"), migrationDocument("alpha", "0002_fields", [][2]string{{"alpha", "0001_initial"}}))

	first, firstReport, firstErr := invoke(t, root, []string{"migrations/z", "migrations/a"}, protocol.RequestDocument(), nil)
	second, secondReport, secondErr := invoke(t, root, []string{"migrations/a", "migrations/z"}, protocol.RequestDocument(), nil)
	if firstErr != nil || secondErr != nil || first != second || !first.OK || first.Result.SourceCount != 2 || first.Result.DefinitionCount != 2 {
		t.Fatalf("permuted results = %+v/%v, %+v/%v", first, firstErr, second, secondErr)
	}
	if !reflect.DeepEqual(firstReport, secondReport) || firstReport.RootsOpened != 2 || firstReport.DirectoryEntriesSeen != 3 || firstReport.SourceReads != 2 || firstReport.OperationsDecoded != 0 {
		t.Fatalf("permuted reports = %+v, %+v", firstReport, secondReport)
	}
}

func TestRootAndCandidateNoFollowSafety(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "real")
	if err := os.Symlink("real", filepath.Join(root, "migrations")); err != nil {
		t.Fatal(err)
	}
	response, report, err := invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || response.Failure.Code != protocol.CodeInvalidSourceRoot || report.LoadCalls != 0 {
		t.Fatalf("symlink root = %+v, %+v, %v", response, report, err)
	}

	fileRoot := newProjectRoot(t, "migrations")
	writeFile(t, filepath.Join(fileRoot, "target.json"), migrationDocument("alpha", "0001", nil))
	if err := os.Symlink("../target.json", filepath.Join(fileRoot, "migrations", "link.godj.json")); err != nil {
		t.Fatal(err)
	}
	response, report, err = invoke(t, fileRoot, []string{"migrations"}, protocol.RequestDocument(), nil)
	if err != nil || response.Failure.Code != protocol.CodeUnsafeSourceEntry || report.SourceReads != 0 || report.LoadCalls != 0 {
		t.Fatalf("symlink candidate = %+v, %+v, %v", response, report, err)
	}

	invalidName := string([]byte{0xff}) + ".godj.json"
	invalidRoot := newProjectRoot(t, "migrations")
	response, report, err = invoke(t, invalidRoot, []string{"migrations"}, protocol.RequestDocument(), systemDependencies{
		enumerateRoot: func(_ string, _ *os.File, yield func([]directoryEntry, error) bool) error {
			yield([]directoryEntry{{name: invalidName}}, io.EOF)
			return nil
		},
	})
	if err != nil || response.Failure.Code != protocol.CodeInvalidSourceEntry || report.SourceReads != 0 || report.LoadCalls != 0 {
		t.Fatalf("invalid-byte candidate = %+v, %+v, %v", response, report, err)
	}
}

func TestRetainedRootAndPostReadReplacementFailClosed(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	rootPath := filepath.Join(root, "migrations")
	replaced := false
	response, report, err := invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), systemDependencies{
		afterRootInitialStat: func(_ int, _ string) {
			if replaced {
				return
			}
			replaced = true
			if renameErr := os.Rename(rootPath, rootPath+".old"); renameErr != nil {
				t.Fatalf("rename source root: %v", renameErr)
			}
			if symlinkErr := os.Symlink("migrations.old", rootPath); symlinkErr != nil {
				t.Fatalf("replace source root: %v", symlinkErr)
			}
		},
	})
	if err != nil || response.Failure.Code != protocol.CodeSourceDiscoveryFailed || report.LoadCalls != 0 {
		t.Fatalf("root replacement = %+v, %+v, %v", response, report, err)
	}

	fileRoot := newProjectRoot(t, "migrations")
	filePath := filepath.Join(fileRoot, "migrations", "source.godj.json")
	writeFile(t, filePath, migrationDocument("alpha", "0001", nil))
	replaced = false
	response, report, err = invoke(t, fileRoot, []string{"migrations"}, protocol.RequestDocument(), systemDependencies{
		afterCandidateRead: func(_ int, _ string) {
			if replaced {
				return
			}
			replaced = true
			if renameErr := os.Rename(filePath, filePath+".old"); renameErr != nil {
				t.Fatalf("rename source: %v", renameErr)
			}
			if symlinkErr := os.Symlink("source.godj.json.old", filePath); symlinkErr != nil {
				t.Fatalf("replace source: %v", symlinkErr)
			}
		},
	})
	if err != nil || response.Failure.Code != protocol.CodeUnsafeSourceEntry || report.SourceReads != 0 || report.LoadCalls != 0 {
		t.Fatalf("post-read replacement = %+v, %+v, %v", response, report, err)
	}
}

func TestEnumerationIOPrecedesEntriesAndCaps(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	injected := errors.New("readdir failed")
	response, report, err := invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), systemDependencies{
		enumerateRoot: func(_ string, _ *os.File, yield func([]directoryEntry, error) bool) error {
			yield([]directoryEntry{{name: "would-open.godj.json"}}, injected)
			return nil
		},
	})
	if err != nil || response.Failure.Code != protocol.CodeSourceDiscoveryFailed || report.DirectoryEntriesSeen != 0 || report.SourceReads != 0 || report.LoadCalls != 0 {
		t.Fatalf("entries+error = %+v, %+v, %v", response, report, err)
	}

	for _, test := range []struct {
		name   string
		actual int
		failed bool
	}{
		{name: "entries maximum minus one", actual: maxDirectoryEntries - 1},
		{name: "entries maximum", actual: maxDirectoryEntries},
		{name: "entries maximum plus one", actual: maxDirectoryEntries + 1, failed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			count := test.actual - 1
			_, exceeded := addDirectoryEntry(count)
			if exceeded != test.failed {
				t.Fatalf("addDirectoryEntry(%d) exceeded = %v", count, exceeded)
			}
		})
	}
}

func TestLinkedResourceBoundaryHelpers(t *testing.T) {
	t.Parallel()
	validRoots := make([]string, maxRoots)
	for index := range validRoots {
		validRoots[index] = fmt.Sprintf("root-%03d", index)
	}
	if _, _, failed := canonicalRoots(validRoots[:maxRoots-1]); failed {
		t.Fatal("maximum-1 roots failed")
	}
	if _, _, failed := canonicalRoots(validRoots); failed {
		t.Fatal("maximum roots failed")
	}
	if _, failure, failed := canonicalRoots(append(validRoots, "overflow")); !failed || failure.Code != protocol.CodeInvalidProjectSourceConfig {
		t.Fatalf("maximum+1 roots = %+v, %v", failure, failed)
	}
	for _, roots := range [][]string{{""}, {"/absolute"}, {"a/../b"}, {"a\\b"}, {"duplicate", "duplicate"}, {string([]byte{0xff})}} {
		if _, failure, failed := canonicalRoots(roots); !failed || failure.Code != protocol.CodeInvalidProjectSourceConfig {
			t.Errorf("canonicalRoots(%q) = %+v, %v", roots, failure, failed)
		}
	}

	for _, actual := range []int{definition.MaxSources - 1, definition.MaxSources, definition.MaxSources + 1} {
		exceeded := sourceCountExceeded(actual)
		if exceeded != (actual > definition.MaxSources) {
			t.Errorf("sourceCountExceeded(%d) = %v", actual, exceeded)
		}
	}
	for _, actual := range []int{definition.MaxSourceIDBytes - 1, definition.MaxSourceIDBytes, definition.MaxSourceIDBytes + 1} {
		exceeded := sourceIDBytesExceeded(strings.Repeat("a", actual))
		if exceeded != (actual > definition.MaxSourceIDBytes) {
			t.Errorf("sourceIDBytesExceeded(%d) = %v", actual, exceeded)
		}
	}
	for _, batch := range []uint64{maxBatchBytes - 1, maxBatchBytes, maxBatchBytes + 1} {
		maximum := candidateReadMaximum(batch)
		want := uint64(0)
		if batch < maxBatchBytes {
			want = maxBatchBytes - batch
			if want > maxDocumentBytes {
				want = maxDocumentBytes
			}
		}
		if maximum != want {
			t.Errorf("candidateReadMaximum(%d) = %d, want %d", batch, maximum, want)
		}
	}
	for _, maximum := range []uint64{maxDocumentBytes - 1, maxDocumentBytes, maxDocumentBytes + 1} {
		document, err := readBounded(bytes.NewReader(bytes.Repeat([]byte{'x'}, int(maximum)+1)), maximum)
		if err != nil || uint64(len(document)) != maximum+1 {
			t.Errorf("readBounded(%d) = %d, %v", maximum, len(document), err)
		}
	}
}

func TestRunInputOwnershipConcurrencyAndErrors(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	roots := []string{"migrations"}
	argv := []string{protocol.PrivateArgument}
	reader := newBlockingReader(protocol.RequestDocument())
	var output bytes.Buffer
	done := make(chan struct{})
	var report Report
	var runErr error
	go func() {
		report, runErr = Run(context.Background(), Config{ProjectRoot: root, MigrationDefinitionRoots: roots}, argv, reader, &output)
		close(done)
	}()
	<-reader.started
	roots[0] = "mutated"
	argv[0] = "mutated"
	close(reader.release)
	<-done
	response, failure, failed := protocol.ParseResponse(output.Bytes(), true)
	if runErr != nil || failed || failure != (protocol.Failure{}) || !response.OK || report.LoadCalls != 1 {
		t.Fatalf("mutation snapshot = %+v, %+v, %v, %+v", response, failure, runErr, report)
	}

	const goroutines = 20
	var wait sync.WaitGroup
	errorsSeen := make(chan error, goroutines)
	for index := 0; index < goroutines; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, report, err := invoke(t, root, []string{"migrations"}, protocol.RequestDocument(), nil)
			if err != nil || !response.OK || report.LoadCalls != 1 {
				errorsSeen <- fmt.Errorf("response=%+v report=%+v err=%w", response, report, err)
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}

	if _, err := Run(nil, Config{ProjectRoot: root}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("nil context succeeded")
	}
	if _, err := Run(context.Background(), Config{ProjectRoot: root}, []string{protocol.PrivateArgument}, nil, io.Discard); err == nil {
		t.Fatal("nil stdin succeeded")
	}
	if _, err := Run(context.Background(), Config{ProjectRoot: root}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), nil); err == nil {
		t.Fatal("nil stdout succeeded")
	}
	if _, err := Run(context.Background(), Config{ProjectRoot: root}, []string{"wrong"}, bytes.NewReader(protocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("wrong private argv succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(canceled, Config{ProjectRoot: root}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Run = %v", err)
	}
	shortReport, err := Run(context.Background(), Config{ProjectRoot: root, MigrationDefinitionRoots: []string{"migrations"}}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), shortWriter{})
	if !errors.Is(err, io.ErrShortWrite) || shortReport.RunnerResponseWrites != 1 {
		t.Fatalf("short write = %+v, %v", shortReport, err)
	}
}

func TestArbitraryPrivateRequestBytesDoNotPanic(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	for length := 0; length <= 256; length++ {
		document := make([]byte, length)
		for index := range document {
			document[index] = byte((length*19 + index*43) & 0xff)
		}
		var output bytes.Buffer
		report, err := Run(
			context.Background(),
			Config{ProjectRoot: root},
			[]string{protocol.PrivateArgument},
			bytes.NewReader(document),
			&output,
		)
		if err != nil || report.RunnerResponseWrites != 1 {
			t.Fatalf("length %d = report %+v, err %v", length, report, err)
		}
		response, parseFailure, parseFailed := protocol.ParseResponse(output.Bytes(), true)
		if parseFailed || parseFailure != (protocol.Failure{}) || response.Failure.Category != protocol.CategoryProtocol {
			t.Fatalf("length %d response = %+v, %+v, %v", length, response, parseFailure, parseFailed)
		}
	}
}

func TestCancellationWinsAtEveryResponseBarrier(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	brokenRoot := newProjectRoot(t, "migrations")
	writeFile(t, filepath.Join(brokenRoot, "migrations", "broken.godj.json"), []byte(`{"broken":true}`))

	tests := []struct {
		name         string
		root         string
		roots        []string
		request      []byte
		wantDispatch int
		wantLoad     int
	}{
		{
			name:    "request failure",
			root:    root,
			roots:   []string{"migrations"},
			request: []byte(`{"protocol_version":2,"command":"migrations.check"}`),
		},
		{
			name:         "discovery failure",
			root:         root,
			roots:        []string{"missing"},
			request:      protocol.RequestDocument(),
			wantDispatch: 1,
		},
		{
			name:         "load failure",
			root:         brokenRoot,
			roots:        []string{"migrations"},
			request:      protocol.RequestDocument(),
			wantDispatch: 1,
			wantLoad:     1,
		},
		{
			name:         "success",
			root:         root,
			roots:        []string{"migrations"},
			request:      protocol.RequestDocument(),
			wantDispatch: 1,
			wantLoad:     1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			var output bytes.Buffer
			calls := 0
			report, err := run(
				ctx,
				Config{ProjectRoot: test.root, MigrationDefinitionRoots: test.roots},
				[]string{protocol.PrivateArgument},
				bytes.NewReader(test.request),
				&output,
				systemDependencies{beforeResponseWrite: func() {
					calls++
					cancel()
				}},
			)
			if !errors.Is(err, context.Canceled) || output.Len() != 0 || report.RunnerResponseWrites != 0 {
				t.Fatalf("barrier cancel = report %+v, output %q, err %v", report, output.Bytes(), err)
			}
			if calls != 1 || report.CommandDispatches != test.wantDispatch || report.LoadCalls != test.wantLoad {
				t.Fatalf("barrier metrics = calls %d report %+v", calls, report)
			}
		})
	}
}

func TestLinkedProductionDependencyAndLoaderGates(t *testing.T) {
	t.Parallel()
	set := token.NewFileSet()
	directory, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	loadCalls := 0
	forbidden := []string{"conformance", "/db/", "Executor.Migrate", "NewPlanner"}
	for _, entry := range directory {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range forbidden {
			if bytes.Contains(source, []byte(fragment)) {
				t.Errorf("%s contains forbidden fragment %q", entry.Name(), fragment)
			}
		}
		file, err := parser.ParseFile(set, entry.Name(), source, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Load" {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if ok && identifier.Name == "definition" {
				loadCalls++
			}
			return true
		})
	}
	if loadCalls != 1 {
		t.Fatalf("production definition.Load callsites = %d, want 1", loadCalls)
	}
}

func invoke(t *testing.T, root string, roots []string, request []byte, dependencies any) (protocol.Response, Report, error) {
	t.Helper()
	var output bytes.Buffer
	var report Report
	var err error
	if dependencies == nil {
		report, err = Run(context.Background(), Config{ProjectRoot: root, MigrationDefinitionRoots: roots}, []string{protocol.PrivateArgument}, bytes.NewReader(request), &output)
	} else {
		report, err = run(context.Background(), Config{ProjectRoot: root, MigrationDefinitionRoots: append([]string(nil), roots...)}, []string{protocol.PrivateArgument}, bytes.NewReader(request), &output, dependencies.(systemDependencies))
	}
	if err != nil {
		return protocol.Response{}, report, err
	}
	response, parseFailure, parseFailed := protocol.ParseResponse(output.Bytes(), true)
	if parseFailed {
		t.Fatalf("linked wrote invalid response %q: %+v", output.Bytes(), parseFailure)
	}
	return response, report, nil
}

func newProjectRoot(t *testing.T, roots ...string) string {
	t.Helper()
	root := t.TempDir()
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	physical, err = filepath.Abs(physical)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range roots {
		if err := os.MkdirAll(filepath.Join(physical, filepath.FromSlash(relative)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return physical
}

func writeFile(t *testing.T, path string, document []byte) {
	t.Helper()
	if err := os.WriteFile(path, document, 0o644); err != nil {
		t.Fatal(err)
	}
}

func migrationDocument(app, name string, dependencies [][2]string) []byte {
	encodedDependencies := make([]string, len(dependencies))
	for index, dependency := range dependencies {
		encodedDependencies[index] = fmt.Sprintf(`{"app":%q,"name":%q}`, dependency[0], dependency[1])
	}
	return []byte(fmt.Sprintf(
		`{"format_version":1,"producer":{"name":"linked-test","version":"1"},"migration":{"app":%q,"name":%q,"dependencies":[%s],"operations":[]}}`,
		app,
		name,
		strings.Join(encodedDependencies, ","),
	))
}

type blockingReader struct {
	document []byte
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newBlockingReader(document []byte) *blockingReader {
	return &blockingReader{document: append([]byte(nil), document...), started: make(chan struct{}), release: make(chan struct{})}
}

func (reader *blockingReader) Read(buffer []byte) (int, error) {
	reader.once.Do(func() { close(reader.started) })
	<-reader.release
	if len(reader.document) == 0 {
		return 0, io.EOF
	}
	read := copy(buffer, reader.document)
	reader.document = reader.document[read:]
	return read, nil
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }
