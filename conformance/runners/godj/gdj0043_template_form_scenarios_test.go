package godj

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestTemplateFormScenarioHandlerObservesPhaseDContracts(t *testing.T) {
	tests := []struct {
		id       string
		scenario string
		phase    protocol.Phase
		dbState  bool
		metrics  map[string]string
	}{
		{id: "WEB-021", scenario: "django.template_form.scalar_and_missing", phase: protocol.PhaseEvaluation, metrics: map[string]string{"rendered_bytes": "8"}},
		{id: "WEB-022", scenario: "django.template_form.dotted_lookup_precedence", phase: protocol.PhaseEvaluation, metrics: map[string]string{"callable_invocations": "0", "object_dictionary_lookups": "0"}},
		{id: "WEB-023", scenario: "django.template_form.autoescape_and_safe", phase: protocol.PhaseEvaluation, metrics: map[string]string{"rendered_bytes": "29", "safe_capabilities": "1"}},
		{id: "WEB-024", scenario: "django.template_form.if_for_and_empty", phase: protocol.PhaseEvaluation, metrics: map[string]string{"loop_items": "2", "renders": "2"}},
		{id: "WEB-025", scenario: "django.template_form.closed_filters", phase: protocol.PhaseEvaluation, metrics: map[string]string{"filters_evaluated": "3"}},
		{id: "WEB-026", scenario: "django.template_form.construction_failures", phase: protocol.PhaseConstruction, metrics: map[string]string{"accepted": "0", "rejected": "4"}},
		{id: "WEB-027", scenario: "django.template_form.callable_exposure", phase: protocol.PhaseEvaluation, metrics: map[string]string{"callable_invocations": "0"}},
		{id: "FRM-001", scenario: "django.template_form.unbound_and_bound_empty", phase: protocol.PhaseEvaluation, metrics: map[string]string{"forms_bound": "1", "forms_constructed": "2"}},
		{id: "FRM-002", scenario: "django.template_form.valid_article_clean", phase: protocol.PhaseEvaluation, metrics: map[string]string{"cleaned_fields": "3", "validation_errors": "0"}},
		{id: "FRM-003", scenario: "django.template_form.field_error_codes", phase: protocol.PhaseEvaluation, metrics: map[string]string{"cases": "3", "valid_cases": "0"}},
		{id: "FRM-004", scenario: "django.template_form.cross_field_validation", phase: protocol.PhaseEvaluation, metrics: map[string]string{"cross_field_validators": "1", "validation_errors": "2"}},
		{id: "FRM-005", scenario: "django.template_form.model_form_write_boundary", phase: protocol.PhaseCommit, dbState: true, metrics: map[string]string{"create_writes": "1", "invalid_writes": "0", "update_writes": "1"}},
	}

	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			handler, ok := templateFormScenarioHandler(test.scenario)
			if !ok || handler == nil {
				t.Fatalf("templateFormScenarioHandler(%q) = %#v, %v", test.scenario, handler, ok)
			}
			observation, err := handler(context.Background(), protocol.Contract{
				ID:       test.id,
				Scenario: test.scenario,
				Phase:    test.phase,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := observation.Validate(); err != nil {
				t.Fatalf("observation validation: %v", err)
			}
			if observation.ID != test.id || observation.Status != protocol.StatusObserved || observation.Phase != test.phase {
				t.Fatalf("observation identity = %#v", observation)
			}
			if observation.Result == nil || observation.Error != nil || observation.Metrics == nil {
				t.Fatalf("observation dimensions = result %v error %v metrics %v", observation.Result != nil, observation.Error != nil, observation.Metrics != nil)
			}
			if got := observation.DBState != nil; got != test.dbState {
				t.Fatalf("db_state present = %v, want %v", got, test.dbState)
			}
			for name, want := range test.metrics {
				metric := templateFormTestObjectField(t, *observation.Metrics, name)
				if metric.Type != protocol.ValueInt || metric.Text == nil || *metric.Text != want {
					t.Fatalf("metric %q = %#v, want int %q", name, metric, want)
				}
			}
		})
	}
}

func TestTemplateFormScenarioHandlerRejectsUnknownScenario(t *testing.T) {
	if handler, ok := templateFormScenarioHandler("django.template_form.scalar_and_missing.extra"); ok || handler != nil {
		t.Fatalf("near-miss handler = %#v, %v", handler, ok)
	}
}

