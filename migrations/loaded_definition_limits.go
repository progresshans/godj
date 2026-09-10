package migrations

// The loader and executor enforce the same bounded current-definition
// envelope at independent trust boundaries. These values are intentionally
// private: they are implementation limits, not a persisted compatibility ABI.
const (
	maxLoadedDefinitions          = 2_048
	maxLoadedSourceIDBytes        = 1_024
	maxLoadedDefinitionBytes      = 1 << 20
	maxLoadedDefinitionSetBytes   = 16 << 20
	maxLoadedDefinitionNodes      = 262_144
	maxLoadedDependencies         = 2_047
	maxLoadedOperations           = 2_048
	maxLoadedFieldsPerCreateModel = 2_048
)
