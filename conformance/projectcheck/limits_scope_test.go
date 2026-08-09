//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestElevenInclusiveCapsHaveExactMaximumMinusOneEqualPlusOneSemantics(t *testing.T) {
	t.Parallel()
	limits := []struct {
		name    string
		maximum uint64
	}{
		{"descriptor_bytes", maxDescriptorBytes},
		{"ancestor_directories", maxAncestors},
		{"semantic_roots", maxRoots},
		{"aggregate_directory_entries", maxDirectoryEntries},
		{"sources", maxSources},
		{"source_id_bytes", maxSourceIDBytes},
		{"document_bytes", maxDocumentBytes},
		{"batch_bytes", maxBatchBytes},
		{"request_bytes", maxRequestBytes},
		{"response_bytes", maxResponseBytes},
		{"diagnostic_retained_prefix_bytes", maxDiagnosticBytes},
	}
	if len(limits) != 11 {
		t.Fatalf("limit count = %d, want 11", len(limits))
	}
	for _, limit := range limits {
		t.Run(limit.name, func(t *testing.T) {
			for _, actual := range []uint64{limit.maximum - 1, limit.maximum, limit.maximum + 1} {
				_, exceeded := checkedAdd(0, actual, limit.maximum)
				if exceeded != (actual > limit.maximum) {
					t.Fatalf("actual %d max %d exceeded=%v", actual, limit.maximum, exceeded)
				}
			}
		})
	}
	if actual, exceeded := checkedAdd(^uint64(0)-1, 2, maxBatchBytes); actual != ^uint64(0) || !exceeded {
		t.Fatalf("overflow was not saturating: actual=%d exceeded=%v", actual, exceeded)
	}
}

func TestDescriptorRequestResponseAndDiagnosticExactByteBoundaries(t *testing.T) {
	t.Parallel()
	lim := contractLimits()
	for _, actual := range []int{maxDescriptorBytes - 1, maxDescriptorBytes, maxDescriptorBytes + 1} {
		document := descriptorOfSize(t, actual)
		_, primary := parseDescriptor(document, lim)
		if (primary != nil) != (actual > maxDescriptorBytes) {
			t.Fatalf("descriptor bytes %d = %v", actual, primary)
		}
	}
	requestBase := []byte(`{"protocol_version":1,"command":"migrations.check"}`)
	for _, actual := range []int{maxRequestBytes - 1, maxRequestBytes, maxRequestBytes + 1} {
		request := padJSON(requestBase, actual)
		primary := parseRunnerRequest(request, lim)
		if (primary != nil) != (actual > maxRequestBytes) {
			t.Fatalf("request bytes %d = %v", actual, primary)
		}
	}
	responseBase := encodeRunnerSuccess(checkResult{SourceCount: 0, DefinitionCount: 0, DefinitionSetDigest: emptySetDigest})
	for _, actual := range []int{maxResponseBytes - 1, maxResponseBytes, maxResponseBytes + 1} {
		response := padJSON(responseBase, actual)
		_, primary := parseRunnerResponse(response, 0, lim)
		if (primary != nil) != (actual > maxResponseBytes) {
			t.Fatalf("response bytes %d = %v", actual, primary)
		}
	}
	for _, actual := range []int{maxDiagnosticBytes - 1, maxDiagnosticBytes, maxDiagnosticBytes + 1} {
		capture := newCappedCapture(maxDiagnosticBytes)
		_, _ = capture.Write(bytes.Repeat([]byte("d"), actual))
		backing := capture.retained.Bytes()
		raw, scalar := capture.takeAndDiscard()
		if scalar.RetainedBytes != minInt(actual, maxDiagnosticBytes) || scalar.Truncated != (actual > maxDiagnosticBytes) || len(raw) != minInt(actual, maxDiagnosticBytes) {
			t.Fatalf("diagnostic bytes %d = raw=%d scalar=%+v", actual, len(raw), scalar)
		}
		if !allZero(backing) {
			t.Fatalf("diagnostic bytes %d retained backing was not zeroed before reset", actual)
		}
		zeroBytes(raw)
		if !allZero(raw) {
			t.Fatalf("diagnostic bytes %d returned copy could not be explicitly discarded", actual)
		}
	}
}

func allZero(payload []byte) bool {
	for _, value := range payload {
		if value != 0 {
			return false
		}
	}
	return true
}