func TestTemplateFormCallableExposureObservesClosedNoCallValue(t *testing.T) {
	handler, ok := templateFormScenarioHandler("django.template_form.callable_exposure")
	if !ok {
		t.Fatal("callable exposure scenario is not registered")
	}
	observation, err := handler(context.Background(), protocol.Contract{ID: "WEB-027"})
	if err != nil {
		t.Fatal(err)
	}
	autoCalled := templateFormTestObjectField(t, *observation.Result, "auto_called")
	if autoCalled.Type != protocol.ValueBool || autoCalled.Bool == nil || *autoCalled.Bool {
		t.Fatalf("auto_called = %#v, want false", autoCalled)
	}
	category := templateFormTestObjectField(t, *observation.Result, "rendered_return_category")
	if category.Type != protocol.ValueString || category.Text == nil || *category.Text != "closed_value" {
		t.Fatalf("rendered_return_category = %#v, want closed_value", category)
	}
	invocations := templateFormTestObjectField(t, *observation.Metrics, "callable_invocations")
	if invocations.Type != protocol.ValueInt || invocations.Text == nil || *invocations.Text != "0" {
		t.Fatalf("callable_invocations = %#v, want 0", invocations)
	}
}

func TestTemplateFormDottedLookupObservesClosedPublicAlgebra(t *testing.T) {
	handler, ok := templateFormScenarioHandler("django.template_form.dotted_lookup_precedence")
	if !ok {
		t.Fatal("dotted lookup scenario is not registered")
	}
	observation, err := handler(context.Background(), protocol.Contract{ID: "WEB-022"})
	if err != nil {
		t.Fatal(err)
	}
	shadowed := templateFormTestObjectField(t, *observation.Result, "attribute_fallback_shadowed")
	if shadowed.Type != protocol.ValueBool || shadowed.Bool == nil || *shadowed.Bool {
		t.Fatalf("attribute_fallback_shadowed = %#v, want false without a competing attribute fallback", shadowed)
	}
	for name, want := range map[string]string{
		"dictionary":        "mapping-value",
		"list_index":        "one",
		"object_dictionary": "dictionary-value",
	} {
		got := templateFormTestObjectField(t, *observation.Result, name)
		if got.Type != protocol.ValueString || got.Text == nil || *got.Text != want {
			t.Fatalf("result %q = %#v, want string %q", name, got, want)
		}
	}
	lookups := templateFormTestObjectField(t, *observation.Metrics, "object_dictionary_lookups")
	if lookups.Type != protocol.ValueInt || lookups.Text == nil || *lookups.Text != "0" {
		t.Fatalf("object_dictionary_lookups = %#v, want no application callback in the closed algebra", lookups)
	}
}

type templateFormASTTypeDefinition struct {
	expression ast.Expr
	imports    map[string]string
}

func TestTemplateValueAPIRemainsClosedToCallables(t *testing.T) {
	packages, err := parser.ParseDir(token.NewFileSet(), "../../../templates", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, ok := packages["templates"]
	if !ok {
		t.Fatalf("parsed packages = %#v, want templates", packages)
	}
	definitions := make(map[string]templateFormASTTypeDefinition)
	importsByFile := make(map[*ast.File]map[string]string, len(pkg.Files))
	for name, file := range pkg.Files {
		imports := templateFormImportPaths(file)
		importsByFile[file] = imports
		for _, path := range imports {
			if path == "reflect" {
				t.Fatalf("closed template implementation %s imports reflect", name)
			}
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range general.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				definitions[typeSpec.Name.Name] = templateFormASTTypeDefinition{
					expression: typeSpec.Type,
					imports:    imports,
				}
				if typeSpec.Name.Name != "Value" {
					continue
				}
				structure, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					t.Fatalf("templates.Value type = %T, want closed struct", typeSpec.Type)
				}
				for _, field := range structure.Fields.List {
					for _, fieldName := range field.Names {
						if ast.IsExported(fieldName.Name) {
							t.Fatalf("templates.Value exposes field %s", fieldName.Name)
						}
					}
				}
			}
		}
	}

	constructors := make([]string, 0, 7)
	for _, file := range pkg.Files {
		imports := importsByFile[file]
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil && !ast.IsExported(function.Name.Name) {
				continue
			}
			if boundary := templateFormFunctionCallableBoundary(function, definitions, imports); boundary != "" {
				t.Fatalf("templates API %s exposes callable ingress through %s", function.Name.Name, boundary)
			}
			if function.Recv == nil && templateFormTypeMentions(function.Type.Results, "Value") {
				constructors = append(constructors, function.Name.Name)
			}
		}
	}
	sort.Strings(constructors)
	want := []string{"Bool", "Integer", "List", "Null", "Object", "String", "TrustedHTML"}
	if strings.Join(constructors, ",") != strings.Join(want, ",") {
		t.Fatalf("exported Value constructors = %v, want %v; review WEB-027 before changing the closed algebra", constructors, want)
	}
}

