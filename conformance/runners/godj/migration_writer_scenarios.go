//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/internal/protocol"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	checkprotocol "github.com/progresshans/godj/internal/projectcheck/protocol"
	generateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
	"github.com/progresshans/godj/internal/projectmigration"
	writerprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const migrationWriterProcessProbe = "godj-migration-writer-determinism-v1"

type migrationWriterRegistration struct {
	id      string
	phase   protocol.Phase
	handler scenarioHandler
}

var migrationWriterScenarioRegistry = map[string]migrationWriterRegistration{
	"django.migration.writer.no_changes_clean": {
		id: "MIG-099", phase: protocol.PhaseConstruction, handler: migrationWriterNoChanges,
	},
	"django.migration.writer.fresh_initial": {
		id: "MIG-100", phase: protocol.PhaseConstruction, handler: migrationWriterFreshInitial,
	},
	"django.migration.writer.repeat_after_initial_noop": {
		id: "MIG-101", phase: protocol.PhaseConstruction, handler: migrationWriterRepeatNoop,
	},
	"godj.migration.writer.deterministic_candidate": {
		id: "MIG-102", phase: protocol.PhaseConstruction, handler: migrationWriterDeterministicCandidate,
	},
	"django.migration.writer.relation_dependency_topology": {
		id: "MIG-103", phase: protocol.PhaseConstruction, handler: migrationWriterRelationTopology,
	},
	"django.migration.writer.additive_model_and_field_tail": {
		id: "MIG-104", phase: protocol.PhaseConstruction, handler: migrationWriterAdditiveTail,
	},
	"django.migration.writer.dry_run_no_mutation": {
		id: "MIG-105", phase: protocol.PhaseEnvironment, handler: migrationWriterDryRun,
	},
	"django.migration.writer.check_clean_and_drift": {
		id: "MIG-106", phase: protocol.PhaseEnvironment, handler: migrationWriterCheck,
	},
	"godj.migration.writer.unsupported_delta_fail_closed": {
		id: "MIG-107", phase: protocol.PhaseConstruction, handler: migrationWriterUnsupported,
	},
	"godj.migration.writer.snapshot_and_protocol_boundary": {
		id: "MIG-108", phase: protocol.PhaseEnvironment, handler: migrationWriterProtocolBoundary,
	},
	"godj.migration.writer.atomic_concurrent_publication": {
		id: "MIG-109", phase: protocol.PhaseCommit, handler: migrationWriterConcurrentPublication,
	},
	"godj.migration.writer.interruption_recovery_and_roundtrip": {
		id: "MIG-110", phase: protocol.PhaseRollback, handler: migrationWriterInterruptionRecovery,
	},
}

func migrationWriterScenarioHandler(scenario string) (scenarioHandler, bool) {
	registration, ok := migrationWriterScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		if ctx == nil {
			return protocol.Observation{}, errors.New("migration-writer scenario context is nil")
		}
		if contract.ID != registration.id {
			return protocol.Observation{}, fmt.Errorf("migration-writer scenario %q contract id %q; want %q", scenario, contract.ID, registration.id)
		}
		if contract.Scenario != scenario {
			return protocol.Observation{}, fmt.Errorf("migration-writer scenario %q contract scenario %q", scenario, contract.Scenario)
		}
		if contract.Phase != registration.phase {
			return protocol.Observation{}, fmt.Errorf("migration-writer scenario %q phase %q; want %q", scenario, contract.Phase, registration.phase)
		}
		return registration.handler(ctx, contract)
	}, true
}

type migrationWriterProject struct {
	universe    string
	root        string
	environment []string
	inventory   []byte
}

func newMigrationWriterProject() (migrationWriterProject, error) {
	universe, err := os.MkdirTemp("", "godj-migration-writer-actual-")
	if err != nil {
		return migrationWriterProject{}, fmt.Errorf("create migration-writer project: %w", err)
	}
	temporaryUniverse := universe
	universe, err = filepath.EvalSymlinks(temporaryUniverse)
	if err == nil {
		universe, err = filepath.Abs(universe)
	}
	if err != nil {
		return migrationWriterProject{}, errors.Join(fmt.Errorf("resolve migration-writer project: %w", err), os.RemoveAll(temporaryUniverse))
	}
	project := migrationWriterProject{universe: universe, root: filepath.Join(universe, "project")}
	fail := func(cause error) (migrationWriterProject, error) {
		return migrationWriterProject{}, errors.Join(cause, os.RemoveAll(universe))
	}
	for _, relative := range []string{"project", "project/cmd/site", "project/migrations", "home", "config", "cache", "tmp"} {
		if err := os.MkdirAll(filepath.Join(universe, relative), 0o700); err != nil {
			return fail(fmt.Errorf("create migration-writer directory: %w", err))
		}
	}
	files := map[string][]byte{
		"godj.toml":        []byte("format_version = 1\n\n[project]\npackage = \"./cmd/site\"\n"),
		"go.mod":           []byte("module example.invalid/migrationwriter\n\ngo 1.26.0\n"),
		"cmd/site/main.go": []byte("package main\nvar migrationWriterValue = 1\n"),
	}
	for relative, document := range files {
		if err := os.WriteFile(filepath.Join(project.root, filepath.FromSlash(relative)), document, 0o640); err != nil {
			return fail(fmt.Errorf("write migration-writer project: %w", err))
		}
	}
	project.environment = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(universe, "home"),
		"XDG_CONFIG_HOME=" + filepath.Join(universe, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(universe, "cache"),
		"TMPDIR=" + filepath.Join(universe, "tmp"),
		"GOTELEMETRY=off",
	}
	inventoryContext, cancelInventory := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelInventory()
	inventoryCommand := exec.CommandContext(inventoryContext, "go", "list", "-deps", "-json", "-mod=readonly", "./cmd/site")
	inventoryCommand.Dir = project.root
	inventoryCommand.Env = append(os.Environ(), "GOTELEMETRY=off")
	var inventoryStderr bytes.Buffer
	inventoryCommand.Stderr = &inventoryStderr
	project.inventory, err = inventoryCommand.Output()
	if err != nil {
		return fail(fmt.Errorf("capture migration-writer inventory: %w", err))
	}
	return project, nil
}

func (project migrationWriterProject) close() error { return os.RemoveAll(project.universe) }

type migrationWriterBackend struct {
	project *migrationWriterProject
	spec    codegen.ProjectSpec

	mu            sync.Mutex
	runnerCalls   int
	linkedReports []linked.MakemigrationsReport
	wires         [][]byte
	err           error
}

