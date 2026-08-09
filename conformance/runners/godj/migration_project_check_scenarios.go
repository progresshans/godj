//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	conformanceprotocol "github.com/progresshans/godj/conformance/internal/protocol"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	productprotocol "github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/migrations/definition"
)

type migrationProjectCheckFixture struct {
	phase              conformanceprotocol.Phase
	cleanupRoot        string
	cwd                string
	argv               []string
	roots              []string
	injectedRunnerWire []byte
	inspectBuildSyntax bool
}

type migrationProjectCheckExecution struct {
	observation  conformanceprotocol.Observation
	globalReport productcheck.Report
	linkedReport linked.Report
	linkedCalls  int
}

var migrationProjectCheckFixtures = map[string]func() (migrationProjectCheckFixture, error){
	"godj.migration.project_check.nested_project_success":        migrationProjectCheckNestedFixture,
	"godj.migration.project_check.explicit_project_override":     migrationProjectCheckExplicitFixture,
	"godj.migration.project_check.empty_catalog":                 migrationProjectCheckEmptyFixture,
	"godj.migration.project_check.canonical_filesystem_order":    migrationProjectCheckCanonicalOrderFixture,
	"godj.migration.project_check.unsafe_source_entry":           migrationProjectCheckUnsafeSourceFixture,
	"godj.migration.project_check.project_not_found":             migrationProjectCheckMissingProjectFixture,
	"godj.migration.project_check.project_protocol_incompatible": migrationProjectCheckProtocolFixture,
	"godj.migration.project_check.project_build_failure_atomic":  migrationProjectCheckBuildFailureFixture,
	"godj.migration.project_check.definition_load_failure":       migrationProjectCheckLoadFailureFixture,
	"godj.migration.project_check.invalid_runner_response":       migrationProjectCheckInvalidResponseFixture,
}

func migrationProjectCheckScenario(
	ctx context.Context,
	contract conformanceprotocol.Contract,
) (conformanceprotocol.Observation, error) {
	factory, ok := migrationProjectCheckFixtures[contract.Scenario]
	if !ok {
		return conformanceprotocol.Observation{}, fmt.Errorf("unsupported migration project-check scenario %q", contract.Scenario)
	}
	fixture, err := factory()
	if err != nil {
		if fixture.cleanupRoot != "" {
			err = errors.Join(err, os.RemoveAll(fixture.cleanupRoot))
		}
		return conformanceprotocol.Observation{}, fmt.Errorf("create migration project-check scenario %q: %w", contract.Scenario, err)
	}
	if fixture.phase != contract.Phase {
		cleanupErr := os.RemoveAll(fixture.cleanupRoot)
		return conformanceprotocol.Observation{}, errors.Join(fmt.Errorf(
			"migration project-check scenario %q phase = %q, manifest requires %q",
			contract.Scenario,
			fixture.phase,
			contract.Phase,
		), cleanupErr)
	}
	execution, runErr := runMigrationProjectCheckFixture(ctx, contract.ID, fixture)
	cleanupErr := os.RemoveAll(fixture.cleanupRoot)
	if runErr != nil || cleanupErr != nil {
		return conformanceprotocol.Observation{}, errors.Join(runErr, cleanupErr)
	}
	return execution.observation, nil
}

