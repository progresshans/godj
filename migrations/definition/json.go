package definition

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

type sourceSnapshot struct {
	sourceID string
	rawID    []byte
	document []byte
}

type jsonKind uint8

const (
	jsonNull jsonKind = iota + 1
	jsonBoolean
	jsonString
	jsonNumber
	jsonArray
	jsonObject
)

type jsonMember struct {
	key   string
	value jsonValue
}

type jsonValue struct {
	kind    jsonKind
	boolean bool
	string  string
	number  string
	array   []jsonValue
	object  []jsonMember
}

func (value jsonValue) member(name string) (jsonValue, bool) {
	for _, member := range value.object {
		if member.key == name {
			return member.value, true
		}
	}
	return jsonValue{}, false
}

type jsonScanStats struct {
	Values   uint64
	Complete bool
}

// jsonPath shares ancestor tokens between parser frames and failure
// candidates. Rendering only the single canonical winner prevents an attacker
// from multiplying one long ancestor key by every duplicate/lone-scalar
// candidate in the document.
type jsonPath struct {
	parent *jsonPath
	token  string
}

type jsonFailureCandidate struct {
	failure failureCandidate
	path    *jsonPath
}

type jsonParser struct {
	data            []byte
	position        int
	sourceID        string
	values          uint64
	buildTree       bool
	resourceFailure *jsonFailureCandidate
	regularFailure  *jsonFailureCandidate
}

func scanJSONDocument(source sourceSnapshot) (jsonValue, jsonScanStats, []failureCandidate) {
	validUTF8 := utf8.Valid(source.document)

	parser := jsonParser{
		data:      source.document,
		sourceID:  source.sourceID,
		buildTree: true,
	}
	parser.skipWhitespace()
	root, err := parser.parseValue(nil, 0)
	if err != nil {
		reason := "syntax"
		if !validUTF8 {
			reason = "invalid_utf8"
		}
		parser.recordFailure(jsonFailureCandidate{failure: documentFailure(source.sourceID, "", reason)})
		return jsonValue{}, jsonScanStats{Values: parser.values}, parser.failures()
	}

	parser.skipWhitespace()
	if parser.position != len(parser.data) {
		// A strict document has one root. Continue scanning complete trailing
		// values only to make the earlier depth/value resource stage observable;
		// the regular framing result remains trailing_value.
		parser.buildTree = false
		for parser.position < len(parser.data) {
			if _, trailingErr := parser.parseValue(nil, 0); trailingErr != nil {
				break
			}
			parser.skipWhitespace()
		}
		reason := "trailing_value"
		if !validUTF8 {
			reason = "invalid_utf8"
		}
		parser.recordFailure(jsonFailureCandidate{failure: documentFailure(source.sourceID, "", reason)})
		return jsonValue{}, jsonScanStats{Values: parser.values}, parser.failures()
	}

	complete := parser.resourceFailure == nil
	return root, jsonScanStats{Values: parser.values, Complete: complete}, parser.failures()
}

func (p *jsonParser) failures() []failureCandidate {
	if p.resourceFailure != nil {
		return []failureCandidate{materializeJSONFailure(*p.resourceFailure)}
	}
	if p.regularFailure != nil {
		return []failureCandidate{materializeJSONFailure(*p.regularFailure)}
	}
	return nil
}

func (p *jsonParser) recordFailure(candidate jsonFailureCandidate) {
	target := &p.regularFailure
	if candidate.failure.context.Reason == resourceLimitReason {
		target = &p.resourceFailure
	}
	if *target == nil || lessJSONFailure(candidate, **target) {
		winner := candidate
		*target = &winner
	}
}

