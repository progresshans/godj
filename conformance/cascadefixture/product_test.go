package cascadefixture_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/progresshans/godj/codegen"
	fixture "github.com/progresshans/godj/conformance/cascadefixture"
	"github.com/progresshans/godj/conformance/cascadefixture/parents"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/projectgenerate"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestCascadeGeneratedFixtureMatchesDeclaration(t *testing.T) {
	spec, err := fixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	report, err := projectgenerate.Check(t.Context(), ".", bundle)
	if err != nil || !report.Clean() {
		t.Fatal("generated cascade fixture drift", report, err)
	}
}

func TestCascadeGeneratedRootFingerprintRejectsDescendantDriftBeforeIO(t *testing.T) {
	spec, err := fixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	original := rootFingerprint(t, spec)
	for _, change := range []string{"policy", "column", "table", "primary key", "nullability", "cardinality", "unrelated"} {
		t.Run(change, func(t *testing.T) {
			changed, err := fixture.ProjectSpec(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for a := range changed.Apps {
				for m := range changed.Apps[a].Schema.Models {
					model := &changed.Apps[a].Schema.Models[m]
					if change == "unrelated" && model.Name == "node" {
						model.Fields[1].Relation.OnDelete = ir.DeleteSetNull
					}
					if model.Name != "grandchild" {
						continue
					}
					switch change {
					case "policy":
						model.Fields[1].Relation.OnDelete = ir.DeleteProtect
					case "column":
						model.Fields[1].Column = "ancestor_id"
					case "table":
						model.DBTable = "cascade_relocated_grandchild"
					case "primary key":
						model.Fields[0].Column = "primary_id"
					case "nullability":
						model.Fields[1].Nullable = true
					case "cardinality":
						model.Fields[1].Relation.Cardinality = ir.RelationOneToOne
						model.Fields[1].Unique = true
					}
				}
			}
			fingerprint := rootFingerprint(t, changed)
			if (fingerprint == original) != (change == "unrelated") {
				t.Fatal("root fingerprint omitted descendant semantics or included an unrelated node")
			}
			var schemas []ir.Schema
			for _, app := range changed.Apps {
				schemas = append(schemas, app.Schema)
			}
			binding, err := orm.BindProject(schemas...)
			if err != nil {
				t.Fatal(err)
			}
			deleter, err := orm.BindRelationDeleter(binding, ir.ModelIdentity{AppLabel: "cascadeparents", ModelName: "root"}, parents.RootDescriptor{}, original)
			if change == "unrelated" {
				if err != nil {
					t.Fatal("unrelated policy changed the root capability", err)
				}
				return
			}
			if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
				t.Fatal("stale descendant binding was accepted", err)
			}
			backend := &forbiddenAtomic{}
			root := parents.Root{Name: "held"}
			(parents.RootDescriptor{}).SetPrimaryKey(&root, 1)
			if count, err := deleter.Delete(t.Context(), backend, &root); count != 0 || err == nil || backend.calls != 0 || root.ID != 1 {
				t.Fatal("stale capability reached I/O or caller state", count, err, backend.calls)
			}
		})
	}
}

type forbiddenAtomic struct{ calls int }

func (b *forbiddenAtomic) AtomicRelation(context.Context, func(db.RelationSession) error) error {
	b.calls++
	return errors.New("unexpected I/O")
}

func rootFingerprint(t *testing.T, spec codegen.ProjectSpec) string {
	t.Helper()
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	var source []byte
	for _, file := range bundle.Files() {
		if file.Path == "project/zz_godj_relation_delete.go" {
			source = file.Source()
		}
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "generated.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 4 {
			return true
		}
		function, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || function.Sel.Name != "BindRelationDeleter" {
			return true
		}
		identity, ok := call.Args[1].(*ast.CompositeLit)
		if !ok {
			return true
		}
		fields := map[string]string{}
		for _, element := range identity.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := pair.Key.(*ast.Ident)
			if !ok {
				continue
			}
			value, ok := pair.Value.(*ast.BasicLit)
			if !ok {
				continue
			}
			fields[key.Name], err = strconv.Unquote(value.Value)
			if err != nil {
				t.Fatal(err)
			}
		}
		if fields["AppLabel"] != "cascadeparents" || fields["ModelName"] != "root" {
			return true
		}
		literal, ok := call.Args[3].(*ast.BasicLit)
		if !ok || found != "" {
			t.Fatal("ambiguous generated root fingerprint")
		}
		found, err = strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		return true
	})
	if len(found) != 64 {
		t.Fatal("generated root fingerprint missing")
	}
	return found
}
