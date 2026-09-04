//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

var migrationSQLRenderingExpectedRegistrations = []struct {
	id       string
	scenario string
	phase    protocol.Phase
}{
	{id: "MIG-129", scenario: "godj.migration.sql_rendering.argv_and_pre_io_rejection", phase: protocol.PhaseEnvironment},
	{id: "MIG-130", scenario: "godj.migration.sql_rendering.complete_load_exact_lookup_and_request", phase: protocol.PhaseConstruction},
	{id: "MIG-131", scenario: "django.migration.sql_rendering.forward_before_state_order", phase: protocol.PhaseConstruction},
	{id: "MIG-132", scenario: "django.migration.sql_rendering.sqlite_create_add_semantics", phase: protocol.PhaseConstruction},
	{id: "MIG-133", scenario: "godj.migration.sql_rendering.postgres_current_projection", phase: protocol.PhaseConstruction},
	{id: "MIG-134", scenario: "godj.migration.sql_rendering.canonical_deterministic_output", phase: protocol.PhaseEvaluation},
	{id: "MIG-135", scenario: "godj.migration.sql_rendering.database_and_history_zero_calls", phase: protocol.PhaseEnvironment},
	{id: "MIG-136", scenario: "godj.migration.sql_rendering.renderer_and_operation_fail_closed", phase: protocol.PhaseEvaluation},
	{id: "MIG-137", scenario: "godj.migration.sql_rendering.resource_cleanup_redaction_and_write", phase: protocol.PhaseEnvironment},
	{id: "MIG-138", scenario: "godj.migration.sql_rendering.external_project_configuration", phase: protocol.PhaseEnvironment},
}

func TestMigrationSQLRenderingRegistryIsExactAndFailsClosed(t *testing.T) {
	if len(migrationSQLRenderingScenarioRegistry) != len(migrationSQLRenderingExpectedRegistrations) {
		t.Fatalf("migration-SQL-rendering registry size = %d, want %d", len(migrationSQLRenderingScenarioRegistry), len(migrationSQLRenderingExpectedRegistrations))
	}
	for _, expected := range migrationSQLRenderingExpectedRegistrations {
		registration, ok := migrationSQLRenderingScenarioRegistry[expected.scenario]
		if !ok || registration.handler == nil || registration.id != expected.id || registration.phase != expected.phase {
			t.Fatalf("migration-SQL-rendering registration %q = %#v", expected.scenario, registration)
		}
		generic, ok := lookupScenarioHandler(expected.scenario)
		if !ok || generic == nil {
			t.Fatalf("generic runner lookup omitted %s", expected.id)
		}
		handler, ok := migrationSQLRenderingScenarioHandler(expected.scenario)
		if !ok || handler == nil {
			t.Fatalf("migration-SQL-rendering handler omitted %s", expected.id)
		}
		valid := protocol.Contract{ID: expected.id, Scenario: expected.scenario, Phase: expected.phase}
		for _, test := range []struct {
			name     string
			ctx      context.Context
			contract protocol.Contract
		}{
			{name: "nil_context", contract: valid},
			{name: "wrong_id", ctx: context.Background(), contract: protocol.Contract{ID: "MIG-999", Scenario: expected.scenario, Phase: expected.phase}},
			{name: "wrong_scenario", ctx: context.Background(), contract: protocol.Contract{ID: expected.id, Scenario: expected.scenario + ".changed", Phase: expected.phase}},
			{name: "wrong_phase", ctx: context.Background(), contract: protocol.Contract{ID: expected.id, Scenario: expected.scenario, Phase: protocol.PhaseCommit}},
		} {
			if _, err := handler(test.ctx, test.contract); err == nil {
				t.Fatalf("%s handler accepted %s", expected.id, test.name)
			}
		}
	}
	for _, scenario := range []string{
		"",
		"godj.migration.sql_rendering",
		"godj.migration.sql_rendering.unknown",
		migrationSQLRenderingExpectedRegistrations[0].scenario + ".changed",
	} {
		if handler, ok := migrationSQLRenderingScenarioHandler(scenario); ok || handler != nil {
			t.Fatalf("unknown migration-SQL-rendering handler %q = %v, %t", scenario, handler, ok)
		}
	}
}

