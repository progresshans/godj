//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	oneModelDigest = "sha256:b15b980386317e4c75746910d01bf5492876a5eb31a2ed3f560722866c15a1b6"
	twoModelDigest = "sha256:23717638bb5e764e69fbda28da817a2a3290f12a729474969a15ed07c9aa55be"
)

type baseScenario struct {
	id       string
	observed observation
	want     observation
}

func TestMIG065ThroughMIG074ExactBaseOutcomesAndMetrics(t *testing.T) {
	universe := t.TempDir()
	tempBase := filepath.Join(universe, "private-temp")
	mustMkdir(t, tempBase)
	lim := contractLimits()

	nestedRoot := filepath.Join(universe, "nested", "nearer")
	nestedCWD := filepath.Join(nestedRoot, "a", "b", "c")
	mustMkdir(t, nestedCWD)
	writeDescriptor(t, nestedRoot)
	writeDescriptor(t, filepath.Join(universe, "nested"))
	writeDefinition(t, nestedRoot, "migrations/0001_initial.godj.json", oneCreateModelDocument())
	mig065 := runScenario(t, nestedCWD, []string{"migrations", "check"}, []string{"migrations"}, tempBase, lim, inProcessBackend{})

	implicitRoot := filepath.Join(universe, "explicit", "implicit")
	explicitRoot := filepath.Join(universe, "explicit", "selected")
	mustMkdir(t, filepath.Join(implicitRoot, "migrations"))
	writeDescriptor(t, implicitRoot)
	writeDescriptor(t, explicitRoot)
	writeDefinition(t, explicitRoot, "migrations/0001_initial.godj.json", oneCreateModelDocument())
	mig066 := runScenario(t, implicitRoot, []string{"migrations", "check", "--project", filepath.Join(explicitRoot, "godj.toml")}, []string{"migrations"}, tempBase, lim, inProcessBackend{})

	emptyRoot := filepath.Join(universe, "empty")
	writeDescriptor(t, emptyRoot)
	mustMkdir(t, filepath.Join(emptyRoot, "migrations"))
	mig067 := runScenario(t, emptyRoot, []string{"migrations", "check"}, []string{"migrations"}, tempBase, lim, inProcessBackend{})

	orderedRoot := filepath.Join(universe, "ordered")
	writeDescriptor(t, orderedRoot)
	writeDefinition(t, orderedRoot, "migrations/z/0001_initial.godj.json", lifecycleRootDocument())
	writeDefinition(t, orderedRoot, "migrations/a/0002_fields.godj.json", lifecycleTailDocument())
	writeDefinition(t, orderedRoot, "migrations/z/notes.txt", []byte("ignored"))
	mig068 := runScenario(t, orderedRoot, []string{"migrations", "check"}, []string{"migrations/z", "migrations/a"}, tempBase, lim, inProcessBackend{})

	unsafeRoot := filepath.Join(universe, "unsafe")
	writeDescriptor(t, unsafeRoot)
	mustMkdir(t, filepath.Join(unsafeRoot, "migrations"))
	if err := os.Symlink(filepath.Join(unsafeRoot, "godj.toml"), filepath.Join(unsafeRoot, "migrations", "link.godj.json")); err != nil {
		t.Fatalf("create unsafe source: %v", err)
	}
	mig069 := runScenario(t, unsafeRoot, []string{"migrations", "check"}, []string{"migrations"}, tempBase, lim, inProcessBackend{})

	missingRoot := filepath.Join(universe, "missing")
	missingCWD := filepath.Join(missingRoot, "a", "b", "c")
	mustMkdir(t, missingCWD)
	mig070 := runGlobal(globalInvocation{
		Context: context.Background(), CWD: missingCWD, Argv: []string{"migrations", "check"}, Limits: lim,
		Deps: globalDependencies{
			Backend:         &inProcessBackend{Limits: lim},
			CreateWorkspace: successfulTestWorkspace(tempBase),
			SelectionHooks:  selectionHooks{virtualFilesystemRoot: missingRoot},
		},
	})

	protocolRoot := filepath.Join(universe, "protocol")
	writeDescriptor(t, protocolRoot)
	versionTwo := []byte(`{"protocol_version":2,"status":"ok","result":{"source_count":0,"definition_count":0,"definition_set_digest":"` + emptySetDigest + `"}}`)
	mig071 := runScenario(t, protocolRoot, []string{"migrations", "check"}, nil, tempBase, lim, inProcessBackend{RunnerWire: versionTwo})

	buildRoot := filepath.Join(universe, "broken-build")
	writeDescriptorWithPackage(t, buildRoot, "./cmd/broken")
	mustMkdir(t, filepath.Join(buildRoot, "cmd", "broken"))
	if err := os.WriteFile(filepath.Join(buildRoot, "go.mod"), []byte("module example.invalid/godj-projectcheck-broken\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatalf("write broken fixture go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildRoot, "cmd", "broken", "main.go"), []byte("package main\nfunc main(\n"), 0o600); err != nil {
		t.Fatalf("write syntax-broken source: %v", err)
	}
	buildHome := filepath.Join(universe, "broken-build-home")
	mustMkdir(t, buildHome)
	buildTreeBefore := snapshotProjectTree(t, buildRoot)
	buildBackend := &concreteExecBackend{Limits: lim, Grace: 100 * time.Millisecond}
	mig072 := runGlobal(globalInvocation{
		Context: context.Background(), CWD: buildRoot, Argv: []string{"migrations", "check"}, Limits: lim,
		Deps: globalDependencies{
			Backend: buildBackend,
			CreateWorkspace: func(projectRoot string, observed *observation) (privateWorkspace, *failure) {
				return createPrivateWorkspace(projectRoot, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + buildHome, "TMPDIR=" + tempBase}, observed)
			},
		},
	})
	buildTreeAfter := snapshotProjectTree(t, buildRoot)
	if !reflect.DeepEqual(buildTreeAfter, buildTreeBefore) {
		t.Fatalf("MIG-072 actual -mod=readonly build changed project tree\nbefore=%v\nafter=%v", buildTreeBefore, buildTreeAfter)
	}
	physicalBuildRoot, err := filepath.EvalSymlinks(buildRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(buildBackend.LastBuild.Argv) != 6 || !reflect.DeepEqual(buildBackend.LastBuild.Argv[:4], []string{"go", "build", "-mod=readonly", "-o"}) || buildBackend.LastBuild.Argv[5] != "./cmd/broken" || buildBackend.LastBuild.Dir != physicalBuildRoot || buildBackend.LastRunner.Argv != nil || mig072.Feasibility.RawDiagnostics != nil {
		t.Fatalf("MIG-072 actual build boundary = build=%+v runner=%+v feasibility=%+v", buildBackend.LastBuild, buildBackend.LastRunner, mig072.Feasibility)
	}
	privateRoot := filepath.Dir(buildBackend.LastBuild.Argv[4])
	if _, err := os.Lstat(privateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MIG-072 private build output remained: %s: %v", privateRoot, err)
	}
	if _, err := os.Lstat(filepath.Join(buildRoot, "go.sum")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MIG-072 build published go.sum: %v", err)
	}
	buildStdout := mig072.Feasibility.Diagnostics["build_stdout"]
	buildStderr := mig072.Feasibility.Diagnostics["build_stderr"]
	if buildStdout.Truncated || buildStderr.Truncated || buildStdout.RetainedBytes+buildStderr.RetainedBytes == 0 {
		t.Fatalf("MIG-072 bounded compiler diagnostic scalar = stdout=%+v stderr=%+v", buildStdout, buildStderr)
	}
	if len(mig072.Stdout) != 0 || string(mig072.Stderr) != "migration_project_build_error/project_build_failed\n" || bytes.Contains(mig072.Stderr, []byte("syntax")) || bytes.Contains(mig072.Stderr, []byte(buildRoot)) || bytes.Contains(mig072.Stderr, []byte(privateRoot)) {
		t.Fatalf("MIG-072 public output leaked private compiler data: stdout=%q stderr=%q", mig072.Stdout, mig072.Stderr)
	}

	loadFailureRoot := filepath.Join(universe, "load-failure")
	writeDescriptor(t, loadFailureRoot)
	duplicate := bytes.Replace(oneCreateModelDocument(), []byte(`"name":"0001_initial"`), []byte(`"name":"0001_initial","name":"shadow"`), 1)
	writeDefinition(t, loadFailureRoot, "migrations/broken.godj.json", duplicate)
	mig073 := runScenario(t, loadFailureRoot, []string{"migrations", "check"}, []string{"migrations"}, tempBase, lim, inProcessBackend{})

	invalidResponseRoot := filepath.Join(universe, "invalid-response")
	writeDescriptor(t, invalidResponseRoot)
	duplicateStatus := []byte(`{"protocol_version":1,"status":"ok","status":"error","result":{"source_count":0,"definition_count":0,"definition_set_digest":"` + emptySetDigest + `"}}`)
	mig074 := runScenario(t, invalidResponseRoot, []string{"migrations", "check"}, nil, tempBase, lim, inProcessBackend{RunnerWire: duplicateStatus})

	scenarios := []baseScenario{
		{"MIG-065", mig065, wantBaseSuccess(1, 1, oneModelDigest, oracleMetrics{BuildCalls: 1, RunnerCalls: 1, RunnerResponseWrites: 1, SourceReads: 1, LoadCalls: 1, DocumentsReceived: 1, HeadersValidated: 1, OperationsDecoded: 1, PlannerConstruction: 1, DefinitionsPublished: 1, DefinitionSetsPublished: 1, UserStdoutWrites: 1, CommandDispatches: 1, AncestorDirectoriesInspected: 4, DescriptorReads: 1, RootsOpened: 1, DirectoryEntriesSeen: 1})},
		{"MIG-066", mig066, wantBaseSuccess(1, 1, oneModelDigest, oracleMetrics{BuildCalls: 1, RunnerCalls: 1, RunnerResponseWrites: 1, SourceReads: 1, LoadCalls: 1, DocumentsReceived: 1, HeadersValidated: 1, OperationsDecoded: 1, PlannerConstruction: 1, DefinitionsPublished: 1, DefinitionSetsPublished: 1, UserStdoutWrites: 1, CommandDispatches: 1, DescriptorReads: 1, RootsOpened: 1, DirectoryEntriesSeen: 1})},
		{"MIG-067", mig067, wantBaseSuccess(0, 0, emptySetDigest, oracleMetrics{BuildCalls: 1, RunnerCalls: 1, RunnerResponseWrites: 1, LoadCalls: 1, PlannerConstruction: 1, DefinitionSetsPublished: 1, UserStdoutWrites: 1, CommandDispatches: 1, AncestorDirectoriesInspected: 1, DescriptorReads: 1, RootsOpened: 1})},
		{"MIG-068", mig068, wantBaseSuccess(2, 2, twoModelDigest, oracleMetrics{BuildCalls: 1, RunnerCalls: 1, RunnerResponseWrites: 1, SourceReads: 2, LoadCalls: 1, DocumentsReceived: 2, HeadersValidated: 2, OperationsDecoded: 3, PlannerConstruction: 1, DefinitionsPublished: 2, DefinitionSetsPublished: 1, UserStdoutWrites: 1, CommandDispatches: 1, AncestorDirectoriesInspected: 1, DescriptorReads: 1, RootsOpened: 2, DirectoryEntriesSeen: 3})},
		{"MIG-069", mig069, wantBaseFailure("migration_definition_discovery_error", "unsafe_source_entry", oracleMetrics{BuildCalls: 1, RunnerCalls: 1, RunnerResponseWrites: 1, UserStderrWrites: 1, ExitCode: 1, CommandDispatches: 1, AncestorDirectoriesInspected: 1, DescriptorReads: 1, RootsOpened: 1, DirectoryEntriesSeen: 1})},
		{"MIG-070", mig070, wantBaseFailure("migration_project_selection_error", "project_not_found", oracleMetrics{UserStderrWrites: 1, ExitCode: 2, AncestorDirectoriesInspected: 4})},
		{"MIG-071", mig071, wantBaseFailure("migration_project_protocol_error", "project_protocol_incompatible", oracleMetrics{BuildCalls: 1, RunnerCalls: 1, RunnerResponseWrites: 1, UserStderrWrites: 1, ExitCode: 3, AncestorDirectoriesInspected: 1, DescriptorReads: 1})},
		{"MIG-072", mig072, wantBaseFailure("migration_project_build_error", "project_build_failed", oracleMetrics{BuildCalls: 1, UserStderrWrites: 1, ExitCode: 3, AncestorDirectoriesInspected: 1, DescriptorReads: 1})},
		{"MIG-073", mig073, wantLoadFailure()},
		{"MIG-074", mig074, wantBaseFailure("migration_project_protocol_error", "invalid_project_runner_response", oracleMetrics{BuildCalls: 1, RunnerCalls: 1, RunnerResponseWrites: 1, UserStderrWrites: 1, ExitCode: 3, AncestorDirectoriesInspected: 1, DescriptorReads: 1})},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.id, func(t *testing.T) {
			assertObservation(t, scenario.observed, scenario.want)
		})
	}

	if got := mig068.Result.DefinitionSetDigest; got != twoModelDigest {
		t.Fatalf("MIG-068 digest = %s, want %s", got, twoModelDigest)
	}
}

