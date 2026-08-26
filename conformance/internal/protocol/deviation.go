package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var (
	deviationPathSegmentPattern         = regexp.MustCompile(`^([a-z][a-z0-9_]*)(?:\[([0-9]+)\])?$`)
	deviationRootListPathSegmentPattern = regexp.MustCompile(`^\[([0-9]+)\]$`)
)

// PrepareDeviationExpectation validates the locked reference with the original
// manifest before deriving a product-only manifest and exact product expected
// suite. The checked-in reference manifest and oracle are never rewritten.
func PrepareDeviationExpectation(
	profile Profile,
	manifest Manifest,
	reference ObservationSuite,
	expectation DeviationExpectation,
	policy DeviationPolicy,
) (Manifest, ObservationSuite, error) {
	if _, err := Compare(profile, manifest, reference, reference); err != nil {
		return Manifest{}, ObservationSuite{}, fmt.Errorf("locked reference suite: %w", err)
	}
	if err := expectation.Validate(); err != nil {
		return Manifest{}, ObservationSuite{}, fmt.Errorf("deviation expectation: %w", err)
	}
	if expectation.ProfileID != profile.ID {
		return Manifest{}, ObservationSuite{}, fmt.Errorf(
			"deviation expectation profile_id %q does not match locked profile %q",
			expectation.ProfileID,
			profile.ID,
		)
	}
	if err := validateDeviationPolicy(policy); err != nil {
		return Manifest{}, ObservationSuite{}, fmt.Errorf("deviation policy: %w", err)
	}
	if expectation.Decision != policy.Decision {
		return Manifest{}, ObservationSuite{}, fmt.Errorf(
			"deviation expectation decision %q does not match policy %q",
			expectation.Decision,
			policy.Decision,
		)
	}

	policyByID := make(map[string]DeviationContractPolicy, len(policy.Contracts))
	policyOrder := make(map[string]int, len(policy.Contracts))
	for index, contract := range policy.Contracts {
		policyByID[contract.ID] = contract
		policyOrder[contract.ID] = index
	}
	if len(expectation.Contracts) != len(policy.Contracts) {
		return Manifest{}, ObservationSuite{}, fmt.Errorf(
			"deviation expectation contains %d contracts; policy requires %d",
			len(expectation.Contracts),
			len(policy.Contracts),
		)
	}
	for index := range policy.Contracts {
		want := policy.Contracts[index]
		got := expectation.Contracts[index]
		if got.ID != want.ID {
			return Manifest{}, ObservationSuite{}, fmt.Errorf(
				"deviation expectation contract %d is %q; policy requires %q in that position",
				index,
				got.ID,
				want.ID,
			)
		}
		if len(got.Changes) != len(want.Changes) {
			return Manifest{}, ObservationSuite{}, fmt.Errorf(
				"%s: deviation expectation contains %d changes; policy requires %d",
				got.ID,
				len(got.Changes),
				len(want.Changes),
			)
		}
		for changeIndex := range want.Changes {
			change := got.Changes[changeIndex]
			selector := want.Changes[changeIndex]
			if change.Dimension != selector.Dimension || change.Path != selector.Path || change.Operation != selector.Operation {
				return Manifest{}, ObservationSuite{}, fmt.Errorf(
					"%s: deviation change %d selector (%q, %q, %q) does not match policy (%q, %q, %q)",
					got.ID,
					changeIndex,
					change.Dimension,
					change.Path,
					change.Operation,
					selector.Dimension,
					selector.Path,
					selector.Operation,
				)
			}
		}
	}

	lastPolicyIndex := -1
	for _, contract := range manifest.Contracts {
		registered, isRegistered := policyByID[contract.ID]
		switch contract.Status {
		case ContractPassing:
			if isRegistered {
				return Manifest{}, ObservationSuite{}, fmt.Errorf(
					"%s: registered deviation is marked passing",
					contract.ID,
				)
			}
			if hasAnyDecisionProvenance(contract) {
				return Manifest{}, ObservationSuite{}, fmt.Errorf(
					"%s: passing contract must not carry decision provenance for policy %q",
					contract.ID,
					policy.Decision,
				)
			}
		case ContractDeviation:
			if !isRegistered {
				return Manifest{}, ObservationSuite{}, fmt.Errorf(
					"%s: manifest contains an unregistered deviation",
					contract.ID,
				)
			}
			if err := validateDecisionProvenance(contract, policy.Decision); err != nil {
				return Manifest{}, ObservationSuite{}, err
			}
			index := policyOrder[registered.ID]
			if index <= lastPolicyIndex {
				return Manifest{}, ObservationSuite{}, fmt.Errorf("%s: deviation policy order does not follow manifest order", contract.ID)
			}
			lastPolicyIndex = index
		case ContractOracleLocked:
			if isRegistered {
				return Manifest{}, ObservationSuite{}, fmt.Errorf(
					"%s: registered deviation status oracle_locked is not approved for product expectation",
					contract.ID,
				)
			}
		default:
			return Manifest{}, ObservationSuite{}, fmt.Errorf(
				"%s: manifest status %q is not approved for product expectation; want passing, deviation, or oracle_locked",
				contract.ID,
				contract.Status,
			)
		}
	}
	if lastPolicyIndex != len(policy.Contracts)-1 {
		return Manifest{}, ObservationSuite{}, fmt.Errorf("manifest is missing one or more registered deviations for decision %q", policy.Decision)
	}

	effective, err := cloneManifestValue(manifest)
	if err != nil {
		return Manifest{}, ObservationSuite{}, err
	}
	product, err := cloneObservationSuiteValue(reference)
	if err != nil {
		return Manifest{}, ObservationSuite{}, err
	}
	manifestIndex := make(map[string]int, len(manifest.Contracts))
	for index, contract := range manifest.Contracts {
		manifestIndex[contract.ID] = index
	}
	for _, contract := range expectation.Contracts {
		index, exists := manifestIndex[contract.ID]
		if !exists {
			return Manifest{}, ObservationSuite{}, fmt.Errorf("%s: deviation contract is absent from manifest", contract.ID)
		}
		observation := &product.Contracts[index]
		for changeIndex := range contract.Changes {
			change := contract.Changes[changeIndex]
			if err := applyDeviationChange(&effective.Contracts[index], observation, change); err != nil {
				return Manifest{}, ObservationSuite{}, fmt.Errorf("%s: apply deviation change %d: %w", contract.ID, changeIndex, err)
			}
		}
	}
	if err := ValidateSuiteAgainst(profile, effective, product); err != nil {
		return Manifest{}, ObservationSuite{}, fmt.Errorf("product expectation: %w", err)
	}
	return effective, product, nil
}

