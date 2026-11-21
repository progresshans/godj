package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
)

type Difference struct {
	ContractID string `json:"contract_id"`
	Path       string `json:"path"`
	Expected   string `json:"expected"`
	Actual     string `json:"actual"`
	Message    string `json:"message"`
}

// Compare validates both suites against the locked profile and manifest, then
// reports every observable mismatch in deterministic manifest order.
func Compare(profile Profile, manifest Manifest, expected, actual ObservationSuite) ([]Difference, error) {
	if err := ValidateSuiteAgainst(profile, manifest, expected); err != nil {
		return nil, fmt.Errorf("expected suite: %w", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, actual); err != nil {
		return nil, fmt.Errorf("actual suite: %w", err)
	}

	differences := make([]Difference, 0)
	for index := range manifest.Contracts {
		contract := manifest.Contracts[index]
		want := expected.Contracts[index]
		got := actual.Contracts[index]

		if want.Status != StatusObserved {
			return nil, fmt.Errorf("expected suite contract %s must be observed, got %q", want.ID, want.Status)
		}
		if got.Status == StatusNotImplemented {
			differences = append(differences, difference(
				contract.ID,
				"status",
				string(StatusObserved),
				string(StatusNotImplemented),
				"actual contract is not implemented",
			))
			continue
		}
		differences = append(differences, compareObservedContract(contract, want, got)...)
	}
	return differences, nil
}

func compareObservedContract(contract Contract, expected, actual Observation) []Difference {
	differences := make([]Difference, 0)
	if actual.Status != expected.Status {
		differences = append(differences, difference(
			contract.ID,
			"status",
			string(expected.Status),
			string(actual.Status),
			"observation status differs",
		))
	}
	if actual.Phase != expected.Phase {
		differences = append(differences, difference(
			contract.ID,
			"phase",
			string(expected.Phase),
			string(actual.Phase),
			"observation phase differs",
		))
	}

	for _, dimension := range contract.Comparison {
		switch dimension {
		case CompareResult:
			differences = append(differences, compareValues(contract.ID, "result", expected.Result, actual.Result)...)
		case CompareError:
			differences = append(differences, compareErrors(contract.ID, expected.Error, actual.Error)...)
		case CompareDBState:
			differences = append(differences, compareValues(contract.ID, "db_state", expected.DBState, actual.DBState)...)
		case CompareMetrics:
			differences = append(differences, compareValues(contract.ID, "metrics", expected.Metrics, actual.Metrics)...)
		}
	}
	return differences
}

func compareErrors(contractID string, expected, actual *ObservedError) []Difference {
	if expected == nil || actual == nil {
		if expected == actual {
			return nil
		}
		return []Difference{difference(
			contractID,
			"error",
			formatJSON(expected),
			formatJSON(actual),
			"error presence differs",
		)}
	}
	differences := make([]Difference, 0, 3)
	if expected.Category != actual.Category {
		differences = append(differences, difference(
			contractID,
			"error.category",
			expected.Category,
			actual.Category,
			"error category differs",
		))
	}
	if expected.Code != actual.Code {
		differences = append(differences, difference(
			contractID,
			"error.code",
			expected.Code,
			actual.Code,
			"error code differs",
		))
	}
	if *expected.MessageIsContract && expected.Message != actual.Message {
		differences = append(differences, difference(
			contractID,
			"error.message",
			expected.Message,
			actual.Message,
			"contractual error message differs",
		))
	}
	return differences
}

func compareValues(contractID, root string, expected, actual *Value) []Difference {
	if expected == nil || actual == nil {
		if expected == actual {
			return nil
		}
		return []Difference{difference(
			contractID,
			root,
			formatJSON(expected),
			formatJSON(actual),
			"value presence differs",
		)}
	}
	return compareValue(contractID, root, *expected, *actual)
}

func compareValue(contractID, path string, expected, actual Value) []Difference {
	if expected.Type != actual.Type {
		return []Difference{difference(
			contractID,
			path+".type",
			string(expected.Type),
			string(actual.Type),
			"value type differs",
		)}
	}

	switch expected.Type {
	case ValueNull:
		return nil
	case ValueBool:
		if *expected.Bool != *actual.Bool {
			return []Difference{difference(contractID, path+".value", formatJSON(*expected.Bool), formatJSON(*actual.Bool), "boolean value differs")}
		}
	case ValueInt, ValueString, ValueDecimal, ValueDatetime, ValueUUID, ValueBytes:
		if *expected.Text != *actual.Text {
			return []Difference{difference(contractID, path+".value", *expected.Text, *actual.Text, "scalar value differs")}
		}
	case ValuePK:
		return compareValue(contractID, path+".value", *expected.Nested, *actual.Nested)
	case ValueList:
		differences := make([]Difference, 0)
		if len(expected.Items) != len(actual.Items) {
			differences = append(differences, difference(
				contractID,
				path+".items.length",
				fmt.Sprint(len(expected.Items)),
				fmt.Sprint(len(actual.Items)),
				"list length differs",
			))
		}
		length := min(len(expected.Items), len(actual.Items))
		for index := 0; index < length; index++ {
			differences = append(differences, compareValue(
				contractID,
				fmt.Sprintf("%s.items[%d]", path, index),
				expected.Items[index],
				actual.Items[index],
			)...)
		}
		return differences
	case ValueObject:
		differences := make([]Difference, 0)
		if len(expected.Fields) != len(actual.Fields) {
			differences = append(differences, difference(
				contractID,
				path+".fields.length",
				fmt.Sprint(len(expected.Fields)),
				fmt.Sprint(len(actual.Fields)),
				"object field count differs",
			))
		}
		length := min(len(expected.Fields), len(actual.Fields))
		for index := 0; index < length; index++ {
			wantField := expected.Fields[index]
			gotField := actual.Fields[index]
			fieldPath := fmt.Sprintf("%s.fields[%d]", path, index)
			if wantField.Name != gotField.Name {
				differences = append(differences, difference(
					contractID,
					fieldPath+".name",
					wantField.Name,
					gotField.Name,
					"object field name differs",
				))
				continue
			}
			differences = append(differences, compareValue(
				contractID,
				fieldPath+".value",
				wantField.Value,
				gotField.Value,
			)...)
		}
		return differences
	}
	return nil
}

func difference(contractID, path, expected, actual, message string) Difference {
	return Difference{
		ContractID: contractID,
		Path:       path,
		Expected:   expected,
		Actual:     actual,
		Message:    message,
	}
}

func formatJSON(value any) string {
	if value == nil || (reflect.ValueOf(value).Kind() == reflect.Ptr && reflect.ValueOf(value).IsNil()) {
		return "null"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<invalid: %v>", err)
	}
	return string(encoded)
}