func descriptorOfSize(t *testing.T, size int) []byte {
	t.Helper()
	base := canonicalDescriptor("./cmd/site")
	if size < len(base)+2 {
		t.Fatalf("descriptor size %d too small", size)
	}
	comment := append([]byte{'#'}, bytes.Repeat([]byte("x"), size-len(base)-2)...)
	comment = append(comment, '\n')
	return append(comment, base...)
}

func padJSON(base []byte, size int) []byte {
	if size < len(base) {
		return append([]byte(nil), base...)
	}
	result := make([]byte, size)
	copy(result, base)
	for index := len(base); index < len(result); index++ {
		result[index] = ' '
	}
	return result
}

func TestAncestorRootEntrySourceAndSourceIDExactCountBoundaries(t *testing.T) {
	t.Parallel()
	t.Run("ancestors", func(t *testing.T) {
		for _, count := range []int{maxAncestors - 1, maxAncestors, maxAncestors + 1} {
			root := t.TempDir()
			cwd := root
			for index := 1; index < count; index++ {
				cwd = filepath.Join(cwd, "d")
			}
			mustMkdir(t, cwd)
			metrics := oracleMetrics{}
			_, primary := selectProject(cwd, commandArguments{}, &metrics, contractLimits(), selectionHooks{virtualFilesystemRoot: root})
			wantCode := "project_not_found"
			wantInspected := count
			if count > maxAncestors {
				wantCode = "project_search_limit_exceeded"
				wantInspected = maxAncestors
			}
			if primary == nil || primary.Code != wantCode || metrics.AncestorDirectoriesInspected != wantInspected {
				t.Fatalf("ancestor count %d = %v metrics=%+v", count, primary, metrics)
			}
		}
	})
	t.Run("roots", func(t *testing.T) {
		for _, count := range []int{maxRoots - 1, maxRoots, maxRoots + 1} {
			roots := make([]string, count)
			for index := range roots {
				roots[index] = "r" + strconv.Itoa(index)
			}
			canonical, primary := canonicalRoots(roots, contractLimits())
			if (primary != nil) != (count > maxRoots) || (primary == nil && len(canonical) != count) {
				t.Fatalf("root count %d = %v len=%d", count, primary, len(canonical))
			}
		}
	})
	t.Run("entries-and-immediate-stop", func(t *testing.T) {
		root := t.TempDir()
		mustMkdir(t, filepath.Join(root, "a"))
		mustMkdir(t, filepath.Join(root, "z"))
		for _, count := range []int{maxDirectoryEntries - 1, maxDirectoryEntries, maxDirectoryEntries + 1} {
			calls := 0
			laterRootCalls := 0
			hooks := discoveryHooks{enumerateRoot: func(logical string, _ *os.File, yield func([]enumeratedEntry, error) bool) error {
				if logical == "z" {
					laterRootCalls++
					yield(nil, io.EOF)
					return nil
				}
				calls++
				chunk := make([]enumeratedEntry, count)
				for index := range chunk {
					chunk[index] = enumeratedEntry{name: "noise.txt"}
				}
				if yield(chunk, nil) {
					calls++
					yield(nil, io.EOF)
				}
				return nil
			}}
			metrics := oracleMetrics{}
			_, primary := discoverSources(root, []string{"z", "a"}, &metrics, contractLimits(), hooks)
			if count <= maxDirectoryEntries {
				if primary != nil || calls != 2 || laterRootCalls != 1 || metrics.DirectoryEntriesSeen != count {
					t.Fatalf("entry count %d = %v calls=%d later=%d metrics=%+v", count, primary, calls, laterRootCalls, metrics)
				}
			} else if primary == nil || primary.Code != "source_catalog_limit_exceeded" || calls != 1 || laterRootCalls != 0 || metrics.DirectoryEntriesSeen != maxDirectoryEntries {
				t.Fatalf("entry overflow = %v calls=%d later=%d metrics=%+v", primary, calls, laterRootCalls, metrics)
			}
		}
	})
	t.Run("sources", func(t *testing.T) {
		for _, count := range []int{maxSources - 1, maxSources, maxSources + 1} {
			primary := sourceCountFailure(count, contractLimits())
			if (primary != nil) != (count > maxSources) {
				t.Fatalf("source count %d = %v", count, primary)
			}
		}
	})
	t.Run("source-id", func(t *testing.T) {
		for _, count := range []int{maxSourceIDBytes - 1, maxSourceIDBytes, maxSourceIDBytes + 1} {
			pathHex, primary := preflightCandidateSourceID(strings.Repeat("p", count), contractLimits())
			if (primary != nil) != (count > maxSourceIDBytes) || (primary != nil && pathHex == "") {
				t.Fatalf("SourceID bytes %d = %v hex=%q", count, primary, pathHex)
			}
		}
	})
}

