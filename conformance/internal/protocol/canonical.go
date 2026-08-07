package protocol

import (
	"encoding/json"
	"fmt"
)

// MarshalCanonical returns compact UTF-8 JSON with one trailing newline.
// Objects use structs or sorted NamedValue slices, while contract and list
// arrays retain their meaningful input order.
func MarshalCanonical(value any) ([]byte, error) {
	if err := validateCanonicalInput(value); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func validateCanonicalInput(value any) error {
	switch typed := value.(type) {
	case Profile:
		return typed.Validate()
	case *Profile:
		if typed == nil {
			return fmt.Errorf("cannot marshal nil profile")
		}
		return typed.Validate()
	case Manifest:
		return typed.Validate()
	case *Manifest:
		if typed == nil {
			return fmt.Errorf("cannot marshal nil manifest")
		}
		return typed.Validate()
	case ObservationSuite:
		return typed.Validate()
	case *ObservationSuite:
		if typed == nil {
			return fmt.Errorf("cannot marshal nil observation suite")
		}
		return typed.Validate()
	case DeviationExpectation:
		return typed.Validate()
	case *DeviationExpectation:
		if typed == nil {
			return fmt.Errorf("cannot marshal nil deviation expectation")
		}
		return typed.Validate()
	case Value:
		return typed.Validate()
	case *Value:
		if typed == nil {
			return fmt.Errorf("cannot marshal nil value")
		}
		return typed.Validate()
	default:
		return fmt.Errorf("unsupported canonical JSON type %T", value)
	}
}
