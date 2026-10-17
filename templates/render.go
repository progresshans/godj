package templates

import (
	"context"
	"fmt"
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

// appendEscaped uses html.EscapeString's five substitutions without building
// an intermediate string. An output failure never publishes the partial buffer.
func (output *boundedOutput) appendEscaped(value string) error {
	if len(value) > output.limit-len(output.bytes) {
		return fmt.Errorf("output limit exceeded")
	}
	for {
		index := strings.IndexAny(value, "&'<>\"")
		if index < 0 {
			return output.append(value)
		}
		if err := output.append(value[:index]); err != nil {
			return err
		}
		var escaped string
		switch value[index] {
		case '&':
			escaped = "&amp;"
		case '\'':
			escaped = "&#39;"
		case '<':
			escaped = "&lt;"
		case '>':
			escaped = "&gt;"
		case '"':
			escaped = "&#34;"
		}
		if err := output.append(escaped); err != nil {
			return err
		}
		value = value[index+1:]
	}
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
}

func (state *renderState) renderTemplate(
	ctx context.Context,
	name string,
	values Context,
	depth int,
) error {
	if err := ctx.Err(); err != nil {
		return renderError(name, 0, 0, "context_canceled", err)
	}
	if depth > state.engine.limits.MaxRenderDepth {
		return renderError(name, 0, 0, "render_depth_exceeded", nil)
	}
	template, ok := state.engine.templates[name]
	if !ok {
		return renderError(name, 0, 0, "unknown_template", nil)
	}
	overrides := template.overrides
	for template.parent != nil {
		template = template.parent
		depth++
		if err := ctx.Err(); err != nil {
			return renderError(template.name, 0, 0, "context_canceled", err)
		}
		if depth > state.engine.limits.MaxRenderDepth {
			return renderError(template.name, 0, 0, "render_depth_exceeded", nil)
		}
	}
	return state.renderNodes(ctx, template.name, template.nodes, values, overrides, depth)
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
			items := value.list
			state.loopItems += len(items)
			// This frame is private to one synchronous loop invocation. Includes
			// borrow it while rendering; nested loops allocate their own frame.
			loop := Value{kind: ValueObject, object: make(map[string]Value, 4)}
			nested := values
			nested.scope = &loopScope{parent: values.scope, name: item.name, loop: loop}
			for index, value := range items {
				if err := ctx.Err(); err != nil {
					return renderError(owner, item.line, item.column, "context_canceled", err)
				}
				loop.object["counter"] = Integer(int64(index + 1))
				loop.object["counter0"] = Integer(int64(index))
				loop.object["first"] = Bool(index == 0)
				loop.object["last"] = Bool(index == len(items)-1)
				nested.scope.value = value
				if err := state.renderNodes(ctx, owner, item.children, nested, overrides, depth); err != nil {
					return err
				}
			}
		case nodeInclude:
			if err := state.renderTemplate(ctx, item.name, values, depth+1); err != nil {
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
			err = state.output.append(`<input type="hidden" name="csrfmiddlewaretoken" value="`)
			if err == nil {
				err = state.output.appendEscaped(token)
			}
			if err == nil {
				err = state.output.append(`">`)
			}
			if err != nil {
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
		if err := state.output.appendEscaped(value.text); err != nil {
			return renderError(owner, item.line, item.column, "output_exceeded", nil)
		}
		return nil
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
	value, ok := context.lookup(expression.path[0])
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