func lessJSONFailure(left, right jsonFailureCandidate) bool {
	leftResource := left.failure.context.Reason == resourceLimitReason
	rightResource := right.failure.context.Reason == resourceLimitReason
	if leftResource != rightResource {
		return leftResource
	}
	if leftResource {
		leftRank := failureLimitRank(left.failure.context.Limit)
		rightRank := failureLimitRank(right.failure.context.Limit)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
	}
	if pointerOrder := compareJSONPaths(left.path, right.path); pointerOrder != 0 {
		return pointerOrder < 0
	}
	if !leftResource {
		leftRank := failureReasonRank("document", left.failure.context.Reason)
		rightRank := failureReasonRank("document", right.failure.context.Reason)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
	}
	if left.failure.code != right.failure.code {
		return left.failure.code < right.failure.code
	}
	if left.failure.context.Limit != right.failure.context.Limit {
		return left.failure.context.Limit < right.failure.context.Limit
	}
	if left.failure.context.Maximum != right.failure.context.Maximum {
		return left.failure.context.Maximum < right.failure.context.Maximum
	}
	return left.failure.context.Actual < right.failure.context.Actual
}

func materializeJSONFailure(candidate jsonFailureCandidate) failureCandidate {
	materialized := candidate.failure
	materialized.context.JSONPointer = renderJSONPath(candidate.path)
	return materialized
}

func childJSONPath(parent *jsonPath, token string) *jsonPath {
	return &jsonPath{parent: parent, token: token}
}

func compareJSONPaths(left, right *jsonPath) int {
	if left == right {
		return 0
	}
	leftNodes := jsonPathNodes(left)
	rightNodes := jsonPathNodes(right)
	common := 0
	for common < len(leftNodes) && common < len(rightNodes) && leftNodes[common] == rightNodes[common] {
		common++
	}
	return compareJSONPathNodeStreams(leftNodes[common:], rightNodes[common:])
}

// compareJSONPathNodeStreams compares RFC 6901 pointer bytes without
// materializing either complete pointer. This matters for hostile documents:
// a long canonical winner may be compared with tens of thousands of shorter
// candidates, and repeatedly rendering the winner would multiply bounded
// input bytes into unbounded transient allocation.
func compareJSONPathNodeStreams(left, right []*jsonPath) int {
	leftIterator := jsonPointerByteIterator{nodes: left}
	rightIterator := jsonPointerByteIterator{nodes: right}
	for {
		leftByte, leftOK := leftIterator.next()
		rightByte, rightOK := rightIterator.next()
		switch {
		case !leftOK && !rightOK:
			return 0
		case !leftOK:
			return -1
		case !rightOK:
			return 1
		case leftByte < rightByte:
			return -1
		case leftByte > rightByte:
			return 1
		}
	}
}

type jsonPointerByteIterator struct {
	nodes          []*jsonPath
	nodeIndex      int
	tokenOffset    int
	segmentStarted bool
	escapeSecond   byte
}

func (iterator *jsonPointerByteIterator) next() (byte, bool) {
	for iterator.nodeIndex < len(iterator.nodes) {
		if !iterator.segmentStarted {
			iterator.segmentStarted = true
			return '/', true
		}
		if iterator.escapeSecond != 0 {
			value := iterator.escapeSecond
			iterator.escapeSecond = 0
			iterator.tokenOffset++
			return value, true
		}

		token := iterator.nodes[iterator.nodeIndex].token
		if iterator.tokenOffset < len(token) {
			switch token[iterator.tokenOffset] {
			case '~':
				iterator.escapeSecond = '0'
				return '~', true
			case '/':
				iterator.escapeSecond = '1'
				return '~', true
			default:
				value := token[iterator.tokenOffset]
				iterator.tokenOffset++
				return value, true
			}
		}

		iterator.nodeIndex++
		iterator.tokenOffset = 0
		iterator.segmentStarted = false
	}
	return 0, false
}

func jsonPathNodes(path *jsonPath) []*jsonPath {
	depth := 0
	for current := path; current != nil; current = current.parent {
		depth++
	}
	nodes := make([]*jsonPath, depth)
	for current := path; current != nil; current = current.parent {
		depth--
		nodes[depth] = current
	}
	return nodes
}

func renderJSONPath(path *jsonPath) string {
	return renderJSONPathNodes(jsonPathNodes(path))
}

func renderJSONPathNodes(nodes []*jsonPath) string {
	var builder strings.Builder
	for _, node := range nodes {
		builder.WriteByte('/')
		for _, current := range []byte(node.token) {
			switch current {
			case '~':
				builder.WriteString("~0")
			case '/':
				builder.WriteString("~1")
			default:
				builder.WriteByte(current)
			}
		}
	}
	return builder.String()
}

