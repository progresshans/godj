//go:build darwin || linux

package project

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
	projectmigrationprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestPublicFacadeMatchesLinkedEntrypointAndOwnsInputs(t *testing.T) {
	root := enterProjectRoot(t)

	for _, request := range [][]byte{
		protocol.RequestDocument(),
		[]byte(`{"protocol_version":2,"command":"migrations.check"}`),
		[]byte(`{"protocol_version":1,"command":"other"}`),
	} {
		var publicOutput bytes.Buffer
		publicErr := Run(
			context.Background(),
			Config{MigrationDefinitionRoots: []string{"migrations"}},
			[]string{protocol.PrivateArgument},
			bytes.NewReader(request),
			&publicOutput,
		)
		var linkedOutput bytes.Buffer
		_, linkedErr := linked.Run(
			context.Background(),
			linked.Config{ProjectRoot: root, MigrationDefinitionRoots: []string{"migrations"}},
			[]string{protocol.PrivateArgument},
			bytes.NewReader(request),
			&linkedOutput,
		)
		if !bytes.Equal(publicOutput.Bytes(), linkedOutput.Bytes()) || !sameError(publicErr, linkedErr) {
			t.Fatalf("public/linked mismatch for %q: public=%q/%v linked=%q/%v", request, publicOutput.Bytes(), publicErr, linkedOutput.Bytes(), linkedErr)
		}
	}

	roots := []string{"migrations"}
	argv := []string{protocol.PrivateArgument}
	reader := newBlockingReader(protocol.RequestDocument())
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Config{MigrationDefinitionRoots: roots}, argv, reader, &output)
	}()
	<-reader.started
	roots[0] = "mutated"
	argv[0] = "mutated"
	close(reader.release)
	if err := <-done; err != nil {
		t.Fatalf("mutated public Run = %v", err)
	}
	response, failure, failed := protocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (protocol.Failure{}) || !response.OK {
		t.Fatalf("mutated public response = %+v, %+v, %v", response, failure, failed)
	}
}

func TestPublicFacadeSeparatesMigrationAndGenerationLoaders(t *testing.T) {
	enterProjectRoot(t)
	loaderCalls := 0
	openerCalls := 0
	config := Config{
		MigrationDefinitionRoots: []string{"migrations"},
		LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) {
			loaderCalls++
			return codegen.ProjectSpec{
				Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/site/project", Directory: "project"},
				Apps: []codegen.AppSpec{{
					Alias:   "blog",
					Package: codegen.PackageSpec{PackageName: "models", ImportPath: "example.com/site/models", Directory: "models"},
					Schema: ir.Schema{
						FormatVersion: ir.CurrentFormatVersion,
						AppLabel:      "blog",
						Models: []ir.Model{{Name: "article", GoName: "Article", DBTable: "blog_article", Fields: []ir.Field{{
							Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true,
						}}}},
					},
				}},
			}, nil
		},
		OpenMigrationBackend: func(context.Context) (MigrationBackend, error) {
			openerCalls++
			return nil, errors.New("must not open outside explicit migrate")
		},
	}

	var migrationOutput bytes.Buffer
	if err := Run(
		context.Background(),
		config,
		[]string{protocol.PrivateArgument},
		bytes.NewReader(protocol.RequestDocument()),
		&migrationOutput,
	); err != nil {
		t.Fatalf("migration request: %v", err)
	}
	if loaderCalls != 0 || openerCalls != 0 {
		t.Fatalf("check request calls = loader %d, opener %d, want 0/0", loaderCalls, openerCalls)
	}

	var generationOutput bytes.Buffer
	if err := Run(
		context.Background(),
		config,
		[]string{projectgenerateprotocol.PrivateArgument},
		bytes.NewReader(projectgenerateprotocol.RequestDocument()),
		&generationOutput,
	); err != nil {
		t.Fatalf("generation request: %v", err)
	}
	if loaderCalls != 1 || openerCalls != 0 {
		t.Fatalf("generation request calls = loader %d, opener %d, want 1/0", loaderCalls, openerCalls)
	}
	response, failure, failed := projectgenerateprotocol.ParseResponse(generationOutput.Bytes(), true)
	if failed || failure != (projectgenerateprotocol.Failure{}) || !response.OK || len(response.ProjectSpec.Apps) != 1 {
		t.Fatalf("generation response = %+v failure=%+v failed=%v", response, failure, failed)
	}
	response.ProjectSpec.Apps[0].Schema.Models[0].Name = "mutated"

	var secondOutput bytes.Buffer
	if err := Run(
		context.Background(),
		config,
		[]string{projectgenerateprotocol.PrivateArgument},
		bytes.NewReader(projectgenerateprotocol.RequestDocument()),
		&secondOutput,
	); err != nil {
		t.Fatalf("second generation request: %v", err)
	}
	second, failure, failed := projectgenerateprotocol.ParseResponse(secondOutput.Bytes(), true)
	if failed || failure != (projectgenerateprotocol.Failure{}) || !second.OK || second.ProjectSpec.Apps[0].Schema.Models[0].Name != "article" {
		t.Fatalf("second generation response = %+v failure=%+v failed=%v", second, failure, failed)
	}
	if openerCalls != 0 {
		t.Fatalf("generation invoked migration opener %d times", openerCalls)
	}
}