func validateDeviationPolicy(policy DeviationPolicy) error {
	if !decisionPattern.MatchString(policy.Decision) {
		return fmt.Errorf("decision %q must match %s", policy.Decision, decisionPattern)
	}
	if len(policy.Contracts) == 0 || len(policy.Contracts) > 12 {
		return fmt.Errorf("contracts must contain 1 to 12 ordered entries, got %d", len(policy.Contracts))
	}
	seenContracts := make(map[string]struct{}, len(policy.Contracts))
	for index, contract := range policy.Contracts {
		if !contractIDPattern.MatchString(contract.ID) {
			return fmt.Errorf("contract %d: id %q must match %s", index, contract.ID, contractIDPattern)
		}
		if _, exists := seenContracts[contract.ID]; exists {
			return fmt.Errorf("contract %d: duplicate id %q", index, contract.ID)
		}
		seenContracts[contract.ID] = struct{}{}
		if len(contract.Changes) == 0 {
			return fmt.Errorf("%s: changes must not be empty", contract.ID)
		}
		seenChanges := make(map[string]struct{}, len(contract.Changes))
		for changeIndex, change := range contract.Changes {
			if err := validateDeviationSelector(change); err != nil {
				return fmt.Errorf("%s: change %d: %w", contract.ID, changeIndex, err)
			}
			key := string(change.Dimension) + "\x00" + change.Path
			if _, exists := seenChanges[key]; exists {
				return fmt.Errorf("%s: change %d duplicates dimension %q path %q", contract.ID, changeIndex, change.Dimension, change.Path)
			}
			seenChanges[key] = struct{}{}
		}
	}
	return nil
}

