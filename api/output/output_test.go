package output_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/serializers"
)

type row struct {
	ID       int64
	Name     string
	Active   bool
	Priority *int64
	Private  string
}

func rowShape() output.Shape[row] {
	return output.Named("Row", output.Object(
		output.Field("id", output.Int64(), func(value row) int64 { return value.ID }),
		output.Field("name", output.String(), func(value row) string { return value.Name }),
		output.Field("active", output.Boolean(), func(value row) bool { return value.Active }),
		output.Field("priority", output.Nullable(output.Int64()), func(value row) *int64 { return value.Priority }),
	))
}

func prepare[T any](t *testing.T, shape output.Shape[T], limits serializers.Limits) output.Output[T] {
	t.Helper()
	prepared, err := output.New(shape, limits)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestOutputMatchesExplicitWireAndSchemaContract(t *testing.T) {
	type page struct {
		Rows  []row
		Total int64
	}
	item := rowShape()
	prepared := prepare(t, output.Named("Page", output.Object(
		output.Field("results", output.Array(item, 0, 2), func(value page) []row { return value.Rows }),
		output.Field("total", output.Int64Range(0, math.MaxInt64), func(value page) int64 { return value.Total }),
	)), serializers.Limits{})
	minimum := int64(math.MinInt64)
	input := page{Rows: []row{{ID: math.MaxInt64, Name: " < & \u2028 한글 ", Priority: &minimum, Private: "never expose"}, {ID: 0, Name: "", Active: true}}, Total: 2}
	response, err := prepared.JSON(t.Context(), http.StatusOK, input)
	if err != nil {
		t.Fatal(err)
	}
	body, err := response.Body()
	const want = `{"results":[{"id":9223372036854775807,"name":" < & \u2028 한글 ","active":false,"priority":-9223372036854775808},{"id":0,"name":"","active":true,"priority":null}],"total":2}`
	if err != nil || string(body) != want || response.Status() != 200 || response.Header().Get("Content-Type") != api.JSONContentType {
		t.Fatalf("response = %s (%v), status %d", body, err, response.Status())
	}
	minimum = 1
	input.Rows[0].Name = "caller mutation"
	body[0] = '!'
	again, err := response.Body()
	if err != nil || string(again) != want {
		t.Fatal("response retained caller data", err)
	}
	components, err := output.Components(prepared.Declaration())
	if err != nil || len(components) != 2 {
		t.Fatalf("components = %#v, %v", components, err)
	}
	for _, component := range components {
		encoded, err := serializers.Encode(component.Schema.Value(), serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		actual := schemaJSON(t, encoded)
		text := `{"type":"object","properties":{"id":{"type":"integer","format":"int64","minimum":-9223372036854775808,"maximum":9223372036854775807},"name":{"type":"string"},"active":{"type":"boolean"},"priority":{"anyOf":[{"type":"integer","format":"int64","minimum":-9223372036854775808,"maximum":9223372036854775807},{"type":"null"}]}},"additionalProperties":false,"required":["id","name","active","priority"]}`
		if component.Name == "Page" {
			text = `{"type":"object","properties":{"results":{"type":"array","items":{"$ref":"#/components/schemas/Row"},"minItems":0,"maxItems":2},"total":{"type":"integer","format":"int64","minimum":0,"maximum":9223372036854775807}},"additionalProperties":false,"required":["results","total"]}`
		} else if component.Name != "Row" {
			t.Fatal("unexpected component", component.Name)
		}
		expected := schemaJSON(t, []byte(text))
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("schema %s = %s", component.Name, encoded)
		}
	}
	components[0] = openapi.NamedSchema{Name: "mutated", Schema: openapi.Boolean()}
	owned, err := output.Components(prepared.Declaration())
	if err != nil || owned[0].Name != "Page" {
		t.Fatal("component slice retained", err)
	}
	empty, err := prepared.Encode(t.Context(), page{})
	if err != nil || string(empty) != `{"results":[],"total":0}` {
		t.Fatalf("nil array = %s, %v", empty, err)
	}
}

func schemaJSON(t *testing.T, data []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestOutputRejectsDeclarationFailuresBeforeGetters(t *testing.T) {
	reads := 0
	field := output.Field("id", output.Int64(), func(value row) int64 { reads++; return value.ID })
	for name, shape := range map[string]output.Shape[row]{
		"zero":               {},
		"nil getter":         output.Object(output.Field("id", output.Int64(), (func(row) int64)(nil))),
		"zero property":      output.Object(output.Property[row]{}),
		"zero nested":        output.Object(output.Field("id", output.Shape[int64]{}, func(value row) int64 { return value.ID })),
		"duplicate":          output.Object(field, field),
		"empty name":         output.Object(output.Field("", output.Int64(), func(value row) int64 { return value.ID })),
		"nul name":           output.Object(output.Field("x\x00", output.Int64(), func(value row) int64 { return value.ID })),
		"invalid UTF8 name":  output.Object(output.Field("x\xff", output.Int64(), func(value row) int64 { return value.ID })),
		"range":              output.Object(output.Field("id", output.Int64Range(2, 1), func(value row) int64 { return value.ID })),
		"bad component":      output.Named("no/slash", output.Object(field)),
		"empty component":    output.Named("", output.Object(field)),
		"too long component": output.Named(strings.Repeat("n", 129), output.Object(field)),
		"repeated identity":  output.Named("Same", output.Named("Same", output.Object(field))),
	} {
		t.Run(name, func(t *testing.T) {
			prepared, err := output.New(shape, serializers.Limits{})
			if !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
				t.Fatal("accepted invalid shape", err)
			}
			if body, err := prepared.Encode(t.Context(), row{}); err == nil || body != nil {
				t.Fatal("failed preparation published output")
			}
		})
	}
	for _, limits := range []serializers.Limits{{MaxValues: -1}, {MaxDepth: 65}, {MaxDocumentBytes: 8<<20 + 1}, {MaxArrayItems: 1<<14 + 1}, {MaxObjectMembers: -1}, {MaxStringBytes: -1}, {MaxNumberBytes: -1}} {
		if _, err := output.New(output.Object(field), limits); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
			t.Fatal("invalid limits accepted", err)
		}
	}
	for _, shape := range []output.Shape[[]row]{output.Array(rowShape(), -1, 2), output.Array(rowShape(), 3, 2), output.Array(output.Shape[row]{}, 0, 2)} {
		if _, err := output.New(shape, serializers.Limits{}); err == nil {
			t.Fatal("invalid array accepted")
		}
	}
	if _, err := output.New(output.Nullable(output.Shape[int64]{}), serializers.Limits{}); err == nil {
		t.Fatal("zero nullable accepted")
	}
	if _, err := output.Components(output.Declaration{}); err == nil {
		t.Fatal("zero declaration accepted")
	}
	if reads != 0 {
		t.Fatal("preparation read DTO", reads)
	}
}