func TestMigrationSQLRenderingActualSourceIsOracleBlindAndBoundaryLocked(t *testing.T) {
	forbiddenText := []string{
		"conformance/oracles/",
		"conformance/contracts/",
		"conformance/fixtures/",
		"runners/django/",
		"migration-sql-rendering-oracle.json",
		"migration-sql-rendering-not-implemented.json",
		"godj-migration-sql-rendering-deviation-expected.json",
		"protocol.Compare(",
		"LoadObservationSuite(",
		"LoadManifest(",
	}
	forbiddenCalls := map[string]bool{
		"io.ReadAll":                    true,
		"os.Lstat":                      true,
		"os.Open":                       true,
		"os.OpenFile":                   true,
		"os.Stat":                       true,
		"filepath.Glob":                 true,
		"filepath.Walk":                 true,
		"filepath.WalkDir":              true,
		"protocol.Compare":              true,
		"protocol.LoadManifest":         true,
		"protocol.LoadObservationSuite": true,
	}
	sources := []struct {
		name      string
		wantCalls map[string]int
	}{
		{
			name: "migration_sql_rendering_scenarios.go",
			wantCalls: map[string]int{
				"databaseconfig.Open":                                1,
				"databaseconfig.PostgreSQL":                          1,
				"definition.Load":                                    2,
				"io.ReadFull":                                        1,
				"io.WriteString":                                     1,
				"linked.RunSQLMigrate":                               2,
				"migrationCommandAssertActualDirectoryEmpty":         1,
				"migrationCommandReadActualFile":                     2,
				"migrationCommandWaitForActualMarker":                1,
				"migrationCommandWaitForProcessGroupAbsent":          1,
				"migrationSQLRenderingObserveConfiguredRenderer":     2,
				"migrationSQLRenderingObserveOneSelection":           1,
				"migrationSQLRenderingObserveProcesses":              4,
				"migrationSQLRenderingProbeAcceptedArgv":             1,
				"migrationSQLRenderingProbePrivateResponseResources": 1,
				"migrationSQLRenderingProbeRedactionAndPublication":  1,
				"migrationSQLRenderingProbeRootResources":            1,
				"migrations.NewStateReconstructor":                   1,
				"migrations.RenderMigrationSQL":                      14,
				"net.DialTimeout":                                    2,
				"net.Listen":                                         1,
				"newMigrationCommandProject":                         2,
				"newMigrationSQLRenderingPoisonNetwork":              1,
				"newMigrationSQLRenderingActualProject":              1,
				"postgres.NewMigrationSQLRenderer":                   1,
				"poisonNetwork.checkpoint":                           1,
				"poisonNetwork.stop":                                 1,
				"poisonNetwork.verifyAttemptObservation":             1,
				"probe.checkpoint":                                   2,
				"Swap":                                               1,
				"productcheck.RunSQLMigrate":                         3,
				"reconstructor.Reconstruct":                          2,
				"selected.MigrationSQLRenderer":                      1,
				"sqlite.NewMigrationSQLRenderer":                     12,
				"sqlmigrateprotocol.EncodeRequest":                   3,
				"sqlmigrateprotocol.EncodeResponse":                  1,
				"sqlmigrateprotocol.ParseResponse":                   4,
				"unsupportedBuiltin.RenderForwardMigrationSQL":       1,
				"writeMigrationCommandActualFile":                    3,
			},
		},
		{
			name: "migration_sql_rendering_argv_probe.go",
			wantCalls: map[string]int{
				"os.ReadDir":                        1,
				"productcheck.RunSQLMigrate":        1,
				"sqlmigrateprotocol.EncodeResponse": 1,
				"sqlmigrateprotocol.ReadRequest":    1,
			},
		},
		{
			name: "migration_sql_rendering_resource_probe.go",
			wantCalls: map[string]int{
				"definition.Load":                            1,
				"linked.RunSQLMigrate":                       2,
				"migrationCommandAssertActualDirectoryEmpty": 1,
				"migrations.RenderMigrationSQL":              4,
				"productcheck.RunSQLMigrate":                 1,
				"sqlmigrateprotocol.EncodeRequest":           1,
				"sqlmigrateprotocol.EncodeResponse":          1,
				"sqlmigrateprotocol.ParseResponse":           4,
				"writeMigrationCommandActualFile":            2,
			},
		},
	}
	readModules := make(map[string]bool, 2)
	for _, source := range sources {
		document, err := os.ReadFile(source.name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range forbiddenText {
			if strings.Contains(string(document), forbidden) {
				t.Fatalf("migration-SQL-rendering actual source %s contains forbidden artifact shortcut %q", source.name, forbidden)
			}
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), source.name, document, 0)
		if err != nil {
			t.Fatal(err)
		}
		callCounts := make(map[string]int)
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := migrationSQLRenderingTestCallName(call.Fun)
			if name != "" {
				callCounts[name]++
			}
			if forbiddenCalls[name] {
				t.Errorf("migration-SQL-rendering actual source %s contains forbidden read/oracle call %s", source.name, name)
			}
			if strings.HasSuffix(name, ".ReadFile") {
				module, allowed := migrationSQLRenderingAllowedModuleRead(call)
				if !allowed {
					t.Errorf("migration-SQL-rendering actual source %s contains non-module ReadFile call at %d", source.name, call.Pos())
					return true
				}
				readModules[module] = true
			}
			return true
		})
		for name, want := range source.wantCalls {
			if got := callCounts[name]; got != want {
				t.Errorf("migration-SQL-rendering actual source %s boundary call %s count = %d, want %d", source.name, name, got, want)
			}
		}
	}
	modules := make([]string, 0, len(readModules))
	for module := range readModules {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	if !reflect.DeepEqual(modules, []string{"go.mod", "go.sum"}) {
		t.Fatalf("migration-SQL-rendering actual source ReadFile allowlist = %q, want only go.mod/go.sum", modules)
	}
	migrationSQLRenderingAssertEmbeddedActualRunner(t)

	runner, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatal(err)
	}
	runnerText := string(runner)
	targetIndex := strings.Index(runnerText, "migrationTargetPlanScenarioHandler(scenario)")
	renderingIndex := strings.Index(runnerText, "migrationSQLRenderingScenarioHandler(scenario)")
	fallbackIndex := strings.Index(runnerText, "migrationProjectCheckFixtures[scenario]")
	if targetIndex < 0 || renderingIndex < 0 || fallbackIndex < 0 || !(targetIndex < renderingIndex && renderingIndex < fallbackIndex) {
		t.Fatalf("migration-SQL-rendering registry order target=%d rendering=%d fallback=%d", targetIndex, renderingIndex, fallbackIndex)
	}
}

