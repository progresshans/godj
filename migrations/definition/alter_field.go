package definition

import (
	"github.com/progresshans/godj/internal/identifiers"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

type alterFieldDocument struct {
	Kind      string        `json:"kind"`
	AppLabel  string        `json:"app_label"`
	ModelName string        `json:"model_name"`
	Before    fieldDocument `json:"before"`
	After     fieldDocument `json:"after"`
}

func validAlterFieldDefinition(value migrations.AlterField) bool {
	return identifiers.SQL(value.ModelName) && fullyNormalizedAddField(value.AppLabel, value.Before) &&
		fullyNormalizedAddField(value.AppLabel, value.After) && ir.ValidateChoiceChange(value.Before, value.After) == nil
}

func materializeAlterField(value jsonValue, app string) (migrations.Operation, bool) {
	model, hasModel := value.member("model_name")
	before, hasBefore := value.member("before")
	after, hasAfter := value.member("after")
	if !hasModel || model.kind != jsonString || !hasBefore || !hasAfter {
		return nil, false
	}
	oldField, validOld := materializeField(before)
	newField, validNew := materializeField(after)
	operation := migrations.AlterField{AppLabel: app, ModelName: model.string, Before: oldField, After: newField}
	if !validOld || !validNew || !validAlterFieldDefinition(operation) {
		return nil, false
	}
	return operation, true
}

func (scanner *encodingSizeScanner) scanAlterField(path string, value migrations.AlterField) error {
	if err := scanner.addStructural(path, uint64(len(`{"kind":"alter_field","app_label":"","model_name":"","before":,"after":}`))); err != nil {
		return err
	}
	if err := scanner.addString(path+".app_label", value.AppLabel); err != nil {
		return err
	}
	if err := scanner.addString(path+".model_name", value.ModelName); err != nil {
		return err
	}
	if err := scanner.scanField(path+".before", value.Before); err != nil {
		return err
	}
	return scanner.scanField(path+".after", value.After)
}

func appendCanonicalAlterField(output []byte, value migrations.AlterField) ([]byte, error) {
	output = append(output, `{"after":`...)
	output, err := appendCanonicalField(output, value.After)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"app_label":`...)
	output, err = appendCanonicalString(output, value.AppLabel)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"before":`...)
	output, err = appendCanonicalField(output, value.Before)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"kind":"alter_field","model_name":`...)
	output, err = appendCanonicalString(output, value.ModelName)
	if err != nil {
		return nil, err
	}
	return append(output, '}'), nil
}
