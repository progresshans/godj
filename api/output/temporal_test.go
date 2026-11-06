package output_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/serializers"
)

func TestDateTimeOutputRetainsMicrosecondsNullAndValidYearOne(t *testing.T) {
	type row struct{ At *time.Time }
	shape := output.Object(output.Field("at", output.Nullable(output.DateTime()), func(row row) *time.Time { return row.At }))
	prepared := prepare(t, shape, serializers.Limits{})
	yearOne := time.Time{}
	local := time.Date(2026, 10, 15, 9, 0, 0, 123456789, time.FixedZone("input", 9*3600))
	for _, test := range []struct {
		value *time.Time
		body  string
	}{
		{nil, `{"at":null}`},
		{&yearOne, `{"at":"0001-01-01T00:00:00.000000Z"}`},
		{&local, `{"at":"2026-10-15T00:00:00.123456Z"}`},
	} {
		response, err := prepared.JSON(t.Context(), http.StatusOK, row{test.value})
		if err != nil {
			t.Fatal(err)
		}
		body, err := response.Body()
		if err != nil || string(body) != test.body {
			t.Fatal("DateTime output changed instant/null or precision", string(body), err)
		}
	}
	if local.Nanosecond() != 123456789 || local.Hour() != 9 {
		t.Fatal("output normalized the caller's value in place")
	}
	array := prepare(t, output.Array(shape, 0, 3), serializers.Limits{})
	for _, invalid := range []time.Time{time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := array.JSON(t.Context(), http.StatusOK, []row{{&yearOne}, {&invalid}}); err == nil {
			t.Fatal("invalid later DateTime published a result prefix")
		}
	}
	field, err := serializers.DateTimeField("at", serializers.WithNullable())
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	model, err := openapi.ModelResponseSchema(spec)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := serializers.Encode(prepared.Schema().Value(), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := serializers.Encode(model.Value(), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var left, right any
	if err := json.Unmarshal(actual, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(expected, &right); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatal("computed output and model DateTime schemas differ", string(actual), string(expected))
	}
}