func migrationSQLRenderingTestCallName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		qualifier, ok := value.X.(*ast.Ident)
		if !ok {
			return value.Sel.Name
		}
		return qualifier.Name + "." + value.Sel.Name
	default:
		return ""
	}
}

func migrationSQLRenderingAllowedModuleRead(call *ast.CallExpr) (string, bool) {
	if migrationSQLRenderingTestCallName(call.Fun) != "os.ReadFile" || len(call.Args) != 1 {
		return "", false
	}
	join, ok := call.Args[0].(*ast.CallExpr)
	if !ok || migrationSQLRenderingTestCallName(join.Fun) != "filepath.Join" || len(join.Args) != 2 {
		return "", false
	}
	repository, ok := join.Args[0].(*ast.Ident)
	if !ok || repository.Name != "repository" {
		return "", false
	}
	literal, ok := join.Args[1].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	module, err := strconv.Unquote(literal.Value)
	if err != nil || (module != "go.mod" && module != "go.sum") {
		return "", false
	}
	return module, true
}

func migrationSQLRenderingAssertEmbeddedActualRunner(t *testing.T) {
	t.Helper()
	embedded, source := migrationSQLRenderingParseEmbeddedActualRunner(t)

	wantImports := []string{
		"context",
		"encoding/base64",
		"github.com/progresshans/godj/db/postgres",
		"github.com/progresshans/godj/db/sqlite",
		"github.com/progresshans/godj/migrations/definition",
		"github.com/progresshans/godj/project",
		"os",
		"os/exec",
		"os/signal",
		"path/filepath",
		"strconv",
		"time",
	}
	imports := make([]string, 0, len(embedded.Imports))
	for _, specification := range embedded.Imports {
		if specification.Name != nil {
			t.Fatalf("embedded migration-SQL-rendering runner aliases import %s as %s", specification.Path.Value, specification.Name.Name)
		}
		path, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			t.Fatalf("unquote embedded migration-SQL-rendering import %s: %v", specification.Path.Value, err)
		}
		if migrationSQLRenderingForbiddenEmbeddedImport(path) {
			t.Fatalf("embedded migration-SQL-rendering runner imports forbidden internal/private package %q", path)
		}
		imports = append(imports, path)
	}
	sort.Strings(imports)
	sort.Strings(wantImports)
	if !reflect.DeepEqual(imports, wantImports) {
		t.Fatalf("embedded migration-SQL-rendering runner imports = %q, want %q", imports, wantImports)
	}

	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range embedded.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil {
			continue
		}
		if _, duplicate := functions[function.Name.Name]; duplicate {
			t.Fatalf("embedded migration-SQL-rendering runner duplicates function %q", function.Name.Name)
		}
		functions[function.Name.Name] = function
	}
	mainFunction := functions["main"]
	runProjectFunction := functions["runProject"]
	if mainFunction == nil || runProjectFunction == nil {
		t.Fatalf("embedded migration-SQL-rendering runner functions main=%t runProject=%t", mainFunction != nil, runProjectFunction != nil)
	}

	callCounts := make(map[string]int)
	var projectRun *ast.CallExpr
	var decodeSource *ast.CallExpr
	ast.Inspect(embedded, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := migrationSQLRenderingEmbeddedExprName(call.Fun)
		callCounts[name]++
		if migrationSQLRenderingForbiddenEmbeddedCall(name) {
			t.Errorf("embedded migration-SQL-rendering runner contains response-emitting/private shortcut call %s", name)
		}
		switch name {
		case "project.Run":
			projectRun = call
		case "base64.StdEncoding.DecodeString":
			decodeSource = call
		}
		return true
	})
	for name, want := range map[string]int{
		"base64.StdEncoding.DecodeString":  1,
		"postgres.NewMigrationSQLRenderer": 1,
		"project.Run":                      1,
		"sqlite.NewMigrationSQLRenderer":   2,
	} {
		if got := callCounts[name]; got != want {
			t.Errorf("embedded migration-SQL-rendering runner call %s count = %d, want %d", name, got, want)
		}
	}
	if got := migrationSQLRenderingEmbeddedFunctionCallCount(mainFunction, "runProject"); got != 1 {
		t.Errorf("embedded migration-SQL-rendering main runProject call count = %d, want 1", got)
	}
	migrationSQLRenderingAssertEmbeddedNormalDispatch(t, mainFunction)
	if got := migrationSQLRenderingEmbeddedFunctionCallCount(runProjectFunction, "project.Run"); got != 1 {
		t.Errorf("embedded migration-SQL-rendering runProject project.Run call count = %d, want 1", got)
	}
	if projectRun != nil {
		migrationSQLRenderingAssertEmbeddedProjectRun(t, projectRun)
	}
	migrationSQLRenderingAssertEmbeddedRendererAssignments(t, mainFunction)
	if decodeSource != nil {
		migrationSQLRenderingAssertEmbeddedDefinitionSource(t, embedded, runProjectFunction, decodeSource)
	}

	for _, literal := range migrationSQLRenderingEmbeddedStringLiterals(t, embedded) {
		lower := strings.ToLower(literal)
		for _, forbidden := range []string{
			"alter table",
			"create table",
			"drop table",
			"insert into",
			`"ok"`,
			`"statements"`,
		} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("embedded migration-SQL-rendering runner contains hard-coded response/SQL literal %q", literal)
			}
		}
	}
	if strings.Contains(source, "GODJ_SQLMIGRATE_PRIVATE") {
		t.Error("embedded migration-SQL-rendering runner hard-codes the private protocol argument")
	}
	for _, required := range []string{
		`pending := filepath.Join(directory, ".godj-marker-"+name+".pending")`,
		`os.WriteFile(pending, []byte(value), 0o600)`,
		`os.Rename(pending, path)`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("embedded migration-SQL-rendering runner lacks atomic marker publication fragment %q", required)
		}
	}
	if strings.Contains(source, `os.WriteFile(path, []byte(value), 0o600)`) {
		t.Error("embedded migration-SQL-rendering runner publishes marker contents directly to the observable path")
	}
}

