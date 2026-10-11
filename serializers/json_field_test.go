package serializers_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/jsoninput"
	"github.com/progresshans/godj/internal/jsontest"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func jsonSpec(t *testing.T, name string) serializers.Spec {
	t.Helper()
	options := []serializers.FieldOption{}
	if name != "required" {
		options = append(options, serializers.WithNullable(), serializers.WithRequired(false))
	}
	if name == "default" {
		options = append(options, serializers.WithDefault(serializers.JSON(jsonvalue.Value{Text: `{}`})))
	}
	field, err := serializers.JSONField("payload", options...)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestJSONSerializersAgainstPinnedDRF(t *testing.T) {
	reference := jsontest.Load(t)
	inputs := map[string]jsontest.Value{}
	for _, raw := range reference.Model {
		var row struct {
			Label string
			Input jsontest.Value
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatal(err)
		}
		inputs[row.Label] = row.Input
	}
	rejectedText, unsupportedBytes, rejectedNonfinite := 0, 0, 0
	for index, raw := range reference.Serializer {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			var row struct {
				Serializer, Label string
				Partial, Valid    bool
				Validated         jsontest.Value
				Errors            map[string][]string
				RenderException   string `json:"render_exception"`
			}
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			if row.Label == "bytes" {
				if row.Valid || !reflect.DeepEqual(row.Errors, map[string][]string{"payload": {"invalid"}}) {
					t.Fatal("Python-only bytes observation changed")
				}
				unsupportedBytes++
				return
			}
			value := serializers.Value{}
			switch row.Label {
			case "omitted":
			case "null":
				value = serializers.Null()
			case "nan":
				value = serializers.Float(math.NaN())
			case "infinity":
				value = serializers.Float(math.Inf(1))
			case "negative_infinity":
				value = serializers.Float(math.Inf(-1))
			case "decimal":
				number, err := decimal.Parse("0.1")
				if err != nil {
					t.Fatal(err)
				}
				value = serializers.Decimal(number)
			case "datetime":
				value = serializers.DateTime(time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC))
			case "surrogate", "nul", "nul_key":
				if !row.Valid {
					t.Fatal("independent text boundary observation changed")
				}
				text := `"\u0000"`
				if row.Label == "surrogate" {
					text = `"\ud800"`
					if row.RenderException != "UnicodeEncodeError" {
						t.Fatal("surrogate renderer observation changed")
					}
				} else if row.Label == "nul_key" {
					text = `{"\u0000":"nul key"}`
				}
				if _, err := jsonSpec(t, row.Serializer).DecodeObject([]byte(`{"payload":`+text+`}`), serializers.Limits{}); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidDocument}) {
					t.Fatal("JSON field bypassed API text boundary", err)
				}
				rejectedText++
				return
			default:
				input, ok := inputs[row.Label]
				if !ok {
					t.Fatal("independent input absent")
				}
				value = serializers.JSON(input.JSON(t))
			}
			members := []serializers.Member{}
			if row.Label != "omitted" {
				members = append(members, serializers.MemberOf("payload", value))
			}
			object, err := serializers.NewObject(members...)
			if row.Label == "nan" || row.Label == "infinity" || row.Label == "negative_infinity" {
				if row.Valid || !reflect.DeepEqual(row.Errors, map[string][]string{"payload": {"invalid"}}) || !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) {
					t.Fatal("nonfinite input must fail before object publication", err)
				}
				rejectedNonfinite++
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			mode := serializers.ModeFull
			if row.Partial {
				mode = serializers.ModePartial
			}
			result, err := jsonSpec(t, row.Serializer).Bind(object, mode)
			if err != nil {
				t.Fatal(err)
			}
			codes := map[string][]string{}
			for _, violation := range result.Errors().All() {
				key := string(violation.Field())
				codes[key] = append(codes[key], string(violation.Code()))
			}
			if result.Valid() != row.Valid || !reflect.DeepEqual(codes, row.Errors) {
				t.Fatalf("%s JSON codes %v want %v", row.Label, codes, row.Errors)
			}
			var validated [][]jsontest.Value
			if row.Validated.Kind != "object" {
				t.Fatal("independent validated data is not an object")
			}
			if err := json.Unmarshal(row.Validated.Value, &validated); err != nil {
				t.Fatal(err)
			}
			got, present := result.Values().Get("payload")
			if present != (len(validated) == 1) {
				t.Fatal("JSON omission/default/invalid presence changed")
			}
			if !present {
				return
			}
			if len(validated[0]) != 2 {
				t.Fatal("invalid reference validated member")
			}
			want := validated[0][1]
			if want.Kind == "null" {
				if !got.IsNull() {
					t.Fatal("input JSON null no longer clears nullable value")
				}
			} else {
				document, ok := got.AsJSON()
				if !ok || !jsoninput.Equal(document, want.JSON(t)) {
					t.Fatal("JSON validated content differs from reference")
				}
			}
			if _, err := serializers.Encode(got, serializers.Limits{}); err != nil {
				t.Fatal(err)
			}
		})
	}
	if rejectedText != 18 || unsupportedBytes != 6 || rejectedNonfinite != 18 {
		t.Fatal("JSON input boundary selectors missing", rejectedText, unsupportedBytes, rejectedNonfinite)
	}
}

