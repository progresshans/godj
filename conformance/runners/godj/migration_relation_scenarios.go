package godj

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/conformance/migrationrelationproduct"
)

type migrationRelationCharacterizationCase struct {
	phase      protocol.Phase
	comparison []protocol.ComparisonDimension
	product    migrationrelationproduct.Case
}

var migrationRelationCharacterizationCases = map[string]migrationRelationCharacterizationCase{
	"godj.migration.relation.current_abi": {
		phase: protocol.PhaseConstruction, comparison: migrationRelationResultMetrics(),
		product: migrationrelationproduct.CaseCurrentABI,
	},
	"godj.migration.relation.current_format_validation": {
		phase: protocol.PhaseEnvironment, comparison: migrationRelationResultMetrics(),
		product: migrationrelationproduct.CaseCurrentFormat,
	},
	"godj.migration.relation.current_digest": {
		phase: protocol.PhaseConstruction, comparison: migrationRelationResultMetrics(),
		product: migrationrelationproduct.CaseCurrentDigest,
	},
	"godj.migration.relation.current_state": {
		phase: protocol.PhaseConstruction, comparison: migrationRelationResultMetrics(),
		product: migrationrelationproduct.CaseCurrentState,
	},
	"godj.migration.relation.structural_preflight": {
		phase: protocol.PhaseEvaluation, comparison: migrationRelationResultMetrics(),
		product: migrationrelationproduct.CaseStructuralPreflight,
	},
	"django.migration.relation.create_lifecycle": {
		phase: protocol.PhaseCommit, comparison: migrationRelationResultDatabaseMetrics(),
		product: migrationrelationproduct.CaseCreateLifecycle,
	},
	"django.migration.relation.add_nullable_populated": {
		phase: protocol.PhaseCommit, comparison: migrationRelationResultDatabaseMetrics(),
		product: migrationrelationproduct.CaseAddRelation,
	},
	"django.migration.relation.remove_remake": {
		phase: protocol.PhaseCommit, comparison: migrationRelationResultDatabaseMetrics(),
		product: migrationrelationproduct.CaseRemoveRemake,
	},
	"django.migration.relation.physical_fk_policy": {
		phase: protocol.PhaseCommit, comparison: migrationRelationResultDatabaseMetrics(),
		product: migrationrelationproduct.CasePhysicalFKPolicy,
	},
	"django.migration.relation.file_restart": {
		phase: protocol.PhaseCommit, comparison: migrationRelationResultDatabaseMetrics(),
		product: migrationrelationproduct.CaseFileRestart,
	},
	"django.migration.relation.precommit_faults": {
		phase: protocol.PhaseRollback, comparison: migrationRelationResultDatabaseMetrics(),
		product: migrationrelationproduct.CasePrecommitFaults,
	},
	"godj.migration.relation.commit_outcomes": {
		phase: protocol.PhaseCommit, comparison: migrationRelationResultMetrics(),
		product: migrationrelationproduct.CaseCommitOutcomes,
	},
}

func migrationRelationResultMetrics() []protocol.ComparisonDimension {
	return []protocol.ComparisonDimension{protocol.CompareResult, protocol.CompareMetrics}
}

func migrationRelationResultDatabaseMetrics() []protocol.ComparisonDimension {
	return []protocol.ComparisonDimension{protocol.CompareResult, protocol.CompareDBState, protocol.CompareMetrics}
}