func migrationSQLRenderingParseEmbeddedActualRunner(t *testing.T) (*ast.File, string) {
	t.Helper()
	document, err := os.ReadFile("migration_sql_rendering_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	outer, err := parser.ParseFile(token.NewFileSet(), "migration_sql_rendering_scenarios.go", document, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse migration-SQL-rendering outer source: %v", err)
	}
	var literal *ast.BasicLit
	for _, declaration := range outer.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range value.Names {
				if name.Name != "migrationSQLRenderingActualRunnerSource" {
					continue
				}
				if literal != nil || len(value.Names) != 1 || len(value.Values) != 1 || index != 0 {
					t.Fatal("migrationSQLRenderingActualRunnerSource must be one exact string constant")
				}
				candidate, ok := value.Values[0].(*ast.BasicLit)
				if !ok || candidate.Kind != token.STRING {
					t.Fatal("migrationSQLRenderingActualRunnerSource is not a string literal")
				}
				literal = candidate
			}
		}
	}
	if literal == nil {
		t.Fatal("migrationSQLRenderingActualRunnerSource constant is missing")
	}
	source, err := strconv.Unquote(literal.Value)
	if err != nil {
		t.Fatalf("unquote migrationSQLRenderingActualRunnerSource: %v", err)
	}
	embedded, err := parser.ParseFile(token.NewFileSet(), "migration_sql_rendering_actual_runner.go", source, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse migrationSQLRenderingActualRunnerSource: %v", err)
	}
	return embedded, source
}

func migrationSQLRenderingForbiddenEmbeddedImport(path string) bool {
	return path == "github.com/progresshans/godj/internal" ||
		path == "github.com/progresshans/godj/private" ||
		strings.Contains(path, "/internal/") ||
		strings.Contains(path, "/private/") ||
		strings.Contains(path, "github.com/progresshans/godj/conformance/")
}

func migrationSQLRenderingForbiddenEmbeddedCall(name string) bool {
	switch name {
	case "Encode", "EncodeResponse", "Marshal", "Write", "WriteString", "print", "println",
		"fmt.Fprint", "fmt.Fprintf", "fmt.Fprintln", "fmt.Print", "fmt.Printf", "fmt.Println",
		"io.Copy", "io.WriteString",
		"json.Marshal", "json.MarshalIndent", "json.NewEncoder",
		"linked.RunSQLMigrate", "migrations.RenderMigrationSQL", "protocol.MarshalCanonical",
		"sqlmigrateprotocol.EncodeRequest", "sqlmigrateprotocol.EncodeResponse",
		"sqlmigrateprotocol.ParseResponse", "sqlmigrateprotocol.ReadRequest":
		return true
	}
	if strings.HasPrefix(name, "project.") && name != "project.Run" ||
		strings.HasPrefix(name, "sqlite.") && name != "sqlite.NewMigrationSQLRenderer" ||
		strings.HasPrefix(name, "postgres.") && name != "postgres.NewMigrationSQLRenderer" ||
		strings.HasPrefix(name, "definition.") {
		return true
	}
	return strings.HasSuffix(name, ".EncodeResponse") ||
		strings.HasSuffix(name, ".Marshal") ||
		strings.HasSuffix(name, ".Write") ||
		strings.HasSuffix(name, ".WriteString")
}

func migrationSQLRenderingAssertEmbeddedNormalDispatch(t *testing.T, mainFunction *ast.FuncDecl) {
	t.Helper()
	var normalCases int
	ast.Inspect(mainFunction.Body, func(node ast.Node) bool {
		switchStatement, ok := node.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		getenv, ok := switchStatement.Tag.(*ast.CallExpr)
		if !ok || migrationSQLRenderingEmbeddedExprName(getenv.Fun) != "os.Getenv" || len(getenv.Args) != 1 || migrationSQLRenderingEmbeddedExprName(getenv.Args[0]) != "modeEnvironment" {
			return true
		}
		for _, statement := range switchStatement.Body.List {
			clause, ok := statement.(*ast.CaseClause)
			if !ok || len(clause.List) != 1 || !migrationSQLRenderingEmbeddedString(clause.List[0], "normal") {
				continue
			}
			normalCases++
			calls := 0
			for _, bodyStatement := range clause.Body {
				ast.Inspect(bodyStatement, func(child ast.Node) bool {
					call, ok := child.(*ast.CallExpr)
					if ok && migrationSQLRenderingEmbeddedExprName(call.Fun) == "runProject" && len(call.Args) == 0 {
						calls++
					}
					return true
				})
			}
			if calls != 1 || len(clause.Body) != 1 {
				t.Errorf("embedded normal mode body has runProject calls/body = %d/%d, want 1/1", calls, len(clause.Body))
			}
		}
		return true
	})
	if normalCases != 1 {
		t.Errorf("embedded migration-SQL-rendering normal dispatch cases = %d, want 1", normalCases)
	}
}

