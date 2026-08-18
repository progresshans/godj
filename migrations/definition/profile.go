package definition

import "github.com/progresshans/godj/migrations/internal/definitionhandoff"

type definitionProfile uint8

const (
	legacyDefinitionProfile definitionProfile = iota + 1
	relationDefinitionProfile
)

var legacyCompatibility = compatibilityTuple{
	definitionFormat: DefinitionFormatVersion,
	loaderABI:        LoaderABIVersion,
	operationCodec:   OperationCodecVersion,
	schemaIR:         SchemaIRVersion,
}

var relationCompatibility = compatibilityTuple{
	definitionFormat: DefinitionFormatVersion,
	loaderABI:        RelationLoaderABIVersion,
	operationCodec:   RelationOperationCodecVersion,
	schemaIR:         RelationSchemaIRVersion,
}

func dispatchProfile(value compatibilityTuple) (definitionProfile, bool) {
	switch value {
	case legacyCompatibility:
		return legacyDefinitionProfile, true
	case relationCompatibility:
		return relationDefinitionProfile, true
	default:
		return 0, false
	}
}

func unsupportedProfileCoordinate(value compatibilityTuple) string {
	coordinates := []struct {
		name     string
		actual   int64
		legacy   int64
		relation int64
	}{
		{name: "definition_format", actual: value.definitionFormat, legacy: legacyCompatibility.definitionFormat, relation: relationCompatibility.definitionFormat},
		{name: "loader_abi", actual: value.loaderABI, legacy: legacyCompatibility.loaderABI, relation: relationCompatibility.loaderABI},
		{name: "operation_codec", actual: value.operationCodec, legacy: legacyCompatibility.operationCodec, relation: relationCompatibility.operationCodec},
		{name: "schema_ir", actual: value.schemaIR, legacy: legacyCompatibility.schemaIR, relation: relationCompatibility.schemaIR},
	}
	for _, coordinate := range coordinates {
		if coordinate.actual != coordinate.legacy && coordinate.actual != coordinate.relation {
			return coordinate.name
		}
	}
	for _, coordinate := range coordinates {
		if coordinate.actual != coordinate.legacy {
			return coordinate.name
		}
	}
	return "definition_format"
}

func handoffCompatibility(value compatibilityTuple) definitionhandoff.Compatibility {
	return definitionhandoff.Compatibility{
		DefinitionFormat: value.definitionFormat,
		LoaderABI:        value.loaderABI,
		OperationCodec:   value.operationCodec,
		SchemaIR:         value.schemaIR,
	}
}
