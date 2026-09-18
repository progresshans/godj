package definition

import (
	"strconv"

	"github.com/progresshans/godj/schema/ir"
)

func collectChoicesCandidates(value, field jsonValue, sourceID, pointer, app, name string, operationIndex int) []failureCandidate {
	if value.kind != jsonArray || len(value.array) == 0 {
		return []failureCandidate{semanticFailure(CodeInvalidIR, sourceID, pointer, app, name, operationIndex, "invalid_ir")}
	}
	var candidates []failureCandidate
	for index, choice := range value.array {
		itemPath := pointer + "/" + strconv.Itoa(index)
		object, valid, faults := semanticObjectCandidates(choice, []string{"label", "value"}, sourceID, itemPath, app, name, operationIndex, CodeInvalidIR)
		candidates = append(candidates, faults...)
		if !valid {
			continue
		}
		if label, exists := object.member("label"); exists && label.kind != jsonString {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, itemPath+"/label", app, name, operationIndex, "invalid_ir"))
		}
		if scalar, exists := object.member("value"); exists {
			if scalar.kind == jsonNull {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, itemPath+"/value", app, name, operationIndex, "invalid_ir"))
			} else {
				candidates = append(candidates, collectDefaultCandidates(scalar, sourceID, itemPath+"/value", app, name, operationIndex)...)
			}
		}
	}
	choices, valid := materializeChoices(value)
	kind, hasKind := field.member("kind")
	maximum, hasMaximum := field.member("max_length")
	length, _, validLength := signedInteger(maximum)
	if valid && hasKind && kind.kind == jsonString && hasMaximum && validLength && length >= 0 && length <= maximumWireLength {
		if err := ir.ValidateChoices(ir.Field{Kind: ir.FieldKind(kind.string), MaxLength: int(length), Choices: choices}); err != nil {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer, app, name, operationIndex, "invalid_ir"))
		}
	}
	return candidates
}

func materializeChoices(value jsonValue) ([]ir.Choice, bool) {
	if value.kind != jsonArray || len(value.array) == 0 {
		return nil, false
	}
	choices := make([]ir.Choice, len(value.array))
	for index, item := range value.array {
		if item.kind != jsonObject {
			return nil, false
		}
		label, hasLabel := item.member("label")
		value, hasValue := item.member("value")
		if !hasLabel || label.kind != jsonString || !hasValue {
			return nil, false
		}
		scalar, valid := materializeDefault(value)
		if !valid || scalar == nil {
			return nil, false
		}
		choices[index] = ir.Choice{Value: *scalar, Label: label.string}
	}
	return choices, true
}
