package definition

const (
	MaxSources                  = 2_048
	MaxSourceIDBytes            = 1_024
	MaxDocumentBytes            = 1 << 20
	MaxBatchBytes               = 16 << 20
	MaxJSONDepth                = 64
	MaxDocumentJSONValues       = 65_536
	MaxJSONValues               = 262_144
	MaxDependenciesPerMigration = 2_047
	MaxOperationsPerMigration   = 2_048
	MaxFieldsPerCreateModel     = 2_048
)

const (
	resourceLimitReason = "resource_limit_exceeded"

	limitSourceCount              = "source_count"
	limitSourceIDBytes            = "source_id_bytes"
	limitDocumentBytes            = "document_bytes"
	limitBatchBytes               = "batch_bytes"
	limitJSONDepth                = "json_depth"
	limitDocumentJSONValues       = "document_json_values"
	limitJSONValues               = "json_values"
	limitDependenciesPerMigration = "dependencies_per_migration"
	limitOperationsPerMigration   = "operations_per_migration"
	limitFieldsPerCreateModel     = "fields_per_create_model"
)

// saturatingAdd reports whether left+right overflows uint64. On overflow it
// returns the largest uint64 value so callers never observe a wrapped, smaller
// resource measurement.
func saturatingAdd(left, right uint64) (uint64, bool) {
	const maximum = ^uint64(0)
	if right > maximum-left {
		return maximum, true
	}
	return left + right, false
}
