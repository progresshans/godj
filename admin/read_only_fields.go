package admin

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/templates"
)

const MaximumReadOnlyFields = 64

// ReadOnlyField derives bounded plain display text from the authorized object
// snapshot. Value must be pure: it must not read storage, acquire authorization,
// or retain the supplied value. HTML is always escaped by the renderer.
// These fields appear on existing-object change/detail pages, never as inputs.
type ReadOnlyField[M any] struct {
	Name  string
	Label string
	Value func(M) (string, error)
}

type ReadOnlyFieldDescriptor struct {
	Name  string
	Label string
}

func prepareReadOnlyFields[M any](configured []ReadOnlyField[M], form, create forms.Spec) ([]ReadOnlyFieldDescriptor, func(context.Context, M) (templates.Value, error), error) {
	if len(configured) > MaximumReadOnlyFields {
		return nil, nil, &ConfigError{Path: "model.read_only_fields", Code: "limit_exceeded"}
	}
	owned := append([]ReadOnlyField[M](nil), configured...)
	reserved := map[string]bool{"csrfmiddlewaretoken": true, "expected_revision": true}
	for _, field := range append(form.Fields(), create.Fields()...) {
		reserved[field.Name()] = true
	}
	descriptors := make([]ReadOnlyFieldDescriptor, len(owned))
	for index, field := range owned {
		if _, err := forms.CharField(field.Name); err != nil || reserved[field.Name] {
			return nil, nil, &ConfigError{Path: "model.read_only_fields", Code: "invalid_duplicate_or_editable_name"}
		}
		if field.Value == nil || strings.TrimSpace(field.Label) == "" || len(field.Label) > MaximumDisplayBytes || !utf8.ValidString(field.Label) || containsUnsafeDisplayControl(field.Label) {
			return nil, nil, &ConfigError{Path: "model.read_only_fields", Code: "invalid_definition"}
		}
		reserved[field.Name] = true
		descriptors[index] = ReadOnlyFieldDescriptor{Name: field.Name, Label: field.Label}
	}
	read := func(ctx context.Context, item M) (templates.Value, error) {
		values := make([]templates.Value, 0, len(owned))
		for _, field := range owned {
			if err := ctx.Err(); err != nil {
				return templates.Value{}, err
			}
			value, err := field.Value(item)
			if err != nil {
				return templates.Value{}, &ConfigError{Path: "object.read_only_fields", Code: "read_failed", Cause: err}
			}
			if len(value) > MaximumDisplayBytes || !utf8.ValidString(value) || containsUnsafeDisplayControl(value) {
				return templates.Value{}, &ConfigError{Path: "object.read_only_fields", Code: "invalid_value"}
			}
			entry, err := templates.Object(map[string]templates.Value{"name": templates.String(field.Name), "label": templates.String(field.Label), "value": templates.String(value)})
			if err != nil {
				return templates.Value{}, err
			}
			values = append(values, entry)
		}
		if err := ctx.Err(); err != nil {
			return templates.Value{}, err
		}
		return templates.List(values...), nil
	}
	return descriptors, read, nil
}