func (backend *migrationWriterBackend) Execute(
	ctx context.Context,
	_ <-chan struct{},
	stage productcheck.ProcessStage,
	command productcheck.Command,
) productcheck.ProcessResult {
	switch stage {
	case productcheck.MakemigrationsInventoryStage:
		document := append([]byte(nil), backend.project.inventory...)
		return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: document, StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(document)}}
	case productcheck.BuildStage:
		return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	case productcheck.MakemigrationsRunnerStage:
		if len(command.Argv) != 2 || command.Argv[1] != writerprotocol.PrivateArgument || !bytes.Equal(command.Stdin, writerprotocol.RequestDocument()) {
			backend.recordError(errors.New("invalid migration-writer runner command"))
			return productcheck.ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
		}
		backend.mu.Lock()
		backend.runnerCalls++
		backend.mu.Unlock()
		var stdout bytes.Buffer
		report, err := linked.RunMakemigrations(ctx, linked.MakemigrationsConfig{
			ProjectRoot: backend.project.root, MigrationDefinitionRoots: []string{"migrations"},
			LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) { return backend.spec, nil },
		}, append([]string(nil), command.Argv[1:]...), bytes.NewReader(command.Stdin), &stdout)
		document := append([]byte(nil), stdout.Bytes()...)
		backend.mu.Lock()
		backend.linkedReports = append(backend.linkedReports, report)
		backend.wires = append(backend.wires, append([]byte(nil), document...))
		if backend.err == nil {
			backend.err = err
		}
		backend.mu.Unlock()
		if err != nil {
			return productcheck.ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
		}
		return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: document, StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(document)}}
	default:
		backend.recordError(fmt.Errorf("unexpected migration-writer process stage %d", stage))
		return productcheck.ProcessResult{Started: true, ExitCode: 1, DirectReaps: 1}
	}
}

func (backend *migrationWriterBackend) recordError(err error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.err == nil {
		backend.err = err
	}
}

func (backend *migrationWriterBackend) snapshot() (int, []linked.MakemigrationsReport, [][]byte, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	reports := append([]linked.MakemigrationsReport(nil), backend.linkedReports...)
	wires := make([][]byte, len(backend.wires))
	for index := range backend.wires {
		wires[index] = append([]byte(nil), backend.wires[index]...)
	}
	return backend.runnerCalls, reports, wires, backend.err
}

type migrationWriterExecution struct {
	report productcheck.MakemigrationsReport
	stdout []byte
	stderr []byte
}

func runMigrationWriter(
	ctx context.Context,
	project *migrationWriterProject,
	backend *migrationWriterBackend,
	args []string,
) migrationWriterExecution {
	var stdout, stderr bytes.Buffer
	report := productcheck.RunMakemigrations(productcheck.MakemigrationsInvocation{
		Context: ctx, CWD: project.root, Args: append([]string(nil), args...), Environment: append([]string(nil), project.environment...),
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	})
	return migrationWriterExecution{report: report, stdout: append([]byte(nil), stdout.Bytes()...), stderr: append([]byte(nil), stderr.Bytes()...)}
}

func runMigrationWriterFinalCatalogBarrier(
	ctx context.Context,
	project *migrationWriterProject,
	backend *migrationWriterBackend,
	barrier *productcheck.MakemigrationsConformanceFinalCatalogBarrier,
) (migrationWriterExecution, error) {
	var stdout, stderr bytes.Buffer
	report, err := productcheck.RunMakemigrationsConformanceFinalCatalog(productcheck.MakemigrationsInvocation{
		Context: ctx, CWD: project.root, Args: []string{"makemigrations"}, Environment: append([]string(nil), project.environment...),
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	}, barrier)
	return migrationWriterExecution{report: report, stdout: append([]byte(nil), stdout.Bytes()...), stderr: append([]byte(nil), stderr.Bytes()...)}, err
}

func runMigrationWriterFault(
	ctx context.Context,
	project *migrationWriterProject,
	backend *migrationWriterBackend,
	fault productcheck.MakemigrationsConformanceFault,
) (migrationWriterExecution, error) {
	var stdout, stderr bytes.Buffer
	report, err := productcheck.RunMakemigrationsConformanceFault(productcheck.MakemigrationsInvocation{
		Context: ctx, CWD: project.root, Args: []string{"makemigrations"}, Environment: append([]string(nil), project.environment...),
		Stdout: &stdout, Stderr: &stderr, Backend: backend,
	}, fault)
	return migrationWriterExecution{report: report, stdout: append([]byte(nil), stdout.Bytes()...), stderr: append([]byte(nil), stderr.Bytes()...)}, err
}

func migrationWriterSpec(apps ...codegen.AppSpec) codegen.ProjectSpec {
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.invalid/migrationwriter/generated/project", Directory: "generated/project"},
		Apps:    apps,
	}
}

func migrationWriterApp(label string, models ...ir.Model) codegen.AppSpec {
	return codegen.AppSpec{
		Alias: label,
		Package: codegen.PackageSpec{
			PackageName: label,
			ImportPath:  "example.invalid/migrationwriter/generated/" + label,
			Directory:   "generated/" + label,
		},
		Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: label, Models: models},
	}
}

func migrationWriterArticle(extra ...ir.Field) ir.Model {
	fields := []ir.Field{
		{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
		{Name: "published", GoName: "Published", Kind: ir.FieldBoolean, Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false}},
	}
	fields = append(fields, extra...)
	return ir.Model{Name: "article", GoName: "Article", Fields: fields}
}

func migrationWriterAuthor() ir.Model {
	return ir.Model{Name: "author", GoName: "Author", Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 64}}}
}

func migrationWriterCategory() ir.Model {
	return ir.Model{Name: "category", GoName: "Category", Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 64}}}
}

func migrationWriterForeignKey(
	name, goName, targetApp, targetModel string,
	nullable bool,
	policy ir.DeletePolicy,
) ir.Field {
	return ir.Field{
		Name: name, GoName: goName, Kind: ir.FieldForeignKey, Nullable: nullable,
		Relation: &ir.ForeignKeyRelation{
			Target: ir.ModelIdentity{AppLabel: targetApp, ModelName: targetModel}, Cardinality: ir.RelationManyToOne,
			Reverse: ir.ReverseRelation{Name: "articles"}, OnDelete: policy,
		},
	}
}

func migrationWriterBaseSpec() codegen.ProjectSpec {
	return migrationWriterSpec(migrationWriterApp("blog", migrationWriterArticle()))
}

func migrationWriterSameAppSpec() codegen.ProjectSpec {
	return migrationWriterSpec(migrationWriterApp(
		"blog",
		migrationWriterAuthor(),
		migrationWriterArticle(migrationWriterForeignKey("author", "AuthorID", "blog", "author", false, ir.DeleteProtect)),
	))
}

func migrationWriterCrossAppSpec() codegen.ProjectSpec {
	return migrationWriterSpec(
		migrationWriterApp("authors", migrationWriterAuthor()),
		migrationWriterApp("blog", migrationWriterArticle(migrationWriterForeignKey("author", "AuthorID", "authors", "author", false, ir.DeleteProtect))),
	)
}

