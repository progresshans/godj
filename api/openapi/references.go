package openapi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/progresshans/godj/serializers"
)

const (
	maxSchemaNameBytes = 128
	maxNamedSchemas    = 256
	maxSchemaDepth     = 64
	maxSchemaNodes     = 4096
	schemaRefPrefix    = "#/components/schemas/"
)

// NamedSchema declares an explicit component identity without deduplicating or
// expanding its schema. Names are 1–128 ASCII bytes matching [A-Za-z0-9._-]+.
// A document accepts at most 256 components. Recursive references are not
// supported; any schema path, including reference edges, is limited to 64 schema
// nodes, and each inline schema to 4096 nodes. JSON serialization budgets still
// apply independently, including the document's total response byte limit.
type NamedSchema struct {
	Name   string
	Schema Schema
}

// Ref describes a local reference to one NamedSchema. The target can be declared
// later; document construction checks that it exists and is not recursive.
// Names use NamedSchema's bounded component-key grammar. Remote references,
// arbitrary JSON pointers, and sibling constraints on a reference are unsupported.
func Ref(name string) (Schema, error) {
	if !validSchemaName(name) {
		return Schema{}, schemaConfigError("reference", "reference name must be 1–128 ASCII letters, digits, dots, underscores, or hyphens")
	}
	return schemaObject(serializers.MemberOf("$ref", serializers.String(schemaRefPrefix+name)))
}