// CharacterizeMigrationRelation executes the locked migration-relation
// contracts without registering them as product handlers. It validates only
// the profile, manifest, actual typed observations, and their declared
// dimensions; it does not load or compare a reference observation suite.
func CharacterizeMigrationRelation(
	ctx context.Context,
	profile protocol.Profile,
	manifest protocol.Manifest,
) (protocol.ObservationSuite, error) {
	if ctx == nil {
		return protocol.ObservationSuite{}, fmt.Errorf("characterize migration relation: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return protocol.ObservationSuite{}, err
	}
	if err := profile.Validate(); err != nil {
		return protocol.ObservationSuite{}, fmt.Errorf("characterize migration relation: profile: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return protocol.ObservationSuite{}, fmt.Errorf("characterize migration relation: manifest: %w", err)
	}
	if manifest.ProfileID != profile.ID {
		return protocol.ObservationSuite{}, fmt.Errorf(
			"characterize migration relation: manifest profile %q does not match %q",
			manifest.ProfileID,
			profile.ID,
		)
	}
	if err := validateMigrationRelationCharacterizationCases(); err != nil {
		return protocol.ObservationSuite{}, err
	}
	if len(manifest.Contracts) != len(migrationRelationCharacterizationCases) {
		return protocol.ObservationSuite{}, fmt.Errorf(
			"characterize migration relation: manifest has %d contracts, want exact %d",
			len(manifest.Contracts),
			len(migrationRelationCharacterizationCases),
		)
	}

	suite := protocol.ObservationSuite{
		FormatVersion: protocol.FormatVersion,
		Profile:       profile.Snapshot(),
		Contracts:     make([]protocol.Observation, 0, len(manifest.Contracts)),
	}
	seen := make(map[string]bool, len(manifest.Contracts))
	for _, contract := range manifest.Contracts {
		if err := ctx.Err(); err != nil {
			return protocol.ObservationSuite{}, err
		}
		characterization, ok := migrationRelationCharacterizationCases[contract.Scenario]
		if !ok {
			return protocol.ObservationSuite{}, fmt.Errorf(
				"characterize migration relation: unsupported locked scenario %q",
				contract.Scenario,
			)
		}
		if seen[contract.Scenario] {
			return protocol.ObservationSuite{}, fmt.Errorf(
				"characterize migration relation: duplicate scenario %q",
				contract.Scenario,
			)
		}
		seen[contract.Scenario] = true
		if contract.Status != protocol.ContractOracleLocked {
			return protocol.ObservationSuite{}, fmt.Errorf(
				"characterize migration relation: contract %s status %q, want oracle_locked",
				contract.ID,
				contract.Status,
			)
		}
		if _, registered := lookupScenarioHandler(contract.Scenario); registered {
			return protocol.ObservationSuite{}, fmt.Errorf(
				"characterize migration relation: locked scenario %q is registered",
				contract.Scenario,
			)
		}
		if contract.Phase != characterization.phase || !reflect.DeepEqual(contract.Comparison, characterization.comparison) {
			return protocol.ObservationSuite{}, fmt.Errorf(
				"characterize migration relation: scenario %q dimensions changed",
				contract.Scenario,
			)
		}
		observation, err := migrationRelationCharacterizationObservation(ctx, contract, characterization)
		if err != nil {
			return protocol.ObservationSuite{}, fmt.Errorf("characterize migration relation %s: %w", contract.ID, err)
		}
		suite.Contracts = append(suite.Contracts, observation)
	}
	if len(seen) != len(migrationRelationCharacterizationCases) {
		return protocol.ObservationSuite{}, fmt.Errorf("characterize migration relation: incomplete scenario coverage")
	}
	if err := protocol.ValidateSuiteAgainst(profile, manifest, suite); err != nil {
		return protocol.ObservationSuite{}, fmt.Errorf("validate migration relation characterization: %w", err)
	}
	return suite, nil
}

func validateMigrationRelationCharacterizationCases() error {
	productCases := migrationrelationproduct.Cases()
	if len(migrationRelationCharacterizationCases) != len(productCases) {
		return fmt.Errorf(
			"characterize migration relation: scenario/product inventory = %d/%d",
			len(migrationRelationCharacterizationCases),
			len(productCases),
		)
	}
	known := make(map[migrationrelationproduct.Case]bool, len(productCases))
	for _, productCase := range productCases {
		if known[productCase] {
			return fmt.Errorf("characterize migration relation: duplicate product case %q", productCase)
		}
		known[productCase] = true
	}
	seen := make(map[migrationrelationproduct.Case]string, len(productCases))
	for scenario, characterization := range migrationRelationCharacterizationCases {
		if !known[characterization.product] {
			return fmt.Errorf(
				"characterize migration relation: scenario %q uses unknown product case %q",
				scenario,
				characterization.product,
			)
		}
		if previous, duplicate := seen[characterization.product]; duplicate {
			return fmt.Errorf(
				"characterize migration relation: scenarios %q and %q share product case %q",
				previous,
				scenario,
				characterization.product,
			)
		}
		seen[characterization.product] = scenario
	}
	for _, productCase := range productCases {
		if _, exists := seen[productCase]; !exists {
			return fmt.Errorf("characterize migration relation: product case %q is unmapped", productCase)
		}
	}
	return nil
}

type migrationRelationResult struct {
	Case     migrationrelationproduct.Case          `json:"case"`
	Outcomes []migrationrelationproduct.OutcomeFact `json:"outcomes"`
}

func migrationRelationCharacterizationObservation(
	ctx context.Context,
	contract protocol.Contract,
	characterization migrationRelationCharacterizationCase,
) (protocol.Observation, error) {
	actual, err := migrationrelationproduct.Observe(ctx, characterization.product)
	if err != nil {
		return protocol.Observation{}, err
	}
	result, err := migrationRelationProtocolValue(migrationRelationResult{Case: actual.Case, Outcomes: actual.Outcomes})
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("encode result facts: %w", err)
	}
	metrics, err := migrationRelationProtocolValue(actual.Metrics)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("encode metric facts: %w", err)
	}
	observation := protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  valuePointer(result),
		Metrics: valuePointer(metrics),
	}
	if actual.Database != nil {
		database, err := migrationRelationProtocolValue(*actual.Database)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("encode database facts: %w", err)
		}
		observation.DBState = valuePointer(database)
	}
	return observation, nil
}