func TestDocumentAndBatchExactByteBoundaries(t *testing.T) {
	t.Parallel()
	t.Run("document", func(t *testing.T) {
		for _, count := range []int{maxDocumentBytes - 1, maxDocumentBytes, maxDocumentBytes + 1} {
			root := t.TempDir()
			document := padDefinition(oneCreateModelDocument(), count)
			writeDefinition(t, root, "migrations/a.godj.json", document)
			metrics := oracleMetrics{}
			linked := invokeLinked(linkedInvocation{ProjectRoot: root, Roots: []string{"migrations"}, Request: []byte(`{"protocol_version":1,"command":"migrations.check"}`), Limits: contractLimits(), Metrics: &metrics})
			_, primary := parseRunnerResponse(linked.Wire, 0, contractLimits())
			if count <= maxDocumentBytes {
				if primary != nil || metrics.LoadCalls != 1 || metrics.SourceReads != 1 {
					t.Fatalf("document bytes %d = %v metrics=%+v", count, primary, metrics)
				}
			} else if primary == nil || primary.Code != "source_catalog_limit_exceeded" || metrics.LoadCalls != 0 || metrics.SourceReads != 0 {
				t.Fatalf("document overflow = %v metrics=%+v", primary, metrics)
			}
		}
	})
	t.Run("batch", func(t *testing.T) {
		for _, delta := range []int{-1, 0, 1} {
			root := t.TempDir()
			path := filepath.Join(root, "source.godj.json")
			payloadSize := 2 + delta
			if err := os.WriteFile(path, bytes.Repeat([]byte("x"), payloadSize), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			var stat unix.Stat_t
			if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
				file.Close()
				t.Fatal(err)
			}
			file.Close()
			rootHandle, err := os.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			retained := retainedRoot{logical: ".", handle: rootHandle}
			item := candidate{root: &retained, name: "source.godj.json", sourceID: "source.godj.json", identity: identityOf(&stat)}
			_, primary := readStableCandidate(item, maxBatchBytes-2, contractLimits(), discoveryHooks{})
			rootHandle.Close()
			if (primary != nil) != (delta > 0) || (primary != nil && primary.Code != "source_catalog_limit_exceeded") {
				t.Fatalf("batch delta %d = %v", delta, primary)
			}
		}
	})
}

func padDefinition(base []byte, size int) []byte {
	if size < len(base) {
		return append([]byte(nil), base...)
	}
	padding := bytes.Repeat([]byte{' '}, size-len(base))
	return append(padding, base...)
}

func TestRunnerStdoutValidPrefixPlusOverflowIsRejectedAfterDrain(t *testing.T) {
	t.Parallel()
	valid := encodeRunnerSuccess(checkResult{SourceCount: 0, DefinitionCount: 0, DefinitionSetDigest: emptySetDigest})
	lim := contractLimits()
	lim.responseBytes = len(valid)
	backend := &concreteExecBackend{Limits: lim, Grace: 40 * time.Millisecond}
	observed := observation{Feasibility: feasibilityMetrics{Diagnostics: map[string]diagnosticScalar{}}}
	result := backend.Run(context.Background(), "runner", helperSpec("wire-padding", map[string]string{"GODJ_HELPER_WIRE": string(valid), "GODJ_HELPER_PADDING": "1"}), &observed)
	if !result.StdoutOverflow || !bytes.Equal(result.Stdout, valid) || observed.Feasibility.Diagnostics["runner_stderr"].RetainedBytes != 0 {
		t.Fatalf("runner overflow transport = result=%+v feasibility=%+v", result, observed.Feasibility)
	}
	root := t.TempDir()
	writeDescriptor(t, root)
	workspaceBase := t.TempDir()
	fixed := &fixedBackend{Runner: result}
	global := runGlobal(globalInvocation{Context: context.Background(), CWD: root, Argv: []string{"migrations", "check"}, Limits: lim, Deps: globalDependencies{Backend: fixed, CreateWorkspace: successfulTestWorkspace(workspaceBase)}})
	if global.Failure == nil || global.Failure.Code != "invalid_project_runner_response" || global.Metrics.ExitCode != 3 {
		t.Fatalf("runner overflow global = %+v", global)
	}
	nonzeroObserved := observation{Feasibility: feasibilityMetrics{Diagnostics: map[string]diagnosticScalar{}}}
	nonzero := backend.Run(context.Background(), "runner", helperSpec("wire-padding", map[string]string{"GODJ_HELPER_WIRE": string(valid), "GODJ_HELPER_PADDING": "1", "GODJ_HELPER_EXIT": "9"}), &nonzeroObserved)
	if !nonzero.StdoutOverflow || nonzero.Exit != 9 || nonzeroObserved.Metrics.RunnerResponseWrites != 1 {
		t.Fatalf("nonzero overflow transport = result=%+v metrics=%+v", nonzero, nonzeroObserved.Metrics)
	}
	nonzeroGlobal := runGlobal(globalInvocation{Context: context.Background(), CWD: root, Argv: []string{"migrations", "check"}, Limits: lim, Deps: globalDependencies{Backend: &fixedBackend{Runner: nonzero}, CreateWorkspace: successfulTestWorkspace(workspaceBase)}})
	if nonzeroGlobal.Failure == nil || nonzeroGlobal.Failure.Code != "project_runner_failed" {
		t.Fatalf("transport exit did not precede stdout overflow: %+v", nonzeroGlobal)
	}
}