func migrationSQLRenderingEmbeddedExprName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		qualifier := migrationSQLRenderingEmbeddedExprName(value.X)
		if qualifier == "" {
			return value.Sel.Name
		}
		return qualifier + "." + value.Sel.Name
	default:
		return ""
	}
}

func migrationSQLRenderingEmbeddedFunctionCallCount(function *ast.FuncDecl, target string) int {
	count := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && migrationSQLRenderingEmbeddedExprName(call.Fun) == target {
			count++
		}
		return true
	})
	return count
}

func migrationSQLRenderingAssertEmbeddedProjectRun(t *testing.T, call *ast.CallExpr) {
	t.Helper()
	if len(call.Args) != 5 {
		t.Fatalf("embedded project.Run arguments = %d, want 5", len(call.Args))
	}
	contextCall, ok := call.Args[0].(*ast.CallExpr)
	if !ok || migrationSQLRenderingEmbeddedExprName(contextCall.Fun) != "context.Background" || len(contextCall.Args) != 0 {
		t.Errorf("embedded project.Run context is not context.Background()")
	}
	configuration, ok := call.Args[1].(*ast.CompositeLit)
	if !ok || migrationSQLRenderingEmbeddedExprName(configuration.Type) != "project.Config" {
		t.Fatalf("embedded project.Run configuration is not project.Config")
	}
	fields := migrationSQLRenderingEmbeddedCompositeFields(t, configuration)
	if !reflect.DeepEqual(migrationSQLRenderingSortedKeys(fields), []string{"MigrationDefinitionSources", "MigrationSQLRenderer"}) {
		t.Errorf("embedded project.Run configuration fields = %v", migrationSQLRenderingSortedKeys(fields))
	}
	if migrationSQLRenderingEmbeddedExprName(fields["MigrationDefinitionSources"]) != "sources" {
		t.Errorf("embedded project.Run definition sources are not the decoded sources slice")
	}
	renderer, ok := fields["MigrationSQLRenderer"].(*ast.CallExpr)
	if !ok || migrationSQLRenderingEmbeddedExprName(renderer.Fun) != "sqlite.NewMigrationSQLRenderer" || len(renderer.Args) != 0 {
		t.Errorf("embedded project.Run renderer is not sqlite.NewMigrationSQLRenderer()")
	}
	argv, ok := call.Args[2].(*ast.SliceExpr)
	if !ok || migrationSQLRenderingEmbeddedExprName(argv.X) != "os.Args" || !migrationSQLRenderingEmbeddedInteger(argv.Low, "1") || argv.High != nil || argv.Max != nil {
		t.Errorf("embedded project.Run argv is not os.Args[1:]")
	}
	if migrationSQLRenderingEmbeddedExprName(call.Args[3]) != "os.Stdin" || migrationSQLRenderingEmbeddedExprName(call.Args[4]) != "os.Stdout" {
		t.Errorf("embedded project.Run I/O is not os.Stdin/os.Stdout")
	}
}

func migrationSQLRenderingAssertEmbeddedRendererAssignments(t *testing.T, mainFunction *ast.FuncDecl) {
	t.Helper()
	renderers := make([]*ast.CallExpr, 0, 2)
	ast.Inspect(mainFunction.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || assignment.Tok != token.ASSIGN || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 || migrationSQLRenderingEmbeddedExprName(assignment.Lhs[0]) != "_" {
			return true
		}
		configuration, ok := assignment.Rhs[0].(*ast.CompositeLit)
		if !ok || migrationSQLRenderingEmbeddedExprName(configuration.Type) != "project.Config" {
			return true
		}
		fields := migrationSQLRenderingEmbeddedCompositeFields(t, configuration)
		if !reflect.DeepEqual(migrationSQLRenderingSortedKeys(fields), []string{"MigrationSQLRenderer"}) {
			t.Errorf("embedded compile-time project.Config fields = %v, want only MigrationSQLRenderer", migrationSQLRenderingSortedKeys(fields))
			return true
		}
		renderer, ok := fields["MigrationSQLRenderer"].(*ast.CallExpr)
		if !ok {
			t.Error("embedded compile-time project.Config renderer is not a constructor call")
			return true
		}
		renderers = append(renderers, renderer)
		return true
	})
	if len(renderers) != 2 {
		t.Fatalf("embedded migration-SQL-rendering renderer config assignments = %d, want 2", len(renderers))
	}
	byName := make(map[string]*ast.CallExpr, 2)
	for _, renderer := range renderers {
		name := migrationSQLRenderingEmbeddedExprName(renderer.Fun)
		if byName[name] != nil {
			t.Fatalf("embedded migration-SQL-rendering duplicates renderer assignment %s", name)
		}
		byName[name] = renderer
	}
	sqliteRenderer := byName["sqlite.NewMigrationSQLRenderer"]
	if sqliteRenderer == nil || len(sqliteRenderer.Args) != 0 {
		t.Errorf("embedded SQLite renderer config assignment is not sqlite.NewMigrationSQLRenderer()")
	}
	postgresRenderer := byName["postgres.NewMigrationSQLRenderer"]
	if postgresRenderer == nil || len(postgresRenderer.Args) != 1 {
		t.Fatalf("embedded PostgreSQL renderer config assignment is not a one-argument constructor")
	}
	configuration, ok := postgresRenderer.Args[0].(*ast.CompositeLit)
	if !ok || migrationSQLRenderingEmbeddedExprName(configuration.Type) != "postgres.MigrationSQLConfig" {
		t.Fatalf("embedded PostgreSQL renderer argument is not postgres.MigrationSQLConfig")
	}
	fields := migrationSQLRenderingEmbeddedCompositeFields(t, configuration)
	if !reflect.DeepEqual(migrationSQLRenderingSortedKeys(fields), []string{"Schema"}) || !migrationSQLRenderingEmbeddedString(fields["Schema"], "public") {
		t.Errorf("embedded PostgreSQL renderer config is not exact public schema: fields=%v", migrationSQLRenderingSortedKeys(fields))
	}
}