func TestJSONDeclaredDecoderPreservesEnvelopeAndAggregateBudgets(t *testing.T) {
	spec := jsonSpec(t, "optional")
	for _, raw := range []string{`null`, `false`, `0`, `1.00`, `1e400`, `340282366920938463463374607431768211455`, `"{\"a\":1}"`, `{"": [false,null]}`, `{"__proto__":{"constructor":1}}`} {
		wire := []byte(`{"payload":` + raw + `}`)
		object, err := spec.DecodeObject(wire, serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		result, err := spec.Bind(object, serializers.ModeFull)
		if err != nil || !result.Valid() {
			t.Fatal("valid JSON input rejected", err)
		}
		value, _ := result.Values().Get("payload")
		if raw == "null" {
			if !value.IsNull() {
				t.Fatal("explicit null lost input presence")
			}
			continue
		}
		document, ok := value.AsJSON()
		expected, err := jsonvalue.Parse([]byte(raw))
		if !ok || err != nil || document != expected {
			t.Fatal("JSON field changed exact tokens or decoded a JSON-bearing string twice", err)
		}
		wire[0] = 'x'
		if got, _ := value.AsJSON(); got != expected {
			t.Fatal("JSON input aliases bytes")
		}
	}
	for _, raw := range []string{`{"":1}`, `{"payload":{"":1,"":2}}`, `{"payload":{"a":1,"a":2}}`, `{"foreign":{"":1}}`, `{"payload":{"x":"\u0000"}}`, `{"payload":"\ud800"}`, `{"payload":NaN}`, `{"payload":{}} {}`} {
		if _, err := spec.DecodeObject([]byte(raw), serializers.Limits{}); err == nil {
			t.Fatal("JSON declaration broadened another boundary", raw)
		}
	}
	if _, err := serializers.DecodeObject([]byte(`{"payload":{"":1}}`), serializers.Limits{}); err == nil {
		t.Fatal("generic named-object decoder changed")
	}
	if _, err := serializers.NewObject(serializers.MemberOf("", serializers.Integer(1))); err == nil {
		t.Fatal("named-object constructor changed")
	}
	left, err := serializers.JSONField("left")
	if err != nil {
		t.Fatal(err)
	}
	right, err := serializers.JSONField("right")
	if err != nil {
		t.Fatal(err)
	}
	both, err := serializers.NewSpec([]serializers.Field{left, right})
	if err != nil {
		t.Fatal(err)
	}
	wire := []byte(`{"left":[1,2],"right":[3,4]}`)
	for _, maximum := range []int{6, 7} {
		object, err := both.DecodeObject(wire, serializers.Limits{MaxValues: maximum})
		if (err == nil) != (maximum == 7) {
			t.Fatal("JSON fields reset shared decode value budget", maximum, err)
		}
		if maximum == 7 {
			for _, outLimit := range []int{6, 7} {
				_, err := serializers.Encode(object.Value(), serializers.Limits{MaxValues: outLimit})
				if (err == nil) != (outLimit == 7) {
					t.Fatal("opaque JSON fields reset encode value budget", outLimit, err)
				}
			}
		}
	}
	for _, limit := range []int{3, 4} {
		object, err := spec.DecodeObject([]byte(`{"payload":[{"":true}]}`), serializers.Limits{MaxDepth: limit})
		if (err == nil) != (limit == 4) {
			t.Fatal("JSON field reset decode depth", limit, err)
		}
		if err == nil {
			if _, err := serializers.Encode(object.Value(), serializers.Limits{MaxDepth: 3}); err == nil {
				t.Fatal("JSON field reset encoder depth")
			}
		}
	}
	deep := serializers.Integer(1)
	for range 1000 {
		deep, err = serializers.NewList(deep)
		if err != nil {
			t.Fatal(err)
		}
	}
	object, err := serializers.NewObject(serializers.MemberOf("payload", deep))
	if err != nil {
		t.Fatal(err)
	}
	if result, err := spec.Bind(object, serializers.ModeFull); err != nil || result.Valid() {
		t.Fatal("deep programmatic JSON bypassed bounded validation", err)
	}
}

func TestJSONFieldParsedRequestsAgainstIndependentJSONParser(t *testing.T) {
	spec := jsonSpec(t, "optional")
	strict, exact := 0, 0
	for index, raw := range jsontest.Load(t).Parsed {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			var row struct {
				Input          string
				Valid          bool
				Errors         map[string][]string
				ParseException string `json:"parse_exception"`
			}
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			object, err := spec.DecodeObject([]byte(`{"payload":`+row.Input+`}`), serializers.Limits{})
			strictText := row.Input == `{"a":1,"a":2}` || row.Input == `"\u0000"` || row.Input == `"\ud800"` || row.Input == `{"\u0000":"nul key"}`
			if row.ParseException != "" || strictText {
				if err == nil {
					t.Fatal("invalid JSON parser boundary accepted", row.Input)
				}
				if strictText {
					strict++
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := spec.Bind(object, serializers.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			if row.Input == `{"v":1e400}` {
				if row.Valid || !reflect.DeepEqual(row.Errors, map[string][]string{"payload": {"invalid"}}) || !result.Valid() {
					t.Fatal("overflow selector no longer distinguishes exact JSON from Python infinity")
				}
				exact++
			} else if result.Valid() != row.Valid {
				t.Fatal("parsed JSON validation differs", row.Input)
			}
			if !result.Valid() {
				t.Fatal("unexpected invalid parsed JSON", row.Input)
			}
			value, present := result.Values().Get("payload")
			if !present {
				t.Fatal("parsed field disappeared")
			}
			if strings.TrimSpace(row.Input) == "null" {
				if !value.IsNull() {
					t.Fatal("parsed null lost nullable meaning")
				}
				return
			}
			want, err := jsonvalue.Parse([]byte(row.Input))
			if err != nil {
				t.Fatal(err)
			}
			got, ok := value.AsJSON()
			if !ok || got != want {
				t.Fatal("parsed JSON lost exact source tokens or string kind", row.Input)
			}
		})
	}
	if strict != 4 || exact != 1 {
		t.Fatal("JSON parsed deviation selectors incomplete", strict, exact)
	}
}

func TestJSONModelEncoderDefaultsAndLiteralNull(t *testing.T) {
	built, err := schema.Build(schema.Definition{AppLabel: "refs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.JSONField("payload", "Payload", schema.Default(jsonvalue.Null()))}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := built.Models[0]
	spec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "payload"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := serializers.NewObject()
	if err != nil {
		t.Fatal(err)
	}
	full, err := spec.Bind(empty, serializers.ModeFull)
	if err != nil || !full.Valid() {
		t.Fatal(err)
	}
	value, ok := full.Values().JSON("payload")
	if !ok || value != jsonvalue.Null() {
		t.Fatal("literal JSON null default became SQL NULL")
	}
	partial, err := spec.Bind(empty, serializers.ModePartial)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := partial.Values().Get("payload"); present {
		t.Fatal("PATCH inserted JSON default")
	}
	request, err := spec.DecodeObject([]byte(`{"payload":null}`), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := spec.Bind(request, serializers.ModeFull)
	if err != nil || rejected.Valid() {
		t.Fatal("nonnullable JSON request accepted null", err)
	}
	// Go callers may supply the opaque model JSON value directly. Explicit
	// input still has the HTTP null contract; only omission applies the richer
	// non-null database default above.
	request, err = serializers.NewObject(serializers.MemberOf("payload", serializers.JSON(jsonvalue.Null())))
	if err != nil {
		t.Fatal(err)
	}
	rejected, err = spec.Bind(request, serializers.ModeFull)
	if err != nil || rejected.Valid() {
		t.Fatal("opaque JSON null bypassed nonnullable input", err)
	}
	cleared, err := jsonSpec(t, "optional").Bind(request, serializers.ModePartial)
	if err != nil || !cleared.Valid() {
		t.Fatal("opaque JSON null did not clear nullable input", err)
	}
	if value, present := cleared.Values().Get("payload"); !present || !value.IsNull() {
		t.Fatal("opaque input null retained the model default tag")
	}
	encoder, err := serializers.NewModelEncoder(spec, metadata, func(v jsonvalue.Value, field ir.Field) (query.Value, bool) {
		field.Default.JSON = `"foreign"`
		return query.JSON(v), true
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{"":[]}`, `[true,340282366920938463463374607431768211455]`, `"not parsed again"`} {
		document, err := jsonvalue.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		response, err := encoder.Encode(document)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := serializers.Encode(response, serializers.Limits{})
		if err != nil || string(encoded) != `{"payload":`+document.Text+`}` {
			t.Fatal("model JSON response was encoded as a Go object or string", string(encoded), err)
		}
	}
	if metadata.Fields[1].Default.JSON != "null" {
		t.Fatal("JSON encoder exposes model metadata")
	}
	for _, raw := range []string{"", `"\u0000"`, `{"\u0000":true}`, `"\ud800"`, strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65)} {
		if _, err := serializers.Encode(serializers.JSON(jsonvalue.Value{Text: raw}), serializers.Limits{}); err == nil {
			t.Fatal("invalid model JSON became a valid output")
		}
	}
}
