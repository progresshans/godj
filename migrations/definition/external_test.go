package definition_test

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestPublicMigrationDefinitionConstants(t *testing.T) {
	t.Parallel()

	if definition.DefinitionFormatVersion != 1 || definition.LoaderABIVersion != 1 || definition.OperationCodecVersion != 1 {
		t.Fatalf("wire tuple prefix = (%d,%d,%d), want (1,1,1)", definition.DefinitionFormatVersion, definition.LoaderABIVersion, definition.OperationCodecVersion)
	}
	if definition.SchemaIRVersion != 2 || ir.FormatVersion != 2 {
		t.Fatalf("Schema IR wire/current versions = %d/%d, want literal 2/current 2", definition.SchemaIRVersion, ir.FormatVersion)
	}
	if definition.EmptySetDigest != "sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450" {
		t.Fatalf("empty digest = %q", definition.EmptySetDigest)
	}
	if definition.CategorySource != "migration_definition_source_error" {
		t.Fatalf("source category = %q", definition.CategorySource)
	}

	limits := []struct {
		name string
		got  int
		want int
	}{
		{"MaxSources", definition.MaxSources, 2_048},
		{"MaxSourceIDBytes", definition.MaxSourceIDBytes, 1_024},
		{"MaxDocumentBytes", definition.MaxDocumentBytes, 1 << 20},
		{"MaxBatchBytes", definition.MaxBatchBytes, 16 << 20},
		{"MaxJSONDepth", definition.MaxJSONDepth, 64},
		{"MaxDocumentJSONValues", definition.MaxDocumentJSONValues, 65_536},
		{"MaxJSONValues", definition.MaxJSONValues, 262_144},
		{"MaxDependenciesPerMigration", definition.MaxDependenciesPerMigration, 2_047},
		{"MaxOperationsPerMigration", definition.MaxOperationsPerMigration, 2_048},
		{"MaxFieldsPerCreateModel", definition.MaxFieldsPerCreateModel, 2_048},
	}
	for _, limit := range limits {
		if limit.got != limit.want {
			t.Errorf("%s = %d, want %d", limit.name, limit.got, limit.want)
		}
	}

	codes := []definition.ErrorCode{
		definition.CodeInvalidSource,
		definition.CodeInvalidDocument,
		definition.CodeDefinitionFormatIncompatible,
		definition.CodeLoaderABIIncompatible,
		definition.CodeOperationCodecIncompatible,
		definition.CodeSchemaIRIncompatible,
		definition.CodeUnsupportedOperation,
		definition.CodeInvalidOperation,
		definition.CodeInvalidIR,
	}
	wantCodes := []definition.ErrorCode{
		"invalid_definition_source",
		"invalid_definition_document",
		"definition_format_incompatible",
		"loader_abi_incompatible",
		"operation_codec_incompatible",
		"schema_ir_incompatible",
		"unsupported_definition_operation",
		"invalid_definition_operation",
		"invalid_definition_ir",
	}
	seen := make(map[definition.ErrorCode]struct{}, len(codes))
	for index, code := range codes {
		if code != wantCodes[index] {
			t.Errorf("error code[%d] = %q, want %q", index, code, wantCodes[index])
		}
		if _, duplicate := seen[code]; duplicate {
			t.Errorf("duplicate public source error code %q", code)
		}
		seen[code] = struct{}{}
	}
	if len(seen) != 9 {
		t.Fatalf("public source error codes = %d, want exactly 9", len(seen))
	}
}

func TestPublicMigrationDefinitionAPITypes(t *testing.T) {
	t.Parallel()

	var load func(...definition.Source) (definition.Set, definition.LoadReport, error) = definition.Load
	var migrate func(definition.Set, context.Context, migrations.Executor, migrations.LifecycleRequest) (migrations.ProjectState, error) = definition.Set.Migrate
	_ = load
	_ = migrate

	// Keep all public value shapes constructible without relying on private
	// implementation fields. The external compile fixture additionally locks
	// the real context.Context method expression.
	key := migrations.MigrationKey{App: "news", Name: "0001_initial"}
	producer := definition.Producer{Name: "generator", Version: "1"}
	_ = definition.Source{SourceID: "source", Document: []byte(`{}`)}
	_ = definition.SourceInfo{SourceID: "source", Producer: producer, Migration: key}
	_ = definition.GraphSource{Migration: key, SourceID: "source"}
	context := definition.FailureContext{
		Stage: "semantic", SourceID: "source", JSONPointer: "/migration", App: key.App, Name: key.Name,
		OperationIndex: -1, Reason: "invalid_operation", Maximum: 0, Actual: 0,
	}
	_ = context.GraphSources()
	report := definition.LoadReport{DocumentsReceived: 1}
	_, _ = report.Failure()
	definitionError := &definition.Error{Category: definition.CategorySource, Code: definition.CodeInvalidOperation}
	_ = definitionError.Error()
	_ = definitionError.Context()
}

