package input_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/uuid"
)

func checkCodec[T any](t *testing.T, field func(string, ...serializers.FieldOption) (serializers.Field, error), codec input.Codec[T], raw string, want T) {
	t.Helper()
	f := require[serializers.Field](t)
	spec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{f(field("value"))}))
	type row struct{ Value input.Presence[T] }
	body := require[input.Body[row]](t)(input.New(spec, api.ParserConfig{}, input.Field("value", codec, func(r *row, v input.Presence[T]) { r.Value = v })))
	result, violations, err := body.Bind(t.Context(), object(t, spec, `{"value":`+raw+`}`), serializers.ModeFull)
	value, present := result.Value.Get()
	if err != nil || !violations.Empty() || !present || !reflect.DeepEqual(value, want) {
		t.Fatal("cleaned scalar", value, want, violations, err)
	}
	result, violations, err = body.Bind(t.Context(), object(t, spec, `{"value":null}`), serializers.ModeFull)
	if err != nil || !reflect.DeepEqual(result, row{}) || !reflect.DeepEqual(codes(violations), []string{"value/null"}) {
		t.Fatal("nonnullable null", result, violations, err)
	}
	nullSpec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{f(field("value", serializers.WithNullable()))}))
	type optional struct{ Value input.Presence[*T] }
	nullBody := require[input.Body[optional]](t)(input.New(nullSpec, api.ParserConfig{}, input.Field("value", input.Nullable(codec), func(r *optional, v input.Presence[*T]) { r.Value = v })))
	for _, rawValue := range []string{raw, "null"} {
		got, diagnostics, err := nullBody.Bind(t.Context(), object(t, nullSpec, `{"value":`+rawValue+`}`), serializers.ModeFull)
		value, present := got.Value.Get()
		if err != nil || !diagnostics.Empty() || !present || (rawValue == "null") != (value == nil) {
			t.Fatal("nullable scalar presence", value, diagnostics, err)
		}
		if value != nil && !reflect.DeepEqual(*value, want) {
			t.Fatal("nullable scalar changed", *value, want)
		}
	}
	absent, diagnostics, err := nullBody.Bind(t.Context(), object(t, nullSpec, `{}`), serializers.ModePartial)
	valuePointer, present := absent.Value.Get()
	if err != nil || !diagnostics.Empty() || present || valuePointer != nil {
		t.Fatal("partial scalar absence", absent, err)
	}
}

func TestEveryCleanedScalarAndCollectionType(t *testing.T) {
	t.Run("string", func(t *testing.T) { checkCodec(t, serializers.StringField, input.String(), `"  한글  "`, "한글") })
	t.Run("email", func(t *testing.T) {
		checkCodec(t, serializers.EmailField, input.String(), `"  User@example.com  "`, "User@example.com")
	})
	t.Run("url", func(t *testing.T) {
		checkCodec(t, serializers.URLField, input.String(), `"  https://example.com/a  "`, "https://example.com/a")
	})
	t.Run("slug", func(t *testing.T) { checkCodec(t, serializers.SlugField, input.String(), `"  a-b_C  "`, "a-b_C") })
	t.Run("boolean", func(t *testing.T) { checkCodec(t, serializers.BooleanField, input.Boolean(), `false`, false) })
	t.Run("integer", func(t *testing.T) {
		checkCodec(t, serializers.IntegerField, input.Integer(), `9223372036854775807`, int64(math.MaxInt64))
	})
	t.Run("float", func(t *testing.T) { checkCodec(t, serializers.FloatField, input.Float(), `"1.25"`, 1.25) })
	t.Run("datetime", func(t *testing.T) {
		checkCodec(t, serializers.DateTimeField, input.DateTime(), `"2026-10-06T09:00:00.123456+09:00"`, time.Date(2026, 10, 6, 0, 0, 0, 123456000, time.UTC))
	})
	t.Run("date", func(t *testing.T) {
		checkCodec(t, serializers.DateField, input.Date(), `"2026-10-06"`, require[calendar.Date](t)(calendar.Parse("2026-10-06")))
	})
	t.Run("time", func(t *testing.T) {
		checkCodec(t, serializers.TimeField, input.Time(), `"12:34:56.123456"`, require[clock.Time](t)(clock.Parse("12:34:56.123456")))
	})
	t.Run("duration", func(t *testing.T) {
		checkCodec(t, serializers.DurationField, input.Duration(), `"1 02:03:04.000005"`, require[duration.Duration](t)(duration.Parse("1 02:03:04.000005")))
	})
	t.Run("decimal", func(t *testing.T) {
		checkCodec(t, func(name string, options ...serializers.FieldOption) (serializers.Field, error) {
			return serializers.DecimalField(name, 5, 2, options...)
		}, input.Decimal(), `"12.50"`, require[decimal.Decimal](t)(decimal.Parse("12.50")))
	})
	t.Run("uuid", func(t *testing.T) {
		checkCodec(t, serializers.UUIDField, input.UUID(), `"12345678123456781234567812345678"`, require[uuid.UUID](t)(uuid.Parse("12345678-1234-5678-1234-567812345678")))
	})
	t.Run("binary", func(t *testing.T) {
		checkCodec(t, serializers.BinaryField, input.Binary(), `"AAH/"`, binaryvalue.Value{Data: string([]byte{0, 1, 255})})
	})
	t.Run("json", func(t *testing.T) {
		checkCodec(t, serializers.JSONField, input.JSON(), `{"a b":[1,null,{"nested/key":true}]}`, require[jsonvalue.Value](t)(jsonvalue.Parse([]byte(`{"a b":[1,null,{"nested/key":true}]}`))))
	})
	t.Run("integer list", func(t *testing.T) {
		checkCodec(t, serializers.IntegerListField, input.Integers(), `[1,-1,1,9223372036854775807]`, []int64{1, -1, 1, math.MaxInt64})
	})
}
