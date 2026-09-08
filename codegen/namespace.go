package codegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path"
	"strconv"
)

type relationCompanion uint8

const (
	companionQuery relationCompanion = iota
	companionObject
	companionReverse
	companionPrefetch
	companionSelect
	companionDelete
	companionFacade
)

// A standalone companion is checked against the actual declarations of its
// prerequisite companions. The whole-project path renders the same raw files
// directly and finalizes once, after attaching snapshot markers.
func generateProjectCompanion(packageName string, plan *relationProjectPlan, through relationCompanion) ([]byte, error) {
	bridges := make([]BridgePackage, len(plan.apps))
	for index, app := range plan.apps {
		bridges[index] = BridgePackage{Alias: app.alias, ImportPath: app.importPath}
	}
	binding, err := generateProjectBridge(packageName, bridges)
	if err != nil {
		return nil, err
	}
	files := []projectRenderedFile{{path: "bindings.go", source: binding}}
	renderers := []func(string, *relationProjectPlan) ([]byte, error){
		generateProjectRelationQuery, generateProjectRelationObject, generateProjectRelationReverse,
		generateProjectRelationPrefetch, generateProjectRelationSelectRelated, generateProjectRelationDelete,
		generateProjectRelationFacade,
	}
	for index, render := range renderers[:int(through)+1] {
		source, err := render(packageName, plan)
		if err != nil {
			return nil, err
		}
		files = append(files, projectRenderedFile{path: fmt.Sprintf("companion%d.go", index), source: source})
	}
	if err := finalizeGeneratedFiles(files); err != nil {
		return nil, err
	}
	return files[len(files)-1].source, nil
}

func finalizeSource(source []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	files := []projectRenderedFile{{path: "generated.go", source: source}}
	if err := finalizeGeneratedFiles(files); err != nil {
		return nil, err
	}
	return files[0].source, nil
}

// generatedNamespace is the one owner of generated package declarations and
// receiver-member namespaces. Names come from generated ASTs, never a second
// handwritten roster of the declarations another renderer is expected to emit.
type generatedNamespace struct {
	names   map[string]string
	imports map[string]string
	members map[string]map[string]string
}

func (namespace *generatedNamespace) add(name, owner string) error {
	if name == "_" {
		return nil
	}
	if previous, duplicate := namespace.names[name]; duplicate {
		return fmt.Errorf("generated package symbol %s for %s conflicts with %s", name, owner, previous)
	}
	if previous, duplicate := namespace.imports[name]; duplicate {
		return fmt.Errorf("generated package symbol %s for %s conflicts with import in %s", name, owner, previous)
	}
	namespace.names[name] = owner
	return nil
}

func (namespace *generatedNamespace) member(receiver, name, owner string) error {
	if name == "_" {
		return nil
	}
	names := namespace.members[receiver]
	if names == nil {
		names = make(map[string]string)
		namespace.members[receiver] = names
	}
	if previous, duplicate := names[name]; duplicate {
		return fmt.Errorf("generated %s member %s for %s conflicts with %s", receiver, name, owner, previous)
	}
	names[name] = owner
	return nil
}

func finalizeGeneratedFiles(files []projectRenderedFile) error {
	packages := make(map[string]*generatedNamespace)
	for index := range files {
		candidate := &files[index]
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, candidate.path, candidate.source, parser.ParseComments|parser.AllErrors)
		if err != nil {
			return fmt.Errorf("parse generated output %q: %w", candidate.path, err)
		}
		directory := path.Dir(candidate.path)
		namespace := packages[directory]
		if namespace == nil {
			namespace = &generatedNamespace{names: make(map[string]string), imports: make(map[string]string), members: make(map[string]map[string]string)}
			packages[directory] = namespace
		}
		if err := namespace.inspect(parsed, candidate.path); err != nil {
			return err
		}
		var formatted bytes.Buffer
		if err := format.Node(&formatted, fileSet, parsed); err != nil {
			return fmt.Errorf("format generated output %q: %w", candidate.path, err)
		}
		candidate.source = formatted.Bytes()
	}
	return nil
}

func (namespace *generatedNamespace) inspect(file *ast.File, filename string) error {
	for _, imported := range file.Imports {
		var name string
		if imported.Name != nil {
			name = imported.Name.Name
		} else {
			// Generated app imports without aliases are framework and standard
			// packages whose package name matches their final path element.
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return fmt.Errorf("decode generated import in %s: %w", filename, err)
			}
			name = path.Base(importPath)
		}
		if name == "_" || name == "." {
			continue
		}
		if previous, collision := namespace.names[name]; collision {
			return fmt.Errorf("import alias %s in %s conflicts with %s", name, filename, previous)
		}
		namespace.imports[name] = filename
	}
	for _, declaration := range file.Decls {
		switch current := declaration.(type) {
		case *ast.FuncDecl:
			if current.Recv == nil {
				if err := namespace.add(current.Name.Name, filename); err != nil {
					return err
				}
			} else if err := namespace.member(typeName(current.Recv.List[0].Type), current.Name.Name, filename); err != nil {
				return err
			}
		case *ast.GenDecl:
			for _, specification := range current.Specs {
				switch value := specification.(type) {
				case *ast.TypeSpec:
					if err := namespace.add(value.Name.Name, filename); err != nil {
						return err
					}
					if structure, ok := value.Type.(*ast.StructType); ok {
						for _, field := range structure.Fields.List {
							if len(field.Names) == 0 {
								if err := namespace.member(value.Name.Name, typeName(field.Type), filename); err != nil {
									return err
								}
							}
							for _, name := range field.Names {
								if err := namespace.member(value.Name.Name, name.Name, filename); err != nil {
									return err
								}
							}
						}
					}
				case *ast.ValueSpec:
					for _, name := range value.Names {
						if err := namespace.add(name.Name, filename); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func typeName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	case *ast.StarExpr:
		return typeName(value.X)
	case *ast.IndexExpr:
		return typeName(value.X)
	case *ast.IndexListExpr:
		return typeName(value.X)
	default:
		return ""
	}
}