func migrationSQLRenderingAssertEmbeddedDefinitionSource(
	t *testing.T,
	embedded *ast.File,
	runProjectFunction *ast.FuncDecl,
	decode *ast.CallExpr,
) {
	t.Helper()
	if len(decode.Args) != 1 {
		t.Fatalf("embedded base64 source decode arguments = %d, want 1", len(decode.Args))
	}
	getenv, ok := decode.Args[0].(*ast.CallExpr)
	if !ok || migrationSQLRenderingEmbeddedExprName(getenv.Fun) != "os.Getenv" || len(getenv.Args) != 1 {
		t.Fatalf("embedded base64 source decode does not read one environment value")
	}
	key, ok := getenv.Args[0].(*ast.BinaryExpr)
	if !ok || key.Op != token.ADD || migrationSQLRenderingEmbeddedExprName(key.X) != "sourceEnvironmentPrefix" {
		t.Errorf("embedded base64 source environment key does not use sourceEnvironmentPrefix")
	} else {
		index, ok := key.Y.(*ast.CallExpr)
		if !ok || migrationSQLRenderingEmbeddedExprName(index.Fun) != "strconv.Itoa" || len(index.Args) != 1 || migrationSQLRenderingEmbeddedExprName(index.Args[0]) != "index" {
			t.Errorf("embedded base64 source environment key does not append strconv.Itoa(index)")
		}
	}
	decodeAssigned := false
	var sourceLiteral *ast.CompositeLit
	var sourceAssignment *ast.AssignStmt
	ast.Inspect(embedded, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || migrationSQLRenderingEmbeddedExprName(literal.Type) != "definition.Source" {
			return true
		}
		if sourceLiteral != nil {
			t.Error("embedded migration-SQL-rendering runner constructs definition.Source more than once")
		}
		sourceLiteral = literal
		return true
	})
	if sourceLiteral == nil {
		t.Fatal("embedded migration-SQL-rendering runner does not construct definition.Source")
	}
	ast.Inspect(runProjectFunction.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if ok && len(assignment.Rhs) == 1 && assignment.Rhs[0] == decode && assignment.Tok == token.DEFINE && len(assignment.Lhs) == 2 &&
			migrationSQLRenderingEmbeddedExprName(assignment.Lhs[0]) == "document" && migrationSQLRenderingEmbeddedExprName(assignment.Lhs[1]) == "err" {
			decodeAssigned = true
		}
		return true
	})
	if !decodeAssigned {
		t.Error("embedded base64 source decode is not assigned to document, err in runProject")
	}
	fields := migrationSQLRenderingEmbeddedCompositeFields(t, sourceLiteral)
	if !reflect.DeepEqual(migrationSQLRenderingSortedKeys(fields), []string{"Document", "SourceID"}) ||
		migrationSQLRenderingEmbeddedExprName(fields["SourceID"]) != "identifier" ||
		migrationSQLRenderingEmbeddedExprName(fields["Document"]) != "document" {
		t.Errorf("embedded definition.Source does not bind identifier/document: fields=%v", migrationSQLRenderingSortedKeys(fields))
	}
	ast.Inspect(runProjectFunction.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Rhs) != 1 || assignment.Rhs[0] != sourceLiteral {
			return true
		}
		sourceAssignment = assignment
		return true
	})
	if sourceAssignment == nil || sourceAssignment.Tok != token.ASSIGN || len(sourceAssignment.Lhs) != 1 {
		t.Fatalf("embedded definition.Source is not assigned into the decoded sources slice")
	}
	target, ok := sourceAssignment.Lhs[0].(*ast.IndexExpr)
	if !ok || migrationSQLRenderingEmbeddedExprName(target.X) != "sources" || migrationSQLRenderingEmbeddedExprName(target.Index) != "index" {
		t.Errorf("embedded definition.Source assignment target is not sources[index]")
	}
}

