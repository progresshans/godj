package consumer

import (
	"math"
	"reflect"
	"testing"
	"time"

	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/uuid"
)

// Other deliberately has no Decimal/Duration/UUID/JSON source fields. Native
// result adaptation must follow the selected value domain, not root metadata.
func checkComputedCodecs(t *testing.T, source orm.QuerySet[records.Other]) {
	t.Helper()
	checkComputedCodec(t, source, "integer", int64(math.MaxInt64), query.Integer(math.MaxInt64))
	checkComputedCodec(t, source, "float", float64(1.25), query.Float(1.25))
	checkComputedCodec(t, source, "string", "' OR 1=1 -- 雪", query.String("' OR 1=1 -- 雪"))
	checkComputedCodec(t, source, "boolean", true, query.Boolean(true))
	number, err := decimal.Parse("123.45")
	check(t, err)
	checkComputedCodec(t, source, "decimal", number, query.Decimal(number))
	elapsed := duration.FromMicroseconds(-3)
	checkComputedCodec(t, source, "duration", elapsed, query.Duration(elapsed))
	moment := time.Date(9999, time.December, 31, 23, 59, 59, 999999000, time.UTC)
	checkComputedCodec(t, source, "datetime", moment, query.DateTime(moment))
	day, err := calendar.New(1, time.January, 1)
	check(t, err)
	checkComputedCodec(t, source, "date", day, query.Date(day))
	instant, err := clock.New(23, 59, 59, 999999)
	check(t, err)
	checkComputedCodec(t, source, "time", instant, query.Time(instant))
	identifier, err := uuid.Parse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	check(t, err)
	checkComputedCodec(t, source, "uuid", identifier, query.UUID(identifier))
	for _, test := range []struct {
		name  string
		bytes []byte
	}{{"binary", []byte{0, 255, 128, 65}}, {"binary_empty", []byte{}}} {
		value, err := binaryvalue.FromBytes(test.bytes)
		check(t, err)
		checkComputedCodec(t, source, test.name, value, query.Binary(value))
	}
	for _, test := range []struct{ name, json string }{{"json", `{"large":9007199254740993,"text":"한글"}`}, {"json_null", `null`}} {
		value, err := jsonvalue.Parse([]byte(test.json))
		check(t, err)
		checkComputedCodec(t, source, test.name, value, query.JSON(value))
	}
}

func checkComputedCodec[V any](t *testing.T, source orm.QuerySet[records.Other], name string, input V, expected query.Value) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		literal, null := orm.Value[records.Other](input), orm.NullValue[records.Other, V]()
		selected := orm.Case(null, orm.When(records.OtherFields.Enabled.Exact(true), literal))
		rows, err := orm.SelectInto(t.Context(), source, orm.Project3(literal, selected, null,
			func(a, b, c orm.Optional[V]) [3]orm.Optional[V] { return [3]orm.Optional[V]{a, b, c} }))
		check(t, err)
		if len(rows) != 1 || rows[0][2].Valid() {
			t.Fatal("typed literal/NULL result shape changed", rows)
		}
		for _, value := range rows[0][:2] {
			actual, present := value.Get()
			if !present || !reflect.DeepEqual(actual, input) {
				t.Fatal("typed literal or CASE changed its scalar value", actual, input)
			}
		}
		kind := query.FieldKind(expected.Kind())
		projection, err := orm.ProjectDynamic(source, orm.DynamicValue(input),
			orm.DynamicCase(orm.DynamicNull(kind), orm.DynamicWhen(orm.DynamicF("enabled").Exact(true), input)), orm.DynamicNull(kind))
		check(t, err)
		dynamic, err := orm.SelectInto(t.Context(), source, projection)
		check(t, err)
		if len(dynamic) != 1 || len(dynamic[0]) != 3 || dynamic[0][0] != expected || dynamic[0][1] != expected || !dynamic[0][2].IsNull() {
			t.Fatal("dynamic literal or CASE changed its scalar domain", dynamic, expected)
		}
	})
}