func TestPublicMigrateOwnsSourcesAndSignalContext(t *testing.T) {
	enterProjectRoot(t)
	sources := []definition.Source{{
		SourceID: "framework/system-state.godj.json",
		Document: []byte(`{"format_version":1,"producer":{"name":"project-test","version":"1"},"migration":{"app":"system","name":"0001_initial","dependencies":[],"operations":[]}}`),
	}}
	argv := []string{migrateprotocol.PrivateArgument}
	reader := newBlockingReader(migrateprotocol.RequestDocument())
	openerStarted := make(chan struct{})
	var ownedCancel context.CancelFunc
	ownerCalls := 0
	stopCalls := 0
	config := Config{
		MigrationDefinitionSources: sources,
		OpenMigrationBackend: func(ctx context.Context) (MigrationBackend, error) {
			close(openerStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), config, argv, reader, &output, func(parent context.Context) (context.Context, context.CancelFunc) {
			ownerCalls++
			owned, cancel := context.WithCancel(parent)
			ownedCancel = cancel
			return owned, func() {
				stopCalls++
				cancel()
			}
		})
	}()
	<-reader.started
	sources[0].SourceID = "mutated"
	sources[0].Document[0] = 'x'
	argv[0] = "mutated"
	close(reader.release)
	<-openerStarted
	ownedCancel()
	if err := <-done; err != nil {
		t.Fatalf("migrate dispatch = %v", err)
	}
	response, failure, failed := migrateprotocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (migrateprotocol.Failure{}) || response.Failure != (migrateprotocol.Failure{
		Category: migrateprotocol.CategoryBackend,
		Code:     migrateprotocol.CodeBackendOpenFailed,
	}) {
		t.Fatalf("migrate cancellation response = %+v, %+v, %v", response, failure, failed)
	}
	if ownerCalls != 1 || stopCalls != 1 {
		t.Fatalf("signal owner calls = owner %d, stop %d", ownerCalls, stopCalls)
	}
}

