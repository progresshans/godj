package serializers_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/serializers"
)

func TestDeclaredObjectListUsesOneJSONBudgetAndExactFieldPolicy(t *testing.T) {
	spec := jsonSpec(t, "required")
	wire := []byte(`[{"payload":{"":340282366920938463463374607431768211455}},{"payload":false}]`)
	rows, err := spec.DecodeList(wire, serializers.Limits{MaxValues: 6, MaxDepth: 4})
	if err != nil || len(rows) != 2 {
		t.Fatal("list decode", err)
	}
	wire[0] = '{'
	first, _ := rows[0].Get("payload")
	document, valid := first.AsJSON()
	if !valid || document.Text != `{"":340282366920938463463374607431768211455}` {
		t.Fatal("exact JSON or ownership lost")
	}
	second, _ := rows[1].Get("payload")
	boolean, valid := second.AsJSON()
	if !valid || boolean.Text != "false" {
		t.Fatal("second row was not decoded by its declared spec")
	}
	for _, row := range rows {
		bound, err := spec.Bind(row, serializers.ModeFull)
		if err != nil || !bound.Valid() {
			t.Fatal("decoded row did not bind", err)
		}
	}
	rows, err = spec.DecodeList([]byte(`[]`), serializers.Limits{})
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty list representation", err)
	}
	rows, err = spec.DecodeList([]byte(`[{"unknown":1}]`), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(rows[0], serializers.ModeFull)
	if err != nil || bound.Valid() {
		t.Fatal("decoding bypassed binding's unknown/required fields", err)
	}
	if values, err := (serializers.Spec{}).DecodeList([]byte(`[]`), serializers.Limits{}); values != nil || !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
		t.Fatal("zero spec", err)
	}
}

func TestDeclaredObjectListRejectsAmbiguityAndAggregateOverflowWithoutPartialRows(t *testing.T) {
	spec := jsonSpec(t, "required")
	for _, test := range []struct {
		name, wire string
		limits     serializers.Limits
		code       serializers.ErrorCode
	}{
		{"object", `{}`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"null", `null`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"scalar item", `[{"payload":1},2]`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"array item", `[{"payload":1},[]]`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"duplicate row field", `[{"payload":1,"payload":2}]`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"duplicate JSON field", `[{"payload":{"":1,"":2}}]`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"empty row field", `[{"":1}]`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"undeclared JSON", `[{"other":{"":1}}]`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"nested declaration", `[{"other":{"payload":{"":1}}}]`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"trailing", `[{"payload":1}][]`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"NUL", `[{"payload":"\u0000"}]`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"surrogate", `[{"payload":"\ud800"}]`, serializers.Limits{}, serializers.CodeInvalidDocument},
		{"UTF8", "[{\"payload\":\"\xff\"}]", serializers.Limits{}, serializers.CodeInvalidDocument},
		{"combined values", `[{"payload":{"":1}},{"payload":false}]`, serializers.Limits{MaxValues: 5}, serializers.CodeResourceLimit},
		{"array envelope depth", `[{"payload":{"":1}}]`, serializers.Limits{MaxDepth: 3}, serializers.CodeResourceLimit},
		{"combined bytes", `[{"payload":1},{"payload":2}]`, serializers.Limits{MaxDocumentBytes: 25}, serializers.CodeResourceLimit},
		{"array size", `[{"payload":1},{"payload":2}]`, serializers.Limits{MaxArrayItems: 1}, serializers.CodeResourceLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := spec.DecodeList([]byte(test.wire), test.limits)
			if rows != nil || !errors.Is(err, &serializers.Error{Code: test.code}) {
				t.Fatalf("partial rows or wrong failure: %v", err)
			}
		})
	}
}
