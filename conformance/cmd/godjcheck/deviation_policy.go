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
	case "DEV-0006":
		return parameterRoutingDeviationPolicy(), nil
	case "DEV-0007":
		return articleAPIDeviationPolicy(), nil
	case "DEV-0008":
		return systemStateDeviationPolicy(), nil
	case "DEV-0009":
		return apiAuthenticationDeviationPolicy(), nil
	case "DEV-0010":
		return migrationWriterDeviationPolicy(), nil
	default:
		return protocol.DeviationPolicy{}, fmt.Errorf("unsupported deviation decision %q", decision)
	}
}

// deviationPolicyForProduct keeps decision-only dispatch stable while
// disambiguating decisions that intentionally own more than one product
// manifest. DEV-0002 is reused for two independently reviewed app-zero
// surfaces, so accepting either sparse policy without the exact manifest
// contract set would let one fixture authorize selectors owned by the other.
func deviationPolicyForProduct(decision string, manifest protocol.Manifest) (protocol.DeviationPolicy, error) {
	if decision != "DEV-0002" {
		return deviationPolicyForDecision(decision)
	}

	hasLifecycle := manifestHasContractID(manifest, "MIG-052")
	hasTargetPlan := manifestHasContractID(manifest, "MIG-122")
	switch {
	case hasLifecycle && hasTargetPlan:
		return protocol.DeviationPolicy{}, fmt.Errorf("DEV-0002 manifest is ambiguous: contains both MIG-052 and MIG-122")
	case !hasLifecycle && !hasTargetPlan:
		return protocol.DeviationPolicy{}, fmt.Errorf("DEV-0002 manifest contains neither MIG-052 nor MIG-122")
	case hasLifecycle:
		if !manifestHasExactContractSet(manifest, migrationLifecycleDEV0002ContractIDs()) {
			return protocol.DeviationPolicy{}, fmt.Errorf("DEV-0002 manifest does not match the exact MIG-047..MIG-056 contract set")
		}
		return migrationLifecycleDeviationPolicy(), nil
	default:
		if !manifestHasExactContractSet(manifest, migrationTargetPlanDEV0002ContractIDs()) {
			return protocol.DeviationPolicy{}, fmt.Errorf("DEV-0002 manifest does not match the exact MIG-119..MIG-128 contract set")
		}
		return migrationTargetPlanDeviationPolicy(), nil
	}
}

func migrationLifecycleDEV0002ContractIDs() []string {
	return []string{
		"MIG-047", "MIG-048", "MIG-049", "MIG-050", "MIG-051",
		"MIG-052", "MIG-053", "MIG-054", "MIG-055", "MIG-056",
	}
}

func migrationTargetPlanDEV0002ContractIDs() []string {
	return []string{
		"MIG-119", "MIG-120", "MIG-121", "MIG-122", "MIG-123",
		"MIG-124", "MIG-125", "MIG-126", "MIG-127", "MIG-128",
	}
}

func manifestHasContractID(manifest protocol.Manifest, id string) bool {
	for _, contract := range manifest.Contracts {
		if contract.ID == id {
			return true
		}
	}
	return false
}

func manifestHasExactContractSet(manifest protocol.Manifest, ids []string) bool {
	if len(manifest.Contracts) != len(ids) {
		return false
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	for _, contract := range manifest.Contracts {
		if _, exists := want[contract.ID]; !exists {
			return false
		}
		delete(want, contract.ID)
	}
	return len(want) == 0
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

func migrationTargetPlanDeviationPolicy() protocol.DeviationPolicy {
	replace := protocol.DeviationReplace
	return protocol.DeviationPolicy{
		Decision: "DEV-0002",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "MIG-122",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "plan[0]", Operation: replace},
					{Dimension: protocol.DeviationResult, Path: "plan[1]", Operation: replace},
					{Dimension: protocol.DeviationResult, Path: "plan[2]", Operation: replace},
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

func parameterRoutingDeviationPolicy() protocol.DeviationPolicy {
	return protocol.DeviationPolicy{
		Decision: "DEV-0006",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "WEB-028",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "parameter.pk_type", Operation: protocol.DeviationReplace},
				},
			},
			{
				ID: "WEB-029",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "invalid[0].matched", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "invalid[1].matched", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "invalid[2].matched", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "invalid[3].matched", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "valid[0].type", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "valid[1].type", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "valid[2].type", Operation: protocol.DeviationReplace},
				},
			},
		},
	}
}