func TestPublicMigrationDefinitionValueShapesAreExact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		typeOf reflect.Type
		fields []string
	}{
		{"Source", reflect.TypeOf(definition.Source{}), []string{"SourceID", "Document"}},
		{"Producer", reflect.TypeOf(definition.Producer{}), []string{"Name", "Version"}},
		{"SourceInfo", reflect.TypeOf(definition.SourceInfo{}), []string{"SourceID", "Producer", "Migration"}},
		{"GraphSource", reflect.TypeOf(definition.GraphSource{}), []string{"Migration", "SourceID"}},
		{"FailureContext", reflect.TypeOf(definition.FailureContext{}), []string{"Stage", "SourceID", "JSONPointer", "App", "Name", "OperationIndex", "Reason", "Limit", "Maximum", "Actual"}},
		{"LoadReport", reflect.TypeOf(definition.LoadReport{}), []string{"DocumentsReceived", "HeadersValidated", "OperationsDecoded", "PlannerConstruction", "DefinitionsPublished", "DefinitionSetsPublished"}},
		{"Error", reflect.TypeOf(definition.Error{}), []string{"Category", "Code"}},
		{"Set", reflect.TypeOf(definition.Set{}), []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exported := make([]string, 0)
			for index := 0; index < test.typeOf.NumField(); index++ {
				field := test.typeOf.Field(index)
				if field.IsExported() {
					exported = append(exported, field.Name)
				}
			}
			if !reflect.DeepEqual(exported, test.fields) {
				t.Fatalf("%s exported fields = %v, want exactly %v", test.name, exported, test.fields)
			}
		})
	}
}