func migrationSQLRenderingEmbeddedCompositeFields(t *testing.T, literal *ast.CompositeLit) map[string]ast.Expr {
	t.Helper()
	fields := make(map[string]ast.Expr, len(literal.Elts))
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			t.Fatalf("embedded %s composite contains an unkeyed element", migrationSQLRenderingEmbeddedExprName(literal.Type))
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if !ok || fields[key.Name] != nil {
			t.Fatalf("embedded %s composite contains invalid/duplicate key", migrationSQLRenderingEmbeddedExprName(literal.Type))
		}
		fields[key.Name] = keyValue.Value
	}
	return fields
}

func migrationSQLRenderingSortedKeys(values map[string]ast.Expr) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func migrationSQLRenderingEmbeddedInteger(expression ast.Expr, want string) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && literal.Kind == token.INT && literal.Value == want
}

func migrationSQLRenderingEmbeddedString(expression ast.Expr, want string) bool {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	return err == nil && value == want
}

func migrationSQLRenderingEmbeddedStringLiterals(t *testing.T, embedded *ast.File) []string {
	t.Helper()
	var literals []string
	ast.Inspect(embedded, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Errorf("unquote embedded migration-SQL-rendering string literal: %v", err)
			return true
		}
		literals = append(literals, value)
		return true
	})
	return literals
}

type migrationSQLRenderingProductTestState struct {
	profile         protocol.Profile
	manifest        protocol.Manifest
	oracle          protocol.ObservationSuite
	first           protocol.ObservationSuite
	second          protocol.ObservationSuite
	firstCanonical  []byte
	secondCanonical []byte
	err             error
}

var (
	migrationSQLRenderingProductOnce sync.Once
	migrationSQLRenderingProductData migrationSQLRenderingProductTestState
)

func migrationSQLRenderingProductState(t *testing.T) *migrationSQLRenderingProductTestState {
	t.Helper()
	migrationSQLRenderingProductOnce.Do(func() {
		state := &migrationSQLRenderingProductData
		root := filepath.Join("..", "..", "..")
		state.profile, state.err = protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
		if state.err != nil {
			state.err = fmt.Errorf("load migration SQL rendering profile: %w", state.err)
			return
		}
		state.manifest, state.err = protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-sql-rendering-manifest.json"))
		if state.err != nil {
			state.err = fmt.Errorf("load migration SQL rendering manifest: %w", state.err)
			return
		}
		state.oracle, state.err = protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-sql-rendering-oracle.json"))
		if state.err != nil {
			state.err = fmt.Errorf("load migration SQL rendering oracle: %w", state.err)
			return
		}
		// One complete observation performs three intentionally cold external
		// builds. The process-level bounds remain authoritative; this outer
		// budget only prevents their sequential composition from expiring first.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		state.first, state.err = Generate(ctx, state.profile, state.manifest)
		if state.err != nil {
			state.err = fmt.Errorf("generate first migration SQL rendering suite: %w", state.err)
			return
		}
		// The process evidence cache is intentionally retained here. The second
		// complete run proves canonical determinism without rebuilding the three
		// repository-external fresh/empty/cancellation process probes.
		state.second, state.err = Generate(ctx, state.profile, state.manifest)
		if state.err != nil {
			state.err = fmt.Errorf("generate second migration SQL rendering suite: %w", state.err)
			return
		}
		state.firstCanonical, state.err = protocol.MarshalCanonical(state.first)
		if state.err != nil {
			state.err = fmt.Errorf("marshal first migration SQL rendering suite: %w", state.err)
			return
		}
		state.secondCanonical, state.err = protocol.MarshalCanonical(state.second)
		if state.err != nil {
			state.err = fmt.Errorf("marshal second migration SQL rendering suite: %w", state.err)
		}
	})
	if migrationSQLRenderingProductData.err != nil {
		t.Fatal(migrationSQLRenderingProductData.err)
	}
	return &migrationSQLRenderingProductData
}