type fixedBackend struct {
	Build  childResult
	Runner childResult
}

func TestPrivateBackendFailurePairsAreStageCheckedAndFailClosed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDescriptor(t, root)
	base := t.TempDir()
	for _, test := range []struct {
		name         string
		backend      *fixedBackend
		wantBuild    int
		wantRunner   int
		wantCategory string
		wantCode     string
	}{
		{
			name:      "unknown-build-pair",
			backend:   &fixedBackend{Build: childResult{Failure: &failure{Category: "invented", Code: "invented", ExitCode: 3}}},
			wantBuild: 1, wantCategory: "migration_project_internal_error", wantCode: "project_internal_error",
		},
		{
			name:      "linked-pair-cannot-escape-private-runner-backend",
			backend:   &fixedBackend{Runner: childResult{Failure: fail("migration_definition_source_error", "invalid_definition_document")}},
			wantBuild: 1, wantRunner: 1, wantCategory: "migration_project_internal_error", wantCode: "project_internal_error",
		},
		{
			name:      "known-build-pair-is-canonicalized",
			backend:   &fixedBackend{Build: childResult{Failure: &failure{Category: "migration_project_build_error", Code: "project_build_failed", ExitCode: 99}}},
			wantBuild: 1, wantCategory: "migration_project_build_error", wantCode: "project_build_failed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := runGlobal(globalInvocation{
				Context: context.Background(), CWD: root, Argv: []string{"migrations", "check"}, Limits: contractLimits(),
				Deps: globalDependencies{Backend: test.backend, CreateWorkspace: successfulTestWorkspace(base)},
			})
			if observed.Failure == nil || observed.Failure.Category != test.wantCategory || observed.Failure.Code != test.wantCode || observed.Failure.ExitCode != 3 || observed.Metrics.BuildCalls != test.wantBuild || observed.Metrics.RunnerCalls != test.wantRunner {
				t.Fatalf("private backend classification = %+v", observed)
			}
		})
	}
}

func TestGlobalDiscardsBackendRawBuffersBeforePublicOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDescriptor(t, root)
	base := t.TempDir()
	t.Run("build-diagnostics", func(t *testing.T) {
		stdout := []byte("private build stdout")
		stderr := []byte("private build stderr")
		observed := runGlobal(globalInvocation{
			Context: context.Background(), CWD: root, Argv: []string{"migrations", "check"}, Limits: contractLimits(),
			Deps: globalDependencies{
				Backend:         &fixedBackend{Build: childResult{Stdout: stdout, Stderr: stderr, Exit: 1}},
				CreateWorkspace: successfulTestWorkspace(base),
			},
		})
		if observed.Failure == nil || observed.Failure.Code != "project_build_failed" || !allZero(stdout) || !allZero(stderr) || bytes.Contains(observed.Stderr, []byte("private build")) {
			t.Fatalf("build raw discard = observed=%+v stdout=%q stderr=%q", observed, stdout, stderr)
		}
	})
	t.Run("runner-wire-and-diagnostic", func(t *testing.T) {
		wire := encodeRunnerSuccess(checkResult{SourceCount: 0, DefinitionCount: 0, DefinitionSetDigest: emptySetDigest})
		stderr := []byte("private runner stderr")
		observed := runGlobal(globalInvocation{
			Context: context.Background(), CWD: root, Argv: []string{"migrations", "check"}, Limits: contractLimits(),
			Deps: globalDependencies{
				Backend:         &fixedBackend{Runner: childResult{Stdout: wire, Stderr: stderr}},
				CreateWorkspace: successfulTestWorkspace(base),
			},
		})
		if observed.Failure != nil || observed.Result == nil || !allZero(wire) || !allZero(stderr) || bytes.Contains(observed.Stdout, []byte("protocol_version")) || bytes.Contains(observed.Stderr, []byte("private runner")) {
			t.Fatalf("runner raw discard = observed=%+v wire=%q stderr=%q", observed, wire, stderr)
		}
	})
}