func migrationWriterAdditiveSpec() codegen.ProjectSpec {
	return migrationWriterSpec(migrationWriterApp(
		"blog",
		migrationWriterArticle(
			ir.Field{Name: "summary", GoName: "Summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200},
			migrationWriterForeignKey("category", "CategoryID", "blog", "category", true, ir.DeleteSetNull),
		),
		migrationWriterCategory(),
	))
}

func migrationWriterSnapshot(root string, spec codegen.ProjectSpec) (projectmigration.Snapshot, error) {
	sources, err := migrationWriterVisibleSources(root)
	if err != nil {
		return projectmigration.Snapshot{}, err
	}
	return projectmigration.BuildSnapshot(projectmigration.Request{
		ProjectSpec: spec, FilesystemSources: sources, ProgrammaticSources: []definition.Source{}, WriterRoot: "migrations",
	})
}

func migrationWriterSnapshotFromSources(spec codegen.ProjectSpec, sources []definition.Source) (projectmigration.Snapshot, error) {
	owned := cloneMigrationWriterSources(sources)
	return projectmigration.BuildSnapshot(projectmigration.Request{
		ProjectSpec: spec, FilesystemSources: owned, ProgrammaticSources: []definition.Source{}, WriterRoot: "migrations",
	})
}

func migrationWriterVisibleSources(root string) ([]definition.Source, error) {
	directory := filepath.Join(root, "migrations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	sources := make([]definition.Source, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".godj.json") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("unsafe migration-writer source %q", entry.Name())
		}
		document, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		sources = append(sources, definition.Source{SourceID: filepath.ToSlash(filepath.Join("migrations", entry.Name())), Document: document})
	}
	sort.Slice(sources, func(left, right int) bool { return sources[left].SourceID < sources[right].SourceID })
	return sources, nil
}

type migrationWriterVisibleSourceSeal struct {
	source definition.Source
	info   os.FileInfo
}

func migrationWriterVisibleSourceSeals(root string) ([]migrationWriterVisibleSourceSeal, error) {
	sources, err := migrationWriterVisibleSources(root)
	if err != nil {
		return nil, err
	}
	seals := make([]migrationWriterVisibleSourceSeal, 0, len(sources))
	for _, source := range sources {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(source.SourceID)))
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("unsafe migration-writer source %q", source.SourceID)
		}
		seals = append(seals, migrationWriterVisibleSourceSeal{source: source, info: info})
	}
	return seals, nil
}

func cloneMigrationWriterSources(sources []definition.Source) []definition.Source {
	cloned := make([]definition.Source, len(sources))
	for index := range sources {
		cloned[index] = definition.Source{SourceID: sources[index].SourceID, Document: append([]byte(nil), sources[index].Document...)}
	}
	return cloned
}

func migrationWriterCandidateSources(snapshot projectmigration.Snapshot) []definition.Source {
	candidates := snapshot.Candidates()
	sources := make([]definition.Source, len(candidates))
	for index := range candidates {
		basename := candidates[index].App() + "_" + candidates[index].Name() + ".godj.json"
		sources[index] = definition.Source{
			SourceID: filepath.ToSlash(filepath.Join(snapshot.WriterRoot(), basename)), Document: candidates[index].Document(),
		}
	}
	return sources
}

func migrationWriterWriteSources(root string, sources []definition.Source) error {
	for _, source := range sources {
		relative := filepath.FromSlash(source.SourceID)
		basename := filepath.Base(relative)
		if filepath.Clean(relative) != relative || filepath.ToSlash(relative) != source.SourceID || filepath.Dir(relative) != "migrations" || basename == "." || basename == string(filepath.Separator) {
			return fmt.Errorf("invalid migration-writer source id %q", source.SourceID)
		}
		if err := os.WriteFile(filepath.Join(root, "migrations", basename), source.Document, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func migrationWriterPublishInitial(ctx context.Context, project *migrationWriterProject, spec codegen.ProjectSpec) (migrationWriterExecution, error) {
	backend := &migrationWriterBackend{project: project, spec: spec}
	execution := runMigrationWriter(ctx, project, backend, []string{"makemigrations"})
	_, _, _, backendErr := backend.snapshot()
	if backendErr != nil {
		return execution, backendErr
	}
	if execution.report.ExitCode != 0 || !execution.report.HasMakemigrationsResult || execution.report.MakemigrationsResult.Status != "generated" {
		return execution, fmt.Errorf("publish initial migration: report=%+v stdout=%q stderr=%q", execution.report, execution.stdout, execution.stderr)
	}
	return execution, nil
}

func migrationWriterStrictState(root string) (migrations.ProjectState, int, error) {
	sources, err := migrationWriterVisibleSources(root)
	if err != nil {
		return migrations.ProjectState{}, 0, err
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		return migrations.ProjectState{}, len(sources), err
	}
	reconstructor, err := migrations.NewStateReconstructor(loaded.Definitions()...)
	if err != nil {
		return migrations.ProjectState{}, len(sources), err
	}
	state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	return state, len(sources), err
}

func migrationWriterFileRoster(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func migrationWriterReservedTemps(root string) (int, error) {
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".godj-makemigrations-tmp-v1-") {
			count++
		}
	}
	return count, nil
}

func migrationWriterLines(document []byte) protocol.Value {
	text := strings.TrimSuffix(string(document), "\n")
	if text == "" {
		return protocol.List()
	}
	parts := strings.Split(text, "\n")
	values := make([]protocol.Value, len(parts))
	for index := range parts {
		values[index] = protocol.String(parts[index])
	}
	return protocol.List(values...)
}

func migrationWriterStrings(values []string) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index := range values {
		items[index] = protocol.String(values[index])
	}
	return protocol.List(items...)
}

func migrationWriterInt(value int) protocol.Value { return protocol.Integer(strconv.Itoa(value)) }

func migrationWriterObservation(id string, phase protocol.Phase, result, metrics protocol.Value) protocol.Observation {
	return protocol.Observation{ID: id, Status: protocol.StatusObserved, Phase: phase, Result: &result, Metrics: &metrics}
}

func migrationWriterMigrationValues(snapshot projectmigration.Snapshot) ([]protocol.Value, error) {
	sources := snapshot.FilesystemSources()
	sources = append(sources, snapshot.ProgrammaticSources()...)
	sources = append(sources, migrationWriterCandidateSources(snapshot)...)
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		return nil, fmt.Errorf("strict-load migration-writer candidates: %w", err)
	}
	definitions := make(map[migrations.MigrationKey]migrations.Migration)
	for _, migration := range loaded.Definitions() {
		definitions[migration.Key()] = migration
	}
	targets := make(map[ir.ModelIdentity]string)
	for _, app := range snapshot.ProjectSpec().Apps {
		for _, model := range app.Schema.Models {
			targets[ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}] = model.GoName
		}
	}
	candidates := snapshot.Candidates()
	values := make([]protocol.Value, len(candidates))
	for index := range candidates {
		key := migrations.MigrationKey{App: candidates[index].App(), Name: candidates[index].Name()}
		migration, ok := definitions[key]
		if !ok {
			return nil, fmt.Errorf("migration-writer candidate %s.%s missing after strict load", key.App, key.Name)
		}
		value, err := migrationWriterMigrationValue(migration, targets)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	return values, nil
}

