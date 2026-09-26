// Package irresource bounds the IR carried by a migration execution intent.
// It counts structure before a caller clones or expands it; backend policy and
// error classification remain with the boundary that requests the scan.
package irresource

import (
	"fmt"

	"github.com/progresshans/godj/schema/ir"
)

type Limits struct {
	Fields      int
	StringBytes int
	Nodes       uint64
	Bytes       uint64
}

type Budget struct {
	limits Limits
	nodes  uint64
	bytes  uint64
}

func New(limits Limits) Budget { return Budget{limits: limits} }

func (budget Budget) Counts() (nodes, bytes uint64) { return budget.nodes, budget.bytes }

func (budget *Budget) ScanModel(path string, model ir.Model) error {
	if err := budget.ConsumeNodes(path, 1); err != nil {
		return err
	}
	for _, value := range []string{model.Name, model.GoName, model.DBTable} {
		if err := budget.ConsumeString(path, value); err != nil {
			return err
		}
	}
	if len(model.ManyToMany) != 0 && (len(model.ManyToMany) > budget.limits.Fields || len(model.Fields) > budget.limits.Fields-len(model.ManyToMany)) {
		return fmt.Errorf("%s exceeds the combined stored and many-to-many field limit %d", path, budget.limits.Fields)
	}
	for index, field := range model.ManyToMany {
		p := fmt.Sprintf("%s.many_to_many[%d]", path, index)
		if err := budget.ConsumeNodes(p, 3); err != nil {
			return err
		}
		values := []string{field.Name, field.GoName, field.Target.AppLabel, field.Target.ModelName, field.Reverse.Name, string(field.Symmetry)}
		if through := field.Through; through != nil {
			if err := budget.ConsumeNodes(p+".through", 2); err != nil {
				return err
			}
			values = append(values, through.Model.AppLabel, through.Model.ModelName, through.SourceField, through.TargetField)
		}
		for _, value := range values {
			if err := budget.ConsumeString(p, value); err != nil {
				return err
			}
		}
	}
	if len(model.Fields) > budget.limits.Fields {
		return fmt.Errorf("%s has %d fields, maximum %d", path, len(model.Fields), budget.limits.Fields)
	}
	if err := budget.ConsumeNodes(path+".fields", len(model.Fields)); err != nil {
		return err
	}
	for index := range model.Fields {
		if err := budget.ScanField(fmt.Sprintf("%s.fields[%d]", path, index), model.Fields[index]); err != nil {
			return err
		}
	}
	if err := budget.ConsumeNodes(path+".unique_constraints", len(model.UniqueConstraints)); err != nil {
		return err
	}
	for index, constraint := range model.UniqueConstraints {
		constraintPath := fmt.Sprintf("%s.unique_constraints[%d]", path, index)
		if err := budget.ConsumeString(constraintPath+".name", constraint.Name); err != nil {
			return err
		}
		if len(constraint.Fields) > budget.limits.Fields {
			return fmt.Errorf("%s has %d members, maximum %d", constraintPath, len(constraint.Fields), budget.limits.Fields)
		}
		if err := budget.ConsumeNodes(constraintPath+".fields", len(constraint.Fields)); err != nil {
			return err
		}
		for _, name := range constraint.Fields {
			if err := budget.ConsumeString(constraintPath+".fields", name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (budget *Budget) ScanField(path string, field ir.Field) error {
	if err := budget.ConsumeNodes(path, 1); err != nil {
		return err
	}
	for _, value := range []string{field.Name, field.GoName, field.Column, string(field.Kind)} {
		if err := budget.ConsumeString(path, value); err != nil {
			return err
		}
	}
	if field.Decimal != nil {
		if err := budget.ConsumeNodes(path+".decimal", 1); err != nil {
			return err
		}
	}
	if field.Default != nil {
		if err := budget.ConsumeNodes(path+".default", 1); err != nil {
			return err
		}
		for _, value := range []string{string(field.Default.Kind), field.Default.String, field.Default.Time, field.Default.Duration, field.Default.Date, field.Default.DateTime, field.Default.FloatBits, field.Default.Decimal, field.Default.UUID, field.Default.JSON} {
			if err := budget.ConsumeString(path+".default", value); err != nil {
				return err
			}
		}
	}
	if err := budget.ConsumeNodes(path+".choices", len(field.Choices)); err != nil {
		return err
	}
	if err := budget.ConsumeNodes(path+".choices.values", len(field.Choices)); err != nil {
		return err
	}
	for _, choice := range field.Choices {
		for _, value := range []string{choice.Label, string(choice.Value.Kind), choice.Value.String, choice.Value.Time, choice.Value.Duration, choice.Value.Date, choice.Value.DateTime, choice.Value.FloatBits, choice.Value.Decimal, choice.Value.UUID, choice.Value.JSON} {
			if err := budget.ConsumeString(path+".choices", value); err != nil {
				return err
			}
		}
	}
	if field.Relation != nil {
		if err := budget.ConsumeNodes(path+".relation", 1); err != nil {
			return err
		}
		for _, value := range []string{
			field.Relation.Target.AppLabel, field.Relation.Target.ModelName,
			string(field.Relation.Cardinality), field.Relation.Reverse.Name,
			string(field.Relation.OnDelete),
		} {
			if err := budget.ConsumeString(path+".relation", value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (budget *Budget) ConsumeNodes(path string, count int) error {
	if count < 0 || uint64(count) > budget.limits.Nodes-budget.nodes {
		return fmt.Errorf("%s exceeds the aggregate relation intent node limit %d", path, budget.limits.Nodes)
	}
	budget.nodes += uint64(count)
	return nil
}

func (budget *Budget) ConsumeString(path, value string) error {
	if len(value) > budget.limits.StringBytes {
		return fmt.Errorf("%s contains a string of %d bytes, maximum %d", path, len(value), budget.limits.StringBytes)
	}
	if uint64(len(value)) > budget.limits.Bytes-budget.bytes {
		return fmt.Errorf("%s exceeds the aggregate relation intent byte limit %d", path, budget.limits.Bytes)
	}
	budget.bytes += uint64(len(value))
	return nil
}