func TestTemplateFormCallableBoundaryDetectionAllowsOnlyTypedUnrelatedPackages(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		imports    map[string]string
		unsafe     bool
	}{
		{name: "function", expression: "func() string", unsafe: true},
		{name: "any", expression: "any", unsafe: true},
		{name: "nested any", expression: "map[string]any", unsafe: true},
		{name: "empty interface", expression: "interface{}", unsafe: true},
		{name: "function field", expression: "struct { Callback func() }", unsafe: true},
		{name: "reflection", expression: "reflect.Value", imports: map[string]string{"reflect": "reflect"}, unsafe: true},
		{name: "context", expression: "context.Context", imports: map[string]string{"context": "context"}},
		{name: "filesystem", expression: "fs.FS", imports: map[string]string{"fs": "io/fs"}},
		{name: "typed capability", expression: "interface { Token(context.Context) (string, error) }", imports: map[string]string{"context": "context"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expression, err := parser.ParseExpr(test.expression)
			if err != nil {
				t.Fatal(err)
			}
			boundary := templateFormCallableBoundary(expression, nil, test.imports, make(map[string]bool))
			if got := boundary != ""; got != test.unsafe {
				t.Fatalf("callable boundary for %s = %q, unsafe=%t", test.expression, boundary, test.unsafe)
			}
		})
	}
}

