package templates

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"unicode/utf8"
)

type boundedOutput struct {
	bytes []byte
	limit int
}

func (output *boundedOutput) append(value string) error {
	if len(value) > output.limit-len(output.bytes) {
		return fmt.Errorf("output limit exceeded")
	}
	output.bytes = append(output.bytes, value...)
	return nil
}

type blockOverride struct {
	owner string
	node  *node
}

type renderState struct {
	engine       *Engine
	capabilities Capabilities
	output       boundedOutput
	loopItems    int
	stack        []string
}

func (state *renderState) renderTemplate(
	ctx context.Context,
	name string,
	values Context,
	overrides map[string]blockOverride,
	depth int,
) error {
	if err := ctx.Err(); err != nil {
		return renderError(name, 0, 0, "context_canceled", err)
	}
	if depth > state.engine.limits.MaxRenderDepth {
		return renderError(name, 0, 0, "render_depth_exceeded", nil)
	}
	for _, active := range state.stack {
		if active == name {
			return renderError(name, 0, 0, "template_cycle", nil)
		}
	}
	template, ok := state.engine.templates[name]
	if !ok {
		return renderError(name, 0, 0, "unknown_template", nil)
	}
	state.stack = append(state.stack, name)
	defer func() { state.stack = state.stack[:len(state.stack)-1] }()

	merged := make(map[string]blockOverride, len(template.blocks)+len(overrides))
	for blockName, override := range overrides {
		merged[blockName] = override
	}
	for blockName, block := range template.blocks {
		if _, exists := merged[blockName]; !exists {
			merged[blockName] = blockOverride{owner: name, node: block}
		}
	}
	if template.extends != "" {
		return state.renderTemplate(ctx, template.extends, values, merged, depth+1)
	}
	return state.renderNodes(ctx, name, template.nodes, values, merged, depth)
}

func (state *renderState) renderNodes(
	ctx context.Context,
	owner string,
	nodes []*node,
	values Context,
	overrides map[string]blockOverride,
	depth int,
) error {
	for _, item := range nodes {
		if err := ctx.Err(); err != nil {
			return renderError(owner, item.line, item.column, "context_canceled", err)
		}
		switch item.kind {
		case nodeText:
			if err := state.output.append(item.text); err != nil {
				return renderError(owner, item.line, item.column, "output_exceeded", nil)
			}
		case nodeVariable:
			value, err := evaluate(item.expression, values)
			if err != nil {
				return renderError(owner, item.line, item.column, "evaluation_failed", err)
			}
			if err := state.renderValue(owner, item, value); err != nil {
				return err
			}
		case nodeIf:
			value, err := evaluate(item.expression, values)
			if err != nil {
				return renderError(owner, item.line, item.column, "evaluation_failed", err)
			}
			branch := item.alternate
			if value.truth() {
				branch = item.children
			}
			if err := state.renderNodes(ctx, owner, branch, values, overrides, depth); err != nil {
				return err
			}
		case nodeFor:
			value, err := evaluate(item.expression, values)
			if err != nil {
				return renderError(owner, item.line, item.column, "evaluation_failed", err)
			}
			if value.kind == ValueNull || value.kind == ValueList && len(value.list) == 0 {
				if err := state.renderNodes(ctx, owner, item.alternate, values, overrides, depth); err != nil {
					return err
				}
				continue
			}
			if value.kind != ValueList {
				return renderError(owner, item.line, item.column, "for_value_not_list", nil)
			}
			if len(value.list) > state.engine.limits.MaxLoopItems-state.loopItems {
				return renderError(owner, item.line, item.column, "loop_items_exceeded", nil)
			}
			state.loopItems += len(value.list)
			for index, value := range value.list {
				if err := ctx.Err(); err != nil {
					return renderError(owner, item.line, item.column, "context_canceled", err)
				}
				loop := Value{kind: ValueObject, object: map[string]Value{
					"counter":  Integer(int64(index + 1)),
					"counter0": Integer(int64(index)),
					"first":    Bool(index == 0),
					"last":     Bool(index == len(value.list)-1),
				}}
				nested := values.with(item.name, value).with("forloop", loop)
				if err := state.renderNodes(ctx, owner, item.children, nested, overrides, depth); err != nil {
					return err
				}
			}
		case nodeInclude:
			if err := state.renderTemplate(ctx, item.name, values, nil, depth+1); err != nil {
				return err
			}
		case nodeExtends:
			// Extends is consumed before root rendering. Reaching one here would
			// indicate an invalid compiled template and must fail closed.
			return renderError(owner, item.line, item.column, "unexpected_extends", nil)
		case nodeBlock:
			override, ok := overrides[item.name]
			if ok && override.node != item {
				if err := state.renderNodes(ctx, override.owner, override.node.children, values, overrides, depth); err != nil {
					return err
				}
			} else if err := state.renderNodes(ctx, owner, item.children, values, overrides, depth); err != nil {
				return err
			}
		case nodeURL:
			if state.capabilities.URL == nil {
				return renderError(owner, item.line, item.column, "url_capability_missing", nil)
			}
			resolved, err := state.capabilities.URL.Reverse(ctx, item.name)
			if err != nil {
				return renderError(owner, item.line, item.column, "url_reverse_failed", ctx.Err())
			}
			if !validTextValue(resolved) {
				return renderError(owner, item.line, item.column, "url_invalid", nil)
			}
			if err := state.renderValue(owner, item, String(resolved)); err != nil {
				return err
			}
		case nodeCSRF:
			if state.capabilities.CSRF == nil {
				return renderError(owner, item.line, item.column, "csrf_capability_missing", nil)
			}
			token, err := state.capabilities.CSRF.Token(ctx)
			if err != nil {
				return renderError(owner, item.line, item.column, "csrf_token_failed", ctx.Err())
			}
			if !validTextValue(token) {
				return renderError(owner, item.line, item.column, "csrf_token_invalid", nil)
			}
			markup := `<input type="hidden" name="csrfmiddlewaretoken" value="` + html.EscapeString(token) + `">`
			if err := state.output.append(markup); err != nil {
				return renderError(owner, item.line, item.column, "output_exceeded", nil)
			}
		default:
			return renderError(owner, item.line, item.column, "unknown_node", nil)
		}
	}
	return nil
}

