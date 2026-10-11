package serializers_test

import (
	"encoding/json"
	"errors"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/internal/clocktimetest"
	"github.com/progresshans/godj/serializers"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClockTimeSerializersAgainstPinnedDRF(t *testing.T) {
	for index, observation := range clocktimetest.Load(t).Serializer {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			options := []serializers.FieldOption{serializers.WithNullable(), serializers.WithRequired(false)}
			if observation.Serializer == "default" {
				options = append(options, serializers.WithDefault(serializers.Time(clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: 123456})))
			}
			field, err := serializers.TimeField("at", options...)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := serializers.NewSpec([]serializers.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			var members []serializers.Member
			for name, input := range observation.Input {
				var value serializers.Value
				switch input.Type {
				case "time":
					var raw string
					if err := json.Unmarshal(input.Value, &raw); err != nil {
						t.Fatal(err)
					}
					if strings.ContainsAny(raw, "+-") {
						// Python's typed aware time has no counterpart in the
						// timezone-free Go value. Preserve that raw observation
						// and verify the Go boundary separately, without counting
						// this case as field parity or skipping a required case.
						if !observation.Valid || observation.Validated["at"] == nil || *observation.Validated["at"] != raw {
							t.Fatal("typed aware reference changed")
						}
						object, err := serializers.NewObject(serializers.MemberOf(name, serializers.DateTime(time.Date(2026, 9, 20, 12, 34, 56, 123456000, time.UTC))))
						if err != nil {
							t.Fatal(err)
						}
						result, err := spec.Bind(object, serializers.ModeFull)
						if err != nil || result.Valid() || result.Errors().All()[0].Code() != serializers.CodeTime {
							t.Fatal("datetime was coerced to a clock")
						}
						return
					}
					clockValue, err := clock.Parse(raw)
					if err != nil {
						t.Fatal(err)
					}
					value = serializers.Time(clockValue)
				case "date":
					value = serializers.Date(calendar.Date{Year: 2026, Month: 9, Day: 20})
				case "datetime":
					// The closed value API preserves the datetime type; the TimeField must
					// reject it without silently dropping its clock or timezone.
					value = serializers.DateTime(time.Date(2026, 9, 20, 0, 30, 0, 0, time.UTC))
				default:
					wire := []byte(`{"at":` + string(input.Value) + `}`)
					object, decodeErr := serializers.DecodeObject(wire, serializers.Limits{})
					var raw string
					_ = json.Unmarshal(input.Value, &raw)
					if input.Type == "str" && strings.ContainsRune(raw, 0) {
						// Global JSON rules reject NUL before TimeField validation.
						// Preserve this boundary; do not claim its code is DRF's field error.
						if !errors.Is(decodeErr, &serializers.Error{Code: serializers.CodeInvalidDocument}) {
							t.Fatal("NUL document boundary changed")
						}
						return
					}
					if decodeErr != nil {
						t.Fatal(decodeErr)
					}
					value, _ = object.Get("at")
				}
				members = append(members, serializers.MemberOf(name, value))
			}
			object, err := serializers.NewObject(members...)
			if err != nil {
				t.Fatal(err)
			}
			mode := serializers.ModeFull
			if observation.Partial {
				mode = serializers.ModePartial
			}
			result, err := spec.Bind(object, mode)
			if err != nil {
				t.Fatal(err)
			}
			codes := map[string][]string{}
			for _, v := range result.Errors().All() {
				codes[string(v.Field())] = append(codes[string(v.Field())], string(v.Code()))
			}
			if result.Valid() != observation.Valid || !reflect.DeepEqual(codes, observation.Errors) {
				t.Fatalf("input %v: errors %v want %v", observation.Input, codes, observation.Errors)
			}
			got, present := result.Values().Get("at")
			want, exists := observation.Validated["at"]
			if present != exists {
				t.Fatal("time default/presence differs")
			}
			if exists {
				if want == nil {
					if !got.IsNull() {
						t.Fatal("explicit null lost")
					}
				} else {
					time, ok := got.AsTime()
					if !ok || time.String() != *want {
						t.Fatal("validated time differs")
					}
					encoded, err := serializers.Encode(got, serializers.Limits{})
					expected, _ := json.Marshal(*want)
					if err != nil || string(encoded) != string(expected) {
						t.Fatalf("time wire: %s %v", encoded, err)
					}
				}
			}
		})
	}
	for _, time := range []clock.Time{{Minute: -1}, {Second: 60}, {Hour: 24}} {
		if _, err := serializers.Encode(serializers.Time(time), serializers.Limits{}); err == nil {
			t.Fatal("invalid time encoded")
		}
		if _, err := serializers.TimeField("at", serializers.WithDefault(serializers.Time(time))); err == nil {
			t.Fatal("invalid time default accepted")
		}
	}
}