func runMigrationProjectCheckFixture(
	ctx context.Context,
	contractID string,
	fixture migrationProjectCheckFixture,
) (migrationProjectCheckExecution, error) {
	if ctx == nil {
		return migrationProjectCheckExecution{}, errors.New("migration project-check scenario: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return migrationProjectCheckExecution{}, err
	}
	backend := &migrationProjectCheckBackend{
		roots:              append([]string(nil), fixture.roots...),
		injectedRunnerWire: append([]byte(nil), fixture.injectedRunnerWire...),
		inspectBuildSyntax: fixture.inspectBuildSyntax,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	globalReport := productcheck.Run(productcheck.Invocation{
		Context: ctx,
		CWD:     fixture.cwd,
		Args:    append([]string(nil), fixture.argv...),
		Stdout:  &stdout,
		Stderr:  &stderr,
		Backend: backend,
	})
	if backend.err != nil {
		return migrationProjectCheckExecution{}, backend.err
	}
	if backend.linkedCalls < 0 || backend.linkedCalls > 1 {
		return migrationProjectCheckExecution{}, fmt.Errorf(
			"migration project-check linked calls = %d, want at most one",
			backend.linkedCalls,
		)
	}
	if fixture.injectedRunnerWire == nil && globalReport.RunnerCalls != backend.linkedCalls {
		return migrationProjectCheckExecution{}, fmt.Errorf(
			"migration project-check runner/linked calls = %d/%d, want identical without injected wire",
			globalReport.RunnerCalls,
			backend.linkedCalls,
		)
	}
	if fixture.injectedRunnerWire != nil && (globalReport.RunnerCalls != 1 || backend.linkedCalls != 0) {
		return migrationProjectCheckExecution{}, fmt.Errorf(
			"migration project-check injected runner/linked calls = %d/%d, want 1/0",
			globalReport.RunnerCalls,
			backend.linkedCalls,
		)
	}
	observation, err := migrationProjectCheckObservation(contractID, fixture.phase, globalReport, backend.linkedReport)
	if err != nil {
		return migrationProjectCheckExecution{}, err
	}
	return migrationProjectCheckExecution{
		observation:  observation,
		globalReport: globalReport,
		linkedReport: backend.linkedReport,
		linkedCalls:  backend.linkedCalls,
	}, nil
}

type migrationProjectCheckBackend struct {
	roots              []string
	injectedRunnerWire []byte
	inspectBuildSyntax bool

	linkedReport linked.Report
	linkedCalls  int
	err          error
}

func (backend *migrationProjectCheckBackend) Execute(
	ctx context.Context,
	_ <-chan struct{},
	stage productcheck.ProcessStage,
	command productcheck.Command,
) productcheck.ProcessResult {
	switch stage {
	case productcheck.BuildStage:
		if backend.inspectBuildSyntax {
			failed, err := migrationProjectCheckBuildHasSyntaxFailure(command)
			if err != nil {
				backend.err = err
				return productcheck.ProcessResult{}
			}
			if failed {
				return productcheck.ProcessResult{
					Started:      true,
					ExitCode:     1,
					DirectReaps:  1,
					StderrScalar: productcheck.StreamScalar{RetainedBytes: 1},
				}
			}
		}
		return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	case productcheck.RunnerStage:
		if backend.injectedRunnerWire != nil {
			wire := append([]byte(nil), backend.injectedRunnerWire...)
			return productcheck.ProcessResult{
				Started:      true,
				ExitCode:     0,
				Stdout:       wire,
				StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(wire)},
				DirectReaps:  1,
			}
		}
		backend.linkedCalls++
		if backend.linkedCalls != 1 {
			backend.err = errors.New("migration project-check backend invoked linked runner more than once")
			return productcheck.ProcessResult{}
		}
		if len(command.Argv) != 2 {
			backend.err = fmt.Errorf("migration project-check runner argv length = %d, want 2", len(command.Argv))
			return productcheck.ProcessResult{}
		}
		var response bytes.Buffer
		report, err := linked.Run(
			ctx,
			linked.Config{
				ProjectRoot:              command.Dir,
				MigrationDefinitionRoots: append([]string(nil), backend.roots...),
			},
			command.Argv[1:],
			bytes.NewReader(command.Stdin),
			&response,
		)
		backend.linkedReport = report
		if err != nil {
			backend.err = fmt.Errorf("migration project-check linked runner: %w", err)
			return productcheck.ProcessResult{}
		}
		wire := append([]byte(nil), response.Bytes()...)
		return productcheck.ProcessResult{
			Started:      true,
			ExitCode:     0,
			Stdout:       wire,
			StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(wire)},
			DirectReaps:  1,
		}
	default:
		backend.err = fmt.Errorf("migration project-check backend stage = %d", stage)
		return productcheck.ProcessResult{}
	}
}

func migrationProjectCheckBuildHasSyntaxFailure(command productcheck.Command) (bool, error) {
	if len(command.Argv) == 0 {
		return false, errors.New("migration project-check build has no argv")
	}
	packagePath := command.Argv[len(command.Argv)-1]
	if !strings.HasPrefix(packagePath, "./") {
		return false, fmt.Errorf("migration project-check build package = %q", packagePath)
	}
	directory := filepath.Join(command.Dir, filepath.FromSlash(strings.TrimPrefix(packagePath, "./")))
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, fmt.Errorf("read migration project-check build package: %w", err)
	}
	parsed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		parsed++
		_, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, entry.Name()), nil, parser.AllErrors)
		if parseErr != nil {
			return true, nil
		}
	}
	return parsed == 0, nil
}