func TestPublicShowMigrationsOwnsSourcesAndUsesReadOnlySignalSession(t *testing.T) {
	enterProjectRoot(t)
	sources := []definition.Source{{
		SourceID: "framework/alpha-0001.godj.json",
		Document: []byte(`{"format_version":1,"producer":{"name":"project-test","version":"1"},"migration":{"app":"alpha","name":"0001","dependencies":[],"operations":[]}}`),
	}}
	argv := []string{showmigrationsprotocol.PrivateArgument}
	reader := newBlockingReader(showmigrationsprotocol.RequestDocument())
	database := &publicShowMigrationsBackend{session: &publicShowMigrationsSession{
		records: []backend.AppliedMigration{{App: "alpha", Name: "0001"}},
	}}
	ownerCalls := 0
	stopCalls := 0
	config := Config{
		MigrationDefinitionSources: sources,
		OpenMigrationBackend: func(context.Context) (MigrationBackend, error) {
			return database, nil
		},
	}
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), config, argv, reader, &output, func(parent context.Context) (context.Context, context.CancelFunc) {
			ownerCalls++
			owned, cancel := context.WithCancel(parent)
			return owned, func() {
				stopCalls++
				cancel()
			}
		})
	}()
	<-reader.started
	sources[0].SourceID = "mutated"
	sources[0].Document[0] = 'x'
	argv[0] = "mutated"
	close(reader.release)
	if err := <-done; err != nil {
		t.Fatalf("showmigrations dispatch = %v", err)
	}
	response, failure, failed := showmigrationsprotocol.ParseResponse(output.Bytes(), true)
	want := []showmigrationsprotocol.Row{{App: "alpha", Name: "0001", Status: showmigrationsprotocol.StatusApplied}}
	if failed || failure != (showmigrationsprotocol.Failure{}) || !response.OK || !reflect.DeepEqual(response.Result.Rows, want) {
		t.Fatalf("showmigrations response = %+v, %+v, %v", response, failure, failed)
	}
	if ownerCalls != 1 || stopCalls != 1 || database.openCalls != 1 || database.session.readCalls != 1 ||
		database.session.beginCalls != 0 || database.session.closeCalls != 1 || database.closeCalls != 1 {
		t.Fatalf("showmigrations ownership = owner:%d stop:%d backend:%+v", ownerCalls, stopCalls, database)
	}
}

func TestPublicMakemigrationsOwnsInputsAndNeverOpensBackend(t *testing.T) {
	enterProjectRoot(t)
	roots := []string{"migrations"}
	sources := []definition.Source{{
		SourceID: "embedded/system-state.godj.json",
		Document: []byte(`{"format_version":1,"producer":{"name":"project-test","version":"1"},"migration":{"app":"system","name":"0001_initial","dependencies":[],"operations":[]}}`),
	}}
	argv := []string{projectmigrationprotocol.PrivateArgument}
	reader := newBlockingReader(projectmigrationprotocol.RequestDocument())
	loaderCalls := 0
	openerCalls := 0
	config := Config{
		MigrationDefinitionRoots:   roots,
		MigrationDefinitionSources: sources,
		LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) {
			loaderCalls++
			return codegen.ProjectSpec{
				Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/site/project", Directory: "project"},
				Apps: []codegen.AppSpec{{
					Alias: "content", Package: codegen.PackageSpec{PackageName: "content", ImportPath: "example.com/site/content", Directory: "content"},
					Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "content", Models: []ir.Model{{
						Name: "article", GoName: "Article", Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200}},
					}}},
				}},
			}, nil
		},
		OpenMigrationBackend: func(context.Context) (MigrationBackend, error) {
			openerCalls++
			return nil, errors.New("makemigrations must not open backend")
		},
	}
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), config, argv, reader, &output) }()
	<-reader.started
	roots[0] = "mutated"
	sources[0].SourceID = "mutated"
	sources[0].Document[0] ^= 0xff
	argv[0] = "mutated"
	close(reader.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	response, failure, failed := projectmigrationprotocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (projectmigrationprotocol.Failure{}) || !response.OK || len(response.Result.Candidates) != 1 ||
		response.Result.ProgrammaticCatalog.SourceCount != 1 || response.Result.ProgrammaticCatalog.Sources[0].SourceID != "embedded/system-state.godj.json" {
		t.Fatalf("response=%+v failure=%+v failed=%v", response, failure, failed)
	}
	if loaderCalls != 1 || openerCalls != 0 {
		t.Fatalf("calls loader=%d opener=%d, want 1/0", loaderCalls, openerCalls)
	}
}