func validSchemaName(name string) bool {
	if len(name) == 0 || len(name) > maxSchemaNameBytes {
		return false
	}
	for index := range len(name) {
		character := name[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

// schemaCatalog snapshots declarations and memoizes graph depth at preparation.
// All fields are read-only afterward; validation never changes shared state.
type schemaCatalog struct {
	schemas map[string]Schema
	depths  map[string]int
	names   []string
}

func prepareSchemaCatalog(declarations []NamedSchema) (schemaCatalog, error) {
	if len(declarations) > maxNamedSchemas {
		return schemaCatalog{}, schemaConfigError("components", "component count exceeds 256")
	}
	catalog := schemaCatalog{
		schemas: make(map[string]Schema, len(declarations)),
		depths:  make(map[string]int, len(declarations)),
		names:   make([]string, 0, len(declarations)),
	}
	for _, declaration := range declarations {
		if !validSchemaName(declaration.Name) {
			return schemaCatalog{}, schemaConfigError("components", "component name must be 1–128 ASCII letters, digits, dots, underscores, or hyphens")
		}
		if _, duplicate := catalog.schemas[declaration.Name]; duplicate {
			return schemaCatalog{}, schemaConfigError("components", fmt.Sprintf("component %q is duplicated", declaration.Name))
		}
		if !schemaValid(declaration.Schema) {
			return schemaCatalog{}, schemaConfigError("components", fmt.Sprintf("component %q is zero or invalid", declaration.Name))
		}
		catalog.schemas[declaration.Name] = declaration.Schema
		catalog.names = append(catalog.names, declaration.Name)
	}
	sort.Strings(catalog.names)
	visiting := make(map[string]bool, len(declarations))
	var resolveDepth func(string) (int, error)
	resolveDepth = func(name string) (int, error) {
		if depth, found := catalog.depths[name]; found {
			return depth, nil
		}
		schema, found := catalog.schemas[name]
		if !found {
			return 0, schemaConfigError("reference", fmt.Sprintf("component %q is not declared", name))
		}
		if visiting[name] {
			return 0, schemaConfigError("reference", fmt.Sprintf("recursive schema reference through %q is unsupported", name))
		}
		if len(visiting) >= maxSchemaDepth {
			return 0, schemaConfigError("depth", "schema paths including references exceed 64 nodes")
		}
		visiting[name] = true
		depth, err := measureSchemaDepth(schema, resolveDepth)
		delete(visiting, name)
		if err != nil {
			return 0, err
		}
		catalog.depths[name] = depth
		return depth, nil
	}
	for _, name := range catalog.names {
		if _, err := resolveDepth(name); err != nil {
			return schemaCatalog{}, err
		}
	}
	return catalog, nil
}

func (catalog schemaCatalog) validate(schema Schema) error {
	_, err := measureSchemaDepth(schema, func(name string) (int, error) {
		depth, found := catalog.depths[name]
		if !found {
			return 0, schemaConfigError("reference", fmt.Sprintf("component %q is not declared", name))
		}
		return depth, nil
	})
	return err
}

func (catalog schemaCatalog) components() (map[string]json.RawMessage, error) {
	components := make(map[string]json.RawMessage, len(catalog.names))
	encodedBytes := 0
	for _, name := range catalog.names {
		encoded, err := rawSchema(catalog.schemas[name])
		if err != nil {
			return nil, fmt.Errorf("OpenAPI component %q: %w", name, err)
		}
		encodedBytes += len(encoded)
		// Components alone cannot fit in a document when their encodings already
		// exceed the existing one-MiB JSON budget. Bound temporary allocations
		// before the document encoder accounts for names and surrounding syntax.
		if encodedBytes > serializers.DefaultMaxDocumentBytes {
			return nil, schemaConfigError("components", "component encodings exceed the default JSON document byte limit")
		}
		components[name] = encoded
	}
	return components, nil
}

// resolveRoot follows only pure root aliases. Nested references remain intact;
// this does not establish structural equivalence for differently composed schemas.
func (catalog schemaCatalog) resolveRoot(schema Schema) (Schema, error) {
	if err := catalog.validate(schema); err != nil {
		return Schema{}, err
	}
	for {
		object, _ := schema.value.AsObject()
		name, referenced, err := schemaReference(object)
		if err != nil {
			return Schema{}, err
		}
		if !referenced {
			return schema, nil
		}
		schema = catalog.schemas[name]
	}
}

func schemaReference(object serializers.Object) (string, bool, error) {
	reference, found := object.Get("$ref")
	if !found {
		return "", false, nil
	}
	text, valid := reference.AsString()
	name, local := strings.CutPrefix(text, schemaRefPrefix)
	if !valid || !local || !validSchemaName(name) || object.Len() != 1 {
		return "", false, schemaConfigError("reference", "only a single local component reference without siblings is supported")
	}
	return name, true, nil
}

// measureSchemaDepth visits only positions owned by the schema vocabulary.
// Object property names and annotation/default payloads are ordinary JSON data,
// even when their member names happen to be "$ref", "items", or "anyOf".
func measureSchemaDepth(schema Schema, referenceDepth func(string) (int, error)) (int, error) {
	nodes := 0
	var visit func(serializers.Value, int) (int, error)
	visit = func(value serializers.Value, inlineDepth int) (int, error) {
		nodes++
		if nodes > maxSchemaNodes {
			return 0, schemaConfigError("nodes", "inline schema exceeds 4096 nodes")
		}
		if inlineDepth > maxSchemaDepth {
			return 0, schemaConfigError("depth", "schema paths including references exceed 64 nodes")
		}
		object, valid := value.AsObject()
		if !valid || !object.Valid() {
			return 0, schemaConfigError("value", "schema is zero or invalid")
		}
		name, referenced, err := schemaReference(object)
		if err != nil {
			return 0, err
		}
		depth := 1
		if referenced {
			targetDepth, err := referenceDepth(name)
			if err != nil {
				return 0, err
			}
			depth += targetDepth
		} else {
			childDepth := func(child serializers.Value) error {
				measured, err := visit(child, inlineDepth+1)
				if err == nil {
					depth = max(depth, 1+measured)
				}
				return err
			}
			if properties, found := object.Get("properties"); found {
				fields, valid := properties.AsObject()
				if !valid || !fields.Valid() {
					return 0, schemaConfigError("properties", "schema properties must be an object")
				}
				for _, property := range fields.Members() {
					if err := childDepth(property.Value()); err != nil {
						return 0, err
					}
				}
			}
			if items, found := object.Get("items"); found {
				if err := childDepth(items); err != nil {
					return 0, err
				}
			}
			if anyOf, found := object.Get("anyOf"); found {
				alternatives, valid := anyOf.AsList()
				if !valid || len(alternatives) == 0 {
					return 0, schemaConfigError("anyOf", "schema alternatives must be a nonempty list")
				}
				for _, alternative := range alternatives {
					if err := childDepth(alternative); err != nil {
						return 0, err
					}
				}
			}
		}
		if depth > maxSchemaDepth {
			return 0, schemaConfigError("depth", "schema paths including references exceed 64 nodes")
		}
		return depth, nil
	}
	return visit(schema.value, 1)
}
