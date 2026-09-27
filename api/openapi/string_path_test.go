package openapi_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
)

func TestStringPathSchemaRetainsRouterKindAndLimits(t *testing.T) {
	authentication := &describedAuthentication{description: sessionDescription()}
	config := documentConfig(t, authentication)
	config.Operations = config.Operations[:1]
	config.Operations[0].Parameters = nil
	config.Operations[0].Route.Path = "/articles/<str:uid>/<str:token>/<int64:id>/"
	document := newDocument(t, config)
	operation := decodeDocument(t, document).Paths["/articles/{uid}/{token}/{id}/"]["get"]
	if len(operation.Parameters) != 3 {
		t.Fatal("parameters not derived from mixed route")
	}
	for i, name := range []string{"uid", "token"} {
		p := operation.Parameters[i]
		if p.Name != name || p.In != "path" || !p.Required || p.Schema["type"] != "string" || p.Schema["format"] != nil || p.Schema["minLength"] != json.Number("1") || p.Schema["maxLength"] != json.Number("512") || p.Schema["x-godj-max-bytes"] != json.Number("512") {
			t.Fatalf("wrong string parameter %#v", p)
		}
	}
	numeric := operation.Parameters[2]
	if numeric.Name != "id" || numeric.Schema["type"] != "integer" || numeric.Schema["format"] != "int64" || numeric.Schema["maximum"] != json.Number("9223372036854775807") {
		t.Fatal("integer contract changed", numeric)
	}
	pattern := operation.Parameters[0].Schema["pattern"].(string)
	// RE2's $ is strict end-of-input. ECMA's $ also admits end-before-newline;
	// the published trailing guard closes that difference. Strip ONLY that
	// guard for this independent RE2 check of the published character grammar.
	const ecmaGuard = `(?![\s\S])`
	if !strings.HasSuffix(pattern, ecmaGuard) {
		t.Fatal("ECMA pattern admits trailing newline")
	}
	compiled, err := regexp.Compile(strings.TrimSuffix(pattern, ecmaGuard))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		value string
		valid bool
	}{{"r1.abc.ABC_xyz-", true}, {"dXNlci0x", true}, {"set-password", true}, {"0", true}, {"01", true}, {"-1", true}, {"한글é", true}, {"e\u0301", true}, {"%2f", true}, {" ?#", true}, {"...", true}, {".a", true}, {"..a", true}, {"a..", true}, {"", false}, {".", false}, {"..", false}, {"a/b", false}, {"a\\b", false}, {"a\n", false}, {"a\r\n", false}, {"a\x00b", false}, {"a\x7fb", false}}
	for _, c := range cases {
		if got := compiled.MatchString(c.value); got != c.valid {
			t.Fatalf("schema grammar disagrees for %q: %t", c.value, got)
		}
	}
	// OAS code-point maxLength cannot replace the declared UTF-8 byte limit.
	multi := strings.Repeat("é", 257)
	if utf8.RuneCountInString(multi) > 512 || len(multi) <= 512 || !compiled.MatchString(multi) {
		t.Fatal("byte/code-point boundary fixture broken")
	}
	if authentication.requireCalls != 0 {
		t.Fatal("documentation performed authentication")
	}
}

func TestStringPathPolicyAndEquivalentTemplatesFailClosed(t *testing.T) {
	config := documentConfig(t, &describedAuthentication{description: sessionDescription()})
	config.Operations = config.Operations[:1]
	config.Operations[0].Route.Path = "/articles/<str:value>/"
	for _, prefix := range []string{"/articles/token/", "/articles/r1.abc.xyz/", "/articles/01/"} {
		policy, err := api.NewJSONPolicy(prefix)
		if err != nil {
			t.Fatal(err)
		}
		config.JSONPolicy = policy
		if _, err := openapi.New(config); err == nil {
			t.Fatal("partial string policy advertised", prefix)
		}
	}
	policy, err := api.NewJSONPolicy("/articles/")
	if err != nil {
		t.Fatal(err)
	}
	config.JSONPolicy = policy
	config.Operations = append(config.Operations, config.Operations[0])
	config.Operations[1].Route.Name = "articles:integer"
	config.Operations[1].Route.Method = http.MethodPost
	config.Operations[1].Route.Path = "/articles/<int64:value>/"
	document := newDocument(t, config)
	methods := decodeDocument(t, document).Paths["/articles/{value}/"]
	if methods["get"].Parameters[0].Schema["type"] != "string" || methods["post"].Parameters[0].Schema["type"] != "integer" {
		t.Fatal("method-specific converter was collapsed")
	}
	config.Operations[1].Route.Path = "/articles/<int64:id>/"
	if _, err := openapi.New(config); err == nil {
		t.Fatal("same OAS path shape with different names accepted")
	}
	config.Operations[1].Route.Path = "/articles/<int64:value>/"
	config.Operations[1].Route.Method = http.MethodGet
	if _, err := openapi.New(config); err == nil {
		t.Fatal("overlapping string/integer languages accepted")
	}
}
