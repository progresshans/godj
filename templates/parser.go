package templates

import (
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokenKind uint8

const (
	tokenText tokenKind = iota
	tokenVariable
	tokenTag
)

type token struct {
	kind   tokenKind
	text   string
	line   int
	column int
}

type nodeKind uint8

const (
	nodeText nodeKind = iota
	nodeVariable
	nodeIf
	nodeFor
	nodeInclude
	nodeExtends
	nodeBlock
	nodeURL
	nodeCSRF
)

type node struct {
	kind       nodeKind
	line       int
	column     int
	text       string
	name       string
	expression expression
	children   []*node
	alternate  []*node
}

type expression struct {
	path    []string
	filters []filter
	negate  bool
}

type filterKind uint8

const (
	filterDefault filterKind = iota + 1
	filterLength
	filterLower
)

type filter struct {
	kind filterKind
	arg  Value
}

type compiledTemplate struct {
	name    string
	nodes   []*node
	extends string
	blocks  map[string]*node
	refs    []string
}

func tokenize(name, source string) ([]token, error) {
	if !utf8.ValidString(source) {
		return nil, parseError(name, 1, 1, "invalid_utf8", nil)
	}
	if strings.ContainsRune(source, 0) {
		return nil, parseError(name, 1, 1, "nul_source", nil)
	}
	result := make([]token, 0)
	offset, line, column := 0, 1, 1
	for offset < len(source) {
		relative, opener := nextOpener(source[offset:])
		if relative < 0 {
			result = append(result, token{kind: tokenText, text: source[offset:], line: line, column: column})
			break
		}
		if relative > 0 {
			text := source[offset : offset+relative]
			result = append(result, token{kind: tokenText, text: text, line: line, column: column})
			advancePosition(text, &line, &column)
			offset += relative
		}
		tokenLine, tokenColumn := line, column
		closer := "}}"
		kind := tokenVariable
		switch opener {
		case "{%":
			closer = "%}"
			kind = tokenTag
		case "{#":
			closer = "#}"
		}
		closeRelative := strings.Index(source[offset+2:], closer)
		if closeRelative < 0 {
			return nil, parseError(name, tokenLine, tokenColumn, "unclosed_delimiter", nil)
		}
		end := offset + 2 + closeRelative + 2
		if opener != "{#" {
			result = append(result, token{
				kind:   kind,
				text:   strings.TrimSpace(source[offset+2 : offset+2+closeRelative]),
				line:   tokenLine,
				column: tokenColumn,
			})
		}
		advancePosition(source[offset:end], &line, &column)
		offset = end
	}
	return result, nil
}

func nextOpener(source string) (int, string) {
	index := -1
	opener := ""
	for _, candidate := range []string{"{{", "{%", "{#"} {
		found := strings.Index(source, candidate)
		if found >= 0 && (index < 0 || found < index) {
			index, opener = found, candidate
		}
	}
	return index, opener
}

func advancePosition(text string, line, column *int) {
	for _, r := range text {
		if r == '\n' {
			*line++
			*column = 1
		} else {
			*column++
		}
	}
}

type parser struct {
	name      string
	tokens    []token
	index     int
	limits    Limits
	nodeCount int
}

func parseTemplate(name, source string, limits Limits) (*compiledTemplate, int, error) {
	tokens, err := tokenize(name, source)
	if err != nil {
		return nil, 0, err
	}
	parser := &parser{name: name, tokens: tokens, limits: limits}
	nodes, stop, _, err := parser.parseNodes(nil, 1)
	if err != nil {
		return nil, 0, err
	}
	if stop != "" {
		return nil, 0, parseError(name, 0, 0, "unexpected_end_tag", nil)
	}
	compiled := &compiledTemplate{name: name, nodes: nodes, blocks: make(map[string]*node)}
	if err := inspectTemplate(compiled); err != nil {
		return nil, 0, err
	}
	return compiled, parser.nodeCount, nil
}

func (p *parser) parseNodes(stops map[string]struct{}, depth int) ([]*node, string, string, error) {
	if depth > p.limits.MaxParseDepth {
		return nil, "", "", parseError(p.name, 0, 0, "parse_depth_exceeded", nil)
	}
	nodes := make([]*node, 0)
	for p.index < len(p.tokens) {
		current := p.tokens[p.index]
		p.index++
		switch current.kind {
		case tokenText:
			if current.text != "" {
				item := &node{kind: nodeText, line: current.line, column: current.column, text: current.text}
				if err := p.addNode(item); err != nil {
					return nil, "", "", err
				}
				nodes = append(nodes, item)
			}
		case tokenVariable:
			expression, err := parseExpression(current.text, false)
			if err != nil {
				return nil, "", "", parseError(p.name, current.line, current.column, "invalid_expression", err)
			}
			item := &node{kind: nodeVariable, line: current.line, column: current.column, expression: expression}
			if err := p.addNode(item); err != nil {
				return nil, "", "", err
			}
			nodes = append(nodes, item)
		case tokenTag:
			command, rest := splitCommand(current.text)
			if command == "" {
				return nil, "", "", parseError(p.name, current.line, current.column, "empty_tag", nil)
			}
			if _, ok := stops[command]; ok {
				return nodes, command, rest, nil
			}
			item, err := p.parseTag(command, rest, current, depth)
			if err != nil {
				return nil, "", "", err
			}
			nodes = append(nodes, item)
		}
	}
	if len(stops) != 0 {
		return nil, "", "", parseError(p.name, 0, 0, "unclosed_block", nil)
	}
	return nodes, "", "", nil
}

func (p *parser) parseTag(command, rest string, current token, depth int) (*node, error) {
	positioned := func(kind nodeKind) *node {
		return &node{kind: kind, line: current.line, column: current.column}
	}
	switch command {
	case "if":
		expression, err := parseExpression(rest, true)
		if err != nil {
			return nil, parseError(p.name, current.line, current.column, "invalid_if", err)
		}
		item := positioned(nodeIf)
		item.expression = expression
		if err := p.addNode(item); err != nil {
			return nil, err
		}
		children, stop, stopRest, err := p.parseNodes(stopSet("else", "endif"), depth+1)
		if err != nil {
			return nil, err
		}
		if stopRest != "" {
			return nil, parseError(p.name, current.line, current.column, "invalid_end_tag", nil)
		}
		item.children = children
		if stop == "else" {
			alternate, final, finalRest, err := p.parseNodes(stopSet("endif"), depth+1)
			if err != nil {
				return nil, err
			}
			if final != "endif" || finalRest != "" {
				return nil, parseError(p.name, current.line, current.column, "invalid_endif", nil)
			}
			item.alternate = alternate
		}
		return item, nil
	case "for":
		parts := strings.Fields(rest)
		if len(parts) != 3 || parts[1] != "in" || !validIdentifier(parts[0]) {
			return nil, parseError(p.name, current.line, current.column, "invalid_for", nil)
		}
		expression, err := parseExpression(parts[2], false)
		if err != nil {
			return nil, parseError(p.name, current.line, current.column, "invalid_for", err)
		}
		item := positioned(nodeFor)
		item.name, item.expression = parts[0], expression
		if err := p.addNode(item); err != nil {
			return nil, err
		}
		children, stop, stopRest, err := p.parseNodes(stopSet("empty", "endfor"), depth+1)
		if err != nil {
			return nil, err
		}
		if stopRest != "" {
			return nil, parseError(p.name, current.line, current.column, "invalid_end_tag", nil)
		}
		item.children = children
		if stop == "empty" {
			alternate, final, finalRest, err := p.parseNodes(stopSet("endfor"), depth+1)
			if err != nil {
				return nil, err
			}
			if final != "endfor" || finalRest != "" {
				return nil, parseError(p.name, current.line, current.column, "invalid_endfor", nil)
			}
			item.alternate = alternate
		}
		return item, nil
	case "block":
		if !validIdentifier(rest) {
			return nil, parseError(p.name, current.line, current.column, "invalid_block_name", nil)
		}
		item := positioned(nodeBlock)
		item.name = rest
		if err := p.addNode(item); err != nil {
			return nil, err
		}
		children, stop, stopRest, err := p.parseNodes(stopSet("endblock"), depth+1)
		if err != nil {
			return nil, err
		}
		if stop != "endblock" || stopRest != "" && stopRest != rest {
			return nil, parseError(p.name, current.line, current.column, "invalid_endblock", nil)
		}
		item.children = children
		return item, nil
	case "extends", "include", "url":
		name, err := parseStringLiteral(rest)
		if err != nil || !validTemplateOrRouteName(name, command != "url") {
			return nil, parseError(p.name, current.line, current.column, "invalid_"+command, err)
		}
		kind := nodeInclude
		if command == "extends" {
			kind = nodeExtends
		} else if command == "url" {
			kind = nodeURL
		}
		item := positioned(kind)
		item.name = name
		if err := p.addNode(item); err != nil {
			return nil, err
		}
		return item, nil
	case "csrf_token":
		if rest != "" {
			return nil, parseError(p.name, current.line, current.column, "invalid_csrf_token", nil)
		}
		item := positioned(nodeCSRF)
		if err := p.addNode(item); err != nil {
			return nil, err
		}
		return item, nil
	default:
		return nil, parseError(p.name, current.line, current.column, "unknown_tag", nil)
	}
}

func (p *parser) addNode(item *node) error {
	p.nodeCount++
	if p.nodeCount > p.limits.MaxParseNodes {
		return parseError(p.name, item.line, item.column, "parse_nodes_exceeded", nil)
	}
	return nil
}

func inspectTemplate(compiled *compiledTemplate) error {
	semanticIndex := 0
	extendsIndex := -1
	for index, item := range compiled.nodes {
		if item.kind == nodeText && strings.TrimSpace(item.text) == "" {
			continue
		}
		if item.kind == nodeExtends {
			if compiled.extends != "" {
				return parseError(compiled.name, item.line, item.column, "duplicate_extends", nil)
			}
			compiled.extends = item.name
			extendsIndex = semanticIndex
			compiled.refs = append(compiled.refs, item.name)
		}
		semanticIndex++
		_ = index
	}
	if compiled.extends != "" {
		if extendsIndex != 0 {
			return parseError(compiled.name, 0, 0, "extends_not_first", nil)
		}
		for _, item := range compiled.nodes {
			if item.kind == nodeText && strings.TrimSpace(item.text) == "" || item.kind == nodeExtends || item.kind == nodeBlock {
				continue
			}
			return parseError(compiled.name, item.line, item.column, "content_outside_extending_block", nil)
		}
	}
	if err := inspectNodes(compiled, compiled.nodes); err != nil {
		return err
	}
	return nil
}

func inspectNodes(compiled *compiledTemplate, nodes []*node) error {
	for _, item := range nodes {
		switch item.kind {
		case nodeBlock:
			if _, duplicate := compiled.blocks[item.name]; duplicate {
				return parseError(compiled.name, item.line, item.column, "duplicate_block", nil)
			}
			compiled.blocks[item.name] = item
		case nodeInclude:
			compiled.refs = append(compiled.refs, item.name)
		}
		if err := inspectNodes(compiled, item.children); err != nil {
			return err
		}
		if err := inspectNodes(compiled, item.alternate); err != nil {
			return err
		}
	}
	return nil
}

func parseExpression(source string, allowNot bool) (expression, error) {
	source = strings.TrimSpace(source)
	result := expression{}
	if allowNot && strings.HasPrefix(source, "not ") {
		result.negate = true
		source = strings.TrimSpace(strings.TrimPrefix(source, "not "))
	}
	parts, err := splitUnquoted(source, '|')
	if err != nil || len(parts) == 0 {
		return expression{}, fmt.Errorf("empty expression")
	}
	path := strings.Split(strings.TrimSpace(parts[0]), ".")
	if len(path) == 0 {
		return expression{}, fmt.Errorf("empty path")
	}
	for index, segment := range path {
		if index > 0 {
			if numeric, err := strconv.Atoi(segment); err == nil && numeric >= 0 && segment != "" {
				continue
			}
		}
		if !validIdentifier(segment) {
			return expression{}, fmt.Errorf("invalid path segment")
		}
	}
	result.path = append([]string(nil), path...)
	for _, raw := range parts[1:] {
		name, argument, hasArgument, err := splitFilter(strings.TrimSpace(raw))
		if err != nil {
			return expression{}, err
		}
		switch name {
		case "default":
			if !hasArgument {
				return expression{}, fmt.Errorf("default requires an argument")
			}
			value, err := parseLiteral(argument)
			if err != nil {
				return expression{}, err
			}
			result.filters = append(result.filters, filter{kind: filterDefault, arg: value})
		case "length":
			if hasArgument {
				return expression{}, fmt.Errorf("length rejects arguments")
			}
			result.filters = append(result.filters, filter{kind: filterLength})
		case "lower":
			if hasArgument {
				return expression{}, fmt.Errorf("lower rejects arguments")
			}
			result.filters = append(result.filters, filter{kind: filterLower})
		default:
			return expression{}, fmt.Errorf("unknown filter %q", name)
		}
	}
	return result, nil
}

func splitFilter(source string) (string, string, bool, error) {
	parts, err := splitUnquoted(source, ':')
	if err != nil || len(parts) > 2 || len(parts) == 0 {
		return "", "", false, fmt.Errorf("invalid filter")
	}
	name := strings.TrimSpace(parts[0])
	if !validIdentifier(name) {
		return "", "", false, fmt.Errorf("invalid filter name")
	}
	if len(parts) == 1 {
		return name, "", false, nil
	}
	return name, strings.TrimSpace(parts[1]), true, nil
}

func splitUnquoted(source string, separator rune) ([]string, error) {
	parts := make([]string, 0, 2)
	start := 0
	quote := rune(0)
	escaped := false
	for index, current := range source {
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' && quote != 0 {
			escaped = true
			continue
		}
		if current == '\'' || current == '"' {
			if quote == 0 {
				quote = current
			} else if quote == current {
				quote = 0
			}
			continue
		}
		if current == separator && quote == 0 {
			parts = append(parts, source[start:index])
			start = index + utf8.RuneLen(current)
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("unclosed quote")
	}
	parts = append(parts, source[start:])
	return parts, nil
}

func parseLiteral(source string) (Value, error) {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "\"") || strings.HasPrefix(source, "'") {
		value, err := parseStringLiteral(source)
		if err != nil {
			return Value{}, err
		}
		return String(value), nil
	}
	switch source {
	case "true", "True":
		return Bool(true), nil
	case "false", "False":
		return Bool(false), nil
	case "null", "None":
		return Null(), nil
	}
	integer, err := strconv.ParseInt(source, 10, 64)
	if err != nil {
		return Value{}, fmt.Errorf("invalid literal")
	}
	return Integer(integer), nil
}

