package forms

// InlineParentField carries a server-owned integer parent identity. Empty or
// omitted input resolves to that identity; a nonempty value must match its
// canonical spelling. NULL denotes a parent whose key has not been assigned.
// This hidden field never counts as an editable change. The inline model layer
// must also check its raw value on deleted and unchanged optional rows.
func InlineParentField(name string, parent Value) (Field, error) {
	if !parent.IsNull() && parent.Kind() != ValueInteger {
		return Field{}, &ConfigError{Path: "fields." + name, Code: "invalid_parent"}
	}
	field, err := IntegerField(name, WithRequired(false), WithNullable(), WithDefault(parent), WithWidget(HiddenInput))
	if err != nil {
		return Field{}, err
	}
	field.inlineParent = true
	return field, nil
}

// InlineParent returns the server identity for display, never submitted input.
func (field Field) InlineParent() (Value, bool) {
	return field.defaultValue, field.inlineParent
}
