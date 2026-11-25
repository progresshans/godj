package definition

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

// Field order is the canonical definition encoding. All strings in a valid
// data operation are ASCII identifiers or closed action names.
type dataDocument struct {
	App     string                `json:"app_label"`
	Forward migrations.DataAction `json:"forward"`
	Kind    string                `json:"kind"`
	Models  []ir.ModelIdentity    `json:"models"`
	Name    string                `json:"name"`
	Reverse migrations.DataAction `json:"reverse"`
	Version uint32                `json:"version"`
}

func dataOperationDocument(input migrations.RunData) (dataDocument, error) {
	op, err := input.Normalized()
	if err != nil {
		return dataDocument{}, err
	}
	if op.Models == nil {
		op.Models = []ir.ModelIdentity{}
	}
	return dataDocument{App: op.Key.App, Forward: op.Forward, Kind: "run_data", Models: op.Models, Name: op.Key.Name, Reverse: op.Reverse, Version: op.Key.Version}, nil
}

func appendCanonicalData(output []byte, op migrations.RunData) ([]byte, error) {
	doc, err := dataOperationDocument(op)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(doc)
	return append(output, body...), err
}

func materializeData(value jsonValue, app string) (migrations.Operation, bool) {
	name, _ := value.member("name")
	forward, _ := value.member("forward")
	reverse, _ := value.member("reverse")
	version, _ := value.member("version")
	models, _ := value.member("models")
	number, _, valid := signedInteger(version)
	if !valid || number <= 0 || number > math.MaxUint32 || name.kind != jsonString || forward.kind != jsonString ||
		reverse.kind != jsonString || models.kind != jsonArray || len(models.array) > MaxSources {
		return nil, false
	}
	op := migrations.RunData{
		Key:     migrations.DataKey{App: app, Name: name.string, Version: uint32(number)},
		Forward: migrations.DataAction(forward.string), Reverse: migrations.DataAction(reverse.string),
		Models: make([]ir.ModelIdentity, len(models.array)),
	}
	for i, item := range models.array {
		appLabel, _ := item.member("app_label")
		modelName, _ := item.member("model_name")
		if item.kind != jsonObject || len(item.object) != 2 || appLabel.kind != jsonString || modelName.kind != jsonString {
			return nil, false
		}
		op.Models[i] = ir.ModelIdentity{AppLabel: appLabel.string, ModelName: modelName.string}
	}
	normalized, err := op.Normalized()
	return normalized, err == nil
}

func collectDataCandidates(value jsonValue, sourceID, pointer, app, name string, index int) []failureCandidate {
	object, valid, failures := semanticObjectCandidates(value, []string{"app_label", "forward", "kind", "models", "name", "reverse", "version"}, sourceID, pointer, app, name, index, CodeInvalidOperation)
	if !valid {
		return failures
	}
	appLabel, _ := object.member("app_label")
	if appLabel.kind != jsonString || appLabel.string != app {
		failures = append(failures, semanticFailure(CodeInvalidOperation, sourceID, pointer+"/app_label", app, name, index, "invalid_operation"))
	}
	if _, ok := materializeData(object, app); !ok {
		failures = append(failures, semanticFailure(CodeInvalidOperation, sourceID, pointer, app, name, index, "invalid_operation"))
	}
	return failures
}

func (scanner *encodingSizeScanner) scanData(path string, op migrations.RunData) error {
	if len(op.Models) > MaxSources {
		return encodeFailure(path+".models", "data operation model count exceeds the definition limit")
	}
	framing := len(`{"app_label":"","forward":"","kind":"run_data","models":[],"name":"","reverse":"","version":}`) + len(strconv.FormatUint(uint64(op.Key.Version), 10))
	if len(op.Models) > 1 {
		framing += len(op.Models) - 1
	}
	if err := scanner.addStructural(path, uint64(framing)); err != nil {
		return err
	}
	for _, str := range []string{op.Key.App, op.Key.Name, string(op.Forward), string(op.Reverse)} {
		if err := scanner.addString(path, str); err != nil {
			return err
		}
	}
	for i, model := range op.Models {
		p := path + ".models[" + strconv.Itoa(i) + "]"
		if err := scanner.addStructural(p, uint64(len(`{"app_label":"","model_name":""}`))); err != nil {
			return err
		}
		if err := scanner.addString(p+".app_label", model.AppLabel); err != nil {
			return err
		}
		if err := scanner.addString(p+".model_name", model.ModelName); err != nil {
			return err
		}
	}
	return nil
}