func migrationRelationProtocolValue(value any) (protocol.Value, error) {
	return migrationRelationReflectValue(reflect.ValueOf(value))
}

func migrationRelationReflectValue(value reflect.Value) (protocol.Value, error) {
	if !value.IsValid() {
		return protocol.Null(), nil
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return protocol.Null(), nil
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Bool:
		return protocol.Boolean(value.Bool()), nil
	case reflect.String:
		return protocol.String(value.String()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return protocol.Integer(strconv.FormatInt(value.Int(), 10)), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return protocol.Integer(strconv.FormatUint(value.Uint(), 10)), nil
	case reflect.Slice, reflect.Array:
		items := make([]protocol.Value, value.Len())
		for index := 0; index < value.Len(); index++ {
			item, err := migrationRelationReflectValue(value.Index(index))
			if err != nil {
				return protocol.Value{}, fmt.Errorf("list item %d: %w", index, err)
			}
			items[index] = item
		}
		return protocol.List(items...), nil
	case reflect.Struct:
		fields := make(map[string]protocol.Value)
		valueType := value.Type()
		for index := 0; index < value.NumField(); index++ {
			fieldType := valueType.Field(index)
			if fieldType.PkgPath != "" {
				continue
			}
			name := strings.Split(fieldType.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = migrationRelationSnakeCase(fieldType.Name)
			}
			field, err := migrationRelationReflectValue(value.Field(index))
			if err != nil {
				return protocol.Value{}, fmt.Errorf("field %s: %w", name, err)
			}
			fields[name] = field
		}
		return protocol.Object(fields), nil
	default:
		return protocol.Value{}, fmt.Errorf("unsupported typed fact %s", value.Kind())
	}
}

func migrationRelationSnakeCase(value string) string {
	var result strings.Builder
	for index, character := range value {
		if character >= 'A' && character <= 'Z' {
			if index > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(character + ('a' - 'A'))
			continue
		}
		result.WriteRune(character)
	}
	return result.String()
}
