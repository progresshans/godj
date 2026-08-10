package protocol

import "fmt"

// CompareProduct validates a product suite that may deliberately leave
// oracle-locked contracts unimplemented. Passing and deviation contracts must
// be backed by an explicit, manifest-ordered requiredObserved list and must be
// fully observed. Oracle-locked contracts must remain payload-free
// not-implemented observations.
func CompareProduct(
	profile Profile,
	manifest Manifest,
	expected, actual ObservationSuite,
	requiredObserved []string,
) ([]Difference, error) {
	if err := ValidateSuiteAgainst(profile, manifest, expected); err != nil {
		return nil, fmt.Errorf("expected suite: %w", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, actual); err != nil {
		return nil, fmt.Errorf("actual suite: %w", err)
	}

	wantedRequired := make([]string, 0, len(manifest.Contracts))
	for _, contract := range manifest.Contracts {
		switch contract.Status {
		case ContractPassing, ContractDeviation:
			wantedRequired = append(wantedRequired, contract.ID)
		case ContractOracleLocked:
		case ContractDraft, ContractRed:
			return nil, fmt.Errorf("contract %s has product-ineligible status %q", contract.ID, contract.Status)
		default:
			return nil, fmt.Errorf("contract %s has unknown product status %q", contract.ID, contract.Status)
		}
	}
	if len(requiredObserved) != len(wantedRequired) {
		return nil, fmt.Errorf(
			"required observed contracts contain %d IDs; manifest requires %d",
			len(requiredObserved),
			len(wantedRequired),
		)
	}
	for index, want := range wantedRequired {
		if got := requiredObserved[index]; got != want {
			return nil, fmt.Errorf("required observed contract %d is %q; manifest requires %q in that position", index, got, want)
		}
	}

	differences := make([]Difference, 0)
	for index, contract := range manifest.Contracts {
		want := expected.Contracts[index]
		got := actual.Contracts[index]
		if want.Status != StatusObserved {
			return nil, fmt.Errorf("expected suite contract %s must be observed, got %q", want.ID, want.Status)
		}

		switch contract.Status {
		case ContractPassing, ContractDeviation:
			if got.Status != StatusObserved {
				return nil, fmt.Errorf("required product contract %s must be observed, got %q", contract.ID, got.Status)
			}
			differences = append(differences, compareObservedContract(contract, want, got)...)
		case ContractOracleLocked:
			if got.Status != StatusNotImplemented {
				return nil, fmt.Errorf("oracle-locked product contract %s must be not_implemented, got %q", contract.ID, got.Status)
			}
		}
	}
	return differences, nil
}
