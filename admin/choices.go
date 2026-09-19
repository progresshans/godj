package admin

import (
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
)

func modelChoiceLabels(model ir.Model, selected []string) map[string]map[ir.Scalar]string {
	labels := make(map[string]map[ir.Scalar]string)
	for _, name := range selected {
		for _, field := range model.Fields {
			if field.Name != name || len(field.Choices) == 0 {
				continue
			}
			values := make(map[ir.Scalar]string, len(field.Choices))
			for _, choice := range field.Choices {
				values[choice.Value] = choice.Label
			}
			labels[name] = values
		}
	}
	return labels
}

func choiceDisplayValue(labels map[ir.Scalar]string, value templates.Value) templates.Value {
	var scalar ir.Scalar
	if text, ok := value.AsString(); ok {
		scalar = ir.Scalar{Kind: ir.ScalarString, String: text}
	} else if integer, ok := value.AsInteger(); ok {
		scalar = ir.Scalar{Kind: ir.ScalarInteger, Integer: integer}
	} else {
		return value
	}
	if label, found := labels[scalar]; found {
		return templates.String(label)
	}
	return value
}

func choiceOptionValues(field forms.Field, selected string) ([]templates.Value, error) {
	if field.Widget() != forms.Select && field.Widget() != forms.NullBooleanSelect {
		return nil, nil
	}
	choices := field.Choices()
	options := make([]templates.Value, 0, len(choices)+2)
	appendOption := func(value, label string) error {
		option, err := templateObject(map[string]templates.Value{
			"value": templates.String(value), "label": templates.String(label),
			"selected": templates.Bool(value == selected),
		})
		if err == nil {
			options = append(options, option)
		}
		return err
	}
	if field.Widget() == forms.NullBooleanSelect {
		for _, option := range [][2]string{{"unknown", "Unknown"}, {"true", "Yes"}, {"false", "No"}} {
			if err := appendOption(option[0], option[1]); err != nil {
				return nil, err
			}
		}
		return options, nil
	}
	hasEmpty, hasSelected := false, selected == ""
	for _, choice := range choices {
		hasEmpty = hasEmpty || choice.InputValue() == ""
		hasSelected = hasSelected || choice.InputValue() == selected
	}
	if !hasEmpty {
		if err := appendOption("", "---------"); err != nil {
			return nil, err
		}
	}
	// Preserve an old or rejected value visibly. Opening a change form must not
	// silently select the first allowed value; normal binding still rejects it.
	if !hasSelected {
		if err := appendOption(selected, selected+" (not a current choice)"); err != nil {
			return nil, err
		}
	}
	for _, choice := range choices {
		if err := appendOption(choice.InputValue(), choice.Label); err != nil {
			return nil, err
		}
	}
	return options, nil
}
