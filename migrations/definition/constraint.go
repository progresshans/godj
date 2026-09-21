package definition

import (
	"github.com/progresshans/godj/internal/identifiers"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

type constraintDocument struct {
	Kind       string              `json:"kind"`
	AppLabel   string              `json:"app_label"`
	ModelName  string              `json:"model_name"`
	Constraint ir.UniqueConstraint `json:"constraint"`
}

func validConstraintDefinition(model string, constraint ir.UniqueConstraint) bool {
	if !identifiers.SQL(model) || !identifiers.SQL(constraint.Name) || len(constraint.Fields) == 0 {
		return false
	}
	seen := make(map[string]bool, len(constraint.Fields))
	for _, name := range constraint.Fields {
		if !identifiers.SQL(name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

func materializeConstraintOperation(value jsonValue, app, kind string) (migrations.Operation, bool) {
	model, hasModel := value.member("model_name")
	raw, hasConstraint := value.member("constraint")
	if !hasModel || model.kind != jsonString || !hasConstraint {
		return nil, false
	}
	constraint, valid := materializeUniqueConstraint(raw)
	if !valid || !validConstraintDefinition(model.string, constraint) {
		return nil, false
	}
	if kind == "add_constraint" {
		return migrations.AddConstraint{AppLabel: app, ModelName: model.string, Constraint: constraint}, true
	}
	return migrations.RemoveConstraint{AppLabel: app, ModelName: model.string, Constraint: constraint}, true
}

func (scanner *encodingSizeScanner) scanConstraintOperation(path, kind, app, model string, constraint ir.UniqueConstraint) error {
	if err := scanner.addStructural(path, uint64(len(`{"kind":"","app_label":"","model_name":"","constraint":}`)+len(kind))); err != nil {
		return err
	}
	if err := scanner.addString(path+".app_label", app); err != nil {
		return err
	}
	if err := scanner.addString(path+".model_name", model); err != nil {
		return err
	}
	return scanner.scanUniqueConstraint(path+".constraint", constraint)
}

func appendCanonicalConstraintOperation(output []byte, kind, app, model string, constraint ir.UniqueConstraint) ([]byte, error) {
	output = append(output, `{"app_label":`...)
	output, err := appendCanonicalString(output, app)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"constraint":`...)
	output, err = appendCanonicalUniqueConstraint(output, constraint)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"kind":`...)
	output, err = appendCanonicalString(output, kind)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"model_name":`...)
	output, err = appendCanonicalString(output, model)
	if err != nil {
		return nil, err
	}
	return append(output, '}'), nil
}