func TestPublicMakemigrationsBoundsProgrammaticCatalogWithoutOpeningBackend(t *testing.T) {
	enterProjectRoot(t)
	loaderCalls := 0
	openerCalls := 0
	var output bytes.Buffer
	err := Run(context.Background(), Config{
		MigrationDefinitionRoots:   []string{"migrations"},
		MigrationDefinitionSources: make([]definition.Source, definition.MaxSources+1),
		LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) {
			loaderCalls++
			return codegen.ProjectSpec{}, nil
		},
		OpenMigrationBackend: func(context.Context) (MigrationBackend, error) {
			openerCalls++
			return nil, errors.New("makemigrations must not open backend")
		},
	}, []string{projectmigrationprotocol.PrivateArgument}, bytes.NewReader(projectmigrationprotocol.RequestDocument()), &output)
	if err != nil {
		t.Fatal(err)
	}
	response, failure, failed := projectmigrationprotocol.ParseResponse(output.Bytes(), true)
	want := projectmigrationprotocol.Failure{
		Category: projectmigrationprotocol.CategoryCandidate,
		Code:     projectmigrationprotocol.CodeCandidateResourceLimitExceeded,
	}
	if failed || failure != (projectmigrationprotocol.Failure{}) || response.Failure != want || loaderCalls != 0 || openerCalls != 0 {
		t.Fatalf("response=%+v failure=%+v failed=%v loader=%d opener=%d", response, failure, failed, loaderCalls, openerCalls)
	}
}

func TestPublicFacadePropagatesWriterCancellationAndPrivateArgErrors(t *testing.T) {
	enterProjectRoot(t)
	sentinel := errors.New("writer failed")
	err := Run(
		context.Background(),
		Config{MigrationDefinitionRoots: []string{"migrations"}},
		[]string{protocol.PrivateArgument},
		bytes.NewReader(protocol.RequestDocument()),
		errorWriter{err: sentinel},
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("writer error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(canceled, Config{}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Run = %v", err)
	}
	if err := Run(context.Background(), Config{}, []string{"migrations", "check"}, bytes.NewReader(protocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("public command argv unexpectedly accepted")
	}
	if err := Run(nil, Config{}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("nil context unexpectedly accepted")
	}
}

func TestPublicRequestPrecedesDeletedWorkingDirectory(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		_ = os.Chdir(original)
		t.Skipf("platform does not allow removing the current directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	var output bytes.Buffer
	err = Run(
		context.Background(),
		Config{},
		[]string{protocol.PrivateArgument},
		bytes.NewReader([]byte(`{"protocol_version":2,"command":"migrations.check"}`)),
		&output,
	)
	if err != nil {
		t.Fatalf("invalid request with deleted cwd = %v", err)
	}
	response, parseFailure, parseFailed := protocol.ParseResponse(output.Bytes(), true)
	if parseFailed || parseFailure != (protocol.Failure{}) || response.Failure.Code != protocol.CodeProjectProtocolIncompatible {
		t.Fatalf("invalid request precedence = %+v, %+v, %v", response, parseFailure, parseFailed)
	}

	output.Reset()
	err = Run(
		context.Background(),
		Config{},
		[]string{protocol.PrivateArgument},
		bytes.NewReader(protocol.RequestDocument()),
		&output,
	)
	if err != nil {
		t.Fatalf("valid request with deleted cwd = %v", err)
	}
	response, parseFailure, parseFailed = protocol.ParseResponse(output.Bytes(), true)
	if parseFailed || parseFailure != (protocol.Failure{}) || response.Failure.Code != protocol.CodeSourceDiscoveryFailed {
		t.Fatalf("deleted cwd classification = %+v, %+v, %v", response, parseFailure, parseFailed)
	}
}

func TestPublicFacadeIsRaceFreeWithinFixedWorkingDirectory(t *testing.T) {
	enterProjectRoot(t)
	const calls = 20
	var wait sync.WaitGroup
	failures := make(chan error, calls)
	for index := 0; index < calls; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var output bytes.Buffer
			err := Run(
				context.Background(),
				Config{MigrationDefinitionRoots: []string{"migrations"}},
				[]string{protocol.PrivateArgument},
				bytes.NewReader(protocol.RequestDocument()),
				&output,
			)
			response, parseFailure, parseFailed := protocol.ParseResponse(output.Bytes(), true)
			if err != nil || parseFailed || parseFailure != (protocol.Failure{}) || !response.OK {
				failures <- errors.New("concurrent public invocation failed")
			}
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestPublicSurfaceAndDelegationAreExact(t *testing.T) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "project_unix.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	exports := make([]string, 0)
	linkedCalls := 0
	linkedMigrateCalls := 0
	linkedShowMigrationsCalls := 0
	linkedMakemigrationsCalls := 0
	linkedRawMakemigrationsCalls := 0
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.TypeSpec:
			if ast.IsExported(value.Name.Name) {
				exports = append(exports, value.Name.Name)
			}
		case *ast.FuncDecl:
			if value.Recv == nil && ast.IsExported(value.Name.Name) {
				exports = append(exports, value.Name.Name)
			}
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok {
				break
			}
			identifier, ok := selector.X.(*ast.Ident)
			if ok && identifier.Name == "linked" && selector.Sel.Name == "Run" {
				linkedCalls++
			}
			if ok && identifier.Name == "linked" && selector.Sel.Name == "RunMigrate" {
				linkedMigrateCalls++
			}
			if ok && identifier.Name == "linked" && selector.Sel.Name == "RunShowMigrations" {
				linkedShowMigrationsCalls++
			}
			if ok && identifier.Name == "linked" && selector.Sel.Name == "RunSnapshottedMakemigrations" {
				linkedMakemigrationsCalls++
			}
			if ok && identifier.Name == "linked" && selector.Sel.Name == "RunMakemigrations" {
				linkedRawMakemigrationsCalls++
			}
		}
		return true
	})
	sort.Strings(exports)
	if !reflect.DeepEqual(exports, []string{"Config", "MigrationBackend", "Run"}) {
		t.Fatalf("project exports = %v", exports)
	}
	if linkedCalls != 1 {
		t.Fatalf("linked.Run callsites in public facade = %d, want 1", linkedCalls)
	}
	if linkedMigrateCalls != 1 {
		t.Fatalf("linked.RunMigrate callsites in public facade = %d, want 1", linkedMigrateCalls)
	}
	if linkedShowMigrationsCalls != 1 {
		t.Fatalf("linked.RunShowMigrations callsites in public facade = %d, want 1", linkedShowMigrationsCalls)
	}
	if linkedMakemigrationsCalls != 1 {
		t.Fatalf("linked.RunSnapshottedMakemigrations callsites in public facade = %d, want 1", linkedMakemigrationsCalls)
	}
	if linkedRawMakemigrationsCalls != 0 {
		t.Fatalf("linked.RunMakemigrations raw callsites in public facade = %d, want 0", linkedRawMakemigrationsCalls)
	}
	source, err := os.ReadFile("project_unix.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("os.Exit"), []byte("os.Args"), []byte("os.Stdin"), []byte("os.Stdout"), []byte("conformance")} {
		if bytes.Contains(source, forbidden) {
			t.Errorf("public facade contains forbidden fragment %q", forbidden)
		}
	}
}