func (p *jsonParser) parseValue(pointer *jsonPath, depth int) (jsonValue, error) {
	if p.position >= len(p.data) {
		return jsonValue{}, errors.New("unexpected end of JSON")
	}

	switch p.data[p.position] {
	case '{':
		p.countValue()
		if depth+1 > MaxJSONDepth {
			p.recordDepthFailure(pointer, depth+1)
			if err := p.skipOverDepthContainer(); err != nil {
				return jsonValue{}, err
			}
			return jsonValue{kind: jsonObject}, nil
		}
		return p.parseObject(pointer, depth+1)
	case '[':
		p.countValue()
		if depth+1 > MaxJSONDepth {
			p.recordDepthFailure(pointer, depth+1)
			if err := p.skipOverDepthContainer(); err != nil {
				return jsonValue{}, err
			}
			return jsonValue{kind: jsonArray}, nil
		}
		return p.parseArray(pointer, depth+1)
	case '"':
		decoded, err := p.parseString(pointer)
		if err != nil {
			return jsonValue{}, err
		}
		p.countValue()
		return jsonValue{kind: jsonString, string: decoded}, nil
	case 't':
		if !p.consumeLiteral("true") {
			return jsonValue{}, errors.New("invalid true literal")
		}
		p.countValue()
		return jsonValue{kind: jsonBoolean, boolean: true}, nil
	case 'f':
		if !p.consumeLiteral("false") {
			return jsonValue{}, errors.New("invalid false literal")
		}
		p.countValue()
		return jsonValue{kind: jsonBoolean}, nil
	case 'n':
		if !p.consumeLiteral("null") {
			return jsonValue{}, errors.New("invalid null literal")
		}
		p.countValue()
		return jsonValue{kind: jsonNull}, nil
	default:
		lexeme, integer, err := p.parseNumber()
		if err != nil {
			return jsonValue{}, err
		}
		if !integer {
			p.recordFailure(jsonFailureCandidate{
				failure: documentFailure(p.sourceID, "", "wrong_type"),
				path:    pointer,
			})
		}
		p.countValue()
		return jsonValue{kind: jsonNumber, number: lexeme}, nil
	}
}

func (p *jsonParser) parseObject(pointer *jsonPath, depth int) (jsonValue, error) {
	p.position++
	p.skipWhitespace()
	members := make([]jsonMember, 0)
	seen := make(map[string]*jsonPath)
	if p.consumeByte('}') {
		return jsonValue{kind: jsonObject, object: members}, nil
	}

	for {
		if p.position >= len(p.data) || p.data[p.position] != '"' {
			return jsonValue{}, errors.New("object key is not a string")
		}
		key, err := p.parseString(pointer)
		if err != nil {
			return jsonValue{}, err
		}
		memberPointer := childJSONPath(pointer, key)
		firstPointer, duplicate := seen[key]
		if duplicate {
			memberPointer = firstPointer
		} else {
			seen[key] = memberPointer
		}
		p.skipWhitespace()
		if !p.consumeByte(':') {
			return jsonValue{}, errors.New("object member has no colon")
		}
		p.skipWhitespace()
		child, err := p.parseValue(memberPointer, depth)
		if err != nil {
			return jsonValue{}, err
		}

		if p.buildTree {
			if duplicate {
				p.recordFailure(jsonFailureCandidate{
					failure: documentFailure(p.sourceID, "", "duplicate_key"),
					path:    memberPointer,
				})
			} else {
				members = append(members, jsonMember{key: key, value: child})
			}
		}

		p.skipWhitespace()
		if p.consumeByte('}') {
			return jsonValue{kind: jsonObject, object: members}, nil
		}
		if !p.consumeByte(',') {
			return jsonValue{}, errors.New("object member has no comma")
		}
		p.skipWhitespace()
	}
}