func migrationWriterMigrationValue(migration migrations.Migration, targets map[ir.ModelIdentity]string) (protocol.Value, error) {
	dependencies := make([]protocol.Value, len(migration.Dependencies))
	for index, dependency := range migration.Dependencies {
		dependencies[index] = protocol.Object(map[string]protocol.Value{
			"app": protocol.String(dependency.App), "name": protocol.String(dependency.Name),
		})
	}
	operations := make([]protocol.Value, len(migration.Operations))
	for index, operation := range migration.Operations {
		switch value := operation.(type) {
		case migrations.CreateModel:
			fields := make([]protocol.Value, len(value.Model.Fields))
			for fieldIndex := range value.Model.Fields {
				field, err := migrationWriterFieldValue(value.Model.Fields[fieldIndex], targets)
				if err != nil {
					return protocol.Value{}, err
				}
				fields[fieldIndex] = field
			}
			operations[index] = protocol.Object(map[string]protocol.Value{
				"kind": protocol.String("CreateModel"), "model": protocol.String(value.Model.GoName), "fields": protocol.List(fields...),
			})
		case migrations.AddField:
			field, err := migrationWriterFieldValue(value.Field, targets)
			if err != nil {
				return protocol.Value{}, err
			}
			operations[index] = protocol.Object(map[string]protocol.Value{
				"kind": protocol.String("AddField"), "model": protocol.String(value.ModelName), "field": field,
			})
		default:
			return protocol.Value{}, fmt.Errorf("unsupported migration-writer observation operation %T", operation)
		}
	}
	return protocol.Object(map[string]protocol.Value{
		"app":          protocol.String(migration.App),
		"dependencies": protocol.List(dependencies...),
		"initial":      protocol.Boolean(migration.Name == "0001_initial"),
		"name":         protocol.String(migration.Name),
		"operations":   protocol.List(operations...),
	}), nil
}

func migrationWriterFieldValue(field ir.Field, targets map[ir.ModelIdentity]string) (protocol.Value, error) {
	switch field.Kind {
	case ir.FieldAuto:
		return protocol.Object(map[string]protocol.Value{
			"kind": protocol.String("auto"), "name": protocol.String(field.Name),
			"nullable": protocol.Boolean(field.Nullable), "primary_key": protocol.Boolean(field.PrimaryKey),
		}), nil
	case ir.FieldChar:
		return protocol.Object(map[string]protocol.Value{
			"kind": protocol.String("char"), "name": protocol.String(field.Name),
			"nullable": protocol.Boolean(field.Nullable), "max_length": migrationWriterInt(field.MaxLength),
		}), nil
	case ir.FieldBoolean:
		if field.Default == nil || field.Default.Kind != ir.ScalarBoolean {
			return protocol.Value{}, fmt.Errorf("migration-writer boolean field %q has no explicit boolean default", field.Name)
		}
		return protocol.Object(map[string]protocol.Value{
			"kind": protocol.String("boolean"), "name": protocol.String(field.Name),
			"nullable": protocol.Boolean(field.Nullable), "default": protocol.Boolean(field.Default.Boolean),
		}), nil
	case ir.FieldForeignKey:
		if field.Relation == nil {
			return protocol.Value{}, fmt.Errorf("migration-writer foreign key %q has no relation", field.Name)
		}
		targetName, ok := targets[field.Relation.Target]
		if !ok {
			return protocol.Value{}, fmt.Errorf("migration-writer foreign key %q target is unknown", field.Name)
		}
		var onDelete string
		switch field.Relation.OnDelete {
		case ir.DeleteProtect:
			onDelete = "PROTECT"
		case ir.DeleteSetNull:
			onDelete = "SET_NULL"
		default:
			return protocol.Value{}, fmt.Errorf("migration-writer foreign key %q has unknown delete policy %q", field.Name, field.Relation.OnDelete)
		}
		return protocol.Object(map[string]protocol.Value{
			"kind": protocol.String("foreign_key"), "name": protocol.String(field.Name),
			"nullable": protocol.Boolean(field.Nullable), "on_delete": protocol.String(onDelete),
			"target": protocol.String(field.Relation.Target.AppLabel + "." + targetName),
		}), nil
	default:
		return protocol.Value{}, fmt.Errorf("unsupported migration-writer field kind %q", field.Kind)
	}
}

func migrationWriterNoChanges(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	project, err := newMigrationWriterProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer project.close()
	if _, err := migrationWriterPublishInitial(ctx, &project, migrationWriterBaseSpec()); err != nil {
		return protocol.Observation{}, err
	}
	snapshot, err := migrationWriterSnapshot(project.root, migrationWriterBaseSpec())
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"candidate_count": migrationWriterInt(len(snapshot.Candidates())),
		"clean":           protocol.Boolean(len(snapshot.Candidates()) == 0),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"database_opens": migrationWriterInt(0), "detector_calls": migrationWriterInt(1), "writes": migrationWriterInt(0),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}

func migrationWriterFreshInitial(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	snapshot, err := migrationWriterSnapshotFromSources(migrationWriterBaseSpec(), nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	migrationValues, err := migrationWriterMigrationValues(snapshot)
	if err != nil {
		return protocol.Observation{}, err
	}
	operations := 0
	loaded, _, err := definition.Load(migrationWriterCandidateSources(snapshot)...)
	if err != nil {
		return protocol.Observation{}, err
	}
	for _, migration := range loaded.Definitions() {
		operations += len(migration.Operations)
	}
	result := protocol.Object(map[string]protocol.Value{"migrations": protocol.List(migrationValues...)})
	metrics := protocol.Object(map[string]protocol.Value{
		"candidate_count": migrationWriterInt(len(snapshot.Candidates())), "database_opens": migrationWriterInt(0), "operation_count": migrationWriterInt(operations),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}

func migrationWriterRepeatNoop(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	project, err := newMigrationWriterProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer project.close()
	if _, err := migrationWriterPublishInitial(ctx, &project, migrationWriterBaseSpec()); err != nil {
		return protocol.Observation{}, err
	}
	before, err := migrationWriterVisibleSources(project.root)
	if err != nil {
		return protocol.Observation{}, err
	}
	snapshot, err := migrationWriterSnapshot(project.root, migrationWriterBaseSpec())
	if err != nil {
		return protocol.Observation{}, err
	}
	after, err := migrationWriterVisibleSources(project.root)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"candidate_count":      migrationWriterInt(len(snapshot.Candidates())),
		"prior_source_mutated": protocol.Boolean(!reflect.DeepEqual(before, after)),
		"repeat_is_noop":       protocol.Boolean(len(snapshot.Candidates()) == 0),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"database_opens": migrationWriterInt(0), "detector_calls": migrationWriterInt(1), "writes": migrationWriterInt(0),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}

func migrationWriterDeterministicSpec(reverse bool, withSummary bool) codegen.ProjectSpec {
	article := migrationWriterArticle()
	if withSummary {
		article = migrationWriterArticle(ir.Field{Name: "summary", GoName: "Summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200})
	}
	blog := migrationWriterApp("blog", article)
	auxiliary := migrationWriterApp("zzaux", ir.Model{
		Name: "marker", GoName: "Marker", Fields: []ir.Field{{Name: "value", GoName: "Value", Kind: ir.FieldChar, MaxLength: 16}},
	})
	return migrationWriterSpec(blog, auxiliary)
}

func migrationWriterDeterministicDocument(reverse bool) ([]byte, string, error) {
	initial, err := migrationWriterSnapshotFromSources(migrationWriterDeterministicSpec(reverse, false), nil)
	if err != nil {
		return nil, "", err
	}
	sources := migrationWriterCandidateSources(initial)
	if reverse {
		for left, right := 0, len(sources)-1; left < right; left, right = left+1, right-1 {
			sources[left], sources[right] = sources[right], sources[left]
		}
	}
	pending, err := migrationWriterSnapshotFromSources(migrationWriterDeterministicSpec(reverse, true), sources)
	if err != nil {
		return nil, "", err
	}
	for _, candidate := range pending.Candidates() {
		if candidate.App() == "blog" {
			return candidate.Document(), candidate.App() + "_" + candidate.Name() + ".godj.json", nil
		}
	}
	return nil, "", errors.New("migration-writer deterministic candidate is missing")
}

func init() {
	if os.Getenv("GODJ_MIGRATION_WRITER_PROCESS_PROBE") != migrationWriterProcessProbe {
		return
	}
	document, filename, err := migrationWriterDeterministicDocument(false)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "migration-writer process probe failed")
		os.Exit(2)
	}
	payload := struct {
		Document string `json:"document"`
		Filename string `json:"filename"`
	}{Document: base64.StdEncoding.EncodeToString(document), Filename: filename}
	if err := json.NewEncoder(os.Stdout).Encode(payload); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func migrationWriterDifferentProcessDocument(ctx context.Context) ([]byte, string, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	command := exec.CommandContext(ctx, executable)
	command.Env = append(os.Environ(), "GODJ_MIGRATION_WRITER_PROCESS_PROBE="+migrationWriterProcessProbe)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, "", fmt.Errorf("migration-writer different-process probe: %w", err)
	}
	var payload struct {
		Document string `json:"document"`
		Filename string `json:"filename"`
	}
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, "", fmt.Errorf("decode migration-writer process probe: %w", err)
	}
	document, err := base64.StdEncoding.DecodeString(payload.Document)
	if err != nil {
		return nil, "", fmt.Errorf("decode migration-writer process document: %w", err)
	}
	return document, payload.Filename, nil
}

func migrationWriterTimestampFields(document []byte) (int, error) {
	var decoded any
	if err := json.Unmarshal(document, &decoded); err != nil {
		return 0, err
	}
	count := 0
	var walk func(any)
	walk = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			for key, nested := range item {
				lower := strings.ToLower(key)
				if strings.Contains(lower, "timestamp") || strings.Contains(lower, "created_at") || strings.Contains(lower, "generated_at") {
					count++
				}
				walk(nested)
			}
		case []any:
			for _, nested := range item {
				walk(nested)
			}
		}
	}
	walk(decoded)
	return count, nil
}

