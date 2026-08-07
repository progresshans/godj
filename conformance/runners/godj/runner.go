// Package godj executes the M1 GoDj walking skeleton against the locked
// Django compatibility contracts. The suite embeds the locked Django profile
// as its comparison target; it does not claim that the Go process itself is a
// Django or CPython runtime.
package godj

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

// Generate executes every manifest scenario in manifest order. Query
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

func runScenario(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	switch contract.Scenario {
	case "django.query.exact":
		return queryExact(ctx, contract.ID)
	case "django.query.ascii_icontains":
		return queryASCIIInsensitiveContains(ctx, contract.ID)
	case "django.query.chained_and":
		return queryChainedAnd(ctx, contract.ID)
	case "django.query.chain_preserves_source":
		return queryChainPreservesSource(ctx, contract.ID)
	case "django.query.order_and_limit":
		return queryOrderAndLimit(ctx, contract.ID)
	case "django.query.empty_result":
		return queryEmptyResult(ctx, contract.ID)
	case "django.query.isnull":
		return queryIsNull(ctx, contract.ID)
	case "django.query.unknown_field":
		return queryUnknownField(ctx, contract.ID)
	case "django.query.construction_has_no_io":
		return queryConstructionHasNoIO(ctx, contract.ID)
	case "django.query.unsupported_lookup":
		return queryUnsupportedLookup(ctx, contract.ID)
	case "django.schema.model_metadata":
		return schemaModelMetadata(contract.ID)
	default:
		return protocol.Observation{}, fmt.Errorf("unsupported scenario %q", contract.Scenario)
	}
}