func TestOutputSharesIdentityButRejectsIndependentNameReuse(t *testing.T) {
	shared := rowShape()
	left := prepare(t, shared, serializers.Limits{})
	right := prepare(t, output.Named("Rows", output.Array(shared, 0, 2)), serializers.Limits{})
	components, err := output.Components(left.Declaration(), right.Declaration(), left.Declaration())
	if err != nil || len(components) != 2 {
		t.Fatalf("shared identities = %d, %v", len(components), err)
	}
	independent := prepare(t, rowShape(), serializers.Limits{})
	if _, err := output.Components(left.Declaration(), independent.Declaration()); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
		t.Fatal("independent identity overwritten", err)
	}
	deep := output.Int64()
	for index := range 65 {
		deep = output.Named(fmt.Sprintf("Level%d", index), deep)
	}
	if _, err := output.New(deep, serializers.Limits{}); err == nil {
		t.Fatal("deep references accepted")
	}
	properties := make([]output.Property[row], 257)
	for index := range properties {
		properties[index] = output.Field(fmt.Sprintf("p%d", index), output.Named(fmt.Sprintf("N%d", index), output.Int64()), func(value row) int64 { return value.ID })
	}
	if _, err := output.New(output.Object(properties...), serializers.Limits{}); err == nil {
		t.Fatal("excess components accepted")
	}
}

