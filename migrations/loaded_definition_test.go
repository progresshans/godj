package migrations

import (
	"fmt"

	"github.com/progresshans/godj/migrations/internal/loadeddefinition"
)

// testLoadedDefinitionSet is deliberately test-only. Production callers can
// obtain an executable lifecycle authority only through definition.Load.
func testLoadedDefinitionSet(definitions []Migration) LoadedDefinitionSet {
	sources := make([]DefinitionSourceInfo, len(definitions))
	for index := range definitions {
		sources[index] = DefinitionSourceInfo{
			SourceID:  fmt.Sprintf("test-definition-%04d", index),
			Producer:  DefinitionProducer{Name: "migrations-test", Version: "1"},
			Migration: definitions[index].Key(),
		}
	}
	publication := loadeddefinition.New(
		definitions,
		"sha256:test-loaded-definition-set",
		sources,
		cloneMigrationDefinitions,
		func(values []DefinitionSourceInfo) []DefinitionSourceInfo {
			return append([]DefinitionSourceInfo(nil), values...)
		},
	)
	return LoadedDefinitionSet(publication)
}