func migrationWriterDeterministicCandidate(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	normal, filename, err := migrationWriterDeterministicDocument(false)
	if err != nil {
		return protocol.Observation{}, err
	}
	reversed, reversedFilename, err := migrationWriterDeterministicDocument(true)
	if err != nil {
		return protocol.Observation{}, err
	}
	separate, separateFilename, err := migrationWriterDifferentProcessDocument(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	later, laterFilename, err := migrationWriterDeterministicDocument(false)
	if err != nil {
		return protocol.Observation{}, err
	}
	if filename != reversedFilename || filename != separateFilename || filename != laterFilename {
		return protocol.Observation{}, errors.New("migration-writer deterministic filenames differ")
	}
	documents := [][]byte{normal, reversed, separate, later}
	names := []string{"normal", "reverse_input", "different_process", "different_time"}
	cases := make([]protocol.Value, len(documents))
	distinct := make(map[string]struct{})
	for index := range documents {
		digest := sha256.Sum256(documents[index])
		distinct[string(documents[index])] = struct{}{}
		cases[index] = protocol.Object(map[string]protocol.Value{
			"case": protocol.String(names[index]), "document": protocol.Bytes(documents[index]),
			"sha256": protocol.String(fmt.Sprintf("sha256:%x", digest)),
		})
	}
	timestampFields, err := migrationWriterTimestampFields(normal)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(cases...), "filename": protocol.String(filename),
		"roster": protocol.List(protocol.String(filename)), "timestamp_fields": migrationWriterInt(timestampFields),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"candidate_documents": migrationWriterInt(1), "distinct_documents": migrationWriterInt(len(distinct)),
		"input_permutations": migrationWriterInt(len(documents)), "random_values": migrationWriterInt(0),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}

func migrationWriterRelationTopology(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	tests := []struct {
		name string
		spec codegen.ProjectSpec
	}{{name: "same_app", spec: migrationWriterSameAppSpec()}, {name: "cross_app", spec: migrationWriterCrossAppSpec()}}
	cases := make([]protocol.Value, len(tests))
	migrationCount := 0
	for index, test := range tests {
		snapshot, err := migrationWriterSnapshotFromSources(test.spec, nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		values, err := migrationWriterMigrationValues(snapshot)
		if err != nil {
			return protocol.Observation{}, err
		}
		migrationCount += len(values)
		cases[index] = protocol.Object(map[string]protocol.Value{
			"case": protocol.String(test.name), "migrations": protocol.List(values...),
		})
	}
	result := protocol.Object(map[string]protocol.Value{"cases": protocol.List(cases...)})
	metrics := protocol.Object(map[string]protocol.Value{
		"cases": migrationWriterInt(len(tests)), "database_opens": migrationWriterInt(0), "migrations": migrationWriterInt(migrationCount),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}

func migrationWriterAdditiveTail(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	initial, err := migrationWriterSnapshotFromSources(migrationWriterBaseSpec(), nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	pending, err := migrationWriterSnapshotFromSources(migrationWriterAdditiveSpec(), migrationWriterCandidateSources(initial))
	if err != nil {
		return protocol.Observation{}, err
	}
	values, err := migrationWriterMigrationValues(pending)
	if err != nil {
		return protocol.Observation{}, err
	}
	operationCount := 0
	sources := append(initial.FilesystemSources(), migrationWriterCandidateSources(initial)...)
	sources = append(sources, migrationWriterCandidateSources(pending)...)
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		return protocol.Observation{}, err
	}
	for _, candidate := range pending.Candidates() {
		for _, migration := range loaded.Definitions() {
			if migration.App == candidate.App() && migration.Name == candidate.Name() {
				operationCount += len(migration.Operations)
			}
		}
	}
	result := protocol.Object(map[string]protocol.Value{"migrations": protocol.List(values...)})
	metrics := protocol.Object(map[string]protocol.Value{
		"candidate_count": migrationWriterInt(len(pending.Candidates())), "database_opens": migrationWriterInt(0), "operation_count": migrationWriterInt(operationCount),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}

func migrationWriterCommandCase(
	ctx context.Context,
	project *migrationWriterProject,
	spec codegen.ProjectSpec,
	action string,
	args []string,
) (protocol.Value, int, error) {
	before, err := migrationWriterFileRoster(project.root)
	if err != nil {
		return protocol.Value{}, 0, err
	}
	backend := &migrationWriterBackend{project: project, spec: spec}
	execution := runMigrationWriter(ctx, project, backend, args)
	runnerCalls, _, _, backendErr := backend.snapshot()
	if backendErr != nil {
		return protocol.Value{}, 0, backendErr
	}
	after, err := migrationWriterFileRoster(project.root)
	if err != nil {
		return protocol.Value{}, 0, err
	}
	if !reflect.DeepEqual(before, after) {
		return protocol.Value{}, 0, fmt.Errorf("migration-writer %s mutated files: before=%v after=%v", action, before, after)
	}
	return protocol.Object(map[string]protocol.Value{
		"action":        protocol.String(action),
		"exit_code":     migrationWriterInt(execution.report.ExitCode),
		"files_after":   migrationWriterStrings(after),
		"files_before":  migrationWriterStrings(before),
		"output":        migrationWriterLines(execution.stdout),
		"stderr":        migrationWriterLines(execution.stderr),
		"tables_after":  protocol.List(),
		"tables_before": protocol.List(),
	}), runnerCalls, nil
}

func migrationWriterDryRun(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	project, err := newMigrationWriterProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer project.close()
	value, workers, err := migrationWriterCommandCase(
		ctx, &project, migrationWriterBaseSpec(), "dry_run", []string{"makemigrations", "--dry-run"},
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	metrics := protocol.Object(map[string]protocol.Value{
		"database_schema_mutations": migrationWriterInt(0), "filesystem_mutations": migrationWriterInt(0), "worker_processes": migrationWriterInt(workers),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, value, metrics), nil
}

func migrationWriterCheck(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	cleanProject, err := newMigrationWriterProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer cleanProject.close()
	if _, err := migrationWriterPublishInitial(ctx, &cleanProject, migrationWriterBaseSpec()); err != nil {
		return protocol.Observation{}, err
	}
	clean, cleanWorkers, err := migrationWriterCommandCase(
		ctx, &cleanProject, migrationWriterBaseSpec(), "check_clean", []string{"makemigrations", "--check"},
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	driftProject, err := newMigrationWriterProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer driftProject.close()
	drift, driftWorkers, err := migrationWriterCommandCase(
		ctx, &driftProject, migrationWriterBaseSpec(), "check_drift", []string{"makemigrations", "--check"},
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{"cases": protocol.List(clean, drift)})
	metrics := protocol.Object(map[string]protocol.Value{
		"database_schema_mutations": migrationWriterInt(0), "filesystem_mutations": migrationWriterInt(0),
		"worker_processes": migrationWriterInt(cleanWorkers + driftWorkers),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}

func migrationWriterInitialSources(spec codegen.ProjectSpec) ([]definition.Source, error) {
	snapshot, err := migrationWriterSnapshotFromSources(spec, nil)
	if err != nil {
		return nil, err
	}
	return migrationWriterCandidateSources(snapshot), nil
}

func migrationWriterNoncanonicalSource() ([]definition.Source, error) {
	initial, err := migrationWriterSnapshotFromSources(migrationWriterBaseSpec(), nil)
	if err != nil {
		return nil, err
	}
	loaded, _, err := definition.Load(migrationWriterCandidateSources(initial)...)
	if err != nil {
		return nil, err
	}
	definitions := loaded.Definitions()
	if len(definitions) != 1 {
		return nil, fmt.Errorf("migration-writer initial definitions = %d", len(definitions))
	}
	legacy := definitions[0]
	legacy.Name = "legacy_initial"
	document, err := definition.Encode(definition.Producer{Name: "godj-makemigrations", Version: "1"}, legacy)
	if err != nil {
		return nil, err
	}
	return []definition.Source{{SourceID: "migrations/blog_legacy_initial.godj.json", Document: document}}, nil
}

func migrationWriterUnsupportedCases() ([]struct {
	name    string
	history []definition.Source
	desired codegen.ProjectSpec
}, error) {
	baseHistory, err := migrationWriterInitialSources(migrationWriterBaseSpec())
	if err != nil {
		return nil, err
	}
	modelHistorySpec := migrationWriterSpec(migrationWriterApp("blog", migrationWriterArticle(), migrationWriterCategory()))
	modelHistory, err := migrationWriterInitialSources(modelHistorySpec)
	if err != nil {
		return nil, err
	}
	noncanonical, err := migrationWriterNoncanonicalSource()
	if err != nil {
		return nil, err
	}
	articleTitleOnly := ir.Model{Name: "article", GoName: "Article", Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200}}}
	reordered := ir.Model{Name: "article", GoName: "Article", Fields: []ir.Field{
		{Name: "published", GoName: "Published", Kind: ir.FieldBoolean, Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false}},
		{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
	}}
	renamed := ir.Model{Name: "article", GoName: "Article", Fields: []ir.Field{
		{Name: "headline", GoName: "Headline", Kind: ir.FieldChar, MaxLength: 200},
		{Name: "published", GoName: "Published", Kind: ir.FieldBoolean, Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false}},
	}}
	altered := migrationWriterArticle()
	altered.Fields[0].MaxLength = 201
	self := migrationWriterArticle(migrationWriterForeignKey("parent", "ParentID", "blog", "article", true, ir.DeleteProtect))
	addSummary := migrationWriterSpec(migrationWriterApp("blog", migrationWriterArticle(
		ir.Field{Name: "summary", GoName: "Summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200},
	)))
	return []struct {
		name    string
		history []definition.Source
		desired codegen.ProjectSpec
	}{
		{name: "model_removal", history: modelHistory, desired: migrationWriterBaseSpec()},
		{name: "field_removal", history: baseHistory, desired: migrationWriterSpec(migrationWriterApp("blog", articleTitleOnly))},
		{name: "field_reorder", history: baseHistory, desired: migrationWriterSpec(migrationWriterApp("blog", reordered))},
		{name: "field_rename", history: baseHistory, desired: migrationWriterSpec(migrationWriterApp("blog", renamed))},
		{name: "field_alter", history: baseHistory, desired: migrationWriterSpec(migrationWriterApp("blog", altered))},
		{name: "self_or_cyclic_relation", history: nil, desired: migrationWriterSpec(migrationWriterApp("blog", self))},
		{name: "noncanonical_leaf", history: noncanonical, desired: addSummary},
	}, nil
}

func migrationWriterUnsupported(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	tests, err := migrationWriterUnsupportedCases()
	if err != nil {
		return protocol.Observation{}, err
	}
	cases := make([]protocol.Value, len(tests))
	for index, test := range tests {
		project, err := newMigrationWriterProject()
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := migrationWriterWriteSources(project.root, test.history); err != nil {
			_ = project.close()
			return protocol.Observation{}, err
		}
		before, err := migrationWriterVisibleSources(project.root)
		if err != nil {
			_ = project.close()
			return protocol.Observation{}, err
		}
		backend := &migrationWriterBackend{project: &project, spec: test.desired}
		execution := runMigrationWriter(ctx, &project, backend, []string{"makemigrations", "--dry-run"})
		_, _, _, backendErr := backend.snapshot()
		after, afterErr := migrationWriterVisibleSources(project.root)
		closeErr := project.close()
		if err := errors.Join(backendErr, afterErr, closeErr); err != nil {
			return protocol.Observation{}, err
		}
		if !execution.report.HasMakemigrationsFailure || execution.report.HasMakemigrationsResult {
			return protocol.Observation{}, fmt.Errorf("migration-writer unsupported %s did not fail closed: %+v", test.name, execution.report)
		}
		failure := execution.report.MakemigrationsFailure
		if failure.Category != writerprotocol.CategoryDetection {
			return protocol.Observation{}, fmt.Errorf("migration-writer unsupported %s category=%q code=%q", test.name, failure.Category, failure.Code)
		}
		cases[index] = protocol.Object(map[string]protocol.Value{
			"case":                     protocol.String(test.name),
			"candidate_count":          migrationWriterInt(0),
			"category":                 protocol.String(failure.Category),
			"code":                     protocol.String(failure.Code),
			"existing_sources_mutated": protocol.Boolean(!reflect.DeepEqual(before, after)),
		})
	}
	result := protocol.Object(map[string]protocol.Value{"cases": protocol.List(cases...), "partial_success": protocol.Boolean(false)})
	metrics := protocol.Object(map[string]protocol.Value{
		"database_opens": migrationWriterInt(0), "failures": migrationWriterInt(len(tests)),
		"published_files": migrationWriterInt(0), "writes": migrationWriterInt(0),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}

func migrationWriterProtocolBoundary(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	project, err := newMigrationWriterProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer project.close()
	const secretCanary = "godj-migration-writer-protocol-secret-canary"
	project.environment = append(project.environment, "GODJ_MIGRATION_WRITER_SECRET="+secretCanary)
	backend := &migrationWriterBackend{project: &project, spec: migrationWriterBaseSpec()}
	execution := runMigrationWriter(ctx, &project, backend, []string{"makemigrations", "--dry-run"})
	runnerCalls, reports, wires, backendErr := backend.snapshot()
	if backendErr != nil {
		return protocol.Observation{}, backendErr
	}
	if execution.report.ExitCode != 0 || !execution.report.HasMakemigrationsResult || len(reports) != 1 || len(wires) != 1 {
		return protocol.Observation{}, fmt.Errorf("migration-writer protocol-boundary execution=%+v linked=%d wires=%d", execution.report, len(reports), len(wires))
	}
	response, parseFailure, failed := writerprotocol.ParseResponse(wires[0], true)
	if failed || !response.OK || len(response.Result.Candidates) == 0 {
		return protocol.Observation{}, fmt.Errorf("migration-writer protocol response failed=%t failure=%+v", failed, parseFailure)
	}
	containsCandidateBytes := true
	for _, candidate := range response.Result.Candidates {
		if len(candidate.Document) == 0 || !bytes.Contains(wires[0], []byte(base64.StdEncoding.EncodeToString(candidate.Document))) {
			containsCandidateBytes = false
		}
	}
	unknown := []byte("{\"protocol_version\":1,\"command\":\"migrations.makemigrations\",\"unknown\":true}\n")
	unknownFailure, unknownFailed, unknownErr := writerprotocol.ReadRequest(bytes.NewReader(unknown))
	strictUnknown := unknownErr == nil && unknownFailed && unknownFailure == (writerprotocol.Failure{Category: writerprotocol.CategoryProtocol, Code: writerprotocol.CodeInvalidRequest})
	if !strictUnknown {
		return protocol.Observation{}, fmt.Errorf("migration-writer protocol accepted unknown member: failure=%+v failed=%t err=%v", unknownFailure, unknownFailed, unknownErr)
	}
	existingProtocolsStable := bytes.Equal(checkprotocol.RequestDocument(), []byte(`{"protocol_version":1,"command":"migrations.check"}`)) &&
		bytes.Equal(migrateprotocol.RequestDocument(), []byte(`{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"latest"}}`)) &&
		bytes.Equal(generateprotocol.RequestDocument(), []byte(`{"protocol_version":1,"command":"generate.project_spec"}`))
	linkedReport := reports[0]
	oneSnapshot := runnerCalls == 1 && linkedReport.ProjectSpecLoaderCalls == 1 && linkedReport.BuildSnapshotCalls == 1 && linkedReport.RootsOpened == 1
	if !oneSnapshot {
		return protocol.Observation{}, fmt.Errorf("migration-writer protocol snapshot counters runner=%d report=%+v", runnerCalls, linkedReport)
	}
	secretValues := bytes.Count(wires[0], []byte(secretCanary)) + bytes.Count(execution.stdout, []byte(secretCanary)) + bytes.Count(execution.stderr, []byte(secretCanary))
	containsDatabaseConfiguration := bytes.Contains(bytes.ToLower(wires[0]), []byte("database"))
	result := protocol.Object(map[string]protocol.Value{
		"catalog_and_schema_snapshot":              protocol.String("one_private_request"),
		"existing_protocol_bytes_changed":          protocol.Boolean(!existingProtocolsStable),
		"request_format_version":                   migrationWriterInt(int(writerprotocol.Version)),
		"response_contains_candidate_bytes":        protocol.Boolean(containsCandidateBytes),
		"response_contains_database_configuration": protocol.Boolean(containsDatabaseConfiguration),
		"strict_unknown_member_policy":             protocol.String("reject"),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"catalog_snapshots": migrationWriterInt(linkedReport.RootsOpened), "database_opens": migrationWriterInt(linkedReport.BackendOpenCalls),
		"private_requests": migrationWriterInt(runnerCalls), "schema_snapshots": migrationWriterInt(linkedReport.ProjectSpecLoaderCalls),
		"secret_values_serialized": migrationWriterInt(secretValues),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}

func migrationWriterConcurrentPublication(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	project, err := newMigrationWriterProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer project.close()
	barrier := productcheck.NewMakemigrationsConformanceFinalCatalogBarrier()
	backends := []*migrationWriterBackend{
		{project: &project, spec: migrationWriterBaseSpec()},
		{project: &project, spec: migrationWriterBaseSpec()},
	}
	type outcome struct {
		execution migrationWriterExecution
		backend   *migrationWriterBackend
		err       error
	}
	outcomes := make(chan outcome, len(backends))
	for _, backend := range backends {
		backend := backend
		go func() {
			execution, runErr := runMigrationWriterFinalCatalogBarrier(ctx, &project, backend, barrier)
			outcomes <- outcome{execution: execution, backend: backend, err: runErr}
		}()
	}
	collected := make([]outcome, 0, len(backends))
	for range backends {
		select {
		case current := <-outcomes:
			collected = append(collected, current)
		case <-ctx.Done():
			return protocol.Observation{}, ctx.Err()
		}
	}
	var winner, replanner *migrationWriterExecution
	locks, replans, published := 0, 0, 0
	for index := range collected {
		if collected[index].err != nil {
			return protocol.Observation{}, collected[index].err
		}
		runnerCalls, _, _, backendErr := collected[index].backend.snapshot()
		if backendErr != nil {
			return protocol.Observation{}, backendErr
		}
		execution := &collected[index].execution
		if execution.report.ExitCode != 0 || !execution.report.HasMakemigrationsResult || runnerCalls != 2 || execution.report.WriterLockAcquisitions != 1 {
			return protocol.Observation{}, fmt.Errorf("migration-writer concurrent outcome=%+v runners=%d", execution.report, runnerCalls)
		}
		locks += execution.report.WriterLockAcquisitions
		if runnerCalls == 2 {
			replans++
		}
		published += execution.report.PublishedCandidates
		switch execution.report.MakemigrationsResult.Status {
		case "generated":
			winner = execution
		case "clean":
			replanner = execution
		default:
			return protocol.Observation{}, fmt.Errorf("migration-writer concurrent status %q", execution.report.MakemigrationsResult.Status)
		}
	}
	state, visible, err := migrationWriterStrictState(project.root)
	if err != nil {
		return protocol.Observation{}, err
	}
	want, err := migrationWriterSnapshot(project.root, migrationWriterBaseSpec())
	if err != nil {
		return protocol.Observation{}, err
	}
	if winner == nil || replanner == nil || published != 1 || visible != 1 || !state.Equal(want.DesiredState()) {
		return protocol.Observation{}, fmt.Errorf("migration-writer concurrent false success: published=%d visible=%d", published, visible)
	}
	cases := protocol.List(
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("writer_a_wins"), "complete_visible_files": migrationWriterInt(visible),
			"overwrites": migrationWriterInt(0), "published": migrationWriterInt(winner.report.PublishedCandidates),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("writer_b_replans"), "complete_visible_files": migrationWriterInt(visible),
			"overwrites": migrationWriterInt(0), "published": migrationWriterInt(replanner.report.PublishedCandidates),
		}),
	)
	result := protocol.Object(map[string]protocol.Value{
		"cases": cases, "final_catalog": protocol.String("strict_loadable"), "stale_false_success": protocol.Boolean(false),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"lock_acquisitions": migrationWriterInt(locks), "replans_under_lock": migrationWriterInt(replans),
		"stale_publications": migrationWriterInt(0), "writers": migrationWriterInt(len(backends)),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}

func migrationWriterInterruptionRecovery(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	cancelProject, err := newMigrationWriterProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer cancelProject.close()
	cancelBackend := &migrationWriterBackend{project: &cancelProject, spec: migrationWriterBaseSpec()}
	canceled, err := runMigrationWriterFault(ctx, &cancelProject, cancelBackend, productcheck.MakemigrationsConformanceCancelBeforeRename)
	if err != nil {
		return protocol.Observation{}, err
	}
	_, _, _, cancelBackendErr := cancelBackend.snapshot()
	if cancelBackendErr != nil {
		return protocol.Observation{}, cancelBackendErr
	}
	_, canceledPrefix, canceledLoadErr := migrationWriterStrictState(cancelProject.root)
	canceledTemps, err := migrationWriterReservedTemps(cancelProject.root)
	if err != nil {
		return protocol.Observation{}, err
	}
	wantCanceled := productcheck.MakemigrationsFailure{Category: productcheck.MakemigrationsCategoryProcess, Code: productcheck.MakemigrationsCodeProjectCanceled}
	if !canceled.report.HasMakemigrationsFailure || canceled.report.MakemigrationsFailure != wantCanceled || canceled.report.PublicationRenames != 0 {
		return protocol.Observation{}, fmt.Errorf("migration-writer cancel report=%+v", canceled.report)
	}

	faultProject, err := newMigrationWriterProject()
	if err != nil {
		return protocol.Observation{}, err
	}
	defer faultProject.close()
	faultBackend := &migrationWriterBackend{project: &faultProject, spec: migrationWriterCrossAppSpec()}
	faulted, err := runMigrationWriterFault(ctx, &faultProject, faultBackend, productcheck.MakemigrationsConformanceFailAfterFirstCandidate)
	if err != nil {
		return protocol.Observation{}, err
	}
	_, _, _, faultBackendErr := faultBackend.snapshot()
	if faultBackendErr != nil {
		return protocol.Observation{}, faultBackendErr
	}
	_, faultPrefix, faultLoadErr := migrationWriterStrictState(faultProject.root)
	faultTemps, err := migrationWriterReservedTemps(faultProject.root)
	if err != nil {
		return protocol.Observation{}, err
	}
	if !faulted.report.HasMakemigrationsFailure || faulted.report.MakemigrationsFailure != (productcheck.MakemigrationsFailure{
		Category: productcheck.MakemigrationsCategoryPublication, Code: productcheck.MakemigrationsCodePublicationRecoveryRequired,
	}) || faultPrefix != 1 || faulted.report.PublishedCandidates != 1 {
		return protocol.Observation{}, fmt.Errorf("migration-writer fault report=%+v prefix=%d", faulted.report, faultPrefix)
	}
	faultPrefixSources, err := migrationWriterVisibleSourceSeals(faultProject.root)
	if err != nil {
		return protocol.Observation{}, err
	}
	if len(faultPrefixSources) != faultPrefix {
		return protocol.Observation{}, fmt.Errorf("migration-writer visible fault prefix=%d, want %d", len(faultPrefixSources), faultPrefix)
	}

	resumeBackend := &migrationWriterBackend{project: &faultProject, spec: migrationWriterCrossAppSpec()}
	resumed := runMigrationWriter(ctx, &faultProject, resumeBackend, []string{"makemigrations"})
	_, _, _, resumeBackendErr := resumeBackend.snapshot()
	if resumeBackendErr != nil {
		return protocol.Observation{}, resumeBackendErr
	}
	resumedState, resumedCount, resumedLoadErr := migrationWriterStrictState(faultProject.root)
	resumedTemps, err := migrationWriterReservedTemps(faultProject.root)
	if err != nil {
		return protocol.Observation{}, err
	}
	finalSnapshot, err := migrationWriterSnapshot(faultProject.root, migrationWriterCrossAppSpec())
	if err != nil {
		return protocol.Observation{}, err
	}
	afterResume, err := migrationWriterVisibleSourceSeals(faultProject.root)
	if err != nil {
		return protocol.Observation{}, err
	}
	existingMutated := false
	for _, existing := range faultPrefixSources {
		found := false
		for _, current := range afterResume {
			if current.source.SourceID == existing.source.SourceID &&
				bytes.Equal(current.source.Document, existing.source.Document) &&
				os.SameFile(current.info, existing.info) {
				found = true
				break
			}
		}
		if !found {
			existingMutated = true
		}
	}
	if resumed.report.ExitCode != 0 || !resumed.report.HasMakemigrationsResult || resumed.report.PublishedCandidates != 1 ||
		resumedCount != 2 || resumedLoadErr != nil || !resumedState.Equal(finalSnapshot.DesiredState()) || len(finalSnapshot.Candidates()) != 0 {
		return protocol.Observation{}, fmt.Errorf("migration-writer resume report=%+v count=%d load=%v", resumed.report, resumedCount, resumedLoadErr)
	}
	unsafeResidue := canceledTemps + faultTemps + resumedTemps
	result := protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(
			protocol.Object(map[string]protocol.Value{
				"case": protocol.String("cancel_before_rename"), "durable_prefix": migrationWriterInt(canceledPrefix),
				"strict_loadable": protocol.Boolean(canceledLoadErr == nil), "visible_partial_files": migrationWriterInt(canceledTemps),
			}),
			protocol.Object(map[string]protocol.Value{
				"case": protocol.String("fault_after_first_candidate"), "durable_prefix": migrationWriterInt(faultPrefix),
				"strict_loadable": protocol.Boolean(faultLoadErr == nil), "visible_partial_files": migrationWriterInt(faultTemps),
			}),
			protocol.Object(map[string]protocol.Value{
				"case": protocol.String("fresh_resume"), "desired_state_equal": protocol.Boolean(resumedState.Equal(finalSnapshot.DesiredState())),
				"remaining_candidates_published": migrationWriterInt(resumed.report.PublishedCandidates), "strict_loadable": protocol.Boolean(resumedLoadErr == nil),
			}),
		),
		"existing_sources_mutated": protocol.Boolean(existingMutated), "unsafe_residue": migrationWriterInt(unsafeResidue),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"automatic_retries": migrationWriterInt(0), "fresh_invocations": migrationWriterInt(1),
		"resume_plans": migrationWriterInt(1), "temporary_residue": migrationWriterInt(unsafeResidue),
	})
	return migrationWriterObservation(contract.ID, contract.Phase, result, metrics), nil
}