func TestOutputBoundsCancellationAndFailuresNeverPublishPartialBytes(t *testing.T) {
	reads := 0
	shape := output.Object(output.Field("n", output.Int64Range(0, 3), func(n int64) int64 { reads++; return n }))
	for name, test := range map[string]struct {
		shape     output.Shape[int64]
		limits    serializers.Limits
		value     int64
		wantReads int
	}{
		"range":              {shape: shape, value: 4, wantReads: 1},
		"bytes":              {shape: shape, value: 2, limits: serializers.Limits{MaxDocumentBytes: 6}, wantReads: 1},
		"depth before read":  {shape: shape, value: 2, limits: serializers.Limits{MaxDepth: 1}, wantReads: 0},
		"values before read": {shape: shape, value: 2, limits: serializers.Limits{MaxValues: 1}, wantReads: 0},
	} {
		t.Run(name, func(t *testing.T) {
			reads = 0
			prepared := prepare(t, test.shape, test.limits)
			response, err := prepared.JSON(t.Context(), 200, test.value)
			if !errors.Is(err, &api.Error{Code: api.FailureInvalidResponse}) || response.Status() != 0 || reads != test.wantReads {
				t.Fatalf("failure = %v status %d reads %d", err, response.Status(), reads)
			}
		})
	}
	for _, text := range []string{"bad\x00", "bad\xff", strings.Repeat("a", 17)} {
		prepared := prepare(t, output.String(), serializers.Limits{MaxStringBytes: 16})
		if body, err := prepared.Encode(t.Context(), text); err == nil || body != nil {
			t.Fatalf("bad string published %q, %v", body, err)
		}
	}
	for _, values := range [][]row{nil, make([]row, 3)} {
		prepared := prepare(t, output.Array(rowShape(), 1, 2), serializers.Limits{})
		if body, err := prepared.Encode(t.Context(), values); err == nil || body != nil {
			t.Fatal("array bound not enforced", err)
		}
	}
	prepared := prepare(t, shape, serializers.Limits{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	reads = 0
	if body, err := prepared.Encode(ctx, 1); !errors.Is(err, context.Canceled) || body != nil || reads != 0 {
		t.Fatal("cancelled input visited", err, reads)
	}
	if body, err := prepared.Encode(nil, 1); err == nil || body != nil || reads != 0 {
		t.Fatal("nil context visited", err, reads)
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancelling := prepare(t, output.Object(
		output.Field("first", output.Int64(), func(n int64) int64 { reads++; cancel(); return n }),
		output.Field("last", output.Int64(), func(n int64) int64 { reads++; return n }),
	), serializers.Limits{})
	if body, err := cancelling.Encode(ctx, 1); !errors.Is(err, context.Canceled) || body != nil || reads != 1 {
		t.Fatal("cancellation during getter published or read more", err, reads)
	}
	if response, err := prepared.JSON(t.Context(), 999, 1); err == nil || response.Status() != 0 {
		t.Fatal("invalid response status accepted", err)
	}
}

func TestOutputOwnsDeclarationsAndConcurrentResponses(t *testing.T) {
	fields := []output.Property[row]{output.Field("id", output.Int64(), func(value row) int64 { return value.ID })}
	shape := output.Object(fields...)
	fields[0] = output.Field("secret", output.String(), func(value row) string { return value.Private })
	prepared := prepare(t, shape, serializers.Limits{})
	var workers sync.WaitGroup
	for index := range 24 {
		workers.Go(func() {
			for range 8 {
				body, err := prepared.Encode(t.Context(), row{ID: int64(index), Private: "must not appear"})
				if err != nil || !bytes.Equal(body, fmt.Appendf(nil, `{"id":%d}`, index)) {
					t.Errorf("concurrent body = %s, %v", body, err)
					return
				}
				body[0] = '!'
			}
		})
	}
	workers.Wait()
}