func (p *jsonParser) parseArray(pointer *jsonPath, depth int) (jsonValue, error) {
	p.position++
	p.skipWhitespace()
	values := make([]jsonValue, 0)
	if p.consumeByte(']') {
		return jsonValue{kind: jsonArray, array: values}, nil
	}

	for index := 0; ; index++ {
		child, err := p.parseValue(childJSONPath(pointer, strconv.Itoa(index)), depth)
		if err != nil {
			return jsonValue{}, err
		}
		if p.buildTree {
			values = append(values, child)
		}
		p.skipWhitespace()
		if p.consumeByte(']') {
			return jsonValue{kind: jsonArray, array: values}, nil
		}
		if !p.consumeByte(',') {
			return jsonValue{}, errors.New("array element has no comma")
		}
		p.skipWhitespace()
	}
}

func (p *jsonParser) parseString(pointer *jsonPath) (string, error) {
	if !p.consumeByte('"') {
		return "", errors.New("missing string quote")
	}
	var builder strings.Builder
	for p.position < len(p.data) {
		current := p.data[p.position]
		if current == '"' {
			p.position++
			return builder.String(), nil
		}
		if current < 0x20 {
			return "", errors.New("unescaped control character")
		}
		if current != '\\' {
			runeValue, size := utf8.DecodeRune(p.data[p.position:])
			if runeValue == utf8.RuneError && size == 1 {
				return "", errors.New("invalid UTF-8 in string")
			}
			builder.Write(p.data[p.position : p.position+size])
			p.position += size
			continue
		}

		p.position++
		if p.position >= len(p.data) {
			return "", errors.New("unfinished string escape")
		}
		escape := p.data[p.position]
		p.position++
		switch escape {
		case '"', '\\', '/':
			builder.WriteByte(escape)
		case 'b':
			builder.WriteByte('\b')
		case 'f':
			builder.WriteByte('\f')
		case 'n':
			builder.WriteByte('\n')
		case 'r':
			builder.WriteByte('\r')
		case 't':
			builder.WriteByte('\t')
		case 'u':
			unit, err := p.parseHexUnit()
			if err != nil {
				return "", err
			}
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if p.position+6 <= len(p.data) && p.data[p.position] == '\\' && p.data[p.position+1] == 'u' {
					low, lowErr := parseHex4(p.data[p.position+2 : p.position+6])
					if lowErr == nil && low >= 0xdc00 && low <= 0xdfff {
						p.position += 6
						builder.WriteRune(rune(0x10000 + (int(unit)-0xd800)*0x400 + int(low) - 0xdc00))
						continue
					}
				}
				p.recordFailure(jsonFailureCandidate{
					failure: documentFailure(p.sourceID, "", "lone_surrogate"),
					path:    pointer,
				})
				builder.WriteRune(utf8.RuneError)
			case unit >= 0xdc00 && unit <= 0xdfff:
				p.recordFailure(jsonFailureCandidate{
					failure: documentFailure(p.sourceID, "", "lone_surrogate"),
					path:    pointer,
				})
				builder.WriteRune(utf8.RuneError)
			default:
				builder.WriteRune(rune(unit))
			}
		default:
			return "", errors.New("unknown string escape")
		}
	}
	return "", errors.New("unterminated string")
}

func (p *jsonParser) parseHexUnit() (uint16, error) {
	if p.position+4 > len(p.data) {
		return 0, errors.New("short Unicode escape")
	}
	unit, err := parseHex4(p.data[p.position : p.position+4])
	if err != nil {
		return 0, err
	}
	p.position += 4
	return unit, nil
}

func parseHex4(value []byte) (uint16, error) {
	if len(value) != 4 {
		return 0, errors.New("Unicode escape is not four digits")
	}
	var result uint16
	for _, current := range value {
		result <<= 4
		switch {
		case current >= '0' && current <= '9':
			result += uint16(current - '0')
		case current >= 'a' && current <= 'f':
			result += uint16(current-'a') + 10
		case current >= 'A' && current <= 'F':
			result += uint16(current-'A') + 10
		default:
			return 0, errors.New("invalid Unicode escape")
		}
	}
	return result, nil
}