func TestProductSourceASTBoundaries(t *testing.T) {
	t.Parallel()

	directory := definitionPackageDirectory(t)
	files, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		t.Fatalf("list definition package: %v", err)
	}

	allowedLocalImports := map[string]struct{}{
		"github.com/progresshans/godj/migrations": {},
		"github.com/progresshans/godj/schema/ir":  {},
	}
	fset := token.NewFileSet()
	plannerCalls := 0
	executorMigrateCalls := 0
	setMigrateMethods := 0
	schemaIRLiteral := 0
	exportedDeclarations := make(map[string]string)
	exportedMethods := make(map[string]struct{})
	driftAssertions := map[string]bool{
		"SchemaIRVersion-int64(ir.FormatVersion)": false,
		"int64(ir.FormatVersion)-SchemaIRVersion": false,
	}

	for _, filename := range files {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, filename, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", filename, parseErr)
		}
		importAliases := make(map[string]string)
		for _, imported := range parsed.Imports {
			pathValue, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("unquote import in %s: %v", filename, unquoteErr)
			}
			if forbiddenDefinitionImport(pathValue) {
				t.Errorf("non-test definition product imports forbidden package %q in %s", pathValue, filepath.Base(filename))
			}
			if strings.HasPrefix(pathValue, "github.com/progresshans/godj/") {
				if _, allowed := allowedLocalImports[pathValue]; !allowed {
					t.Errorf("definition product imports out-of-bound local package %q in %s", pathValue, filepath.Base(filename))
				}
			}
			alias := importBase(pathValue)
			if imported.Name != nil {
				alias = imported.Name.Name
				if alias == "." {
					t.Errorf("dot import %q obscures product callsite gates in %s", pathValue, filepath.Base(filename))
				}
			}
			importAliases[pathValue] = alias
		}

		migrationsAlias := importAliases["github.com/progresshans/godj/migrations"]
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.CallExpr:
				selector, ok := current.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				identifier, ok := selector.X.(*ast.Ident)
				if ok && migrationsAlias != "" && identifier.Name == migrationsAlias && selector.Sel.Name == "NewPlanner" {
					plannerCalls++
				}
			case *ast.FuncDecl:
				if current.Recv == nil {
					if current.Name.IsExported() {
						exportedDeclarations[current.Name.Name] = "func"
					}
					return true
				}
				if current.Name.IsExported() {
					if receiver := receiverTypeName(current.Recv); receiver != "" {
						exportedMethods[receiver+"."+current.Name.Name] = struct{}{}
					}
				}
				if current.Name.Name != "Migrate" || !isSetReceiver(current.Recv) {
					return true
				}
				setMigrateMethods++
				if current.Body == nil {
					return true
				}
				if !isDirectSetMigrateReturn(current.Body) {
					t.Errorf("Set.Migrate must raw-return executor.Migrate(ctx, cloneMigrations(s.definitions), request), got %s", renderAST(t, fset, current.Body))
				}
				ast.Inspect(current.Body, func(bodyNode ast.Node) bool {
					call, ok := bodyNode.(*ast.CallExpr)
					if !ok {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "Migrate" {
						return true
					}
					receiver, ok := selector.X.(*ast.Ident)
					if !ok || receiver.Name != "executor" {
						t.Errorf("Set.Migrate must directly call the executor parameter, got receiver %T in %s", selector.X, filepath.Base(filename))
						return true
					}
					executorMigrateCalls++
					return true
				})
			}
			return true
		})

		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range general.Specs {
				switch typed := spec.(type) {
				case *ast.TypeSpec:
					if typed.Name.IsExported() {
						exportedDeclarations[typed.Name.Name] = "type"
					}
				case *ast.ValueSpec:
					for _, name := range typed.Names {
						if name.IsExported() {
							exportedDeclarations[name.Name] = general.Tok.String()
						}
					}
				}
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range valueSpec.Names {
					if general.Tok == token.CONST && name.Name == "SchemaIRVersion" {
						if index >= len(valueSpec.Values) {
							t.Errorf("SchemaIRVersion has no explicit literal initializer in %s", filepath.Base(filename))
							continue
						}
						literal, ok := valueSpec.Values[index].(*ast.BasicLit)
						if !ok || literal.Kind != token.INT || literal.Value != "2" {
							t.Errorf("SchemaIRVersion must be literal integer 2, got %s", renderAST(t, fset, valueSpec.Values[index]))
							continue
						}
						schemaIRLiteral++
					}
					if general.Tok != token.VAR || name.Name != "_" || valueSpec.Type == nil {
						continue
					}
					arrayType, ok := valueSpec.Type.(*ast.ArrayType)
					if !ok || arrayType.Len == nil {
						continue
					}
					expression := strings.ReplaceAll(renderAST(t, fset, arrayType.Len), " ", "")
					if _, wanted := driftAssertions[expression]; wanted {
						driftAssertions[expression] = true
					}
				}
			}
		}
	}

	if schemaIRLiteral != 1 {
		t.Errorf("literal SchemaIRVersion declarations = %d, want exactly 1", schemaIRLiteral)
	}
	for expression, found := range driftAssertions {
		if !found {
			t.Errorf("missing two-way Schema IR compile drift assertion %s", expression)
		}
	}
	if plannerCalls != 1 {
		t.Errorf("non-test definition product direct migrations.NewPlanner calls = %d, want exactly 1", plannerCalls)
	}
	if setMigrateMethods != 1 || executorMigrateCalls != 1 {
		t.Errorf("Set.Migrate methods/direct executor.Migrate calls = %d/%d, want 1/1", setMigrateMethods, executorMigrateCalls)
	}
	wantDeclarations := exactPublicDefinitionDeclarations()
	if !reflect.DeepEqual(exportedDeclarations, wantDeclarations) {
		t.Errorf("public definition declarations = %v, want exactly %v", exportedDeclarations, wantDeclarations)
	}
	wantMethods := map[string]struct{}{
		"Error.Context": {}, "Error.Error": {}, "FailureContext.GraphSources": {}, "LoadReport.Failure": {},
		"Set.Definitions": {}, "Set.Digest": {}, "Set.Migrate": {}, "Set.Sources": {},
	}
	if !reflect.DeepEqual(exportedMethods, wantMethods) {
		t.Errorf("public definition methods = %v, want exactly %v", exportedMethods, wantMethods)
	}

	rootMigrationFiles, globErr := filepath.Glob(filepath.Join(directory, "..", "*.go"))
	if globErr != nil {
		t.Fatalf("list root migrations package: %v", globErr)
	}
	for _, filename := range rootMigrationFiles {
		parsed, parseErr := parser.ParseFile(fset, filename, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse root migrations file %s: %v", filename, parseErr)
		}
		for _, imported := range parsed.Imports {
			pathValue, _ := strconv.Unquote(imported.Path.Value)
			if pathValue == "github.com/progresshans/godj/migrations/definition" {
				t.Errorf("forbidden reverse dependency in %s: migrations -> migrations/definition", filepath.Base(filename))
			}
		}
	}
}

func definitionPackageDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve external test source")
	}
	return filepath.Dir(filename)
}

func forbiddenDefinitionImport(pathValue string) bool {
	if pathValue == "os" || pathValue == "io" || strings.HasPrefix(pathValue, "io/") ||
		pathValue == "path" || strings.HasPrefix(pathValue, "path/") || pathValue == "embed" ||
		pathValue == "plugin" || pathValue == "unsafe" {
		return true
	}
	return strings.Contains(pathValue, "/conformance")
}

func importBase(pathValue string) string {
	if index := strings.LastIndexByte(pathValue, '/'); index >= 0 {
		return pathValue[index+1:]
	}
	return pathValue
}

func isSetReceiver(receiver *ast.FieldList) bool {
	if receiver == nil || len(receiver.List) != 1 {
		return false
	}
	typeExpression := receiver.List[0].Type
	if pointer, ok := typeExpression.(*ast.StarExpr); ok {
		typeExpression = pointer.X
	}
	identifier, ok := typeExpression.(*ast.Ident)
	return ok && identifier.Name == "Set"
}

func receiverTypeName(receiver *ast.FieldList) string {
	if receiver == nil || len(receiver.List) != 1 {
		return ""
	}
	typeExpression := receiver.List[0].Type
	if pointer, ok := typeExpression.(*ast.StarExpr); ok {
		typeExpression = pointer.X
	}
	identifier, ok := typeExpression.(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}

func isDirectSetMigrateReturn(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) != 1 {
		return false
	}
	returnStatement, ok := body.List[0].(*ast.ReturnStmt)
	if !ok || len(returnStatement.Results) != 1 {
		return false
	}
	call, ok := returnStatement.Results[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 3 {
		return false
	}
	method, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || method.Sel.Name != "Migrate" {
		return false
	}
	executor, ok := method.X.(*ast.Ident)
	if !ok || executor.Name != "executor" {
		return false
	}
	ctx, ok := call.Args[0].(*ast.Ident)
	if !ok || ctx.Name != "ctx" {
		return false
	}
	clone, ok := call.Args[1].(*ast.CallExpr)
	if !ok || len(clone.Args) != 1 {
		return false
	}
	cloneFunction, ok := clone.Fun.(*ast.Ident)
	if !ok || cloneFunction.Name != "cloneMigrations" {
		return false
	}
	definitions, ok := clone.Args[0].(*ast.SelectorExpr)
	if !ok || definitions.Sel.Name != "definitions" {
		return false
	}
	set, ok := definitions.X.(*ast.Ident)
	if !ok || set.Name != "s" {
		return false
	}
	request, ok := call.Args[2].(*ast.Ident)
	return ok && request.Name == "request"
}

func exactPublicDefinitionDeclarations() map[string]string {
	return map[string]string{
		"DefinitionFormatVersion":          "const",
		"LoaderABIVersion":                 "const",
		"OperationCodecVersion":            "const",
		"SchemaIRVersion":                  "const",
		"EmptySetDigest":                   "const",
		"CategorySource":                   "const",
		"CodeInvalidSource":                "const",
		"CodeInvalidDocument":              "const",
		"CodeDefinitionFormatIncompatible": "const",
		"CodeLoaderABIIncompatible":        "const",
		"CodeOperationCodecIncompatible":   "const",
		"CodeSchemaIRIncompatible":         "const",
		"CodeUnsupportedOperation":         "const",
		"CodeInvalidOperation":             "const",
		"CodeInvalidIR":                    "const",
		"MaxSources":                       "const",
		"MaxSourceIDBytes":                 "const",
		"MaxDocumentBytes":                 "const",
		"MaxBatchBytes":                    "const",
		"MaxJSONDepth":                     "const",
		"MaxDocumentJSONValues":            "const",
		"MaxJSONValues":                    "const",
		"MaxDependenciesPerMigration":      "const",
		"MaxOperationsPerMigration":        "const",
		"MaxFieldsPerCreateModel":          "const",
		"Source":                           "type",
		"Producer":                         "type",
		"SourceInfo":                       "type",
		"Set":                              "type",
		"GraphSource":                      "type",
		"FailureContext":                   "type",
		"LoadReport":                       "type",
		"Error":                            "type",
		"ErrorCode":                        "type",
		"Load":                             "func",
	}
}

func renderAST(t *testing.T, fset *token.FileSet, node any) string {
	t.Helper()
	var output bytes.Buffer
	if err := format.Node(&output, fset, node); err != nil {
		t.Fatalf("render AST node: %v", err)
	}
	return output.String()
}