func validateDeviationSelector(selector DeviationChangePolicy) error {
	switch selector.Dimension {
	case DeviationPhase:
		if selector.Path != "" || selector.Operation != DeviationReplace {
			return fmt.Errorf("phase selector must use an empty path and %q", DeviationReplace)
		}
	case DeviationResult, DeviationDBState, DeviationMetrics:
		if !deviationPathPattern.MatchString(selector.Path) {
			return fmt.Errorf("path %q must match %s", selector.Path, deviationPathPattern)
		}
		switch selector.Operation {
		case DeviationReplace:
		case DeviationInsertBefore:
			if !strings.HasSuffix(selector.Path, "]") {
				return fmt.Errorf("insert_before path %q must end at a list index", selector.Path)
			}
		default:
			return fmt.Errorf("unknown operation %q", selector.Operation)
		}
	default:
		return fmt.Errorf("unknown dimension %q", selector.Dimension)
	}
	return nil
}

func validateDecisionProvenance(contract Contract, decision string) error {
	matches := 0
	for _, provenance := range contract.Provenance {
		if provenance.Kind != "decision" {
			continue
		}
		if provenance.Reference != decision {
			return fmt.Errorf("%s: deviation carries unexpected decision %q", contract.ID, provenance.Reference)
		}
		if provenance.Derived == nil || *provenance.Derived {
			return fmt.Errorf("%s: decision provenance %q must set derived=false", contract.ID, decision)
		}
		matches++
	}
	if matches != 1 {
		return fmt.Errorf("%s: deviation must carry exactly one decision provenance %q, got %d", contract.ID, decision, matches)
	}
	return nil
}

func hasAnyDecisionProvenance(contract Contract) bool {
	for _, provenance := range contract.Provenance {
		if provenance.Kind == "decision" {
			return true
		}
	}
	return false
}

func applyDeviationChange(contract *Contract, observation *Observation, change DeviationChange) error {
	if change.Dimension == DeviationPhase {
		referencePhase := Phase(*change.Reference.Text)
		productPhase := Phase(*change.Product.Text)
		if observation.Phase != referencePhase || contract.Phase != referencePhase {
			return fmt.Errorf(
				"phase reference %q does not match locked observation %q and manifest %q",
				referencePhase,
				observation.Phase,
				contract.Phase,
			)
		}
		observation.Phase = productPhase
		contract.Phase = productPhase
		return nil
	}

	root, err := deviationDimensionRoot(observation, change.Dimension)
	if err != nil {
		return err
	}
	location, err := locateDeviationValue(root, change.Path)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(*location.value, change.Reference) {
		return fmt.Errorf(
			"%s path %q reference does not match locked observation (want %s, got %s)",
			change.Dimension,
			change.Path,
			formatJSON(change.Reference),
			formatJSON(*location.value),
		)
	}
	switch change.Operation {
	case DeviationReplace:
		*location.value = change.Product
	case DeviationInsertBefore:
		if location.list == nil || location.index < 0 {
			return fmt.Errorf("insert_before path %q must end at a list index", change.Path)
		}
		location.list.Items = append(location.list.Items, Value{})
		copy(location.list.Items[location.index+1:], location.list.Items[location.index:])
		location.list.Items[location.index] = change.Product
	default:
		return fmt.Errorf("unknown operation %q", change.Operation)
	}
	return nil
}