func TestMigrationSQLRenderingProductMatchesLockedOracleAndIsDeterministic(t *testing.T) {
	state := migrationSQLRenderingProductState(t)
	if len(state.manifest.Contracts) != len(migrationSQLRenderingExpectedRegistrations) {
		t.Fatalf("migration-SQL-rendering manifest contracts = %d, want %d", len(state.manifest.Contracts), len(migrationSQLRenderingExpectedRegistrations))
	}
	wantRequired := make([]string, len(migrationSQLRenderingExpectedRegistrations))
	for index, expected := range migrationSQLRenderingExpectedRegistrations {
		contract := state.manifest.Contracts[index]
		wantRequired[index] = expected.id
		if contract.ID != expected.id || contract.Scenario != expected.scenario || contract.Phase != expected.phase || contract.Status != protocol.ContractPassing {
			t.Fatalf("migration-SQL-rendering manifest contract %d = %#v, want %s/%s/%s/passing", index, contract, expected.id, expected.scenario, expected.phase)
		}
	}
	required, err := RequiredObservedContractIDs(state.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(required, wantRequired) {
		t.Fatalf("migration-SQL-rendering required observed IDs = %v, want %v", required, wantRequired)
	}

	for _, candidate := range []struct {
		name  string
		suite protocol.ObservationSuite
	}{
		{name: "first", suite: state.first},
		{name: "second", suite: state.second},
	} {
		name, suite := candidate.name, candidate.suite
		if err := protocol.ValidateSuiteAgainst(state.profile, state.manifest, suite); err != nil {
			t.Fatalf("ValidateSuiteAgainst(%s) error = %v", name, err)
		}
		if len(suite.Contracts) != len(migrationSQLRenderingExpectedRegistrations) {
			t.Fatalf("%s migration-SQL-rendering observations = %d", name, len(suite.Contracts))
		}
		for index, observation := range suite.Contracts {
			contract := state.manifest.Contracts[index]
			if err := observation.Validate(); err != nil {
				t.Fatalf("%s %s observation is invalid: %v", name, contract.ID, err)
			}
			if observation.ID != contract.ID || observation.Phase != contract.Phase || observation.Status != protocol.StatusObserved || observation.Result == nil || observation.Error != nil {
				t.Fatalf("%s %s observation identity/status/payload = %#v", name, contract.ID, observation)
			}
			wantDBState := migrationSQLRenderingComparisonContains(contract.Comparison, protocol.CompareDBState)
			wantMetrics := migrationSQLRenderingComparisonContains(contract.Comparison, protocol.CompareMetrics)
			if wantDBState != (observation.DBState != nil) || wantMetrics != (observation.Metrics != nil) {
				t.Fatalf("%s %s observation dimensions db=%t/%t metrics=%t/%t", name, contract.ID, observation.DBState != nil, wantDBState, observation.Metrics != nil, wantMetrics)
			}
		}
		differences, compareErr := protocol.Compare(state.profile, state.manifest, state.oracle, suite)
		if compareErr != nil {
			t.Fatalf("Compare(%s) error = %v", name, compareErr)
		}
		if len(differences) != 0 {
			t.Fatalf("Compare(%s) differences = %#v", name, differences)
		}
	}
	if !bytes.Equal(state.firstCanonical, state.secondCanonical) {
		t.Fatal("independent migration-SQL-rendering runs changed canonical observations")
	}
}

func migrationSQLRenderingComparisonContains(values []protocol.ComparisonDimension, target protocol.ComparisonDimension) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestMigrationSQLRenderingSerializedObservationsDoNotLeakPrivateInputs(t *testing.T) {
	state := migrationSQLRenderingProductState(t)
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	packageDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range [][]byte{state.firstCanonical, state.secondCanonical} {
		for _, forbidden := range []string{
			migrationSQLRenderingSecret,
			migrationSQLRenderingCredentialCanary,
			migrationSQLRenderingProbeRendererCauseCanary,
			migrationSQLRenderingProbePartialSQLCanary,
			migrationSQLRenderingProbeDefinitionCanary,
			migrationSQLRenderingProbeCredentialCanary,
			migrationSQLRenderingProbeChildStderrCanary,
			"partial-secret",
			migrationSQLRenderingProcessMode,
			migrationSQLRenderingProcessTrace,
			migrationSQLRenderingProcessSourcePrefix,
			filepath.Clean(repository),
			filepath.ToSlash(filepath.Clean(repository)),
			filepath.Clean(packageDirectory),
			filepath.ToSlash(filepath.Clean(packageDirectory)),
			filepath.Clean(os.TempDir()),
			filepath.ToSlash(filepath.Clean(os.TempDir())),
			"conformance/oracles/",
			"conformance/contracts/",
			"migration-sql-rendering-oracle.json",
			"migration-sql-rendering-manifest.json",
			"migration-sql-rendering-not-implemented.json",
			"migration_sql_rendering_argv_probe.go",
			"migration_sql_rendering_resource_probe.go",
			"migration_sql_rendering_scenarios.go",
		} {
			if forbidden != "" && bytes.Contains(document, []byte(forbidden)) {
				t.Fatalf("serialized migration-SQL-rendering observation contains private path/secret/artifact %q", forbidden)
			}
		}
	}
}

func TestMigrationSQLRenderingProcessEvidenceCacheHonorsContextAndDefensiveCopies(t *testing.T) {
	_ = migrationSQLRenderingProductState(t)
	bodies, err := migrationSQLRenderingDecisionBodies(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("canceled_context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := migrationSQLRenderingObserveProcesses(ctx, bodies); !errors.Is(err, context.Canceled) {
			t.Fatalf("cached process observation with canceled context error = %v, want context.Canceled", err)
		}
	})

	t.Run("defensive_copy", func(t *testing.T) {
		first, err := migrationSQLRenderingObserveProcesses(context.Background(), bodies)
		if err != nil {
			t.Fatal(err)
		}
		wantStderr := append([]byte(nil), first.cancellationStderr...)
		if len(wantStderr) == 0 {
			t.Fatal("cached process evidence has no cancellation stderr to copy-check")
		}
		first.cancellationStderr[0] ^= 0xff
		second, err := migrationSQLRenderingObserveProcesses(context.Background(), bodies)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(second.cancellationStderr, wantStderr) {
			t.Fatalf("cached process evidence exposed mutable cancellation stderr: got %q want %q", second.cancellationStderr, wantStderr)
		}
		second.cancellationStderr[0] ^= 0xff
		third, err := migrationSQLRenderingObserveProcesses(context.Background(), bodies)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(third.cancellationStderr, wantStderr) {
			t.Fatalf("cached process evidence return was not defensively copied: got %q want %q", third.cancellationStderr, wantStderr)
		}
	})
}
