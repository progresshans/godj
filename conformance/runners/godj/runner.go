// Package godj executes GoDj product slices against the locked Django
// compatibility contracts. The suite embeds the locked Django profile as its
// comparison target; it does not claim that the Go process itself is a Django
// or CPython runtime.
package godj

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

// Generate executes every manifest scenario in manifest order. Stateful
// scenarios provision an independent SQLite database so one observation
// cannot inherit state from another.
func Generate(ctx context.Context, profile protocol.Profile, manifest protocol.Manifest) (protocol.ObservationSuite, error) {
	if ctx == nil {
		return protocol.ObservationSuite{}, fmt.Errorf("generate GoDj observations: context is nil")
	}
	if err := profile.Validate(); err != nil {
		return protocol.ObservationSuite{}, fmt.Errorf("generate GoDj observations: profile: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return protocol.ObservationSuite{}, fmt.Errorf("generate GoDj observations: manifest: %w", err)
	}
	if manifest.ProfileID != profile.ID {
		return protocol.ObservationSuite{}, fmt.Errorf(
			"generate GoDj observations: manifest profile_id %q does not match profile id %q",
			manifest.ProfileID,
			profile.ID,
		)
	}
	if _, err := RequiredObservedContractIDs(manifest); err != nil {
		return protocol.ObservationSuite{}, fmt.Errorf("generate GoDj observations: handler registry: %w", err)
	}

	suite := protocol.ObservationSuite{
		FormatVersion: protocol.FormatVersion,
		Profile:       profile.Snapshot(),
		Contracts:     make([]protocol.Observation, 0, len(manifest.Contracts)),
	}
	for _, contract := range manifest.Contracts {
		if err := ctx.Err(); err != nil {
			return protocol.ObservationSuite{}, fmt.Errorf("generate GoDj observations before %s: %w", contract.ID, err)
		}
		observation, err := runScenario(ctx, contract)
		if err != nil {
			return protocol.ObservationSuite{}, fmt.Errorf("generate GoDj observation %s: %w", contract.ID, err)
		}
		suite.Contracts = append(suite.Contracts, observation)
	}
	if err := protocol.ValidateSuiteAgainst(profile, manifest, suite); err != nil {
		return protocol.ObservationSuite{}, fmt.Errorf("validate generated GoDj observations: %w", err)
	}
	return suite, nil
}

// RequiredObservedContractIDs returns the manifest-ordered contracts backed by
// the actual GoDj handler registry. A registered handler must have a passing or
// deviation status, while an unregistered contract must remain oracle_locked.
// This keeps product coverage independent from expected oracle payloads.
func RequiredObservedContractIDs(manifest protocol.Manifest) ([]string, error) {
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	required := make([]string, 0, len(manifest.Contracts))
	for _, contract := range manifest.Contracts {
		_, registered := lookupScenarioHandler(contract.Scenario)
		if registered {
			switch contract.Status {
			case protocol.ContractPassing, protocol.ContractDeviation:
				required = append(required, contract.ID)
			default:
				return nil, fmt.Errorf("registered scenario %q contract %s has status %q; want passing or deviation", contract.Scenario, contract.ID, contract.Status)
			}
			continue
		}
		if contract.Status != protocol.ContractOracleLocked {
			return nil, fmt.Errorf("unregistered scenario %q contract %s has status %q; want oracle_locked", contract.Scenario, contract.ID, contract.Status)
		}
	}
	return required, nil
}

type scenarioHandler func(context.Context, protocol.Contract) (protocol.Observation, error)

func runScenario(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	handler, ok := lookupScenarioHandler(contract.Scenario)
	if !ok {
		return protocol.Observation{
			ID:     contract.ID,
			Status: protocol.StatusNotImplemented,
			Phase:  contract.Phase,
		}, nil
	}
	return handler(ctx, contract)
}

func lookupScenarioHandler(scenario string) (scenarioHandler, bool) {
	if handler, ok := templateFormScenarioHandler(scenario); ok {
		return handler, true
	}
	if handler, ok := authSessionScenarioHandler(scenario); ok {
		return handler, true
	}
	if handler, ok := articleAdminScenarioHandler(scenario); ok {
		return handler, true
	}
	if handler, ok := systemStateScenarioHandler(scenario); ok {
		return handler, true
	}
	if handler, ok := parameterRoutingScenarioHandler(scenario); ok {
		return handler, true
	}
	if handler, ok := articleAPIScenarioHandler(scenario); ok {
		return handler, true
	}
	if handler, ok := queryExpressionScenarioHandler(scenario); ok {
		return handler, true
	}
	if handler, ok := queryBreadthScenarioHandler(scenario); ok {
		return handler, true
	}
	if handler, ok := relationScenarioHandler(scenario); ok {
		return handler, true
	}
	if _, ok := migrationProjectCheckFixtures[scenario]; ok {
		return migrationProjectCheckScenario, true
	}
	if _, ok := migrationDefinitionSourceFixtures[scenario]; ok {
		return migrationDefinitionSourceScenario, true
	}
	if _, ok := migrationLifecycleFixtures[scenario]; ok {
		return migrationLifecycleScenario, true
	}
	if _, ok := migrationStateReconstructionFixtures[scenario]; ok {
		return migrationStateReconstructionScenario, true
	}
	if _, ok := migrationRestartFixtures[scenario]; ok {
		return migrationRestartScenario, true
	}
	if _, ok := migrationExecutionFixtures[scenario]; ok {
		return migrationExecutionScenario, true
	}
	if _, ok := migrationPlanningFixtures[scenario]; ok {
		return migrationPlanningScenario, true
	}
	switch scenario {
	case "django.query.exact":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryExact(ctx, contract.ID)
		}, true
	case "django.query.ascii_icontains":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryASCIIInsensitiveContains(ctx, contract.ID)
		}, true
	case "django.query.chained_and":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryChainedAnd(ctx, contract.ID)
		}, true
	case "django.query.chain_preserves_source":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryChainPreservesSource(ctx, contract.ID)
		}, true
	case "django.query.order_and_limit":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryOrderAndLimit(ctx, contract.ID)
		}, true
	case "django.query.empty_result":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryEmptyResult(ctx, contract.ID)
		}, true
	case "django.query.isnull":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryIsNull(ctx, contract.ID)
		}, true
	case "django.query.unknown_field":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryUnknownField(ctx, contract.ID)
		}, true
	case "django.query.construction_has_no_io":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryConstructionHasNoIO(ctx, contract.ID)
		}, true
	case "django.query.unsupported_lookup":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryUnsupportedLookup(ctx, contract.ID)
		}, true
	case "django.schema.model_metadata":
		return func(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return schemaModelMetadata(contract.ID)
		}, true
	case "django.model.create_auto_pk":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelCreateAutoPrimaryKey(ctx, contract.ID)
		}, true
	case "django.model.create_nullable_variants":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelCreateNullableVariants(ctx, contract.ID)
		}, true
	case "django.model.partial_update_omits_changed_field":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelPartialUpdateOmitted(ctx, contract.ID)
		}, true
	case "django.model.partial_update_explicit_null":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelPartialUpdateExplicitNull(ctx, contract.ID)
		}, true
	case "django.model.instance_delete":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelInstanceDelete(ctx, contract.ID)
		}, true
	case "django.transaction.atomic_commit":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return transactionAtomicCommit(ctx, contract.ID)
		}, true
	case "django.transaction.atomic_rollback":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return transactionAtomicRollback(ctx, contract.ID)
		}, true
	case "django.migration.create_model":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return migrationCreateModel(ctx, contract.ID)
		}, true
	case "django.migration.add_nullable_field":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return migrationAddNullableField(ctx, contract.ID)
		}, true
	case "django.migration.reverse_nullable_field":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return migrationReverseNullableField(ctx, contract.ID)
		}, true
	case "django.migration.atomic_failure":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return migrationAtomicFailure(ctx, contract.ID)
		}, true
	case "django.model.save.new_auto_pk":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveNewAutoPrimaryKey(ctx, contract.ID)
		}, true
	case "django.model.save.loaded_all_fields":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveLoadedAllFields(ctx, contract.ID)
		}, true
	case "django.model.save.update_fields_named":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveUpdateFieldsNamed(ctx, contract.ID)
		}, true
	case "django.model.save.update_fields_empty":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveUpdateFieldsEmpty(ctx, contract.ID)
		}, true
	case "django.model.save.update_fields_primary_key":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveUpdateFieldsPrimaryKey(ctx, contract.ID)
		}, true
	case "django.model.save.force_insert_conflict":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveForceInsertConflict(ctx, contract.ID)
		}, true
	case "django.model.save.force_update_without_pk":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveForceUpdateWithoutPrimaryKey(ctx, contract.ID)
		}, true
	case "django.model.save.force_update_missing_row":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveForceUpdateMissingRow(ctx, contract.ID)
		}, true
	case "django.model.save.mutually_exclusive_force_flags":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveMutuallyExclusiveForceFlags(ctx, contract.ID)
		}, true
	case "django.model.save.explicit_pk_existing":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveExplicitPrimaryKeyExisting(ctx, contract.ID)
		}, true
	case "django.model.save.explicit_pk_missing":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveExplicitPrimaryKeyMissing(ctx, contract.ID)
		}, true
	case "django.model.save.atomic_rollback_instance_state":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return modelSaveAtomicRollbackInstanceState(ctx, contract.ID)
		}, true
	case "django.query.cache.repeated_full_evaluation":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheRepeatedFullEvaluation(ctx, contract.ID)
		}, true
	case "django.query.cache.empty_full_evaluation":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheEmptyFullEvaluation(ctx, contract.ID)
		}, true
	case "django.query.cache.stale_snapshot_and_fresh_queryset":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheStaleSnapshotAndFreshQuerySet(ctx, contract.ID)
		}, true
	case "django.query.cache.chained_queryset_independence":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheChainedQuerySetIndependence(ctx, contract.ID)
		}, true
	case "django.query.cache.count_cold_and_warm":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheCountColdAndWarm(ctx, contract.ID)
		}, true
	case "django.query.cache.exists_cold_and_warm":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheExistsColdAndWarm(ctx, contract.ID)
		}, true
	case "django.query.cache.iterator_bypass":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheIteratorBypass(ctx, contract.ID)
		}, true
	case "django.query.cache.index_partial_evaluation":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheIndexPartialEvaluation(ctx, contract.ID)
		}, true
	case "django.query.cache.failed_evaluation_retry":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheFailedEvaluationRetry(ctx, contract.ID)
		}, true
	case "django.query.cache.all_fresh_clone":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheFreshClone(ctx, contract.ID)
		}, true
	case "django.query.cache.first_cold_and_warm":
		return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
			return queryCacheFirstColdAndWarm(ctx, contract.ID)
		}, true
	default:
		return nil, false
	}
}