func (state *renderState) renderValue(owner string, item *node, value Value) error {
	var rendered string
	switch value.kind {
	case ValueNull:
		return nil
	case ValueString:
		rendered = html.EscapeString(value.text)
	case ValueSafeHTML:
		rendered = value.text
	case ValueBoolean:
		if value.boolean {
			rendered = "True"
		} else {
			rendered = "False"
		}
	case ValueInteger:
		rendered = strconv.FormatInt(value.integer, 10)
	default:
		return renderError(owner, item.line, item.column, "unrenderable_value", nil)
	}
	if err := state.output.append(rendered); err != nil {
		return renderError(owner, item.line, item.column, "output_exceeded", nil)
	}
	return nil
}

func evaluate(expression expression, context Context) (Value, error) {
	value, ok := context.values[expression.path[0]]
	if !ok {
		value = Null()
	}
	for _, segment := range expression.path[1:] {
		switch value.kind {
		case ValueObject:
			resolved, ok := value.object[segment]
			if !ok {
				value = Null()
			} else {
				value = resolved
			}
		case ValueList:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(value.list) {
				value = Null()
			} else {
				value = value.list[index]
			}
		default:
			value = Null()
		}
	}
	for _, current := range expression.filters {
		switch current.kind {
		case filterDefault:
			if !value.truth() {
				value = current.arg
			}
		case filterLength:
			switch value.kind {
			case ValueString, ValueSafeHTML:
				value = Integer(int64(utf8.RuneCountInString(value.text)))
			case ValueList:
				value = Integer(int64(len(value.list)))
			case ValueObject:
				value = Integer(int64(len(value.object)))
			default:
				value = Integer(0)
			}
		case filterLower:
			switch value.kind {
			case ValueNull:
				value = String("")
			case ValueString, ValueSafeHTML:
				value = String(strings.ToLower(value.text))
			default:
				return Value{}, fmt.Errorf("lower requires a string")
			}
		default:
			return Value{}, fmt.Errorf("unknown filter")
		}
	}
	if expression.negate {
		return Bool(!value.truth()), nil
	}
	return value, nil
}

func renderError(name string, line, column int, code string, cause error) error {
	return &Error{Phase: "render", Code: code, Template: name, Line: line, Column: column, Cause: cause}
}