func runScenario(t *testing.T, cwd string, argv, roots []string, tempBase string, lim limits, configured inProcessBackend) observation {
	t.Helper()
	configured.Roots = roots
	configured.Limits = lim
	return runGlobal(globalInvocation{
		Context: context.Background(), CWD: cwd, Argv: argv, Limits: lim,
		Deps: globalDependencies{Backend: &configured, CreateWorkspace: successfulTestWorkspace(tempBase)},
	})
}

func wantBaseSuccess(sourceCount, definitionCount int, digest string, metrics oracleMetrics) observation {
	metrics.ExitCode = 0
	return observation{
		Result:      &checkResult{SourceCount: sourceCount, DefinitionCount: definitionCount, DefinitionSetDigest: digest},
		Metrics:     metrics,
		Feasibility: feasibilityMetrics{TempCreated: 1, TempCleanupAttempts: 1, DirectChildReaps: metrics.BuildCalls + metrics.RunnerCalls, Diagnostics: map[string]diagnosticScalar{}},
	}
}

func wantBaseFailure(category, code string, metrics oracleMetrics) observation {
	return observation{
		Failure:     &failure{Category: category, Code: code, ExitCode: metrics.ExitCode},
		Metrics:     metrics,
		Feasibility: feasibilityMetrics{TempCreated: boolInt(metrics.BuildCalls > 0), TempCleanupAttempts: boolInt(metrics.BuildCalls > 0), DirectChildReaps: metrics.BuildCalls + metrics.RunnerCalls, Diagnostics: map[string]diagnosticScalar{}},
	}
}

func wantLoadFailure() observation {
	detail := &failureContext{Stage: "document", SourceID: "migrations/broken.godj.json", JSONPointer: "/migration/name", OperationIndex: -1, Reason: "duplicate_key", GraphSources: []string{}}
	metrics := oracleMetrics{
		BuildCalls: 1, RunnerCalls: 1, RunnerResponseWrites: 1, SourceReads: 1, LoadCalls: 1,
		DocumentsReceived: 1, UserStderrWrites: 1, ExitCode: 1, CommandDispatches: 1,
		AncestorDirectoriesInspected: 1, DescriptorReads: 1, RootsOpened: 1, DirectoryEntriesSeen: 1,
		Failure: detail,
	}
	return observation{
		Failure:     &failure{Category: "migration_definition_source_error", Code: "invalid_definition_document", ExitCode: 1},
		Metrics:     metrics,
		Feasibility: feasibilityMetrics{TempCreated: 1, TempCleanupAttempts: 1, DirectChildReaps: 2, Diagnostics: map[string]diagnosticScalar{}},
	}
}

func assertObservation(t *testing.T, actual, expected observation) {
	t.Helper()
	actual.Stdout = nil
	actual.Stderr = nil
	expected.Stdout = nil
	expected.Stderr = nil
	actual.Feasibility.Diagnostics = nil
	expected.Feasibility.Diagnostics = nil
	if !reflect.DeepEqual(actual.Result, expected.Result) || !reflect.DeepEqual(actual.Failure, expected.Failure) || !reflect.DeepEqual(actual.Metrics, expected.Metrics) || !reflect.DeepEqual(actual.Feasibility, expected.Feasibility) {
		actualJSON, _ := json.MarshalIndent(actual, "", "  ")
		expectedJSON, _ := json.MarshalIndent(expected, "", "  ")
		t.Fatalf("observation mismatch\nactual=%s\nexpected=%s\nactual-feas=%+v\nexpected-feas=%+v", actualJSON, expectedJSON, actual.Feasibility, expected.Feasibility)
	}
	if actual.Metrics.DirectPlannerCalls != 0 || actual.Metrics.GoDjDBCalls != 0 || actual.Metrics.RevisionLifecycleCalls != 0 || actual.Metrics.PartialStdoutWrites != 0 {
		t.Fatalf("forbidden call/publication counter = %+v", actual.Metrics)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func writeDescriptor(t *testing.T, root string) {
	t.Helper()
	writeDescriptorWithPackage(t, root, "./cmd/mysite")
}

func writeDescriptorWithPackage(t *testing.T, root, packagePath string) {
	t.Helper()
	mustMkdir(t, root)
	if err := os.WriteFile(filepath.Join(root, "godj.toml"), canonicalDescriptor(packagePath), 0o600); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
}

func writeDefinition(t *testing.T, root, relative string, document []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatalf("write definition %s: %v", relative, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func oneCreateModelDocument() []byte {
	return []byte(`{
  "format_version":1,
  "producer":{"name":"godj-example-generator","version":"0.1.0"},
  "migration":{"app":"alpha","name":"0001_initial","dependencies":[],"operations":[
    {"kind":"create_model","app_label":"alpha","model":{"name":"widget","go_name":"Widget","db_table":"alpha_widget","fields":[
      {"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null}
    ]}}
  ]}
}`)
}

func lifecycleRootDocument() []byte {
	return []byte(`{"format_version":1,"producer":{"name":"godj-reference","version":"0.1.0"},"migration":{"app":"alpha","name":"0001_initial","dependencies":[],"operations":[{"kind":"create_model","app_label":"alpha","model":{"name":"entry","go_name":"Entry","db_table":"godj_definition_alpha_entry","fields":[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null},{"name":"title","go_name":"Title","column":"title","kind":"char","primary_key":false,"nullable":false,"max_length":64,"default":{"kind":"string","string":"untitled"}}]}}]}}`)
}

func lifecycleTailDocument() []byte {
	return []byte(`{"format_version":1,"producer":{"name":"godj-reference","version":"0.1.0"},"migration":{"app":"alpha","name":"0002_fields","dependencies":[{"app":"alpha","name":"0001_initial"}],"operations":[{"kind":"add_field","app_label":"alpha","model_name":"entry","field":{"name":"published","go_name":"Published","column":"published","kind":"boolean","primary_key":false,"nullable":false,"max_length":0,"default":{"kind":"boolean","boolean":false}}},{"kind":"add_field","app_label":"alpha","model_name":"entry","field":{"name":"summary","go_name":"Summary","column":"summary","kind":"char","primary_key":false,"nullable":true,"max_length":255,"default":null}}]}}`)
}

func TestCanonicalFilesystemOrderIgnoresRootAndEnumerationPermutation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDefinition(t, root, "migrations/z/0001_initial.godj.json", lifecycleRootDocument())
	writeDefinition(t, root, "migrations/a/0002_fields.godj.json", lifecycleTailDocument())
	writeDefinition(t, root, "migrations/z/notes.txt", []byte("ignored"))
	cases := []struct {
		roots []string
		hooks discoveryHooks
	}{
		{roots: []string{"migrations/z", "migrations/a"}},
		{roots: []string{"migrations/a", "migrations/z"}},
		{
			roots: []string{"migrations/z", "migrations/a"},
			hooks: discoveryHooks{enumerateRoot: func(root string, _ *os.File, yield func([]enumeratedEntry, error) bool) error {
				switch root {
				case "migrations/z":
					if !yield([]enumeratedEntry{{name: "notes.txt"}}, nil) {
						return nil
					}
					yield([]enumeratedEntry{{name: "0001_initial.godj.json"}}, io.EOF)
				case "migrations/a":
					yield([]enumeratedEntry{{name: "0002_fields.godj.json"}}, io.EOF)
				}
				return nil
			}},
		},
	}
	for _, test := range cases {
		metrics := oracleMetrics{}
		linked := invokeLinked(linkedInvocation{ProjectRoot: root, Roots: test.roots, Request: []byte(`{"protocol_version":1,"command":"migrations.check"}`), Limits: contractLimits(), Hooks: test.hooks, Metrics: &metrics})
		result, primary := parseRunnerResponse(linked.Wire, 0, contractLimits())
		if primary != nil || result.DefinitionSetDigest != twoModelDigest || metrics.SourceReads != 2 || metrics.DirectoryEntriesSeen != 3 {
			t.Fatalf("roots %v = result %+v failure %v metrics %+v", test.roots, result, primary, metrics)
		}
	}
}

func TestLoadFailurePublishesNoPartialSetAndStripsDetailFromWire(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	duplicate := bytes.Replace(oneCreateModelDocument(), []byte(`"name":"0001_initial"`), []byte(`"name":"0001_initial","name":"shadow"`), 1)
	writeDefinition(t, root, "migrations/broken.godj.json", duplicate)
	metrics := oracleMetrics{}
	linked := invokeLinked(linkedInvocation{ProjectRoot: root, Roots: []string{"migrations"}, Request: []byte(`{"protocol_version":1,"command":"migrations.check"}`), Limits: contractLimits(), Metrics: &metrics})
	if linked.SetPublished || linked.FailureDetail == nil || linked.FailureDetail.Stage != "document" || linked.FailureDetail.JSONPointer != "/migration/name" || linked.FailureDetail.Reason != "duplicate_key" {
		t.Fatalf("load failure detail/publication = %+v", linked)
	}
	if bytes.Contains(linked.Wire, []byte("broken.godj.json")) || bytes.Contains(linked.Wire, []byte("duplicate_key")) || !bytes.Contains(linked.Wire, []byte("invalid_definition_document")) {
		t.Fatalf("wire leaked or lost detail: %s", linked.Wire)
	}
	if metrics.DefinitionsPublished != 0 || metrics.DefinitionSetsPublished != 0 || metrics.LoadCalls != 1 || metrics.PlannerConstruction != 0 {
		t.Fatalf("partial load metrics = %+v", metrics)
	}
}

func TestBaseOutputPublicationIsExactlyOnceAfterCleanup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDescriptor(t, root)
	mustMkdir(t, filepath.Join(root, "migrations"))
	tempBase := t.TempDir()
	observed := runScenario(t, root, []string{"migrations", "check"}, []string{"migrations"}, tempBase, contractLimits(), inProcessBackend{})
	if observed.Metrics.UserStdoutWrites != 1 || observed.Metrics.UserStderrWrites != 0 || observed.Metrics.PartialStdoutWrites != 0 || len(observed.Stdout) == 0 || len(observed.Stderr) != 0 || observed.Feasibility.TempCleanupAttempts != 1 || observed.Feasibility.ResidualTemp != 0 {
		t.Fatalf("success publication = %+v", observed)
	}
	if !strings.HasSuffix(string(observed.Stdout), "\n") {
		t.Fatalf("stdout is not a single line: %q", observed.Stdout)
	}
	if _, err := os.Stat(filepath.Dir(root)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture unexpectedly changed: %v", err)
	}
}

type recordingPublicationSink struct {
	writes int
	bytes.Buffer
	short bool
}

func (sink *recordingPublicationSink) Write(payload []byte) (int, error) {
	sink.writes++
	if sink.short && len(payload) > 1 {
		written := len(payload) / 2
		_, _ = sink.Buffer.Write(payload[:written])
		return written, io.ErrShortWrite
	}
	return sink.Buffer.Write(payload)
}

func TestHealthyInjectedPublicationIsOnceAndStrictlyAfterCleanup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDescriptor(t, root)
	mustMkdir(t, filepath.Join(root, "migrations"))
	base := t.TempDir()
	stdout := &recordingPublicationSink{}
	stderr := &recordingPublicationSink{}
	events := make([]string, 0, 3)
	observed := runGlobal(globalInvocation{
		Context: context.Background(), CWD: root, Argv: []string{"migrations", "check"}, Limits: contractLimits(),
		Deps: globalDependencies{
			Backend:         &inProcessBackend{Roots: []string{"migrations"}, Limits: contractLimits()},
			CreateWorkspace: successfulTestWorkspace(base),
			Publication: publicationHarness{
				stdout: stdout,
				stderr: stderr,
				event:  func(event string) { events = append(events, event) },
			},
		},
	})
	if observed.Failure != nil || stdout.writes != 1 || stderr.writes != 0 || !bytes.Equal(stdout.Bytes(), observed.Stdout) || observed.Metrics.PartialStdoutWrites != 0 {
		t.Fatalf("healthy publication = observed=%+v stdout=%q/%d stderr=%q/%d", observed, stdout.Bytes(), stdout.writes, stderr.Bytes(), stderr.writes)
	}
	wantEvents := []string{"cleanup.start", "cleanup.complete", "publish.stdout"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("publication lifecycle events = %v, want %v", events, wantEvents)
	}
}

func TestHealthyInjectedErrorPublicationIsOnceAndStrictlyAfterCleanup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDescriptor(t, root)
	base := t.TempDir()
	stdout := &recordingPublicationSink{}
	stderr := &recordingPublicationSink{}
	events := make([]string, 0, 3)
	observed := runGlobal(globalInvocation{
		Context: context.Background(), CWD: root, Argv: []string{"migrations", "check"}, Limits: contractLimits(),
		Deps: globalDependencies{
			Backend:         &inProcessBackend{BuildFailure: true, Limits: contractLimits()},
			CreateWorkspace: successfulTestWorkspace(base),
			Publication: publicationHarness{
				stdout: stdout,
				stderr: stderr,
				event:  func(event string) { events = append(events, event) },
			},
		},
	})
	if observed.Failure == nil || observed.Failure.Code != "project_build_failed" || stdout.writes != 0 || stderr.writes != 1 || !bytes.Equal(stderr.Bytes(), observed.Stderr) {
		t.Fatalf("healthy error publication = observed=%+v stdout=%q/%d stderr=%q/%d", observed, stdout.Bytes(), stdout.writes, stderr.Bytes(), stderr.writes)
	}
	wantEvents := []string{"cleanup.start", "cleanup.complete", "publish.stderr"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("error publication lifecycle events = %v, want %v", events, wantEvents)
	}
}

func TestFailedOutputSinkRecordsPartialAttemptWithoutAtomicDeliveryClaim(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDescriptor(t, root)
	mustMkdir(t, filepath.Join(root, "migrations"))
	stdout := &recordingPublicationSink{short: true}
	observed := runGlobal(globalInvocation{
		Context: context.Background(), CWD: root, Argv: []string{"migrations", "check"}, Limits: contractLimits(),
		Deps: globalDependencies{
			Backend:         &inProcessBackend{Roots: []string{"migrations"}, Limits: contractLimits()},
			CreateWorkspace: successfulTestWorkspace(t.TempDir()),
			Publication:     publicationHarness{stdout: stdout},
		},
	})
	if stdout.writes != 1 || observed.Metrics.UserStdoutWrites != 1 || observed.Metrics.PartialStdoutWrites != 1 || len(observed.Stdout) == 0 || !bytes.Equal(observed.Stdout, stdout.Bytes()) {
		t.Fatalf("partial sink evidence = observed=%+v sink=%q/%d", observed, stdout.Bytes(), stdout.writes)
	}
}
