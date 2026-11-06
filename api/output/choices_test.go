package output_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/serializers"
)

func TestStringChoiceOutputOwnsItsDomainAndRejectsPartialResponses(t *testing.T) {
	choices := []string{"day", "week", ""}
	shape := output.StringChoices(choices...)
	choices[0] = "changed"
	response := prepare(t, shape, serializers.Limits{})
	schema, err := serializers.Encode(response.Schema().Value(), serializers.Limits{})
	if err != nil || string(schema) != `{"type":"string","enum":["day","week",""]}` {
		t.Fatal("output choices lost order, empty string or ownership", string(schema), err)
	}
	for _, value := range []string{"day", "week", ""} {
		body, err := response.Encode(t.Context(), value)
		if err != nil || string(body) != `"`+value+`"` {
			t.Fatal("declared choice did not encode", string(body), err)
		}
	}
	array := prepare(t, output.Array(output.Nullable(shape), 0, 3), serializers.Limits{})
	for _, invalid := range []string{"changed", "Day", "week ", "other", "\xff"} {
		values := []*string{new("day"), nil, &invalid}
		body, err := array.Encode(t.Context(), values)
		if err == nil || body != nil || strings.Contains(err.Error(), invalid) {
			t.Fatal("invalid choice published a prefix or exposed its raw value", body, err)
		}
		prepared, err := array.Prepare(t.Context(), http.StatusOK, values)
		if err == nil {
			t.Fatal("invalid choice prepared a response")
		}
		if published, err := array.Response(prepared); err == nil || published.Status() != 0 {
			t.Fatal("failed choice preparation was published")
		}
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			body, err := array.Encode(t.Context(), []*string{new("day"), nil, new("")})
			if err != nil || string(body) != `["day",null,""]` {
				t.Error("shared output choices changed", string(body), err)
			}
		})
	}
	group.Wait()
	for _, invalid := range [][]string{nil, {"day", "day"}, {"\xff"}} {
		if _, err := output.New(output.Nullable(output.StringChoices(invalid...)), serializers.Limits{}); err == nil {
			t.Fatal("invalid choice configuration was admitted")
		}
	}
}
