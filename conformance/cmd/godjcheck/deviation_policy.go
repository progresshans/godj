package main

import (
	"fmt"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func deviationPolicyForDecision(decision string) (protocol.DeviationPolicy, error) {
	switch decision {
	case "DEV-0001":
		return migrationExecutionDeviationPolicy(), nil
	case "DEV-0002":
		return migrationLifecycleDeviationPolicy(), nil
	case "DEV-0003":
		return templateFormDeviationPolicy(), nil
	case "DEV-0004":
		return authSessionDeviationPolicy(), nil
	case "DEV-0005":
		return articleAdminDeviationPolicy(), nil
	default:
		return protocol.DeviationPolicy{}, fmt.Errorf("unsupported deviation decision %q", decision)
	}
}

func migrationExecutionDeviationPolicy() protocol.DeviationPolicy {
	replace := protocol.DeviationReplace
	metrics := protocol.DeviationMetrics
	return protocol.DeviationPolicy{
		Decision: "DEV-0001",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "MIG-018",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: metrics, Path: "steps[0].transaction_model", Operation: replace},
					{Dimension: metrics, Path: "steps[1].transaction_model", Operation: replace},
					{Dimension: metrics, Path: "steps[2].transaction_model", Operation: replace},
				},
			},
			{
				ID: "MIG-020",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: metrics, Path: "steps[0].transaction_model", Operation: replace},
					{Dimension: metrics, Path: "steps[1].transaction_model", Operation: replace},
				},
			},
			{
				ID: "MIG-022",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: metrics, Path: "steps[0].transaction_model", Operation: replace},
					{Dimension: metrics, Path: "steps[1].transaction_model", Operation: replace},
				},
			},
			{
				ID: "MIG-024",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationPhase, Path: "", Operation: replace},
					{Dimension: protocol.DeviationDBState, Path: "after.managed_schema[0].columns[1]", Operation: protocol.DeviationInsertBefore},
					{Dimension: metrics, Path: "steps[0].transaction_model", Operation: replace},
					{Dimension: metrics, Path: "steps[1].schema_outcome", Operation: replace},
					{Dimension: metrics, Path: "steps[1].status", Operation: replace},
					{Dimension: metrics, Path: "steps[1].transaction_model", Operation: replace},
				},
			},
		},
	}
}

func migrationLifecycleDeviationPolicy() protocol.DeviationPolicy {
	replace := protocol.DeviationReplace
	return protocol.DeviationPolicy{
		Decision: "DEV-0002",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "MIG-052",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "plan[0]", Operation: replace},
					{Dimension: protocol.DeviationResult, Path: "plan[1]", Operation: replace},
					{Dimension: protocol.DeviationResult, Path: "plan[2]", Operation: replace},
					{Dimension: protocol.DeviationMetrics, Path: "steps[0]", Operation: replace},
					{Dimension: protocol.DeviationMetrics, Path: "steps[1]", Operation: replace},
					{Dimension: protocol.DeviationMetrics, Path: "steps[2]", Operation: replace},
				},
			},
		},
	}
}

func templateFormDeviationPolicy() protocol.DeviationPolicy {
	return protocol.DeviationPolicy{
		Decision: "DEV-0003",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "WEB-022",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "attribute_fallback_shadowed", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationMetrics, Path: "object_dictionary_lookups", Operation: protocol.DeviationReplace},
				},
			},
			{
				ID: "WEB-027",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "auto_called", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "rendered_return_category", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationMetrics, Path: "callable_invocations", Operation: protocol.DeviationReplace},
				},
			},
		},
	}
}

func authSessionDeviationPolicy() protocol.DeviationPolicy {
	return protocol.DeviationPolicy{
		Decision: "DEV-0004",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "AUT-004",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "redirect", Operation: protocol.DeviationReplace},
				},
			},
			{
				ID: "AUT-005",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "delete.http_only", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "login.expires_present", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "login.max_age", Operation: protocol.DeviationReplace},
				},
			},
		},
	}
}

func articleAdminDeviationPolicy() protocol.DeviationPolicy {
	return protocol.DeviationPolicy{
		Decision: "DEV-0005",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "ADM-002",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "actions", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationMetrics, Path: "registered_models", Operation: protocol.DeviationReplace},
				},
			},
		},
	}
}
