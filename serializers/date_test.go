package serializers_test

import (
	"encoding/json"
	"errors"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/internal/calendardatetest"
	"github.com/progresshans/godj/serializers"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCalendarDateSerializersAgainstPinnedDRF(t *testing.T) {
	for index, observation := range calendardatetest.Load(t).Serializer {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			options := []serializers.FieldOption{serializers.WithNullable(), serializers.WithRequired(false)}
			if observation.Serializer == "default" {
				options = append(options, serializers.WithDefault(serializers.Date(calendar.Date{Year: 2026, Month: 9, Day: 20})))
			}
			field, err := serializers.DateField("day", options...)
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
				case "date":
					var raw string
					if err := json.Unmarshal(input.Value, &raw); err != nil {
						t.Fatal(err)
					}
					date, err := calendar.Parse(raw)
					if err != nil {
						t.Fatal(err)
					}
					value = serializers.Date(date)
				case "datetime":
					// The closed value API preserves the datetime type; the DateField must
					// reject it without silently dropping its clock or timezone.
					value = serializers.DateTime(time.Date(2026, 9, 20, 0, 30, 0, 0, time.UTC))
				default:
					wire := []byte(`{"day":` + string(input.Value) + `}`)
					object, decodeErr := serializers.DecodeObject(wire, serializers.Limits{})
					var raw string
					_ = json.Unmarshal(input.Value, &raw)
					if input.Type == "str" && strings.ContainsRune(raw, 0) {
						// Global JSON rules reject NUL before DateField validation.
						// Preserve this boundary; do not claim its code is DRF's field error.
						if !errors.Is(decodeErr, &serializers.Error{Code: serializers.CodeInvalidDocument}) || observation.Valid || !reflect.DeepEqual(observation.Errors["day"], []string{"invalid"}) {
							t.Fatal("NUL document boundary changed")
						}
						return
					}
					if decodeErr != nil {
						t.Fatal(decodeErr)
					}
					value, _ = object.Get("day")
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
			got, present := result.Values().Get("day")
			want, exists := observation.Validated["day"]
			if present != exists {
				t.Fatal("date default/presence differs")
			}
			if exists {
				if want == nil {
					if !got.IsNull() {
						t.Fatal("explicit null lost")
					}
				} else {
					date, ok := got.AsDate()
					if !ok || date.String() != *want {
						t.Fatal("validated date differs")
					}
					encoded, err := serializers.Encode(got, serializers.Limits{})
					expected, _ := json.Marshal(*want)
					if err != nil || string(encoded) != string(expected) {
						t.Fatalf("date wire: %s %v", encoded, err)
					}
				}
			}
		})
	}
	for _, date := range []calendar.Date{{}, {Year: 1900, Month: 2, Day: 29}, {Year: 10000, Month: 1, Day: 1}} {
		if _, err := serializers.Encode(serializers.Date(date), serializers.Limits{}); err == nil {
			t.Fatal("invalid date encoded")
		}
		if _, err := serializers.DateField("day", serializers.WithDefault(serializers.Date(date))); err == nil {
			t.Fatal("invalid date default accepted")
		}
	}
}