func migrationProjectCheckDuplicateStatus(document []byte) ([]byte, error) {
	marker := []byte(`"status":"ok"`)
	index := bytes.Index(document, marker)
	if index < 0 {
		return nil, errors.New("migration project-check runner response has no success status member")
	}
	duplicate := []byte(`"status":"ok","status":"error"`)
	result := make([]byte, 0, len(document)+len(duplicate)-len(marker))
	result = append(result, document[:index]...)
	result = append(result, duplicate...)
	result = append(result, document[index+len(marker):]...)
	return result, nil
}

func migrationProjectCheckEmptyRunnerWire() ([]byte, error) {
	return productprotocol.EncodeResponse(productprotocol.Response{
		OK: true,
		Result: productprotocol.Result{
			DefinitionSetDigest: productprotocol.EmptySetDigest,
		},
	})
}

func migrationProjectCheckProtocolV2RunnerWire() []byte {
	return []byte(fmt.Sprintf(
		`{"protocol_version":2,"status":"ok","result":{"source_count":0,"definition_count":0,"definition_set_digest":%q}}`,
		productprotocol.EmptySetDigest,
	))
}

func migrationProjectCheckObservation(
	contractID string,
	phase conformanceprotocol.Phase,
	global productcheck.Report,
	linkedReport linked.Report,
) (conformanceprotocol.Observation, error) {
	if global.HasResult == global.HasFailure {
		return conformanceprotocol.Observation{}, errors.New("migration project-check global report has ambiguous outcome")
	}
	observation := conformanceprotocol.Observation{
		ID:      contractID,
		Status:  conformanceprotocol.StatusObserved,
		Phase:   phase,
		Metrics: valuePointer(migrationProjectCheckMetrics(global, linkedReport)),
	}
	if global.HasFailure {
		observation.Error = &conformanceprotocol.ObservedError{
			Category:          global.Failure.Category,
			Code:              global.Failure.Code,
			MessageIsContract: boolPointer(false),
		}
		return observation, nil
	}
	observation.Result = valuePointer(conformanceprotocol.Object(map[string]conformanceprotocol.Value{
		"definition_count":      conformanceprotocol.Integer(strconv.Itoa(global.Result.DefinitionCount)),
		"definition_set_digest": conformanceprotocol.String(global.Result.DefinitionSetDigest),
		"source_count":          conformanceprotocol.Integer(strconv.Itoa(global.Result.SourceCount)),
	}))
	return observation, nil
}

func migrationProjectCheckMetrics(global productcheck.Report, linkedReport linked.Report) conformanceprotocol.Value {
	failure := conformanceprotocol.Null()
	if linkedReport.HasLoadFailure {
		failure = migrationProjectCheckFailureValue(linkedReport.LoadFailure)
	}
	return conformanceprotocol.Object(map[string]conformanceprotocol.Value{
		"ancestor_directories_inspected": conformanceprotocol.Integer(strconv.Itoa(global.AncestorDirectoriesInspected)),
		"build_calls":                    conformanceprotocol.Integer(strconv.Itoa(global.BuildCalls)),
		"command_dispatches":             conformanceprotocol.Integer(strconv.Itoa(linkedReport.CommandDispatches)),
		"definition_sets_published":      conformanceprotocol.Integer(strconv.Itoa(linkedReport.DefinitionSetsPublished)),
		"definitions_published":          conformanceprotocol.Integer(strconv.Itoa(linkedReport.DefinitionsPublished)),
		"descriptor_reads":               conformanceprotocol.Integer(strconv.Itoa(global.DescriptorReads)),
		"direct_planner_calls":           conformanceprotocol.Integer(strconv.Itoa(linkedReport.DirectPlannerCalls)),
		"directory_entries_seen":         conformanceprotocol.Integer(strconv.Itoa(linkedReport.DirectoryEntriesSeen)),
		"documents_received":             conformanceprotocol.Integer(strconv.Itoa(linkedReport.DocumentsReceived)),
		"exit_code":                      conformanceprotocol.Integer(strconv.Itoa(global.ExitCode)),
		"failure":                        failure,
		"godj_db_calls":                  conformanceprotocol.Integer(strconv.Itoa(linkedReport.GoDjDBCalls)),
		"headers_validated":              conformanceprotocol.Integer(strconv.Itoa(linkedReport.HeadersValidated)),
		"load_calls":                     conformanceprotocol.Integer(strconv.Itoa(linkedReport.LoadCalls)),
		"operations_decoded":             conformanceprotocol.Integer(strconv.Itoa(linkedReport.OperationsDecoded)),
		"partial_stdout_writes":          conformanceprotocol.Integer(strconv.Itoa(global.PartialStdoutWrites)),
		"planner_construction":           conformanceprotocol.Integer(strconv.Itoa(linkedReport.PlannerConstruction)),
		"revision_lifecycle_calls":       conformanceprotocol.Integer(strconv.Itoa(linkedReport.RevisionLifecycleCalls)),
		"roots_opened":                   conformanceprotocol.Integer(strconv.Itoa(linkedReport.RootsOpened)),
		"runner_calls":                   conformanceprotocol.Integer(strconv.Itoa(global.RunnerCalls)),
		"runner_response_writes":         conformanceprotocol.Integer(strconv.Itoa(global.RunnerResponseWrites)),
		"source_reads":                   conformanceprotocol.Integer(strconv.Itoa(linkedReport.SourceReads)),
		"user_stderr_writes":             conformanceprotocol.Integer(strconv.Itoa(global.UserStderrWrites)),
		"user_stdout_writes":             conformanceprotocol.Integer(strconv.Itoa(global.UserStdoutWrites)),
	})
}

