package definition

import (
	"fmt"
	"slices"
	"sort"

	"github.com/progresshans/godj/internal/identifiers"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

type manyDocument struct {
	Kind        string              `json:"kind"`
	AppLabel    string              `json:"app_label"`
	ModelName   string              `json:"model_name"`
	Field       *ir.ManyToManyField `json:"field,omitempty"`
	BeforeField string              `json:"before_field,omitempty"`
	Before      *ir.ManyToManyField `json:"before,omitempty"`
	After       *ir.ManyToManyField `json:"after,omitempty"`
}

func manyOperationDocument(operation migrations.Operation) manyDocument {
	switch op := operation.(type) {
	case migrations.AddManyToMany:
		return manyDocument{Kind: "add_many_to_many", AppLabel: op.AppLabel, ModelName: op.ModelName, Field: &op.Field, BeforeField: op.BeforeField}
	case migrations.RemoveManyToMany:
		return manyDocument{Kind: "remove_many_to_many", AppLabel: op.AppLabel, ModelName: op.ModelName, Field: &op.Field, BeforeField: op.BeforeField}
	case migrations.RenameManyToMany:
		return manyDocument{Kind: "rename_many_to_many", AppLabel: op.AppLabel, ModelName: op.ModelName, Before: &op.Before, After: &op.After}
	}
	return manyDocument{}
}

func validManyOperation(op manyDocument) bool {
	if !identifiers.SQL(op.AppLabel) || !identifiers.SQL(op.ModelName) {
		return false
	}
	for _, field := range []*ir.ManyToManyField{op.Field, op.Before, op.After} {
		if field == nil {
			continue
		}
		normalized, err := ir.NormalizeManyToManyField(op.AppLabel, op.ModelName, *field)
		if err != nil || !normalized.Equal(*field) {
			return false
		}
	}
	if op.Kind == "rename_many_to_many" {
		return op.Before != nil && op.After != nil && ir.ManyToManyRename(*op.Before, *op.After)
	}
	return op.Field != nil && (op.BeforeField == "" || identifiers.SQL(op.BeforeField) && op.BeforeField != op.Field.Name)
}

// The parser has already rejected duplicate keys and bounded all nodes/bytes.
// These closed shapes additionally reject unknown or missing nested members.
func manyObject(value jsonValue, required, optional []string) bool {
	if value.kind != jsonObject {
		return false
	}
	for _, member := range value.object {
		if !slices.Contains(required, member.key) && !slices.Contains(optional, member.key) {
			return false
		}
	}
	for _, key := range required {
		if _, exists := value.member(key); !exists {
			return false
		}
	}
	return true
}

func manyString(value jsonValue, key string) (string, bool) {
	item, exists := value.member(key)
	return item.string, exists && item.kind == jsonString
}

func materializeManyIdentity(value jsonValue) (ir.ModelIdentity, bool) {
	if !manyObject(value, []string{"app_label", "model_name"}, nil) {
		return ir.ModelIdentity{}, false
	}
	app, a := manyString(value, "app_label")
	model, m := manyString(value, "model_name")
	return ir.ModelIdentity{AppLabel: app, ModelName: model}, a && m
}

func materializeManyField(value jsonValue) (ir.ManyToManyField, bool) {
	var field ir.ManyToManyField
	if !manyObject(value, []string{"name", "go_name", "target", "reverse", "symmetry"}, []string{"through"}) {
		return field, false
	}
	name, a := manyString(value, "name")
	goName, b := manyString(value, "go_name")
	symmetry, c := manyString(value, "symmetry")
	target, _ := value.member("target")
	identity, d := materializeManyIdentity(target)
	reverse, _ := value.member("reverse")
	if !a || !b || !c || !d || !manyObject(reverse, nil, []string{"name", "disabled"}) {
		return field, false
	}
	field = ir.ManyToManyField{Name: name, GoName: goName, Target: identity, Symmetry: ir.ManyToManySymmetry(symmetry)}
	if v, exists := reverse.member("name"); exists {
		if v.kind != jsonString {
			return field, false
		}
		field.Reverse.Name = v.string
	}
	if v, exists := reverse.member("disabled"); exists {
		if v.kind != jsonBoolean {
			return field, false
		}
		field.Reverse.Disabled = v.boolean
	}
	if through, exists := value.member("through"); exists {
		if !manyObject(through, []string{"model", "source_field", "target_field"}, nil) {
			return field, false
		}
		model, _ := through.member("model")
		identity, valid := materializeManyIdentity(model)
		source, s := manyString(through, "source_field")
		target, t := manyString(through, "target_field")
		if !valid || !s || !t {
			return field, false
		}
		field.Through = &ir.ThroughModel{Model: identity, SourceField: source, TargetField: target}
	}
	return field, true
}

func materializeManyOperation(value jsonValue, app, kind string) (migrations.Operation, bool) {
	model, ok := manyString(value, "model_name")
	if !ok {
		return nil, false
	}
	if kind == "rename_many_to_many" {
		rawBefore, _ := value.member("before")
		rawAfter, _ := value.member("after")
		before, b := materializeManyField(rawBefore)
		after, a := materializeManyField(rawAfter)
		op := migrations.RenameManyToMany{AppLabel: app, ModelName: model, Before: before, After: after}
		return op, a && b && validManyOperation(manyOperationDocument(op))
	}
	raw, _ := value.member("field")
	field, ok := materializeManyField(raw)
	anchor, valid := materializeBeforeField(value)
	if !ok || !valid {
		return nil, false
	}
	var op migrations.Operation = migrations.AddManyToMany{AppLabel: app, ModelName: model, Field: field, BeforeField: anchor}
	if kind == "remove_many_to_many" {
		op = migrations.RemoveManyToMany{AppLabel: app, ModelName: model, Field: field, BeforeField: anchor}
	}
	return op, validManyOperation(manyOperationDocument(op))
}

func (scanner *encodingSizeScanner) scanManyOperation(path string, op manyDocument) error {
	if err := scanner.addStructural(path, uint64(len(`{"kind":"","app_label":"","model_name":""}`)+len(op.Kind))); err != nil {
		return err
	}
	for _, value := range []string{op.AppLabel, op.ModelName} {
		if err := scanner.addString(path, value); err != nil {
			return err
		}
	}
	if op.BeforeField != "" {
		if err := scanner.addStructural(path, uint64(len(`,"before_field":""`))); err != nil {
			return err
		}
		if err := scanner.addString(path+".before_field", op.BeforeField); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		name  string
		field *ir.ManyToManyField
	}{{"field", op.Field}, {"before", op.Before}, {"after", op.After}} {
		if item.field == nil {
			continue
		}
		if err := scanner.addStructural(path, uint64(len(item.name)+4)); err != nil {
			return err
		}
		if err := scanner.scanManyField(path+"."+item.name, *item.field); err != nil {
			return err
		}
	}
	return nil
}

func (scanner *encodingSizeScanner) scanManyField(path string, field ir.ManyToManyField) error {
	if err := scanner.addStructural(path, uint64(len(`{"name":"","go_name":"","target":{"app_label":"","model_name":""},"reverse":{},"symmetry":""}`))); err != nil {
		return err
	}
	for _, value := range []string{field.Name, field.GoName, field.Target.AppLabel, field.Target.ModelName, string(field.Symmetry)} {
		if err := scanner.addString(path, value); err != nil {
			return err
		}
	}
	if field.Reverse.Name != "" {
		if err := scanner.addStructural(path, uint64(len(`"name":""`))); err != nil {
			return err
		}
		if err := scanner.addString(path, field.Reverse.Name); err != nil {
			return err
		}
	}
	if field.Reverse.Disabled {
		if err := scanner.addStructural(path, uint64(len(`"disabled":true`))); err != nil {
			return err
		}
	}
	if through := field.Through; through != nil {
		if err := scanner.addStructural(path, uint64(len(`,"through":{"model":{"app_label":"","model_name":""},"source_field":"","target_field":""}`))); err != nil {
			return err
		}
		for _, value := range []string{through.Model.AppLabel, through.Model.ModelName, through.SourceField, through.TargetField} {
			if err := scanner.addString(path+".through", value); err != nil {
				return err
			}
		}
	}
	return nil
}

func canonicalManyField(field ir.ManyToManyField) map[string]any {
	identity := func(value ir.ModelIdentity) map[string]any {
		return map[string]any{"app_label": value.AppLabel, "model_name": value.ModelName}
	}
	reverse := map[string]any{}
	if field.Reverse.Name != "" {
		reverse["name"] = field.Reverse.Name
	}
	if field.Reverse.Disabled {
		reverse["disabled"] = true
	}
	value := map[string]any{"name": field.Name, "go_name": field.GoName, "target": identity(field.Target), "reverse": reverse, "symmetry": string(field.Symmetry)}
	if through := field.Through; through != nil {
		value["through"] = map[string]any{"model": identity(through.Model), "source_field": through.SourceField, "target_field": through.TargetField}
	}
	return value
}

func appendCanonicalManyOperation(output []byte, operation migrations.Operation) ([]byte, error) {
	op := manyOperationDocument(operation)
	value := map[string]any{"kind": op.Kind, "app_label": op.AppLabel, "model_name": op.ModelName}
	if op.BeforeField != "" {
		value["before_field"] = op.BeforeField
	}
	if op.Field != nil {
		value["field"] = canonicalManyField(*op.Field)
	}
	if op.Before != nil {
		value["before"] = canonicalManyField(*op.Before)
		value["after"] = canonicalManyField(*op.After)
	}
	return appendCanonicalManyObject(output, value)
}

func appendCanonicalManyObject(output []byte, object map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	output = append(output, '{')
	for index, key := range keys {
		if index != 0 {
			output = append(output, ',')
		}
		var err error
		output, err = appendCanonicalString(output, key)
		if err != nil {
			return nil, err
		}
		output = append(output, ':')
		switch value := object[key].(type) {
		case string:
			output, err = appendCanonicalString(output, value)
		case bool:
			if value {
				output = append(output, "true"...)
			} else {
				output = append(output, "false"...)
			}
		case map[string]any:
			output, err = appendCanonicalManyObject(output, value)
		default:
			return nil, fmt.Errorf("invalid canonical ManyToMany value %T", value)
		}
		if err != nil {
			return nil, err
		}
	}
	return append(output, '}'), nil
}