func (p *jsonParser) parseNumber() (string, bool, error) {
	start := p.position
	if p.consumeByte('-') && p.position >= len(p.data) {
		return "", false, errors.New("unfinished negative number")
	}
	if p.consumeByte('0') {
		// A following decimal digit is rejected by the parent delimiter or
		// trailing-value check.
	} else {
		if p.position >= len(p.data) || p.data[p.position] < '1' || p.data[p.position] > '9' {
			return "", false, errors.New("invalid number integer part")
		}
		for p.position < len(p.data) && p.data[p.position] >= '0' && p.data[p.position] <= '9' {
			p.position++
		}
	}

	integer := true
	if p.consumeByte('.') {
		integer = false
		if p.position >= len(p.data) || p.data[p.position] < '0' || p.data[p.position] > '9' {
			return "", false, errors.New("invalid number fraction")
		}
		for p.position < len(p.data) && p.data[p.position] >= '0' && p.data[p.position] <= '9' {
			p.position++
		}
	}
	if p.position < len(p.data) && (p.data[p.position] == 'e' || p.data[p.position] == 'E') {
		integer = false
		p.position++
		if p.position < len(p.data) && (p.data[p.position] == '+' || p.data[p.position] == '-') {
			p.position++
		}
		if p.position >= len(p.data) || p.data[p.position] < '0' || p.data[p.position] > '9' {
			return "", false, errors.New("invalid number exponent")
		}
		for p.position < len(p.data) && p.data[p.position] >= '0' && p.data[p.position] <= '9' {
			p.position++
		}
	}
	return string(p.data[start:p.position]), integer, nil
}

func (p *jsonParser) countValue() {
	maximum := uint64(MaxDocumentJSONValues)
	if p.values >= maximum+1 {
		return
	}
	p.values++
	if p.values == maximum+1 {
		p.recordFailure(jsonFailureCandidate{failure: resourceFailure(
			CodeInvalidDocument, "document", p.sourceID, "",
			limitDocumentJSONValues, maximum, p.values, -1,
		)})
		p.buildTree = false
	}
}

func (p *jsonParser) recordDepthFailure(pointer *jsonPath, actual int) {
	p.recordFailure(jsonFailureCandidate{
		failure: resourceFailure(
			CodeInvalidDocument, "document", p.sourceID, "",
			limitJSONDepth, uint64(MaxJSONDepth), uint64(actual), -1,
		),
		path: pointer,
	})
	p.buildTree = false
}

// skipOverDepthContainer advances over a container after the depth guard has
// fired. It is iterative, so hostile nesting cannot grow the Go call stack.
// The raw document byte limit bounds its temporary delimiter stack.
func (p *jsonParser) skipOverDepthContainer() error {
	if p.position >= len(p.data) || (p.data[p.position] != '{' && p.data[p.position] != '[') {
		return errors.New("over-depth value is not a container")
	}

	stack := []byte{p.data[p.position]}
	p.position++
	inString := false
	for p.position < len(p.data) {
		current := p.data[p.position]
		p.position++
		if inString {
			switch current {
			case '\\':
				if p.position >= len(p.data) {
					return errors.New("unfinished string escape")
				}
				p.position++
			case '"':
				inString = false
			}
			continue
		}

		switch current {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, current)
		case '}', ']':
			if len(stack) == 0 || !matchingJSONDelimiter(stack[len(stack)-1], current) {
				return errors.New("mismatched JSON delimiter")
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return nil
			}
		}
	}
	return errors.New("unterminated over-depth container")
}

func matchingJSONDelimiter(open, close byte) bool {
	return open == '{' && close == '}' || open == '[' && close == ']'
}

func (p *jsonParser) skipWhitespace() {
	for p.position < len(p.data) {
		switch p.data[p.position] {
		case ' ', '\t', '\n', '\r':
			p.position++
		default:
			return
		}
	}
}

func (p *jsonParser) consumeByte(want byte) bool {
	if p.position >= len(p.data) || p.data[p.position] != want {
		return false
	}
	p.position++
	return true
}

func (p *jsonParser) consumeLiteral(want string) bool {
	if len(p.data)-p.position < len(want) || string(p.data[p.position:p.position+len(want)]) != want {
		return false
	}
	p.position += len(want)
	return true
}