func templateFormTypeMentions(fields *ast.FieldList, identifier string) bool {
	if fields == nil {
		return false
	}
	found := false
	for _, field := range fields.List {
		ast.Inspect(field.Type, func(node ast.Node) bool {
			if name, ok := node.(*ast.Ident); ok && name.Name == identifier {
				found = true
				return false
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

func templateFormImportPaths(file *ast.File) map[string]string {
	paths := make(map[string]string, len(file.Imports))
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		} else if slash := strings.LastIndexByte(importPath, '/'); slash >= 0 {
			name = importPath[slash+1:]
		} else {
			name = importPath
		}
		if name != "_" {
			paths[name] = importPath
		}
	}
	return paths
}

func templateFormFunctionCallableBoundary(
	function *ast.FuncDecl,
	definitions map[string]templateFormASTTypeDefinition,
	imports map[string]string,
) string {
	fields := []struct {
		name   string
		values *ast.FieldList
	}{
		{name: "receiver", values: function.Recv},
		{name: "type parameters", values: function.Type.TypeParams},
		{name: "parameters", values: function.Type.Params},
		{name: "results", values: function.Type.Results},
	}
	for _, group := range fields {
		if boundary := templateFormFieldsCallableBoundary(group.values, definitions, imports, make(map[string]bool)); boundary != "" {
			return group.name + ": " + boundary
		}
	}
	return ""
}

func templateFormFieldsCallableBoundary(
	fields *ast.FieldList,
	definitions map[string]templateFormASTTypeDefinition,
	imports map[string]string,
	visiting map[string]bool,
) string {
	if fields == nil {
		return ""
	}
	for _, field := range fields.List {
		if boundary := templateFormCallableBoundary(field.Type, definitions, imports, visiting); boundary != "" {
			return boundary
		}
	}
	return ""
}

func templateFormCallableBoundary(
	expression ast.Expr,
	definitions map[string]templateFormASTTypeDefinition,
	imports map[string]string,
	visiting map[string]bool,
) string {
	switch typed := expression.(type) {
	case *ast.FuncType:
		return "function type"
	case *ast.InterfaceType:
		if typed.Methods == nil || len(typed.Methods.List) == 0 {
			return "empty interface"
		}
		for _, method := range typed.Methods.List {
			if function, ok := method.Type.(*ast.FuncType); ok {
				if boundary := templateFormFieldsCallableBoundary(function.TypeParams, definitions, imports, visiting); boundary != "" {
					return boundary
				}
				if boundary := templateFormFieldsCallableBoundary(function.Params, definitions, imports, visiting); boundary != "" {
					return boundary
				}
				if boundary := templateFormFieldsCallableBoundary(function.Results, definitions, imports, visiting); boundary != "" {
					return boundary
				}
				continue
			}
			if boundary := templateFormCallableBoundary(method.Type, definitions, imports, visiting); boundary != "" {
				return boundary
			}
		}
	case *ast.Ident:
		if typed.Name == "any" {
			return "any"
		}
		definition, ok := definitions[typed.Name]
		if !ok || visiting[typed.Name] {
			return ""
		}
		visiting[typed.Name] = true
		boundary := templateFormCallableBoundary(definition.expression, definitions, definition.imports, visiting)
		delete(visiting, typed.Name)
		return boundary
	case *ast.SelectorExpr:
		if qualifier, ok := typed.X.(*ast.Ident); ok && imports[qualifier.Name] == "reflect" {
			return "reflection type"
		}
	case *ast.StructType:
		return templateFormFieldsCallableBoundary(typed.Fields, definitions, imports, visiting)
	case *ast.ArrayType:
		return templateFormCallableBoundary(typed.Elt, definitions, imports, visiting)
	case *ast.MapType:
		if boundary := templateFormCallableBoundary(typed.Key, definitions, imports, visiting); boundary != "" {
			return boundary
		}
		return templateFormCallableBoundary(typed.Value, definitions, imports, visiting)
	case *ast.StarExpr:
		return templateFormCallableBoundary(typed.X, definitions, imports, visiting)
	case *ast.ChanType:
		return templateFormCallableBoundary(typed.Value, definitions, imports, visiting)
	case *ast.Ellipsis:
		return templateFormCallableBoundary(typed.Elt, definitions, imports, visiting)
	case *ast.ParenExpr:
		return templateFormCallableBoundary(typed.X, definitions, imports, visiting)
	case *ast.IndexExpr:
		if boundary := templateFormCallableBoundary(typed.X, definitions, imports, visiting); boundary != "" {
			return boundary
		}
		return templateFormCallableBoundary(typed.Index, definitions, imports, visiting)
	case *ast.IndexListExpr:
		if boundary := templateFormCallableBoundary(typed.X, definitions, imports, visiting); boundary != "" {
			return boundary
		}
		for _, index := range typed.Indices {
			if boundary := templateFormCallableBoundary(index, definitions, imports, visiting); boundary != "" {
				return boundary
			}
		}
	case *ast.UnaryExpr:
		return templateFormCallableBoundary(typed.X, definitions, imports, visiting)
	case *ast.BinaryExpr:
		if boundary := templateFormCallableBoundary(typed.X, definitions, imports, visiting); boundary != "" {
			return boundary
		}
		return templateFormCallableBoundary(typed.Y, definitions, imports, visiting)
	}
	return ""
}

func TestTemplateFormModelFormWriteBoundaryObservesExactRows(t *testing.T) {
	handler, ok := templateFormScenarioHandler("django.template_form.model_form_write_boundary")
	if !ok {
		t.Fatal("model form write scenario is not registered")
	}
	observation, err := handler(context.Background(), protocol.Contract{ID: "FRM-005"})
	if err != nil {
		t.Fatal(err)
	}
	before := templateFormTestObjectField(t, *observation.DBState, "before")
	after := templateFormTestObjectField(t, *observation.DBState, "after")
	if before.Type != protocol.ValueList || len(before.Items) != 4 {
		t.Fatalf("before rows = %#v, want 4", before)
	}
	if after.Type != protocol.ValueList || len(after.Items) != 5 {
		t.Fatalf("after rows = %#v, want 5", after)
	}
	updated := after.Items[0]
	if got := templateFormTestObjectField(t, updated, "title"); got.Type != protocol.ValueString || got.Text == nil || *got.Text != "Updated" {
		t.Fatalf("updated title = %#v", got)
	}
	created := after.Items[4]
	if got := templateFormTestObjectField(t, created, "title"); got.Type != protocol.ValueString || got.Text == nil || *got.Text != "Created" {
		t.Fatalf("created title = %#v", got)
	}
}

func templateFormTestObjectField(t *testing.T, value protocol.Value, name string) protocol.Value {
	t.Helper()
	if value.Type != protocol.ValueObject {
		t.Fatalf("value = %#v, want object", value)
	}
	for _, field := range value.Fields {
		if field.Name == name {
			return field.Value
		}
	}
	t.Fatalf("object has no field %q: %#v", name, value)
	return protocol.Value{}
}
