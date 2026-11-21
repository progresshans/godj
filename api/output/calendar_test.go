package output_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/serializers"
)

func TestCalendarOutputPreservesExactDomainsAndNullablePresence(t *testing.T) {
	type row struct {
		Day   *calendar.Date
		Clock *clock.Time
	}
	shape := output.Object(
		output.Field("day", output.Nullable(output.Date()), func(value row) *calendar.Date { return value.Day }),
		output.Field("clock", output.Nullable(output.Time()), func(value row) *clock.Time { return value.Clock }),
	)
	prepared := prepare(t, shape, serializers.Limits{})
	first := calendar.Date{Year: 1, Month: 1, Day: 1}
	last := calendar.Date{Year: 9999, Month: 12, Day: 31}
	midnight, final := clock.Time{}, clock.Time{Hour: 23, Minute: 59, Second: 59, Microsecond: 999999}
	for _, test := range []struct {
		value row
		body  string
	}{
		{row{}, `{"day":null,"clock":null}`},
		{row{&first, &midnight}, `{"day":"0001-01-01","clock":"00:00:00"}`},
		{row{&last, &final}, `{"day":"9999-12-31","clock":"23:59:59.999999"}`},
	} {
		response, err := prepared.JSON(t.Context(), http.StatusOK, test.value)
		if err != nil {
			t.Fatal(err)
		}
		body, err := response.Body()
		if err != nil || string(body) != test.body {
			t.Fatal("computed calendar output changed domain or NULL", string(body), err)
		}
	}
	array := prepare(t, output.Array(shape, 0, 3), serializers.Limits{})
	for _, invalid := range []row{
		{new(calendar.Date{}), &midnight},
		{new(calendar.Date{Year: 1900, Month: 2, Day: 29}), &midnight},
		{&first, new(clock.Time{Hour: 24})},
		{&first, new(clock.Time{Microsecond: 1000000})},
	} {
		if _, err := array.JSON(t.Context(), http.StatusOK, []row{{&first, &midnight}, invalid}); err == nil {
			t.Fatal("invalid later calendar value published a prefix")
		}
	}
	dateField, err := serializers.DateField("day", serializers.WithNullable())
	if err != nil {
		t.Fatal(err)
	}
	timeField, err := serializers.TimeField("clock", serializers.WithNullable())
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{dateField, timeField})
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
		t.Fatal("computed calendar and model schemas differ", string(actual), string(expected))
	}
}
