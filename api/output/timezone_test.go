package output_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/timezone"
)

func TestTimeZoneOutputPreservesAliasesAndRejectsInvalidValuesBeforePublication(t *testing.T) {
	names, err := timezone.Names()
	if err != nil {
		t.Fatal(err)
	}
	values := make([]timezone.Zone, len(names))
	for i, name := range names {
		values[i], err = timezone.Parse(name)
		if err != nil {
			t.Fatal(err)
		}
	}
	response := prepare(t, output.Array(output.TimeZone(), 0, len(names)), serializers.Limits{})
	body, err := response.Encode(t.Context(), values)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []string
	if err := json.Unmarshal(body, &decoded); err != nil || !slices.Equal(names, decoded) {
		t.Fatal("output changed exact pinned names or aliases", err)
	}
	values[len(values)-1] = timezone.Zone{}
	if body, err := response.Encode(t.Context(), values); err == nil || body != nil {
		t.Fatal("late invalid timezone published a prefix", err)
	}
	if _, err := response.Prepare(t.Context(), 200, values); err == nil {
		t.Fatal("late invalid timezone prepared a response")
	}
	nullable := prepare(t, output.Nullable(output.TimeZone()), serializers.Limits{})
	if body, err := nullable.Encode(t.Context(), nil); err != nil || string(body) != "null" {
		t.Fatal("timezone null is not preserved", string(body), err)
	}
	if body, err := nullable.Encode(t.Context(), new(timezone.Zone{})); err == nil || body != nil {
		t.Fatal("present zero timezone became null or UTC")
	}
	single := prepare(t, output.TimeZone(), serializers.Limits{})
	query, err := openapi.QueryTimeZone(nil)
	if err != nil {
		t.Fatal(err)
	}
	inputSchema, err := serializers.Encode(query.Value(), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	outputSchema, err := serializers.Encode(single.Schema().Value(), serializers.Limits{})
	if err != nil || string(inputSchema) != string(outputSchema) {
		t.Fatal("timezone parameter and output schemas differ", err)
	}
}
