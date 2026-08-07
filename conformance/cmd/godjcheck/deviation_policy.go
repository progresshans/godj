package main

import "github.com/progresshans/godj/conformance/internal/protocol"

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
