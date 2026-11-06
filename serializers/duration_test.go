package serializers_test

import (
	"encoding/json"
	"errors"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/durationtest"
	"github.com/progresshans/godj/serializers"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDurationSerializersAgainstPinnedDRF(t *testing.T) {
	for index, observation := range durationtest.Load(t).Serializer {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			options := []serializers.FieldOption{serializers.WithNullable(), serializers.WithRequired(false)}
			if observation.Serializer == "default" {
				options = append(options, serializers.WithDefault(serializers.Duration(duration.Duration{Days: 1, Microseconds: 7384123456})))
			}
			field, err := serializers.DurationField("elapsed", options...)
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
				case "timedelta":
					var raw string
					if err := json.Unmarshal(input.Value, &raw); err != nil {
						t.Fatal(err)
					}
					elapsed, err := duration.Parse(raw)
					if err != nil {
						t.Fatal(err)
					}
					value = serializers.Duration(elapsed)
				case "time":
					value = serializers.Time(clock.Time{Hour: 12, Minute: 34, Second: 56})
				case "date":
					value = serializers.Date(calendar.Date{Year: 2026, Month: 9, Day: 20})
				case "datetime":
					// The closed value API preserves the datetime type; the DurationField must
					// reject it without silently dropping its clock or timezone.
					value = serializers.DateTime(time.Date(2026, 9, 20, 0, 30, 0, 0, time.UTC))
				default:
					wire := []byte(`{"elapsed":` + string(input.Value) + `}`)
					object, decodeErr := serializers.DecodeObject(wire, serializers.Limits{})
					var raw string
					_ = json.Unmarshal(input.Value, &raw)
					if input.Type == "str" && strings.ContainsRune(raw, 0) {
						// Global JSON rules reject NUL before DurationField validation.
						// Preserve this boundary; do not claim its code is DRF's field error.
						if !errors.Is(decodeErr, &serializers.Error{Code: serializers.CodeInvalidDocument}) {
							t.Fatal("NUL document boundary changed")
						}
						return
					}
					if decodeErr != nil {
						t.Fatal(decodeErr)
					}
					value, _ = object.Get("elapsed")
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
			got, present := result.Values().Get("elapsed")
			want, exists := observation.Validated["elapsed"]
			if present != exists {
				t.Fatal("time default/presence differs")
			}
			if exists {
				if want == nil {
					if !got.IsNull() {
						t.Fatal("explicit null lost")
					}
				} else {
					time, ok := got.AsDuration()
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
	for _, time := range []duration.Duration{{Microseconds: -1}, {Microseconds: duration.MicrosecondsPerDay}, {Days: 1000000000}} {
		if _, err := serializers.Encode(serializers.Duration(time), serializers.Limits{}); err == nil {
			t.Fatal("invalid time encoded")
		}
		if _, err := serializers.DurationField("elapsed", serializers.WithDefault(serializers.Duration(time))); err == nil {
			t.Fatal("invalid time default accepted")
		}
	}
}

func TestDurationJSONNumbersAgainstPinnedDRF(t *testing.T) {
	field, err := serializers.DurationField("elapsed", serializers.WithNullable(), serializers.WithRequired(false))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range durationtest.Load(t).JSONNumbers {
		object, err := serializers.DecodeObject([]byte(`{"elapsed":`+observation.Raw+`}`), serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		result, err := spec.Bind(object, serializers.ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		codes := map[string][]string{}
		for _, v := range result.Errors().All() {
			codes[string(v.Field())] = append(codes[string(v.Field())], string(v.Code()))
		}
		if result.Valid() != observation.Valid || !reflect.DeepEqual(codes, observation.Errors) {
			t.Fatalf("JSON number %s: %v want %v", observation.Raw, codes, observation.Errors)
		}
		got, present := result.Values().Get("elapsed")
		want, exists := observation.Validated["elapsed"]
		if present != exists {
			t.Fatal("numeric duration lost presence")
		}
		if exists {
			value, ok := got.AsDuration()
			if !ok || want == nil || value.String() != *want {
				t.Fatalf("numeric duration %s: %v want %v", observation.Raw, value, want)
			}
		}
	}
}
