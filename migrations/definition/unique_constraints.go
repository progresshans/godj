package definition

import (
	"fmt"
	"strconv"

	"github.com/progresshans/godj/internal/identifiers"
	"github.com/progresshans/godj/schema/ir"
)

func collectModelConstraintCandidates(value, model jsonValue, sourceID, pointer, app, name string, operationIndex int) []failureCandidate {
	if value.kind != jsonArray || len(value.array) == 0 {
		return []failureCandidate{semanticFailure(CodeInvalidIR, sourceID, pointer, app, name, operationIndex, "invalid_ir")}
	}
	if len(value.array) > MaxConstraintsPerCreateModel {
		return []failureCandidate{semanticResourceFailure(CodeInvalidIR, sourceID, pointer, app, name, "constraints_per_create_model", MaxConstraintsPerCreateModel, uint64(len(value.array)), operationIndex)}
	}
	fields := make(map[string]bool)
	if members, present := model.member("fields"); present && members.kind == jsonArray {
		for _, field := range members.array {
			if name, exists := field.member("name"); exists && name.kind == jsonString {
				fields[name.string] = true
			}
		}
	}
	var candidates []failureCandidate
	previous := ""
	for index, value := range value.array {
		path := pointer + "/" + strconv.Itoa(index)
		candidates = append(candidates, collectUniqueConstraintCandidates(value, sourceID, path, app, name, operationIndex)...)
		constraint, valid := materializeUniqueConstraint(value)
		if !valid {
			continue
		}
		if constraint.Name <= previous {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, path+"/name", app, name, operationIndex, "invalid_ir"))
		}
		previous = constraint.Name
		for memberIndex, member := range constraint.Fields {
			if !fields[member] {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, path+"/fields/"+strconv.Itoa(memberIndex), app, name, operationIndex, "invalid_ir"))
			}
		}
	}
	return candidates
}

func collectUniqueConstraintCandidates(value jsonValue, sourceID, pointer, app, name string, operationIndex int) []failureCandidate {
	object, valid, candidates := semanticObjectCandidates(value, []string{"name", "fields"}, sourceID, pointer, app, name, operationIndex, CodeInvalidIR)
	if !valid {
		return candidates
	}
	if identity, exists := object.member("name"); exists && (identity.kind != jsonString || !identifiers.SQL(identity.string)) {
		candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/name", app, name, operationIndex, "invalid_ir"))
	}
	if fields, exists := object.member("fields"); exists {
		if fields.kind != jsonArray || len(fields.array) == 0 {
			return append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/fields", app, name, operationIndex, "invalid_ir"))
		}
		if len(fields.array) > MaxFieldsPerConstraint {
			return append(candidates, semanticResourceFailure(CodeInvalidIR, sourceID, pointer+"/fields", app, name, "fields_per_constraint", MaxFieldsPerConstraint, uint64(len(fields.array)), operationIndex))
		}
		seen := make(map[string]bool, len(fields.array))
		for index, field := range fields.array {
			if field.kind != jsonString || !identifiers.SQL(field.string) || seen[field.string] {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/fields/"+strconv.Itoa(index), app, name, operationIndex, "invalid_ir"))
			}
			seen[field.string] = true
		}
	}
	return candidates
}

func materializeUniqueConstraint(value jsonValue) (ir.UniqueConstraint, bool) {
	if value.kind != jsonObject {
		return ir.UniqueConstraint{}, false
	}
	name, named := value.member("name")
	fields, present := value.member("fields")
	if !named || name.kind != jsonString || !present || fields.kind != jsonArray || len(fields.array) == 0 || len(fields.array) > MaxFieldsPerConstraint {
		return ir.UniqueConstraint{}, false
	}
	constraint := ir.UniqueConstraint{Name: name.string, Fields: make([]string, len(fields.array))}
	for index, field := range fields.array {
		if field.kind != jsonString {
			return ir.UniqueConstraint{}, false
		}
		constraint.Fields[index] = field.string
	}
	return constraint, true
}

func appendCanonicalUniqueConstraint(output []byte, constraint ir.UniqueConstraint) ([]byte, error) {
	output = append(output, `{"fields":[`...)
	var err error
	for index, name := range constraint.Fields {
		if index != 0 {
			output = append(output, ',')
		}
		output, err = appendCanonicalString(output, name)
		if err != nil {
			return nil, err
		}
	}
	output = append(output, `],"name":`...)
	output, err = appendCanonicalString(output, constraint.Name)
	if err != nil {
		return nil, err
	}
	return append(output, '}'), nil
}

func (scanner *encodingSizeScanner) scanUniqueConstraint(path string, constraint ir.UniqueConstraint) error {
	if len(constraint.Fields) > MaxFieldsPerConstraint {
		return encodeFailure(path+".fields", "fields_per_constraint resource limit exceeded: got %d, maximum %d", len(constraint.Fields), MaxFieldsPerConstraint)
	}
	if err := scanner.addStructural(path, uint64(len(`{"name":"","fields":[]}`))); err != nil {
		return err
	}
	if err := scanner.addString(path+".name", constraint.Name); err != nil {
		return err
	}
	for index, field := range constraint.Fields {
		memberPath := fmt.Sprintf("%s.fields[%d]", path, index)
		punctuation := uint64(2)
		if index != 0 {
			punctuation++
		}
		if err := scanner.addStructural(memberPath, punctuation); err != nil {
			return err
		}
		if err := scanner.addString(memberPath, field); err != nil {
			return err
		}
	}
	return nil
}
