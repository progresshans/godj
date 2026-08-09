//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

type jsonValue any

var canonicalUnsigned = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

func decodeClosedJSON(document []byte, maximum int) (map[string]jsonValue, error) {
	if len(document) > maximum || !utf8.Valid(document) {
		return nil, fmt.Errorf("invalid framing")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing token %v", token)
		}
		return nil, fmt.Errorf("trailing framing: %w", err)
	}
	object, ok := value.(map[string]jsonValue)
	if !ok {
		return nil, fmt.Errorf("top-level value is not object")
	}
	return object, nil
}

func decodeJSONValue(decoder *json.Decoder, depth int) (jsonValue, error) {
	if depth > 32 {
		return nil, fmt.Errorf("wire nesting too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]jsonValue)
		for decoder.More() {
			member, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := member.(string)
			if !ok {
				return nil, fmt.Errorf("object key is not string")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate key %q", key)
			}
			value, err := decodeJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return nil, fmt.Errorf("invalid object close")
		}
		return object, nil
	case '[':
		array := make([]jsonValue, 0)
		for decoder.More() {
			value, err := decodeJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return nil, fmt.Errorf("invalid array close")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter %q", delimiter)
	}
}

func canonicalUint(value jsonValue, maximum uint64) (uint64, bool) {
	number, ok := value.(json.Number)
	if !ok || !canonicalUnsigned.MatchString(number.String()) {
		return 0, false
	}
	parsed, err := strconv.ParseUint(number.String(), 10, 64)
	return parsed, err == nil && parsed <= maximum
}

func parseRunnerRequest(document []byte, lim limits) *failure {
	object, err := decodeClosedJSON(document, lim.requestBytes)
	if err != nil {
		return fail("migration_project_protocol_error", "invalid_project_runner_request")
	}
	versionValue, exists := object["protocol_version"]
	if !exists {
		return fail("migration_project_protocol_error", "invalid_project_runner_request")
	}
	version, valid := canonicalUint(versionValue, 65_535)
	if !valid {
		return fail("migration_project_protocol_error", "invalid_project_runner_request")
	}
	if version != supportedProtocolVersion {
		return fail("migration_project_protocol_error", "project_protocol_incompatible")
	}
	if !exactKeys(object, "protocol_version", "command") || object["command"] != "migrations.check" {
		return fail("migration_project_protocol_error", "invalid_project_runner_request")
	}
	return nil
}

func parseRunnerResponse(document []byte, transportExit int, lim limits) (*checkResult, *failure) {
	if transportExit != 0 {
		return nil, fail("migration_project_protocol_error", "project_runner_failed")
	}
	object, err := decodeClosedJSON(document, lim.responseBytes)
	if err != nil {
		return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
	}
	versionValue, exists := object["protocol_version"]
	if !exists {
		return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
	}
	version, valid := canonicalUint(versionValue, 65_535)
	if !valid {
		return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
	}
	if version != supportedProtocolVersion {
		return nil, fail("migration_project_protocol_error", "project_protocol_incompatible")
	}
	status, ok := object["status"].(string)
	if !ok {
		return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
	}
	switch status {
	case "ok":
		if !exactKeys(object, "protocol_version", "status", "result") {
			return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
		}
		resultObject, ok := object["result"].(map[string]jsonValue)
		if !ok || !exactKeys(resultObject, "source_count", "definition_count", "definition_set_digest") {
			return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
		}
		sourceCount, sourceValid := canonicalUint(resultObject["source_count"], maxSources)
		definitionCount, definitionValid := canonicalUint(resultObject["definition_count"], maxSources)
		digest, digestValid := resultObject["definition_set_digest"].(string)
		if !sourceValid || !definitionValid || !digestValid || !validDigest(digest) {
			return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
		}
		if sourceCount != definitionCount || (sourceCount == 0 && digest != emptySetDigest) || (sourceCount > 0 && digest == emptySetDigest) {
			return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
		}
		return &checkResult{SourceCount: int(sourceCount), DefinitionCount: int(definitionCount), DefinitionSetDigest: digest}, nil
	case "error":
		if !exactKeys(object, "protocol_version", "status", "error") {
			return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
		}
		errorObject, ok := object["error"].(map[string]jsonValue)
		if !ok || !exactKeys(errorObject, "category", "code") {
			return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
		}
		category, categoryOK := errorObject["category"].(string)
		code, codeOK := errorObject["code"].(string)
		if !categoryOK || !codeOK || !allowedLinkedPair(category, code) {
			return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
		}
		return nil, fail(category, code)
	default:
		return nil, fail("migration_project_protocol_error", "invalid_project_runner_response")
	}
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func allowedLinkedPair(category, code string) bool {
	switch category {
	case "migration_project_protocol_error":
		return code == "invalid_project_runner_request" || code == "project_protocol_incompatible"
	case "migration_definition_discovery_error":
		switch code {
		case "invalid_project_source_config", "invalid_source_root", "invalid_source_entry", "unsafe_source_entry", "source_catalog_limit_exceeded", "source_discovery_failed", "source_read_failed":
			return true
		}
	case "migration_definition_source_error":
		switch code {
		case "invalid_definition_source", "invalid_definition_document", "definition_format_incompatible", "loader_abi_incompatible", "operation_codec_incompatible", "schema_ir_incompatible", "unsupported_definition_operation", "invalid_definition_operation", "invalid_definition_ir":
			return true
		}
	case "migration_graph_error":
		switch code {
		case "invalid_node", "duplicate_node", "invalid_dependency", "duplicate_dependency", "dependency_not_found", "dependency_cycle":
			return true
		}
	}
	return false
}

func encodeRunnerSuccess(result checkResult) []byte {
	return []byte(fmt.Sprintf(`{"protocol_version":1,"status":"ok","result":{"source_count":%d,"definition_count":%d,"definition_set_digest":%q}}`, result.SourceCount, result.DefinitionCount, result.DefinitionSetDigest))
}

func encodeRunnerFailure(primary *failure) []byte {
	return []byte(fmt.Sprintf(`{"protocol_version":1,"status":"error","error":{"category":%q,"code":%q}}`, primary.Category, primary.Code))
}

type commandArguments struct {
	ExplicitDescriptor string
}

func parseArguments(argv []string) (commandArguments, *failure) {
	if len(argv) == 2 && argv[0] == "migrations" && argv[1] == "check" {
		return commandArguments{}, nil
	}
	if len(argv) == 4 && argv[0] == "migrations" && argv[1] == "check" && argv[2] == "--project" && argv[3] != "" {
		return commandArguments{ExplicitDescriptor: argv[3]}, nil
	}
	return commandArguments{}, fail("migration_project_command_error", "invalid_arguments")
}

type descriptor struct {
	Version uint64
	Package string
}

func parseDescriptor(document []byte, lim limits) (descriptor, *failure) {
	invalid := func() (descriptor, *failure) {
		return descriptor{}, fail("migration_project_selection_error", "invalid_project_descriptor")
	}
	if len(document) > lim.descriptorBytes || len(document) == 0 || !utf8.Valid(document) || bytes.HasPrefix(document, []byte{0xef, 0xbb, 0xbf}) {
		return invalid()
	}
	for _, character := range document {
		if character > 0x7f || (character < 0x20 && character != '\n' && character != '\r' && character != '\t') {
			return invalid()
		}
	}
	newline := []byte{'\n'}
	if bytes.Contains(document, []byte{'\r'}) {
		newline = []byte{'\r', '\n'}
		withoutCRLF := bytes.ReplaceAll(document, []byte{'\r', '\n'}, nil)
		if bytes.Contains(withoutCRLF, []byte{'\r'}) || bytes.Contains(withoutCRLF, []byte{'\n'}) {
			return invalid()
		}
	}
	if !bytes.HasSuffix(document, newline) {
		return invalid()
	}
	lines := bytes.Split(document[:len(document)-len(newline)], newline)
	semantic := make([]string, 0, 3)
	for _, raw := range lines {
		line := string(raw)
		trimmed := strings.Trim(line, " \t")
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			for _, character := range trimmed[1:] {
				if character != '\t' && (character < 0x20 || character > 0x7e) {
					return invalid()
				}
			}
			continue
		}
		semantic = append(semantic, line)
	}
	if len(semantic) != 3 {
		return invalid()
	}
	versionLexeme, ok := descriptorAssignment(semantic[0], "format_version", false)
	if !ok || strings.Trim(semantic[1], " \t") != "[project]" {
		return invalid()
	}
	packageLexeme, ok := descriptorAssignment(semantic[2], "package", true)
	if !ok || !validPackage(packageLexeme) {
		return invalid()
	}
	if !canonicalUnsigned.MatchString(versionLexeme) {
		return invalid()
	}
	version, err := strconv.ParseUint(versionLexeme, 10, 16)
	if err != nil {
		return invalid()
	}
	if version != supportedDescriptorVersion {
		return descriptor{}, fail("migration_project_selection_error", "project_descriptor_incompatible")
	}
	return descriptor{Version: version, Package: packageLexeme}, nil
}

func descriptorAssignment(line, key string, quoted bool) (string, bool) {
	trimmed := strings.Trim(line, " \t")
	if !strings.HasPrefix(trimmed, key) {
		return "", false
	}
	remainder := trimmed[len(key):]
	if remainder == "" || (remainder[0] != ' ' && remainder[0] != '\t' && remainder[0] != '=') {
		return "", false
	}
	remainder = strings.TrimLeft(remainder, " \t")
	if !strings.HasPrefix(remainder, "=") {
		return "", false
	}
	remainder = strings.Trim(remainder[1:], " \t")
	if quoted {
		if len(remainder) < 2 || remainder[0] != '"' || remainder[len(remainder)-1] != '"' {
			return "", false
		}
		remainder = remainder[1 : len(remainder)-1]
		if strings.ContainsRune(remainder, '"') {
			return "", false
		}
	} else if strings.ContainsAny(remainder, " \t") {
		return "", false
	}
	return remainder, remainder != ""
}

func validPackage(candidate string) bool {
	if !strings.HasPrefix(candidate, "./") {
		return false
	}
	remainder := strings.TrimPrefix(candidate, "./")
	if remainder == "" || path.Clean(remainder) != remainder || strings.ContainsAny(remainder, `\`+"\x00*?[]{}") {
		return false
	}
	for _, character := range remainder {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	for _, segment := range strings.Split(remainder, "/") {
		if segment == "" || segment == "." || segment == ".." || segment == "..." {
			return false
		}
	}
	return true
}

func canonicalDescriptor(packagePath string) []byte {
	return []byte("format_version = 1\n\n[project]\npackage = \"" + packagePath + "\"\n")
}

func TestArgumentsAreClosedBeforeSelection(t *testing.T) {
	t.Parallel()
	valid := [][]string{{"migrations", "check"}, {"migrations", "check", "--project", "godj.toml"}}
	for _, argv := range valid {
		if _, err := parseArguments(argv); err != nil {
			t.Fatalf("parseArguments(%q) = %v", argv, err)
		}
	}
	invalid := [][]string{
		nil,
		{"check", "migrations"},
		{"migrations", "--project", "godj.toml", "check"},
		{"migrations", "check", "--project=godj.toml"},
		{"migrations", "check", "--project", ""},
		{"migrations", "check", "--project", "godj.toml", "extra"},
		{"migrations", "check", "--project", "godj.toml", "--project", "other/godj.toml"},
	}
	for _, argv := range invalid {
		if _, err := parseArguments(argv); err == nil || err.Code != "invalid_arguments" || err.ExitCode != 2 {
			t.Fatalf("parseArguments(%q) = %v, want invalid_arguments/2", argv, err)
		}
	}
}

func TestDescriptorV1ClosedShapeAndVersionPrecedence(t *testing.T) {
	t.Parallel()
	lim := contractLimits()
	valid := [][]byte{
		canonicalDescriptor("./cmd/mysite"),
		[]byte("# project\r\nformat_version\t=\t1\r\n\r\n [project] \r\npackage = \"./cmd/site\"\r\n"),
	}
	for _, document := range valid {
		parsed, err := parseDescriptor(document, lim)
		if err != nil || parsed.Version != 1 || !strings.HasPrefix(parsed.Package, "./") {
			t.Fatalf("parseDescriptor(valid) = %+v, %v", parsed, err)
		}
	}
	invalid := [][]byte{
		[]byte("format_version = 1\n[project]\npackage = \"./cmd/site\""),
		[]byte("\xef\xbb\xbfformat_version = 1\n[project]\npackage = \"./cmd/site\"\n"),
		[]byte("format_version = 1\r\n[project]\npackage = \"./cmd/site\"\n"),
		[]byte("format_version = 01\n[project]\npackage = \"./cmd/site\"\n"),
		[]byte("format_version = +1\n[project]\npackage = \"./cmd/site\"\n"),
		[]byte("format_version = 1\n[project]\npackage = \"cmd/site\"\n"),
		[]byte("format_version = 1\n[project]\npackage = \"./cmd/../site\"\n"),
		[]byte("format_version = 1\n[project]\npackage = \"./...\"\n"),
		[]byte("format_version = 2\n[project]\npackage = \"./bad//path\"\n"),
		[]byte("format_version = 1\n[project]\nunknown = \"./cmd/site\"\n"),
	}
	for _, document := range invalid {
		if _, err := parseDescriptor(document, lim); err == nil || err.Code != "invalid_project_descriptor" {
			t.Fatalf("parseDescriptor(%q) = %v, want invalid descriptor", document, err)
		}
	}
	incompatible := []byte("format_version = 2\n[project]\npackage = \"./cmd/site\"\n")
	if _, err := parseDescriptor(incompatible, lim); err == nil || err.Code != "project_descriptor_incompatible" {
		t.Fatalf("version 2 = %v, want incompatible", err)
	}
}

func TestProtocolCoordinateAndClosedSchemaPrecedence(t *testing.T) {
	t.Parallel()
	lim := contractLimits()
	if err := parseRunnerRequest([]byte(`{"protocol_version":1,"command":"migrations.check"}`), lim); err != nil {
		t.Fatalf("valid request = %v", err)
	}
	requestCases := []struct {
		wire string
		code string
	}{
		{`{"protocol_version":2,"command":"migrations.check"}`, "project_protocol_incompatible"},
		{`{"protocol_version":2,"protocol_version":1,"command":"migrations.check"}`, "invalid_project_runner_request"},
		{`{"protocol_version":1.0,"command":"migrations.check"}`, "invalid_project_runner_request"},
		{`{"protocol_version":1,"command":"other"}`, "invalid_project_runner_request"},
		{`{"protocol_version":1,"command":"migrations.check","extra":0}`, "invalid_project_runner_request"},
	}
	for _, test := range requestCases {
		if err := parseRunnerRequest([]byte(test.wire), lim); err == nil || err.Code != test.code {
			t.Fatalf("request %s = %v, want %s", test.wire, err, test.code)
		}
	}
	valid := encodeRunnerSuccess(checkResult{SourceCount: 0, DefinitionCount: 0, DefinitionSetDigest: emptySetDigest})
	if result, err := parseRunnerResponse(valid, 0, lim); err != nil || result.SourceCount != 0 {
		t.Fatalf("valid response = %+v, %v", result, err)
	}
	responseCases := []struct {
		wire string
		code string
	}{
		{`{"protocol_version":2,"status":"ok","result":{"source_count":0,"definition_count":0,"definition_set_digest":"` + emptySetDigest + `"}}`, "project_protocol_incompatible"},
		{`{"protocol_version":2,"protocol_version":1,"status":"ok","result":{"source_count":0,"definition_count":0,"definition_set_digest":"` + emptySetDigest + `"}}`, "invalid_project_runner_response"},
		{`{"protocol_version":1,"status":"ok","status":"error","result":{"source_count":0,"definition_count":0,"definition_set_digest":"` + emptySetDigest + `"}}`, "invalid_project_runner_response"},
		{`{"protocol_version":1,"status":"ok","result":{"source_count":01,"definition_count":1,"definition_set_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`, "invalid_project_runner_response"},
		{`{"protocol_version":1,"status":"ok","result":{"source_count":1,"definition_count":0,"definition_set_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`, "invalid_project_runner_response"},
		{`{"protocol_version":1,"status":"ok","result":{"source_count":1,"definition_count":1,"definition_set_digest":"` + emptySetDigest + `"}}`, "invalid_project_runner_response"},
		{`{"protocol_version":1,"status":"error","error":{"category":"migration_project_build_error","code":"project_build_failed"}}`, "invalid_project_runner_response"},
		{`{"protocol_version":1,"status":"error","error":{"category":"migration_definition_source_error","code":"invented_code"}}`, "invalid_project_runner_response"},
	}
	for _, test := range responseCases {
		if _, err := parseRunnerResponse([]byte(test.wire), 0, lim); err == nil || err.Code != test.code {
			t.Fatalf("response %s = %v, want %s", test.wire, err, test.code)
		}
	}
	if _, err := parseRunnerResponse(valid, 9, lim); err == nil || err.Code != "project_runner_failed" {
		t.Fatalf("transport exit precedence = %v", err)
	}
}

func TestClosedTaxonomyHasExactExitMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		category string
		code     string
		exit     int
	}{
		{"migration_project_command_error", "invalid_arguments", 2},
		{"migration_project_selection_error", "project_not_found", 2},
		{"migration_project_selection_error", "project_selection_failed", 3},
		{"migration_project_build_error", "project_build_failed", 3},
		{"migration_project_protocol_error", "invalid_project_runner_response", 3},
		{"migration_project_process_error", "project_canceled", 3},
		{"migration_project_process_error", "project_interrupted", 130},
		{"migration_definition_discovery_error", "invalid_source_root", 2},
		{"migration_definition_discovery_error", "unsafe_source_entry", 1},
		{"migration_definition_discovery_error", "source_read_failed", 3},
		{"migration_definition_source_error", "invalid_definition_document", 1},
		{"migration_graph_error", "duplicate_node", 1},
		{"migration_project_internal_error", "project_internal_error", 3},
	}
	for _, test := range cases {
		if actual := exitFor(test.category, test.code); actual != test.exit {
			t.Fatalf("%s exit = %d, want %d", joinedPair(test.category, test.code), actual, test.exit)
		}
	}
	if actual := exitFor("migration_definition_source_error", "invented_code"); actual != -1 {
		t.Fatalf("unknown pair exit = %d, want closed-taxonomy sentinel -1", actual)
	}
	if primary := fail("invented_category", "invented_code"); primary.Category != "migration_project_internal_error" || primary.Code != "project_internal_error" || primary.ExitCode != 3 {
		t.Fatalf("unknown local failure did not fail closed: %+v", primary)
	}
}