func (backend *fixedBackend) Run(_ context.Context, stage string, _ commandSpec, _ *observation) childResult {
	if stage == "build" {
		return backend.Build
	}
	return backend.Runner
}

func TestBarrierSamplesInterruptOnceAndTerminalCommitIsImmutable(t *testing.T) {
	t.Parallel()
	calls := 0
	interruptOnce := func() bool {
		calls++
		return calls == 1
	}
	observed := runGlobal(globalInvocation{Context: context.Background(), Argv: []string{"bad"}, Limits: contractLimits(), Deps: globalDependencies{Interrupted: interruptOnce}})
	if observed.Failure == nil || observed.Failure.Code != "project_interrupted" || calls != 1 {
		t.Fatalf("one-shot barrier = %+v calls=%d", observed, calls)
	}
	committed := observation{}
	committed.choose(fail("migration_project_build_error", "project_build_failed"), nil)
	committed.choose(fail("migration_project_process_error", "project_interrupted"), nil)
	if committed.Failure.Code != "project_build_failed" {
		t.Fatalf("terminal outcome mutated: %+v", committed)
	}
}

func TestStageCancelAndProcessCleanupThreeWayPrecedence(t *testing.T) {
	t.Parallel()
	ordinary := fail("migration_project_build_error", "project_build_failed")
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name          string
		input         globalInvocation
		primary       *failure
		cleanupFailed bool
		wantCode      string
	}{
		{"ordinary-only", globalInvocation{Context: context.Background()}, ordinary, false, "project_build_failed"},
		{"ordinary-plus-cleanup", globalInvocation{Context: context.Background()}, ordinary, true, "project_build_failed"},
		{"cancel-beats-ordinary", globalInvocation{Context: canceledContext}, ordinary, false, "project_canceled"},
		{"cleanup-beats-committed-cancel", globalInvocation{Context: canceledContext}, ordinary, true, "project_cleanup_failed"},
		{"cleanup-beats-cancel-without-ordinary", globalInvocation{Context: canceledContext}, nil, true, "project_cleanup_failed"},
	}
	for _, test := range cases {
		committed := barrierFailure(test.input, test.primary)
		final := combineProcessFinalization(committed, test.cleanupFailed)
		if final == nil || final.Code != test.wantCode {
			t.Fatalf("%s = %v, want %s", test.name, final, test.wantCode)
		}
	}
}

func TestOracleMetricShapeIsExactlyTwentyFourClosedFields(t *testing.T) {
	t.Parallel()
	typeOf := reflect.TypeFor[oracleMetrics]()
	if typeOf.NumField() != 24 {
		t.Fatalf("oracle metric fields = %d, want 24", typeOf.NumField())
	}
	want := []string{
		"build_calls", "runner_calls", "runner_response_writes", "source_reads", "load_calls", "documents_received",
		"headers_validated", "operations_decoded", "planner_construction", "definitions_published", "definition_sets_published",
		"direct_planner_calls", "godj_db_calls", "revision_lifecycle_calls", "user_stdout_writes", "user_stderr_writes",
		"partial_stdout_writes", "exit_code", "command_dispatches", "ancestor_directories_inspected", "descriptor_reads",
		"roots_opened", "directory_entries_seen", "failure",
	}
	actual := make([]string, typeOf.NumField())
	for index := range actual {
		actual[index] = strings.Split(typeOf.Field(index).Tag.Get("json"), ",")[0]
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("oracle metric keys = %v, want %v", actual, want)
	}
	payload, err := json.Marshal(oracleMetrics{})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatal(err)
	}
	if len(object) != 24 || string(object["failure"]) != "null" {
		t.Fatalf("oracle metric wire shape = %s", payload)
	}
}

