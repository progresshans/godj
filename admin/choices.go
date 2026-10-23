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

func choiceOptionValues(field forms.Field, selections ...string) ([]templates.Value, error) {
	if field.Widget() == forms.SelectMultiple {
		return multipleChoiceOptionValues(field, selections)
	}
	selected := ""
	if len(selections) > 0 {
		selected = selections[0]
	}
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

func multipleChoiceOptionValues(field forms.Field, selected []string) ([]templates.Value, error) {
	chosen := make(map[string]bool, len(selected))
	for _, value := range selected {
		chosen[value] = true
	}
	choices := field.Choices()
	known := make(map[string]bool, len(choices))
	for _, choice := range choices {
		known[choice.InputValue()] = true
	}
	result := make([]templates.Value, 0, len(choices)+len(selected))
	appendOption := func(value, label string, chosen bool) error {
		option, err := templateObject(map[string]templates.Value{"value": templates.String(value), "label": templates.String(label), "selected": templates.Bool(chosen)})
		if err == nil {
			result = append(result, option)
		}
		return err
	}
	for _, value := range selected {
		if !known[value] {
			if err := appendOption(value, value+" (not a current choice)", true); err != nil {
				return nil, err
			}
			known[value] = true
		}
	}
	for _, choice := range choices {
		if err := appendOption(choice.InputValue(), choice.Label, chosen[choice.InputValue()]); err != nil {
			return nil, err
		}
	}
	return result, nil
}
