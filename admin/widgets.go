package admin

import (
	"context"
	"fmt"
	"io/fs"
	"reflect"
	"slices"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/templates"
)

const MaximumWidgetTemplates = 64

// WidgetTemplate replaces the control markup for one native widget. Template
// source is trusted application code. It receives only the sanitized field
// context, never an object, request, principal, cleaned data or file capability.
// The template must preserve the native control's name, value representation,
// accessibility links and disabled state. Common labels, choice-search controls,
// file clear/reselection and errors remain owned by Admin.
type WidgetTemplate struct {
	Name     string
	Widget   forms.Widget
	Template string
}

// WidgetConfig compiles and snapshots a bounded template filesystem at startup.
// There are no per-request filesystem reads, mutable loaders or Go callbacks.
// Names are selected by forms.WithWidgetTemplate or its model-form override.
type WidgetConfig struct {
	Templates fs.FS
	Entries   []WidgetTemplate
}

type widgetRegistry struct {
	engine *templates.Engine
	byName map[string]WidgetTemplate
	budget int
}

func prepareWidgets(config WidgetConfig, registry Registry, limits templates.Limits) (widgetRegistry, error) {
	result := widgetRegistry{byName: make(map[string]WidgetTemplate), budget: limits.MaxOutputBytes}
	if result.budget == 0 {
		result.budget = templates.DefaultLimits().MaxOutputBytes
	}
	if len(config.Entries) > MaximumWidgetTemplates || (len(config.Entries) == 0) != (config.Templates == nil) || config.Templates != nil && reflect.ValueOf(config.Templates).Kind() == reflect.Pointer && reflect.ValueOf(config.Templates).IsNil() {
		return widgetRegistry{}, &ConfigError{Path: "site.widgets", Code: "invalid_configuration"}
	}
	if len(config.Entries) != 0 {
		engine, err := templates.New(config.Templates, templates.Config{Limits: limits})
		if err != nil {
			return widgetRegistry{}, &ConfigError{Path: "site.widgets.templates", Code: "invalid", Cause: err}
		}
		result.engine = engine
		for index, entry := range config.Entries {
			path := fmt.Sprintf("site.widgets.entries[%d]", index)
			// Use the field option's exact identifier and protected-widget rules.
			if _, err := forms.CharField("probe", forms.WithWidgetTemplate(entry.Name)); err != nil {
				return widgetRegistry{}, &ConfigError{Path: path + ".name", Code: "invalid", Cause: err}
			}
			if _, duplicate := result.byName[entry.Name]; duplicate {
				return widgetRegistry{}, &ConfigError{Path: path + ".name", Code: "duplicate"}
			}
			if entry.Widget < forms.TextInput || entry.Widget > forms.ClearableFileInput || entry.Widget == forms.PasswordInput || entry.Widget == forms.HiddenInput {
				return widgetRegistry{}, &ConfigError{Path: path + ".widget", Code: "invalid_or_protected"}
			}
			if !slices.Contains(engine.Names(), entry.Template) {
				return widgetRegistry{}, &ConfigError{Path: path + ".template", Code: "missing"}
			}
			result.byName[entry.Name] = entry
		}
	}
	check := func(path string, fields []forms.Field) error {
		for _, field := range fields {
			if field.WidgetTemplate() == "" {
				continue
			}
			entry, found := result.byName[field.WidgetTemplate()]
			if !found || entry.Widget != field.Widget() {
				return &ConfigError{Path: path + "." + field.Name() + ".widget_template", Code: "missing_or_incompatible"}
			}
		}
		return nil
	}
	for _, model := range registry.models {
		path := "site.models." + model.slug
		type fieldGroup struct {
			path   string
			fields []forms.Field
		}
		groups := []fieldGroup{{path + ".form", model.form.Fields()}, {path + ".create_form", model.createForm.Fields()}}
		for _, command := range model.commands {
			groups = append(groups, fieldGroup{path + ".commands." + command.name, command.form.Fields()})
		}
		for _, command := range model.collectionCommands {
			groups = append(groups, fieldGroup{path + ".collection_commands." + command.name, command.form.Fields()})
		}
		for _, inline := range model.inlines {
			groups = append(groups, fieldGroup{path + ".inlines." + inline.prefix, inline.fields})
		}
		for _, collection := range model.collectionFormSets {
			groups = append(groups, fieldGroup{path + ".collection_sets." + collection.name, collection.fields})
		}
		for _, group := range groups {
			if err := check(group.path, group.fields); err != nil {
				return widgetRegistry{}, err
			}
		}
	}
	return result, nil
}

// apply walks only Admin-owned field lists. Read-only computed values, navigation
// and model snapshots cannot acquire an input renderer by sharing object keys.
func (registry widgetRegistry) apply(ctx context.Context, page map[string]templates.Value) error {
	if registry.engine == nil {
		return ctx.Err()
	}
	remaining := registry.budget
	fields := func(value templates.Value) (templates.Value, error) {
		items, list := value.Items()
		if !list {
			return value, nil
		}
		for index, item := range items {
			if err := ctx.Err(); err != nil {
				return templates.Value{}, err
			}
			nameValue, _ := item.Member("widget_template")
			name, _ := nameValue.AsString()
			if name == "" {
				continue
			}
			entry, found := registry.byName[name]
			kindValue, _ := item.Member("widget_kind")
			kind, _ := kindValue.AsInteger()
			if !found || int64(entry.Widget) != kind {
				return templates.Value{}, &ConfigError{Path: "site.widgets.render", Code: "missing_or_incompatible"}
			}
			values, err := templates.NewContext(map[string]templates.Value{"field": item})
			if err != nil {
				return templates.Value{}, err
			}
			output, err := registry.engine.Render(ctx, entry.Template, values, templates.Capabilities{})
			if err != nil {
				return templates.Value{}, err
			}
			remaining -= len(output)
			if remaining < 0 {
				return templates.Value{}, &ConfigError{Path: "site.widgets.render", Code: "output_limit_exceeded"}
			}
			// Only the bounded engine output of trusted source bypasses escaping.
			// Every request-derived value in that engine was a plain String.
			items[index], err = widgetObjectWith(item, "custom_widget", templates.TrustedHTML(string(output)))
			if err != nil {
				return templates.Value{}, err
			}
		}
		return templates.List(items...), nil
	}
	if value, found := page["fields"]; found {
		result, err := fields(value)
		if err != nil {
			return err
		}
		page["fields"] = result
	}
	groups, _ := page["inlines"].Items()
	for index, group := range groups {
		for _, key := range []string{"rows", "empty"} {
			list, _ := group.Member(key)
			rows, _ := list.Items()
			for rowIndex, row := range rows {
				list, _ := row.Member("fields")
				result, err := fields(list)
				if err != nil {
					return err
				}
				rows[rowIndex], err = widgetObjectWith(row, "fields", result)
				if err != nil {
					return err
				}
			}
			var err error
			group, err = widgetObjectWith(group, key, templates.List(rows...))
			if err != nil {
				return err
			}
		}
		groups[index] = group
	}
	if len(groups) != 0 {
		page["inlines"] = templates.List(groups...)
	}
	return ctx.Err()
}

func widgetObjectWith(value templates.Value, key string, replacement templates.Value) (templates.Value, error) {
	members, ok := value.Members()
	if !ok {
		return templates.Value{}, &ConfigError{Path: "site.widgets.context", Code: "not_object"}
	}
	result := make(map[string]templates.Value, len(members)+1)
	for _, member := range members {
		result[member.Name()] = member.Value()
	}
	result[key] = replacement
	return templates.Object(result)
}