func TestProjectCheckPackageIsTestOnlyUnixScopedAndUnimportedByProduction(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	packageDir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(packageDir)
	if err != nil {
		t.Fatal(err)
	}
	definitionImporters := make([]string, 0)
	definitionLoadCalls := 0
	forbiddenImports := make([]string, 0)
	forbiddenCalls := make([]string, 0)
	forbiddenMethod := map[string]struct{}{
		"NewPlanner": {}, "Migrate": {}, "LoadAppliedState": {}, "OpenRevisionFencedSession": {},
		"BeginFencedMigration": {}, "ReadAppliedMigrations": {}, "RecordApplied": {}, "RecordUnapplied": {},
		"Exec": {}, "ExecContext": {}, "Query": {}, "QueryContext": {}, "Begin": {}, "BeginTx": {},
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			t.Fatalf("non-test Go source in test-only package: %s", entry.Name())
		}
		payload, err := os.ReadFile(filepath.Join(packageDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(payload, []byte("//go:build darwin || linux\n")) {
			t.Fatalf("Unix non-goal build constraint absent from %s", entry.Name())
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), payload, 0)
		if err != nil {
			t.Fatal(err)
		}
		aliases := make(map[string]string)
		for _, imported := range file.Imports {
			importPath, _ := strconv.Unquote(imported.Path.Value)
			if importPath == "github.com/progresshans/godj/migrations/definition" {
				definitionImporters = append(definitionImporters, entry.Name())
			}
			if importPath == "database/sql" || importPath == "modernc.org/sqlite" || importPath == "github.com/progresshans/godj/db" || strings.HasPrefix(importPath, "github.com/progresshans/godj/db/") || importPath == "github.com/progresshans/godj/migrations/backend" {
				forbiddenImports = append(forbiddenImports, entry.Name()+":"+importPath)
			}
			if imported.Name != nil && imported.Name.Name == "." && (importPath == "github.com/progresshans/godj/migrations" || importPath == "github.com/progresshans/godj/migrations/definition") {
				forbiddenImports = append(forbiddenImports, entry.Name()+":dot:"+importPath)
			}
			alias := ""
			if imported.Name != nil {
				alias = imported.Name.Name
			} else if slash := strings.LastIndexByte(importPath, '/'); slash >= 0 {
				alias = importPath[slash+1:]
			} else {
				alias = importPath
			}
			aliases[alias] = importPath
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				if _, forbidden := forbiddenMethod[selector.Sel.Name]; forbidden {
					forbiddenCalls = append(forbiddenCalls, entry.Name()+":"+selector.Sel.Name)
				}
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, identifierOK := selector.X.(*ast.Ident)
			if identifierOK && aliases[identifier.Name] == "github.com/progresshans/godj/migrations/definition" && selector.Sel.Name == "Load" {
				definitionLoadCalls++
			}
			return true
		})
	}
	sort.Strings(definitionImporters)
	if !reflect.DeepEqual(definitionImporters, []string{"linked_discovery_test.go"}) {
		t.Fatalf("actual loader import boundary = %v", definitionImporters)
	}
	if definitionLoadCalls != 1 {
		t.Fatalf("direct definition.Load callsites = %d, want exactly 1", definitionLoadCalls)
	}
	if len(forbiddenImports) != 0 || len(forbiddenCalls) != 0 {
		t.Fatalf("DB/planner/revision lifecycle boundary violated: imports=%v calls=%v", forbiddenImports, forbiddenCalls)
	}
	repository := filepath.Clean(filepath.Join(packageDir, "..", ".."))
	err = filepath.WalkDir(repository, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			importPath, _ := strconv.Unquote(imported.Path.Value)
			if importPath == "github.com/progresshans/godj/conformance/projectcheck" {
				return errors.New("production imports test-only projectcheck harness: " + path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNoShellInvocationIsPresentInHarnessAST(t *testing.T) {
	t.Parallel()
	_, thisFile, _, _ := runtime.Caller(0)
	packageDir := filepath.Dir(thisFile)
	files, err := parser.ParseDir(token.NewFileSet(), packageDir, func(info os.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, packageAST := range files {
		ast.Inspect(packageAST, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Command" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if ok && (literal.Value == `"sh"` || literal.Value == `"bash"` || literal.Value == `"zsh"`) {
				t.Errorf("shell invocation found: %s", literal.Value)
			}
			return true
		})
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