func parseStringLiteral(source string) (string, error) {
	source = strings.TrimSpace(source)
	if len(source) < 2 || source[0] != source[len(source)-1] || source[0] != '\'' && source[0] != '"' {
		return "", fmt.Errorf("expected quoted literal")
	}
	quote := source[0]
	var builder strings.Builder
	for index := 1; index < len(source)-1; index++ {
		current := source[index]
		if current == '\\' {
			index++
			if index >= len(source)-1 {
				return "", fmt.Errorf("invalid escape")
			}
			escaped := source[index]
			switch escaped {
			case '\\', quote:
				builder.WriteByte(escaped)
			case 'n':
				builder.WriteByte('\n')
			case 't':
				builder.WriteByte('\t')
			default:
				return "", fmt.Errorf("unsupported escape")
			}
			continue
		}
		if current == quote {
			return "", fmt.Errorf("unescaped quote")
		}
		builder.WriteByte(current)
	}
	value := builder.String()
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("invalid literal")
	}
	return value, nil
}

func splitCommand(source string) (string, string) {
	source = strings.TrimSpace(source)
	index := strings.IndexAny(source, " \t\r\n")
	if index < 0 {
		return source, ""
	}
	return source[:index], strings.TrimSpace(source[index:])
}

func stopSet(names ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
}

func validTemplateOrRouteName(name string, template bool) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return false
	}
	if template {
		return fs.ValidPath(name) && name != "." && path.Clean(name) == name
	}
	if !utf8.ValidString(name) {
		return false
	}
	for _, current := range name {
		if unicode.IsLetter(current) || unicode.IsDigit(current) || strings.ContainsRune("_:-./", current) {
			continue
		}
		return false
	}
	return true
}

func parseError(name string, line, column int, code string, cause error) error {
	return &Error{Phase: "parse", Code: code, Template: name, Line: line, Column: column, Cause: cause}
}