func deviationDimensionRoot(observation *Observation, dimension DeviationDimension) (*Value, error) {
	var root *Value
	switch dimension {
	case DeviationResult:
		root = observation.Result
	case DeviationDBState:
		root = observation.DBState
	case DeviationMetrics:
		root = observation.Metrics
	default:
		return nil, fmt.Errorf("dimension %q has no value root", dimension)
	}
	if root == nil {
		return nil, fmt.Errorf("dimension %q is absent from locked observation", dimension)
	}
	return root, nil
}

type deviationValueLocation struct {
	value *Value
	list  *Value
	index int
}

func locateDeviationValue(root *Value, path string) (deviationValueLocation, error) {
	current := root
	location := deviationValueLocation{value: root, index: -1}
	segments := strings.Split(path, ".")
	firstObjectSegment := 0
	if matches := deviationRootListPathSegmentPattern.FindStringSubmatch(segments[0]); matches != nil {
		if current.Type != ValueList {
			return deviationValueLocation{}, fmt.Errorf("path %q root index requires a list, got %q", path, current.Type)
		}
		index, err := strconv.Atoi(matches[1])
		if err != nil {
			return deviationValueLocation{}, fmt.Errorf("path %q root index %q is invalid", path, matches[1])
		}
		if index < 0 || index >= len(current.Items) {
			return deviationValueLocation{}, fmt.Errorf("path %q root index %d is outside %d items", path, index, len(current.Items))
		}
		list := current
		current = &list.Items[index]
		location = deviationValueLocation{value: current, list: list, index: index}
		firstObjectSegment = 1
	}
	for segmentIndex := firstObjectSegment; segmentIndex < len(segments); segmentIndex++ {
		segment := segments[segmentIndex]
		matches := deviationPathSegmentPattern.FindStringSubmatch(segment)
		if matches == nil {
			return deviationValueLocation{}, fmt.Errorf("invalid path segment %q", segment)
		}
		if current.Type != ValueObject {
			return deviationValueLocation{}, fmt.Errorf("path %q segment %d requires an object, got %q", path, segmentIndex, current.Type)
		}
		field := valueObjectField(current, matches[1])
		if field == nil {
			return deviationValueLocation{}, fmt.Errorf("path %q field %q is absent", path, matches[1])
		}
		current = field
		location = deviationValueLocation{value: current, index: -1}
		if matches[2] == "" {
			continue
		}
		index, err := strconv.Atoi(matches[2])
		if err != nil {
			return deviationValueLocation{}, fmt.Errorf("path %q index %q is invalid", path, matches[2])
		}
		if current.Type != ValueList {
			return deviationValueLocation{}, fmt.Errorf("path %q segment %d requires a list, got %q", path, segmentIndex, current.Type)
		}
		if index < 0 || index >= len(current.Items) {
			return deviationValueLocation{}, fmt.Errorf("path %q index %d is outside %d items", path, index, len(current.Items))
		}
		list := current
		current = &list.Items[index]
		location = deviationValueLocation{value: current, list: list, index: index}
	}
	return location, nil
}

func valueObjectField(value *Value, name string) *Value {
	for index := range value.Fields {
		if value.Fields[index].Name == name {
			return &value.Fields[index].Value
		}
	}
	return nil
}

func cloneManifestValue(value Manifest) (Manifest, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return Manifest{}, fmt.Errorf("clone manifest: %w", err)
	}
	var cloned Manifest
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return Manifest{}, fmt.Errorf("clone manifest: %w", err)
	}
	return cloned, nil
}

func cloneObservationSuiteValue(value ObservationSuite) (ObservationSuite, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ObservationSuite{}, fmt.Errorf("clone observation suite: %w", err)
	}
	var cloned ObservationSuite
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return ObservationSuite{}, fmt.Errorf("clone observation suite: %w", err)
	}
	return cloned, nil
}