func migrationProjectCheckFailureValue(context definition.FailureContext) conformanceprotocol.Value {
	graphSources := context.GraphSources()
	graphValues := make([]conformanceprotocol.Value, len(graphSources))
	for index, source := range graphSources {
		graphValues[index] = conformanceprotocol.Object(map[string]conformanceprotocol.Value{
			"app":       conformanceprotocol.String(source.Migration.App),
			"name":      conformanceprotocol.String(source.Migration.Name),
			"source_id": conformanceprotocol.String(source.SourceID),
		})
	}
	return conformanceprotocol.Object(map[string]conformanceprotocol.Value{
		"actual":          conformanceprotocol.Integer(strconv.FormatUint(context.Actual, 10)),
		"app":             conformanceprotocol.String(context.App),
		"graph_sources":   conformanceprotocol.List(graphValues...),
		"json_pointer":    conformanceprotocol.String(context.JSONPointer),
		"limit":           conformanceprotocol.String(context.Limit),
		"maximum":         conformanceprotocol.Integer(strconv.FormatUint(context.Maximum, 10)),
		"name":            conformanceprotocol.String(context.Name),
		"operation_index": conformanceprotocol.Integer(strconv.Itoa(context.OperationIndex)),
		"reason":          conformanceprotocol.String(context.Reason),
		"source_id":       conformanceprotocol.String(context.SourceID),
		"stage":           conformanceprotocol.String(context.Stage),
	})
}

func migrationProjectCheckNestedFixture() (migrationProjectCheckFixture, error) {
	fixture, root, err := migrationProjectCheckBaseFixture("nested")
	if err != nil {
		return fixture, err
	}
	nearer := filepath.Join(root, "nested", "nearer")
	fixture.cwd = filepath.Join(nearer, "a", "b", "c")
	fixture.roots = []string{"migrations"}
	if err := migrationProjectCheckMkdir(fixture.cwd); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDescriptor(filepath.Join(root, "nested"), "./cmd/mysite"); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDescriptor(nearer, "./cmd/mysite"); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDefinition(nearer, "migrations/0001_initial.godj.json", migrationProjectCheckOneModelDocument()); err != nil {
		return fixture, err
	}
	return fixture, nil
}

func migrationProjectCheckExplicitFixture() (migrationProjectCheckFixture, error) {
	fixture, root, err := migrationProjectCheckBaseFixture("explicit")
	if err != nil {
		return fixture, err
	}
	implicit := filepath.Join(root, "implicit")
	selected := filepath.Join(root, "selected")
	fixture.cwd = implicit
	fixture.roots = []string{"migrations"}
	if err := migrationProjectCheckWriteDescriptor(implicit, "./cmd/mysite"); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDescriptor(selected, "./cmd/mysite"); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDefinition(selected, "migrations/0001_initial.godj.json", migrationProjectCheckOneModelDocument()); err != nil {
		return fixture, err
	}
	fixture.argv = []string{"migrations", "check", "--project", filepath.Join(selected, "godj.toml")}
	return fixture, nil
}