type publicShowMigrationsBackend struct {
	session    *publicShowMigrationsSession
	openCalls  int
	closeCalls int
}

func (*publicShowMigrationsBackend) MigrationCapabilities() backend.MigrationCapabilities {
	return backend.MigrationCapabilities{}
}

func (value *publicShowMigrationsBackend) OpenRevisionFencedSession(context.Context) (backend.RevisionFencedSession, error) {
	value.openCalls++
	return value.session, nil
}

func (value *publicShowMigrationsBackend) Close() error {
	value.closeCalls++
	return nil
}

type publicShowMigrationsSession struct {
	records    []backend.AppliedMigration
	readCalls  int
	beginCalls int
	closeCalls int
}

func (value *publicShowMigrationsSession) ReadAppliedMigrations(context.Context) ([]backend.AppliedMigration, error) {
	value.readCalls++
	return append([]backend.AppliedMigration(nil), value.records...), nil
}

func (value *publicShowMigrationsSession) BeginMigration(context.Context, backend.HistoryTransition, backend.MigrationIntent) (backend.RevisionFencedTransaction, error) {
	value.beginCalls++
	return nil, errors.New("showmigrations must not begin a transaction")
}

func (value *publicShowMigrationsSession) Close(context.Context) error {
	value.closeCalls++
	return nil
}

var _ MigrationBackend = (*publicShowMigrationsBackend)(nil)
var _ backend.RevisionFencedSession = (*publicShowMigrationsSession)(nil)

func enterProjectRoot(t *testing.T) string {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	return root
}

func sameError(left, right error) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Error() == right.Error()
}

type blockingReader struct {
	document []byte
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newBlockingReader(document []byte) *blockingReader {
	return &blockingReader{
		document: append([]byte(nil), document...),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
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

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }
