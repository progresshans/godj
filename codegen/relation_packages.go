package codegen

import (
	"fmt"
	"go/token"
	"sort"

	"github.com/progresshans/godj/internal/identifiers"
	"github.com/progresshans/godj/schema/ir"
)

type relationPackageInput struct {
	alias      string
	importPath string
	schema     ir.Schema
}

type relationPackagePolicy struct {
	name          string
	validAlias    func(string) bool
	reservedPaths []string
}

// canonicalRelationPackages is the only normalization and identity validation
// path for individual relation renderers. Whole-project generation already owns
// prepared app schemas and constructs its shared plan directly from those.
func canonicalRelationPackages(inputs []relationPackageInput, policy relationPackagePolicy) ([]normalizedRelationPackage, error) {
	canonical := make([]normalizedRelationPackage, len(inputs))
	for index, input := range inputs {
		prepared, err := prepareSchema(input.schema)
		if err != nil {
			return nil, fmt.Errorf("normalize relation %s package %q: %w", policy.name, input.alias, err)
		}
		if !policy.validAlias(input.alias) {
			return nil, fmt.Errorf("invalid relation %s package alias %q", policy.name, input.alias)
		}
		if !identifiers.ImportPath(input.importPath) {
			return nil, fmt.Errorf("invalid relation %s import path %q", policy.name, input.importPath)
		}
		canonical[index] = normalizedRelationPackage{
			alias: input.alias, prefix: exportedRelationQueryPrefix(input.alias),
			importPath: input.importPath, preparedSchema: prepared,
		}
	}
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].schema.AppLabel != canonical[right].schema.AppLabel {
			return canonical[left].schema.AppLabel < canonical[right].schema.AppLabel
		}
		if canonical[left].alias != canonical[right].alias {
			return canonical[left].alias < canonical[right].alias
		}
		return canonical[left].importPath < canonical[right].importPath
	})

	aliases := make(map[string]bool, len(canonical))
	paths := make(map[string]bool, len(canonical)+len(policy.reservedPaths))
	for _, path := range policy.reservedPaths {
		paths[path] = true
	}
	prefixes := make(map[string]bool, len(canonical))
	apps := make(map[string]bool, len(canonical))
	for _, app := range canonical {
		for _, identity := range []struct {
			kind  string
			value string
			seen  map[string]bool
		}{
			{"package alias", app.alias, aliases},
			{"import path", app.importPath, paths},
			{"exported prefix", app.prefix, prefixes},
			{"app label", app.schema.AppLabel, apps},
		} {
			if identity.seen[identity.value] {
				return nil, fmt.Errorf("duplicate relation %s %s %q", policy.name, identity.kind, identity.value)
			}
			identity.seen[identity.value] = true
		}
	}
	return canonical, nil
}

func validRelationAlias(alias string, reserved ...string) bool {
	if alias == "" || alias == "init" || token.Lookup(alias).IsKeyword() || alias[0] < 'a' || alias[0] > 'z' {
		return false
	}
	for _, name := range reserved {
		if alias == name {
			return false
		}
	}
	for index := 1; index < len(alias); index++ {
		current := alias[index]
		if !('a' <= current && current <= 'z') && !('A' <= current && current <= 'Z') && !('0' <= current && current <= '9') {
			return false
		}
	}
	return true
}