func migrationProjectCheckEmptyFixture() (migrationProjectCheckFixture, error) {
	fixture, root, err := migrationProjectCheckBaseFixture("empty")
	if err != nil {
		return fixture, err
	}
	fixture.phase = conformanceprotocol.PhaseConstruction
	fixture.cwd = root
	fixture.roots = []string{"migrations"}
	if err := migrationProjectCheckWriteDescriptor(root, "./cmd/mysite"); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckMkdir(filepath.Join(root, "migrations")); err != nil {
		return fixture, err
	}
	return fixture, nil
}

func migrationProjectCheckCanonicalOrderFixture() (migrationProjectCheckFixture, error) {
	fixture, root, err := migrationProjectCheckBaseFixture("ordered")
	if err != nil {
		return fixture, err
	}
	fixture.phase = conformanceprotocol.PhaseConstruction
	fixture.cwd = root
	fixture.roots = []string{"migrations/z", "migrations/a"}
	if err := migrationProjectCheckWriteDescriptor(root, "./cmd/mysite"); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDefinition(root, "migrations/z/0001_initial.godj.json", migrationDefinitionMarshal(migrationDefinitionRootDocument(), false)); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDefinition(root, "migrations/a/0002_fields.godj.json", migrationDefinitionMarshal(migrationDefinitionTailDocument(), false)); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDefinition(root, "migrations/z/notes.txt", []byte("ignored")); err != nil {
		return fixture, err
	}
	return fixture, nil
}

func migrationProjectCheckUnsafeSourceFixture() (migrationProjectCheckFixture, error) {
	fixture, root, err := migrationProjectCheckBaseFixture("unsafe")
	if err != nil {
		return fixture, err
	}
	fixture.phase = conformanceprotocol.PhaseConstruction
	fixture.cwd = root
	fixture.roots = []string{"migrations"}
	if err := migrationProjectCheckWriteDescriptor(root, "./cmd/mysite"); err != nil {
		return fixture, err
	}
	directory := filepath.Join(root, "migrations")
	if err := migrationProjectCheckMkdir(directory); err != nil {
		return fixture, err
	}
	if err := os.Symlink(filepath.Join(root, "godj.toml"), filepath.Join(directory, "link.godj.json")); err != nil {
		return fixture, fmt.Errorf("create migration project-check source symlink: %w", err)
	}
	return fixture, nil
}

func migrationProjectCheckMissingProjectFixture() (migrationProjectCheckFixture, error) {
	root, err := os.MkdirTemp("/tmp", "godj-conformance-project-missing-")
	if err != nil {
		return migrationProjectCheckFixture{}, err
	}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		_ = os.RemoveAll(root)
		return migrationProjectCheckFixture{}, err
	}
	for migrationProjectCheckRootDepth(physical) < 4 {
		root = filepath.Join(root, "nested")
		if err := migrationProjectCheckMkdir(root); err != nil {
			return migrationProjectCheckFixture{cleanupRoot: filepath.Dir(root)}, err
		}
		physical, err = filepath.EvalSymlinks(root)
		if err != nil {
			return migrationProjectCheckFixture{cleanupRoot: root}, err
		}
	}
	if depth := migrationProjectCheckRootDepth(physical); depth != 4 {
		return migrationProjectCheckFixture{cleanupRoot: root}, fmt.Errorf("migration project-check missing-project physical depth = %d, want 4", depth)
	}
	cleanupRoot := root
	for filepath.Base(cleanupRoot) == "nested" {
		cleanupRoot = filepath.Dir(cleanupRoot)
	}
	return migrationProjectCheckFixture{
		phase:       conformanceprotocol.PhaseEnvironment,
		cleanupRoot: cleanupRoot,
		cwd:         root,
		argv:        []string{"migrations", "check"},
	}, nil
}

func migrationProjectCheckProtocolFixture() (migrationProjectCheckFixture, error) {
	fixture, root, err := migrationProjectCheckBaseFixture("protocol")
	if err != nil {
		return fixture, err
	}
	fixture.cwd = root
	fixture.injectedRunnerWire = migrationProjectCheckProtocolV2RunnerWire()
	if err := migrationProjectCheckWriteDescriptor(root, "./cmd/mysite"); err != nil {
		return fixture, err
	}
	return fixture, nil
}