func articleAPIDeviationPolicy() protocol.DeviationPolicy {
	return protocol.DeviationPolicy{
		Decision: "DEV-0007",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "API-001",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "[10].response.error_codes.detail", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "[10].response.status", Operation: protocol.DeviationReplace},
				},
			},
			{
				ID: "API-003",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "unsafe_attempts[0].response.error_codes.detail", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "unsafe_attempts[1].response.error_codes.detail", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "unsafe_attempts[2].response.error_codes.detail", Operation: protocol.DeviationReplace},
				},
			},
			{
				ID: "API-010",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "missing_csrf.error_codes.detail", Operation: protocol.DeviationReplace},
				},
			},
		},
	}
}

func systemStateDeviationPolicy() protocol.DeviationPolicy {
	return protocol.DeviationPolicy{
		Decision: "DEV-0008",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "SYS-009",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "pre_restart.accepted", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "pre_restart.status", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationDBState, Path: "pre_restart.article_delta", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationMetrics, Path: "pre_restart_mutations", Operation: protocol.DeviationReplace},
				},
			},
		},
	}
}

func apiAuthenticationDeviationPolicy() protocol.DeviationPolicy {
	return protocol.DeviationPolicy{
		Decision: "DEV-0009",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "AUT-012",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "[0].response.error_codes.detail", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "[0].response.www_authenticate", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "[1].response.error_codes.detail", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "[1].response.www_authenticate", Operation: protocol.DeviationReplace},
				},
			},
			{
				ID: "AUT-013",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "www_authenticate", Operation: protocol.DeviationReplace},
				},
			},
			{
				ID: "AUT-015",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "[1].response.error_codes.detail", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "[1].response.www_authenticate", Operation: protocol.DeviationReplace},
				},
			},
		},
	}
}

func migrationWriterDeviationPolicy() protocol.DeviationPolicy {
	replace := protocol.DeviationReplace
	result := protocol.DeviationResult
	return protocol.DeviationPolicy{
		Decision: "DEV-0010",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "MIG-103",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: result, Path: "cases[0].migrations[0].operations[1].fields[3].on_delete", Operation: replace},
					{Dimension: result, Path: "cases[1].migrations[1].operations[0].fields[3].on_delete", Operation: replace},
				},
			},
			{
				ID: "MIG-104",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: result, Path: "migrations[0].name", Operation: replace},
				},
			},
			{
				ID: "MIG-105",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: result, Path: "files_before", Operation: replace},
					{Dimension: result, Path: "files_after", Operation: replace},
					{Dimension: result, Path: "output", Operation: replace},
				},
			},
			{
				ID: "MIG-106",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: result, Path: "cases[0].files_before", Operation: replace},
					{Dimension: result, Path: "cases[0].files_after", Operation: replace},
					{Dimension: result, Path: "cases[0].output", Operation: replace},
					{Dimension: result, Path: "cases[1].files_before", Operation: replace},
					{Dimension: result, Path: "cases[1].files_after", Operation: replace},
					{Dimension: result, Path: "cases[1].output", Operation: replace},
				},
			},
			{
				ID: "MIG-107",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: result, Path: "cases[0].code", Operation: replace},
					{Dimension: result, Path: "cases[1].code", Operation: replace},
					{Dimension: result, Path: "cases[2].code", Operation: replace},
					{Dimension: result, Path: "cases[3].code", Operation: replace},
					{Dimension: result, Path: "cases[4].code", Operation: replace},
					{Dimension: result, Path: "cases[5].code", Operation: replace},
					{Dimension: result, Path: "cases[6].code", Operation: replace},
				},
			},
		},
	}
}
