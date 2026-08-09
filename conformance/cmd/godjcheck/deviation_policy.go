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