func migrationProjectCheckBuildFailureFixture() (migrationProjectCheckFixture, error) {
	fixture, root, err := migrationProjectCheckBaseFixture("build")
	if err != nil {
		return fixture, err
	}
	fixture.cwd = root
	fixture.inspectBuildSyntax = true
	if err := migrationProjectCheckWriteDescriptor(root, "./cmd/broken"); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDefinition(root, "go.mod", []byte("module example.invalid/godj-project-check\n\ngo 1.26.0\n")); err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDefinition(root, "cmd/broken/main.go", []byte("package main\nfunc main(\n")); err != nil {
		return fixture, err
	}
	return fixture, nil
}

func migrationProjectCheckLoadFailureFixture() (migrationProjectCheckFixture, error) {
	fixture, root, err := migrationProjectCheckBaseFixture("load")
	if err != nil {
		return fixture, err
	}
	fixture.phase = conformanceprotocol.PhaseConstruction
	fixture.cwd = root
	fixture.roots = []string{"migrations"}
	if err := migrationProjectCheckWriteDescriptor(root, "./cmd/mysite"); err != nil {
		return fixture, err
	}
	document := bytes.Replace(
		migrationProjectCheckOneModelDocument(),
		[]byte(`"name":"0001_initial"`),
		[]byte(`"name":"0001_initial","name":"shadow"`),
		1,
	)
	if err := migrationProjectCheckWriteDefinition(root, "migrations/broken.godj.json", document); err != nil {
		return fixture, err
	}
	return fixture, nil
}

func migrationProjectCheckInvalidResponseFixture() (migrationProjectCheckFixture, error) {
	fixture, root, err := migrationProjectCheckBaseFixture("response")
	if err != nil {
		return fixture, err
	}
	fixture.cwd = root
	wire, err := migrationProjectCheckEmptyRunnerWire()
	if err != nil {
		return fixture, err
	}
	fixture.injectedRunnerWire, err = migrationProjectCheckDuplicateStatus(wire)
	if err != nil {
		return fixture, err
	}
	if err := migrationProjectCheckWriteDescriptor(root, "./cmd/mysite"); err != nil {
		return fixture, err
	}
	return fixture, nil
}

func migrationProjectCheckBaseFixture(name string) (migrationProjectCheckFixture, string, error) {
	root, err := os.MkdirTemp("", "godj-conformance-project-"+name+"-")
	if err != nil {
		return migrationProjectCheckFixture{}, "", err
	}
	return migrationProjectCheckFixture{
		phase:       conformanceprotocol.PhaseEnvironment,
		cleanupRoot: root,
		cwd:         root,
		argv:        []string{"migrations", "check"},
	}, root, nil
}

func migrationProjectCheckWriteDescriptor(root, packagePath string) error {
	if err := migrationProjectCheckMkdir(root); err != nil {
		return err
	}
	document := []byte("format_version = 1\n[project]\npackage = \"" + packagePath + "\"\n")
	if err := os.WriteFile(filepath.Join(root, "godj.toml"), document, 0o600); err != nil {
		return fmt.Errorf("write migration project-check descriptor: %w", err)
	}
	return nil
}

func migrationProjectCheckWriteDefinition(root, relative string, document []byte) error {
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := migrationProjectCheckMkdir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.WriteFile(path, document, 0o600); err != nil {
		return fmt.Errorf("write migration project-check fixture %s: %w", relative, err)
	}
	return nil
}

func migrationProjectCheckMkdir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("mkdir migration project-check fixture %s: %w", path, err)
	}
	return nil
}

func migrationProjectCheckRootDepth(path string) int {
	depth := 0
	for {
		depth++
		parent := filepath.Dir(path)
		if parent == path {
			return depth
		}
		path = parent
	}
}

func migrationProjectCheckOneModelDocument() []byte {
	return []byte(`{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"producer":{"name":"godj-example-generator","version":"0.1.0"},"migration":{"app":"alpha","name":"0001_initial","dependencies":[],"operations":[{"kind":"create_model","app_label":"alpha","model":{"name":"widget","go_name":"Widget","db_table":"alpha_widget","fields":[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null}]}}]}}`)
}
